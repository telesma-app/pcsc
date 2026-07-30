//go:build darwin || linux || windows

package pcsc

import (
	"errors"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"
)

type fakeReaderTopology struct {
	events chan error
}

func newFakeReaderTopology() *fakeReaderTopology {
	return &fakeReaderTopology{events: make(chan error, 1)}
}

func (t *fakeReaderTopology) Events() <-chan error {
	return t.events
}

func (t *fakeReaderTopology) Close() error {
	return nil
}

func TestSnapshotFromReaderStatesNormalizesChangedBit(t *testing.T) {
	states := []readerState{{
		name:       "reader",
		eventState: ReaderStateChanged | ReaderStatePresent,
		atr:        []byte{1, 2},
	}}
	readers := snapshotFromReaderStates(states)
	if got := readers["reader"].State; got != ReaderStatePresent {
		t.Fatalf("reader state = 0x%x, want 0x%x", got, ReaderStatePresent)
	}
}

func TestSnapshotFromReadersIsSorted(t *testing.T) {
	readers := map[string]*ReaderInfo{
		"B": {Name: "B", State: ReaderStateEmpty},
		"A": {Name: "A", State: ReaderStatePresent, ATR: []byte{1, 2}},
	}

	snapshot := snapshotFromReaders(readers)
	if len(snapshot.Readers) != 2 || snapshot.Readers[0].Name != "A" || snapshot.Readers[1].Name != "B" {
		t.Fatalf("snapshot readers = %#v, want A then B", snapshot.Readers)
	}
}

func TestReaderSnapshotReenumeratesInvalidatedTopology(t *testing.T) {
	originalStatusChange := getStatusChange
	originalListReaders := listReaders
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		listReaders = originalListReaders
	})

	var enumerations int
	listReaders = func(scardContext) ([]string, error) {
		enumerations++
		if enumerations == 1 {
			return []string{"A"}, nil
		}

		return []string{"A", "B"}, nil
	}

	var statusChanges int
	getStatusChange = func(_ scardContext, _ time.Duration, states []readerState) error {
		statusChanges++
		for index := range states {
			switch states[index].name {
			case "A":
				if statusChanges == 1 {
					states[index].eventState = ReaderStateUnknown
				} else {
					states[index].eventState = ReaderStatePresent
					states[index].atr = []byte{1}
				}
			case "B":
				states[index].eventState = ReaderStatePresent
				states[index].atr = []byte{2}
			}
		}

		return nil
	}

	readers, err := readerSnapshot(scardContext(1), nil)
	if err != nil {
		t.Fatalf("readerSnapshot: %v", err)
	}
	if got := slices.Sorted(maps.Keys(readers)); !slices.Equal(got, []string{"A", "B"}) {
		t.Fatalf("readers = %v, want A and B", got)
	}
	if enumerations != 2 {
		t.Fatalf("enumerations = %d, want 2", enumerations)
	}
}

func TestReaderSnapshotStopsBetweenTopologyRetries(t *testing.T) {
	originalStatusChange := getStatusChange
	originalListReaders := listReaders
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		listReaders = originalListReaders
	})

	done := make(chan struct{})
	listReaders = func(scardContext) ([]string, error) {
		return []string{"A"}, nil
	}
	getStatusChange = func(scardContext, time.Duration, []readerState) error {
		close(done)
		return ErrUnknownReader
	}

	_, err := readerSnapshotUntil(scardContext(1), nil, done)
	if !errors.Is(err, ErrCanceled) {
		t.Fatalf("readerSnapshotUntil error = %v, want ErrCanceled", err)
	}
}

