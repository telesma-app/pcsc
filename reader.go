//go:build darwin || linux || windows

package pcsc

import (
	"errors"
	"iter"
	"maps"
	"slices"
)

// Enumerate returns the currently registered PC/SC readers and their current
// card state.
func Enumerate() iter.Seq2[*ReaderInfo, error] {
	return func(yield func(*ReaderInfo, error) bool) {
		nativeContext, err := establishNativeContext()
		if err != nil {
			yield(nil, err)
			return
		}

		readers, snapshotErr := readerSnapshot(nativeContext, nil)
		if err := errors.Join(snapshotErr, releaseNativeContext(nativeContext)); err != nil {
			yield(nil, err)
			return
		}

		for _, name := range slices.Sorted(maps.Keys(readers)) {
			if !yield(readers[name], nil) {
				return
			}
		}
	}
}
