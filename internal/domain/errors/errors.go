package errors

import "errors"

var (
	ErrOrderNotFound  = errors.New("order not found")
	ErrDuplicateOrder = errors.New("duplicate order")
)

// RetriableError — consumer should retry this message later.
type RetriableError struct{ Cause error }

func (e *RetriableError) Error() string { return e.Cause.Error() }
func (e *RetriableError) Unwrap() error { return e.Cause }

// NonRetriableError — message goes straight to DLQ.
type NonRetriableError struct{ Cause error }

func (e *NonRetriableError) Error() string { return e.Cause.Error() }
func (e *NonRetriableError) Unwrap() error { return e.Cause }

func IsRetriable(err error) bool {
	var re *RetriableError
	return errors.As(err, &re)
}
