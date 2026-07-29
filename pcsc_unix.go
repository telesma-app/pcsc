//go:build darwin || linux

package pcsc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const scardScopeSystem = 2

var (
	scardEstablishContext func(scardDWORD, uintptr, uintptr, *scardContext) scardResult
	scardReleaseContext   func(scardContext) scardResult
	scardListReaders      func(scardContext, uintptr, uintptr, *scardDWORD) scardResult
	scardConnect          func(
		scardContext,
		uintptr,
		scardDWORD,
		scardDWORD,
		*scardHandle,
		*scardDWORD,
	) scardResult
	scardReconnect func(
		scardHandle,
		scardDWORD,
		scardDWORD,
		scardDWORD,
		*scardDWORD,
	) scardResult
	scardDisconnect       func(scardHandle, scardDWORD) scardResult
	scardBeginTransaction func(scardHandle) scardResult
	scardEndTransaction   func(scardHandle, scardDWORD) scardResult
	scardStatus           func(
		scardHandle,
		uintptr,
		*scardDWORD,
		*scardDWORD,
		*scardDWORD,
		uintptr,
		*scardDWORD,
	) scardResult
	scardTransmit func(
		scardHandle,
		*scardIORequest,
		uintptr,
		scardDWORD,
		*scardIORequest,
		uintptr,
		*scardDWORD,
	) scardResult
	scardControl func(
		scardHandle,
		scardDWORD,
		uintptr,
		scardDWORD,
		uintptr,
		scardDWORD,
		*scardDWORD,
	) scardResult
	scardGetAttrib       func(scardHandle, scardDWORD, uintptr, *scardDWORD) scardResult
	scardSetAttrib       func(scardHandle, scardDWORD, uintptr, scardDWORD) scardResult
	scardGetStatusChange func(scardContext, scardDWORD, uintptr, scardDWORD) scardResult
	scardCancel          func(scardContext) scardResult
)

var (
	openNativeLibrary   = purego.Dlopen
	ensureNativeLibrary = sync.OnceValue(loadNativeLibrary)
)

func loadNativeLibrary() error {
	lib, err := openNativeLibrary(pcscLibrary, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return fmt.Errorf("%w: load native library: %w", ErrUnavailable, err)
	}
	purego.RegisterLibFunc(&scardEstablishContext, lib, "SCardEstablishContext")
	purego.RegisterLibFunc(&scardReleaseContext, lib, "SCardReleaseContext")
	purego.RegisterLibFunc(&scardListReaders, lib, "SCardListReaders")
	purego.RegisterLibFunc(&scardConnect, lib, "SCardConnect")
	purego.RegisterLibFunc(&scardReconnect, lib, "SCardReconnect")
	purego.RegisterLibFunc(&scardDisconnect, lib, "SCardDisconnect")
	purego.RegisterLibFunc(&scardBeginTransaction, lib, "SCardBeginTransaction")
	purego.RegisterLibFunc(&scardEndTransaction, lib, "SCardEndTransaction")
	purego.RegisterLibFunc(&scardStatus, lib, "SCardStatus")
	purego.RegisterLibFunc(&scardTransmit, lib, "SCardTransmit")
	purego.RegisterLibFunc(&scardControl, lib, scardControlSymbol)
	purego.RegisterLibFunc(&scardGetAttrib, lib, "SCardGetAttrib")
	purego.RegisterLibFunc(&scardSetAttrib, lib, "SCardSetAttrib")
	purego.RegisterLibFunc(&scardGetStatusChange, lib, "SCardGetStatusChange")
	purego.RegisterLibFunc(&scardCancel, lib, "SCardCancel")

	return nil
}

func establishNativeContext() (scardContext, error) {
	if err := ensureNativeLibrary(); err != nil {
		return 0, err
	}

	var nativeContext scardContext
	err := scardError(
		"SCardEstablishContext",
		scardEstablishContext(scardScopeSystem, 0, 0, &nativeContext),
	)
	if err != nil {
		return 0, err
	}

	return nativeContext, nil
}

func releaseNativeContext(nativeContext scardContext) error {
	return scardError("SCardReleaseContext", scardReleaseContext(nativeContext))
}

func cancelNativeContext(nativeContext scardContext) error {
	return scardError("SCardCancel", scardCancel(nativeContext))
}

