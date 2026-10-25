package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/consumers/internal/config"
	"github.com/prasham/ledger/services/consumers/internal/consumer"
	"github.com/prasham/ledger/services/consumers/internal/db"
	"github.com/prasham/ledger/services/consumers/internal/handler"
	"github.com/prasham/ledger/services/consumers/internal/relay"
)

const serviceName = "consumers"

func main() {
	cfg := config.Load()

	logger := logging.New(logging.Config{
		ServiceName: serviceName,
		Level:       zerolog.InfoLevel,
		Pretty:      cfg.LogPretty,
	})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── Tracing ───────────────────────────────────────────────────────────────
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName:    serviceName,
		ServiceVersion: cfg.Version,
	}, cfg.OTLPEndpoint)
	if err != nil {
		logger.Fatal().Err(err).Msg("init tracing")
	}
	defer func() {
		flushCtx, flushCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer flushCancel()
		if err := shutdownTracing(flushCtx); err != nil {
			logger.Warn().Err(err).Msg("flush traces")
		}
	}()

	// ── Postgres ──────────────────────────────────────────────────────────────
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Msg("ping postgres")
	}

	if err := db.Migrate(ctx, pool); err != nil {
		logger.Fatal().Err(err).Msg("migrate")
	}
	logger.Info().Msg("migrations applied")

	repo := db.NewRepo(pool)

	// ── Metrics ───────────────────────────────────────────────────────────────
	reg := metrics.New()
	outboxMetrics := metrics.NewOutboxMetrics(reg)
	consumerMetrics := metrics.NewConsumerMetrics(reg)

	// ── Kafka producer (outbox relay) ─────────────────────────────────────────
	producer, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.KafkaBrokers...),
		// Wait for all in-sync replicas before acknowledging: the relay marks a
		// row published on the strength of this ack, so a weaker guarantee would
		// let a leader failover lose an already-"published" event.
		kgo.RequiredAcks(kgo.AllISRAcks()),
		// Idempotent writes are franz-go's default and are required for
		// AllISRAcks; they suppress broker-side duplicates from internal retries.
		// Bound how long a produce may hang so the relay cannot stall forever.
		kgo.RecordDeliveryTimeout(30*time.Second),
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("init kafka producer")
	}
	defer producer.Close()

	if err := producer.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Strs("brokers", cfg.KafkaBrokers).Msg("ping kafka")
	}

	// ── Outbox relay ──────────────────────────────────────────────────────────
	outboxRelay := relay.NewOutboxRelay(repo, producer, relay.Config{
		Interval:       cfg.RelayInterval,
		BatchSize:      cfg.RelayBatchSize,
		Lease:          cfg.RelayLease,
		PurgeRetention: cfg.PublishedRetention,
		PurgeInterval:  cfg.PurgeInterval,
	}, outboxMetrics, logger)

	// ── Kafka consumer group ──────────────────────────────────────────────────
	h := handler.New(repo, consumerMetrics, logger)
	cons, err := consumer.New(cfg.KafkaBrokers, cfg.KafkaGroupID, h, repo, consumerMetrics, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("init kafka consumer")
	}

	// ── Admin HTTP ────────────────────────────────────────────────────────────
	admin := serverkit.NewAdminServer(serverkit.AdminConfig{
		Addr:        ":" + cfg.MetricsPort,
		Registry:    reg,
		ServiceName: serviceName,
		Log:         logger,
		Checks: []serverkit.Check{
			serverkit.PostgresCheck(pool),
			{
				Name: "kafka",
				Func: func(ctx context.Context) error { return producer.Ping(ctx) },
			},
		},
	})
	admin.Start()

	// ── Start relay + consumer ────────────────────────────────────────────────
	go outboxRelay.Run(ctx)
	go cons.Run(ctx)

	logger.Info().
		Strs("brokers", cfg.KafkaBrokers).
		Str("group", cfg.KafkaGroupID).
		Msg("consumers started")

	<-ctx.Done()
	logger.Info().Msg("shutdown signal received")

	admin.Drain()

	// Give the relay and consumer a moment to finish their current batch and
	// commit, rather than dropping work mid-flight.
	time.Sleep(500 * time.Millisecond)

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	if err := admin.Shutdown(shutCtx); err != nil {
		logger.Warn().Err(err).Msg("admin server shutdown")
	}

	logger.Info().Msg("consumers stopped")
	_ = os.Stdout.Sync()
}
