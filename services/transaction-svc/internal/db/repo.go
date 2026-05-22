package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) CreateTransaction(ctx context.Context, t *Transaction) (*Transaction, error) {
	var out Transaction
	err := r.pool.QueryRow(ctx, `
		INSERT INTO transaction.transactions
		  (source_account_id, dest_account_id, amount, currency, idempotency_key, description)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status, source_account_id, dest_account_id,
		          amount::text, currency, idempotency_key, description,
		          COALESCE(hold_id,''), created_at, updated_at`,
		t.SourceAccountID, t.DestAccountID, t.Amount, t.Currency, t.IdempotencyKey, t.Description,
	).Scan(
		&out.ID, &out.Status, &out.SourceAccountID, &out.DestAccountID,
		&out.Amount, &out.Currency, &out.IdempotencyKey, &out.Description,
		&out.HoldID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("CreateTransaction: %w", err)
	}
	return &out, nil
}

func (r *Repo) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	return r.scan(ctx,
		`SELECT id, status, source_account_id, dest_account_id,
		        amount::text, currency, idempotency_key, description,
		        COALESCE(hold_id,''), created_at, updated_at
		   FROM transaction.transactions WHERE id = $1`, id)
}

func (r *Repo) GetByIdempotencyKey(ctx context.Context, key string) (*Transaction, error) {
	return r.scan(ctx,
		`SELECT id, status, source_account_id, dest_account_id,
		        amount::text, currency, idempotency_key, description,
		        COALESCE(hold_id,''), created_at, updated_at
		   FROM transaction.transactions WHERE idempotency_key = $1`, key)
}

// SetHoldID persists the hold_id acquired during PlaceHold so it can be
// released by a recovery process if the service crashes before settlement.
func (r *Repo) SetHoldID(ctx context.Context, id, holdID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE transaction.transactions SET hold_id=$2, updated_at=NOW() WHERE id=$1`,
		id, holdID)
	return err
}

// FinishTransaction atomically sets the terminal status and writes an outbox
// event. Both happen in a single DB transaction so the event is guaranteed to
// be published if and only if the status row is committed.
func (r *Repo) FinishTransaction(ctx context.Context, id, txnStatus, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("FinishTransaction marshal: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("FinishTransaction begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx,
		`UPDATE transaction.transactions SET status=$2, updated_at=NOW() WHERE id=$1`,
		id, txnStatus,
	); err != nil {
		return fmt.Errorf("FinishTransaction update: %w", err)
	}

	if _, err = tx.Exec(ctx,
		`INSERT INTO transaction.outbox_messages (aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3)`,
		id, eventType, raw,
	); err != nil {
		return fmt.Errorf("FinishTransaction outbox: %w", err)
	}

	return tx.Commit(ctx)
}

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
		s.StartedAt, s.FinishedAt, s.DurationMs, s.ErrorMsg,
	)
	return err
}

func (r *Repo) ListSteps(ctx context.Context, txnID string) ([]SagaStep, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, transaction_id, seq, step_name, status,
		       started_at, finished_at, duration_ms, error_msg
		  FROM transaction.saga_steps
		 WHERE transaction_id = $1
		 ORDER BY seq`, txnID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SagaStep
	for rows.Next() {
		var s SagaStep
		if err := rows.Scan(&s.ID, &s.TransactionID, &s.Seq, &s.StepName, &s.Status,
			&s.StartedAt, &s.FinishedAt, &s.DurationMs, &s.ErrorMsg); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) scan(ctx context.Context, q string, args ...any) (*Transaction, error) {
	var t Transaction
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&t.ID, &t.Status, &t.SourceAccountID, &t.DestAccountID,
		&t.Amount, &t.Currency, &t.IdempotencyKey, &t.Description,
		&t.HoldID, &t.CreatedAt, &t.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