// pcsc-lite and Apple's PCSC framework only guarantee that SCardCancel
// interrupts SCardGetStatusChange. Card operations therefore cannot be
// canceled through the native API.
func cancelNativeCardOperation(scardContext) error {
	return nil
}

func listReadersNative(nativeContext scardContext) ([]string, error) {
	var size scardDWORD
	if err := scardError(
		"SCardListReaders",
		scardListReaders(nativeContext, 0, 0, &size),
	); err != nil {
		if errors.Is(err, ErrNoReaders) {
			return nil, nil
		}
		return nil, err
	}
	if size == 0 {
		return nil, nil
	}

	buffer := make([]byte, size)
	code := scardListReaders(
		nativeContext,
		0,
		uintptr(unsafe.Pointer(unsafe.SliceData(buffer))),
		&size,
	)
	if err := scardError("SCardListReaders", code); err != nil {
		return nil, err
	}

	return parseMultiString(buffer[:min(int(size), len(buffer))]), nil
}

func getStatusChangeNative(
	nativeContext scardContext,
	timeout time.Duration,
	states []readerState,
) error {
	names := make([][]byte, len(states))
	namePointers := make([]uintptr, len(states))
	for index, state := range states {
		names[index] = append([]byte(state.name), 0)
		namePointers[index] = uintptr(unsafe.Pointer(unsafe.SliceData(names[index])))
	}

	layout := newNativeReaderStateLayout(scardReaderStateATRSize, scardReaderStatePacked)
	buffer := layout.encode(states, namePointers)
	timeoutMilliseconds := durationMilliseconds(timeout)
	code := scardGetStatusChange(
		nativeContext,
		scardDWORD(timeoutMilliseconds),
		uintptr(unsafe.Pointer(unsafe.SliceData(buffer))),
		scardDWORD(len(states)),
	)
	runtime.KeepAlive(names)

	if err := scardError("SCardGetStatusChange", code); err != nil {
		return err
	}

	layout.decode(buffer, states)

	return nil
}

func parseMultiString(buf []byte) []string {
	var out []string
	for len(buf) > 0 {
		i := bytes.IndexByte(buf, 0)
		if i < 0 {
			i = len(buf)
		}

		if i == 0 {
			break
		}

		out = append(out, string(buf[:i]))

		if i == len(buf) {
			break
		}

		buf = buf[i+1:]
	}
	return out
}

// Open connects to the card in reader. By default it uses shared access,
// negotiates T=0 or T=1, and leaves the card powered when closed.
func Open(reader string, opts ...OpenOption) (*Card, error) {
	options := newOpenOptions(opts...)
	nativeContext, err := establishNativeContext()
	if err != nil {
		return nil, err
	}

	name := append([]byte(reader), 0)
	var handle scardHandle
	var protocol scardDWORD

	code := scardConnect(
		nativeContext,
		uintptr(unsafe.Pointer(unsafe.SliceData(name))),
		scardDWORD(options.shareMode),
		scardDWORD(options.preferredProtocols),
		&handle,
		&protocol,
	)
	runtime.KeepAlive(name)

	if err := scardError("SCardConnect", code); err != nil {
		return nil, errors.Join(err, releaseNativeContext(nativeContext))
	}

	return &Card{
		nativeContext:         nativeContext,
		handle:                handle,
		protocol:              Protocol(uint32(protocol)),
		disconnectDisposition: options.disconnectDisposition,
	}, nil
}

func (card *Card) Status() (*CardStatus, error) {
	_ = card.lockOperation(context.Background())
	defer card.unlockOperation()

	if card.isClosed() {
		return nil, ErrClosed
	}

	var readerLen, atrLen scardDWORD
	var state, protocol scardDWORD

	code := scardStatus(card.handle, 0, &readerLen, &state, &protocol, 0, &atrLen)
	if code != 0 && uint32(code) != scardEInsufficientBuf {
		return nil, scardError("SCardStatus", code)
	}

	reader := make([]byte, readerLen)
	atr := make([]byte, atrLen)

	code = scardStatus(
		card.handle,
		byteSlicePointer(reader),
		&readerLen,
		&state,
		&protocol,
		byteSlicePointer(atr),
		&atrLen,
	)
	if err := scardError("SCardStatus", code); err != nil {
		return nil, err
	}
	return newCardStatus(
		parseMultiString(reader),
		CardState(uint32(state)),
		Protocol(uint32(protocol)),
		atr[:min(int(atrLen), len(atr))],
	), nil
}
