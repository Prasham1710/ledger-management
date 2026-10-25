package service_test

import (
	"context"
	"io"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/payout-svc/internal/service"
)

// A nil repository and nil clients are safe: every case is rejected during
// validation or by the idempotency guard, before either is used.
func newServer() *service.PayoutServer {
	return service.NewPayoutServer(nil, idempotency.NewMemoryStore(), nil, nil, nil, nil,
		zerolog.New(io.Discard))
}

func validNACHRequest() *payoutv1.InitiatePayoutRequest {
	return &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: "key-1",
		AccountId:      "acc-1",
		Amount:         &commonv1.Money{Amount: "500.00", Currency: "INR"},
		Rail:           payoutv1.PayoutRail_PAYOUT_RAIL_NACH,
		Destination: &payoutv1.BankAccount{
			AccountNumber: "123456789012",
			IfscCode:      "HDFC0001234",
			AccountName:   "Test Beneficiary",
		},
	}
}

func TestInitiatePayout_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*payoutv1.InitiatePayoutRequest)
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.IdempotencyKey = "" },
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing account",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.AccountId = "" },
			wantMsg: "account_id required",
		},
		{
			name:    "nil amount",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.Amount = nil },
			wantMsg: "amount required",
		},
		{
			// Previously only Amount.Amount was checked, so a payout with no
			// currency at all was accepted.
			name: "missing currency",
			mutate: func(r *payoutv1.InitiatePayoutRequest) {
				r.Amount = &commonv1.Money{Amount: "500.00", Currency: ""}
			},
			wantMsg: "currency is required",
		},
		{
			name: "negative amount",
			mutate: func(r *payoutv1.InitiatePayoutRequest) {
				r.Amount = &commonv1.Money{Amount: "-500.00", Currency: "INR"}
			},
			wantMsg: "must be positive",
		},
		{
			name: "sub-minor precision",
			mutate: func(r *payoutv1.InitiatePayoutRequest) {
				r.Amount = &commonv1.Money{Amount: "500.005", Currency: "INR"}
			},
			wantMsg: "finer precision",
		},
		{
			// An unspecified rail previously defaulted silently to NACH, which
			// would route a foreign-currency payout down Indian domestic clearing.
			name:    "unspecified rail",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.Rail = payoutv1.PayoutRail_PAYOUT_RAIL_UNSPECIFIED },
			wantMsg: "rail must be one of",
		},
		{
			name:    "nil destination",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.Destination = nil },
			wantMsg: "destination required",
		},
		{
			// Destination was previously only checked for non-nil, so a payout
			// could be created and the customer debited with no account number.
			name: "empty destination fields",
			mutate: func(r *payoutv1.InitiatePayoutRequest) {
				r.Destination = &payoutv1.BankAccount{}
			},
			wantMsg: "account_name required",
		},
		{
			name:    "NACH without account number",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.Destination.AccountNumber = "" },
			wantMsg: "account_number required for NACH",
		},
		{
			name:    "NACH without IFSC",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.Destination.IfscCode = "" },
			wantMsg: "ifsc_code required for NACH",
		},
		{
			name:    "NACH with malformed IFSC",
			mutate:  func(r *payoutv1.InitiatePayoutRequest) { r.Destination.IfscCode = "NOTANIFSC" },
			wantMsg: "not a valid IFSC",
		},
		{
			// A rail that cannot settle the currency would be rejected by the
			// bank only after the customer had been debited.
			name: "NACH cannot settle USD",
			mutate: func(r *payoutv1.InitiatePayoutRequest) {
				r.Amount = &commonv1.Money{Amount: "500.00", Currency: "USD"}
			},
			wantMsg: "cannot settle USD",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := validNACHRequest()
			tc.mutate(req)

			_, err := newServer().InitiatePayout(context.Background(), req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestInitiatePayout_FailsClosedWhenIdempotencyStoreIsDown(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	store.FailNextClaim(assert.AnError)
	svc := service.NewPayoutServer(nil, store, nil, nil, nil, nil, zerolog.New(io.Discard))

	_, err := svc.InitiatePayout(context.Background(), validNACHRequest())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err),
		"a Redis outage must reject the payout rather than debit unguarded")
}

func TestGetPayout_Validation(t *testing.T) {
	t.Parallel()

	_, err := newServer().GetPayout(context.Background(), &payoutv1.GetPayoutRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "payout_id required")
}

