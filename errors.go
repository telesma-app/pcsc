package pcsc

import (
	"errors"
	"fmt"
)

var (
	// ErrUnavailable indicates that the platform PC/SC runtime could not be loaded.
	ErrUnavailable = errors.New("pcsc: unavailable")

	ErrInternalError          = errors.New("pcsc: internal error")
	ErrCanceled               = errors.New("pcsc: canceled")
	ErrInvalidHandle          = errors.New("pcsc: invalid handle")
	ErrInvalidParameter       = errors.New("pcsc: invalid parameter")
	ErrInvalidTarget          = errors.New("pcsc: invalid target")
	ErrNoMemory               = errors.New("pcsc: not enough memory")
	ErrWaitedTooLong          = errors.New("pcsc: waited too long")
	ErrTimeout                = errors.New("pcsc: timeout")
	ErrInsufficientBuffer     = errors.New("pcsc: insufficient buffer")
	ErrUnknownReader          = errors.New("pcsc: unknown reader")
	ErrNoCard                 = errors.New("pcsc: no smart card")
	ErrUnknownCard            = errors.New("pcsc: unknown card")
	ErrCannotDispose          = errors.New("pcsc: cannot dispose")
	ErrProtocolMismatch       = errors.New("pcsc: protocol mismatch")
	ErrNotReady               = errors.New("pcsc: not ready")
	ErrInvalidValue           = errors.New("pcsc: invalid value")
	ErrSystemCanceled         = errors.New("pcsc: canceled by system")
	ErrCommunication          = errors.New("pcsc: communication error")
	ErrUnknownError           = errors.New("pcsc: unknown internal error")
	ErrInvalidATR             = errors.New("pcsc: invalid ATR")
	ErrNotTransacted          = errors.New("pcsc: not transacted")
	ErrReaderUnavailable      = errors.New("pcsc: reader unavailable")
	ErrShutdown               = errors.New("pcsc: shutdown")
	ErrPCITooSmall            = errors.New("pcsc: PCI buffer too small")
	ErrReaderUnsupported      = errors.New("pcsc: reader unsupported")
	ErrDuplicateReader        = errors.New("pcsc: duplicate reader")
	ErrCardUnsupported        = errors.New("pcsc: card unsupported")
	ErrCardReset              = errors.New("pcsc: card reset")
	ErrCardRemoved            = errors.New("pcsc: card removed")
	ErrSharingViolation       = errors.New("pcsc: sharing violation")
	ErrNoService              = errors.New("pcsc: no service")
	ErrServiceStopped         = errors.New("pcsc: service stopped")
	ErrUnexpected             = errors.New("pcsc: unexpected error")
	ErrICCInstallation        = errors.New("pcsc: ICC installation unavailable")
	ErrICCCreationOrder       = errors.New("pcsc: ICC creation order unsupported")
	ErrUnsupportedFeature     = errors.New("pcsc: unsupported feature")
	ErrDirectoryNotFound      = errors.New("pcsc: directory not found")
	ErrFileNotFound           = errors.New("pcsc: file not found")
	ErrNotDirectory           = errors.New("pcsc: path is not a directory")
	ErrNotFile                = errors.New("pcsc: path is not a file")
	ErrNoAccess               = errors.New("pcsc: access denied")
	ErrWriteTooMany           = errors.New("pcsc: insufficient card memory")
	ErrBadSeek                = errors.New("pcsc: bad seek")
	ErrInvalidCHV             = errors.New("pcsc: invalid CHV")
	ErrUnknownResourceManager = errors.New("pcsc: unknown resource manager error")
	ErrNoSuchCertificate      = errors.New("pcsc: certificate not found")
	ErrCertificateUnavailable = errors.New("pcsc: certificate unavailable")
	ErrNoReaders              = errors.New("pcsc: no readers available")
	ErrCommunicationDataLost  = errors.New("pcsc: communication data lost")
	ErrNoKeyContainer         = errors.New("pcsc: key container not found")
	ErrServerTooBusy          = errors.New("pcsc: server too busy")
	ErrUnsupportedCard        = errors.New("pcsc: unsupported card")
	ErrUnresponsiveCard       = errors.New("pcsc: unresponsive card")
	ErrUnpoweredCard          = errors.New("pcsc: unpowered card")
	ErrSecurityViolation      = errors.New("pcsc: security violation")
	ErrWrongCHV               = errors.New("pcsc: wrong CHV")
	ErrCHVBlocked             = errors.New("pcsc: CHV blocked")
	ErrEndOfFile              = errors.New("pcsc: end of file")
	ErrCanceledByUser         = errors.New("pcsc: canceled by user")
	ErrCardNotAuthenticated   = errors.New("pcsc: card not authenticated")
	ErrClosed                 = errors.New("pcsc: card closed")
)

// Error is a PC/SC return code annotated with the operation which failed.
type Error struct {
	Operation string
	Code      uint32
}

func (e *Error) Error() string {
	if info, ok := errorInfoForCode(e.Code); ok {
		return fmt.Sprintf("pcsc: %s: %s (0x%08x)", e.Operation, info.name, e.Code)
	}

	return fmt.Sprintf("pcsc: %s failed (0x%08x)", e.Operation, e.Code)
}

func (e *Error) Is(target error) bool {
	info, ok := errorInfoForCode(e.Code)

	return ok && target != nil && (target == info.err || target == info.alias)
}

