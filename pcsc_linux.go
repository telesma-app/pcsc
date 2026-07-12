//go:build linux

package pcsc

const pcscLibrary = "libpcsclite.so.1"

// pcsc-lite handles are pointer-sized integer tokens on Unix platforms other than macOS.
type scardContext uintptr
type scardHandle uintptr
