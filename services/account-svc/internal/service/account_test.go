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

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/account-svc/internal/service"
)

// A nil repository is safe in these tests: every case is rejected during
// validation or by the idempotency guard, before the repository is consulted.
func newServer() *service.AccountServer {
	return service.NewAccountServer(nil, idempotency.NewMemoryStore(), zerolog.New(io.Discard))
}

func serverWithStore(store idempotency.Store) *service.AccountServer {
	return service.NewAccountServer(nil, store, zerolog.New(io.Discard))
}

func money(amount, currency string) *commonv1.Money {
	return &commonv1.Money{Amount: amount, Currency: currency}
}

func TestCreateAccount_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *accountv1.CreateAccountRequest
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			req:     &accountv1.CreateAccountRequest{OwnerId: "o1", Currency: "INR"},
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing owner",
			req:     &accountv1.CreateAccountRequest{IdempotencyKey: "k", Currency: "INR"},
			wantMsg: "owner_id required",
		},
		{
			name:    "missing currency",
			req:     &accountv1.CreateAccountRequest{IdempotencyKey: "k", OwnerId: "o1"},
			wantMsg: "currency required",
		},
		{
			name:    "unsupported currency",
			req:     &accountv1.CreateAccountRequest{IdempotencyKey: "k", OwnerId: "o1", Currency: "XYZ"},
			wantMsg: "not supported",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newServer().CreateAccount(context.Background(), tc.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestPlaceHold_Validation(t *testing.T) {
	t.Parallel()

	valid := func() *accountv1.PlaceHoldRequest {
		return &accountv1.PlaceHoldRequest{
			IdempotencyKey: "k",
			AccountId:      "a1",
			ReferenceId:    "txn-1",
			Amount:         money("100.00", "INR"),
			TtlSeconds:     3600,
		}
	}

	tests := []struct {
		name    string
		mutate  func(*accountv1.PlaceHoldRequest)
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.IdempotencyKey = "" },
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing account",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.AccountId = "" },
			wantMsg: "account_id required",
		},
		{
			// The reference is the durable dedup key for the reservation; without
			// it a retry reserves the caller's money a second time.
			name:    "missing reference",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.ReferenceId = "" },
			wantMsg: "reference_id required",
		},
		{
			name:    "nil amount",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.Amount = nil },
			wantMsg: "amount required",
		},
		{
			name:    "missing currency",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.Amount = money("100.00", "") },
			wantMsg: "currency is required",
		},
		{
			name:    "negative amount",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.Amount = money("-1.00", "INR") },
			wantMsg: "must be positive",
		},
		{
			name:    "sub-minor precision",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.Amount = money("1.005", "INR") },
			wantMsg: "finer precision",
		},
		{
			// An unbounded TTL plus a crashed saga is indistinguishable from
			// confiscating the caller's funds.
			name:    "ttl beyond the maximum",
			mutate:  func(r *accountv1.PlaceHoldRequest) { r.TtlSeconds = 365 * 24 * 3600 },
			wantMsg: "ttl_seconds must not exceed",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := valid()
			tc.mutate(req)
			_, err := newServer().PlaceHold(context.Background(), req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestCreditAndDebit_RequireReferenceForIdempotency(t *testing.T) {
	t.Parallel()

	t.Run("credit", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().CreditAccount(context.Background(), &accountv1.CreditAccountRequest{
			IdempotencyKey: "k",
			AccountId:      "a1",
			Amount:         money("10.00", "INR"),
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "reference_id required")
	})

	t.Run("debit", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().DebitAccount(context.Background(), &accountv1.DebitAccountRequest{
			IdempotencyKey: "k",
			AccountId:      "a1",
			Amount:         money("10.00", "INR"),
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "reference_id required")
	})
}

func TestCreditAndDebit_RejectMalformedAmounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		amount  *commonv1.Money
		wantMsg string
	}{
		{name: "zero", amount: money("0.00", "INR"), wantMsg: "must be positive"},
		{name: "negative", amount: money("-5.00", "INR"), wantMsg: "must be positive"},
		{name: "unsupported currency", amount: money("5.00", "XYZ"), wantMsg: "not supported"},
		{name: "sub-minor precision", amount: money("5.001", "INR"), wantMsg: "finer precision"},
		{name: "fractional yen", amount: money("5.5", "JPY"), wantMsg: "finer precision"},
		{name: "unparseable", amount: money("abc", "INR"), wantMsg: "invalid decimal"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run("credit/"+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newServer().CreditAccount(context.Background(), &accountv1.CreditAccountRequest{
				IdempotencyKey: "k", AccountId: "a1", ReferenceId: "r1", Amount: tc.amount,
			})
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

// Every mutating RPC must refuse to run when deduplication cannot be
// guaranteed. With a nil repository, proceeding would panic — so reaching the
// expected Unavailable proves the request stopped at the guard.
func TestMutations_FailClosedWhenIdempotencyStoreIsDown(t *testing.T) {
	t.Parallel()

	t.Run("credit", func(t *testing.T) {
		t.Parallel()
		store := idempotency.NewMemoryStore()
		store.FailNextClaim(assert.AnError)
		_, err := serverWithStore(store).CreditAccount(context.Background(), &accountv1.CreditAccountRequest{
			IdempotencyKey: "k", AccountId: "a1", ReferenceId: "r1", Amount: money("10.00", "INR"),
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})

	t.Run("debit", func(t *testing.T) {
		t.Parallel()
		store := idempotency.NewMemoryStore()
		store.FailNextClaim(assert.AnError)
		_, err := serverWithStore(store).DebitAccount(context.Background(), &accountv1.DebitAccountRequest{
			IdempotencyKey: "k", AccountId: "a1", ReferenceId: "r1", Amount: money("10.00", "INR"),
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})

	t.Run("place hold", func(t *testing.T) {
		t.Parallel()
		store := idempotency.NewMemoryStore()
		store.FailNextClaim(assert.AnError)
		_, err := serverWithStore(store).PlaceHold(context.Background(), &accountv1.PlaceHoldRequest{
			IdempotencyKey: "k", AccountId: "a1", ReferenceId: "r1",
			Amount: money("10.00", "INR"), TtlSeconds: 60,
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})

	t.Run("create account", func(t *testing.T) {
		t.Parallel()
		store := idempotency.NewMemoryStore()
		store.FailNextClaim(assert.AnError)
		_, err := serverWithStore(store).CreateAccount(context.Background(), &accountv1.CreateAccountRequest{
			IdempotencyKey: "k", OwnerId: "o1", Currency: "INR",
		})
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err))
	})
}

func TestReleaseHold_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *accountv1.ReleaseHoldRequest
		wantMsg string
	}{
		{
			name:    "missing hold id",
			req:     &accountv1.ReleaseHoldRequest{AccountId: "a1"},
			wantMsg: "hold_id required",
		},
		{
			name:    "missing account id",
			req:     &accountv1.ReleaseHoldRequest{HoldId: "h1"},
			wantMsg: "account_id required",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newServer().ReleaseHold(context.Background(), tc.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestListAccounts_Validation(t *testing.T) {
	t.Parallel()

	t.Run("owner required", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().ListAccounts(context.Background(), &accountv1.ListAccountsRequest{})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("malformed page token", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().ListAccounts(context.Background(), &accountv1.ListAccountsRequest{
			OwnerId: "o1", PageToken: "not-a-number",
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "invalid page_token")
	})

	t.Run("negative page token", func(t *testing.T) {
		t.Parallel()
		_, err := newServer().ListAccounts(context.Background(), &accountv1.ListAccountsRequest{
			OwnerId: "o1", PageToken: "-5",
		})
		require.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestGetAccount_Validation(t *testing.T) {
	t.Parallel()

	_, err := newServer().GetAccount(context.Background(), &accountv1.GetAccountRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "account_id required")
}
