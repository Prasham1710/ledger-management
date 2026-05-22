package service

import (
	"context"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/fx-svc/internal/db"
)

type FXServer struct {
	fxv1.UnimplementedFXServiceServer
	repo    *db.Repo
	idem    idempotency.Store
	quoteTTL int
}

func NewFXServer(repo *db.Repo, idem idempotency.Store, quoteTTLSec int) *FXServer {
	return &FXServer{repo: repo, idem: idem, quoteTTL: quoteTTLSec}
}

// GetQuote looks up the FX rate for a currency pair, calculates the target
// amount and stores an unlocked quote valid for quoteTTL seconds.
func (s *FXServer) GetQuote(ctx context.Context, req *fxv1.GetQuoteRequest) (*fxv1.GetQuoteResponse, error) {
	from := strings.ToUpper(strings.TrimSpace(req.FromCurrency))
	to   := strings.ToUpper(strings.TrimSpace(req.ToCurrency))

	if from == "" {
		return nil, status.Error(codes.InvalidArgument, "from_currency required")
	}
	if to == "" {
		return nil, status.Error(codes.InvalidArgument, "to_currency required")
	}
	if from == to {
		return nil, status.Error(codes.InvalidArgument, "from_currency and to_currency must differ")
	}
	if req.SourceAmount == nil || req.SourceAmount.Amount == "" {
		return nil, status.Error(codes.InvalidArgument, "source_amount required")
	}
	srcAmt, err := decimal.NewFromString(req.SourceAmount.Amount)
	if err != nil || !srcAmt.IsPositive() {
		return nil, status.Error(codes.InvalidArgument, "source_amount must be a positive decimal")
	}

	rate, err := s.resolveRate(ctx, from, to)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "rate lookup: %v", err)
	}
	if rate == nil {
		return nil, status.Errorf(codes.NotFound, "no active rate for %s → %s", from, to)
	}

	tgtAmt := srcAmt.Mul(rate.Rate).Round(4)

	quote, err := s.repo.CreateQuote(ctx, from, to, rate.Rate, srcAmt, tgtAmt, s.quoteTTL)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create quote: %v", err)
	}

	return &fxv1.GetQuoteResponse{Quote: toProtoQuote(quote)}, nil
}

// LockQuote atomically reserves a quote for one transaction.
// Returns locked=true on success, locked=false if the quote is expired or already used.
func (s *FXServer) LockQuote(ctx context.Context, req *fxv1.LockQuoteRequest) (*fxv1.LockQuoteResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.QuoteId == "" {
		return nil, status.Error(codes.InvalidArgument, "quote_id required")
	}

	idemKey := "fx:lock:" + req.IdempotencyKey
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
	}

	quote, locked, err := s.repo.LockQuote(ctx, req.QuoteId, req.IdempotencyKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "lock quote: %v", err)
	}

	if locked {
		_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 0, CreatedAt: time.Now()})
	}

	return &fxv1.LockQuoteResponse{
		Quote:  toProtoQuote(quote),
		Locked: locked,
	}, nil
}

// resolveRate looks up a direct rate first, then tries the inverse.
func (s *FXServer) resolveRate(ctx context.Context, from, to string) (*db.Rate, error) {
	rate, err := s.repo.GetRate(ctx, from, to)
	if err != nil || rate != nil {
		return rate, err
	}

	// Try inverse: rate(A→B) = 1 / rate(B→A)
	inv, err := s.repo.GetRate(ctx, to, from)
	if err != nil || inv == nil {
		return nil, err
	}
	invRate := decimal.NewFromInt(1).Div(inv.Rate).Round(8)
	return &db.Rate{
		FromCur:    from,
		ToCur:      to,
		Rate:       invRate,
		ValidUntil: inv.ValidUntil,
	}, nil
}

// ── proto helpers ─────────────────────────────────────────────────────────────

func toProtoQuote(q *db.Quote) *fxv1.Quote {
	return &fxv1.Quote{
		Id:           q.ID,
		FromCurrency: q.FromCur,
		ToCurrency:   q.ToCur,
		Rate:         q.Rate.StringFixed(8),
		SourceAmount: &commonv1.Money{Amount: q.SourceAmount.StringFixed(4), Currency: q.FromCur},
		TargetAmount: &commonv1.Money{Amount: q.TargetAmount.StringFixed(4), Currency: q.ToCur},
		Locked:       q.Locked,
		ExpiresAt:    timestamppb.New(q.ExpiresAt),
		CreatedAt:    timestamppb.New(q.CreatedAt),
	}
}

