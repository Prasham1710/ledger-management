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
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/fx-svc/internal/service"
)

// A nil repository is safe: every case is rejected during validation or by the
// idempotency guard, before the repository is consulted.
func newServer() *service.FXServer {
	return service.NewFXServer(nil, idempotency.NewMemoryStore(), 30*time.Second,
		zerolog.New(io.Discard))
}

func TestGetQuote_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *fxv1.GetQuoteRequest
		wantMsg string
	}{
		{
			name:    "missing from currency",
			req:     &fxv1.GetQuoteRequest{ToCurrency: "INR", SourceAmount: &commonv1.Money{Amount: "100.00"}},
			wantMsg: "from_currency required",
		},
		{
			name:    "missing to currency",
			req:     &fxv1.GetQuoteRequest{FromCurrency: "USD", SourceAmount: &commonv1.Money{Amount: "100.00"}},
			wantMsg: "to_currency required",
		},
		{
			name: "same currency",
			req: &fxv1.GetQuoteRequest{
				FromCurrency: "USD", ToCurrency: "USD",
				SourceAmount: &commonv1.Money{Amount: "100.00"},
			},
			wantMsg: "must differ",
		},
		{
			name:    "nil amount",
			req:     &fxv1.GetQuoteRequest{FromCurrency: "USD", ToCurrency: "INR"},
			wantMsg: "source_amount required",
		},
		{
			name: "unsupported source currency",
			req: &fxv1.GetQuoteRequest{
				FromCurrency: "XYZ", ToCurrency: "INR",
				SourceAmount: &commonv1.Money{Amount: "100.00"},
			},
			wantMsg: "not supported",
		},
		{
			name: "unsupported target currency",
			req: &fxv1.GetQuoteRequest{
				FromCurrency: "USD", ToCurrency: "XYZ",
				SourceAmount: &commonv1.Money{Amount: "100.00"},
			},
			wantMsg: "not supported",
		},
		{
			name: "negative amount",
			req: &fxv1.GetQuoteRequest{
				FromCurrency: "USD", ToCurrency: "INR",
				SourceAmount: &commonv1.Money{Amount: "-100.00"},
			},
			wantMsg: "must be positive",
		},
		{
			name: "zero amount",
			req: &fxv1.GetQuoteRequest{
				FromCurrency: "USD", ToCurrency: "INR",
				SourceAmount: &commonv1.Money{Amount: "0"},
			},
			wantMsg: "must be positive",
		},
		{
			name: "sub-minor precision in the source amount",
			req: &fxv1.GetQuoteRequest{
				FromCurrency: "USD", ToCurrency: "INR",
				SourceAmount: &commonv1.Money{Amount: "100.005"},
			},
			wantMsg: "finer precision",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newServer().GetQuote(context.Background(), tc.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestGetQuote_NormalisesCurrencyCase(t *testing.T) {
	t.Parallel()

	// Lowercase input should not be rejected as unsupported; it is upper-cased
	// before validation. With a nil repository the call then panics at the rate
	// lookup, which proves validation passed.
	assert.Panics(t, func() {
		_, _ = newServer().GetQuote(context.Background(), &fxv1.GetQuoteRequest{
			FromCurrency: "usd",
			ToCurrency:   "inr",
			SourceAmount: &commonv1.Money{Amount: "100.00"},
		})
	})
}

func TestLockQuote_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     *fxv1.LockQuoteRequest
		wantMsg string
	}{
		{
			name:    "missing idempotency key",
			req:     &fxv1.LockQuoteRequest{QuoteId: "q1"},
			wantMsg: "idempotency_key required",
		},
		{
			name:    "missing quote id",
			req:     &fxv1.LockQuoteRequest{IdempotencyKey: "k"},
			wantMsg: "quote_id required",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := newServer().LockQuote(context.Background(), tc.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			assert.Contains(t, status.Convert(err).Message(), tc.wantMsg)
		})
	}
}

func TestLockQuote_FailsClosedWhenIdempotencyStoreIsDown(t *testing.T) {
	t.Parallel()

	store := idempotency.NewMemoryStore()
	store.FailNextClaim(assert.AnError)
	svc := service.NewFXServer(nil, store, 30*time.Second, zerolog.New(io.Discard))

	_, err := svc.LockQuote(context.Background(), &fxv1.LockQuoteRequest{
		IdempotencyKey: "k", QuoteId: "q1",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}
