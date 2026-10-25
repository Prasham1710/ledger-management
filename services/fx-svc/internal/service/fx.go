package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	"github.com/prasham/ledger/internal/idemguard"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/money"
	"github.com/prasham/ledger/services/fx-svc/internal/db"
)

// rateScale is the precision at which rates and derived inverses are held. It
// matches the fx.rates column and money.StorageScale.
const rateScale = 8

type FXServer struct {
	fxv1.UnimplementedFXServiceServer
	repo     *db.Repo
	idem     idempotency.Store
	quoteTTL time.Duration
	log      zerolog.Logger
}

func NewFXServer(repo *db.Repo, idem idempotency.Store, quoteTTL time.Duration, log zerolog.Logger) *FXServer {
	if quoteTTL <= 0 {
		quoteTTL = 30 * time.Second
	}
	return &FXServer{repo: repo, idem: idem, quoteTTL: quoteTTL, log: log}
}

// GetQuote prices a conversion and stores a short-lived quote.
//
// The target amount is quantized to the destination currency's minor unit, and
// the residue that quantization discards is reported alongside it. Rounding an
// FX result without accounting for the remainder is how unexplained
// reconciliation drift accumulates.
func (s *FXServer) GetQuote(ctx context.Context, req *fxv1.GetQuoteRequest) (*fxv1.GetQuoteResponse, error) {
	from := strings.ToUpper(strings.TrimSpace(req.FromCurrency))
	to := strings.ToUpper(strings.TrimSpace(req.ToCurrency))

	switch {
	case from == "":
		return nil, status.Error(codes.InvalidArgument, "from_currency required")
	case to == "":
		return nil, status.Error(codes.InvalidArgument, "to_currency required")
	case from == to:
		return nil, status.Error(codes.InvalidArgument, "from_currency and to_currency must differ")
	case req.SourceAmount == nil:
		return nil, status.Error(codes.InvalidArgument, "source_amount required")
	}

	if err := money.Currency(to).Validate(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "to_currency: %v", err)
	}
	// Parse validates the source currency, positivity, and minor-unit precision.
	srcAmount, err := money.Parse(req.SourceAmount.Amount, from)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "source_amount: %v", err)
	}

	rate, err := s.resolveRate(ctx, from, to)
	if err != nil {
		s.log.Error().Err(err).Str("pair", from+"->"+to).Msg("rate lookup failed")
		return nil, status.Error(codes.Internal, "rate lookup failed")
	}
	if rate == nil {
		// Either no rate exists, or the one that does has expired. Both mean the
		// service cannot price this transfer, and quoting a stale rate would be
		// worse than refusing.
		return nil, status.Errorf(codes.FailedPrecondition,
			"no currently valid rate for %s → %s", from, to)
	}

	// Convert at full storage precision, then quantize to the target currency.
	rawTarget := money.FromDecimal(srcAmount.Value().Mul(rate.Rate), money.Currency(to))
	target, residue := rawTarget.Quantize()

	if target.IsZero() {
		// The converted value rounds away entirely — e.g. a fraction of a yen.
		return nil, status.Errorf(codes.InvalidArgument,
			"converting %s at %s yields less than the smallest unit of %s",
			srcAmount, rate.Rate.StringFixed(rateScale), to)
	}

	quote, err := s.repo.CreateQuote(ctx, from, to, rate.Rate,
		srcAmount.Value(), target.Value(), int(s.quoteTTL.Seconds()))
	if err != nil {
		s.log.Error().Err(err).Msg("create quote failed")
		return nil, status.Error(codes.Internal, "create quote failed")
	}

	if !residue.IsZero() {
		// Visible rather than silent: summed across a batch this is real money.
		s.log.Debug().
			Str("quote_id", quote.ID).
			Str("pair", from+"->"+to).
			Str("residue", residue.StringFixed()).
			Msg("fx conversion rounded to the target currency's minor unit")
	}

	return &fxv1.GetQuoteResponse{Quote: toProtoQuote(quote)}, nil
}

