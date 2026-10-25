// Package relay publishes transactional-outbox rows to Kafka.
//
// Ordering is the whole point of this package. The relay claims a batch,
// produces it, and only marks rows published after the broker acknowledges the
// write:
//
//	claim → produce → await ack → mark published
//
// The previous implementation read and DELETEd rows in a single statement and
// then produced, logging any broker error. That is at-most-once: a Kafka hiccup
// destroyed the event with no trace, which defeats the purpose of an outbox.
//
// The cost of the correct ordering is that a crash between the ack and the
// database update replays those messages. That is why consumers deduplicate on
// the envelope id.
package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/outbox"
	"github.com/prasham/ledger/services/consumers/internal/db"
)

// Config controls relay behaviour.
type Config struct {
	// Interval between polls.
	Interval time.Duration
	// BatchSize bounds how many rows one pass claims per schema.
	BatchSize int
	// Lease is how long a claimed batch is reserved. It must exceed the time a
	// produce can take, or another replica may publish the same rows
	// concurrently — harmless (consumers deduplicate) but wasteful.
	Lease time.Duration
	// PurgeRetention is how long published rows are kept before deletion.
	// Retaining a window keeps recent events available for debugging.
	PurgeRetention time.Duration
	// PurgeInterval is how often published rows are purged.
	PurgeInterval time.Duration
}

func (c Config) withDefaults() Config {
	if c.Interval <= 0 {
		c.Interval = time.Second
	}
	if c.BatchSize <= 0 {
		c.BatchSize = 100
	}
	if c.Lease <= 0 {
		c.Lease = 30 * time.Second
	}
	if c.PurgeRetention <= 0 {
		c.PurgeRetention = 24 * time.Hour
	}
	if c.PurgeInterval <= 0 {
		c.PurgeInterval = time.Hour
	}
	return c
}

// OutboxRelay publishes outbox rows to Kafka.
type OutboxRelay struct {
	repo      *db.Repo
	producer  *kgo.Client
	cfg       Config
	metrics   *metrics.OutboxMetrics
	log       zerolog.Logger
	schemas   []string
	lastPurge time.Time
}

// NewOutboxRelay builds a relay. metrics may be nil to disable instrumentation.
func NewOutboxRelay(
	repo *db.Repo,
	producer *kgo.Client,
	cfg Config,
	m *metrics.OutboxMetrics,
	log zerolog.Logger,
) *OutboxRelay {
	return &OutboxRelay{
		repo:     repo,
		producer: producer,
		cfg:      cfg.withDefaults(),
		metrics:  m,
		log:      log,
		schemas:  db.OutboxSchemas(),
	}
}

// Run polls until ctx is cancelled.
func (r *OutboxRelay) Run(ctx context.Context) {
	r.log.Info().
		Dur("interval", r.cfg.Interval).
		Int("batch_size", r.cfg.BatchSize).
		Strs("schemas", r.schemas).
		Msg("outbox relay started")

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.log.Info().Msg("outbox relay stopped")
			return
		case <-ticker.C:
			for _, schema := range r.schemas {
				// Keep draining while batches come back full, so a backlog
				// clears quickly instead of one batch per tick.
				for {
					published := r.relay(ctx, schema)
					if published < r.cfg.BatchSize || ctx.Err() != nil {
						break
					}
				}
				r.reportBacklog(ctx, schema)
			}
			r.maybePurge(ctx)
		}
	}
}

