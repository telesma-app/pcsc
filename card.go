//go:build darwin || linux || windows

package pcsc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"
)

const (
	scardEInsufficientBuf = uint32(0x80100008)
	maxAPDUResponseSize   = 65538
)

var cancelCardOperation = cancelNativeCardOperation

type scardIORequest struct {
	Protocol scardDWORD
	Length   scardDWORD
}

// Card is a connection to a smart card. Context cancellation returns promptly,
// but the native operation may continue in the background on implementations
// that cannot cancel card operations, including pcsc-lite.
type Card struct {
	operationGate         chan struct{}
	stateMu               sync.RWMutex
	nativeContext         scardContext
	handle                scardHandle
	protocol              Protocol
	disconnectDisposition Disposition
	closed                bool
	closeOnce             sync.Once
	closeErr              error
}

func newCard(
	nativeContext scardContext,
	handle scardHandle,
	protocol Protocol,
	disconnectDisposition Disposition,
) *Card {
	operationGate := make(chan struct{}, 1)
	operationGate <- struct{}{}

	return &Card{
		operationGate:         operationGate,
		nativeContext:         nativeContext,
		handle:                handle,
		protocol:              protocol,
		disconnectDisposition: disconnectDisposition,
	}
}

func scardError(operation string, result scardResult) error {
	return pcscError(operation, uint32(result))
}

func (card *Card) isClosed() bool {
	card.stateMu.RLock()
	defer card.stateMu.RUnlock()

	return card.closed
}

func (card *Card) cancelOperation() error {
	card.stateMu.RLock()
	defer card.stateMu.RUnlock()

	if card.closed {
		return nil
	}

	return cancelCardOperation(card.nativeContext)
}

func (card *Card) lockOperation(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-card.operationGate:
		if err := ctx.Err(); err != nil {
			card.unlockOperation()
			return err
		}

		return nil
	}
}

func (card *Card) unlockOperation() {
	card.operationGate <- struct{}{}
}

