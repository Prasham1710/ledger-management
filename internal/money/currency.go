// Package money provides type-safe monetary values and currency codes.
// All arithmetic uses shopspring/decimal — float64 is never used for money.
package money

import "fmt"

// Currency is an ISO 4217 three-letter currency code.
type Currency string

// Supported ISO 4217 currency codes.
const (
	INR Currency = "INR"
	USD Currency = "USD"
	EUR Currency = "EUR"
	GBP Currency = "GBP"
	SGD Currency = "SGD"
	AED Currency = "AED"
	JPY Currency = "JPY"
)

var supported = map[Currency]struct{}{
	INR: {}, USD: {}, EUR: {}, GBP: {}, SGD: {}, AED: {}, JPY: {},
}

// minorUnits maps each currency to its ISO 4217 minor-unit exponent — the
// number of decimal places in which amounts of that currency are denominated.
// JPY has no minor unit; a fractional yen is not a representable amount.
var minorUnits = map[Currency]int32{
	INR: 2, USD: 2, EUR: 2, GBP: 2, SGD: 2, AED: 2, JPY: 0,
}

// StorageScale is the number of decimal places every money column in Postgres
// is declared with, and the scale at which the ledger performs arithmetic.
//
// It is deliberately wider than any currency's minor unit so that intermediate
// results (notably FX conversions) keep full precision. Amounts crossing a
// service boundary are quantized to their currency's minor unit; the extra
// digits exist so that quantization is explicit and its residue accountable,
// rather than being silently truncated by a narrower column.
const StorageScale int32 = 8

// Scale returns the number of decimal places in which c is denominated.
// Unsupported currencies report the storage scale so callers that skipped
// validation lose no precision.
func (c Currency) Scale() int32 {
	if s, ok := minorUnits[c]; ok {
		return s
	}
	return StorageScale
}

// Validate returns an error if c is not a supported ISO 4217 code.
func (c Currency) Validate() error {
	if _, ok := supported[c]; !ok {
		return fmt.Errorf("currency %q is not supported", c)
	}
	return nil
}

// String implements fmt.Stringer.
func (c Currency) String() string { return string(c) }
