//go:build darwin || linux || windows

package pcsc

import (
	"bytes"
	"testing"
	"time"
)

func TestNativeReaderStateLayoutRoundTrip(t *testing.T) {
	layout := newNativeReaderStateLayout(scardReaderStateATRSize, scardReaderStatePacked)
	states := []readerState{
		{
			name:         "reader",
			currentState: ReaderStatePresent,
			eventState:   ReaderStateChanged | ReaderStateInUse,
			atr:          []byte{0x3b, 0x00, 0x01},
		},
	}
	buffer := layout.encode(states, []uintptr{0x1234})
	states[0].eventState = 0
	states[0].atr = nil
	layout.decode(buffer, states)

	if got, want := states[0].eventState, ReaderStateChanged|ReaderStateInUse; got != want {
		t.Fatalf("event state = 0x%x, want 0x%x", got, want)
	}
	if got, want := states[0].atr, []byte{0x3b, 0x00, 0x01}; !bytes.Equal(got, want) {
		t.Fatalf("ATR = %x, want %x", got, want)
	}
}

func TestNativeReaderStateLayoutDoesNotOverlapEntries(t *testing.T) {
	layout := newNativeReaderStateLayout(scardReaderStateATRSize, scardReaderStatePacked)
	states := []readerState{
		{currentState: ReaderStatePresent, eventState: ReaderStatePresent, atr: []byte{1}},
		{currentState: ReaderStateEmpty, eventState: ReaderStateEmpty, atr: []byte{2}},
	}
	buffer := layout.encode(states, []uintptr{1, 2})
	if len(buffer) != 2*layout.stride {
		t.Fatalf("buffer length = %d, want %d", len(buffer), 2*layout.stride)
	}
	layout.decode(buffer, states)
	if states[0].eventState != ReaderStatePresent || states[1].eventState != ReaderStateEmpty {
		t.Fatalf("decoded states = %#v", states)
	}
}

func TestDurationMillisecondsKeepsFiniteTimeoutFinite(t *testing.T) {
	const infinite = ^uint32(0)

	duration := time.Duration(infinite) * time.Millisecond
	if got := durationMilliseconds(duration); got != infinite-1 {
		t.Fatalf("durationMilliseconds(%s) = 0x%x, want 0x%x", duration, got, infinite-1)
	}
}

func TestDurationMillisecondsMapsNegativeDurationToInfinite(t *testing.T) {
	if got := durationMilliseconds(-time.Nanosecond); got != ^uint32(0) {
		t.Fatalf("durationMilliseconds(-1ns) = 0x%x, want PC/SC infinite", got)
	}
}
