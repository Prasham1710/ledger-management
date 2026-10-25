package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	"github.com/prasham/ledger/internal/grpcmw"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/payout-svc/internal/config"
	"github.com/prasham/ledger/services/payout-svc/internal/db"
	"github.com/prasham/ledger/services/payout-svc/internal/recovery"
	"github.com/prasham/ledger/services/payout-svc/internal/service"
	"github.com/prasham/ledger/services/payout-svc/internal/storage"
)

const serviceName = "payout-svc"

func main() {
	logger := logging.New(logging.Config{
		ServiceName: serviceName,
		Pretty:      os.Getenv("LOG_PRETTY") != "",
	})

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

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

	if err := db.Migrate(ctx, pool); err != nil {
		logger.Fatal().Err(err).Msg("migrate")
	}
	logger.Info().Msg("migrations applied")

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(cfg.Redis)
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(ctx).Err(); err != nil {
		logger.Fatal().Err(err).Msg("ping redis")
	}
	idem := idempotency.NewRedisStore(rdb)

	// ── Object storage for settlement files ───────────────────────────────────
	// A hard dependency: a payout debits the customer and must produce a
	// settlement instruction, so running without usable storage would mean
	// debiting customers only to roll every payout back.
	store, err := storage.NewMinIO(cfg.Storage)
	if err != nil {
		logger.Fatal().Err(err).Msg("init settlement storage")
	}
	if err := store.EnsureBucket(ctx); err != nil {
		logger.Fatal().Err(err).Msg("ensure settlement bucket")
	}
	if cfg.Storage.EncryptionKey == "" {
		logger.Warn().Msg("settlement files are unencrypted at rest — set SETTLEMENT_ENCRYPTION_KEY")
	}

	// ── Upstream gRPC clients ─────────────────────────────────────────────────
	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, grpcmw.ClientOptions()...)

	ledgerConn, err := grpc.NewClient(cfg.LedgerAddr, dialOpts...)
	if err != nil {
		logger.Fatal().Err(err).Msg("dial ledger-svc")
	}
	defer func() { _ = ledgerConn.Close() }()

	accountConn, err := grpc.NewClient(cfg.AccountAddr, dialOpts...)
	if err != nil {
		logger.Fatal().Err(err).Msg("dial account-svc")
	}
	defer func() { _ = accountConn.Close() }()

	// ── Metrics ───────────────────────────────────────────────────────────────
	reg := metrics.New()
	rpcMetrics := metrics.NewRPCMetrics(reg)
	sagaMetrics := metrics.NewSagaMetrics(reg)
	recoveryMetrics := recovery.NewMetrics(reg)

	// ── Service ───────────────────────────────────────────────────────────────
	repo := db.NewRepo(pool)
	svc := service.NewPayoutServer(repo, idem,
		ledgerv1.NewLedgerServiceClient(ledgerConn),
		accountv1.NewAccountServiceClient(accountConn),
		store, sagaMetrics, logger)

	// ── Recovery worker ───────────────────────────────────────────────────────
	// Refunds payouts abandoned after the debit but before completion.
	recoveryWorker := recovery.New(repo, svc, recovery.Config{
		Interval:   cfg.RecoveryInterval,
		StuckAfter: cfg.RecoveryStuckAfter,
		BatchSize:  cfg.RecoveryBatchSize,
	}, recoveryMetrics, logger)
	go recoveryWorker.Run(ctx)

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcSrv := grpc.NewServer(grpcmw.ServerOptions(grpcmw.Deps{
		Log:     logger,
		Metrics: rpcMetrics,
		// The payout saga makes several sequential calls plus an object upload.
		Timeout: 60 * time.Second,
	})...)
	payoutv1.RegisterPayoutServiceServer(grpcSrv, svc)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("payout.v1.PayoutService", grpc_health_v1.HealthCheckResponse_SERVING)

	if cfg.EnableReflection {
		reflection.Register(grpcSrv)
		logger.Warn().Msg("gRPC reflection enabled — do not enable in production")
	}

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logger.Fatal().Err(err).Msg("listen gRPC")
	}

	go func() {
		logger.Info().Str("port", cfg.GRPCPort).Msg("gRPC listening")
		if err := grpcSrv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.Error().Err(err).Msg("gRPC serve")
		}
	}()

	// ── Admin HTTP ────────────────────────────────────────────────────────────
	admin := serverkit.NewAdminServer(serverkit.AdminConfig{
		Addr:        ":" + cfg.MetricsPort,
		Registry:    reg,
		ServiceName: serviceName,
		Log:         logger,
		Checks: []serverkit.Check{
			serverkit.PostgresCheck(pool),
			{
				Name: "redis",
				Func: func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
			},
			{
				Name: "settlement-storage",
				// Without storage a payout cannot produce its instruction, so
				// this replica must not accept payout traffic.
				Func: store.Ping,
			},
		},
	})
	admin.Start()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	<-ctx.Done()
	logger.Info().Msg("shutting down")

	admin.Drain()
	healthSrv.SetServingStatus("payout.v1.PayoutService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	grpcSrv.GracefulStop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := admin.Shutdown(shutCtx); err != nil {
		logger.Warn().Err(err).Msg("admin server shutdown")
	}

	logger.Info().Msg("stopped")
}
