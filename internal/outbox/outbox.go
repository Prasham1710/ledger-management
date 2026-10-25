// Package outbox implements the transactional outbox pattern.
//
// Domain events are written to an outbox table inside the same Postgres
// transaction as the business change, so an event exists if and only if the
// business change committed. A relay process (services/consumers) then polls
// unpublished rows, produces them to Kafka, and only marks them published after
// the broker acknowledges the write.
//
// Delivery semantics are at-least-once, deliberately:
//
//   - A crash between the Kafka ack and the database update replays the event.
//     Consumers therefore MUST deduplicate on Message.ID, which is carried in
//     the envelope for exactly that purpose.
//   - The reverse order (mark published, then produce) would be at-most-once and
//     would silently drop events whenever the broker was unavailable.
//
// There is exactly one publication path. An earlier design also routed these
// tables through Debezium's EventRouter, which produced to the same topics from
// the WAL: every event was delivered twice, in two different envelope shapes,
// and the relay's row deletion raced the WAL reader. Debezium no longer
// subscribes to the outbox tables — do not reintroduce a second publisher.
//
// Services must NOT produce to Kafka directly; they write a Message inside
// their business transaction and let the relay forward it.
package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

// EventType identifies the kind of domain event being published.
type EventType string

// Domain event types published by the platform. Each must have a topic in the
// registry below, or the relay parks the row rather than dropping it.
const (
	EventPaymentCompleted EventType = "payment.completed"
	EventPaymentFailed    EventType = "payment.failed"
	// EventPaymentReversed is emitted when a completed payment is compensated
	// after the fact, including its ledger reversal.
	EventPaymentReversed EventType = "payment.reversed"
	EventPayoutInitiated EventType = "payout.initiated"
	EventPayoutFinalized EventType = "payout.finalized"
	EventPayoutFailed    EventType = "payout.failed"
	EventReconDrift      EventType = "recon.drift"
	// EventHoldMatured is emitted by the hold sweeper when a hold outlives its
	// expiry and its funds are returned to available balance.
	EventHoldMatured EventType = "hold.matured"
)

// Message represents one row in an outbox table. Every service's
// outbox_messages table shares this shape so the relay can poll them uniformly.
type Message struct {
	// ID uniquely identifies this message for its whole lifetime. Consumers
	// deduplicate on it, which is what makes at-least-once delivery safe.
	ID uuid.UUID `db:"id" json:"id"`
	// AggregateID is the entity the event concerns. The relay uses it as the
	// Kafka partition key, so events for one aggregate stay strictly ordered.
	AggregateID string `db:"aggregate_id" json:"aggregate_id"`
	// AggregateType names the root entity (e.g. "payment", "payout").
	AggregateType string `db:"aggregate_type" json:"aggregate_type"`
	// EventType identifies what happened.
	EventType EventType `db:"event_type" json:"event_type"`
	// Payload is the event body, stored as JSONB.
	Payload json.RawMessage `db:"payload" json:"payload"`
	// CreatedAt is set by the application (not the DB) to preserve nanosecond precision.
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// Envelope is the wire format the relay produces to Kafka and consumers parse.
// ID is the deduplication key; it is the same value as the originating
// Message.ID, so a replayed row is recognisable as a duplicate.
type Envelope struct {
	ID            string          `json:"id"`
	AggregateID   string          `json:"aggregate_id"`
	AggregateType string          `json:"aggregate_type"`
	EventType     string          `json:"event_type"`
	Payload       json.RawMessage `json:"payload"`
}

// TopicFor maps an event type to its Kafka topic. Returning false for unknown
// types lets the relay surface a misconfiguration instead of dropping the row.
func TopicFor(eventType string) (string, bool) {
	topic, ok := topics[eventType]
	return topic, ok
}

// topics is the authoritative event-type → topic mapping, shared by the relay
// (which publishes) and the consumer (which subscribes) so the two cannot drift.
var topics = map[string]string{
	string(EventPaymentCompleted): "payments.payment.completed",
	string(EventPaymentFailed):    "payments.payment.failed",
	string(EventPaymentReversed):  "payments.payment.reversed",
	string(EventPayoutInitiated):  "payments.payout.initiated",
	string(EventPayoutFinalized):  "payments.payout.finalized",
	string(EventPayoutFailed):     "payments.payout.failed",
	string(EventReconDrift):       "payments.recon.drift",
	string(EventHoldMatured):      "payments.hold.matured",
}

// Topics returns every topic the relay may publish to, for consumer
// subscription. Sorted for stable ordering across processes.
func Topics() []string {
	out := make([]string, 0, len(topics))
	for _, t := range topics {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
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
