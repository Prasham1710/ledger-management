package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/recon-worker/internal/config"
	"github.com/prasham/ledger/services/recon-worker/internal/worker"
)

func main() {
	cfg := config.Load()
	pretty := os.Getenv("LOG_PRETTY") != ""
	logger := logging.New(logging.Config{ServiceName: "recon-worker", Pretty: pretty})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	w, err := worker.New(pool,
		cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOBucket,
		cfg.Interval, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("init worker")
	}

	// ── Metrics + healthz ─────────────────────────────────────────────────────
	reg := metrics.New()
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(hw http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(hw, "ok")
	})
	httpSrv := &http.Server{Addr: ":" + cfg.MetricsPort, Handler: mux}
	go func() {
		logger.Info().Str("port", cfg.MetricsPort).Msg("metrics listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("metrics serve")
		}
	}()

	// ── Worker ────────────────────────────────────────────────────────────────
	go w.Run(ctx)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()
	logger.Info().Msg("shutting down")
}
