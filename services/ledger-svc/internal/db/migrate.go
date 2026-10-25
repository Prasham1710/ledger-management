package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema owned by ledger-svc.
const Schema = "ledger"

// Migrate applies all ledger-svc migrations. Safe to call concurrently from
// multiple replicas: migrate.Run serialises on a Postgres advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

// migrations is append-only. Never edit a shipped migration — the runner
// verifies checksums and refuses to start if one changed.
var migrations = []migrate.Migration{
	{Version: 1, Name: "baseline_entries_and_outbox", SQL: migration001},
	{Version: 2, Name: "widen_amount_to_storage_scale", SQL: migration002},
	{Version: 3, Name: "pin_one_currency_per_account", SQL: migration003},
	{Version: 4, Name: "entry_kind_and_dedup_constraints", SQL: migration004},
	{Version: 5, Name: "outbox_publish_tracking", SQL: migration005},
}

// migration001 is the historical baseline. It is written with IF NOT EXISTS so
// databases created by the pre-versioning bootstrap adopt it as a no-op.
const migration001 = `
CREATE TABLE IF NOT EXISTS ledger.entries (
    id           UUID          NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    account_id   TEXT          NOT NULL,
    type         TEXT          NOT NULL,
    amount       NUMERIC(20,4) NOT NULL,
    currency     TEXT          NOT NULL,
    reference_id TEXT          NOT NULL,
    description  TEXT          NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_entries_type   CHECK (type IN ('DEBIT', 'CREDIT')),
    CONSTRAINT chk_entries_amount CHECK (amount > 0)
);
CREATE INDEX IF NOT EXISTS idx_entries_account_created
    ON ledger.entries (account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_entries_reference
    ON ledger.entries (reference_id);
CREATE TABLE IF NOT EXISTS ledger.outbox_messages (
    id             UUID        NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);`

// migration002 widens amount to the platform-wide storage scale.
//
// The baseline used NUMERIC(20,4) while account balances used NUMERIC(28,8), so
// any value carrying more than four decimal places — every FX-derived amount —
// was silently rounded on its way into the ledger and then failed to match the
// account balance it was meant to mirror.
const migration002 = `
ALTER TABLE ledger.entries
    ALTER COLUMN amount TYPE NUMERIC(28,8);`

// migration003 pins each account to a single currency.
//
// Balances are projected as SUM(credits) - SUM(debits) over an account's
// entries. Nothing previously stopped entries in different currencies landing on
// one account, which made that sum meaningless and let GetBalance report a total
// labelled with an arbitrary MAX(currency). The composite foreign key makes the
// first entry fix the account's currency and rejects any later disagreement.
const migration003 = `
CREATE TABLE IF NOT EXISTS ledger.account_currency (
    account_id TEXT        NOT NULL PRIMARY KEY,
    currency   TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_account_currency UNIQUE (account_id, currency)
);

-- Adopt whatever currency existing rows already use. An account already holding
-- mixed currencies is real corruption, so surface it loudly rather than picking
-- a winner: this INSERT fails on the duplicate primary key.
INSERT INTO ledger.account_currency (account_id, currency)
SELECT DISTINCT account_id, currency FROM ledger.entries;

ALTER TABLE ledger.entries
    ADD CONSTRAINT fk_entries_account_currency
    FOREIGN KEY (account_id, currency)
    REFERENCES ledger.account_currency (account_id, currency);`

// migration004 adds the entry kind and the uniqueness constraints that make
// posting and reversal idempotent in the database.
//
// The only thing previously preventing a double post was a non-atomic Redis
// check, so a Redis outage or a concurrent retry produced duplicate entries with
// no trace. These indexes turn a duplicate post into a constraint violation the
// service maps to AlreadyExists. Reversals are a distinct kind, so a reference
// can hold exactly one posting pair and at most one reversal pair.
const migration004 = `
ALTER TABLE ledger.entries
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'POSTING';

ALTER TABLE ledger.entries
    ADD CONSTRAINT chk_entries_kind CHECK (kind IN ('POSTING', 'REVERSAL'));

-- One debit and one credit per (reference, kind): posting a reference twice, or
-- reversing it twice, is now impossible rather than merely unlikely.
CREATE UNIQUE INDEX IF NOT EXISTS uq_entries_reference_kind_type
    ON ledger.entries (reference_id, kind, type);

CREATE INDEX IF NOT EXISTS idx_entries_account_currency
    ON ledger.entries (account_id, currency);`

// migration005 adds publish tracking to the outbox.
//
// The relay previously deleted rows in the same statement that read them, before
// Kafka acknowledged anything, making delivery at-most-once: any broker error
// lost the event permanently. These columns let the relay claim a row, publish
// it, and only then mark it published, retaining attempt counts and the last
// error for diagnosis.
const migration005 = `
ALTER TABLE ledger.outbox_messages
    ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempts     INT  NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_error   TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

ALTER TABLE ledger.outbox_messages
    ALTER COLUMN aggregate_type SET DEFAULT 'ledger';

-- Partial index: the relay only scans unpublished rows, so this keeps that scan
-- proportional to the backlog rather than to total history.
CREATE INDEX IF NOT EXISTS idx_ledger_outbox_unpublished
    ON ledger.outbox_messages (created_at)
    WHERE published_at IS NULL;`
