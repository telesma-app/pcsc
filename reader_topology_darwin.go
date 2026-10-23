//go:build darwin

package pcsc

import (
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	ioKitLibrary          = "/System/Library/Frameworks/IOKit.framework/IOKit"
	coreFoundationLibrary = "/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"

	ioUSBHostInterfaceClass  = "IOUSBHostInterface"
	ioPropertyMatchKey       = "IOPropertyMatch"
	ioUSBInterfaceClassKey   = "bInterfaceClass"
	ioFirstMatchNotification = "IOServiceFirstMatch"
	ioTerminatedNotification = "IOServiceTerminate"
	cfRunLoopDefaultModeName = "kCFRunLoopDefaultMode"

	usbChipSmartCardInterfaceClass = 11
	cfNumberSInt32Type             = 3
	cfStringEncodingUTF8           = 0x08000100
	readerRegistrationDelay        = 100 * time.Millisecond
)

var (
	ioNotificationPortCreate           func(uint32) uintptr
	ioNotificationPortDestroy          func(uintptr)
	ioNotificationPortGetRunLoopSource func(uintptr) uintptr
	ioServiceMatching                  func(uintptr) uintptr
	ioServiceAddMatchingNotification   func(uintptr, uintptr, uintptr, uintptr, uintptr, *uint32) int32
	ioIteratorNext                     func(uint32) uint32
	ioObjectRelease                    func(uint32) int32
	cfDictionaryCreateMutable          func(uintptr, int64, uintptr, uintptr) uintptr
	cfDictionarySetValue               func(uintptr, uintptr, uintptr)
	cfNumberCreate                     func(uintptr, int32, uintptr) uintptr
	cfStringCreateWithCString          func(uintptr, uintptr, uint32) uintptr
	cfRelease                          func(uintptr)
	cfRunLoopGetCurrent                func() uintptr
	cfRunLoopAddSource                 func(uintptr, uintptr, uintptr)
	cfRunLoopRemoveSource              func(uintptr, uintptr, uintptr)
	cfRunLoopRun                       func()
	cfRunLoopStop                      func(uintptr)
	cfRunLoopDefaultMode               uintptr
	cfTypeDictionaryKeyCallbacks       uintptr
	cfTypeDictionaryValueCallbacks     uintptr
	loadReaderTopologyNative           = sync.OnceValue(loadReaderTopologyFrameworks)
	readerTopologyMatchingCallback     = purego.NewCallback(handleReaderTopologyMatch)
	readerTopologyRegistry             = struct {
		sync.Mutex
		next       uintptr
		topologies map[uintptr]*darwinReaderTopology
	}{topologies: make(map[uintptr]*darwinReaderTopology)}
)

type darwinReaderTopology struct {
	events  chan error
	stopped chan struct{}
	runLoop uintptr

	mu     sync.Mutex
	closed bool
}

func newReaderTopologyNative() (readerTopology, error) {
	if err := loadReaderTopologyNative(); err != nil {
		return nil, err
	}

	topology := &darwinReaderTopology{
		events:  make(chan error, 1),
		stopped: make(chan struct{}),
	}

	ready := make(chan error)
	go topology.run(ready)

	if err := <-ready; err != nil {
		<-topology.stopped
		return nil, err
	}

	return topology, nil
}

func (t *darwinReaderTopology) Events() <-chan error {
	return t.events
}

func (t *darwinReaderTopology) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()

	for {
		select {
		case <-t.stopped:
			return nil
		default:
		}

		t.mu.Lock()
		if t.runLoop != 0 {
			cfRunLoopStop(t.runLoop)
		}
		t.mu.Unlock()
		runtime.Gosched()
	}
}

