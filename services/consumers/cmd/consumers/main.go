package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/prasham/ledger/services/consumers/internal/config"
	"github.com/prasham/ledger/services/consumers/internal/consumer"
	"github.com/prasham/ledger/services/consumers/internal/db"
	"github.com/prasham/ledger/services/consumers/internal/handler"
	"github.com/prasham/ledger/services/consumers/internal/relay"
)

func main() {
	cfg := config.Load()
	if cfg.LogPretty {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Postgres pool
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("postgres connect failed")
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("migration failed")
	}
	log.Info().Msg("migrations applied")

	repo := db.NewRepo(pool)

	// Kafka producer (for outbox relay)
	producer, err := kgo.NewClient(
		kgo.SeedBrokers(strings.Split(cfg.KafkaBrokers, ",")...),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("kafka producer init failed")
	}
	defer producer.Close()

	// Outbox relay
	relayInterval := 5 * time.Second
	if secs, err := strconv.Atoi(cfg.RelayInterval); err == nil {
		relayInterval = time.Duration(secs) * time.Second
	}
	outboxRelay := relay.NewOutboxRelay(repo, producer, relayInterval, log.Logger)

	// Kafka consumer group
	h := handler.New(repo, log.Logger)
	cons, err := consumer.New(cfg.KafkaBrokers, cfg.KafkaGroupID, h, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("kafka consumer init failed")
	}

	// Metrics + healthz
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"consumers"}`))
	})
	metricsSrv := &http.Server{Addr: ":" + cfg.MetricsPort, Handler: metricsMux}
	go func() {
		log.Info().Str("port", cfg.MetricsPort).Msg("metrics server starting")
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("metrics server error")
		}
	}()

	// Start relay + consumer
	go outboxRelay.Run(ctx)
	go cons.Run(ctx)

	log.Info().
		Str("brokers", cfg.KafkaBrokers).
		Str("group", cfg.KafkaGroupID).
		Msg("consumers started")

	<-ctx.Done()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = metricsSrv.Shutdown(shutCtx)
	log.Info().Msg("consumers stopped")
}
