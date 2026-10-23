package pcsc

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestDeviceEventQueueBurstIsLosslessAndOrdered(t *testing.T) {
	queue := newDeviceEventQueue()

	const count = 1000
	for index := range count {
		if !queue.Send(DeviceEvent{ReaderInfo: &ReaderInfo{Name: fmt.Sprintf("reader-%04d", index)}}) {
			t.Fatalf("Send(%d) rejected", index)
		}
	}

	for index := range count {
		select {
		case event := <-queue.Listen():
			want := fmt.Sprintf("reader-%04d", index)
			if event.ReaderInfo == nil || event.ReaderInfo.Name != want {
				t.Fatalf("event %d = %#v, want reader %q", index, event, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out receiving event %d", index)
		}
	}

	queue.Close()

	if _, ok := <-queue.Listen(); ok {
		t.Fatal("event channel remains open after Close")
	}
}

func TestDeviceEventQueueCloseWithoutListener(t *testing.T) {
	queue := newDeviceEventQueue()
	for range 100 {
		if !queue.Send(DeviceEvent{}) {
			t.Fatal("Send rejected before Close")
		}
	}

	done := make(chan struct{})

	go func() {
		queue.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked without a listener")
	}

	if queue.Send(DeviceEvent{}) {
		t.Fatal("Send accepted after Close")
	}
}

func TestDeviceEventQueueConcurrentSendAndClose(t *testing.T) {
	queue := newDeviceEventQueue()

	const senderCount = 32

	var senders sync.WaitGroup

	var firstSends sync.WaitGroup
	firstSends.Add(senderCount)
	senders.Add(senderCount)

	for range senderCount {
		go func() {
			defer senders.Done()

			queue.Send(DeviceEvent{})
			firstSends.Done()

			for queue.Send(DeviceEvent{}) {
			}
		}()
	}

	firstSends.Wait()

	const closerCount = 8

	var closers sync.WaitGroup
	closers.Add(closerCount)

	for range closerCount {
		go func() {
			defer closers.Done()

			queue.Close()
		}()
	}

	finished := make(chan struct{})

	go func() {
		senders.Wait()
		closers.Wait()
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("concurrent Send and Close did not finish")
	}

	if _, ok := <-queue.Listen(); ok {
		t.Fatal("event channel remains open after concurrent Close")
	}
}

func TestReconcileDeviceEvents(t *testing.T) {
	presentA := &ReaderInfo{Name: "A", State: ReaderStatePresent, ATR: []byte{1}}
	emptyB := &ReaderInfo{Name: "B", State: ReaderStateEmpty}
	presentB := &ReaderInfo{Name: "B", State: ReaderStatePresent, ATR: []byte{2}}
	replacedA := &ReaderInfo{Name: "A", State: ReaderStatePresent, ATR: []byte{3}}

	tests := []struct {
		name     string
		previous map[string]*ReaderInfo
		current  map[string]*ReaderInfo
		want     []DeviceEvent
	}{
		{
			name:    "initial snapshot",
			current: map[string]*ReaderInfo{"B": emptyB, "A": presentA},
			want: []DeviceEvent{
				{Type: DeviceEventReaderConnected, ReaderInfo: presentA},
				{Type: DeviceEventCardInserted, ReaderInfo: presentA},
				{Type: DeviceEventReaderConnected, ReaderInfo: emptyB},
			},
		},
		{
			name:     "card inserted",
			previous: map[string]*ReaderInfo{"B": emptyB},
			current:  map[string]*ReaderInfo{"B": presentB},
			want:     []DeviceEvent{{Type: DeviceEventCardInserted, ReaderInfo: presentB}},
		},
		{
			name:     "card replaced without empty observation",
			previous: map[string]*ReaderInfo{"A": presentA},
			current:  map[string]*ReaderInfo{"A": replacedA},
			want: []DeviceEvent{
				{Type: DeviceEventCardRemoved, ReaderInfo: presentA},
				{Type: DeviceEventCardInserted, ReaderInfo: replacedA},
			},
		},
		{
			name:     "reader removed with card",
			previous: map[string]*ReaderInfo{"A": presentA},
			want: []DeviceEvent{
				{Type: DeviceEventCardRemoved, ReaderInfo: presentA},
				{Type: DeviceEventReaderDisconnected, ReaderInfo: presentA},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := reconcileDeviceEvents(test.previous, test.current)
			if !reflect.DeepEqual(events, test.want) {
				t.Fatalf("events = %#v, want %#v", events, test.want)
			}
		})
	}
}
