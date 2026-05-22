package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate runs all ledger-svc migrations in order.
// Each migration is idempotent (IF NOT EXISTS / CREATE INDEX IF NOT EXISTS).
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	for i, m := range migrations {
		if _, err := pool.Exec(ctx, m); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	return nil
}

var migrations = []string{migration001}

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
