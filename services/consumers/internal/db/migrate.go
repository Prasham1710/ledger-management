package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/migrate"
)

// Schema is the Postgres schema owned by the consumers service. The audit and
// analytics tables live in their own schemas, created by migration 1.
const Schema = "consumer"

// Migrate applies all consumers migrations. Safe to call concurrently from
// multiple replicas: migrate.Run serialises on a Postgres advisory lock.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return migrate.Run(ctx, pool, Schema, migrations)
}

// migrations is append-only. Never edit a shipped migration.
var migrations = []migrate.Migration{
	{Version: 1, Name: "baseline_audit_and_analytics", SQL: migration001},
	{Version: 2, Name: "event_dedup_and_dlq", SQL: migration002},
}

// migration001 is the historical baseline, written with IF NOT EXISTS so
// databases created by the pre-versioning bootstrap adopt it as a no-op.
const migration001 = `
CREATE SCHEMA IF NOT EXISTS audit;
CREATE SCHEMA IF NOT EXISTS analytics;

CREATE TABLE IF NOT EXISTS audit.events (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    topic          TEXT        NOT NULL,
    event_type     TEXT        NOT NULL,
    aggregate_id   TEXT        NOT NULL,
    aggregate_type TEXT        NOT NULL DEFAULT '',
    payload        JSONB       NOT NULL DEFAULT '{}',
    processed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_events_type ON audit.events(event_type);
CREATE INDEX IF NOT EXISTS idx_audit_events_agg  ON audit.events(aggregate_id);

CREATE TABLE IF NOT EXISTS analytics.event_counts (
    event_type TEXT   NOT NULL,
    period     DATE   NOT NULL DEFAULT CURRENT_DATE,
    count      BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (event_type, period)
);`

// migration002 adds deduplication and a dead-letter table.
//
// Delivery is at-least-once by design, so the same event can arrive more than
// once — and it reliably did, because two publishers (the polling relay and
// Debezium's EventRouter) were producing to the same topics. With no dedup key,
// every audit row was duplicated and every analytics counter double-counted.
//
// event_id carries the originating outbox message id. The unique index makes a
// redelivery a no-op rather than a second record.
//
// The dead-letter table exists because handler failures were previously logged
// and then had their offsets committed anyway, silently discarding financial
// audit records. Events that cannot be processed now land here instead.
const migration002 = `
ALTER TABLE audit.events
    ADD COLUMN IF NOT EXISTS event_id TEXT;

-- Backfill existing rows with a synthetic id so the unique index can be built.
UPDATE audit.events SET event_id = id::text WHERE event_id IS NULL;

ALTER TABLE audit.events
    ALTER COLUMN event_id SET NOT NULL;

-- The deduplication key: one audit row per originating outbox message.
CREATE UNIQUE INDEX IF NOT EXISTS uq_audit_events_event_id
    ON audit.events (event_id);

-- Analytics counters need the same key, or a redelivery inflates the count even
-- though the audit insert was suppressed.
CREATE TABLE IF NOT EXISTS analytics.counted_events (
    event_id   TEXT        NOT NULL PRIMARY KEY,
    event_type TEXT        NOT NULL,
    counted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS consumer.dead_letters (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    topic         TEXT        NOT NULL,
    partition     INT         NOT NULL DEFAULT -1,
    kafka_offset  BIGINT      NOT NULL DEFAULT -1,
    event_id      TEXT        NOT NULL DEFAULT '',
    raw_value     BYTEA       NOT NULL,
    failure_stage TEXT        NOT NULL,
    error_msg     TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dead_letters_topic
    ON consumer.dead_letters (topic, created_at DESC);`
