package main

import "fmt"

// usageError wraps an error to signal that it should map to exit code 2.
// Use this for invalid arguments, missing flags, and other caller mistakes.
type usageError struct {
	err error
}

func (e *usageError) Error() string {
	return e.err.Error()
}

func (e *usageError) Unwrap() error {
	return e.err
}

// newUsageErrorf creates a usageError with formatted text.
func newUsageErrorf(format string, args ...any) error {
	return &usageError{err: fmt.Errorf(format, args...)}
}
