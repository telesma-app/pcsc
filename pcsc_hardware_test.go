//go:build windows || linux || darwin

package pcsc

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"os"
	"testing"
)

const hardwareTestEnv = "PCSC_TEST_CTAPNFC"
const lifecycleHardwareTestEnv = "PCSC_TEST"

var (
	// FIDO application AID, as assigned to the FIDO Alliance.
	selectFIDOApplet = []byte{
		0x00, 0xa4, 0x04, 0x00, 0x08,
		0xa0, 0x00, 0x00, 0x06, 0x47, 0x2f, 0x00, 0x01,
		0x00,
	}
	// NFCCTAP_MSG carrying authenticatorGetInfo (0x04). P1=0x80 advertises
	// support for NFCCTAP_GETRESPONSE status polling.
	getInfoAPDU = []byte{0x80, 0x10, 0x80, 0x00, 0x01, 0x04, 0x00}
)

func TestPCSCLifecycle(t *testing.T) {
	if os.Getenv(lifecycleHardwareTestEnv) != "1" {
		t.Skip("set " + lifecycleHardwareTestEnv + "=1 to run the hardware test")
	}

	var readers []*ReaderInfo

	for reader, err := range Enumerate() {
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}

		readers = append(readers, reader)
	}

	if len(readers) == 0 {
		t.Fatal("no PC/SC readers found")
	}

	receiver, err := Watch()
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	connected := make(map[string]bool, len(readers))

	for _, reader := range receiver.Snapshot().Readers {
		t.Logf(
			"snapshot reader=%q state=0x%x ATR=%x",
			reader.Name,
			reader.State,
			reader.ATR,
		)
		connected[reader.Name] = true
	}

	if len(connected) != len(readers) {
		t.Fatalf("initial snapshot reported %d of %d readers", len(connected), len(readers))
	}

	if err := receiver.Close(); err != nil {
		t.Errorf("close event receiver: %v", err)
	}

	var opened int

	for _, reader := range readers {
		card, err := Open(reader.Name)
		if errors.Is(err, ErrNoCard) {
			t.Logf("reader %q has no card", reader.Name)
			continue
		}

		if err != nil {
			t.Errorf("Open(%q): %v", reader.Name, err)
			continue
		}

		opened++

		status, err := card.Status()
		if err != nil {
			t.Errorf("Status(%q): %v", reader.Name, err)
		} else {
			t.Logf("readers=%q protocol=%d state=0x%x ATR=%x", status.ReaderNames, status.Protocol, status.State, status.ATR)

			atr, attributeErr := card.GetAttribute(AttributeATRString)
			if attributeErr != nil {
				t.Errorf("GetAttribute(ATR, %q): %v", reader.Name, attributeErr)
			} else if !bytes.Equal(atr, status.ATR) {
				t.Errorf("attribute ATR = %x, status ATR = %x", atr, status.ATR)
			}
		}

		if err := card.BeginTransaction(t.Context()); err != nil {
			t.Errorf("BeginTransaction(%q): %v", reader.Name, err)
		} else if err := card.EndTransaction(DispositionLeaveCard); err != nil {
			t.Errorf("EndTransaction(%q): %v", reader.Name, err)
		}

		if err := card.Close(); err != nil {
			t.Errorf("Close(%q): %v", reader.Name, err)
		}
	}

	if opened == 0 {
		t.Fatal("no card could be opened")
	}
}

func TestCTAPNFC(t *testing.T) {
	if os.Getenv(hardwareTestEnv) != "1" {
		t.Skip("set " + hardwareTestEnv + "=1 to run the hardware test")
	}

	var readers []*ReaderInfo

	for reader, err := range Enumerate() {
		if err != nil {
			t.Fatalf("Enumerate: %v", err)
		}

		readers = append(readers, reader)
	}

	if len(readers) == 0 {
		t.Fatal("no PC/SC readers found")
	}

	var tested int

	for _, reader := range readers {
		card, err := Open(reader.Name)
		if errors.Is(err, ErrNoCard) {
			t.Logf("reader %q has no card", reader.Name)
			continue
		}

		if err != nil {
			t.Errorf("Open(%q): %v", reader.Name, err)
			continue
		}

		if testCTAPNFCCard(t, reader, card) {
			tested++
		}

		if err := card.Close(); err != nil {
			t.Errorf("Close(%q): %v", reader.Name, err)
		}
	}

	if tested == 0 {
		t.Fatal("no FIDO card found; present a CTAP authenticator to a PC/SC reader")
	}
}

