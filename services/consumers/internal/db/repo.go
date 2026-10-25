package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// outboxSchemas are the schemas whose outbox_messages tables the relay polls.
// Every service that writes events must appear here, or its events are never
// published: the previous list held only transaction and payout, so the ledger
// and account outbox tables filled up and were never drained.
var outboxSchemas = []string{"transaction", "payout", "ledger", "account", "recon"}

// OutboxSchemas returns the schemas the relay polls.
func OutboxSchemas() []string {
	out := make([]string, len(outboxSchemas))
	copy(out, outboxSchemas)
	return out
}

// validSchema guards the schema name that is interpolated into SQL. Identifiers
// cannot be parameterised, so the value must come from this fixed set.
func validSchema(schema string) error {
	for _, s := range outboxSchemas {
		if s == schema {
			return nil
		}
	}
	return fmt.Errorf("unknown outbox schema %q", schema)
}

type Repo struct{ pool *pgxpool.Pool }

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// ── audit and analytics ───────────────────────────────────────────────────────

// InsertAuditEvent records a processed event. Returns false without error when
// eventID has already been recorded, which is the expected outcome of an
// at-least-once redelivery.
func (r *Repo) InsertAuditEvent(
	ctx context.Context,
	eventID, topic, eventType, aggregateID, aggregateType string,
	payload json.RawMessage,
) (inserted bool, err error) {
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO audit.events
		  (event_id, topic, event_type, aggregate_id, aggregate_type, payload)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (event_id) DO NOTHING`,
		eventID, topic, eventType, aggregateID, aggregateType, payload,
	)
	if err != nil {
		return false, fmt.Errorf("insert audit event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// IncrAnalyticsCounter increments the daily counter for eventType, exactly once
// per eventID.
//
// The claim on counted_events and the counter increment happen in one
// transaction, so a redelivery cannot inflate the count even if it interleaves
// with the original.
func (r *Repo) IncrAnalyticsCounter(ctx context.Context, eventID, eventType string) (counted bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin analytics tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op once committed

	tag, err := tx.Exec(ctx, `
		INSERT INTO analytics.counted_events (event_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (event_id) DO NOTHING`,
		eventID, eventType)
	if err != nil {
		return false, fmt.Errorf("claim analytics event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil // already counted
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO analytics.event_counts (event_type, count)
		VALUES ($1, 1)
		ON CONFLICT (event_type, period) DO UPDATE
		SET count = analytics.event_counts.count + 1`,
		eventType); err != nil {
		return false, fmt.Errorf("increment analytics counter: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit analytics tx: %w", err)
	}
	return true, nil
}

// InsertDeadLetter parks an event that could not be processed.
//
// Handler failures were previously logged and their offsets committed anyway,
// which silently dropped financial audit records. Parking the raw message keeps
// it recoverable.
func (r *Repo) InsertDeadLetter(
	ctx context.Context,
	topic string,
	partition int32,
	offset int64,
	eventID string,
	raw []byte,
	stage, errMsg string,
) error {
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO consumer.dead_letters
		  (topic, partition, kafka_offset, event_id, raw_value, failure_stage, error_msg)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		topic, partition, offset, eventID, raw, stage, truncate(errMsg, 2000),
	); err != nil {
		return fmt.Errorf("insert dead letter: %w", err)
	}
	return nil
}

// ── outbox relay ──────────────────────────────────────────────────────────────

// OutboxRow is one unpublished outbox message.
type OutboxRow struct {
	ID            string
	AggregateID   string
	AggregateType string
	EventType     string
	Payload       json.RawMessage
	Attempts      int
	CreatedAt     time.Time
}

