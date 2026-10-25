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
		assert.Equal(t, "150.00000000", result.StringFixed())
		assert.Equal(t, money.INR, result.Currency())
	})

	t.Run("Sub", func(t *testing.T) {
		t.Parallel()
		result := a.Sub(b)
		assert.Equal(t, "50.00000000", result.StringFixed())
	})

	t.Run("Mul", func(t *testing.T) {
		t.Parallel()
		result := a.Mul(decimal.NewFromFloat(0.02)) // 2% fee
		assert.Equal(t, "2.00000000", result.StringFixed())
	})

	t.Run("Neg", func(t *testing.T) {
		t.Parallel()
		result := a.Neg()
		assert.True(t, result.IsNegative())
		assert.Equal(t, "-100.00000000", result.StringFixed())
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
	assert.Equal(t, "100.00 INR", a.String(), "String uses minor-unit scale")
	assert.Equal(t, "100.00000000", a.StringFixed(), "StringFixed uses storage scale")
	assert.Equal(t, "100.00", a.StringMinor())

	// JPY has no minor unit — a fractional yen is not representable.
	y := money.MustNew("100", money.JPY)
	assert.Equal(t, "100 JPY", y.String())
	assert.Equal(t, "100", y.StringMinor())
}

func TestParse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		amount   string
		currency string
		wantErr  string
	}{
		{name: "valid two-decimal", amount: "100.50", currency: "INR"},
		{name: "valid integer", amount: "100", currency: "USD"},
		{name: "valid zero-scale currency", amount: "1500", currency: "JPY"},

		{name: "empty currency", amount: "100", currency: "", wantErr: "currency is required"},
		{name: "unsupported currency", amount: "100", currency: "XYZ", wantErr: "not supported"},
		{name: "empty amount", amount: "", currency: "INR", wantErr: "amount is required"},
		{name: "not a number", amount: "abc", currency: "INR", wantErr: "invalid decimal"},

		// Direction is encoded by DEBIT/CREDIT, never by sign.
		{name: "negative rejected", amount: "-100.00", currency: "INR", wantErr: "must be positive"},
		{name: "zero rejected", amount: "0", currency: "INR", wantErr: "must be positive"},

		// Sub-minor-unit precision must not enter the system: it is invisible at
		// the API scale and shows up later as unexplained reconciliation drift.
		{name: "sub-minor precision rejected", amount: "100.005", currency: "INR", wantErr: "finer precision"},
		{name: "fractional yen rejected", amount: "100.5", currency: "JPY", wantErr: "finer precision"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := money.Parse(tc.amount, tc.currency)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, money.Currency(tc.currency), got.Currency())
			assert.True(t, got.IsQuantized())
		})
	}
}

func TestQuantize(t *testing.T) {
	t.Parallel()

	t.Run("exact value has no residue", func(t *testing.T) {
		t.Parallel()
		rounded, residue := money.MustNew("100.25", money.USD).Quantize()
		assert.Equal(t, "100.25", rounded.StringMinor())
		assert.True(t, residue.IsZero())
	})

	t.Run("residue is returned, not discarded", func(t *testing.T) {
		t.Parallel()
		// A typical FX result: 8dp precision that must land on 2dp.
		rounded, residue := money.MustNew("100.12345678", money.USD).Quantize()
		assert.Equal(t, "100.12", rounded.StringMinor())
		// The caller is handed the discarded remainder so it can be accounted for.
		assert.Equal(t, "0.00345678", residue.StringFixed())

		// Rounded + residue must reconstruct the original exactly.
		assert.True(t, rounded.Add(residue).Equal(money.MustNew("100.12345678", money.USD)))
	})

	t.Run("rounds half away from zero", func(t *testing.T) {
		t.Parallel()
		rounded, _ := money.MustNew("100.005", money.USD).Quantize()
		assert.Equal(t, "100.01", rounded.StringMinor())
	})

	t.Run("zero-scale currency", func(t *testing.T) {
		t.Parallel()
		rounded, residue := money.MustNew("1500.75", money.JPY).Quantize()
		assert.Equal(t, "1501", rounded.StringMinor())
		assert.Equal(t, "-0.25000000", residue.StringFixed())
	})
}

func TestDiv(t *testing.T) {
	t.Parallel()

	t.Run("divides at storage scale", func(t *testing.T) {
		t.Parallel()
		got, err := money.MustNew("100", money.USD).Div(decimal.NewFromInt(3))
		require.NoError(t, err)
		assert.Equal(t, "33.33333333", got.StringFixed())
	})

	t.Run("division by zero returns an error rather than panicking", func(t *testing.T) {
		t.Parallel()
		_, err := money.MustNew("100", money.USD).Div(decimal.Zero)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "division by zero")
	})
}

func TestScale(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(2), money.INR.Scale())
	assert.Equal(t, int32(2), money.USD.Scale())
	assert.Equal(t, int32(0), money.JPY.Scale(), "JPY has no minor unit")
	// Storage is wider than every currency's minor unit so FX keeps precision.
	for _, c := range []money.Currency{money.INR, money.USD, money.EUR, money.GBP, money.SGD, money.AED, money.JPY} {
		assert.LessOrEqual(t, c.Scale(), money.StorageScale,
			"storage scale must not lose precision for %s", c)
	}
}

func TestLedgerZeroSum(t *testing.T) {
	t.Parallel()
	// Core invariant: debit and credit sides of a journal entry must sum to zero.
	debit := money.MustNew("500.0000", money.INR)
	credit := debit.Neg()
	sum := debit.Add(credit)
	assert.True(t, sum.IsZero(), "ledger entries must sum to zero per currency")
}
