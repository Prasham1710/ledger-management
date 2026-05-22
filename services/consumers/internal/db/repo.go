package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// InsertAuditEvent writes a processed Kafka event to audit.events.
func (r *Repo) InsertAuditEvent(ctx context.Context, topic, eventType, aggregateID, aggregateType string, payload json.RawMessage) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO audit.events (topic, event_type, aggregate_id, aggregate_type, payload)
		 VALUES ($1, $2, $3, $4, $5)`,
		topic, eventType, aggregateID, aggregateType, payload,
	)
	return err
}

// IncrAnalyticsCounter upserts a daily event counter.
func (r *Repo) IncrAnalyticsCounter(ctx context.Context, eventType string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO analytics.event_counts (event_type, count)
		 VALUES ($1, 1)
		 ON CONFLICT (event_type, period) DO UPDATE
		 SET count = analytics.event_counts.count + 1`,
		eventType,
	)
	return err
}

// OutboxRow represents a single unprocessed outbox message.
type OutboxRow struct {
	ID            string
	AggregateID   string
	AggregateType string
	EventType     string
	Payload       json.RawMessage
}

// PollOutbox fetches up to limit unprocessed rows from the named schema's
// outbox_messages table and returns them. Rows are deleted in the same call
// using a CTE so the relay is effectively at-least-once.
func (r *Repo) PollOutbox(ctx context.Context, schema string, limit int) ([]OutboxRow, error) {
	rows, err := r.pool.Query(ctx,
		`WITH claimed AS (
		   SELECT id, aggregate_id, aggregate_type, event_type, payload
		   FROM `+schema+`.outbox_messages
		   ORDER BY created_at
		   LIMIT $1
		   FOR UPDATE SKIP LOCKED
		 ), deleted AS (
		   DELETE FROM `+schema+`.outbox_messages
		   WHERE id IN (SELECT id FROM claimed)
		 )
		 SELECT id, aggregate_id, aggregate_type, event_type, payload FROM claimed`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var row OutboxRow
		if err := rows.Scan(&row.ID, &row.AggregateID, &row.AggregateType, &row.EventType, &row.Payload); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
