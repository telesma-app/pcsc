//go:build darwin || linux || windows

package pcsc

import (
	"context"
	"errors"
	"testing"
	"time"
	"unsafe"
)

func TestTransmitDoesNotRetryAfterInsufficientBuffer(t *testing.T) {
	original := scardTransmit
	t.Cleanup(func() { scardTransmit = original })

	calls := 0
	scardTransmit = func(
		_ scardHandle,
		sendPCI *scardIORequest,
		_ uintptr,
		_ scardDWORD,
		_ *scardIORequest,
		_ uintptr,
		recvLength *scardDWORD,
	) scardResult {
		calls++
		if sendPCI == nil {
			t.Fatal("send PCI is nil")
		}
		if got, want := sendPCI.Length, scardDWORD(unsafe.Sizeof(*sendPCI)); got != want {
			t.Errorf("send PCI length = %d, want %d", got, want)
		}
		if got := *recvLength; got != scardDWORD(maxAPDUResponseSize) {
			t.Errorf("receive buffer size = %d, want %d", got, maxAPDUResponseSize)
		}

		*recvLength = 8192
		return scardResultFromCodeForTest(scardEInsufficientBuf)
	}

	c := &Card{handle: scardHandle(1), protocol: ProtocolT1}
	response, err := c.Transmit(context.Background(), []byte{0x00, 0xa4, 0x04, 0x00})
	var pcscErr *Error
	if !errors.As(err, &pcscErr) || pcscErr.Code != scardEInsufficientBuf {
		t.Fatalf("error = %v, want PC/SC insufficient buffer", err)
	}
	if response != nil {
		t.Fatalf("response = %x, want nil", response)
	}
	if calls != 1 {
		t.Fatalf("SCardTransmit calls = %d, want 1", calls)
	}
}

func TestTransmitCancellationReturnsPromptlyAndRequestsNativeCancellation(t *testing.T) {
	originalTransmit := scardTransmit
	originalCancel := cancelCardOperation
	t.Cleanup(func() {
		scardTransmit = originalTransmit
		cancelCardOperation = originalCancel
	})

	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	cancelCalled := make(chan struct{})

	scardTransmit = func(
		_ scardHandle,
		_ *scardIORequest,
		_ uintptr,
		_ scardDWORD,
		_ *scardIORequest,
		_ uintptr,
		recvLength *scardDWORD,
	) scardResult {
		close(started)
		<-release
		*recvLength = 0
		close(finished)
		return 0
	}
	cancelCardOperation = func(_ scardContext) error {
		close(cancelCalled)
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Card{nativeContext: scardContext(1), handle: scardHandle(1), protocol: ProtocolT1}
	result := make(chan error, 1)
	go func() {
		_, err := c.Transmit(ctx, []byte{0x00, 0xa4, 0x04, 0x00})
		result <- err
	}()

	<-started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Transmit did not return after context cancellation")
	}

	select {
	case <-cancelCalled:
	case <-time.After(time.Second):
		t.Fatal("native cancellation was not requested")
	}

	close(release)
	<-finished
}

func TestTransmitOwnsInputAfterCancellation(t *testing.T) {
	originalTransmit := scardTransmit
	originalCancel := cancelCardOperation
	t.Cleanup(func() {
		scardTransmit = originalTransmit
		cancelCardOperation = originalCancel
	})

	started := make(chan struct{})
	readInput := make(chan struct{})
	observed := make(chan uintptr, 1)
	scardTransmit = func(
		_ scardHandle,
		_ *scardIORequest,
		sendBuffer uintptr,
		sendLength scardDWORD,
		_ *scardIORequest,
		_ uintptr,
		receiveLength *scardDWORD,
	) scardResult {
		close(started)
		<-readInput
		if sendLength != 4 {
			t.Errorf("send length = %d, want 4", sendLength)
		}
		observed <- sendBuffer
		*receiveLength = 0
		return 0
	}
	cancelCardOperation = func(scardContext) error { return nil }

	apdu := []byte{1, 2, 3, 4}
	inputPointer := byteSlicePointer(apdu)
	ctx, cancel := context.WithCancel(context.Background())
	card := &Card{nativeContext: scardContext(1), handle: scardHandle(1), protocol: ProtocolT1}
	result := make(chan error, 1)
	go func() {
		_, err := card.Transmit(ctx, apdu)
		result <- err
	}()

	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Transmit error = %v, want context.Canceled", err)
	}
	for index := range apdu {
		apdu[index] = 0xff
	}
	close(readInput)

	if got := <-observed; got == inputPointer {
		t.Fatal("native call retained the caller's APDU buffer")
	}
}

func TestTransmitDoesNotStartAfterCancellationWhileQueued(t *testing.T) {
	original := scardTransmit
	t.Cleanup(func() { scardTransmit = original })

	scardTransmit = func(
		_ scardHandle,
		_ *scardIORequest,
		_ uintptr,
		_ scardDWORD,
		_ *scardIORequest,
		_ uintptr,
		_ *scardDWORD,
	) scardResult {
		t.Fatal("SCardTransmit called after cancellation while waiting for the card")
		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	c := &Card{handle: scardHandle(1), protocol: ProtocolT1}
	if err := c.lockOperation(context.Background()); err != nil {
		t.Fatalf("lockOperation: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, err := c.Transmit(ctx, []byte{0x00})
		result <- err
	}()

	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Transmit did not return after cancellation while waiting for the card")
	}
	c.unlockOperation()
}

func scardResultFromCodeForTest(code uint32) scardResult {
	return scardResult(int32(code))
}
