# go-pcsc

Minimal, CGO-free PC/SC access for Go. Windows calls `winscard.dll` through
`golang.org/x/sys/windows`; macOS loads the PCSC framework and Linux loads
`libpcsclite.so.1` at runtime through `purego`.

The package intentionally exposes only the primitives needed by token clients:
reader enumeration, connect/disconnect, status/ATR and raw APDU exchange.

The opt-in hardware test expects a FIDO authenticator presented to a PC/SC
reader:

```sh
PCSC_TEST_CTAPNFC=1 go test -run TestCTAPNFC -v
```

```go
import "github.com/go-ctap/pcsc"

for reader, err := range pcsc.Enumerate() {
	if err != nil {
		log.Fatal(err)
	}

	card, err := pcsc.Open(reader.Name)
	if err != nil {
		continue
	}
	defer card.Close()

	status, err := card.Status()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("reader=%q ATR=%x protocol=%d", status.Reader, status.ATR, status.Protocol)

	// SELECT the Token2 applet seen in token2.pcapng.
	response, err := card.Transmit([]byte{0x00, 0xa4, 0x04, 0x00, 0x07, 0xa0, 0x00, 0x00, 0x05, 0x27, 0x21, 0x01})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("response=%x", response) // response includes SW1/SW2
}
```
