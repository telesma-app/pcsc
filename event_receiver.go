//go:build darwin || linux || windows

package pcsc

import (
	"errors"
	"maps"
	"runtime"
	"slices"
	"time"
)

const statusChangeInfinite = -time.Nanosecond

var (
	getStatusChange   = getStatusChangeNative
	listReaders       = listReadersNative
	newReaderTopology = newReaderTopologyNative
)

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
	topology      readerTopology
	nativeContext scardContext
	stopped       chan struct{}
	snapshot      Snapshot
	err           error
}

type readerTopology interface {
	Events() <-chan error
	Close() error
}

type statusChangeResult struct {
	states []readerState
	err    error
}

func (r *eventReceiver) Listen() <-chan DeviceEvent {
	return r.events.Listen()
}

func (r *eventReceiver) Snapshot() Snapshot {
	return r.snapshot
}

func (r *eventReceiver) Close() error {
	r.events.Close()
	<-r.stopped

	return r.err
}

func (r *eventReceiver) isDone() bool {
	select {
	case <-r.events.done:
		return true
	default:
		return false
	}
}

func (r *eventReceiver) run(current map[string]*ReaderInfo) {
	defer close(r.stopped)
	defer r.events.Close()
	defer func() {
		r.err = errors.Join(
			r.err,
			r.topology.Close(),
			releaseNativeContext(r.nativeContext),
		)
	}()

	for {
		var (
			topologyErr  error
			statusResult statusChangeResult
			refresh      bool
		)

		if len(current) == 0 {
			select {
			case topologyErr = <-r.topology.Events():
				refresh = true
			case <-r.events.done:
				return
			}
		} else {
			statusChanges := r.startStatusChange(current)
			select {
			case statusResult = <-statusChanges:
			case topologyErr = <-r.topology.Events():
				statusResult, _ = r.cancelStatusChange(statusChanges)
				refresh = true
			case <-r.events.done:
				_, cancelErr := r.cancelStatusChange(statusChanges)
				r.err = cancelErr

				return
			}
		}

		if topologyErr != nil {
			r.err = topologyErr
			return
		}

		if statusResult.err != nil {
			switch {
			case errors.Is(statusResult.err, ErrCanceled):
				if r.isDone() {
					return
				}
			case errors.Is(statusResult.err, ErrUnknownReader):
				refresh = true
			default:
				r.err = statusResult.err
				return
			}
		} else if statusResult.states != nil {
			observed := snapshotFromReaderStates(statusResult.states)
			for _, event := range reconcileDeviceEvents(current, observed) {
				r.events.Send(event)
			}

			current = observed
			refresh = refresh || readerTopologyInvalidated(statusResult.states)
		}

		if !refresh {
			continue
		}

		next, err := readerSnapshot(r.nativeContext, current, r.events.done)
		if err != nil {
			if errors.Is(err, ErrCanceled) && r.isDone() {
				return
			}

			r.err = err

			return
		}

		if r.isDone() {
			return
		}

		for _, event := range reconcileDeviceEvents(current, next) {
			r.events.Send(event)
		}

		current = next
	}
}

func (r *eventReceiver) startStatusChange(current map[string]*ReaderInfo) <-chan statusChangeResult {
	states := readerStates(slices.Sorted(maps.Keys(current)), current)

	result := make(chan statusChangeResult, 1)
	go func() {
		result <- statusChangeResult{
			states: states,
			err:    getStatusChange(r.nativeContext, statusChangeInfinite, states),
		}
	}()

	return result
}

func (r *eventReceiver) cancelStatusChange(
	result <-chan statusChangeResult,
) (statusChangeResult, error) {
	var cancelErr error

	for {
		err := cancelNativeContext(r.nativeContext)
		if err != nil && !errors.Is(err, ErrCanceled) && cancelErr == nil {
			cancelErr = err
		}

		select {
		case statusResult := <-result:
			if statusResult.err != nil && !errors.Is(statusResult.err, ErrCanceled) {
				statusResult.err = errors.Join(statusResult.err, cancelErr)
			}

			return statusResult, cancelErr
		default:
		}

		runtime.Gosched()
	}
}

// Watch captures the current reader and card snapshot and then publishes later
// reader and card-presence changes. Each call creates an independent watcher.
func Watch() (Watcher, error) {
	nativeContext, err := establishNativeContext()
	if err != nil {
		return nil, err
	}

	topology, err := newReaderTopology()
	if err != nil {
		return nil, errors.Join(err, releaseNativeContext(nativeContext))
	}

	current, err := readerSnapshot(nativeContext, nil, nil)
	if err != nil {
		return nil, errors.Join(err, topology.Close(), releaseNativeContext(nativeContext))
	}

	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		topology:      topology,
		nativeContext: nativeContext,
		stopped:       make(chan struct{}),
		snapshot:      snapshotFromReaders(current),
	}

	go receiver.run(current)

	return receiver, nil
}

func snapshotFromReaders(readers map[string]*ReaderInfo) Snapshot {
	snapshot := Snapshot{Readers: make([]*ReaderInfo, 0, len(readers))}
	for _, name := range slices.Sorted(maps.Keys(readers)) {
		snapshot.Readers = append(snapshot.Readers, readers[name])
	}

	return snapshot
}

func readerSnapshot(
	nativeContext scardContext,
	base map[string]*ReaderInfo,
	done <-chan struct{},
) (map[string]*ReaderInfo, error) {
	for {
		select {
		case <-done:
			return nil, ErrCanceled
		default:
		}

		names, err := listReaders(nativeContext)
		if err != nil {
			return nil, err
		}

		states := readerStates(names, base)

		err = getStatusChange(nativeContext, 0, states)
		if errors.Is(err, ErrUnknownReader) {
			// A reader disappeared after ListReaders. Enumerate the topology
			// again instead of terminating the watcher.
			continue
		}

		if err != nil && !errors.Is(err, ErrTimeout) {
			return nil, err
		}

		if readerTopologyInvalidated(states) {
			continue
		}

		return snapshotFromReaderStates(states), nil
	}
}

func readerTopologyInvalidated(states []readerState) bool {
	for _, state := range states {
		if state.eventState&(ReaderStateUnknown|ReaderStateUnavailable) != 0 {
			return true
		}
	}

	return false
}

func readerStates(
	names []string,
	current map[string]*ReaderInfo,
) []readerState {
	states := make([]readerState, 0, len(names))
	for _, name := range names {
		state := readerState{name: name}
		if info, ok := current[name]; ok {
			state.currentState = info.State
			state.eventState = info.State
			state.atr = info.ATR
		}

		states = append(states, state)
	}

	return states
}

func snapshotFromReaderStates(states []readerState) map[string]*ReaderInfo {
	readers := make(map[string]*ReaderInfo, len(states))

	for _, state := range states {
		normalized := state.eventState &^ ReaderStateChanged
		readers[state.name] = &ReaderInfo{
			Name:  state.name,
			State: normalized,
			ATR:   state.atr,
		}
	}

	return readers
}