func pcscError(operation string, code uint32) error {
	if code == 0 {
		return nil
	}

	return &Error{Operation: operation, Code: code}
}

type errorInfo struct {
	name string
	err  error
	// alias is used for the pcsc-lite collision between
	// SCARD_E_UNEXPECTED and SCARD_E_UNSUPPORTED_FEATURE.
	alias error
}

func errorInfoForCode(code uint32) (errorInfo, bool) {
	info, ok := errorsByCode[code]
	if !ok {
		info, ok = platformErrorsByCode[code]
	}

	if code == unsupportedFeatureCode {
		info.name = "unsupported feature"
		info.alias = info.err
		info.err = ErrUnsupportedFeature
		ok = true
	}

	return info, ok
}

var errorsByCode = map[uint32]errorInfo{
	0x80100001: {name: "internal error", err: ErrInternalError},
	0x80100002: {name: "canceled", err: ErrCanceled},
	0x80100003: {name: "invalid handle", err: ErrInvalidHandle},
	0x80100004: {name: "invalid parameter", err: ErrInvalidParameter},
	0x80100005: {name: "invalid target", err: ErrInvalidTarget},
	0x80100006: {name: "not enough memory", err: ErrNoMemory},
	0x80100007: {name: "waited too long", err: ErrWaitedTooLong},
	0x80100008: {name: "insufficient buffer", err: ErrInsufficientBuffer},
	0x80100009: {name: "unknown reader", err: ErrUnknownReader},
	0x8010000a: {name: "timeout", err: ErrTimeout},
	0x8010000b: {name: "sharing violation", err: ErrSharingViolation},
	0x8010000c: {name: "no smart card", err: ErrNoCard},
	0x8010000d: {name: "unknown card", err: ErrUnknownCard},
	0x8010000e: {name: "cannot dispose", err: ErrCannotDispose},
	0x8010000f: {name: "protocol mismatch", err: ErrProtocolMismatch},
	0x80100010: {name: "not ready", err: ErrNotReady},
	0x80100011: {name: "invalid value", err: ErrInvalidValue},
	0x80100012: {name: "canceled by system", err: ErrSystemCanceled},
	0x80100013: {name: "communication error", err: ErrCommunication},
	0x80100014: {name: "unknown internal error", err: ErrUnknownError},
	0x80100015: {name: "invalid ATR", err: ErrInvalidATR},
	0x80100016: {name: "not transacted", err: ErrNotTransacted},
	0x80100017: {name: "reader unavailable", err: ErrReaderUnavailable},
	0x80100018: {name: "shutdown", err: ErrShutdown},
	0x80100019: {name: "PCI buffer too small", err: ErrPCITooSmall},
	0x8010001a: {name: "reader unsupported", err: ErrReaderUnsupported},
	0x8010001b: {name: "duplicate reader", err: ErrDuplicateReader},
	0x8010001c: {name: "card unsupported", err: ErrCardUnsupported},
	0x8010001d: {name: "no service", err: ErrNoService},
	0x8010001e: {name: "service stopped", err: ErrServiceStopped},
	0x8010001f: {name: "unexpected error", err: ErrUnexpected},
	0x80100020: {name: "ICC installation unavailable", err: ErrICCInstallation},
	0x80100021: {name: "ICC creation order unsupported", err: ErrICCCreationOrder},
	0x80100023: {name: "directory not found", err: ErrDirectoryNotFound},
	0x80100024: {name: "file not found", err: ErrFileNotFound},
	0x80100025: {name: "path is not a directory", err: ErrNotDirectory},
	0x80100026: {name: "path is not a file", err: ErrNotFile},
	0x80100027: {name: "access denied", err: ErrNoAccess},
	0x80100028: {name: "insufficient card memory", err: ErrWriteTooMany},
	0x80100029: {name: "bad seek", err: ErrBadSeek},
	0x8010002a: {name: "invalid CHV", err: ErrInvalidCHV},
	0x8010002b: {name: "unknown resource manager error", err: ErrUnknownResourceManager},
	0x8010002c: {name: "certificate not found", err: ErrNoSuchCertificate},
	0x8010002d: {name: "certificate unavailable", err: ErrCertificateUnavailable},
	0x8010002e: {name: "no readers available", err: ErrNoReaders},
	0x8010002f: {name: "communication data lost", err: ErrCommunicationDataLost},
	0x80100030: {name: "key container not found", err: ErrNoKeyContainer},
	0x80100031: {name: "server too busy", err: ErrServerTooBusy},
	0x80100065: {name: "unsupported card", err: ErrUnsupportedCard},
	0x80100066: {name: "unresponsive card", err: ErrUnresponsiveCard},
	0x80100067: {name: "unpowered card", err: ErrUnpoweredCard},
	0x80100068: {name: "card reset", err: ErrCardReset},
	0x80100069: {name: "card removed", err: ErrCardRemoved},
	0x8010006a: {name: "security violation", err: ErrSecurityViolation},
	0x8010006b: {name: "wrong CHV", err: ErrWrongCHV},
	0x8010006c: {name: "CHV blocked", err: ErrCHVBlocked},
	0x8010006d: {name: "end of file", err: ErrEndOfFile},
	0x8010006e: {name: "canceled by user", err: ErrCanceledByUser},
	0x8010006f: {name: "card not authenticated", err: ErrCardNotAuthenticated},
}
