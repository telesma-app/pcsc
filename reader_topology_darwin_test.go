//go:build darwin

package pcsc

import (
	"sync"
	"testing"
	"time"
)

func TestDarwinReaderTopologyLifecycle(t *testing.T) {
	for range 100 {
		topology, err := newReaderTopologyNative()
		if err != nil {
			t.Fatal(err)
		}

		closed := make(chan error, 1)
		go func() {
			closed <- topology.Close()
		}()

		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("immediate Close did not stop the IOKit run loop")
		}
	}
}

func TestDarwinReaderTopologyConcurrentClose(t *testing.T) {
	topology, err := newReaderTopologyNative()
	if err != nil {
		t.Fatal(err)
	}

	const closerCount = 16

	var closers sync.WaitGroup
	closers.Add(closerCount)

	for range closerCount {
		go func() {
			defer closers.Done()

			if err := topology.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		}()
	}

	closed := make(chan struct{})

	go func() {
		closers.Wait()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("concurrent Close did not stop the IOKit run loop")
	}
}

func TestDarwinReaderTopologySchedulesNotificationAfterPCSCRegistration(t *testing.T) {
	topology := &darwinReaderTopology{
		events: make(chan error, 1),
	}

	topology.changed()

	select {
	case <-topology.events:
	case <-time.After(2 * readerRegistrationDelay):
		t.Fatal("delayed topology event was not delivered")
	}
}
