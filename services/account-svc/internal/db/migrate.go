package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE SCHEMA IF NOT EXISTS account;

		CREATE TABLE IF NOT EXISTS account.accounts (
			id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			owner_id     TEXT        NOT NULL,
			currency     TEXT        NOT NULL,
			status       TEXT        NOT NULL DEFAULT 'ACTIVE'
				CHECK (status IN ('ACTIVE','SUSPENDED','CLOSED')),
			balance      NUMERIC(28,8) NOT NULL DEFAULT 0 CHECK (balance >= 0),
			hold_balance NUMERIC(28,8) NOT NULL DEFAULT 0 CHECK (hold_balance >= 0),
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS account.holds (
			id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			account_id   UUID        NOT NULL REFERENCES account.accounts(id),
			amount       NUMERIC(28,8) NOT NULL CHECK (amount > 0),
			reference_id TEXT        NOT NULL,
			expires_at   TIMESTAMPTZ NOT NULL,
			created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS account.outbox_messages (
			id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
			aggregate_id   TEXT        NOT NULL,
			aggregate_type TEXT        NOT NULL,
			event_type     TEXT        NOT NULL,
			payload        JSONB       NOT NULL,
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE INDEX IF NOT EXISTS idx_accounts_owner
			ON account.accounts(owner_id);

		CREATE INDEX IF NOT EXISTS idx_holds_account
			ON account.holds(account_id);

		CREATE UNIQUE INDEX IF NOT EXISTS idx_accounts_idem
			ON account.accounts(owner_id, currency)
			WHERE status = 'ACTIVE';
	`)
	return err
}
