# telesma-app/pcsc

[![Go Reference](https://pkg.go.dev/badge/github.com/telesma-app/pcsc.svg)](https://pkg.go.dev/github.com/telesma-app/pcsc)
[![Go](https://github.com/telesma-app/pcsc/actions/workflows/go.yml/badge.svg)](https://github.com/telesma-app/pcsc/actions/workflows/go.yml)

`telesma-app/pcsc` is a cgo-free Go library for PC/SC smart-card readers. It
supports Windows, macOS and Linux.

> [!WARNING]
> This module is under active development. Its public API may change during `v0.x`.

## Support

The package supports:

- reader enumeration and connection events;
- shared, exclusive and direct connections;
- card status and ATR;
- APDU and reader control commands;
- transactions and reconnect;
- reader and card attributes.

It uses the native PC/SC service on each platform:

| Platform | Backend            |
|----------|--------------------|
| Windows  | `winscard.dll`     |
| macOS    | `PCSC.framework`   |
| Linux    | `libpcsclite.so.1` |

The native library is loaded on the first PC/SC operation, not during package
import. If it is not available, the operation returns `pcsc.ErrUnavailable`.
This lets an application keep PC/SC support optional.

## Installation

```sh
go get github.com/telesma-app/pcsc@latest
```

See [`go.mod`](go.mod) for the required Go version.

## Quick start

This example lists all readers and opens the first one:

```go
package main

import (
	"fmt"
	"log"

	"github.com/telesma-app/pcsc"
)

func main() {
	var readerName string

	for reader, err := range pcsc.Enumerate() {
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("reader: %s, state: 0x%x, ATR: %x\n",
			reader.Name, reader.State, reader.ATR)

		if readerName == "" {
			readerName = reader.Name
		}
	}

	if readerName == "" {
		log.Fatal("no PC/SC readers found")
	}

	card, err := pcsc.Open(readerName)
	if err != nil {
		log.Fatal(err)
	}
	defer card.Close()

	status, err := card.Status()
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("protocol: %d, ATR: %x\n", status.Protocol, status.ATR)
}
```

`Open` uses a shared connection, offers T=0 and T=1, and leaves the card
unchanged on `Close`. These settings can be changed with options:

```go
card, err := pcsc.Open(
	readerName,
	pcsc.WithShareMode(pcsc.ShareModeExclusive),
	pcsc.WithPreferredProtocols(pcsc.ProtocolT1),
	pcsc.WithDisconnectDisposition(pcsc.DispositionResetCard),
)
```

In direct mode, the default preferred protocol is `ProtocolUndefined`.

## Card operations

`Transmit` sends one raw APDU. The returned data includes the final SW1 and SW2
status bytes:

```go
response, err := card.Transmit(ctx, []byte{
	0x00, 0x84, 0x00, 0x00, 0x08, // GET CHALLENGE
})
if err != nil {
	log.Fatal(err)
}
fmt.Printf("response: %x\n", response)
```

`Control` sends a reader-specific control command. The last argument is the
expected response-buffer size:

```go
response, err := card.Control(ctx, pcsc.ControlCode(3400), nil, 4096)
```

Control commands are not retried automatically because they may have side
effects.

The package also provides:

- `Status` for reader names, card state, protocol and ATR;
- `BeginTransaction` and `EndTransaction` for exclusive card access;
- `Reconnect` for new connection settings or card reset;
- `GetAttribute` and `SetAttribute` for PC/SC attributes.

## Connection events

`Watch` captures the current reader and card state in an initial snapshot. It
then sends live changes in order:

- `DeviceEventReaderConnected`;
- `DeviceEventReaderDisconnected`;
- `DeviceEventCardInserted`;
- `DeviceEventCardRemoved`.

Each watcher has its own PC/SC context and must be closed:

```go
watcher, err := pcsc.Watch()
if err != nil {
	log.Fatal(err)
}
defer watcher.Close()

for _, reader := range watcher.Snapshot().Readers {
	fmt.Printf("present: %s\n", reader.Name)
}

for event := range watcher.Listen() {
	fmt.Printf("%s: %s\n", event.Type, event.ReaderInfo.Name)
}

if err := watcher.Close(); err != nil {
	log.Printf("PC/SC watcher stopped: %v", err)
}
```

If the watcher stops because of a PC/SC error, `Listen` closes and `Close`
returns that terminal error.

## Transactions

Transactions stop other PC/SC applications from sending commands between your
operations:

```go
if err := card.BeginTransaction(ctx); err != nil {
	log.Fatal(err)
}
defer card.EndTransaction(pcsc.DispositionLeaveCard)
```

A transaction does not provide rollback. The disposition passed to
`EndTransaction` only tells PC/SC what to do with the card.

## Cancellation and concurrency

One `Card` runs one operation at a time. A call waiting for another operation
can be canceled through its context.

On Windows, the package asks PC/SC to cancel an active card operation. On
macOS and Linux, an APDU, control or transaction call may continue in the
background after the Go method returns `ctx.Err()`. The card stays locked until
the native call ends, and `Close` waits for it.

Do not retry a canceled APDU or control command automatically. The card or
reader may already have processed it.

## Errors

PC/SC errors support `errors.Is` with the package error values:

```go
if errors.Is(err, pcsc.ErrNoCard) {
	log.Print("insert a smart card")
}
```

The original PC/SC result is available as `*pcsc.Error`. It contains the failed
operation and the native result code:

```go
var pcscErr *pcsc.Error
if errors.As(err, &pcscErr) {
	log.Printf("%s failed with 0x%08x", pcscErr.Operation, pcscErr.Code)
}
```

Common errors include `ErrUnavailable`, `ErrNoReaders`, `ErrNoCard`,
`ErrCardReset`, `ErrCardRemoved` and `ErrClosed`.

## Platform notes

- `CardState` values are platform-specific. Windows uses an enum. macOS and
  Linux use a bitmask.
- `ProtocolRaw` has a different native value on Windows and Unix systems.
- Direct connections have small platform differences. Use the Go constants
  instead of hard-coded native values.
- Linux needs pcsc-lite, a running `pcscd` service and permission to access the
  reader.

## Scope

This package provides low-level PC/SC access. It does not provide:

- ISO 7816 APDU encoding;
- status-word processing;
- `GET RESPONSE` or command chaining;
- application protocols such as CTAP, PIV or OpenPGP.

Use [`telesma-app/ctap`](https://github.com/telesma-app/ctap) for FIDO2 commands and
[`telesma-app/token2`](https://github.com/telesma-app/token2) for Token2 device support.

## Linting

Run golangci-lint v2 with the standard linters and `wsl_v5`:

```sh
golangci-lint run ./...
```

## Testing

Run the normal test suite without hardware:

```sh
go test ./...
```

The generic hardware test is read-only and works with any connected smart
card:

```sh
PCSC_TEST=1 go test -run TestPCSCLifecycle -v
```

The CTAP-over-NFC test needs a FIDO authenticator connected through a PC/SC
reader:

```sh
PCSC_TEST_CTAPNFC=1 go test -run TestCTAPNFC -v
```

## License

Apache License 2.0. See [`LICENSE`](LICENSE).
