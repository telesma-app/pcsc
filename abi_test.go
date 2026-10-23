//go:build darwin || linux || windows

package pcsc

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestSCardIORequestABI(t *testing.T) {
	wordSize := uintptr(4)
	if runtime.GOOS == "linux" {
		wordSize = unsafe.Sizeof(uintptr(0))
	}

	var request scardIORequest
	if got := unsafe.Sizeof(request.Protocol); got != wordSize {
		t.Fatalf("dwProtocol size = %d, want %d", got, wordSize)
	}

	if got := unsafe.Sizeof(request.Length); got != wordSize {
		t.Fatalf("cbPciLength size = %d, want %d", got, wordSize)
	}

	if got, want := unsafe.Sizeof(request), 2*wordSize; got != want {
		t.Fatalf("SCARD_IO_REQUEST size = %d, want %d", got, want)
	}

	if got := unsafe.Offsetof(request.Protocol); got != 0 {
		t.Fatalf("dwProtocol offset = %d, want 0", got)
	}

	if got := unsafe.Offsetof(request.Length); got != wordSize {
		t.Fatalf("cbPciLength offset = %d, want %d", got, wordSize)
	}
}
