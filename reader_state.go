//go:build darwin || linux || windows

package pcsc

import (
	"encoding/binary"
	"time"
	"unsafe"
)

type nativeReaderStateLayout struct {
	pointerSize     int
	dwordSize       int
	currentOffset   int
	eventOffset     int
	atrLengthOffset int
	atrOffset       int
	atrSize         int
	stride          int
}

func newNativeReaderStateLayout(atrSize int, packed bool) nativeReaderStateLayout {
	pointerSize := int(unsafe.Sizeof(uintptr(0)))
	dwordSize := int(unsafe.Sizeof(scardDWORD(0)))

	currentOffset := 2 * pointerSize
	eventOffset := currentOffset + dwordSize
	atrLengthOffset := eventOffset + dwordSize
	atrOffset := atrLengthOffset + dwordSize
	rawSize := atrOffset + atrSize

	stride := rawSize
	if !packed {
		alignment := max(pointerSize, dwordSize)
		stride = (rawSize + alignment - 1) &^ (alignment - 1)
	}

	return nativeReaderStateLayout{
		pointerSize:     pointerSize,
		dwordSize:       dwordSize,
		currentOffset:   currentOffset,
		eventOffset:     eventOffset,
		atrLengthOffset: atrLengthOffset,
		atrOffset:       atrOffset,
		atrSize:         atrSize,
		stride:          stride,
	}
}

func (layout nativeReaderStateLayout) encode(states []readerState, namePointers []uintptr) []byte {
	buffer := make([]byte, layout.stride*len(states))

	for index, state := range states {
		entry := buffer[index*layout.stride:]

		putNativeUint(entry[:layout.pointerSize], uint64(namePointers[index]))
		putNativeUint(
			entry[layout.currentOffset:layout.currentOffset+layout.dwordSize],
			uint64(state.currentState),
		)
		putNativeUint(
			entry[layout.eventOffset:layout.eventOffset+layout.dwordSize],
			uint64(state.eventState),
		)

		atrLength := min(len(state.atr), layout.atrSize)
		putNativeUint(
			entry[layout.atrLengthOffset:layout.atrLengthOffset+layout.dwordSize],
			uint64(atrLength),
		)
		copy(entry[layout.atrOffset:layout.atrOffset+layout.atrSize], state.atr[:atrLength])
	}

	return buffer
}

func (layout nativeReaderStateLayout) decode(buffer []byte, states []readerState) {
	for index := range states {
		entry := buffer[index*layout.stride:]

		states[index].eventState = ReaderState(readNativeUint(
			entry[layout.eventOffset : layout.eventOffset+layout.dwordSize],
		))
		atrLength := min(int(readNativeUint(
			entry[layout.atrLengthOffset:layout.atrLengthOffset+layout.dwordSize],
		)), layout.atrSize)
		states[index].atr = entry[layout.atrOffset : layout.atrOffset+atrLength]
	}
}

func putNativeUint(dst []byte, value uint64) {
	switch len(dst) {
	case 4:
		binary.NativeEndian.PutUint32(dst, uint32(value))
	case 8:
		binary.NativeEndian.PutUint64(dst, value)
	default:
		panic("pcsc: unsupported native integer size")
	}
}

func readNativeUint(src []byte) uint64 {
	switch len(src) {
	case 4:
		return uint64(binary.NativeEndian.Uint32(src))
	case 8:
		return binary.NativeEndian.Uint64(src)
	default:
		panic("pcsc: unsupported native integer size")
	}
}

func durationMilliseconds(duration time.Duration) uint32 {
	if duration < 0 {
		return ^uint32(0)
	}
	if duration == 0 {
		return 0
	}

	milliseconds := duration.Milliseconds()
	if milliseconds >= int64(^uint32(0)) {
		// 0xffffffff is reserved by PC/SC for INFINITE. Keep every finite
		// duration finite when converting it to the native DWORD.
		return ^uint32(0) - 1
	}

	return uint32(milliseconds)
}
