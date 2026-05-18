// Package outbox implements the transactional outbox pattern.
//
// Domain events are written to an outbox table inside the same Postgres
// transaction as the business change. Debezium tails the WAL and publishes
// each row to Kafka, guaranteeing exactly-once delivery even if the
// application crashes immediately after commit.
//
// Services must NOT publish directly to Kafka — they must write an outbox
// Message and let Debezium forward it.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// EventType identifies the kind of domain event being published.
type EventType string

const (
	EventPaymentCompleted EventType = "payment.completed"
	EventPaymentFailed    EventType = "payment.failed"
	EventPayoutInitiated  EventType = "payout.initiated"
	EventPayoutFinalized  EventType = "payout.finalized"
	EventReconDrift       EventType = "recon.drift"
	EventHoldMatured      EventType = "hold.matured"
)

// Message represents one row in the outbox table.
// The schema must match the Debezium connector configuration exactly.
type Message struct {
	// ID is a UUID v4 unique per message — used for Kafka message key deduplication.
	ID uuid.UUID `db:"id" json:"id"`
	// AggregateID is the account_id — Debezium uses this as the Kafka partition key
	// so all events for one account are strictly ordered.
	AggregateID string `db:"aggregate_id" json:"aggregate_id"`
	// AggregateType names the root entity (e.g. "payment", "payout").
	AggregateType string `db:"aggregate_type" json:"aggregate_type"`
	// EventType identifies what happened.
	EventType EventType `db:"event_type" json:"event_type"`
	// Payload is the Avro-serialised event body stored as JSONB for debugging.
	Payload json.RawMessage `db:"payload" json:"payload"`
	// CreatedAt is set by the application (not the DB) to preserve nanosecond precision.
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// NewMessage constructs a Message, JSON-encoding payload.
// aggregateID must be the account_id so Kafka partitioning by account is correct.
func NewMessage(aggregateID, aggregateType string, eventType EventType, payload any) (Message, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("outbox.NewMessage: marshal payload: %w", err)
	}
	return Message{
		ID:            uuid.New(),
		AggregateID:   aggregateID,
		AggregateType: aggregateType,
		EventType:     eventType,
		Payload:       raw,
		CreatedAt:     time.Now().UTC(),
	}, nil
}

// Inserter is satisfied by any type that can persist an outbox Message
// inside a currently-open database transaction.
// Each service's sqlc-generated repository must implement this interface.
type Inserter interface {
	InsertOutboxMessage(ctx context.Context, msg Message) error
}
