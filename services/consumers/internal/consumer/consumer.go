package consumer

import (
	"context"
	"strings"

	"github.com/rs/zerolog"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/prasham/ledger/services/consumers/internal/handler"
)

var subscribedTopics = []string{
	"payments.payment.completed",
	"payments.payment.failed",
	"payments.payout.initiated",
	"payments.payout.finalized",
	"payments.hold.matured",
	"payments.recon.drift",
}

type Consumer struct {
	client  *kgo.Client
	handler *handler.Handler
	log     zerolog.Logger
}

func New(brokers, groupID string, h *handler.Handler, log zerolog.Logger) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(brokers, ",")...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(subscribedTopics...),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, err
	}
	return &Consumer{client: cl, handler: h, log: log}, nil
}

func (c *Consumer) Run(ctx context.Context) {
	c.log.Info().Strs("topics", subscribedTopics).Msg("kafka consumer starting")
	for {
		fetches := c.client.PollFetches(ctx)
		if ctx.Err() != nil {
			break
		}
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, e := range errs {
				c.log.Error().Err(e.Err).Str("topic", e.Topic).Msg("fetch error")
			}
			continue
		}
		fetches.EachRecord(func(r *kgo.Record) {
			if err := c.handler.Handle(ctx, r.Topic, r.Value); err != nil {
				c.log.Error().Err(err).Str("topic", r.Topic).Msg("handler error")
			}
		})
		if err := c.client.CommitUncommittedOffsets(ctx); err != nil {
			c.log.Error().Err(err).Msg("commit failed")
		}
	}
	c.client.Close()
}
