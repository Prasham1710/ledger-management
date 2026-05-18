// Package money provides type-safe monetary values and currency codes.
// All arithmetic uses shopspring/decimal — float64 is never used for money.
package money

import "fmt"

// Currency is an ISO 4217 three-letter currency code.
type Currency string

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

// Validate returns an error if c is not a supported ISO 4217 code.
func (c Currency) Validate() error {
	if _, ok := supported[c]; !ok {
		return fmt.Errorf("currency %q is not supported", c)
	}
	return nil
}

// String implements fmt.Stringer.
func (c Currency) String() string { return string(c) }
