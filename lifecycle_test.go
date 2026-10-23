//go:build darwin || linux || windows

package pcsc

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunCardOperationDiscardsValueOnError(t *testing.T) {
	expectedErr := errors.New("operation failed")

	value, err := runCardOperation(newCard(0, 0, 0, 0), t.Context(), func() (int, error) {
		return 42, expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("error = %v, want operation error", err)
	}

	if value != 0 {
		t.Fatalf("value = %d, want zero on error", value)
	}
}

func TestBeginTransactionReturnsNativeResult(t *testing.T) {
	originalBegin := scardBeginTransaction
	originalEnd := scardEndTransaction

	t.Cleanup(func() {
		scardBeginTransaction = originalBegin
		scardEndTransaction = originalEnd
	})

	scardEndTransaction = func(scardHandle, scardDWORD) scardResult {
		t.Error("accepted transaction was ended automatically")
		return 0
	}

	tests := []struct {
		name string
		code uint32
		want error
	}{
		{name: "success"},
		{name: "sharing violation", code: 0x8010000b, want: ErrSharingViolation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scardBeginTransaction = func(scardHandle) scardResult {
				return scardResultFromCodeForTest(test.code)
			}
			card := newCard(0, scardHandle(1), ProtocolT1, 0)

			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()

			if err := card.BeginTransaction(ctx); !errors.Is(err, test.want) {
				t.Errorf("BeginTransaction error = %v, want %v", err, test.want)
			}

			if err := card.lockOperation(ctx); err != nil {
				t.Fatalf("operation gate was not released after BeginTransaction: %v", err)
			}

			card.unlockOperation()
		})
	}
}

func TestBeginTransactionCancellationReturnsPromptly(t *testing.T) {
	originalBegin := scardBeginTransaction
	originalCancel := cancelCardOperation
	originalEnd := scardEndTransaction

	t.Cleanup(func() {
		scardBeginTransaction = originalBegin
		cancelCardOperation = originalCancel
		scardEndTransaction = originalEnd
	})

	started := make(chan struct{})
	release := make(chan struct{})
	cancelCalled := make(chan struct{})
	cleanupCalled := make(chan struct{})

	scardBeginTransaction = func(scardHandle) scardResult {
		close(started)
		<-release

		return 0
	}
	cancelCardOperation = func(scardContext) error {
		close(cancelCalled)

		return nil
	}
	scardEndTransaction = func(_ scardHandle, disposition scardDWORD) scardResult {
		if disposition != scardDWORD(DispositionLeaveCard) {
			t.Errorf("cleanup disposition = %d, want %d", disposition, DispositionLeaveCard)
		}

		close(cleanupCalled)

		return 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	card := newCard(scardContext(1), scardHandle(1), 0, 0)

	result := make(chan error, 1)
	go func() {
		result <- card.BeginTransaction(ctx)
	}()

	<-started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("BeginTransaction error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BeginTransaction did not return after cancellation")
	}

	select {
	case <-cancelCalled:
	case <-time.After(time.Second):
		t.Fatal("native cancellation was not requested")
	}

	close(release)

	select {
	case <-cleanupCalled:
	case <-time.After(time.Second):
		t.Fatal("late successful transaction was not ended")
	}
}

