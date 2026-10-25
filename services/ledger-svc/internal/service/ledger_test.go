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
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/ledger-svc/internal/service"
)

// These tests cover request validation and the idempotency contract, which are
// the parts of ledger-svc that do not require a database. Repository behaviour
// (currency pinning, the uniqueness constraints, reversal arithmetic) is
// enforced by SQL constraints and is exercised by the integration suite.

func newServer() *service.LedgerServer {
	// A nil repository is safe here: every case below is rejected during
	// validation, before the repository is consulted.
	return service.NewLedgerServer(nil, idempotency.NewMemoryStore(), zerolog.New(io.Discard))
}

func money(amount, currency string) *commonv1.Money {
	return &commonv1.Money{Amount: amount, Currency: currency}
}

func validPostEntry() *ledgerv1.PostEntryRequest {
	return &ledgerv1.PostEntryRequest{
		IdempotencyKey:  "key-1",
		DebitAccountId:  "acct-src",
		CreditAccountId: "acct-dst",
		ReferenceId:     "txn-1",
		Amount:          money("100.00", "INR"),
	}
}

func TestPostEntry_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*ledgerv1.PostEntryRequest)
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.IdempotencyKey = "" },
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing debit account",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.DebitAccountId = "" },
			wantMsg: "debit_account_id required",
		},
		{
			name:    "missing credit account",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.CreditAccountId = "" },
			wantMsg: "credit_account_id required",
		},
		{
			name:    "same account both sides",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.CreditAccountId = r.DebitAccountId },
			wantMsg: "must differ",
		},
		{
			// The reference is the deduplication key for the entry pair. Without
			// it the uniqueness constraint cannot prevent a double post.
			name:    "missing reference id",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.ReferenceId = "" },
			wantMsg: "reference_id required",
		},
		{
			name:    "nil amount",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = nil },
			wantMsg: "amount required",
		},
		{
			name:    "empty amount string",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("", "INR") },
			wantMsg: "amount is required",
		},
		{
			name:    "missing currency",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("100.00", "") },
			wantMsg: "currency is required",
		},
		{
			name:    "unsupported currency",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("100.00", "XYZ") },
			wantMsg: "not supported",
		},
		{
			name:    "unparseable amount",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("not-a-number", "INR") },
			wantMsg: "invalid decimal",
		},
		{
			// Direction is expressed by DEBIT/CREDIT. A signed amount would let
			// a caller invert a transfer.
			name:    "negative amount",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("-100.00", "INR") },
			wantMsg: "must be positive",
		},
		{
			name:    "zero amount",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("0.00", "INR") },
			wantMsg: "must be positive",
		},
		{
			// Sub-minor precision is invisible at the API scale and reappears
			// later as unexplained reconciliation drift.
			name:    "sub-minor-unit precision",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("100.005", "INR") },
			wantMsg: "finer precision",
		},
		{
			name:    "fractional yen",
			mutate:  func(r *ledgerv1.PostEntryRequest) { r.Amount = money("100.5", "JPY") },
			wantMsg: "finer precision",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := validPostEntry()
			tc.mutate(req)

			_, err := newServer().PostEntry(context.Background(), req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestPostEntry_RejectsRequestWhenIdempotencyStoreIsDown(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	store.FailNextClaim(assert.AnError)
	svc := service.NewLedgerServer(nil, store, zerolog.New(io.Discard))

	// With a nil repository, reaching the database would panic. The request must
	// be rejected before that: posting without a working dedup guarantee is how
	// double-spend happens.
	_, err := svc.PostEntry(context.Background(), validPostEntry())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err),
		"a Redis outage must fail the request closed, never post unguarded")
}

func TestReverseEntry_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *ledgerv1.ReverseEntryRequest
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			req:     &ledgerv1.ReverseEntryRequest{ReferenceId: "txn-1"},
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing reference id",
			req:     &ledgerv1.ReverseEntryRequest{IdempotencyKey: "key-1"},
			wantMsg: "reference_id required",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newServer().ReverseEntry(context.Background(), tc.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestGetBalance_Validation(t *testing.T) {
	t.Parallel()

	t.Run("account id required", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().GetBalance(context.Background(), &ledgerv1.GetBalanceRequest{})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "account_id required")
	})

	t.Run("currency must be supported when supplied", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().GetBalance(context.Background(), &ledgerv1.GetBalanceRequest{
			AccountId: "acct-1",
			Currency:  "XYZ",
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "not supported")
	})
}

func TestListEntries_Validation(t *testing.T) {
	t.Parallel()

	t.Run("account id required", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().ListEntries(context.Background(), &ledgerv1.ListEntriesRequest{})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "account_id required")
	})
}