func TestWatcherReconcilesUnknownReader(t *testing.T) {
	originalStatusChange := getStatusChange
	originalListReaders := listReaders
	originalCancel := scardCancel
	originalRelease := scardReleaseContext
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		listReaders = originalListReaders
		scardCancel = originalCancel
		scardReleaseContext = originalRelease
	})

	listReaders = func(scardContext) ([]string, error) {
		return []string{"B"}, nil
	}

	blocked := make(chan struct{})
	var unblock sync.Once
	var statusChanges int
	getStatusChange = func(_ scardContext, _ time.Duration, states []readerState) error {
		statusChanges++
		switch statusChanges {
		case 1:
			return ErrUnknownReader
		case 2:
			states[0].eventState = ReaderStatePresent
			states[0].atr = []byte{2}
			return nil
		default:
			<-blocked
			return ErrCanceled
		}
	}
	scardCancel = func(scardContext) scardResult {
		unblock.Do(func() {
			close(blocked)
		})
		return 0
	}
	scardReleaseContext = func(scardContext) scardResult {
		return 0
	}

	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		topology:      newFakeReaderTopology(),
		nativeContext: scardContext(1),
		stopped:       make(chan struct{}),
	}
	go receiver.run(map[string]*ReaderInfo{
		"A": {
			Name:  "A",
			State: ReaderStatePresent,
			ATR:   []byte{1},
		},
	})

	assertEventTypes(t, receiver, []DeviceEventType{
		DeviceEventCardRemoved,
		DeviceEventReaderDisconnected,
		DeviceEventReaderConnected,
		DeviceEventCardInserted,
	})
	if err := receiver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestWatcherReconcilesTopologyEvent(t *testing.T) {
	originalStatusChange := getStatusChange
	originalListReaders := listReaders
	originalCancel := scardCancel
	originalRelease := scardReleaseContext
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		listReaders = originalListReaders
		scardCancel = originalCancel
		scardReleaseContext = originalRelease
	})

	topology := newFakeReaderTopology()
	listReaders = func(scardContext) ([]string, error) {
		return []string{"A", "B"}, nil
	}

	blocked := make(chan struct{})
	var unblock sync.Once
	var statusChanges int
	getStatusChange = func(_ scardContext, timeout time.Duration, states []readerState) error {
		statusChanges++
		if timeout == statusChangeInfinite {
			<-blocked
			return ErrCanceled
		}
		for index := range states {
			states[index].eventState = ReaderStateEmpty
		}
		return nil
	}
	scardCancel = func(scardContext) scardResult {
		unblock.Do(func() {
			close(blocked)
		})
		return 0
	}
	scardReleaseContext = func(scardContext) scardResult {
		return 0
	}

	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		topology:      topology,
		nativeContext: scardContext(1),
		stopped:       make(chan struct{}),
	}
	go receiver.run(map[string]*ReaderInfo{
		"A": {Name: "A", State: ReaderStateEmpty},
	})
	topology.events <- nil

	assertEventTypes(t, receiver, []DeviceEventType{DeviceEventReaderConnected})
	if err := receiver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if statusChanges < 2 {
		t.Fatalf("status-change calls = %d, want at least 2", statusChanges)
	}
}

func TestWatcherDoesNotCollapseBackToBackCardChanges(t *testing.T) {
	originalStatusChange := getStatusChange
	originalCancel := scardCancel
	originalRelease := scardReleaseContext
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		scardCancel = originalCancel
		scardReleaseContext = originalRelease
	})

	blocked := make(chan struct{})
	var unblock sync.Once
	var statusChanges int
	getStatusChange = func(_ scardContext, _ time.Duration, states []readerState) error {
		statusChanges++
		switch statusChanges {
		case 1:
			states[0].eventState = ReaderStatePresent
			states[0].atr = []byte{1}
			return nil
		case 2:
			states[0].eventState = ReaderStateEmpty
			states[0].atr = nil
			return nil
		default:
			<-blocked
			return ErrCanceled
		}
	}
	scardCancel = func(scardContext) scardResult {
		unblock.Do(func() {
			close(blocked)
		})
		return 0
	}
	scardReleaseContext = func(scardContext) scardResult {
		return 0
	}

	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		topology:      newFakeReaderTopology(),
		nativeContext: scardContext(1),
		stopped:       make(chan struct{}),
	}
	go receiver.run(map[string]*ReaderInfo{
		"A": {Name: "A", State: ReaderStateEmpty},
	})

	assertEventTypes(t, receiver, []DeviceEventType{
		DeviceEventCardInserted,
		DeviceEventCardRemoved,
	})
	if err := receiver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if statusChanges != 3 {
		t.Fatalf("status-change calls = %d, want 3", statusChanges)
	}
}

func TestWatcherReturnsTerminalErrorFromClose(t *testing.T) {
	originalRelease := scardReleaseContext
	t.Cleanup(func() {
		scardReleaseContext = originalRelease
	})
	scardReleaseContext = func(scardContext) scardResult {
		return 0
	}

	topology := newFakeReaderTopology()
	receiver := &eventReceiver{
		events:        newDeviceEventQueue(),
		topology:      topology,
		nativeContext: scardContext(1),
		stopped:       make(chan struct{}),
	}
	go receiver.run(nil)
	topology.events <- ErrCommunication

	if _, ok := <-receiver.Listen(); ok {
		t.Fatal("Listen remained open after the terminal error")
	}
	if err := receiver.Close(); !errors.Is(err, ErrCommunication) {
		t.Fatalf("Close error = %v, want ErrCommunication", err)
	}
}

func assertEventTypes(t *testing.T, receiver *eventReceiver, want []DeviceEventType) {
	t.Helper()
	for index, eventType := range want {
		select {
		case event := <-receiver.Listen():
			if event.Type != eventType {
				t.Fatalf("event %d = %q, want %q", index, event.Type, eventType)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out receiving event %d", index)
		}
	}
}
