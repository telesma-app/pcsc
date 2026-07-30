//go:build linux || windows

package pcsc

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestPnPTopologyDoesNotCancelAfterRunStopped(t *testing.T) {
	originalStatusChange := getStatusChange
	originalCancel := scardCancel
	originalRelease := scardReleaseContext
	t.Cleanup(func() {
		getStatusChange = originalStatusChange
		scardCancel = originalCancel
		scardReleaseContext = originalRelease
	})

	getStatusChange = func(scardContext, time.Duration, []readerState) error {
		return ErrCommunication
	}
	var cancels atomic.Int32
	scardCancel = func(scardContext) scardResult {
		cancels.Add(1)
		return 0
	}
	var releases atomic.Int32
	scardReleaseContext = func(scardContext) scardResult {
		releases.Add(1)
		return 0
	}

	topology := &pnpReaderTopology{
		events:        make(chan error, 1),
		nativeContext: scardContext(1),
		done:          make(chan struct{}),
		stopped:       make(chan struct{}),
	}
	go topology.run(ReaderStateUnaware)

	if err := <-topology.Events(); !errors.Is(err, ErrCommunication) {
		t.Fatalf("topology error = %v, want ErrCommunication", err)
	}
	<-topology.stopped

	if err := topology.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := cancels.Load(); got != 0 {
		t.Fatalf("SCardCancel calls = %d, want 0", got)
	}
	if got := releases.Load(); got != 1 {
		t.Fatalf("SCardReleaseContext calls = %d, want 1", got)
	}
}