func (t *darwinReaderTopology) run(ready chan<- error) {
	runtime.LockOSThread()

	defer runtime.UnlockOSThread()
	defer close(t.stopped)

	readerTopologyRegistry.Lock()
	readerTopologyRegistry.next++
	registry := readerTopologyRegistry.next
	readerTopologyRegistry.topologies[registry] = t

	readerTopologyRegistry.Unlock()
	defer func() {
		readerTopologyRegistry.Lock()
		delete(readerTopologyRegistry.topologies, registry)
		readerTopologyRegistry.Unlock()
	}()

	port := ioNotificationPortCreate(0)
	if port == 0 {
		ready <- fmt.Errorf("%w: create IOKit notification port", ErrUnavailable)
		return
	}

	var arrivals, removals uint32
	defer func() {
		releaseIOObject(arrivals)
		releaseIOObject(removals)
		ioNotificationPortDestroy(port)
	}()

	source := ioNotificationPortGetRunLoopSource(port)
	runLoop := cfRunLoopGetCurrent()

	t.mu.Lock()
	t.runLoop = runLoop
	cfRunLoopAddSource(runLoop, source, cfRunLoopDefaultMode)

	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		cfRunLoopRemoveSource(runLoop, source, cfRunLoopDefaultMode)
		t.runLoop = 0
		t.mu.Unlock()
	}()

	if err := addReaderTopologyNotification(
		port,
		registry,
		ioFirstMatchNotification,
		&arrivals,
	); err != nil {
		ready <- err
		return
	}

	if err := addReaderTopologyNotification(
		port,
		registry,
		ioTerminatedNotification,
		&removals,
	); err != nil {
		ready <- err
		return
	}

	drainReaderTopologyIterator(arrivals)
	drainReaderTopologyIterator(removals)

	ready <- nil

	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()

	if !closed {
		cfRunLoopRun()
	}
}

func addReaderTopologyNotification(
	port uintptr,
	registry uintptr,
	notification string,
	iterator *uint32,
) error {
	matching, err := usbSmartCardInterfaceMatching()
	if err != nil {
		return err
	}

	notificationName := cString(notification)
	code := ioServiceAddMatchingNotification(
		port,
		uintptr(unsafe.Pointer(unsafe.SliceData(notificationName))),
		matching,
		readerTopologyMatchingCallback,
		registry,
		iterator,
	)
	runtime.KeepAlive(notificationName)

	if code != 0 {
		return fmt.Errorf("pcsc: IOKit notification %q failed (0x%08x)", notification, uint32(code))
	}

	return nil
}

func handleReaderTopologyMatch(registry uintptr, iterator uint32) {
	readerTopologyRegistry.Lock()
	topology := readerTopologyRegistry.topologies[registry]
	readerTopologyRegistry.Unlock()

	topology.mu.Lock()
	if topology.closed || !drainReaderTopologyIterator(iterator) {
		topology.mu.Unlock()
		return
	}
	topology.mu.Unlock()

	topology.changed()
}

func (t *darwinReaderTopology) changed() {
	// IOKit observes the USB interface before usbsmartcardreaderd publishes the
	// corresponding PC/SC reader. One delayed wake closes that registration gap.
	time.AfterFunc(readerRegistrationDelay, t.notify)
}

func (t *darwinReaderTopology) notify() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	select {
	case t.events <- nil:
	default:
	}
}

func drainReaderTopologyIterator(iterator uint32) bool {
	var found bool

	for {
		object := ioIteratorNext(iterator)
		if object == 0 {
			return found
		}

		found = true

		ioObjectRelease(object)
	}
}

func usbSmartCardInterfaceMatching() (uintptr, error) {
	className := cString(ioUSBHostInterfaceClass)
	matching := ioServiceMatching(uintptr(unsafe.Pointer(unsafe.SliceData(className))))
	runtime.KeepAlive(className)

	if matching == 0 {
		return 0, fmt.Errorf("%w: create IOKit matching dictionary", ErrUnavailable)
	}

	properties := cfDictionaryCreateMutable(
		0,
		1,
		cfTypeDictionaryKeyCallbacks,
		cfTypeDictionaryValueCallbacks,
	)
	if properties == 0 {
		cfRelease(matching)
		return 0, fmt.Errorf("%w: create IOKit property dictionary", ErrUnavailable)
	}
	defer cfRelease(properties)

	propertyMatchKey := newCFString(ioPropertyMatchKey)
	defer cfRelease(propertyMatchKey)

	interfaceClassKey := newCFString(ioUSBInterfaceClassKey)
	defer cfRelease(interfaceClassKey)

	interfaceClass := int32(usbChipSmartCardInterfaceClass)

	interfaceClassValue := cfNumberCreate(
		0,
		cfNumberSInt32Type,
		uintptr(unsafe.Pointer(&interfaceClass)),
	)
	defer cfRelease(interfaceClassValue)

	cfDictionarySetValue(properties, interfaceClassKey, interfaceClassValue)
	cfDictionarySetValue(matching, propertyMatchKey, properties)

	return matching, nil
}

