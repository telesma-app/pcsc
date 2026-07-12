//go:build darwin || linux || windows

package pcsc

import (
	"testing"
	"unsafe"
)

func TestSCardIORequestABI(t *testing.T) {
	var request scardIORequest
	if got := unsafe.Sizeof(request); got != 8 {
		t.Fatalf("SCARD_IO_REQUEST size = %d, want 8", got)
	}
	if got := unsafe.Offsetof(request.Protocol); got != 0 {
		t.Fatalf("dwProtocol offset = %d, want 0", got)
	}
	if got := unsafe.Offsetof(request.Length); got != 4 {
		t.Fatalf("cbPciLength offset = %d, want 4", got)
	}
}
