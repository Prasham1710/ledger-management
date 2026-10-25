package service_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/transaction-svc/internal/service"
)

// A nil repository and nil clients are safe here: every case is rejected during
// validation or by the idempotency guard, before either is used.
func newServer() *service.TransactionServer {
	return service.NewTransactionServer(nil, idempotency.NewMemoryStore(), nil, nil, nil,
		time.Hour, zerolog.New(io.Discard))
}

func serverWithStore(store idempotency.Store) *service.TransactionServer {
	return service.NewTransactionServer(nil, store, nil, nil, nil,
		time.Hour, zerolog.New(io.Discard))
}

func validRequest() *txnv1.InitiatePaymentRequest {
	return &txnv1.InitiatePaymentRequest{
		IdempotencyKey:  "key-1",
		SourceAccountId: "acc-1",
		DestAccountId:   "acc-2",
		Amount:          &commonv1.Money{Amount: "100.00", Currency: "INR"},
	}
}

func TestInitiatePayment_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*txnv1.InitiatePaymentRequest)
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			mutate:  func(r *txnv1.InitiatePaymentRequest) { r.IdempotencyKey = "" },
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing source account",
			mutate:  func(r *txnv1.InitiatePaymentRequest) { r.SourceAccountId = "" },
			wantMsg: "source_account_id required",
		},
		{
			name:    "missing destination account",
			mutate:  func(r *txnv1.InitiatePaymentRequest) { r.DestAccountId = "" },
			wantMsg: "dest_account_id required",
		},
		{
			name: "source equals destination",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.DestAccountId = r.SourceAccountId
			},
			wantMsg: "must differ",
		},
		{
			name:    "nil amount",
			mutate:  func(r *txnv1.InitiatePaymentRequest) { r.Amount = nil },
			wantMsg: "amount required",
		},
		{
			name: "empty currency",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.Amount = &commonv1.Money{Amount: "100.00", Currency: ""}
			},
			wantMsg: "currency is required",
		},
		{
			name: "unsupported currency",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.Amount = &commonv1.Money{Amount: "100.00", Currency: "XYZ"}
			},
			wantMsg: "not supported",
		},
		{
			name: "negative amount",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.Amount = &commonv1.Money{Amount: "-100.00", Currency: "INR"}
			},
			wantMsg: "must be positive",
		},
		{
			name: "zero amount",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.Amount = &commonv1.Money{Amount: "0", Currency: "INR"}
			},
			wantMsg: "must be positive",
		},
		{
			// Sub-minor precision would be silently rounded somewhere downstream
			// and reappear as reconciliation drift.
			name: "sub-minor precision",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.Amount = &commonv1.Money{Amount: "100.005", Currency: "INR"}
			},
			wantMsg: "finer precision",
		},
		{
			name: "unparseable amount",
			mutate: func(r *txnv1.InitiatePaymentRequest) {
				r.Amount = &commonv1.Money{Amount: "abc", Currency: "INR"}
			},
			wantMsg: "invalid decimal",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := validRequest()
			tc.mutate(req)

			_, err := newServer().InitiatePayment(context.Background(), req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

// The saga must not begin when deduplication cannot be guaranteed. Reaching the
// repository with a nil pool would panic, so an Unavailable proves the request
// stopped at the guard.
func TestInitiatePayment_FailsClosedWhenIdempotencyStoreIsDown(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	store.FailNextClaim(assert.AnError)

	_, err := serverWithStore(store).InitiatePayment(context.Background(), validRequest())
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err),
		"a Redis outage must reject the payment, never run the saga unguarded")
}

// A second concurrent attempt with the same key must not start a second saga.
func TestInitiatePayment_ConcurrentDuplicateIsRejected(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	ctx := context.Background()

	// Simulate an attempt already in flight by taking the claim first.
	claim, err := store.Claim(ctx, "txn:initiate:key-1")
	require.NoError(t, err)
	require.True(t, claim.Acquired)

	_, err = serverWithStore(store).InitiatePayment(ctx, validRequest())
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err),
		"a duplicate while the original is in flight must be told to retry, not run a second saga")
}

func TestGetTransaction_Validation(t *testing.T) {
	t.Parallel()

	_, err := newServer().GetTransaction(context.Background(), &txnv1.GetTransactionRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "transaction_id required")
}

func TestGetTransactionTrace_Validation(t *testing.T) {
	t.Parallel()

	_, err := newServer().GetTransactionTrace(context.Background(), &txnv1.GetTransactionTraceRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), "transaction_id required")
}
