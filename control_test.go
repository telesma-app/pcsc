//go:build darwin || linux || windows

package pcsc

import (
	"context"
	"errors"
	"testing"
)

func TestControlForwardsRequestAndReturnsResponse(t *testing.T) {
	original := scardControl

	t.Cleanup(func() { scardControl = original })

	const responseSize = 64

	input := []byte{1, 2, 3}
	scardControl = func(
		_ scardHandle,
		controlCode scardDWORD,
		inputPointer uintptr,
		inputLength scardDWORD,
		outputPointer uintptr,
		outputLength scardDWORD,
		bytesReturned *scardDWORD,
	) scardResult {
		if controlCode != 42 {
			t.Errorf("control code = %d, want 42", controlCode)
		}

		if inputPointer == 0 || inputLength != scardDWORD(len(input)) {
			t.Errorf("input pointer = 0x%x, length = %d", inputPointer, inputLength)
		}

		if outputPointer == 0 || outputLength != responseSize {
			t.Errorf("output pointer = 0x%x, length = %d", outputPointer, outputLength)
		}

		*bytesReturned = 0

		return 0
	}

	card := newCard(0, scardHandle(1), 0, 0)

	response, err := card.Control(context.Background(), 42, input, responseSize)
	if err != nil {
		t.Fatalf("Control: %v", err)
	}

	if len(response) != 0 {
		t.Fatalf("response = %x, want empty", response)
	}
}

func TestControlDoesNotRetryAfterInsufficientBuffer(t *testing.T) {
	original := scardControl

	t.Cleanup(func() { scardControl = original })

	calls := 0
	scardControl = func(
		_ scardHandle,
		_ scardDWORD,
		_ uintptr,
		_ scardDWORD,
		_ uintptr,
		_ scardDWORD,
		bytesReturned *scardDWORD,
	) scardResult {
		calls++
		*bytesReturned = 5

		return scardResultFromCodeForTest(scardEInsufficientBuf)
	}

	card := newCard(0, scardHandle(1), 0, 0)
	_, err := card.Control(context.Background(), 42, nil, 4)

	var pcscErr *Error
	if !errors.As(err, &pcscErr) || pcscErr.Code != scardEInsufficientBuf {
		t.Fatalf("Control error = %v, want insufficient buffer", err)
	}

	if calls != 1 {
		t.Fatalf("SCardControl calls = %d, want 1", calls)
	}
}

func TestControlUsesRequestedResponseSize(t *testing.T) {
	original := scardControl

	t.Cleanup(func() { scardControl = original })

	scardControl = func(
		_ scardHandle,
		_ scardDWORD,
		_ uintptr,
		_ scardDWORD,
		output uintptr,
		outputLength scardDWORD,
		bytesReturned *scardDWORD,
	) scardResult {
		if outputLength != 4 {
			t.Fatalf("output length = %d, want 4", outputLength)
		}

		if output == 0 {
			t.Fatal("output pointer is nil")
		}

		*bytesReturned = 3

		return 0
	}

	card := newCard(0, scardHandle(1), 0, 0)

	response, err := card.Control(context.Background(), 42, nil, 4)
	if err != nil {
		t.Fatalf("Control: %v", err)
	}

	if len(response) != 3 {
		t.Fatalf("response length = %d, want 3", len(response))
	}
}

func TestControlRejectsInvalidResponseSize(t *testing.T) {
	card := newCard(0, scardHandle(1), 0, 0)

	_, err := card.Control(context.Background(), 42, nil, -1)
	if !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("error = %v, want ErrInvalidParameter", err)
	}
}

func TestControlRejectsInvalidReturnedSize(t *testing.T) {
	original := scardControl

	t.Cleanup(func() { scardControl = original })

	scardControl = func(
		_ scardHandle,
		_ scardDWORD,
		_ uintptr,
		_ scardDWORD,
		_ uintptr,
		outputLength scardDWORD,
		bytesReturned *scardDWORD,
	) scardResult {
		*bytesReturned = outputLength + 1
		return 0
	}

	card := newCard(0, scardHandle(1), 0, 0)

	_, err := card.Control(context.Background(), 42, nil, 4)
	if !errors.Is(err, ErrInsufficientBuffer) {
		t.Fatalf("error = %v, want ErrInsufficientBuffer", err)
	}
}

func TestControlOwnsInputAfterCancellation(t *testing.T) {
	originalControl := scardControl
	originalCancel := cancelCardOperation

	t.Cleanup(func() {
		scardControl = originalControl
		cancelCardOperation = originalCancel
	})

	started := make(chan struct{})
	readInput := make(chan struct{})
	observed := make(chan uintptr, 1)
	scardControl = func(
		_ scardHandle,
		_ scardDWORD,
		inputPointer uintptr,
		inputLength scardDWORD,
		_ uintptr,
		_ scardDWORD,
		bytesReturned *scardDWORD,
	) scardResult {
		close(started)
		<-readInput

		if inputLength != 4 {
			t.Errorf("input length = %d, want 4", inputLength)
		}

		observed <- inputPointer

		*bytesReturned = 0

		return 0
	}
	cancelCardOperation = func(scardContext) error { return nil }

	input := []byte{1, 2, 3, 4}
	callerPointer := byteSlicePointer(input)
	ctx, cancel := context.WithCancel(context.Background())
	card := newCard(scardContext(1), scardHandle(1), 0, 0)
	result := make(chan error, 1)

	go func() {
		_, err := card.Control(ctx, 42, input, 4)
		result <- err
	}()

	<-started
	cancel()

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Control error = %v, want context.Canceled", err)
	}

	for index := range input {
		input[index] = 0xff
	}

	close(readInput)

	if got := <-observed; got == callerPointer {
		t.Fatal("native call retained the caller's control buffer")
	}
}

func TestGetAttributeQueriesSize(t *testing.T) {
	original := scardGetAttrib

	t.Cleanup(func() { scardGetAttrib = original })

	calls := 0
	scardGetAttrib = func(_ scardHandle, attribute scardDWORD, value uintptr, length *scardDWORD) scardResult {
		calls++

		if attribute != 7 {
			t.Errorf("attribute = %d, want 7", attribute)
		}

		if value != 0 {
			t.Errorf("value pointer = 0x%x, want zero for size query", value)
		}

		*length = 0

		return 0
	}

	card := newCard(0, scardHandle(1), 0, 0)

	value, err := card.GetAttribute(Attribute(7))
	if err != nil {
		t.Fatalf("GetAttribute: %v", err)
	}

	if value != nil {
		t.Fatalf("value = %x, want nil", value)
	}

	if calls != 1 {
		t.Fatalf("SCardGetAttrib calls = %d, want 1", calls)
	}
}

func TestSetAttributeForwardsValue(t *testing.T) {
	original := scardSetAttrib

	t.Cleanup(func() { scardSetAttrib = original })

	want := []byte("value")
	scardSetAttrib = func(_ scardHandle, attribute scardDWORD, value uintptr, length scardDWORD) scardResult {
		if attribute != 7 {
			t.Errorf("attribute = %d, want 7", attribute)
		}

		if value == 0 || length != scardDWORD(len(want)) {
			t.Errorf("value pointer = 0x%x, length = %d", value, length)
		}

		return 0
	}

	card := newCard(0, scardHandle(1), 0, 0)
	if err := card.SetAttribute(Attribute(7), want); err != nil {
		t.Fatalf("SetAttribute: %v", err)
	}
}
