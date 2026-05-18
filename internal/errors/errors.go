// Package errors defines platform-wide sentinel error types and wrapping helpers.
// All errors must be wrapped with fmt.Errorf("context: %w", err) at every layer boundary.
package errors

import (
	"errors"
	"fmt"
)

// NotFoundError is returned when a requested resource does not exist.
type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s %q not found", e.Resource, e.ID)
}

// ValidationError is returned when input fails domain validation rules.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed on %q: %s", e.Field, e.Message)
}

// ConflictError is returned when an operation conflicts with existing state
// (e.g. duplicate idempotency key with different payload, insufficient balance).
type ConflictError struct {
	Message string
}

func (e *ConflictError) Error() string { return fmt.Sprintf("conflict: %s", e.Message) }

// InternalError wraps an unexpected infrastructure or programming failure.
type InternalError struct {
	Cause error
}

func (e *InternalError) Error() string { return fmt.Sprintf("internal error: %s", e.Cause) }
func (e *InternalError) Unwrap() error { return e.Cause }

// Wrap adds context to err using %w so errors.Is and errors.As still work.
// Returns nil if err is nil.
func Wrap(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

// IsNotFound reports whether err is or wraps a NotFoundError.
func IsNotFound(err error) bool {
	var e *NotFoundError
	return errors.As(err, &e)
}

// IsValidation reports whether err is or wraps a ValidationError.
func IsValidation(err error) bool {
	var e *ValidationError
	return errors.As(err, &e)
}

// IsConflict reports whether err is or wraps a ConflictError.
func IsConflict(err error) bool {
	var e *ConflictError
	return errors.As(err, &e)
}