func testCTAPNFCCard(t *testing.T, reader *ReaderInfo, card *Card) bool {
	t.Helper()
	ctx := t.Context()

	status, err := card.Status()
	if err != nil {
		t.Errorf("Status(%q): %v", reader.Name, err)
		return false
	}

	t.Logf("reader=%q status_readers=%q protocol=%d state=0x%x ATR=%s",
		reader.Name, status.ReaderNames, status.Protocol, status.State, hex.EncodeToString(status.ATR))

	if len(status.ReaderNames) == 0 {
		t.Errorf("Status(%q) returned an empty reader name", reader.Name)
	}

	if len(status.ATR) == 0 {
		t.Errorf("Status(%q) returned an empty ATR", reader.Name)
	}

	if status.Protocol != ProtocolT0 && status.Protocol != ProtocolT1 {
		t.Errorf("Status(%q) protocol = %d, want T=0 or T=1", reader.Name, status.Protocol)
	}

	selected, err := exchangeISOResponse(ctx, card, selectFIDOApplet)
	if err != nil {
		t.Errorf("SELECT FIDO applet on %q: %v", reader.Name, err)
		return false
	}

	if !hasStatusWord(selected, 0x90, 0x00) {
		t.Logf("reader %q is not a FIDO card: SELECT response=%x", reader.Name, selected)
		return false
	}

	response, err := exchangeISOResponse(ctx, card, getInfoAPDU)
	if err != nil {
		t.Errorf("authenticatorGetInfo on %q: %v", reader.Name, err)
		return true
	}

	for hasStatusWord(response, 0x91, 0x00) {
		response, err = exchangeISOResponse(ctx, card, []byte{0x80, 0x11, 0x00, 0x00, 0x00})
		if err != nil {
			t.Errorf("NFCCTAP_GETRESPONSE on %q: %v", reader.Name, err)
			return true
		}
	}

	if !hasStatusWord(response, 0x90, 0x00) {
		t.Errorf("authenticatorGetInfo on %q response = %x, want SW=9000", reader.Name, response)
		return true
	}

	payload := response[:len(response)-2]
	if len(payload) < 2 {
		t.Errorf("authenticatorGetInfo on %q payload = %x, want CTAP status and CBOR body", reader.Name, payload)
		return true
	}

	if payload[0] != 0 {
		t.Errorf("authenticatorGetInfo on %q CTAP status = 0x%02x, want success", reader.Name, payload[0])
	}
	// A successful authenticatorGetInfo body is a CBOR map (major type 5).
	if payload[1]>>5 != 5 {
		t.Errorf("authenticatorGetInfo on %q body starts with 0x%02x, want a CBOR map", reader.Name, payload[1])
	}

	t.Logf("authenticatorGetInfo response=%x", response)

	return true
}

func exchangeISOResponse(ctx context.Context, card *Card, command []byte) ([]byte, error) {
	response, err := card.Transmit(ctx, command)
	if err != nil {
		return nil, err
	}

	var data []byte

	for {
		if len(response) < 2 {
			return response, nil
		}

		data = append(data, response[:len(response)-2]...)

		sw1, sw2 := response[len(response)-2], response[len(response)-1]
		if sw1 != 0x61 && sw1 != 0x9f {
			return append(data, sw1, sw2), nil
		}

		response, err = card.Transmit(ctx, []byte{command[0], 0xc0, 0x00, 0x00, sw2})
		if err != nil {
			return nil, err
		}
	}
}

func hasStatusWord(response []byte, sw1, sw2 byte) bool {
	return len(response) >= 2 && response[len(response)-2] == sw1 && response[len(response)-1] == sw2
}
