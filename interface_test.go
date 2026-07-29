package pcsc

import (
	"testing"
)

func TestCardInterfaceFromICCType(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
		want  CardInterface
	}{
		{name: "unknown", value: []byte{0}, want: CardInterfaceUnknown},
		{name: "ISO 7816 asynchronous", value: []byte{1}, want: CardInterfaceContact},
		{name: "ISO 7816 synchronous", value: []byte{2}, want: CardInterfaceContact},
		{name: "ISO 14443 A", value: []byte{5}, want: CardInterfaceContactless},
		{name: "ISO 14443 B", value: []byte{6}, want: CardInterfaceContactless},
		{name: "ISO 15693", value: []byte{7}, want: CardInterfaceContactless},
		{name: "unsupported", value: []byte{0xff}, want: CardInterfaceUnknown},
		{name: "empty", want: CardInterfaceUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardInterfaceFromICCType(tt.value); got != tt.want {
				t.Fatalf("interface = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCardInterfaceFromATR(t *testing.T) {
	tests := []struct {
		name string
		atr  []byte
		want CardInterface
	}{
		{
			name: "PCSC contactless mapping",
			atr:  []byte{0x3b, 0x80, 0x80, 0x01, 0x01},
			want: CardInterfaceContactless,
		},
		{
			name: "contact card",
			atr:  []byte{0x3b, 0x90, 0x11, 0x00},
			want: CardInterfaceContact,
		},
		{
			name: "empty",
			want: CardInterfaceUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardInterfaceFromATR(tt.atr); got != tt.want {
				t.Fatalf("interface = %q, want %q", got, tt.want)
			}
		})
	}
}
