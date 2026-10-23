//go:build linux

package pcsc

import (
	"testing"
	"unsafe"
)

func TestLinuxReaderStateABI(t *testing.T) {
	layout := newNativeReaderStateLayout(scardReaderStateATRSize, scardReaderStatePacked)
	pointerSize := int(unsafe.Sizeof(uintptr(0)))
	dwordSize := int(unsafe.Sizeof(scardDWORD(0)))
	rawSize := 2*pointerSize + 3*dwordSize + 33
	alignment := max(pointerSize, dwordSize)

	wantStride := (rawSize + alignment - 1) &^ (alignment - 1)
	if layout.stride != wantStride {
		t.Fatalf("SCARD_READERSTATE stride = %d, want %d", layout.stride, wantStride)
	}
}
