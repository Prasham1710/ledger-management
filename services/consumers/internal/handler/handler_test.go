package handler_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/outbox"
)

// These tests pin the envelope contract, which is where the silent corruption
// happened: a Debezium-shaped message unmarshalled into outbox.Envelope without
// error and left every field blank, so audit rows were written with an empty
// event_type and a null payload and nothing logged an error.

func TestEnvelope_RelayShapeDecodes(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"id": "01234567-89ab-cdef-0123-456789abcdef",
		"aggregate_id": "txn-1",
		"aggregate_type": "transaction",
		"event_type": "payment.completed",
		"payload": {"transaction_id":"txn-1","amount":"100.00"}
	}`)

	var env outbox.Envelope
	require.NoError(t, json.Unmarshal(raw, &env))

	assert.Equal(t, "01234567-89ab-cdef-0123-456789abcdef", env.ID)
	assert.Equal(t, "txn-1", env.AggregateID)
	assert.Equal(t, "transaction", env.AggregateType)
	assert.Equal(t, "payment.completed", env.EventType)
	assert.JSONEq(t, `{"transaction_id":"txn-1","amount":"100.00"}`, string(env.Payload))
}

// This is the failure mode the envelope validation now catches: valid JSON that
// carries none of the fields the platform needs.
func TestEnvelope_ForeignShapeDecodesToBlankFields(t *testing.T) {
	t.Parallel()

	// The unwrapped payload a Debezium EventRouter would publish.
	raw := []byte(`{"transaction_id":"txn-1","amount":"100.00","currency":"INR"}`)

	var env outbox.Envelope
	// Unmarshal succeeds — which is precisely why an explicit field check is
	// required rather than relying on a decode error.
	require.NoError(t, json.Unmarshal(raw, &env),
		"a foreign shape decodes without error, so the handler must validate fields explicitly")

	assert.Empty(t, env.ID, "no dedup key")
	assert.Empty(t, env.EventType)
	assert.Empty(t, env.AggregateID)
}

func TestTopicFor_CoversEveryEventType(t *testing.T) {
	t.Parallel()

	// Every event type a service can emit must have a topic, or the relay parks
	// the row and the event is never delivered.
	eventTypes := []outbox.EventType{
		outbox.EventPaymentCompleted,
		outbox.EventPaymentFailed,
		outbox.EventPaymentReversed,
		outbox.EventPayoutInitiated,
		outbox.EventPayoutFinalized,
		outbox.EventPayoutFailed,
		outbox.EventReconDrift,
		outbox.EventHoldMatured,
	}

	for _, et := range eventTypes {
		topic, ok := outbox.TopicFor(string(et))
		assert.True(t, ok, "event type %q has no topic mapping", et)
		assert.NotEmpty(t, topic)
	}
}

func TestTopicFor_UnknownTypeIsReported(t *testing.T) {
	t.Parallel()

	_, ok := outbox.TopicFor("something.unmapped")
	assert.False(t, ok, "an unmapped event type must be reported, not silently dropped")
}

// The relay publishes to exactly the topics the consumer subscribes to. Deriving
// both from one registry is what keeps them from drifting apart.
func TestTopics_MatchTopicForOutput(t *testing.T) {
	t.Parallel()

	topics := outbox.Topics()
	require.NotEmpty(t, topics)

	seen := make(map[string]bool, len(topics))
	for _, tp := range topics {
		assert.False(t, seen[tp], "duplicate topic %q", tp)
		seen[tp] = true
	}

	for _, et := range []outbox.EventType{
		outbox.EventPaymentCompleted,
		outbox.EventPayoutInitiated,
		outbox.EventHoldMatured,
		outbox.EventReconDrift,
	} {
		topic, ok := outbox.TopicFor(string(et))
		require.True(t, ok)
		assert.True(t, seen[topic],
			"topic %q for %q is published but not in the subscription list", topic, et)
	}
}
