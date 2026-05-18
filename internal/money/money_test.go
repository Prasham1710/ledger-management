package money_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/money"
)

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		currency money.Currency
		wantErr  bool
	}{
		{name: "valid integer", value: "100", currency: money.INR},
		{name: "valid decimal", value: "99.9999", currency: money.USD},
		{name: "zero", value: "0", currency: money.EUR},
		{name: "negative", value: "-50.25", currency: money.INR},
		{name: "invalid string", value: "abc", currency: money.INR, wantErr: true},
		{name: "empty string", value: "", currency: money.INR, wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			a, err := money.New(tc.value, tc.currency)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.currency, a.Currency())
		})
	}
}

func TestArithmetic(t *testing.T) {
	t.Parallel()

	a := money.MustNew("100.0000", money.INR)
	b := money.MustNew("50.0000", money.INR)

	t.Run("Add", func(t *testing.T) {
		t.Parallel()
		result := a.Add(b)
		assert.Equal(t, "150.0000", result.StringFixed())
		assert.Equal(t, money.INR, result.Currency())
	})

	t.Run("Sub", func(t *testing.T) {
		t.Parallel()
		result := a.Sub(b)
		assert.Equal(t, "50.0000", result.StringFixed())
	})

	t.Run("Mul", func(t *testing.T) {
		t.Parallel()
		result := a.Mul(decimal.NewFromFloat(0.02)) // 2% fee
		assert.Equal(t, "2.0000", result.StringFixed())
	})

	t.Run("Neg", func(t *testing.T) {
		t.Parallel()
		result := a.Neg()
		assert.True(t, result.IsNegative())
		assert.Equal(t, "-100.0000", result.StringFixed())
	})
}

func TestComparisons(t *testing.T) {
	t.Parallel()

	a := money.MustNew("100", money.USD)
	b := money.MustNew("200", money.USD)
	z := money.Zero(money.USD)

	assert.True(t, a.LessThan(b))
	assert.True(t, b.GreaterThan(a))
	assert.True(t, z.IsZero())
	assert.False(t, a.IsZero())
	assert.True(t, a.Equal(money.MustNew("100.0000", money.USD)))
	assert.False(t, a.Equal(b))
}

func TestCurrencyMismatchPanics(t *testing.T) {
	t.Parallel()

	a := money.MustNew("100", money.INR)
	b := money.MustNew("100", money.USD)

	assert.Panics(t, func() { a.Add(b) })
	assert.Panics(t, func() { a.Sub(b) })
	assert.Panics(t, func() { a.GreaterThan(b) })
	assert.Panics(t, func() { a.LessThan(b) })
}

func TestString(t *testing.T) {
	t.Parallel()

	a := money.MustNew("100", money.INR)
	assert.Equal(t, "100.0000 INR", a.String())
	assert.Equal(t, "100.0000", a.StringFixed())
}

func TestLedgerZeroSum(t *testing.T) {
	t.Parallel()
	// Core invariant: debit and credit sides of a journal entry must sum to zero.
	debit := money.MustNew("500.0000", money.INR)
	credit := debit.Neg()
	sum := debit.Add(credit)
	assert.True(t, sum.IsZero(), "ledger entries must sum to zero per currency")
}
