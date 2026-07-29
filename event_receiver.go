//go:build darwin || linux || windows

package pcsc

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"time"
)

const (
	pnpNotificationReader = `\\?PnP?\Notification`
	eventPollInterval     = time.Second
)

var getStatusChange = getStatusChangeNative

// readerState is the platform-independent representation passed to the
// SCardGetStatusChange ABI adapters.
type readerState struct {
	name         string
	currentState ReaderState
	eventState   ReaderState
	atr          []byte
}

type eventReceiver struct {
	events        *deviceEventQueue
	nativeContext scardContext
	stopped       chan struct{}

	mu       sync.Mutex
	closed   bool
	runErr   error
	closeErr error

	closeOnce sync.Once
}

func (receiver *eventReceiver) Listen() <-chan DeviceEvent {
	return receiver.events.Listen()
}

func (receiver *eventReceiver) Close() error {
	receiver.closeOnce.Do(func() {
		receiver.mu.Lock()
		if !receiver.closed {
			receiver.closed = true
			cancelErr := cancelNativeContext(receiver.nativeContext)
			if errors.Is(cancelErr, ErrCanceled) {
				cancelErr = nil
			}
			receiver.closeErr = errors.Join(receiver.closeErr, cancelErr)
		}
		receiver.mu.Unlock()

		receiver.events.Close()
		<-receiver.stopped
	})

	receiver.mu.Lock()
	defer receiver.mu.Unlock()

	return errors.Join(receiver.runErr, receiver.closeErr)
}

func (receiver *eventReceiver) recordRunError(err error) bool {
	receiver.mu.Lock()
	defer receiver.mu.Unlock()

	report := !receiver.closed || !errors.Is(err, ErrCanceled)
	if report {
		receiver.runErr = errors.Join(receiver.runErr, err)
	}
	receiver.closed = true

	return report
}

func (receiver *eventReceiver) run(current map[string]*ReaderInfo, pnpState ReaderState) {
	defer close(receiver.stopped)
	defer receiver.events.Close()
	defer func() {
		receiver.mu.Lock()
		receiver.closeErr = errors.Join(
			receiver.closeErr,
			releaseNativeContext(receiver.nativeContext),
		)
		receiver.mu.Unlock()
	}()

	for {
		states := readerStates(slices.Sorted(maps.Keys(current)), current, pnpState)
		err := waitForStatusChange(receiver.nativeContext, eventPollInterval, states)
		if err != nil && !errors.Is(err, ErrTimeout) {
			if receiver.recordRunError(err) {
				receiver.events.SendTerminal(DeviceEvent{Err: err})
			}
			return
		}

		receiver.mu.Lock()
		closed := receiver.closed
		receiver.mu.Unlock()
		if closed {
			return
		}

		base, nextPnPState := snapshotFromReaderStates(states)
		next, nextPnPState, err := readerSnapshot(receiver.nativeContext, base, nextPnPState)
		if err != nil {
			if receiver.recordRunError(err) {
				receiver.events.SendTerminal(DeviceEvent{Err: err})
			}
			return
		}

		receiver.mu.Lock()
		closed = receiver.closed
		receiver.mu.Unlock()
		if closed {
			return
		}

		for _, event := range reconcileDeviceEvents(current, next) {
			receiver.events.Send(event)
		}

		current = next
		pnpState = nextPnPState
	}
}

// Events publishes the current reader and card snapshot followed by live
// reader and card-presence changes. Each call creates an independent receiver.
func Events() (EventReceiver, error) {
	nativeContext, err := establishNativeContext()
	if err != nil {
		return nil, err
	}

	current, pnpState, err := readerSnapshot(nativeContext, nil, ReaderStateUnaware)
	if err != nil {
		return nil, errors.Join(err, releaseNativeContext(nativeContext))
	}

	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		nativeContext: nativeContext,
		stopped:       make(chan struct{}),
	}
	for _, event := range reconcileDeviceEvents(nil, current) {
		receiver.events.Send(event)
	}

	go receiver.run(current, pnpState)

	return receiver, nil
}

func readerSnapshot(
	nativeContext scardContext,
	base map[string]*ReaderInfo,
	pnpState ReaderState,
) (map[string]*ReaderInfo, ReaderState, error) {
	names, err := listReadersNative(nativeContext)
	if err != nil {
		return nil, ReaderStateUnaware, err
	}

	states := readerStates(names, base, pnpState)
	err = waitForStatusChange(nativeContext, 0, states)
	if err != nil && !errors.Is(err, ErrTimeout) {
		return nil, ReaderStateUnaware, err
	}

	current, nextPnPState := snapshotFromReaderStates(states)

	return current, nextPnPState, nil
}

// Some PC/SC implementations do not recognize the PnP notification
// pseudo-reader. Ignoring it preserves card events while the periodic reader
// snapshot provides a portable hot-plug fallback.
func waitForStatusChange(
	nativeContext scardContext,
	timeout time.Duration,
	states []readerState,
) error {
	err := getStatusChange(nativeContext, timeout, states)
	if !errors.Is(err, ErrUnknownReader) {
		return err
	}

	for index := range states {
		if states[index].name != pnpNotificationReader || states[index].currentState == ReaderStateIgnore {
			continue
		}

		states[index].currentState = ReaderStateIgnore
		states[index].eventState = ReaderStateIgnore

		return getStatusChange(nativeContext, timeout, states)
	}

	return err
}

func readerStates(
	names []string,
	current map[string]*ReaderInfo,
	pnpState ReaderState,
) []readerState {
	states := make([]readerState, 0, len(names)+1)
	for _, name := range names {
		state := readerState{name: name}
		if info := current[name]; info != nil {
			state.currentState = info.State
			state.eventState = info.State
			state.atr = info.ATR
		}

		states = append(states, state)
	}

	states = append(states, readerState{
		name:         pnpNotificationReader,
		currentState: pnpState,
		eventState:   pnpState,
	})

	return states
}

func snapshotFromReaderStates(states []readerState) (map[string]*ReaderInfo, ReaderState) {
	readers := make(map[string]*ReaderInfo, len(states))
	var pnpState ReaderState

	for _, state := range states {
		normalized := state.eventState &^ ReaderStateChanged
		if state.name == pnpNotificationReader {
			pnpState = normalized
			continue
		}

		readers[state.name] = &ReaderInfo{
			Name:  state.name,
			State: normalized,
			ATR:   state.atr,
		}
	}

	return readers, pnpState
}