// ClaimOutbox leases up to limit unpublished rows for the caller to publish.
//
// This replaces a query that read and DELETEd rows in the same statement, before
// Kafka had acknowledged anything — making delivery at-most-once and losing
// events permanently on any broker error.
//
// Rows are leased rather than removed: locked_until reserves them for this
// worker, and MarkPublished only clears them once the broker has confirmed the
// write. A worker that dies mid-publish therefore replays its batch when the
// lease expires, which is the at-least-once behaviour consumers deduplicate.
//
// FOR UPDATE SKIP LOCKED lets several relay replicas claim disjoint batches.
func (r *Repo) ClaimOutbox(ctx context.Context, schema string, limit int, lease time.Duration) ([]OutboxRow, error) {
	if err := validSchema(schema); err != nil {
		return nil, err
	}

	rows, err := r.pool.Query(ctx, fmt.Sprintf(`
		WITH claimed AS (
		    SELECT id
		    FROM %s.outbox_messages
		    WHERE published_at IS NULL
		      AND (locked_until IS NULL OR locked_until < NOW())
		    ORDER BY created_at
		    LIMIT $1
		    FOR UPDATE SKIP LOCKED
		)
		UPDATE %s.outbox_messages o
		SET locked_until = NOW() + $2::interval,
		    attempts     = o.attempts + 1
		WHERE o.id IN (SELECT id FROM claimed)
		RETURNING o.id, o.aggregate_id, o.aggregate_type, o.event_type,
		          o.payload, o.attempts, o.created_at`,
		pgx.Identifier{schema}.Sanitize(), pgx.Identifier{schema}.Sanitize()),
		limit, lease.String())
	if err != nil {
		// A service that has not yet run its migrations has no outbox table. That
		// is a normal startup ordering condition, not a relay fault, so report it
		// distinctly instead of erroring on every poll.
		if isUndefinedTable(err) {
			return nil, fmt.Errorf("%w: %s.outbox_messages", ErrOutboxMissing, schema)
		}
		return nil, fmt.Errorf("claim outbox rows in %s: %w", schema, err)
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var row OutboxRow
		if err := rows.Scan(&row.ID, &row.AggregateID, &row.AggregateType,
			&row.EventType, &row.Payload, &row.Attempts, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan outbox row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox rows: %w", err)
	}
	return out, nil
}

// MarkPublished records that the broker accepted these messages. Only called
// after a successful produce, so an unpublished row is never lost.
func (r *Repo) MarkPublished(ctx context.Context, schema string, ids []string) error {
	if err := validSchema(schema); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.outbox_messages
		SET published_at = NOW(), locked_until = NULL, last_error = ''
		WHERE id = ANY($1)`, pgx.Identifier{schema}.Sanitize()), ids); err != nil {
		return fmt.Errorf("mark outbox rows published in %s: %w", schema, err)
	}
	return nil
}

// ReleaseFailed clears the lease on rows that failed to publish and records why,
// so the next pass retries them promptly instead of waiting for lease expiry.
func (r *Repo) ReleaseFailed(ctx context.Context, schema string, ids []string, reason string) error {
	if err := validSchema(schema); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := r.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.outbox_messages
		SET locked_until = NULL, last_error = $2
		WHERE id = ANY($1)`, pgx.Identifier{schema}.Sanitize()),
		ids, truncate(reason, 1000)); err != nil {
		return fmt.Errorf("release failed outbox rows in %s: %w", schema, err)
	}
	return nil
}

// OutboxBacklog reports the number of unpublished rows and the age of the oldest,
// the two signals that reveal a stalled relay.
func (r *Repo) OutboxBacklog(ctx context.Context, schema string) (count int64, oldestAge time.Duration, err error) {
	if err := validSchema(schema); err != nil {
		return 0, 0, err
	}
	var oldest *time.Time
	if err := r.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT COUNT(*), MIN(created_at)
		FROM %s.outbox_messages
		WHERE published_at IS NULL`, pgx.Identifier{schema}.Sanitize()),
	).Scan(&count, &oldest); err != nil {
		if isUndefinedTable(err) {
			return 0, 0, fmt.Errorf("%w: %s.outbox_messages", ErrOutboxMissing, schema)
		}
		return 0, 0, fmt.Errorf("read outbox backlog for %s: %w", schema, err)
	}
	if oldest != nil {
		oldestAge = time.Since(*oldest)
	}
	return count, oldestAge, nil
}

// PurgePublished deletes published rows older than retain, keeping the outbox
// tables bounded while leaving a recent window available for debugging.
func (r *Repo) PurgePublished(ctx context.Context, schema string, retain time.Duration) (int64, error) {
	if err := validSchema(schema); err != nil {
		return 0, err
	}
	tag, err := r.pool.Exec(ctx, fmt.Sprintf(`
		DELETE FROM %s.outbox_messages
		WHERE published_at IS NOT NULL AND published_at < NOW() - $1::interval`,
		pgx.Identifier{schema}.Sanitize()), retain.String())
	if err != nil {
		if isUndefinedTable(err) {
			return 0, fmt.Errorf("%w: %s.outbox_messages", ErrOutboxMissing, schema)
		}
		return 0, fmt.Errorf("purge published outbox rows in %s: %w", schema, err)
	}
	return tag.RowsAffected(), nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…(truncated)"
}

// ErrOutboxMissing means a schema's outbox_messages table does not exist yet,
// which happens while its owning service is still starting up.
var ErrOutboxMissing = errors.New("outbox table does not exist")

// isUndefinedTable reports whether err is Postgres undefined_table (42P01).
func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return false
}
