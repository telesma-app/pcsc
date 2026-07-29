//go:build darwin || linux || windows

package pcsc

import (
	"errors"
	"testing"
	"time"
)

func TestSnapshotFromReaderStatesNormalizesChangedBit(t *testing.T) {
	states := []readerState{
		{name: "reader", eventState: ReaderStateChanged | ReaderStatePresent, atr: []byte{1, 2}},
		{name: pnpNotificationReader, eventState: ReaderStateChanged | ReaderStateUnknown},
	}
	readers, pnpState := snapshotFromReaderStates(states)
	if got := readers["reader"].State; got != ReaderStatePresent {
		t.Fatalf("reader state = 0x%x, want 0x%x", got, ReaderStatePresent)
	}
	if pnpState != ReaderStateUnknown {
		t.Fatalf("PnP state = 0x%x, want 0x%x", pnpState, ReaderStateUnknown)
	}
}

func TestStatusChangeFallsBackWhenPnPReaderIsUnsupported(t *testing.T) {
	original := getStatusChange
	t.Cleanup(func() { getStatusChange = original })

	calls := 0
	getStatusChange = func(_ scardContext, _ time.Duration, states []readerState) error {
		calls++
		pnp := &states[len(states)-1]
		if calls == 1 {
			if pnp.currentState != ReaderStateUnaware {
				t.Fatalf("initial PnP state = 0x%x, want unaware", pnp.currentState)
			}

			return &Error{Operation: "SCardGetStatusChange", Code: 0x80100009}
		}

		if pnp.currentState != ReaderStateIgnore {
			t.Fatalf("fallback PnP state = 0x%x, want ignore", pnp.currentState)
		}

		return ErrTimeout
	}

	states := []readerState{
		{name: "reader"},
		{name: pnpNotificationReader},
	}
	err := waitForStatusChange(scardContext(1), 0, states)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("waitForStatusChange error = %v, want ErrTimeout", err)
	}
	if calls != 2 {
		t.Fatalf("status-change calls = %d, want 2", calls)
	}
}

func TestEventReceiverPublishesTerminalError(t *testing.T) {
	originalStatusChange := getStatusChange
	originalRelease := scardReleaseContext
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		scardReleaseContext = originalRelease
	})

	getStatusChange = func(scardContext, time.Duration, []readerState) error {
		return ErrCommunication
	}
	scardReleaseContext = func(scardContext) scardResult {
		return 0
	}

	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		nativeContext: scardContext(1),
		stopped:       make(chan struct{}),
	}
	go receiver.run(nil, ReaderStateUnaware)

	event, ok := <-receiver.Listen()
	if !ok {
		t.Fatal("Listen closed without publishing the terminal error")
	}
	if !errors.Is(event.Err, ErrCommunication) {
		t.Fatalf("event error = %v, want ErrCommunication", event.Err)
	}
	if _, ok := <-receiver.Listen(); ok {
		t.Fatal("Listen remained open after the terminal error")
	}
	if err := receiver.Close(); !errors.Is(err, ErrCommunication) {
		t.Fatalf("Close error = %v, want ErrCommunication", err)
	}
}
