package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// ErrQuoteNotFound means no quote exists with the requested id.
var ErrQuoteNotFound = errors.New("quote not found")

type Rate struct {
	FromCur    string
	ToCur      string
	Rate       decimal.Decimal
	ValidUntil time.Time
}

type Quote struct {
	ID           string
	FromCur      string
	ToCur        string
	Rate         decimal.Decimal
	SourceAmount decimal.Decimal
	TargetAmount decimal.Decimal
	Locked       bool
	LockedBy     string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// GetRate returns the active rate for a currency pair, or (nil, nil) when no
// unexpired rate exists.
//
// The valid_until filter is the staleness guard: an expired rate is treated as
// absent rather than used, so a service left running with old market data
// refuses to quote instead of quoting wrongly.
func (r *Repo) GetRate(ctx context.Context, from, to string) (*Rate, error) {
	rt := &Rate{}
	err := r.pool.QueryRow(ctx, `
		SELECT from_cur, to_cur, rate, valid_until
		FROM fx.rates
		WHERE from_cur = $1 AND to_cur = $2 AND valid_until > NOW()`,
		from, to,
	).Scan(&rt.FromCur, &rt.ToCur, &rt.Rate, &rt.ValidUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rate %s->%s: %w", from, to, err)
	}
	return rt, nil
}

// ExpireQuotes deletes quotes that expired without being locked, keeping the
// table bounded. Nothing previously removed them.
func (r *Repo) ExpireQuotes(ctx context.Context, retain time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM fx.quotes
		WHERE locked = FALSE AND expires_at < NOW() - $1::interval`, retain.String())
	if err != nil {
		return 0, fmt.Errorf("expire quotes: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CreateQuote inserts a new unlocked quote and returns it.
func (r *Repo) CreateQuote(ctx context.Context, from, to string, rate, srcAmt, tgtAmt decimal.Decimal, ttlSec int) (*Quote, error) {
	q := &Quote{}
	expiresAt := time.Now().UTC().Add(time.Duration(ttlSec) * time.Second)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO fx.quotes (from_cur, to_cur, rate, source_amount, target_amount, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, from_cur, to_cur, rate, source_amount, target_amount,
		          locked, COALESCE(locked_by,''), expires_at, created_at`,
		from, to, rate, srcAmt, tgtAmt, expiresAt,
	).Scan(&q.ID, &q.FromCur, &q.ToCur, &q.Rate, &q.SourceAmount, &q.TargetAmount,
		&q.Locked, &q.LockedBy, &q.ExpiresAt, &q.CreatedAt)
	return q, err
}

// LockQuote atomically marks a quote as used.
// Returns (quote, true, nil) on success.
// Returns (quote, false, nil) if already locked or expired — caller maps to FailedPrecondition.
func (r *Repo) LockQuote(ctx context.Context, quoteID, idempotencyKey string) (*Quote, bool, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op once committed

	q := &Quote{}
	err = tx.QueryRow(ctx, `
		SELECT id, from_cur, to_cur, rate, source_amount, target_amount,
		       locked, COALESCE(locked_by,''), expires_at, created_at
		FROM fx.quotes
		WHERE id = $1
		FOR UPDATE`,
		quoteID,
	).Scan(&q.ID, &q.FromCur, &q.ToCur, &q.Rate, &q.SourceAmount, &q.TargetAmount,
		&q.Locked, &q.LockedBy, &q.ExpiresAt, &q.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrQuoteNotFound
	}
	if err != nil {
		return nil, false, err
	}

	// Already locked by the same idempotency key: an idempotent retry. Commit so
	// the FOR UPDATE lock is released cleanly rather than rolled back.
	if q.Locked && q.LockedBy == idempotencyKey {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit idempotent lock replay: %w", err)
		}
		return q, true, nil
	}

	// Already locked by someone else or expired
	if q.Locked {
		return q, false, nil
	}
	if time.Now().UTC().After(q.ExpiresAt) {
		return q, false, nil
	}

	_, err = tx.Exec(ctx, `
		UPDATE fx.quotes
		SET locked = true, locked_by = $1
		WHERE id = $2`,
		idempotencyKey, quoteID,
	)
	if err != nil {
		return nil, false, err
	}
	q.Locked = true
	q.LockedBy = idempotencyKey

	return q, true, tx.Commit(ctx)
}

// ListRates returns all current FX rates (for UI display).
func (r *Repo) ListRates(ctx context.Context) ([]Rate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT from_cur, to_cur, rate, valid_until
		FROM fx.rates
		ORDER BY from_cur, to_cur`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rates []Rate
	for rows.Next() {
		var rt Rate
		if err := rows.Scan(&rt.FromCur, &rt.ToCur, &rt.Rate, &rt.ValidUntil); err != nil {
			return nil, err
		}
		rates = append(rates, rt)
	}
	return rates, rows.Err()
}
