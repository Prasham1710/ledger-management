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

// Transaction statuses.
const (
	StatusPending      = "PENDING"
	StatusCompensating = "COMPENSATING"
	StatusCompleted    = "COMPLETED"
	StatusFailed       = "FAILED"
	StatusReversed     = "REVERSED"
)

// Sentinel errors, mapped to gRPC codes by the service layer.
var (
	// ErrDuplicateIdempotencyKey means a transaction already exists for this
	// key. Enforced by a unique constraint, so it holds even if Redis is down.
	ErrDuplicateIdempotencyKey = errors.New("a transaction already exists for this idempotency_key")
	// ErrNotFound means no such transaction.
	ErrNotFound = errors.New("transaction not found")
	// ErrTerminal means the transaction has already reached a terminal state and
	// cannot be transitioned again.
	ErrTerminal = errors.New("transaction is already in a terminal state")
)

// Transaction is a payment saga's durable state.
type Transaction struct {
	ID              string
	Status          string
	SourceAccountID string
	DestAccountID   string
	Amount          string
	Currency        string
	IdempotencyKey  string
	Description     string
	HoldID          string
	// Progress flags record which forward steps actually took effect, so
	// compensation can undo exactly those and no others.
	LedgerPosted  bool
	SourceDebited bool
	DestCredited  bool
	FailureReason string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// IsTerminal reports whether no further transition is expected.
func (t *Transaction) IsTerminal() bool {
	switch t.Status {
	case StatusCompleted, StatusFailed, StatusReversed:
		return true
	default:
		return false
	}
}

// NeedsCompensation reports whether any forward step took effect and must be
// undone. Used by both the inline failure path and the recovery worker.
func (t *Transaction) NeedsCompensation() bool {
	return t.LedgerPosted || t.SourceDebited || t.DestCredited || t.HoldID != ""
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

const txnColumns = `id, status, source_account_id, dest_account_id,
	amount::text, currency, idempotency_key, description,
	COALESCE(hold_id,''), ledger_posted, source_debited, dest_credited,
	failure_reason, created_at, updated_at`

// CreateTransaction inserts a new saga row in PENDING state.
//
// A duplicate idempotency_key is reported as ErrDuplicateIdempotencyKey rather
// than a raw constraint error, so the service can map it to AlreadyExists and
// return the existing transaction instead of a bare Internal.
func (r *Repo) CreateTransaction(ctx context.Context, t *Transaction) (*Transaction, error) {
	var out Transaction
	err := r.pool.QueryRow(ctx, `
		INSERT INTO transaction.transactions
		  (source_account_id, dest_account_id, amount, currency, idempotency_key, description)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+txnColumns,
		t.SourceAccountID, t.DestAccountID, t.Amount, t.Currency, t.IdempotencyKey, t.Description,
	).Scan(scanTargets(&out)...)
	if isUniqueViolation(err) {
		return nil, ErrDuplicateIdempotencyKey
	}
	if err != nil {
		return nil, fmt.Errorf("create transaction: %w", err)
	}
	return &out, nil
}

func (r *Repo) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	return r.scan(ctx, `SELECT `+txnColumns+` FROM transaction.transactions WHERE id = $1`, id)
}

func (r *Repo) GetByIdempotencyKey(ctx context.Context, key string) (*Transaction, error) {
	return r.scan(ctx, `SELECT `+txnColumns+` FROM transaction.transactions WHERE idempotency_key = $1`, key)
}

// SetHoldID records the reservation so compensation can release it even if this
// process dies immediately afterwards.
func (r *Repo) SetHoldID(ctx context.Context, id, holdID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE transaction.transactions SET hold_id = $2, updated_at = NOW() WHERE id = $1`,
		id, holdID)
	if err != nil {
		return fmt.Errorf("set hold_id: %w", err)
	}
	return nil
}

// MarkStepDone records that a forward step took effect.
//
// Progress is persisted immediately after each step rather than inferred later,
// because compensation must know exactly which effects exist. Guessing wrong
// either leaves money moved or moves it back twice.
func (r *Repo) MarkStepDone(ctx context.Context, id, column string) error {
	// column is chosen from a fixed set below, never from caller input.
	switch column {
	case "ledger_posted", "source_debited", "dest_credited":
	default:
		return fmt.Errorf("unknown saga progress column %q", column)
	}
	if _, err := r.pool.Exec(ctx, fmt.Sprintf(
		`UPDATE transaction.transactions SET %s = TRUE, updated_at = NOW() WHERE id = $1`, column,
	), id); err != nil {
		return fmt.Errorf("mark %s: %w", column, err)
	}
	return nil
}

// BeginCompensation moves a saga into COMPENSATING and records why.
//
// The transition is guarded: only a PENDING or already-COMPENSATING saga may
// enter compensation, so a COMPLETED transaction can never be silently
// overwritten with FAILED. The previous unguarded update allowed exactly that.
func (r *Repo) BeginCompensation(ctx context.Context, id, reason string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE transaction.transactions
		SET status = 'COMPENSATING', failure_reason = $2, updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'COMPENSATING')`,
		id, truncateReason(reason))
	if err != nil {
		return fmt.Errorf("begin compensation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTerminal
	}
	return nil
}

// FinishTransaction sets a terminal status and writes an outbox event in one
// transaction, so the event exists if and only if the status was committed.
//
// The status transition is guarded against overwriting an existing terminal
// state, and the event is only written when the transition actually happened.
func (r *Repo) FinishTransaction(ctx context.Context, id, txnStatus, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("finish transaction: marshal payload: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("finish transaction: begin: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op once committed

	// Only in-flight sagas may reach a terminal state. Without this guard a
	// late-arriving failure path could downgrade a COMPLETED payment to FAILED.
	tag, err := tx.Exec(ctx, `
		UPDATE transaction.transactions
		SET status = $2,
		    compensated_at = CASE WHEN $2 IN ('FAILED','REVERSED') THEN NOW() ELSE compensated_at END,
		    updated_at = NOW()
		WHERE id = $1 AND status IN ('PENDING', 'COMPENSATING')`,
		id, txnStatus)
	if err != nil {
		return fmt.Errorf("finish transaction: update status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrTerminal
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO transaction.outbox_messages (aggregate_id, event_type, payload)
		VALUES ($1, $2, $3)`,
		id, eventType, raw,
	); err != nil {
		return fmt.Errorf("finish transaction: insert outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("finish transaction: commit: %w", err)
	}
	return nil
}

// FindStuck returns in-flight sagas untouched for longer than olderThan.
//
// These are sagas whose owning process died mid-flight. Nothing previously
// looked for them: a transaction could sit in PENDING forever with its funds
// held and its ledger entry unreversed, and the only trace was a log line.
func (r *Repo) FindStuck(ctx context.Context, olderThan time.Duration, limit int) ([]*Transaction, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	rows, err := r.pool.Query(ctx, `
		SELECT `+txnColumns+`
		FROM transaction.transactions
		WHERE status IN ('PENDING', 'COMPENSATING') AND updated_at < $1
		ORDER BY updated_at
		LIMIT $2`, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("find stuck transactions: %w", err)
	}
	defer rows.Close()

	var out []*Transaction
	for rows.Next() {
		var t Transaction
		if err := rows.Scan(scanTargets(&t)...); err != nil {
			return nil, fmt.Errorf("scan stuck transaction: %w", err)
		}
		out = append(out, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stuck transactions: %w", err)
	}
	return out, nil
}

// CountInFlight reports how many sagas are mid-flight, for the metrics gauge.
func (r *Repo) CountInFlight(ctx context.Context) (int64, error) {
	var n int64
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM transaction.transactions
		WHERE status IN ('PENDING','COMPENSATING')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count in-flight transactions: %w", err)
	}
	return n, nil
}

// ── saga steps ────────────────────────────────────────────────────────────────

type SagaStep struct {
	ID            string
	TransactionID string
	Seq           int
	StepName      string
	Status        string
	StartedAt     time.Time
	FinishedAt    time.Time
	DurationMs    int64
	ErrorMsg      string
}

func (r *Repo) RecordStep(ctx context.Context, s *SagaStep) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO transaction.saga_steps
		  (transaction_id, seq, step_name, status, started_at, finished_at, duration_ms, error_msg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (transaction_id, seq) DO NOTHING`,
		s.TransactionID, s.Seq, s.StepName, s.Status,
		s.StartedAt, s.FinishedAt, s.DurationMs, truncateReason(s.ErrorMsg),
	)
	if err != nil {
		return fmt.Errorf("record saga step: %w", err)
	}
	return nil
}

func (r *Repo) ListSteps(ctx context.Context, txnID string) ([]SagaStep, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, transaction_id, seq, step_name, status,
		       started_at, finished_at, duration_ms, error_msg
		FROM transaction.saga_steps
		WHERE transaction_id = $1
		ORDER BY seq`, txnID)
	if err != nil {
		return nil, fmt.Errorf("list saga steps: %w", err)
	}
	defer rows.Close()

	var out []SagaStep
	for rows.Next() {
		var s SagaStep
		if err := rows.Scan(&s.ID, &s.TransactionID, &s.Seq, &s.StepName, &s.Status,
			&s.StartedAt, &s.FinishedAt, &s.DurationMs, &s.ErrorMsg); err != nil {
			return nil, fmt.Errorf("scan saga step: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saga steps: %w", err)
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func scanTargets(t *Transaction) []any {
	return []any{
		&t.ID, &t.Status, &t.SourceAccountID, &t.DestAccountID,
		&t.Amount, &t.Currency, &t.IdempotencyKey, &t.Description,
		&t.HoldID, &t.LedgerPosted, &t.SourceDebited, &t.DestCredited,
		&t.FailureReason, &t.CreatedAt, &t.UpdatedAt,
	}
}

func (r *Repo) scan(ctx context.Context, q string, args ...any) (*Transaction, error) {
	var t Transaction
	err := r.pool.QueryRow(ctx, q, args...).Scan(scanTargets(&t)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load transaction: %w", err)
	}
	return &t, nil
}

// truncateReason bounds stored error text. Upstream errors can be arbitrarily
// long, and an unbounded reason column is a slow-motion storage problem.
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
