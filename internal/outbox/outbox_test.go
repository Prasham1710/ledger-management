package outbox_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/outbox"
)

type paymentPayload struct {
	TransactionID string `json:"transaction_id"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
}

func TestNewMessage(t *testing.T) {
	t.Parallel()

	payload := paymentPayload{
		TransactionID: "txn-001",
		Amount:        "500.0000",
		Currency:      "INR",
	}

	msg, err := outbox.NewMessage(
		"account-uuid-123",
		"payment",
		outbox.EventPaymentCompleted,
		payload,
	)

	require.NoError(t, err)
	assert.NotNil(t, msg.ID)
	assert.Equal(t, "account-uuid-123", msg.AggregateID)
	assert.Equal(t, "payment", msg.AggregateType)
	assert.Equal(t, outbox.EventPaymentCompleted, msg.EventType)
	assert.False(t, msg.CreatedAt.IsZero())

	// Payload must round-trip through JSON.
	var got paymentPayload
	require.NoError(t, json.Unmarshal(msg.Payload, &got))
	assert.Equal(t, payload, got)
}

func TestNewMessage_UniqueIDs(t *testing.T) {
	t.Parallel()

	msg1, _ := outbox.NewMessage("acc1", "payment", outbox.EventPaymentCompleted, struct{}{})
	msg2, _ := outbox.NewMessage("acc1", "payment", outbox.EventPaymentCompleted, struct{}{})

	assert.NotEqual(t, msg1.ID, msg2.ID, "each message must have a unique UUID")
}

func TestNewMessage_UnmarshallablePayload(t *testing.T) {
	t.Parallel()

	// Channels cannot be marshalled to JSON.
	_, err := outbox.NewMessage("acc", "payment", outbox.EventPaymentCompleted, make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outbox.NewMessage")
}

func TestEventTypes(t *testing.T) {
	t.Parallel()

	// All event type constants must be non-empty strings.
	events := []outbox.EventType{
		outbox.EventPaymentCompleted,
		outbox.EventPaymentFailed,
		outbox.EventPayoutInitiated,
		outbox.EventPayoutFinalized,
		outbox.EventReconDrift,
		outbox.EventHoldMatured,
	}
	for _, e := range events {
		assert.NotEmpty(t, string(e))
	}
}
