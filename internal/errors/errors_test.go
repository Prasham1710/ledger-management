package errors_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/errors"
)

func TestNotFoundError(t *testing.T) {
	t.Parallel()

	err := &errors.NotFoundError{Resource: "wallet", ID: "abc-123"}
	assert.Contains(t, err.Error(), "wallet")
	assert.Contains(t, err.Error(), "abc-123")
}

func TestValidationError(t *testing.T) {
	t.Parallel()

	err := &errors.ValidationError{Field: "amount", Message: "must be positive"}
	assert.Contains(t, err.Error(), "amount")
	assert.Contains(t, err.Error(), "must be positive")
}

func TestConflictError(t *testing.T) {
	t.Parallel()

	err := &errors.ConflictError{Message: "insufficient balance"}
	assert.Contains(t, err.Error(), "insufficient balance")
}

func TestInternalErrorUnwrap(t *testing.T) {
	t.Parallel()

	cause := fmt.Errorf("db timeout")
	err := &errors.InternalError{Cause: cause}
	assert.ErrorIs(t, err, cause)
}

func TestWrap(t *testing.T) {
	t.Parallel()

	original := &errors.NotFoundError{Resource: "account", ID: "x"}
	wrapped := errors.Wrap(original, "service.GetAccount")

	require.Error(t, wrapped)
	assert.Contains(t, wrapped.Error(), "service.GetAccount")
	assert.True(t, errors.IsNotFound(wrapped), "IsNotFound must unwrap through Wrap")
}

func TestWrapNil(t *testing.T) {
	t.Parallel()
	assert.NoError(t, errors.Wrap(nil, "any context"))
}

func TestTypeCheckers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		checker func(error) bool
		want    bool
	}{
		{
			name:    "IsNotFound true",
			err:     errors.Wrap(&errors.NotFoundError{Resource: "x", ID: "1"}, "ctx"),
			checker: errors.IsNotFound,
			want:    true,
		},
		{
			name:    "IsNotFound false for ValidationError",
			err:     &errors.ValidationError{Field: "f", Message: "m"},
			checker: errors.IsNotFound,
			want:    false,
		},
		{
			name:    "IsValidation true",
			err:     errors.Wrap(&errors.ValidationError{Field: "f", Message: "m"}, "ctx"),
			checker: errors.IsValidation,
			want:    true,
		},
		{
			name:    "IsConflict true",
			err:     errors.Wrap(&errors.ConflictError{Message: "dup"}, "ctx"),
			checker: errors.IsConflict,
			want:    true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.checker(tc.err))
		})
	}
}
