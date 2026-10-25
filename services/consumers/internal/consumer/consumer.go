// Package consumer reads platform events from Kafka.
//
// The commit discipline here is what changed. Previously every fetched record
// was handed to the handler, errors were logged, and offsets were committed
// unconditionally — so a database blip during an audit insert permanently
// discarded that financial audit record.
//
// Now offsets advance only past records that were either processed successfully
// or are provably unprocessable (and therefore dead-lettered). A transient
// failure leaves the offset where it is, so the record is redelivered.
package consumer

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/outbox"
	"github.com/prasham/ledger/services/consumers/internal/db"
	"github.com/prasham/ledger/services/consumers/internal/handler"
)

// retryBackoff is how long the consumer pauses after a transient handler failure
// before refetching, so a struggling database is not hammered.
const retryBackoff = 2 * time.Second

type Consumer struct {
	client  *kgo.Client
	handler *handler.Handler
	repo    *db.Repo
	metrics *metrics.ConsumerMetrics
	log     zerolog.Logger
}

// New builds a consumer subscribed to every topic the relay publishes. The topic
// list comes from the shared registry in internal/outbox, so a publisher and its
// subscriber cannot drift apart.
func New(
	brokers []string,
	groupID string,
	h *handler.Handler,
	repo *db.Repo,
	m *metrics.ConsumerMetrics,
	log zerolog.Logger,
) (*Consumer, error) {
	topics := outbox.Topics()

	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(topics...),
		// Manual commits: the whole point is to commit only what was processed.
		kgo.DisableAutoCommit(),
		// Start from the beginning for a new group so historical events are not
		// skipped on first deploy.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// Rebalance callbacks let us commit what we have before losing partitions.
		kgo.BlockRebalanceOnPoll(),
	)
	if err != nil {
		return nil, errors.Join(errors.New("create kafka consumer"), err)
	}

	log.Info().Strs("topics", topics).Str("group", groupID).Msg("kafka consumer configured")
	return &Consumer{client: cl, handler: h, repo: repo, metrics: m, log: log}, nil
}

// Run consumes until ctx is cancelled.
func (c *Consumer) Run(ctx context.Context) {
	c.log.Info().Msg("kafka consumer starting")
	defer c.client.Close()

	for {
		if ctx.Err() != nil {
			return
		}

		fetches := c.client.PollRecords(ctx, 500)
		if ctx.Err() != nil {
			c.client.AllowRebalance()
			return
		}

		if errs := fetches.Errors(); len(errs) > 0 {
			retryable := false
			for _, e := range errs {
				if errors.Is(e.Err, context.Canceled) {
					continue
				}
				c.log.Error().Err(e.Err).
					Str("topic", e.Topic).
					Int32("partition", e.Partition).
					Msg("kafka fetch error")
				retryable = true
			}
			if retryable {
				c.client.AllowRebalance()
				sleep(ctx, retryBackoff)
				continue
			}
		}

		progress := c.processFetches(ctx, fetches)

		// Commit only the offsets for records we finished with. Records left
		// unprocessed keep their offset so they are redelivered.
		if progress.committable {
			if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
				// Not committing means reprocessing, which dedup makes safe.
				c.log.Error().Err(err).Msg("offset commit failed — records will be redelivered")
			}
		}

		c.client.AllowRebalance()

		if progress.transientFailure {
			// Back off before refetching the records we deliberately did not
			// commit, rather than spinning on a failing dependency.
			c.log.Warn().Dur("backoff", retryBackoff).
				Msg("transient handler failure — pausing before retry")
			sleep(ctx, retryBackoff)
		}
	}
}

// progress summarises what one poll accomplished.
type progress struct {
	// committable is true when at least one record was fully resolved and no
	// transient failure preceded an unresolved record in the same partition.
	committable bool
	// transientFailure is true when a record must be retried.
	transientFailure bool
}

// processFetches handles records partition by partition, stopping a partition at
// its first transient failure so ordering and at-least-once delivery hold.
func (c *Consumer) processFetches(ctx context.Context, fetches kgo.Fetches) progress {
	var p progress

	fetches.EachPartition(func(fp kgo.FetchTopicPartition) {
		for _, rec := range fp.Records {
			if ctx.Err() != nil {
				return
			}

			err := c.handler.Handle(ctx, rec.Topic, rec.Value)
			switch {
			case err == nil:
				// Processed. Mark it consumed so its offset can be committed.
				c.client.MarkCommitRecords(rec)
				p.committable = true

			case errors.Is(err, handler.ErrMalformed):
				// Provably unprocessable: park it and move on. Blocking the
				// partition on a message that can never succeed would stall every
				// later event behind it.
				c.deadLetter(ctx, rec, "malformed", err)
				c.client.MarkCommitRecords(rec)
				p.committable = true
				if c.metrics != nil {
					c.metrics.ConsumedTotal.WithLabelValues(rec.Topic, "dead_lettered").Inc()
				}

			default:
				// Transient. Do NOT mark this record: leaving its offset
				// uncommitted is what makes the retry happen. Stop processing this
				// partition so ordering is preserved.
				c.log.Error().Err(err).
					Str("topic", rec.Topic).
					Int32("partition", rec.Partition).
					Int64("offset", rec.Offset).
					Msg("handler failed — offset not advanced, will retry")
				p.transientFailure = true
				if c.metrics != nil {
					c.metrics.ConsumedTotal.WithLabelValues(rec.Topic, "retry").Inc()
				}
				return
			}
		}
	})

	return p
}

// deadLetter parks an unprocessable record for later inspection.
func (c *Consumer) deadLetter(ctx context.Context, rec *kgo.Record, stage string, cause error) {
	// Detached: the record is being skipped, so preserving it must not be lost to
	// a cancelled context.
	if err := c.repo.InsertDeadLetter(context.WithoutCancel(ctx),
		rec.Topic, rec.Partition, rec.Offset, "", rec.Value, stage, cause.Error()); err != nil {
		// If even the dead-letter write fails, log the payload so it is at least
		// recoverable from logs.
		c.log.Error().Err(err).
			Str("topic", rec.Topic).
			Int64("offset", rec.Offset).
			Bytes("raw_value", rec.Value).
			Msg("dead-letter insert failed — payload logged as last resort")
		return
	}
	c.log.Warn().
		Str("topic", rec.Topic).
		Int32("partition", rec.Partition).
		Int64("offset", rec.Offset).
		Msg("record dead-lettered")
}

// Ping verifies broker reachability, for readiness checks.
func (c *Consumer) Ping(ctx context.Context) error {
	return c.client.Ping(ctx) //nolint:wrapcheck // surfaced verbatim in the probe body
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
