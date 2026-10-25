package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/recon-worker/internal/config"
	"github.com/prasham/ledger/services/recon-worker/internal/db"
	"github.com/prasham/ledger/services/recon-worker/internal/worker"
)

const serviceName = "recon-worker"

func main() {
	logger := logging.New(logging.Config{
		ServiceName: serviceName,
		Pretty:      os.Getenv("LOG_PRETTY") != "",
	})

	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Tracing ───────────────────────────────────────────────────────────────
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName:    serviceName,
		ServiceVersion: cfg.Version,
	}, cfg.OTLPEndpoint)
	if err != nil {
		logger.Fatal().Err(err).Msg("init tracing")
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flushCtx); err != nil {
			logger.Warn().Err(err).Msg("flush traces")
		}
	}()

	// ── Postgres ──────────────────────────────────────────────────────────────
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logger.Fatal().Err(err).Msg("ping postgres")
	}

	// The worker owns an outbox table so drift events reach Kafka through the
	// same relay as every other platform event.
	if err := db.Migrate(ctx, pool); err != nil {
		logger.Fatal().Err(err).Msg("migrate")
	}

	// ── Object storage ────────────────────────────────────────────────────────
	mc, err := minio.New(cfg.MinIO.Endpoint, &minio.Options{
		Creds: credentials.NewStaticV4(cfg.MinIO.AccessKey, cfg.MinIO.SecretKey, ""),
		// Honoured rather than hardcoded false, so report traffic can use TLS.
		Secure: cfg.MinIO.UseSSL,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("init object storage")
	}
	if err := ensureBucket(ctx, mc, cfg.MinIO.Bucket); err != nil {
		logger.Fatal().Err(err).Str("bucket", cfg.MinIO.Bucket).Msg("ensure recon bucket")
	}

	// ── Metrics ───────────────────────────────────────────────────────────────
	reg := metrics.New()
	reconMetrics := metrics.NewReconMetrics(reg)

	// ── Worker ────────────────────────────────────────────────────────────────
	w := worker.New(pool, mc, db.NewEmitter(pool), worker.Config{
		Interval:    cfg.Interval,
		MaxFindings: cfg.MaxFindings,
		Bucket:      cfg.MinIO.Bucket,
	}, reconMetrics, logger)

	go w.Run(ctx)

	// ── Admin HTTP ────────────────────────────────────────────────────────────
	admin := serverkit.NewAdminServer(serverkit.AdminConfig{
		Addr:        ":" + cfg.MetricsPort,
		Registry:    reg,
		ServiceName: serviceName,
		Log:         logger,
		Checks: []serverkit.Check{
			serverkit.PostgresCheck(pool),
			{
				Name: "object-storage",
				Func: func(ctx context.Context) error {
					if _, err := mc.BucketExists(ctx, cfg.MinIO.Bucket); err != nil {
						return fmt.Errorf("object storage unreachable: %w", err)
					}
					return nil
				},
			},
		},
	})
	admin.Start()

	<-ctx.Done()
	logger.Info().Msg("shutting down")

	admin.Drain()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := admin.Shutdown(shutCtx); err != nil {
		logger.Warn().Err(err).Msg("admin server shutdown")
	}

	logger.Info().Msg("stopped")
}

// ensureBucket creates the report bucket if it is absent, so a fresh environment
// does not fail its first reconciliation run.
func ensureBucket(ctx context.Context, mc *minio.Client, bucket string) error {
	exists, err := mc.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", bucket, err)
	}
	if exists {
		return nil
	}
	if err := mc.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
		// A concurrent replica may have created it between the two calls.
		if exists, checkErr := mc.BucketExists(ctx, bucket); checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("create bucket %s: %w", bucket, err)
	}
	return nil
}
