package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/prasham/ledger/services/consumers/internal/db"
)

// eventTopicMap maps event_type values written by services to Kafka topic names.
var eventTopicMap = map[string]string{
	"payment.completed": "payments.payment.completed",
	"payment.failed":    "payments.payment.failed",
	"payout.initiated":  "payments.payout.initiated",
	"payout.finalized":  "payments.payout.finalized",
	"recon.drift":       "payments.recon.drift",
	"hold.matured":      "payments.hold.matured",
}

// outboxSchemas are the Postgres schemas whose outbox_messages tables we poll.
var outboxSchemas = []string{"transaction", "payout"}

type OutboxRelay struct {
	repo     *db.Repo
	producer *kgo.Client
	interval time.Duration
	log      zerolog.Logger
}

func NewOutboxRelay(repo *db.Repo, producer *kgo.Client, interval time.Duration, log zerolog.Logger) *OutboxRelay {
	return &OutboxRelay{repo: repo, producer: producer, interval: interval, log: log}
}

func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, schema := range outboxSchemas {
				r.relay(ctx, schema)
			}
		}
	}
}

func (r *OutboxRelay) relay(ctx context.Context, schema string) {
	rows, err := r.repo.PollOutbox(ctx, schema, 100)
	if err != nil {
		r.log.Error().Err(err).Str("schema", schema).Msg("outbox poll failed")
		return
	}
	if len(rows) == 0 {
		return
	}

	var records []*kgo.Record
	for _, row := range rows {
		topic, ok := eventTopicMap[row.EventType]
		if !ok {
			r.log.Warn().Str("event_type", row.EventType).Msg("no topic mapping — skipping")
			continue
		}
		envelope, _ := json.Marshal(map[string]any{
			"id":             row.ID,
			"aggregate_id":   row.AggregateID,
			"aggregate_type": row.AggregateType,
			"event_type":     row.EventType,
			"payload":        row.Payload,
		})
		records = append(records, &kgo.Record{
			Topic: topic,
			Key:   []byte(row.AggregateID),
			Value: envelope,
		})
	}

	if len(records) == 0 {
		return
	}

	results := r.producer.ProduceSync(ctx, records...)
	for _, res := range results {
		if res.Err != nil {
			r.log.Error().Err(res.Err).Msg("kafka produce failed")
		}
	}
	r.log.Info().Str("schema", schema).Int("published", len(records)).Msg("outbox relayed")
}
