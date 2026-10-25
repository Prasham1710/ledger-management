package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema owned by account-svc.
const Schema = "account"

// Migrate applies all account-svc migrations. Safe to call concurrently from
// multiple replicas: migrate.Run serialises on a Postgres advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

// migrations is append-only. Never edit a shipped migration.
var migrations = []migrate.Migration{
	{Version: 1, Name: "baseline_accounts_holds_outbox", SQL: migration001},
	{Version: 2, Name: "hold_reference_uniqueness_and_state", SQL: migration002},
	{Version: 3, Name: "outbox_publish_tracking", SQL: migration003},
	{Version: 4, Name: "balance_mutation_idempotency", SQL: migration004},
}

// migration001 is the historical baseline, written with IF NOT EXISTS so
// databases created by the pre-versioning bootstrap adopt it as a no-op.
const migration001 = `
CREATE TABLE IF NOT EXISTS account.accounts (
    id           UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id     TEXT          NOT NULL,
    currency     TEXT          NOT NULL,
    status       TEXT          NOT NULL DEFAULT 'ACTIVE'
        CHECK (status IN ('ACTIVE','SUSPENDED','CLOSED')),
    balance      NUMERIC(28,8) NOT NULL DEFAULT 0 CHECK (balance >= 0),
    hold_balance NUMERIC(28,8) NOT NULL DEFAULT 0 CHECK (hold_balance >= 0),
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account.holds (
    id           UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID          NOT NULL REFERENCES account.accounts(id),
    amount       NUMERIC(28,8) NOT NULL CHECK (amount > 0),
    reference_id TEXT          NOT NULL,
    expires_at   TIMESTAMPTZ   NOT NULL,
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS account.outbox_messages (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_accounts_owner ON account.accounts(owner_id);
CREATE INDEX IF NOT EXISTS idx_holds_account  ON account.holds(account_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_idem
    ON account.accounts(owner_id, currency)
    WHERE status = 'ACTIVE';`

// migration002 makes holds idempotent and gives them an explicit lifecycle.
//
// Two problems are addressed. First, nothing stopped a retried PlaceHold from
// creating a second hold for the same reference, reserving the caller's money
// twice; the unique index on active holds prevents that in the database.
//
// Second, holds were only ever deleted — by release or by consumption — so
// expires_at was written and never read, and a saga that died mid-flight left
// funds reserved forever with no record of why. Holds now carry a status and are
// retained after resolution, which preserves the audit trail and lets the expiry
// sweeper find abandoned ones.
const migration002 = `
ALTER TABLE account.holds
    ADD COLUMN IF NOT EXISTS status      TEXT NOT NULL DEFAULT 'ACTIVE',
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resolution  TEXT NOT NULL DEFAULT '';

ALTER TABLE account.holds
    ADD CONSTRAINT chk_holds_status
    CHECK (status IN ('ACTIVE', 'CONSUMED', 'RELEASED', 'EXPIRED'));

-- One active hold per reference: a retried PlaceHold now conflicts instead of
-- reserving the same funds a second time.
CREATE UNIQUE INDEX IF NOT EXISTS uq_holds_reference_active
    ON account.holds (reference_id)
    WHERE status = 'ACTIVE';

-- Drives the expiry sweeper; scoped to active holds so it stays small.
CREATE INDEX IF NOT EXISTS idx_holds_expiry
    ON account.holds (expires_at)
    WHERE status = 'ACTIVE';`

// migration003 adds publish tracking to the outbox, matching every other
// service so the relay can poll all schemas uniformly.
const migration003 = `
ALTER TABLE account.outbox_messages
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempts     INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

ALTER TABLE account.outbox_messages
    ALTER COLUMN aggregate_type SET DEFAULT 'account';

CREATE INDEX IF NOT EXISTS idx_account_outbox_unpublished
    ON account.outbox_messages (created_at)
    WHERE published_at IS NULL;`

// migration004 records every applied balance mutation, keyed on its reference.
//
// Credits and debits previously had no durable dedup key of their own: the only
// protection was the Redis idempotency cache, so a cache outage let a retried
// credit add the money a second time. Recording each mutation under a unique
// (kind, reference_id) makes the guarantee survive Redis entirely, and doubles
// as the per-account audit trail the reconciliation worker can join against.
const migration004 = `
CREATE TABLE IF NOT EXISTS account.balance_mutations (
    id           UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id   UUID          NOT NULL REFERENCES account.accounts(id),
    kind         TEXT          NOT NULL CHECK (kind IN ('CREDIT','DEBIT')),
    amount       NUMERIC(28,8) NOT NULL CHECK (amount > 0),
    currency     TEXT          NOT NULL,
    reference_id TEXT          NOT NULL,
    applied_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    -- One application per (kind, reference): a replayed credit or debit hits
    -- this constraint instead of moving money again.
    CONSTRAINT uq_balance_mutations_kind_reference UNIQUE (kind, reference_id)
);

CREATE INDEX IF NOT EXISTS idx_balance_mutations_account
    ON account.balance_mutations (account_id, applied_at DESC);`
