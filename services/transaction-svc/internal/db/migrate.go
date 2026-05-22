package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
	CREATE TABLE IF NOT EXISTS transaction.transactions (
		id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
		status            TEXT         NOT NULL DEFAULT 'PENDING'
			CHECK (status IN ('PENDING','COMPLETED','FAILED','REVERSED')),
		source_account_id TEXT         NOT NULL,
		dest_account_id   TEXT         NOT NULL,
		amount            NUMERIC(18,4) NOT NULL,
		currency          TEXT         NOT NULL,
		idempotency_key   TEXT         NOT NULL UNIQUE,
		description       TEXT         NOT NULL DEFAULT '',
		hold_id           TEXT,
		created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
		updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS transaction.outbox_messages (
		id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		aggregate_id    TEXT        NOT NULL,
		aggregate_type  TEXT        NOT NULL DEFAULT 'transaction',
		event_type      TEXT        NOT NULL,
		payload         JSONB       NOT NULL,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS transaction.saga_steps (
		id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		transaction_id  UUID        NOT NULL REFERENCES transaction.transactions(id),
		seq             INT         NOT NULL,
		step_name       TEXT        NOT NULL,
		status          TEXT        NOT NULL CHECK (status IN ('success', 'failed')),
		started_at      TIMESTAMPTZ NOT NULL,
		finished_at     TIMESTAMPTZ NOT NULL,
		duration_ms     BIGINT      NOT NULL,
		error_msg       TEXT        NOT NULL DEFAULT '',
		UNIQUE (transaction_id, seq)
	);
	`)
	return err
}
