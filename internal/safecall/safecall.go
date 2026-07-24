package safecall

import "fmt"

// Invoke calls fn and converts any panic into an error.
func Invoke[T any](label string, fn func() (T, error)) (result T, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: panic recovered: %v", label, r)
		}
	}()
	return fn()
}

// Do calls fn and converts any panic into an error. It is Invoke for
// functions that only return an error.
func Do(label string, fn func() error) error {
	_, err := Invoke(label, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

// Recover converts a panic into an error stored in err. It must be deferred
// directly so its recover call runs while the goroutine is unwinding:
//
//	var panicErr error
//	defer safecall.Recover("label", &panicErr)
func Recover(label string, err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("%s: panic recovered: %v", label, r)
	}
}

// GoSafe runs fn in a new goroutine and reports any panic to onPanic instead
// of crashing the process. Deferred functions inside fn run before onPanic,
// so cleanup (closing channels, releasing leases) still happens on panic.
// onPanic itself runs under a recovery guard: a panic it raises is swallowed,
// since GoSafe exists to keep the process alive. A nil onPanic simply exits
// the goroutine after recovery.
func GoSafe(label string, onPanic func(error), fn func()) {
	go func() {
		defer func() {
			r := recover()
			if r == nil || onPanic == nil {
				return
			}
			err := fmt.Errorf("%s: panic recovered: %v", label, r)
			defer func() { _ = recover() }()
			onPanic(err)
		}()
		fn()
	}()
}
