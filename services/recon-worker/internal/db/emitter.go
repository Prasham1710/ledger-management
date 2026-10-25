// Package db writes reconciliation events to the transactional outbox.
package db

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema the recon worker owns for its outbox.
const Schema = "recon"

// Emitter writes recon.drift events to the outbox so they reach Kafka through the
// same relay as every other platform event.
//
// The worker previously logged drift and uploaded a report, but emitted nothing:
// recon.drift was mapped by the relay and subscribed to by the consumer, yet no
// code path ever produced it, so the alerting chain was inert.
type Emitter struct {
	pool *pgxpool.Pool
}

func NewEmitter(pool *pgxpool.Pool) *Emitter { return &Emitter{pool: pool} }

// Migrate creates the recon outbox table.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

var migrations = []migrate.Migration{
	{Version: 1, Name: "recon_outbox", SQL: migration001},
}

// migration001 creates the recon outbox with the same shape as every other
// service's, so the relay polls it without special-casing.
const migration001 = `
CREATE TABLE IF NOT EXISTS recon.outbox_messages (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL DEFAULT 'recon',
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    published_at   TIMESTAMPTZ,
    attempts       INT         NOT NULL DEFAULT 0,
    last_error     TEXT        NOT NULL DEFAULT '',
    locked_until   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_recon_outbox_unpublished
    ON recon.outbox_messages (created_at)
    WHERE published_at IS NULL;`

// EmitDrift writes a recon.drift event to the outbox.
func (e *Emitter) EmitDrift(ctx context.Context, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal recon.drift payload: %w", err)
	}

	// Each run is its own aggregate, so events partition evenly rather than all
	// landing on one Kafka partition.
	aggregateID := uuid.New().String()

	if _, err := e.pool.Exec(ctx, `
		INSERT INTO recon.outbox_messages (aggregate_id, aggregate_type, event_type, payload)
		VALUES ($1, 'recon', 'recon.drift', $2)`,
		aggregateID, raw,
	); err != nil {
		return fmt.Errorf("insert recon.drift event: %w", err)
	}
	return nil
}
