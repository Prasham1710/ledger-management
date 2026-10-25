package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Payout statuses.
const (
	StatusPending      = "PENDING"
	StatusCompensating = "COMPENSATING"
	StatusProcessing   = "PROCESSING"
	StatusSettled      = "SETTLED"
	StatusFailed       = "FAILED"
)

// Sentinel errors, mapped to gRPC codes by the service layer.
var (
	ErrDuplicateIdempotencyKey = errors.New("a payout already exists for this idempotency_key")
	ErrTerminal                = errors.New("payout is already in a terminal state")
)

// Destination holds the beneficiary bank details for a payout.
//
// These fields are personal financial data. They are persisted because the
// settlement file must be regenerable, but they should never be logged, and the
// generated file must not be written to unencrypted storage.
type Destination struct {
	AccountNumber string `json:"account_number,omitempty"`
	IFSCCode      string `json:"ifsc_code,omitempty"`
	IBAN          string `json:"iban,omitempty"`
	BIC           string `json:"bic,omitempty"`
	AccountName   string `json:"account_name,omitempty"`
}

type Payout struct {
	ID             string
	Status         string
	Rail           string
	AccountID      string
	Amount         string
	Currency       string
	Destination    Destination
	BatchID        string
	SettlementFile string
	IdempotencyKey string
	// Progress flags record which forward steps took effect, so compensation
	// undoes exactly those.
	AccountDebited bool
	LedgerPosted   bool
	FailureReason  string
	ScheduledAt    *time.Time
	SettledAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NeedsCompensation reports whether money has left the account and must be
// returned.
func (p *Payout) NeedsCompensation() bool { return p.AccountDebited || p.LedgerPosted }

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

const payoutColumns = `id, status, rail, account_id, amount::text, currency,
	destination, COALESCE(batch_id,''), COALESCE(settlement_file,''),
	idempotency_key, account_debited, ledger_posted, failure_reason,
	scheduled_at, settled_at, created_at, updated_at`

func (r *Repo) CreatePayout(ctx context.Context, p *Payout) (*Payout, error) {
	dest, err := json.Marshal(p.Destination)
	if err != nil {
		return nil, fmt.Errorf("marshal destination: %w", err)
	}

	var out Payout
	var destRaw []byte
	err = r.pool.QueryRow(ctx, `
		INSERT INTO payout.payouts
		  (account_id, amount, currency, rail, destination, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+payoutColumns,
		p.AccountID, p.Amount, p.Currency, p.Rail, dest, p.IdempotencyKey,
	).Scan(scanTargets(&out, &destRaw)...)
	if isUniqueViolation(err) {
		return nil, ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return nil, fmt.Errorf("create payout: %w", err)
	}
	if err := json.Unmarshal(destRaw, &out.Destination); err != nil {
		return nil, fmt.Errorf("decode destination: %w", err)
	}
	return &out, nil
}

func (r *Repo) GetPayout(ctx context.Context, id string) (*Payout, error) {
	return r.scan(ctx, `SELECT `+payoutColumns+` FROM payout.payouts WHERE id = $1`, id)
}

func (r *Repo) GetByIdempotencyKey(ctx context.Context, key string) (*Payout, error) {
	return r.scan(ctx, `SELECT `+payoutColumns+` FROM payout.payouts WHERE idempotency_key = $1`, key)
}

// MarkStepDone records that a forward step took effect. Persisted immediately so
// compensation knows what exists even if this process dies next.
func (r *Repo) MarkStepDone(ctx context.Context, id, column string) error {
	// column comes from a fixed set, never from caller input.
	switch column {
	case "account_debited", "ledger_posted":
	default:
		return fmt.Errorf("unknown payout progress column %q", column)
	}
	if _, err := r.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE payout.payouts SET %s = TRUE, updated_at = NOW() WHERE id = $1`, column,
	), id); err != nil {
		return fmt.Errorf("mark %s: %w", column, err)
	}
	return nil
}