func TestValidateDestination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rail    string
		dest    *payoutv1.BankAccount
		wantErr string
	}{
		{
			name: "valid NACH",
			rail: service.RailNACH,
			dest: &payoutv1.BankAccount{
				AccountNumber: "123456789012",
				IfscCode:      "HDFC0001234",
				AccountName:   "Beneficiary",
			},
		},
		{
			name: "valid SEPA",
			rail: service.RailSEPA,
			dest: &payoutv1.BankAccount{
				Iban:        "DE89370400440532013000",
				AccountName: "Beneficiary",
			},
		},
		{
			name: "SEPA accepts a spaced IBAN",
			rail: service.RailSEPA,
			dest: &payoutv1.BankAccount{
				Iban:        "DE89 3704 0044 0532 0130 00",
				AccountName: "Beneficiary",
			},
		},
		{
			name: "SEPA without IBAN",
			rail: service.RailSEPA,
			dest: &payoutv1.BankAccount{
				AccountNumber: "123456789012",
				AccountName:   "Beneficiary",
			},
			wantErr: "iban required for SEPA",
		},
		{
			name: "SEPA with malformed IBAN",
			rail: service.RailSEPA,
			dest: &payoutv1.BankAccount{
				Iban:        "NOTANIBAN",
				AccountName: "Beneficiary",
			},
			wantErr: "not a valid IBAN",
		},
		{
			name: "valid SWIFT with BIC and IBAN",
			rail: service.RailSWIFT,
			dest: &payoutv1.BankAccount{
				Bic:         "DEUTDEFF",
				Iban:        "DE89370400440532013000",
				AccountName: "Beneficiary",
			},
		},
		{
			name: "valid SWIFT with 11-char BIC and account number",
			rail: service.RailSWIFT,
			dest: &payoutv1.BankAccount{
				Bic:           "DEUTDEFF500",
				AccountNumber: "123456789012",
				AccountName:   "Beneficiary",
			},
		},
		{
			name: "SWIFT without BIC",
			rail: service.RailSWIFT,
			dest: &payoutv1.BankAccount{
				Iban:        "DE89370400440532013000",
				AccountName: "Beneficiary",
			},
			wantErr: "bic required for SWIFT",
		},
		{
			name: "SWIFT without any account identifier",
			rail: service.RailSWIFT,
			dest: &payoutv1.BankAccount{
				Bic:         "DEUTDEFF",
				AccountName: "Beneficiary",
			},
			wantErr: "either destination.iban or destination.account_number required",
		},
		{
			name:    "nil destination",
			rail:    service.RailNACH,
			dest:    nil,
			wantErr: "destination required",
		},
		{
			name:    "unknown rail",
			rail:    "IMPS",
			dest:    &payoutv1.BankAccount{AccountName: "Beneficiary"},
			wantErr: "unsupported rail",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := service.ValidateDestination(tc.rail, tc.dest)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestValidateRailCurrency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		rail     string
		currency string
		wantErr  bool
	}{
		{rail: service.RailNACH, currency: "INR", wantErr: false},
		{rail: service.RailNACH, currency: "USD", wantErr: true},
		{rail: service.RailNACH, currency: "EUR", wantErr: true},
		{rail: service.RailSEPA, currency: "EUR", wantErr: false},
		{rail: service.RailSEPA, currency: "INR", wantErr: true},
		// SWIFT carries most currencies; the sponsor bank enforces the real list.
		{rail: service.RailSWIFT, currency: "USD", wantErr: false},
		{rail: service.RailSWIFT, currency: "JPY", wantErr: false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.rail+"/"+tc.currency, func(t *testing.T) {
			t.Parallel()
			err := service.ValidateRailCurrency(tc.rail, tc.currency)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRailString_RejectsUnspecified(t *testing.T) {
	t.Parallel()

	// Defaulting an unspecified rail to NACH would silently route foreign
	// payouts through Indian domestic clearing.
	_, err := service.RailString(payoutv1.PayoutRail_PAYOUT_RAIL_UNSPECIFIED)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rail must be one of")

	for _, r := range []payoutv1.PayoutRail{
		payoutv1.PayoutRail_PAYOUT_RAIL_NACH,
		payoutv1.PayoutRail_PAYOUT_RAIL_SEPA,
		payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT,
	} {
		got, err := service.RailString(r)
		require.NoError(t, err)
		assert.NotEmpty(t, got)
	}
}
