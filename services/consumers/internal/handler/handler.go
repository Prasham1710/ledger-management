package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/outbox"
	"github.com/prasham/ledger/services/consumers/internal/db"
)

// ErrMalformed marks a message that can never be processed. The consumer parks
// these in the dead-letter table and advances past them, because retrying a
// message that cannot be parsed blocks the partition forever.
var ErrMalformed = errors.New("malformed event")

// Handler dispatches Kafka messages to audit, analytics, and notification.
type Handler struct {
	repo    *db.Repo
	metrics *metrics.ConsumerMetrics
	log     zerolog.Logger
}

func New(repo *db.Repo, m *metrics.ConsumerMetrics, log zerolog.Logger) *Handler {
	return &Handler{repo: repo, metrics: m, log: log}
}

// Handle processes one message.
//
// The return value is meaningful, unlike the previous implementation which
// always returned nil: the consumer commits offsets only for messages that
// Handle reported success or ErrMalformed for. A transient failure (a database
// blip) therefore replays rather than being silently discarded.
func (h *Handler) Handle(ctx context.Context, topic string, value []byte) error {
	var env outbox.Envelope
	if err := json.Unmarshal(value, &env); err != nil {
		h.log.Error().Err(err).Str("topic", topic).Msg("undecodable event — dead-lettering")
		h.countFailure(topic, "decode")
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	// A structurally valid JSON document with none of our fields unmarshals
	// without error and leaves everything blank. That happened routinely when
	// Debezium's EventRouter published its own unwrapped shape to these topics,
	// producing audit rows with an empty event_type and a null payload and no log
	// line to explain them. Require the identifying fields explicitly.
	if err := validateEnvelope(env); err != nil {
		h.log.Error().Err(err).
			Str("topic", topic).
			Int("bytes", len(value)).
			Msg("event missing required envelope fields — dead-lettering")
		h.countFailure(topic, "invalid_envelope")
		return fmt.Errorf("%w: %w", ErrMalformed, err)
	}

	inserted, err := h.repo.InsertAuditEvent(ctx,
		env.ID, topic, env.EventType, env.AggregateID, env.AggregateType, env.Payload)
	if err != nil {
		// Transient. Returning the error keeps the offset uncommitted so the
		// message is retried — an audit record must not be lost.
		h.log.Error().Err(err).Str("event_id", env.ID).Msg("audit insert failed — will retry")
		h.countFailure(topic, "audit_insert")
		return fmt.Errorf("insert audit event: %w", err)
	}
	if !inserted {
		// Expected: delivery is at-least-once.
		h.log.Debug().Str("event_id", env.ID).Str("topic", topic).
			Msg("duplicate event suppressed")
		if h.metrics != nil {
			h.metrics.DuplicateTotal.WithLabelValues(topic).Inc()
			h.metrics.ConsumedTotal.WithLabelValues(topic, "duplicate").Inc()
		}
		return nil
	}

	if _, err := h.repo.IncrAnalyticsCounter(ctx, env.ID, env.EventType); err != nil {
		h.log.Error().Err(err).Str("event_id", env.ID).Msg("analytics counter failed — will retry")
		h.countFailure(topic, "analytics")
		return fmt.Errorf("increment analytics counter: %w", err)
	}

	h.notify(env)

	if h.metrics != nil {
		h.metrics.ConsumedTotal.WithLabelValues(topic, "success").Inc()
	}
	return nil
}

// validateEnvelope requires the fields the platform depends on.
func validateEnvelope(env outbox.Envelope) error {
	if env.ID == "" {
		// Without an id there is no deduplication key, so the event cannot be
		// processed safely at all.
		return errors.New("envelope id is empty")
	}
	if env.EventType == "" {
		return errors.New("event_type is empty")
	}
	if env.AggregateID == "" {
		return errors.New("aggregate_id is empty")
	}
	return nil
}

// notify emits operational notifications. Every subscribed event type is
// handled: payout.finalized and hold.matured were previously subscribed but had
// no case, so they arrived and produced nothing.
func (h *Handler) notify(env outbox.Envelope) {
	switch outbox.EventType(env.EventType) {
	case outbox.EventPaymentCompleted:
		h.log.Info().Str("aggregate_id", env.AggregateID).Msg("[notification] payment completed")
	case outbox.EventPaymentFailed:
		h.log.Warn().Str("aggregate_id", env.AggregateID).Msg("[notification] payment failed")
	case outbox.EventPaymentReversed:
		h.log.Warn().Str("aggregate_id", env.AggregateID).Msg("[notification] payment reversed")
	case outbox.EventPayoutInitiated:
		h.log.Info().Str("aggregate_id", env.AggregateID).Msg("[notification] payout initiated")
	case outbox.EventPayoutFinalized:
		h.log.Info().Str("aggregate_id", env.AggregateID).Msg("[notification] payout finalized")
	case outbox.EventPayoutFailed:
		h.log.Warn().Str("aggregate_id", env.AggregateID).Msg("[notification] payout failed")
	case outbox.EventHoldMatured:
		// A matured hold means a saga never finished; the funds are back but the
		// cause is worth investigating.
		h.log.Warn().Str("aggregate_id", env.AggregateID).
			RawJSON("payload", jsonOrNull(env.Payload)).
			Msg("[notification] hold expired and funds returned")
	case outbox.EventReconDrift:
		// The platform's most serious correctness signal.
		h.log.Error().Str("aggregate_id", env.AggregateID).
			RawJSON("payload", jsonOrNull(env.Payload)).
			Msg("[notification] RECON DRIFT DETECTED")
	default:
		h.log.Warn().Str("event_type", env.EventType).
			Msg("no notification handler for event type")
	}
}

// jsonOrNull guards RawJSON, which panics on empty input.
func jsonOrNull(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

func (h *Handler) countFailure(topic, reason string) {
	if h.metrics != nil {
		h.metrics.HandlerFailure.WithLabelValues(topic, reason).Inc()
	}
}
