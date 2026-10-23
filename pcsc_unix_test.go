//go:build darwin || linux

package pcsc

import (
	"slices"
	"testing"
)

func TestParseMultiString(t *testing.T) {
	got := parseMultiString([]byte("Reader A\x00Reader B\x00\x00junk"))

	want := []string{"Reader A", "Reader B"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseMultiString() = %#v, want %#v", got, want)
	}
}

func TestUnixRawProtocolValue(t *testing.T) {
	if ProtocolRaw != 0x0004 {
		t.Fatalf("ProtocolRaw = 0x%x, want 0x0004", ProtocolRaw)
	}

	if ProtocolAny != ProtocolT0|ProtocolT1 {
		t.Fatalf("ProtocolAny = 0x%x, want T=0 | T=1", ProtocolAny)
	}
}

func TestUnixProtocolAttributeValues(t *testing.T) {
	if AttributeAsyncProtocolTypes != 0x00030120 {
		t.Fatalf("AttributeAsyncProtocolTypes = 0x%x, want 0x00030120", AttributeAsyncProtocolTypes)
	}

	if AttributeSyncProtocolTypes != 0x00030126 {
		t.Fatalf("AttributeSyncProtocolTypes = 0x%x, want 0x00030126", AttributeSyncProtocolTypes)
	}
}

func TestUnixControlSymbolIsConfigured(t *testing.T) {
	if scardControlSymbol == "" {
		t.Fatal("SCardControl symbol is empty")
	}
}

func TestUnixCardOperationCancellationDoesNotCallSCardCancel(t *testing.T) {
	original := scardCancel

	t.Cleanup(func() { scardCancel = original })

	called := false
	scardCancel = func(scardContext) scardResult {
		called = true
		return 0
	}

	if err := cancelNativeCardOperation(scardContext(1)); err != nil {
		t.Fatalf("cancelNativeCardOperation: %v", err)
	}

	if called {
		t.Fatal("card operation cancellation called SCardCancel on Unix")
	}
}
