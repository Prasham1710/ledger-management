package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
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
		CREATE INDEX IF NOT EXISTS idx_quotes_exp   ON fx.quotes(expires_at) WHERE locked = false;
	`)
	return err
}
