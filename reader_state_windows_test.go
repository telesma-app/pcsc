//go:build windows

package pcsc

import (
	"testing"
	"unsafe"
)

func TestWindowsReaderStateABI(t *testing.T) {
	layout := newNativeReaderStateLayout(scardReaderStateATRSize, scardReaderStatePacked)

	wantStride := 56
	if unsafe.Sizeof(uintptr(0)) == 8 {
		wantStride = 64
	}

	if layout.stride != wantStride {
		t.Fatalf("SCARD_READERSTATEW stride = %d, want %d", layout.stride, wantStride)
	}
}
