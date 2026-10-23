//go:build windows

package pcsc

import (
	"errors"
	"testing"
)

func TestWindowsReconnectRejectsDirectAndEject(t *testing.T) {
	if err := validateReconnectParameters(ShareModeDirect, DispositionLeaveCard); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("direct share error = %v, want ErrInvalidValue", err)
	}

	if err := validateReconnectParameters(ShareModeShared, DispositionEjectCard); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("eject error = %v, want ErrInvalidValue", err)
	}
}
