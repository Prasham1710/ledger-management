package service

import (
	"fmt"
	"regexp"
	"strings"

	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
)

// Rail identifiers.
const (
	RailNACH  = "NACH"
	RailSEPA  = "SEPA"
	RailSWIFT = "SWIFT"
)

// Beneficiary detail formats. These are shape checks, not authoritative
// validation — the sponsor bank remains the final arbiter — but they stop
// obviously unusable instructions from reaching a settlement file.
var (
	// ifscPattern is the Indian IFSC format: 4-letter bank, '0', 6-char branch.
	ifscPattern = regexp.MustCompile(`^[A-Z]{4}0[A-Z0-9]{6}$`)
	// ibanPattern is the general IBAN shape: 2-letter country, 2 check digits,
	// then up to 30 alphanumerics.
	ibanPattern = regexp.MustCompile(`^[A-Z]{2}[0-9]{2}[A-Z0-9]{11,30}$`)
	// bicPattern is the SWIFT BIC shape: 8 or 11 characters.
	bicPattern = regexp.MustCompile(`^[A-Z]{6}[A-Z0-9]{2}([A-Z0-9]{3})?$`)
	// accountNumberPattern allows digits and dashes, 6-34 characters.
	accountNumberPattern = regexp.MustCompile(`^[0-9-]{6,34}$`)
)

// maxAccountNameLen bounds the beneficiary name written into the bank file.
const maxAccountNameLen = 140

// ValidateDestination checks that the beneficiary details are complete and
// plausible for the chosen rail.
//
// Previously the only check was that Destination was non-nil, so a payout could
// be created and debited with no account number at all — producing a settlement
// file the bank could never act on while the customer's money had already left.
// Each rail needs different fields, so the requirement is rail-specific.
func ValidateDestination(rail string, d *payoutv1.BankAccount) error {
	if d == nil {
		return fmt.Errorf("destination required")
	}

	name := strings.TrimSpace(d.AccountName)
	if name == "" {
		return fmt.Errorf("destination.account_name required")
	}
	if len(name) > maxAccountNameLen {
		return fmt.Errorf("destination.account_name must be at most %d characters", maxAccountNameLen)
	}

	switch rail {
	case RailNACH:
		// Indian domestic clearing: account number plus IFSC.
		acct := strings.TrimSpace(d.AccountNumber)
		if acct == "" {
			return fmt.Errorf("destination.account_number required for NACH")
		}
		if !accountNumberPattern.MatchString(acct) {
			return fmt.Errorf("destination.account_number must be 6-34 digits for NACH")
		}
		ifsc := strings.ToUpper(strings.TrimSpace(d.IfscCode))
		if ifsc == "" {
			return fmt.Errorf("destination.ifsc_code required for NACH")
		}
		if !ifscPattern.MatchString(ifsc) {
			return fmt.Errorf("destination.ifsc_code %q is not a valid IFSC", d.IfscCode)
		}

	case RailSEPA:
		// Euro area: IBAN is mandatory; BIC is optional for SEPA credit transfers.
		iban := normalizeIBAN(d.Iban)
		if iban == "" {
			return fmt.Errorf("destination.iban required for SEPA")
		}
		if !ibanPattern.MatchString(iban) {
			return fmt.Errorf("destination.iban %q is not a valid IBAN", d.Iban)
		}
		if bic := strings.ToUpper(strings.TrimSpace(d.Bic)); bic != "" && !bicPattern.MatchString(bic) {
			return fmt.Errorf("destination.bic %q is not a valid BIC", d.Bic)
		}

	case RailSWIFT:
		// Cross-border: BIC is mandatory, plus either IBAN or an account number.
		bic := strings.ToUpper(strings.TrimSpace(d.Bic))
		if bic == "" {
			return fmt.Errorf("destination.bic required for SWIFT")
		}
		if !bicPattern.MatchString(bic) {
			return fmt.Errorf("destination.bic %q is not a valid BIC", d.Bic)
		}
		iban := normalizeIBAN(d.Iban)
		acct := strings.TrimSpace(d.AccountNumber)
		if iban == "" && acct == "" {
			return fmt.Errorf("either destination.iban or destination.account_number required for SWIFT")
		}
		if iban != "" && !ibanPattern.MatchString(iban) {
			return fmt.Errorf("destination.iban %q is not a valid IBAN", d.Iban)
		}

	default:
		return fmt.Errorf("unsupported rail %q", rail)
	}

	return nil
}

// normalizeIBAN uppercases and strips the spaces IBANs are often printed with.
func normalizeIBAN(iban string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(iban), " ", ""))
}

// RailString converts the proto rail enum to its stored form.
//
// Unlike the previous version this rejects an unspecified rail rather than
// silently defaulting to NACH: routing a euro payout down Indian domestic
// clearing because the field was left blank is not a safe default.
func RailString(r payoutv1.PayoutRail) (string, error) {
	switch r {
	case payoutv1.PayoutRail_PAYOUT_RAIL_NACH:
		return RailNACH, nil
	case payoutv1.PayoutRail_PAYOUT_RAIL_SEPA:
		return RailSEPA, nil
	case payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT:
		return RailSWIFT, nil
	default:
		return "", fmt.Errorf("rail must be one of NACH, SEPA, SWIFT")
	}
}

// RailProto converts a stored rail back to its proto enum.
func RailProto(s string) payoutv1.PayoutRail {
	switch s {
	case RailSEPA:
		return payoutv1.PayoutRail_PAYOUT_RAIL_SEPA
	case RailSWIFT:
		return payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT
	case RailNACH:
		return payoutv1.PayoutRail_PAYOUT_RAIL_NACH
	default:
		return payoutv1.PayoutRail_PAYOUT_RAIL_UNSPECIFIED
	}
}

// railCurrencies constrains which currencies each rail can actually settle.
// A SEPA instruction denominated in rupees would be rejected by the bank after
// the customer's funds had already left.
var railCurrencies = map[string]map[string]bool{
	RailNACH: {"INR": true},
	RailSEPA: {"EUR": true},
	// SWIFT carries most currencies; the sponsor bank enforces the real list.
	RailSWIFT: nil,
}

// ValidateRailCurrency checks that the rail can settle the given currency.
func ValidateRailCurrency(rail, currency string) error {
	allowed, known := railCurrencies[rail]
	if !known {
		return fmt.Errorf("unsupported rail %q", rail)
	}
	if allowed == nil {
		return nil // rail accepts any supported currency
	}
	if !allowed[currency] {
		return fmt.Errorf("rail %s cannot settle %s", rail, currency)
	}
	return nil
}