// relay claims, publishes, and marks one batch. Returns how many rows it handled.
func (r *OutboxRelay) relay(ctx context.Context, schema string) int {
	rows, err := r.repo.ClaimOutbox(ctx, schema, r.cfg.BatchSize, r.cfg.Lease)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		if errors.Is(err, db.ErrOutboxMissing) {
			// The owning service has not migrated yet. Expected during a cold
			// start, so log at debug rather than treating it as a fault.
			r.log.Debug().Str("schema", schema).Msg("outbox table not present yet — skipping")
			return 0
		}
		r.log.Error().Err(err).Str("schema", schema).Msg("claiming outbox rows failed")
		r.countFailure(schema, "claim")
		return 0
	}
	if len(rows) == 0 {
		return 0
	}

	records := make([]*kgo.Record, 0, len(rows))
	recordIDs := make([]string, 0, len(rows))
	var unroutable []string

	for _, row := range rows {
		topic, ok := outbox.TopicFor(row.EventType)
		if !ok {
			// An unmapped event type is a configuration error, not a transient
			// fault: retrying forever would block the backlog behind it. Park it
			// so the rest of the batch proceeds.
			r.log.Error().
				Str("schema", schema).
				Str("event_type", row.EventType).
				Str("outbox_id", row.ID).
				Msg("no Kafka topic mapped for event type — parking row; add it to internal/outbox topics")
			unroutable = append(unroutable, row.ID)
			r.countFailure(schema, "unmapped_event_type")
			continue
		}

		envelope, err := json.Marshal(outbox.Envelope{
			// ID is the consumer's deduplication key, which is what makes
			// at-least-once delivery safe.
			ID:            row.ID,
			AggregateID:   row.AggregateID,
			AggregateType: row.AggregateType,
			EventType:     row.EventType,
			Payload:       row.Payload,
		})
		if err != nil {
			r.log.Error().Err(err).Str("outbox_id", row.ID).Msg("encoding envelope failed — parking row")
			unroutable = append(unroutable, row.ID)
			r.countFailure(schema, "encode")
			continue
		}

		records = append(records, &kgo.Record{
			Topic: topic,
			// Keying by aggregate keeps that aggregate's events in one partition
			// and therefore strictly ordered.
			Key:   []byte(row.AggregateID),
			Value: envelope,
		})
		recordIDs = append(recordIDs, row.ID)
	}

	if len(unroutable) > 0 {
		if err := r.repo.ReleaseFailed(context.WithoutCancel(ctx), schema, unroutable,
			"no topic mapping or unencodable payload"); err != nil {
			r.log.Error().Err(err).Str("schema", schema).Msg("releasing unroutable rows failed")
		}
	}

	if len(records) == 0 {
		return len(rows)
	}

	// Produce and wait for broker acknowledgement before touching the database.
	results := r.producer.ProduceSync(ctx, records...)

	var publishedIDs, failedIDs []string
	var firstErr error
	for i, res := range results {
		// results are returned in the order the records were supplied.
		id := recordIDs[i]
		if res.Err != nil {
			failedIDs = append(failedIDs, id)
			if firstErr == nil {
				firstErr = res.Err
			}
			continue
		}
		publishedIDs = append(publishedIDs, id)
		if r.metrics != nil {
			r.metrics.PublishedTotal.WithLabelValues(schema, records[i].Topic).Inc()
		}
	}

	// Detach: the rows were accepted by the broker, so recording that fact must
	// not be skipped because the relay is shutting down. Failing to mark them
	// would republish them, which consumers tolerate but is avoidable.
	markCtx := context.WithoutCancel(ctx)

	if len(publishedIDs) > 0 {
		if err := r.repo.MarkPublished(markCtx, schema, publishedIDs); err != nil {
			// Published but not marked: they will be republished and deduplicated
			// downstream. Safe, but worth surfacing.
			r.log.Error().Err(err).
				Str("schema", schema).
				Int("count", len(publishedIDs)).
				Msg("marking rows published failed — they will be republished and deduplicated")
			r.countFailure(schema, "mark_published")
		}
	}

	if len(failedIDs) > 0 {
		reason := "unknown produce error"
		if firstErr != nil {
			reason = firstErr.Error()
		}
		r.log.Error().
			Err(firstErr).
			Str("schema", schema).
			Int("failed", len(failedIDs)).
			Msg("Kafka produce failed — rows retained for retry")
		r.countFailure(schema, produceFailureReason(firstErr))

		if err := r.repo.ReleaseFailed(markCtx, schema, failedIDs, reason); err != nil {
			r.log.Error().Err(err).Str("schema", schema).
				Msg("releasing failed rows failed — they retry after lease expiry")
		}
	}

	if len(publishedIDs) > 0 {
		r.log.Debug().Str("schema", schema).Int("published", len(publishedIDs)).Msg("outbox relayed")
	}
	return len(rows)
}

// reportBacklog updates the gauges that reveal a stalled relay.
func (r *OutboxRelay) reportBacklog(ctx context.Context, schema string) {
	if r.metrics == nil {
		return
	}
	count, oldestAge, err := r.repo.OutboxBacklog(ctx, schema)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, db.ErrOutboxMissing) {
			r.log.Debug().Err(err).Str("schema", schema).Msg("reading outbox backlog failed")
		}
		return
	}
	r.metrics.Backlog.WithLabelValues(schema).Set(float64(count))
	r.metrics.OldestAgeSecs.WithLabelValues(schema).Set(oldestAge.Seconds())
}

// maybePurge deletes published rows past the retention window.
func (r *OutboxRelay) maybePurge(ctx context.Context) {
	if time.Since(r.lastPurge) < r.cfg.PurgeInterval {
		return
	}
	r.lastPurge = time.Now()

	for _, schema := range r.schemas {
		deleted, err := r.repo.PurgePublished(ctx, schema, r.cfg.PurgeRetention)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, db.ErrOutboxMissing) {
				r.log.Warn().Err(err).Str("schema", schema).Msg("purging published outbox rows failed")
			}
			continue
		}
		if deleted > 0 {
			r.log.Info().Str("schema", schema).Int64("deleted", deleted).
				Msg("purged published outbox rows")
		}
	}
}

func (r *OutboxRelay) countFailure(schema, reason string) {
	if r.metrics != nil {
		r.metrics.FailuresTotal.WithLabelValues(schema, reason).Inc()
	}
}

// produceFailureReason labels a produce error for metrics without letting broker
// messages become unbounded label cardinality.
func produceFailureReason(err error) string {
	switch {
	case err == nil:
		return "unknown"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, kgo.ErrRecordTimeout):
		return "record_timeout"
	case errors.Is(err, kgo.ErrClientClosed):
		return "client_closed"
	default:
		return "produce_error"
	}
}

// Ping verifies the broker is reachable, for readiness checks.
func (r *OutboxRelay) Ping(ctx context.Context) error {
	if err := r.producer.Ping(ctx); err != nil {
		return fmt.Errorf("kafka unreachable: %w", err)
	}
	return nil
}
