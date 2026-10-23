package pcsc

// ReaderInfo describes a PC/SC reader snapshot. Name is the reader name
// accepted by Open for this snapshot.
type ReaderInfo struct {
	Name  string
	State ReaderState
	ATR   []byte
}

// Protocol is the transport protocol negotiated with a card.
type Protocol uint32

const (
	ProtocolUndefined Protocol = 0
	ProtocolT0        Protocol = 1
	ProtocolT1        Protocol = 2
)

// ShareMode controls how a card connection is shared with other applications.
type ShareMode uint32

const (
	ShareModeExclusive ShareMode = 1
	ShareModeShared    ShareMode = 2
	ShareModeDirect    ShareMode = 3
)

// Disposition controls what PC/SC does with a card when a connection or
// transaction ends.
type Disposition uint32

const (
	DispositionLeaveCard   Disposition = 0
	DispositionResetCard   Disposition = 1
	DispositionUnpowerCard Disposition = 2
	DispositionEjectCard   Disposition = 3
)

// CardState is the state reported for an open card by SCardStatus. PC/SC uses
// platform-specific values for this type.
type CardState uint32

// ReaderState is the state bitmask used by SCardGetStatusChange.
type ReaderState uint32

const (
	ReaderStateUnaware     ReaderState = 0x0000
	ReaderStateIgnore      ReaderState = 0x0001
	ReaderStateChanged     ReaderState = 0x0002
	ReaderStateUnknown     ReaderState = 0x0004
	ReaderStateUnavailable ReaderState = 0x0008
	ReaderStateEmpty       ReaderState = 0x0010
	ReaderStatePresent     ReaderState = 0x0020
	ReaderStateATRMatch    ReaderState = 0x0040
	ReaderStateExclusive   ReaderState = 0x0080
	ReaderStateInUse       ReaderState = 0x0100
	ReaderStateMute        ReaderState = 0x0200
	ReaderStateUnpowered   ReaderState = 0x0400
)

// CardStatus is a snapshot of a connected card.
type CardStatus struct {
	// ReaderNames contains every reader name or alias returned by SCardStatus.
	ReaderNames []string
	State       CardState
	Protocol    Protocol
	ATR         []byte
}

// Attribute identifies a PC/SC reader or card attribute.
type Attribute uint32
