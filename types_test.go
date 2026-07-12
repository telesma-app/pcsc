package pcsc

import (
	"reflect"
	"testing"
)

func TestParseMultiString(t *testing.T) {
	got := parseMultiStringForTest([]byte("Reader A\x00Reader B\x00\x00junk"))
	want := []string{"Reader A", "Reader B"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func parseMultiStringForTest(buf []byte) []string {
	var out []string
	for len(buf) > 0 {
		i := 0
		for i < len(buf) && buf[i] != 0 {
			i++
		}
		if i == 0 {
			break
		}
		out = append(out, string(buf[:i]))
		if i == len(buf) {
			break
		}
		buf = buf[i+1:]
	}
	return out
}
