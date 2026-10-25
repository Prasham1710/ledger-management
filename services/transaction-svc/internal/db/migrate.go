package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema owned by transaction-svc.
const Schema = "transaction"

// Migrate applies all transaction-svc migrations. Safe to call concurrently
// from multiple replicas: migrate.Run serialises on a Postgres advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

// migrations is append-only. Never edit a shipped migration.
var migrations = []migrate.Migration{
	{Version: 1, Name: "baseline_transactions_outbox_saga_steps", SQL: migration001},
	{Version: 2, Name: "widen_amount_and_add_saga_progress", SQL: migration002},
	{Version: 3, Name: "outbox_publish_tracking", SQL: migration003},
}

// migration001 is the historical baseline, written with IF NOT EXISTS so
// databases created by the pre-versioning bootstrap adopt it as a no-op.
const migration001 = `
CREATE TABLE IF NOT EXISTS transaction.transactions (
    id                UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    status            TEXT          NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','COMPLETED','FAILED','REVERSED')),
    source_account_id TEXT          NOT NULL,
    dest_account_id   TEXT          NOT NULL,
    amount            NUMERIC(18,4) NOT NULL,
    currency          TEXT          NOT NULL,
    idempotency_key   TEXT          NOT NULL UNIQUE,
    description       TEXT          NOT NULL DEFAULT '',
    hold_id           TEXT,
    created_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS transaction.outbox_messages (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL DEFAULT 'transaction',
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS transaction.saga_steps (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id UUID        NOT NULL REFERENCES transaction.transactions(id),
    seq            INT         NOT NULL,
    step_name      TEXT        NOT NULL,
    status         TEXT        NOT NULL CHECK (status IN ('success', 'failed')),
    started_at     TIMESTAMPTZ NOT NULL,
    finished_at    TIMESTAMPTZ NOT NULL,
    duration_ms    BIGINT      NOT NULL,
    error_msg      TEXT        NOT NULL DEFAULT '',
    UNIQUE (transaction_id, seq)
);`

// migration002 widens amounts to the platform storage scale and records which
// saga steps have actually taken effect.
//
// The amount column was NUMERIC(18,4) while the ledger and account balances used
// (28,8), so precision was silently lost at this boundary.
//
// The progress columns exist because a crashed saga previously left no record of
// how far it had got. Compensation therefore could not be resumed: the recovery
// worker had no way to know whether the ledger had been written or the source
// debited, and undoing the wrong step corrupts balances. Each flag is set in the
// same transaction that performs the step it describes.
const migration002 = `
ALTER TABLE transaction.transactions
    ALTER COLUMN amount TYPE NUMERIC(28,8);

ALTER TABLE transaction.transactions
    ADD COLUMN IF NOT EXISTS ledger_posted  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS source_debited BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS dest_credited  BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS failure_reason TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS compensated_at TIMESTAMPTZ;

-- 'COMPENSATING' marks a saga whose forward steps are being undone. Without it a
-- saga midway through rollback was indistinguishable from one that never started.
ALTER TABLE transaction.transactions
    DROP CONSTRAINT IF EXISTS transactions_status_check;

ALTER TABLE transaction.transactions
    ADD CONSTRAINT transactions_status_check
    CHECK (status IN ('PENDING','COMPENSATING','COMPLETED','FAILED','REVERSED'));

-- Drives the recovery worker's search for abandoned sagas.
CREATE INDEX IF NOT EXISTS idx_transactions_in_flight
    ON transaction.transactions (updated_at)
    WHERE status IN ('PENDING', 'COMPENSATING');

CREATE INDEX IF NOT EXISTS idx_transactions_idem
    ON transaction.transactions (idempotency_key);`

// migration003 adds publish tracking to the outbox so the relay can publish
// before marking a row done, rather than deleting it beforehand.
const migration003 = `
ALTER TABLE transaction.outbox_messages
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempts     INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_transaction_outbox_unpublished
    ON transaction.outbox_messages (created_at)
    WHERE published_at IS NULL;`