func runCardOperation[T any](
	card *Card,
	ctx context.Context,
	operation func() (T, error),
) (T, error) {
	var zero T

	if err := card.lockOperation(ctx); err != nil {
		return zero, err
	}

	if card.isClosed() {
		card.unlockOperation()

		return zero, ErrClosed
	}

	type operationResult struct {
		value T
		err   error
	}

	result := make(chan operationResult, 1)

	go func() {
		defer card.unlockOperation()

		value, err := operation()
		result <- operationResult{value: value, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = card.cancelOperation()
		return zero, ctx.Err()
	case result := <-result:
		if result.err != nil {
			return zero, result.err
		}

		return result.value, nil
	}
}

// BeginTransaction prevents other PC/SC applications from interleaving card
// operations until EndTransaction is called.
// If cancellation wins, a transaction established later is ended automatically.
func (card *Card) BeginTransaction(ctx context.Context) error {
	if err := card.lockOperation(ctx); err != nil {
		return err
	}

	if card.isClosed() {
		card.unlockOperation()

		return ErrClosed
	}

	result := make(chan error)
	abandoned := make(chan struct{})

	go func() {
		defer card.unlockOperation()

		err := scardError("SCardBeginTransaction", scardBeginTransaction(card.handle))
		select {
		case result <- err:
		case <-abandoned:
			if err == nil {
				_ = scardEndTransaction(card.handle, scardDWORD(DispositionLeaveCard))
			}
		}
	}()

	select {
	case <-ctx.Done():
		close(abandoned)

		_ = card.cancelOperation()

		return ctx.Err()
	case err := <-result:
		return err
	}
}

// EndTransaction releases a transaction and applies disposition to the card.
func (card *Card) EndTransaction(disposition Disposition) error {
	_ = card.lockOperation(context.Background())
	defer card.unlockOperation()

	if card.isClosed() {
		return ErrClosed
	}

	return scardError(
		"SCardEndTransaction",
		scardEndTransaction(card.handle, scardDWORD(disposition)),
	)
}

// Reconnect changes the connection parameters and optionally resets the card.
func (card *Card) Reconnect(
	ctx context.Context,
	shareMode ShareMode,
	preferredProtocols Protocol,
	initialization Disposition,
) (Protocol, error) {
	return runCardOperation(card, ctx, func() (Protocol, error) {
		if err := validateReconnectParameters(shareMode, initialization); err != nil {
			return ProtocolUndefined, err
		}

		var protocol scardDWORD

		result := scardReconnect(
			card.handle,
			scardDWORD(shareMode),
			scardDWORD(preferredProtocols),
			scardDWORD(initialization),
			&protocol,
		)
		if err := scardError("SCardReconnect", result); err != nil {
			return ProtocolUndefined, err
		}

		card.protocol = Protocol(protocol)

		return card.protocol, nil
	})
}

func validateReconnectParameters(shareMode ShareMode, initialization Disposition) error {
	switch shareMode {
	case ShareModeShared, ShareModeExclusive:
	case ShareModeDirect:
		if !reconnectSupportsDirect {
			return fmt.Errorf("%w: reconnect share mode %d", ErrInvalidValue, shareMode)
		}
	default:
		return fmt.Errorf("%w: reconnect share mode %d", ErrInvalidValue, shareMode)
	}

	switch initialization {
	case DispositionLeaveCard, DispositionResetCard, DispositionUnpowerCard:
	case DispositionEjectCard:
		if !reconnectSupportsEject {
			return fmt.Errorf("%w: reconnect disposition %d", ErrInvalidValue, initialization)
		}
	default:
		return fmt.Errorf("%w: reconnect disposition %d", ErrInvalidValue, initialization)
	}

	return nil
}

// Transmit sends one raw APDU and returns the complete response, including
// SW1/SW2.
func (card *Card) Transmit(ctx context.Context, apdu []byte) ([]byte, error) {
	apdu = bytes.Clone(apdu)

	return runCardOperation(card, ctx, func() ([]byte, error) {
		request := scardIORequest{
			Protocol: scardDWORD(card.protocol),
			Length:   scardDWORD(unsafe.Sizeof(scardIORequest{})),
		}
		response := make([]byte, maxAPDUResponseSize)
		size := scardDWORD(len(response))
		result := scardTransmit(
			card.handle,
			&request,
			byteSlicePointer(apdu),
			scardDWORD(len(apdu)),
			nil,
			byteSlicePointer(response),
			&size,
		)
		runtime.KeepAlive(apdu)

		if err := scardError("SCardTransmit", result); err != nil {
			return nil, err
		}

		return response[:min(int(size), len(response))], nil
	})
}

// Control sends a reader-specific control request using an output buffer of
// responseSize bytes. It does not retry the request because a control operation
// may have side effects.
func (card *Card) Control(
	ctx context.Context,
	controlCode uint32,
	input []byte,
	responseSize int,
) ([]byte, error) {
	if responseSize < 0 || uint64(responseSize) > uint64(^scardDWORD(0)) {
		return nil, fmt.Errorf("%w: control response size %d", ErrInvalidParameter, responseSize)
	}

	if uint64(len(input)) > uint64(^scardDWORD(0)) {
		return nil, fmt.Errorf("%w: control input size %d", ErrInvalidParameter, len(input))
	}

	input = bytes.Clone(input)

	return runCardOperation(card, ctx, func() ([]byte, error) {
		response := make([]byte, responseSize)

		var size scardDWORD

		result := scardControl(
			card.handle,
			scardDWORD(controlCode),
			byteSlicePointer(input),
			scardDWORD(len(input)),
			byteSlicePointer(response),
			scardDWORD(len(response)),
			&size,
		)
		runtime.KeepAlive(input)

		if err := scardError("SCardControl", result); err != nil {
			return nil, err
		}

		if uint64(size) > uint64(len(response)) {
			return nil, fmt.Errorf(
				"%w: SCardControl returned %d bytes for a %d-byte buffer",
				ErrInsufficientBuffer,
				size,
				len(response),
			)
		}

		return response[:int(size)], nil
	})
}

// GetAttribute returns a reader or card attribute.
func (card *Card) GetAttribute(attribute Attribute) ([]byte, error) {
	_ = card.lockOperation(context.Background())
	defer card.unlockOperation()

	if card.isClosed() {
		return nil, ErrClosed
	}

	var size scardDWORD

	result := scardGetAttrib(card.handle, scardDWORD(attribute), 0, &size)
	if result != 0 && uint32(result) != scardEInsufficientBuf {
		return nil, scardError("SCardGetAttrib", result)
	}

	if size == 0 {
		return nil, nil
	}

	value := make([]byte, size)

	result = scardGetAttrib(card.handle, scardDWORD(attribute), byteSlicePointer(value), &size)
	if err := scardError("SCardGetAttrib", result); err != nil {
		return nil, err
	}

	return value[:min(int(size), len(value))], nil
}

// SetAttribute sets a reader or card attribute.
func (card *Card) SetAttribute(attribute Attribute, value []byte) error {
	_ = card.lockOperation(context.Background())
	defer card.unlockOperation()

	if card.isClosed() {
		return ErrClosed
	}

	result := scardSetAttrib(
		card.handle,
		scardDWORD(attribute),
		byteSlicePointer(value),
		scardDWORD(len(value)),
	)
	runtime.KeepAlive(value)

	return scardError("SCardSetAttrib", result)
}

// Close prevents new operations, asks the native implementation to cancel an
// in-flight card operation when supported, waits for it to finish, disconnects
// from the card, and releases its PC/SC context.
func (card *Card) Close() error {
	card.closeOnce.Do(func() {
		card.stateMu.Lock()
		card.closed = true
		cancelErr := cancelCardOperation(card.nativeContext)
		card.stateMu.Unlock()

		if errors.Is(cancelErr, ErrCanceled) {
			cancelErr = nil
		}

		_ = card.lockOperation(context.Background())
		defer card.unlockOperation()

		card.closeErr = errors.Join(
			cancelErr,
			scardError(
				"SCardDisconnect",
				scardDisconnect(card.handle, scardDWORD(card.disconnectDisposition)),
			),
			releaseNativeContext(card.nativeContext),
		)
	})

	return card.closeErr
}

func byteSlicePointer(value []byte) uintptr {
	if len(value) == 0 {
		return 0
	}

	return uintptr(unsafe.Pointer(unsafe.SliceData(value)))
}
