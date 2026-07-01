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