func TestCloseCancelsInFlightOperation(t *testing.T) {
	originalTransmit := scardTransmit
	originalCancel := cancelCardOperation
	originalDisconnect := scardDisconnect
	originalRelease := scardReleaseContext

	t.Cleanup(func() {
		scardTransmit = originalTransmit
		cancelCardOperation = originalCancel
		scardDisconnect = originalDisconnect
		scardReleaseContext = originalRelease
	})

	started := make(chan struct{})
	canceled := make(chan struct{})
	scardTransmit = func(
		_ scardHandle,
		_ *scardIORequest,
		_ uintptr,
		_ scardDWORD,
		_ *scardIORequest,
		_ uintptr,
		receiveLength *scardDWORD,
	) scardResult {
		close(started)
		<-canceled

		*receiveLength = 0

		return 0
	}
	cancelCardOperation = func(scardContext) error {
		close(canceled)
		return nil
	}

	disconnected := false
	scardDisconnect = func(_ scardHandle, disposition scardDWORD) scardResult {
		disconnected = true

		if disposition != scardDWORD(DispositionResetCard) {
			t.Errorf("disconnect disposition = %d, want %d", disposition, DispositionResetCard)
		}

		return 0
	}

	released := false
	scardReleaseContext = func(scardContext) scardResult {
		released = true
		return 0
	}

	card := newCard(
		scardContext(1),
		scardHandle(1),
		ProtocolT1,
		DispositionResetCard,
	)
	transmitResult := make(chan error, 1)

	go func() {
		_, err := card.Transmit(context.Background(), []byte{1})
		transmitResult <- err
	}()

	<-started

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- card.Close()
	}()

	select {
	case err := <-closeResult:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel the in-flight operation")
	}

	if err := <-transmitResult; err != nil {
		t.Fatalf("Transmit: %v", err)
	}

	if !disconnected {
		t.Fatal("SCardDisconnect was not called")
	}

	if !released {
		t.Fatal("SCardReleaseContext was not called")
	}

	if err := card.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestEndTransactionForwardsDisposition(t *testing.T) {
	original := scardEndTransaction

	t.Cleanup(func() { scardEndTransaction = original })

	var gotDisposition scardDWORD

	scardEndTransaction = func(_ scardHandle, disposition scardDWORD) scardResult {
		gotDisposition = disposition

		return 0
	}

	card := newCard(0, scardHandle(1), 0, 0)
	if err := card.EndTransaction(DispositionResetCard); err != nil {
		t.Fatalf("EndTransaction: %v", err)
	}

	if gotDisposition != scardDWORD(DispositionResetCard) {
		t.Fatalf("disposition = %d, want %d", gotDisposition, DispositionResetCard)
	}
}

func TestReconnectUpdatesActiveProtocol(t *testing.T) {
	original := scardReconnect

	t.Cleanup(func() { scardReconnect = original })

	scardReconnect = func(
		_ scardHandle,
		shareMode scardDWORD,
		preferredProtocols scardDWORD,
		initialization scardDWORD,
		activeProtocol *scardDWORD,
	) scardResult {
		if shareMode != scardDWORD(ShareModeExclusive) {
			t.Errorf("share mode = %d, want %d", shareMode, ShareModeExclusive)
		}

		if preferredProtocols != scardDWORD(ProtocolT1) {
			t.Errorf("preferred protocols = %d, want %d", preferredProtocols, ProtocolT1)
		}

		if initialization != scardDWORD(DispositionResetCard) {
			t.Errorf("initialization = %d, want %d", initialization, DispositionResetCard)
		}

		*activeProtocol = scardDWORD(ProtocolT1)

		return 0
	}

	card := newCard(0, scardHandle(1), ProtocolT0, 0)

	protocol, err := card.Reconnect(
		context.Background(),
		ShareModeExclusive,
		ProtocolT1,
		DispositionResetCard,
	)
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}

	if protocol != ProtocolT1 {
		t.Fatalf("returned protocol = %d, want %d", protocol, ProtocolT1)
	}

	if card.protocol != ProtocolT1 {
		t.Fatalf("stored protocol = %d, want %d", card.protocol, ProtocolT1)
	}
}

func TestReconnectRejectsInvalidParametersBeforeNativeCall(t *testing.T) {
	original := scardReconnect

	t.Cleanup(func() { scardReconnect = original })

	scardReconnect = func(
		scardHandle,
		scardDWORD,
		scardDWORD,
		scardDWORD,
		*scardDWORD,
	) scardResult {
		t.Fatal("SCardReconnect was called with invalid parameters")
		return 0
	}

	card := newCard(0, scardHandle(1), ProtocolT0, 0)

	_, err := card.Reconnect(
		context.Background(),
		ShareMode(99),
		ProtocolT1,
		DispositionLeaveCard,
	)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Reconnect error = %v, want ErrInvalidValue", err)
	}

	_, err = card.Reconnect(
		context.Background(),
		ShareModeShared,
		ProtocolT1,
		Disposition(99),
	)
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Reconnect error = %v, want ErrInvalidValue", err)
	}
}

func TestClosedCardRejectsLifecycleOperations(t *testing.T) {
	card := newCard(0, 0, 0, 0)
	card.closed = true

	if err := card.BeginTransaction(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("BeginTransaction error = %v, want ErrClosed", err)
	}

	if err := card.EndTransaction(DispositionLeaveCard); !errors.Is(err, ErrClosed) {
		t.Fatalf("EndTransaction error = %v, want ErrClosed", err)
	}

	if _, err := card.Reconnect(context.Background(), ShareModeShared, ProtocolT1, DispositionLeaveCard); !errors.Is(err, ErrClosed) {
		t.Fatalf("Reconnect error = %v, want ErrClosed", err)
	}
}