// ProcessPayout moves a payout to PROCESSING and emits payout.initiated in one
// transaction, so the event exists if and only if the status committed.
//
// The transition is guarded: only an in-flight payout may advance, which stops a
// late path from resurrecting a payout that has already failed.
func (r *Repo) ProcessPayout(ctx context.Context, id, batchID, fileKey string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payout.initiated payload: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin process payout: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op once committed

	tag, err := tx.Exec(ctx, `
		UPDATE payout.payouts
		SET status = 'PROCESSING', batch_id = $2, settlement_file = $3, updated_at = NOW()
		WHERE id = $1 AND status = 'PENDING'`,
		id, batchID, fileKey)
	if err != nil {
		return fmt.Errorf("update payout to processing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTerminal
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO payout.outbox_messages (aggregate_id, event_type, payload)
		VALUES ($1, 'payout.initiated', $2)`,
		id, raw,
	); err != nil {
		return fmt.Errorf("insert payout.initiated: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit process payout: %w", err)
	}
	return nil
}

// BeginCompensation moves a payout into COMPENSATING and records why.
func (r *Repo) BeginCompensation(ctx context.Context, id, reason string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE payout.payouts
		SET status = 'COMPENSATING', failure_reason = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'COMPENSATING')`,
		id, truncateReason(reason))
	if err != nil {
		return fmt.Errorf("begin payout compensation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTerminal
	}
	return nil
}

// FailPayout marks a payout FAILED and emits payout.failed in one transaction.
//
// Unlike the previous implementation this returns an error instead of discarding
// every failure including the commit, so a caller can tell whether the terminal
// state was actually recorded.
func (r *Repo) FailPayout(ctx context.Context, id, reason string) error {
	payload, err := json.Marshal(map[string]string{
		"payout_id": id,
		"reason":    truncateReason(reason),
	})
	if err != nil {
		return fmt.Errorf("marshal payout.failed payload: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail payout: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE payout.payouts
		SET status = 'FAILED', failure_reason = $2, compensated_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'COMPENSATING')`,
		id, truncateReason(reason))
	if err != nil {
		return fmt.Errorf("update payout to failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTerminal
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO payout.outbox_messages (aggregate_id, event_type, payload)
		VALUES ($1, 'payout.failed', $2)`,
		id, payload,
	); err != nil {
		return fmt.Errorf("insert payout.failed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fail payout: %w", err)
	}
	return nil
}

// FindStuck returns in-flight payouts untouched for longer than olderThan —
// those whose owning process died mid-saga.
func (r *Repo) FindStuck(ctx context.Context, olderThan time.Duration, limit int) ([]*Payout, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	rows, err := r.pool.Query(ctx, `
		SELECT `+payoutColumns+`
		FROM payout.payouts
		WHERE status IN ('PENDING', 'COMPENSATING') AND updated_at < $1
		ORDER BY updated_at
		LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("find stuck payouts: %w", err)
	}
	defer rows.Close()

	var out []*Payout
	for rows.Next() {
		var p Payout
		var destRaw []byte
		if err := rows.Scan(scanTargets(&p, &destRaw)...); err != nil {
			return nil, fmt.Errorf("scan stuck payout: %w", err)
		}
		if err := json.Unmarshal(destRaw, &p.Destination); err != nil {
			return nil, fmt.Errorf("decode destination: %w", err)
		}
		out = append(out, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stuck payouts: %w", err)
	}
	return out, nil
}

// CountInFlight reports how many payouts are mid-saga, for the metrics gauge.
func (r *Repo) CountInFlight(ctx context.Context) (int64, error) {
	var n int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM payout.payouts
		WHERE status IN ('PENDING','COMPENSATING')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count in-flight payouts: %w", err)
	}
	return n, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func scanTargets(p *Payout, destRaw *[]byte) []any {
	return []any{
		&p.ID, &p.Status, &p.Rail, &p.AccountID, &p.Amount, &p.Currency,
		destRaw, &p.BatchID, &p.SettlementFile,
		&p.IdempotencyKey, &p.AccountDebited, &p.LedgerPosted, &p.FailureReason,
		&p.ScheduledAt, &p.SettledAt, &p.CreatedAt, &p.UpdatedAt,
	}
}

func (r *Repo) scan(ctx context.Context, q string, args ...any) (*Payout, error) {
	var p Payout
	var destRaw []byte
	err := r.pool.QueryRow(ctx, q, args...).Scan(scanTargets(&p, &destRaw)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load payout: %w", err)
	}
	if err := json.Unmarshal(destRaw, &p.Destination); err != nil {
		return nil, fmt.Errorf("decode destination: %w", err)
	}
	return &p, nil
}

// truncateReason bounds stored error text so an upstream error cannot grow the
// column without limit.
func truncateReason(s string) string {
	const maxLen = 1000
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…(truncated)"
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
