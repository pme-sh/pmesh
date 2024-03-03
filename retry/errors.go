package retry

import (
	"context"
	"errors"
)

var ErrDeadlineExceeded = errors.New("deadline exceeded")
var ErrMaxAttemptsExceeded = errors.New("max attempts exceeded")

type nonRetryableError struct {
	error
}

func (err nonRetryableError) Error() string { return err.error.Error() }
func (err nonRetryableError) Unwrap() error { return err.error }

func Disable(err error) error {
	if !Retryable(err) {
		return nil
	}
	return nonRetryableError{err}
}
func Retryable(err error) bool {
	if err == nil || err == context.Canceled {
		return false
	}
	var nr nonRetryableError
	return !errors.As(err, &nr)
}
