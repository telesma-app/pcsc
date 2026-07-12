package pcsc

import (
	"fmt"
	"iter"
)

// ReaderInfo describes a PC/SC reader. Name is the stable identifier accepted by Open.
type ReaderInfo struct {
	Name string
}

// Protocol is the transport protocol negotiated with a card.
type Protocol uint32

const (
	ProtocolUndefined Protocol = 0
	ProtocolT0        Protocol = 1
	ProtocolT1        Protocol = 2
	ProtocolRaw       Protocol = 4
)

// CardStatus is a snapshot of a connected card.
type CardStatus struct {
	Reader   string
	State    uint32
	Protocol Protocol
	ATR      []byte
}

// Card is a connection to a smart card. Transmit sends one raw APDU and returns
// the complete response, including SW1/SW2.
type Card interface {
	Transmit(apdu []byte) ([]byte, error)
	Status() (*CardStatus, error)
	Close() error
}

// Error is a PC/SC return code annotated with the operation which failed.
type Error struct {
	Op   string
	Code uint32
}

func (e *Error) Error() string {
	if name := errorName(e.Code); name != "" {
		return fmt.Sprintf("pcsc: %s: %s (0x%08x)", e.Op, name, e.Code)
	}
	return fmt.Sprintf("pcsc: %s failed (0x%08x)", e.Op, e.Code)
}

func pcscError(op string, code int32) error {
	if code == 0 {
		return nil
	}
	return &Error{Op: op, Code: uint32(code)}
}

func errorName(code uint32) string {
	switch code {
	case 0x80100008:
		return "insufficient buffer"
	case 0x80100009:
		return "unknown reader"
	case 0x8010000c:
		return "no smart card"
	case 0x8010000e:
		return "cannot dispose"
	case 0x8010000f:
		return "protocol mismatch"
	case 0x80100010:
		return "not ready"
	case 0x80100016:
		return "not transacted"
	case 0x80100017:
		return "reader unavailable"
	case 0x8010001d:
		return "no service"
	case 0x8010001e:
		return "service stopped"
	case 0x8010002e:
		return "no readers available"
	case 0x80100068:
		return "card reset"
	case 0x80100069:
		return "card removed"
	}
	return ""
}

// Enumerate returns the currently registered PC/SC readers.
func Enumerate() iter.Seq2[*ReaderInfo, error] { return enumerate() }

// Open connects to the card in reader using shared access and negotiates T=0 or T=1.
func Open(reader string) (Card, error) { return open(reader) }
