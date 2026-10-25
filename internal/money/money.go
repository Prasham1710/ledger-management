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

// Parse validates and converts an untrusted (amount, currency) pair arriving
// from the wire. It is the single entry point for external money values, and
// enforces every invariant the ledger depends on:
//
//   - currency is a supported ISO 4217 code
//   - amount is a well-formed decimal
//   - amount is strictly positive (the ledger encodes direction with
//     DEBIT/CREDIT, never with a sign)
//   - amount carries no precision finer than the currency's minor unit, so
//     sub-minor residue cannot enter the system undetected
//
// Callers should surface the returned error as an invalid-argument response.
func Parse(amount, currency string) (Amount, error) {
	if currency == "" {
		return Amount{}, fmt.Errorf("money.Parse: currency is required")
	}
	cur := Currency(currency)
	if err := cur.Validate(); err != nil {
		return Amount{}, fmt.Errorf("money.Parse: %w", err)
	}
	if amount == "" {
		return Amount{}, fmt.Errorf("money.Parse: amount is required")
	}
	a, err := New(amount, cur)
	if err != nil {
		return Amount{}, err
	}
	if !a.IsPositive() {
		return Amount{}, fmt.Errorf("money.Parse: amount must be positive, got %s", a.value.String())
	}
	if !a.IsQuantized() {
		return Amount{}, fmt.Errorf(
			"money.Parse: amount %s has finer precision than %s allows (%d decimal places)",
			a.value.String(), cur, cur.Scale())
	}
	return a, nil
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

// Div returns a divided by scalar, rounded to StorageScale.
// Returns an error rather than panicking on division by zero.
func (a Amount) Div(scalar decimal.Decimal) (Amount, error) {
	if scalar.IsZero() {
		return Amount{}, fmt.Errorf("money.Div: division by zero")
	}
	return Amount{value: a.value.DivRound(scalar, StorageScale), currency: a.currency}, nil
}

// IsQuantized reports whether a is expressible in its currency's minor unit —
// that is, whether it carries no precision finer than the currency allows.
func (a Amount) IsQuantized() bool {
	return a.value.Equal(a.value.Truncate(a.currency.Scale()))
}

// Quantize rounds a to its currency's minor unit and returns the rounded amount
// alongside the residue that rounding discarded (original minus rounded).
//
// Callers converting between currencies must not drop the residue: summed
// across a batch it is real money, and an unaccounted residue is exactly the
// kind of drift the reconciliation worker exists to catch. Rounding is
// half-away-from-zero, matching the decimal library's Round.
func (a Amount) Quantize() (rounded, residue Amount) {
	r := Amount{value: a.value.Round(a.currency.Scale()), currency: a.currency}
	return r, Amount{value: a.value.Sub(r.value), currency: a.currency}
}

// StringFixed returns the value at StorageScale — the full precision held in
// the database. Use StringMinor for values crossing an API boundary.
func (a Amount) StringFixed() string { return a.value.StringFixed(StorageScale) }

// StringMinor returns the value at its currency's minor-unit scale, e.g.
// "100.00" for INR and "100" for JPY. This is the canonical wire format.
func (a Amount) StringMinor() string { return a.value.StringFixed(a.currency.Scale()) }

// String returns "100.00 INR" style representation at minor-unit scale.
func (a Amount) String() string {
	return fmt.Sprintf("%s %s", a.StringMinor(), a.currency)
}

func (a Amount) requireSameCurrency(b Amount) {
	if a.currency != b.currency {
		panic(fmt.Sprintf("money: currency mismatch: %s vs %s", a.currency, b.currency))
	}
}