func newCFString(value string) uintptr {
	buffer := cString(value)
	cfString := cfStringCreateWithCString(
		0,
		uintptr(unsafe.Pointer(unsafe.SliceData(buffer))),
		cfStringEncodingUTF8,
	)
	runtime.KeepAlive(buffer)

	return cfString
}

func cString(value string) []byte {
	return append([]byte(value), 0)
}

func releaseIOObject(object uint32) {
	if object != 0 {
		ioObjectRelease(object)
	}
}

func loadReaderTopologyFrameworks() error {
	ioKit, err := purego.Dlopen(ioKitLibrary, purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return fmt.Errorf("%w: load IOKit: %w", ErrUnavailable, err)
	}

	coreFoundation, err := purego.Dlopen(
		coreFoundationLibrary,
		purego.RTLD_NOW|purego.RTLD_GLOBAL,
	)
	if err != nil {
		return fmt.Errorf("%w: load CoreFoundation: %w", ErrUnavailable, err)
	}

	purego.RegisterLibFunc(&ioNotificationPortCreate, ioKit, "IONotificationPortCreate")
	purego.RegisterLibFunc(&ioNotificationPortDestroy, ioKit, "IONotificationPortDestroy")
	purego.RegisterLibFunc(
		&ioNotificationPortGetRunLoopSource,
		ioKit,
		"IONotificationPortGetRunLoopSource",
	)
	purego.RegisterLibFunc(&ioServiceMatching, ioKit, "IOServiceMatching")
	purego.RegisterLibFunc(
		&ioServiceAddMatchingNotification,
		ioKit,
		"IOServiceAddMatchingNotification",
	)
	purego.RegisterLibFunc(&ioIteratorNext, ioKit, "IOIteratorNext")
	purego.RegisterLibFunc(&ioObjectRelease, ioKit, "IOObjectRelease")
	purego.RegisterLibFunc(
		&cfDictionaryCreateMutable,
		coreFoundation,
		"CFDictionaryCreateMutable",
	)
	purego.RegisterLibFunc(&cfDictionarySetValue, coreFoundation, "CFDictionarySetValue")
	purego.RegisterLibFunc(&cfNumberCreate, coreFoundation, "CFNumberCreate")
	purego.RegisterLibFunc(
		&cfStringCreateWithCString,
		coreFoundation,
		"CFStringCreateWithCString",
	)
	purego.RegisterLibFunc(&cfRelease, coreFoundation, "CFRelease")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, coreFoundation, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopAddSource, coreFoundation, "CFRunLoopAddSource")
	purego.RegisterLibFunc(
		&cfRunLoopRemoveSource,
		coreFoundation,
		"CFRunLoopRemoveSource",
	)
	purego.RegisterLibFunc(&cfRunLoopRun, coreFoundation, "CFRunLoopRun")
	purego.RegisterLibFunc(&cfRunLoopStop, coreFoundation, "CFRunLoopStop")

	cfRunLoopDefaultMode = newCFString(cfRunLoopDefaultModeName)
	if cfRunLoopDefaultMode == 0 {
		return fmt.Errorf("%w: create CoreFoundation run loop mode", ErrUnavailable)
	}

	cfTypeDictionaryKeyCallbacks, err = purego.Dlsym(
		coreFoundation,
		"kCFTypeDictionaryKeyCallBacks",
	)
	if err != nil {
		return fmt.Errorf("%w: load CoreFoundation dictionary callbacks: %w", ErrUnavailable, err)
	}

	cfTypeDictionaryValueCallbacks, err = purego.Dlsym(
		coreFoundation,
		"kCFTypeDictionaryValueCallBacks",
	)
	if err != nil {
		return fmt.Errorf("%w: load CoreFoundation dictionary callbacks: %w", ErrUnavailable, err)
	}

	return nil
}
