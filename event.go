package pcsc

import "sync"

// DeviceEvent describes a PC/SC reader or card-presence change.
type DeviceEvent struct {
	Type       DeviceEventType
	ReaderInfo *ReaderInfo
}

type DeviceEventType string

const (
	DeviceEventReaderConnected    DeviceEventType = "reader-connected"
	DeviceEventReaderDisconnected DeviceEventType = "reader-disconnected"
	DeviceEventCardInserted       DeviceEventType = "card-inserted"
	DeviceEventCardRemoved        DeviceEventType = "card-removed"
)

// Snapshot is the initial reader state captured by Watch.
type Snapshot struct {
	Readers []*ReaderInfo
}

// Watcher publishes reader and card changes which happen after Snapshot.
type Watcher interface {
	Snapshot() Snapshot
	Listen() <-chan DeviceEvent
	Close() error
}

// deviceEventQueue decouples the native PC/SC wait from event consumers.
// Send only retains the event and wakes the forwarding goroutine; it never
// waits for a consumer to receive from Listen.
type deviceEventQueue struct {
	mu      sync.Mutex
	pending []DeviceEvent

	out     chan DeviceEvent
	wake    chan struct{}
	done    chan struct{}
	stopped chan struct{}
}

func newDeviceEventQueue() *deviceEventQueue {
	q := &deviceEventQueue{
		out:     make(chan DeviceEvent),
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go q.run()

	return q
}

// Send enqueues an event without waiting for a listener. It reports false once
// closing has started; calls racing with Close are safe.
func (q *deviceEventQueue) Send(event DeviceEvent) bool {
	q.mu.Lock()
	select {
	case <-q.done:
		q.mu.Unlock()
		return false
	default:
	}

	q.pending = append(q.pending, event)
	q.mu.Unlock()

	select {
	case q.wake <- struct{}{}:
	default:
	}

	return true
}

func (q *deviceEventQueue) Listen() <-chan DeviceEvent {
	return q.out
}

// Close stops accepting events and cancels delivery blocked on an absent
// consumer. It waits for out to close, but never waits for out to drain.
func (q *deviceEventQueue) Close() {
	q.mu.Lock()
	select {
	case <-q.done:
	default:
		q.pending = nil
		close(q.done)
	}
	q.mu.Unlock()

	<-q.stopped
}

func (q *deviceEventQueue) run() {
	defer close(q.stopped)
	defer close(q.out)

	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.mu.Unlock()

			select {
			case <-q.wake:
				continue
			case <-q.done:
				return
			}
		}

		event := q.pending[0]

		q.pending[0] = DeviceEvent{}
		if len(q.pending) == 1 {
			q.pending = nil
		} else {
			q.pending = q.pending[1:]
		}
		q.mu.Unlock()

		select {
		case q.out <- event:
		case <-q.done:
			return
		}
	}
}
