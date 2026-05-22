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
	ScheduledAt    *time.Time
	SettledAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) CreatePayout(ctx context.Context, p *Payout) (*Payout, error) {
	dest, err := json.Marshal(p.Destination)
	if err != nil {
		return nil, err
	}
	var out Payout
	var destRaw []byte
	err = r.pool.QueryRow(ctx, `
		INSERT INTO payout.payouts
		  (account_id, amount, currency, rail, destination, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status, rail, account_id, amount::text, currency,
		          destination, COALESCE(batch_id,''), COALESCE(settlement_file,''),
		          idempotency_key, scheduled_at, settled_at, created_at, updated_at`,
		p.AccountID, p.Amount, p.Currency, p.Rail, dest, p.IdempotencyKey,
	).Scan(
		&out.ID, &out.Status, &out.Rail, &out.AccountID, &out.Amount, &out.Currency,
		&destRaw, &out.BatchID, &out.SettlementFile,
		&out.IdempotencyKey, &out.ScheduledAt, &out.SettledAt, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("CreatePayout: %w", err)
	}
	_ = json.Unmarshal(destRaw, &out.Destination)
	return &out, nil
}

func (r *Repo) GetPayout(ctx context.Context, id string) (*Payout, error) {
	return r.scan(ctx,
		`SELECT id, status, rail, account_id, amount::text, currency,
		        destination, COALESCE(batch_id,''), COALESCE(settlement_file,''),
		        idempotency_key, scheduled_at, settled_at, created_at, updated_at
		   FROM payout.payouts WHERE id = $1`, id)
}

func (r *Repo) GetByIdempotencyKey(ctx context.Context, key string) (*Payout, error) {
	return r.scan(ctx,
		`SELECT id, status, rail, account_id, amount::text, currency,
		        destination, COALESCE(batch_id,''), COALESCE(settlement_file,''),
		        idempotency_key, scheduled_at, settled_at, created_at, updated_at
		   FROM payout.payouts WHERE idempotency_key = $1`, key)
}

// ProcessPayout atomically moves to PROCESSING, sets batch/file key, and
// writes the outbox event. Both updates commit together or not at all.
func (r *Repo) ProcessPayout(ctx context.Context, id, batchID, fileKey string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx,
		`UPDATE payout.payouts
		 SET status='PROCESSING', batch_id=$2, settlement_file=$3, updated_at=NOW()
		 WHERE id=$1`,
		id, batchID, fileKey,
	); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO payout.outbox_messages (aggregate_id, event_type, payload)
		 VALUES ($1, 'payout.initiated', $2)`,
		id, raw,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *Repo) FailPayout(ctx context.Context, id, reason string) {
	payload, _ := json.Marshal(map[string]string{"payout_id": id, "reason": reason})
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	tx.Exec(ctx, `UPDATE payout.payouts SET status='FAILED', updated_at=NOW() WHERE id=$1`, id)         //nolint:errcheck
	tx.Exec(ctx, `INSERT INTO payout.outbox_messages (aggregate_id, event_type, payload) VALUES ($1,'payout.failed',$2)`, id, payload) //nolint:errcheck
	tx.Commit(ctx)                                                                                                                      //nolint:errcheck
}

func (r *Repo) scan(ctx context.Context, q string, args ...any) (*Payout, error) {
	var p Payout
	var destRaw []byte
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&p.ID, &p.Status, &p.Rail, &p.AccountID, &p.Amount, &p.Currency,
		&destRaw, &p.BatchID, &p.SettlementFile,
		&p.IdempotencyKey, &p.ScheduledAt, &p.SettledAt, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(destRaw, &p.Destination)
	return &p, nil
}