// LockQuote reserves a quote for one transaction.
//
// The repository already implements this idempotently: a quote locked by the same
// key reports success. The idempotency guard sits in front only to collapse
// concurrent duplicates and to replay the response — it must not convert a
// legitimate retry into an error, which is what the previous pre-check did by
// returning AlreadyExists and thereby breaking the correct database behaviour
// underneath it.
func (s *FXServer) LockQuote(ctx context.Context, req *fxv1.LockQuoteRequest) (*fxv1.LockQuoteResponse, error) {
	switch {
	case req.IdempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.QuoteId == "":
		return nil, status.Error(codes.InvalidArgument, "quote_id required")
	}

	return idemguard.Do(ctx, s.idem, s.log, "fx:lock:"+req.IdempotencyKey,
		func() *fxv1.LockQuoteResponse { return &fxv1.LockQuoteResponse{} },
		func(ctx context.Context) (*fxv1.LockQuoteResponse, error) {
			quote, locked, err := s.repo.LockQuote(ctx, req.QuoteId, req.IdempotencyKey)
			if err != nil {
				if errors.Is(err, db.ErrQuoteNotFound) {
					return nil, status.Errorf(codes.NotFound, "quote %s not found", req.QuoteId)
				}
				s.log.Error().Err(err).Str("quote_id", req.QuoteId).Msg("lock quote failed")
				return nil, status.Error(codes.Internal, "lock quote failed")
			}

			if !locked {
				// Expired or already consumed by a different transaction. This is a
				// precondition failure, not a transient error: the caller must
				// request a fresh quote.
				return nil, status.Errorf(codes.FailedPrecondition,
					"quote %s is expired or already locked by another transaction", req.QuoteId)
			}

			return &fxv1.LockQuoteResponse{
				Quote:  toProtoQuote(quote),
				Locked: true,
			}, nil
		})
}

// resolveRate finds a rate for the pair, deriving the inverse when only the
// opposite direction is stored.
//
// Deriving 1/rate rather than reading a separately stored inverse guarantees the
// two directions are exactly reciprocal. The seed table previously held both
// directions as independently rounded constants — 83.5 and 0.01198, where 1/83.5
// is 0.011976 — so a round trip through both created value.
//
// A real platform applies a spread here; this derivation is deliberately exact so
// that when a spread is introduced it is explicit and auditable rather than an
// artefact of rounding.
func (s *FXServer) resolveRate(ctx context.Context, from, to string) (*db.Rate, error) {
	direct, err := s.repo.GetRate(ctx, from, to)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct, nil
	}

	inverse, err := s.repo.GetRate(ctx, to, from)
	if err != nil {
		return nil, err
	}
	if inverse == nil {
		return nil, nil
	}
	if inverse.Rate.IsZero() {
		// Guarded by a CHECK constraint on the column, but dividing by a stored
		// zero would panic rather than return an error.
		return nil, errors.New("stored rate is zero; cannot derive its inverse")
	}

	return &db.Rate{
		FromCur:    from,
		ToCur:      to,
		Rate:       decimal.NewFromInt(1).DivRound(inverse.Rate, rateScale),
		ValidUntil: inverse.ValidUntil,
	}, nil
}

// ── proto helpers ─────────────────────────────────────────────────────────────

func toProtoQuote(q *db.Quote) *fxv1.Quote {
	if q == nil {
		return nil
	}
	srcCur := money.Currency(q.FromCur)
	dstCur := money.Currency(q.ToCur)
	return &fxv1.Quote{
		Id:           q.ID,
		FromCurrency: q.FromCur,
		ToCurrency:   q.ToCur,
		Rate:         q.Rate.StringFixed(rateScale),
		SourceAmount: &commonv1.Money{
			Amount:   money.FromDecimal(q.SourceAmount, srcCur).StringMinor(),
			Currency: q.FromCur,
		},
		TargetAmount: &commonv1.Money{
			Amount:   money.FromDecimal(q.TargetAmount, dstCur).StringMinor(),
			Currency: q.ToCur,
		},
		Locked:    q.Locked,
		ExpiresAt: timestamppb.New(q.ExpiresAt),
		CreatedAt: timestamppb.New(q.CreatedAt),
	}
}
