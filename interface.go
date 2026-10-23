package pcsc

// CardInterface identifies how a card is coupled to its reader.
type CardInterface string

const (
	CardInterfaceUnknown     CardInterface = "unknown"
	CardInterfaceContact     CardInterface = "contact"
	CardInterfaceContactless CardInterface = "contactless"
)

// Interface returns the best-effort physical interface of the connected card.
// Drivers that do not expose enough information produce CardInterfaceUnknown.
func (card *Card) Interface() CardInterface {
	value, err := card.GetAttribute(AttributeICCTypePerATR)
	if err == nil {
		if cardInterface := cardInterfaceFromICCType(value); cardInterface != CardInterfaceUnknown {
			return cardInterface
		}
	}

	atr, err := card.GetAttribute(AttributeATRString)
	if err != nil {
		return CardInterfaceUnknown
	}

	return cardInterfaceFromATR(atr)
}

func cardInterfaceFromICCType(value []byte) CardInterface {
	if len(value) == 0 {
		return CardInterfaceUnknown
	}

	switch value[0] {
	case 1, 2:
		return CardInterfaceContact
	case 5, 6, 7:
		return CardInterfaceContactless
	default:
		return CardInterfaceUnknown
	}
}

func cardInterfaceFromATR(atr []byte) CardInterface {
	if len(atr) == 0 {
		return CardInterfaceUnknown
	}

	if isContactlessATR(atr) {
		return CardInterfaceContactless
	}

	return CardInterfaceContact
}

func isContactlessATR(atr []byte) bool {
	return len(atr) >= 5 &&
		atr[0] == 0x3b &&
		atr[1]&0xf0 == 0x80 &&
		atr[2] == 0x80 &&
		atr[3] == 0x01
}
