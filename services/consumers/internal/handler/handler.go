package handler

import (
	"context"
	"encoding/json"

	"github.com/rs/zerolog"

	"github.com/prasham/ledger/services/consumers/internal/db"
)

// Envelope is the JSON structure published by the outbox relay.
type Envelope struct {
	ID            string          `json:"id"`
	AggregateID   string          `json:"aggregate_id"`
	AggregateType string          `json:"aggregate_type"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
}

// Handler dispatches Kafka messages to audit, analytics, and notification handlers.
type Handler struct {
	repo *db.Repo
	log  zerolog.Logger
}

func New(repo *db.Repo, log zerolog.Logger) *Handler {
	return &Handler{repo: repo, log: log}
}

func (h *Handler) Handle(ctx context.Context, topic string, value []byte) error {
	var env Envelope
	if err := json.Unmarshal(value, &env); err != nil {
		h.log.Error().Err(err).Str("topic", topic).Msg("bad envelope — skipping")
		return nil // don't retry malformed messages
	}

	h.audit(ctx, topic, env)
	h.analytics(ctx, env)
	h.notify(env)
	return nil
}

func (h *Handler) audit(ctx context.Context, topic string, env Envelope) {
	if err := h.repo.InsertAuditEvent(ctx, topic, env.EventType, env.AggregateID, env.AggregateType, env.Payload); err != nil {
		h.log.Error().Err(err).Str("event_type", env.EventType).Msg("audit insert failed")
	}
}

func (h *Handler) analytics(ctx context.Context, env Envelope) {
	if err := h.repo.IncrAnalyticsCounter(ctx, env.EventType); err != nil {
		h.log.Error().Err(err).Str("event_type", env.EventType).Msg("analytics counter failed")
	}
}

func (h *Handler) notify(env Envelope) {
	switch env.EventType {
	case "payment.completed":
		h.log.Info().Str("aggregate_id", env.AggregateID).Msg("[notification] payment completed")
	case "payment.failed":
		h.log.Warn().Str("aggregate_id", env.AggregateID).Msg("[notification] payment FAILED")
	case "payout.initiated":
		h.log.Info().Str("aggregate_id", env.AggregateID).Msg("[notification] payout initiated")
	case "recon.drift":
		h.log.Error().Str("aggregate_id", env.AggregateID).RawJSON("payload", env.Payload).Msg("[notification] RECON DRIFT DETECTED")
	}
}
