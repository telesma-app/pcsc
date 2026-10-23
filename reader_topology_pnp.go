//go:build linux || windows

package pcsc

import (
	"errors"
	"runtime"
	"sync"
)

const pnpNotificationReader = `\\?PnP?\Notification`

type pnpReaderTopology struct {
	events        chan error
	nativeContext scardContext
	done          chan struct{}
	stopped       chan struct{}

	closeErr error

	closeOnce sync.Once
}

func newReaderTopologyNative() (readerTopology, error) {
	nativeContext, err := establishNativeContext()
	if err != nil {
		return nil, err
	}

	states := []readerState{{name: pnpNotificationReader}}

	err = getStatusChange(nativeContext, 0, states)
	if err != nil && !errors.Is(err, ErrTimeout) {
		return nil, errors.Join(err, releaseNativeContext(nativeContext))
	}

	topology := &pnpReaderTopology{
		events:        make(chan error, 1),
		nativeContext: nativeContext,
		done:          make(chan struct{}),
		stopped:       make(chan struct{}),
	}
	go topology.run(states[0].eventState &^ ReaderStateChanged)

	return topology, nil
}

func (t *pnpReaderTopology) Events() <-chan error {
	return t.events
}

func (t *pnpReaderTopology) Close() error {
	t.closeOnce.Do(func() {
		close(t.done)

		var cancelErr error

		for {
			select {
			case <-t.stopped:
				t.closeErr = errors.Join(
					cancelErr,
					releaseNativeContext(t.nativeContext),
				)

				return
			default:
			}

			err := cancelNativeContext(t.nativeContext)
			if err != nil && !errors.Is(err, ErrCanceled) && cancelErr == nil {
				cancelErr = err
			}

			runtime.Gosched()
		}
	})

	return t.closeErr
}

func (t *pnpReaderTopology) run(current ReaderState) {
	defer close(t.stopped)

	for {
		states := []readerState{{
			name:         pnpNotificationReader,
			currentState: current,
			eventState:   current,
		}}
		err := getStatusChange(t.nativeContext, statusChangeInfinite, states)

		select {
		case <-t.done:
			return
		default:
		}

		if err != nil {
			select {
			case t.events <- err:
			case <-t.done:
			}

			return
		}

		current = states[0].eventState &^ ReaderStateChanged

		select {
		case t.events <- nil:
		default:
		}
	}
}
