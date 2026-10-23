//go:build windows

package pcsc

import (
	"slices"
	"testing"
)

func TestParseUTF16MultiString(t *testing.T) {
	got := parseUTF16MultiString([]uint16{
		'R', 'e', 'a', 'd', 'e', 'r', ' ', 'A', 0,
		'R', 'e', 'a', 'd', 'e', 'r', ' ', 'B', 0,
		0,
		'j', 'u', 'n', 'k',
	})

	want := []string{"Reader A", "Reader B"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseUTF16MultiString() = %#v, want %#v", got, want)
	}
}

func TestWindowsRawProtocolValue(t *testing.T) {
	if ProtocolRaw != 0x00010000 {
		t.Fatalf("ProtocolRaw = 0x%x, want 0x00010000", ProtocolRaw)
	}

	if ProtocolDefault != 0x80000000 {
		t.Fatalf("ProtocolDefault = 0x%x, want 0x80000000", ProtocolDefault)
	}

	if ProtocolTx != ProtocolT0|ProtocolT1 {
		t.Fatalf("ProtocolTx = 0x%x, want T=0 | T=1", ProtocolTx)
	}
}

func TestWindowsProtocolAttributeValue(t *testing.T) {
	if AttributeProtocolTypes != 0x00030120 {
		t.Fatalf("AttributeProtocolTypes = 0x%x, want 0x00030120", AttributeProtocolTypes)
	}
}

func TestWindowsPerformanceAttributeValues(t *testing.T) {
	if AttributePerformanceNumberOfTransmissions != 0x7ffe0001 {
		t.Fatalf(
			"AttributePerformanceNumberOfTransmissions = 0x%x, want 0x7ffe0001",
			AttributePerformanceNumberOfTransmissions,
		)
	}

	if AttributePerformanceBytesTransmitted != 0x7ffe0002 {
		t.Fatalf(
			"AttributePerformanceBytesTransmitted = 0x%x, want 0x7ffe0002",
			AttributePerformanceBytesTransmitted,
		)
	}

	if AttributePerformanceTransmissionTime != 0x7ffe0003 {
		t.Fatalf(
			"AttributePerformanceTransmissionTime = 0x%x, want 0x7ffe0003",
			AttributePerformanceTransmissionTime,
		)
	}
}

func TestWindowsCardOperationCancellationCallsSCardCancel(t *testing.T) {
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

	if !called {
		t.Fatal("card operation cancellation did not call SCardCancel on Windows")
	}
}
