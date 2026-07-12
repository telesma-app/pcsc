//go:build windows

package pcsc

import (
	"errors"
	"iter"
	"runtime"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	scardScopeSystem       = 2
	scardShareShared       = 2
	scardLeaveCard         = 0
	scardProtocolAny       = uint32(ProtocolT0 | ProtocolT1)
	scardEInsufficientBuf  = uint32(0x80100008)
	defaultResponseBufSize = 4096
	maxResponseBufSize     = 65538
)

type scardIORequest struct {
	Protocol uint32
	Length   uint32
}

var (
	modWinSCard               = windows.NewLazySystemDLL("winscard.dll")
	procSCardEstablishContext = modWinSCard.NewProc("SCardEstablishContext")
	procSCardReleaseContext   = modWinSCard.NewProc("SCardReleaseContext")
	procSCardListReadersW     = modWinSCard.NewProc("SCardListReadersW")
	procSCardConnectW         = modWinSCard.NewProc("SCardConnectW")
	procSCardDisconnect       = modWinSCard.NewProc("SCardDisconnect")
	procSCardStatusW          = modWinSCard.NewProc("SCardStatusW")
	procSCardTransmit         = modWinSCard.NewProc("SCardTransmit")
)

func callCode(proc *windows.LazyProc, args ...uintptr) int32 {
	r, _, _ := proc.Call(args...)
	return int32(uint32(r))
}

func establishContext() (uintptr, error) {
	var ctx uintptr
	code := callCode(procSCardEstablishContext, scardScopeSystem, 0, 0, uintptr(unsafe.Pointer(&ctx)))
	return ctx, pcscError("SCardEstablishContext", code)
}

func enumerate() iter.Seq2[*ReaderInfo, error] {
	return func(yield func(*ReaderInfo, error) bool) {
		ctx, err := establishContext()
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = callCode(procSCardReleaseContext, ctx) }()
		var size uint32
		code := callCode(procSCardListReadersW, ctx, 0, 0, uintptr(unsafe.Pointer(&size)))
		if uint32(code) == 0x8010002e {
			return
		}
		if err := pcscError("SCardListReadersW", code); err != nil {
			yield(nil, err)
			return
		}
		if size == 0 {
			return
		}
		buf := make([]uint16, size)
		code = callCode(procSCardListReadersW, ctx, 0, uintptr(unsafe.Pointer(unsafe.SliceData(buf))), uintptr(unsafe.Pointer(&size)))
		runtime.KeepAlive(buf)
		if err := pcscError("SCardListReadersW", code); err != nil {
			yield(nil, err)
			return
		}
		for _, name := range parseUTF16MultiString(buf[:min(int(size), len(buf))]) {
			if !yield(&ReaderInfo{Name: name}, nil) {
				return
			}
		}
	}
}

func parseUTF16MultiString(buf []uint16) []string {
	var out []string
	for len(buf) > 0 {
		i := 0
		for i < len(buf) && buf[i] != 0 {
			i++
		}
		if i == 0 {
			break
		}
		out = append(out, string(utf16.Decode(buf[:i])))
		if i == len(buf) {
			break
		}
		buf = buf[i+1:]
	}
	return out
}

type card struct {
	mu       sync.Mutex
	context  uintptr
	handle   uintptr
	protocol Protocol
	closed   bool
}

func open(reader string) (Card, error) {
	if reader == "" {
		return nil, errors.New("pcsc: reader name is empty")
	}
	ctx, err := establishContext()
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(reader)
	if err != nil {
		_ = callCode(procSCardReleaseContext, ctx)
		return nil, err
	}
	var handle uintptr
	var protocol uint32
	code := callCode(procSCardConnectW, ctx, uintptr(unsafe.Pointer(name)), scardShareShared, uintptr(scardProtocolAny), uintptr(unsafe.Pointer(&handle)), uintptr(unsafe.Pointer(&protocol)))
	runtime.KeepAlive(name)
	if err := pcscError("SCardConnectW", code); err != nil {
		_ = callCode(procSCardReleaseContext, ctx)
		return nil, err
	}
	return &card{context: ctx, handle: handle, protocol: Protocol(protocol)}, nil
}

func (c *card) Transmit(apdu []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("pcsc: card closed")
	}
	if len(apdu) == 0 {
		return nil, errors.New("pcsc: APDU is empty")
	}
	if uint64(len(apdu)) > uint64(^uint32(0)) {
		return nil, errors.New("pcsc: APDU is too large")
	}
	request := scardIORequest{Protocol: uint32(c.protocol), Length: uint32(unsafe.Sizeof(scardIORequest{}))}
	response := make([]byte, defaultResponseBufSize)
	for {
		size := uint32(len(response))
		code := callCode(procSCardTransmit, c.handle, uintptr(unsafe.Pointer(&request)), uintptr(unsafe.Pointer(unsafe.SliceData(apdu))), uintptr(len(apdu)), 0, uintptr(unsafe.Pointer(unsafe.SliceData(response))), uintptr(unsafe.Pointer(&size)))
		runtime.KeepAlive(apdu)
		runtime.KeepAlive(response)
		if uint32(code) != scardEInsufficientBuf {
			return response[:min(int(size), len(response))], pcscError("SCardTransmit", code)
		}
		if size <= uint32(len(response)) || size > maxResponseBufSize {
			return nil, pcscError("SCardTransmit", code)
		}
		response = make([]byte, size)
	}
}

func (c *card) Status() (*CardStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("pcsc: card closed")
	}
	var readerLen, atrLen, state, protocol uint32
	code := callCode(procSCardStatusW, c.handle, 0, uintptr(unsafe.Pointer(&readerLen)), uintptr(unsafe.Pointer(&state)), uintptr(unsafe.Pointer(&protocol)), 0, uintptr(unsafe.Pointer(&atrLen)))
	if code != 0 && uint32(code) != scardEInsufficientBuf {
		return nil, pcscError("SCardStatusW", code)
	}
	reader := make([]uint16, readerLen)
	atr := make([]byte, atrLen)
	code = callCode(procSCardStatusW, c.handle, uint16SlicePointer(reader), uintptr(unsafe.Pointer(&readerLen)), uintptr(unsafe.Pointer(&state)), uintptr(unsafe.Pointer(&protocol)), byteSlicePointer(atr), uintptr(unsafe.Pointer(&atrLen)))
	runtime.KeepAlive(reader)
	runtime.KeepAlive(atr)
	if err := pcscError("SCardStatusW", code); err != nil {
		return nil, err
	}
	return &CardStatus{Reader: firstUTF16String(reader), State: state, Protocol: Protocol(protocol), ATR: append([]byte(nil), atr[:min(int(atrLen), len(atr))]...)}, nil
}

func uint16SlicePointer(b []uint16) uintptr {
	if len(b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))
}
func byteSlicePointer(b []byte) uintptr {
	if len(b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))
}
func firstUTF16String(b []uint16) string {
	for i, v := range b {
		if v == 0 {
			b = b[:i]
			break
		}
	}
	return string(utf16.Decode(b))
}

func (c *card) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return errors.Join(pcscError("SCardDisconnect", callCode(procSCardDisconnect, c.handle, scardLeaveCard)), pcscError("SCardReleaseContext", callCode(procSCardReleaseContext, c.context)))
}
