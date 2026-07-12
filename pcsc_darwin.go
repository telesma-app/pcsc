//go:build darwin

package pcsc

const pcscLibrary = "/System/Library/Frameworks/PCSC.framework/PCSC"

// Apple's PCSC framework deliberately uses fixed-width 32-bit handles.
type scardContext int32
type scardHandle int32
