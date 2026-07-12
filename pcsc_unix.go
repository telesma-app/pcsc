//go:build darwin || linux

package pcsc

import (
	"bytes"
	"errors"
	"iter"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
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
	scardEstablishContext func(uint32, uintptr, uintptr, *scardContext) int32
	scardReleaseContext   func(scardContext) int32
	scardListReaders      func(scardContext, uintptr, uintptr, *uint32) int32
	scardConnect          func(scardContext, uintptr, uint32, uint32, *scardHandle, *uint32) int32
	scardDisconnect       func(scardHandle, uint32) int32
	scardStatus           func(scardHandle, uintptr, *uint32, *uint32, *uint32, uintptr, *uint32) int32
	scardTransmit         func(scardHandle, *scardIORequest, uintptr, uint32, *scardIORequest, uintptr, *uint32) int32
)

func init() {
	lib, err := purego.Dlopen(pcscLibrary, purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		panic("pcsc: load native library: " + err.Error())
	}
	purego.RegisterLibFunc(&scardEstablishContext, lib, "SCardEstablishContext")
	purego.RegisterLibFunc(&scardReleaseContext, lib, "SCardReleaseContext")
	purego.RegisterLibFunc(&scardListReaders, lib, "SCardListReaders")
	purego.RegisterLibFunc(&scardConnect, lib, "SCardConnect")
	purego.RegisterLibFunc(&scardDisconnect, lib, "SCardDisconnect")
	purego.RegisterLibFunc(&scardStatus, lib, "SCardStatus")
	purego.RegisterLibFunc(&scardTransmit, lib, "SCardTransmit")
}

func withContext(fn func(scardContext) error) error {
	var ctx scardContext
	if err := pcscError("SCardEstablishContext", scardEstablishContext(scardScopeSystem, 0, 0, &ctx)); err != nil {
		return err
	}
	err := fn(ctx)
	releaseErr := pcscError("SCardReleaseContext", scardReleaseContext(ctx))
	return errors.Join(err, releaseErr)
}

func enumerate() iter.Seq2[*ReaderInfo, error] {
	return func(yield func(*ReaderInfo, error) bool) {
		var names []string
		err := withContext(func(ctx scardContext) error {
			var size uint32
			if err := pcscError("SCardListReaders", scardListReaders(ctx, 0, 0, &size)); err != nil {
				if e := new(Error); errors.As(err, &e) && e.Code == 0x8010002e {
					return nil
				}
				return err
			}
			if size == 0 {
				return nil
			}
			buf := make([]byte, size)
			if err := pcscError("SCardListReaders", scardListReaders(ctx, 0, uintptr(unsafe.Pointer(unsafe.SliceData(buf))), &size)); err != nil {
				return err
			}
			names = parseMultiString(buf[:min(int(size), len(buf))])
			runtime.KeepAlive(buf)
			return nil
		})
		if err != nil {
			yield(nil, err)
			return
		}
		for _, name := range names {
			if !yield(&ReaderInfo{Name: name}, nil) {
				return
			}
		}
	}
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

type card struct {
	mu       sync.Mutex
	context  scardContext
	handle   scardHandle
	protocol Protocol
	closed   bool
}

func open(reader string) (Card, error) {
	if reader == "" {
		return nil, errors.New("pcsc: reader name is empty")
	}
	var ctx scardContext
	if err := pcscError("SCardEstablishContext", scardEstablishContext(scardScopeSystem, 0, 0, &ctx)); err != nil {
		return nil, err
	}
	name := append([]byte(reader), 0)
	var handle scardHandle
	var protocol uint32
	code := scardConnect(ctx, uintptr(unsafe.Pointer(unsafe.SliceData(name))), scardShareShared, scardProtocolAny, &handle, &protocol)
	runtime.KeepAlive(name)
	if err := pcscError("SCardConnect", code); err != nil {
		_ = pcscError("SCardReleaseContext", scardReleaseContext(ctx))
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
		code := scardTransmit(c.handle, &request, uintptr(unsafe.Pointer(unsafe.SliceData(apdu))), uint32(len(apdu)), nil, uintptr(unsafe.Pointer(unsafe.SliceData(response))), &size)
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
	var readerLen, atrLen uint32
	var state, protocol uint32
	code := scardStatus(c.handle, 0, &readerLen, &state, &protocol, 0, &atrLen)
	if code != 0 && uint32(code) != scardEInsufficientBuf {
		return nil, pcscError("SCardStatus", code)
	}
	reader := make([]byte, readerLen)
	atr := make([]byte, atrLen)
	code = scardStatus(c.handle, slicePointer(reader), &readerLen, &state, &protocol, slicePointer(atr), &atrLen)
	runtime.KeepAlive(reader)
	runtime.KeepAlive(atr)
	if err := pcscError("SCardStatus", code); err != nil {
		return nil, err
	}
	return &CardStatus{Reader: firstString(reader), State: state, Protocol: Protocol(protocol), ATR: append([]byte(nil), atr[:min(int(atrLen), len(atr))]...)}, nil
}

func slicePointer(b []byte) uintptr {
	if len(b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.SliceData(b)))
}

func firstString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

func (c *card) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return errors.Join(pcscError("SCardDisconnect", scardDisconnect(c.handle, scardLeaveCard)), pcscError("SCardReleaseContext", scardReleaseContext(c.context)))
}
