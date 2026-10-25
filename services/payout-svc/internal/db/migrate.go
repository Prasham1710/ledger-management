package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema owned by payout-svc.
const Schema = "payout"

// Migrate applies all payout-svc migrations. Safe to call concurrently from
// multiple replicas: migrate.Run serialises on a Postgres advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

// migrations is append-only. Never edit a shipped migration.
var migrations = []migrate.Migration{
	{Version: 1, Name: "baseline_payouts_outbox", SQL: migration001},
	{Version: 2, Name: "widen_amount_and_add_saga_progress", SQL: migration002},
	{Version: 3, Name: "outbox_publish_tracking", SQL: migration003},
}

// migration001 is the historical baseline, written with IF NOT EXISTS so
// databases created by the pre-versioning bootstrap adopt it as a no-op.
const migration001 = `
CREATE TABLE IF NOT EXISTS payout.payouts (
    id              UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    status          TEXT          NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','PROCESSING','SETTLED','FAILED')),
    rail            TEXT          NOT NULL DEFAULT 'NACH'
        CHECK (rail IN ('NACH','SEPA','SWIFT')),
    account_id      TEXT          NOT NULL,
    amount          NUMERIC(18,4) NOT NULL,
    currency        TEXT          NOT NULL,
    destination     JSONB         NOT NULL DEFAULT '{}',
    batch_id        TEXT,
    settlement_file TEXT,
    idempotency_key TEXT          NOT NULL UNIQUE,
    scheduled_at    TIMESTAMPTZ,
    settled_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payout.outbox_messages (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL DEFAULT 'payout',
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

// migration002 widens amounts to the platform storage scale and records saga
// progress so a failed payout can be compensated correctly.
//
// The amount column was NUMERIC(18,4) against the ledger's and accounts' (28,8).
//
// The progress flags matter more. The payout saga debits the account and then
// writes the ledger; when the ledger write failed, the code logged "manual
// reconciliation required" and left the debit in place, with nothing recording
// that money had left the account. These columns let the compensation path
// return the funds, and let the sweeper find payouts abandoned mid-flight.
const migration002 = `
ALTER TABLE payout.payouts
    ALTER COLUMN amount TYPE NUMERIC(28,8);

ALTER TABLE payout.payouts
    ADD COLUMN IF NOT EXISTS account_debited BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS ledger_posted   BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS failure_reason  TEXT    NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS compensated_at  TIMESTAMPTZ;

-- 'COMPENSATING' marks a payout whose debit is being returned.
ALTER TABLE payout.payouts
    DROP CONSTRAINT IF EXISTS payouts_status_check;

ALTER TABLE payout.payouts
    ADD CONSTRAINT payouts_status_check
    CHECK (status IN ('PENDING','COMPENSATING','PROCESSING','SETTLED','FAILED'));

CREATE INDEX IF NOT EXISTS idx_payouts_in_flight
    ON payout.payouts (updated_at)
    WHERE status IN ('PENDING', 'COMPENSATING');

CREATE INDEX IF NOT EXISTS idx_payouts_idem
    ON payout.payouts (idempotency_key);`

// migration003 adds publish tracking to the outbox so the relay publishes before
// marking a row done rather than deleting it first.
const migration003 = `
ALTER TABLE payout.outbox_messages
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempts     INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_payout_outbox_unpublished
    ON payout.outbox_messages (created_at)
    WHERE published_at IS NULL;`
