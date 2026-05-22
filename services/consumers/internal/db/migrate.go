package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

const schema = `
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS analytics;

CREATE TABLE IF NOT EXISTS audit.events (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    topic         TEXT NOT NULL,
    event_type    TEXT NOT NULL,
    aggregate_id  TEXT NOT NULL,
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL DEFAULT '{}',
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_events_type ON audit.events(event_type);
CREATE INDEX IF NOT EXISTS idx_audit_events_agg  ON audit.events(aggregate_id);

CREATE TABLE IF NOT EXISTS analytics.event_counts (
    event_type   TEXT NOT NULL,
    period       DATE NOT NULL DEFAULT CURRENT_DATE,
    count        BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (event_type, period)
);
`

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schema)
	return err
}
