package pcsc

import (
	"bytes"
	"maps"
	"slices"
)

func reconcileDeviceEvents(previous, current map[string]*ReaderInfo) []DeviceEvent {
	nameSet := make(map[string]struct{}, len(previous)+len(current))
	for name := range previous {
		nameSet[name] = struct{}{}
	}

	for name := range current {
		nameSet[name] = struct{}{}
	}

	var events []DeviceEvent

	for _, name := range slices.Sorted(maps.Keys(nameSet)) {
		previousInfo, wasConnected := previous[name]
		currentInfo, isConnected := current[name]

		switch {
		case !wasConnected:
			events = append(events, DeviceEvent{Type: DeviceEventReaderConnected, ReaderInfo: currentInfo})
			if currentInfo.State&ReaderStatePresent != 0 {
				events = append(events, DeviceEvent{Type: DeviceEventCardInserted, ReaderInfo: currentInfo})
			}

		case !isConnected:
			if previousInfo.State&ReaderStatePresent != 0 {
				events = append(events, DeviceEvent{Type: DeviceEventCardRemoved, ReaderInfo: previousInfo})
			}

			events = append(events, DeviceEvent{Type: DeviceEventReaderDisconnected, ReaderInfo: previousInfo})

		default:
			wasPresent := previousInfo.State&ReaderStatePresent != 0
			isPresent := currentInfo.State&ReaderStatePresent != 0
			cardChanged := wasPresent && isPresent && !bytes.Equal(previousInfo.ATR, currentInfo.ATR)

			if wasPresent && (!isPresent || cardChanged) {
				events = append(events, DeviceEvent{Type: DeviceEventCardRemoved, ReaderInfo: previousInfo})
			}

			if isPresent && (!wasPresent || cardChanged) {
				events = append(events, DeviceEvent{Type: DeviceEventCardInserted, ReaderInfo: currentInfo})
			}
		}
	}

	return events
}
