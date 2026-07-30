package pcsc

import (
	"bytes"
	"sort"
)

func reconcileDeviceEvents(previous, current map[string]*ReaderInfo) []DeviceEvent {
	nameSet := make(map[string]struct{}, len(previous)+len(current))
	for name := range previous {
		nameSet[name] = struct{}{}
	}
	for name := range current {
		nameSet[name] = struct{}{}
	}

	names := make([]string, 0, len(nameSet))
	for name := range nameSet {
		names = append(names, name)
	}
	sort.Strings(names)

	var events []DeviceEvent
	for _, name := range names {
		previousInfo := previous[name]
		currentInfo := current[name]

		switch {
		case previousInfo == nil && currentInfo != nil:
			events = append(events, newDeviceEvent(DeviceEventReaderConnected, currentInfo))
			if currentInfo.State&ReaderStatePresent != 0 {
				events = append(events, newDeviceEvent(DeviceEventCardInserted, currentInfo))
			}

		case previousInfo != nil && currentInfo == nil:
			if previousInfo.State&ReaderStatePresent != 0 {
				events = append(events, newDeviceEvent(DeviceEventCardRemoved, previousInfo))
			}
			events = append(events, newDeviceEvent(DeviceEventReaderDisconnected, previousInfo))

		case previousInfo != nil && currentInfo != nil:
			wasPresent := previousInfo.State&ReaderStatePresent != 0
			isPresent := currentInfo.State&ReaderStatePresent != 0
			cardChanged := wasPresent && isPresent && !bytes.Equal(previousInfo.ATR, currentInfo.ATR)

			if wasPresent && (!isPresent || cardChanged) {
				events = append(events, newDeviceEvent(DeviceEventCardRemoved, previousInfo))
			}
			if isPresent && (!wasPresent || cardChanged) {
				events = append(events, newDeviceEvent(DeviceEventCardInserted, currentInfo))
			}
		}
	}

	return events
}

func newDeviceEvent(eventType DeviceEventType, info *ReaderInfo) DeviceEvent {
	return DeviceEvent{
		Type:       eventType,
		ReaderInfo: info,
	}
}
