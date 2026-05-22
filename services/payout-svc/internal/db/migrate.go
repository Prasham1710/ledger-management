package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
	CREATE TABLE IF NOT EXISTS payout.payouts (
		id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
		status           TEXT         NOT NULL DEFAULT 'PENDING'
			CHECK (status IN ('PENDING','PROCESSING','SETTLED','FAILED')),
		rail             TEXT         NOT NULL DEFAULT 'NACH'
			CHECK (rail IN ('NACH','SEPA','SWIFT')),
		account_id       TEXT         NOT NULL,
		amount           NUMERIC(18,4) NOT NULL,
		currency         TEXT         NOT NULL,
		destination      JSONB        NOT NULL DEFAULT '{}',
		batch_id         TEXT,
		settlement_file  TEXT,
		idempotency_key  TEXT         NOT NULL UNIQUE,
		scheduled_at     TIMESTAMPTZ,
		settled_at       TIMESTAMPTZ,
		created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
		updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS payout.outbox_messages (
		id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
		aggregate_id    TEXT        NOT NULL,
		aggregate_type  TEXT        NOT NULL DEFAULT 'payout',
		event_type      TEXT        NOT NULL,
		payload         JSONB       NOT NULL,
		created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
	);
	`)
	return err
}
