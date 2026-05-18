package money

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// Amount is an immutable monetary value bound to a Currency.
// Construct with New or MustNew — never create the zero value directly.
type Amount struct {
	value    decimal.Decimal
	currency Currency
}

// New parses value as a decimal string and returns an Amount in currency.
func New(value string, currency Currency) (Amount, error) {
	d, err := decimal.NewFromString(value)
	if err != nil {
		return Amount{}, fmt.Errorf("money.New: invalid decimal %q: %w", value, err)
	}
	return Amount{value: d, currency: currency}, nil
}

// MustNew panics if value is not a valid decimal string.
// Intended for constants and tests only.
func MustNew(value string, currency Currency) Amount {
	a, err := New(value, currency)
	if err != nil {
		panic(err)
	}
	return a
}

// FromDecimal wraps an existing decimal.Decimal with a currency.
func FromDecimal(d decimal.Decimal, currency Currency) Amount {
	return Amount{value: d, currency: currency}
}

// Zero returns the zero Amount for currency.
func Zero(currency Currency) Amount {
	return Amount{value: decimal.Zero, currency: currency}
}

// Value returns the raw decimal.Decimal.
func (a Amount) Value() decimal.Decimal { return a.value }

// Currency returns the ISO 4217 currency code.
func (a Amount) Currency() Currency { return a.currency }

// IsZero reports whether the monetary value is exactly zero.
func (a Amount) IsZero() bool { return a.value.IsZero() }

// IsPositive reports whether the value is greater than zero.
func (a Amount) IsPositive() bool { return a.value.IsPositive() }

// IsNegative reports whether the value is less than zero.
func (a Amount) IsNegative() bool { return a.value.IsNegative() }

// Add returns a + b. Panics on currency mismatch.
func (a Amount) Add(b Amount) Amount {
	a.requireSameCurrency(b)
	return Amount{value: a.value.Add(b.value), currency: a.currency}
}

// Sub returns a - b. Panics on currency mismatch.
func (a Amount) Sub(b Amount) Amount {
	a.requireSameCurrency(b)
	return Amount{value: a.value.Sub(b.value), currency: a.currency}
}

// Mul returns a multiplied by scalar. Currency is preserved.
func (a Amount) Mul(scalar decimal.Decimal) Amount {
	return Amount{value: a.value.Mul(scalar), currency: a.currency}
}

// Neg returns the arithmetic negation of a.
func (a Amount) Neg() Amount {
	return Amount{value: a.value.Neg(), currency: a.currency}
}

// Equal reports whether a and b have the same currency and value.
func (a Amount) Equal(b Amount) bool {
	return a.currency == b.currency && a.value.Equal(b.value)
}

// GreaterThan reports whether a > b. Panics on currency mismatch.
func (a Amount) GreaterThan(b Amount) bool {
	a.requireSameCurrency(b)
	return a.value.GreaterThan(b.value)
}

// LessThan reports whether a < b. Panics on currency mismatch.
func (a Amount) LessThan(b Amount) bool {
	a.requireSameCurrency(b)
	return a.value.LessThan(b.value)
}

// StringFixed returns the value as a string with exactly 4 decimal places.
func (a Amount) StringFixed() string { return a.value.StringFixed(4) }

// String returns "100.0000 INR" style representation.
func (a Amount) String() string {
	return fmt.Sprintf("%s %s", a.value.StringFixed(4), a.currency)
}

func (a Amount) requireSameCurrency(b Amount) {
	if a.currency != b.currency {
		panic(fmt.Sprintf("money: currency mismatch: %s vs %s", a.currency, b.currency))
	}
}
