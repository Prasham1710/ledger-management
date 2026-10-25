package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema owned by fx-svc.
const Schema = "fx"

// Migrate applies all fx-svc migrations. Safe to call concurrently from multiple
// replicas: migrate.Run serialises on a Postgres advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

// migrations is append-only. Never edit a shipped migration.
var migrations = []migrate.Migration{
	{Version: 1, Name: "baseline_rates_and_quotes", SQL: migration001},
	{Version: 2, Name: "quote_indexes", SQL: migration002},
}

// migration001 is the historical baseline, written with IF NOT EXISTS so
// databases created by the pre-versioning bootstrap adopt it as a no-op.
const migration001 = `
		CREATE SCHEMA IF NOT EXISTS fx;

		-- Live FX rates seeded by fx-svc on startup.
		-- valid_until lets the service detect stale rates (circuit-breaker hook).
		CREATE TABLE IF NOT EXISTS fx.rates (
			id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			from_cur    TEXT        NOT NULL,
			to_cur      TEXT        NOT NULL,
			rate        NUMERIC(28,8) NOT NULL CHECK (rate > 0),
			source      TEXT        NOT NULL DEFAULT 'seed',
			valid_until TIMESTAMPTZ NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE (from_cur, to_cur)
		);

		-- A quote locks in a rate for a single transaction (30 s window).
		-- locked=true means it has been consumed — cannot be reused.
		CREATE TABLE IF NOT EXISTS fx.quotes (
			id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			from_cur      TEXT        NOT NULL,
			to_cur        TEXT        NOT NULL,
			rate          NUMERIC(28,8) NOT NULL,
			source_amount NUMERIC(28,8) NOT NULL,
			target_amount NUMERIC(28,8) NOT NULL,
			locked        BOOLEAN     NOT NULL DEFAULT FALSE,
			locked_by     TEXT,
			expires_at    TIMESTAMPTZ NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_rates_pair   ON fx.rates(from_cur, to_cur);
		CREATE INDEX IF NOT EXISTS idx_quotes_exp   ON fx.quotes(expires_at) WHERE locked = false;`

// migration002 supports idempotent quote locking and the expiry purge.
//
// locked_by is the caller's idempotency key: a retry that presents the same key
// must be recognised as the original locker rather than rejected, so the lookup
// needs to be indexed. The purge scans expired unlocked quotes, which nothing
// previously removed.
const migration002 = `
CREATE INDEX IF NOT EXISTS idx_quotes_locked_by
    ON fx.quotes (locked_by)
    WHERE locked_by IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_quotes_purge
    ON fx.quotes (expires_at)
    WHERE locked = FALSE;

-- Rates are market data: record when each was last written so a stale-rate
-- investigation has something to work from.
ALTER TABLE fx.rates
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();`
