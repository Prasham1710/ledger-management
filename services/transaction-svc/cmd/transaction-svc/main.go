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
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/internal/grpcmw"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/transaction-svc/internal/config"
	"github.com/prasham/ledger/services/transaction-svc/internal/db"
	"github.com/prasham/ledger/services/transaction-svc/internal/recovery"
	"github.com/prasham/ledger/services/transaction-svc/internal/service"
)

const serviceName = "transaction-svc"

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

	// ── Upstream gRPC clients ─────────────────────────────────────────────────
	// ClientOptions propagates trace context, so a saga's spans join into one
	// trace instead of appearing as unrelated fragments per service.
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

	ledgerClient := ledgerv1.NewLedgerServiceClient(ledgerConn)
	accountClient := accountv1.NewAccountServiceClient(accountConn)

	// ── Metrics ───────────────────────────────────────────────────────────────
	reg := metrics.New()
	rpcMetrics := metrics.NewRPCMetrics(reg)
	sagaMetrics := metrics.NewSagaMetrics(reg)
	recoveryMetrics := recovery.NewMetrics(reg)

	// ── Service ───────────────────────────────────────────────────────────────
	repo := db.NewRepo(pool)
	svc := service.NewTransactionServer(repo, idem, ledgerClient, accountClient,
		sagaMetrics, cfg.HoldTTL, logger)

	// ── Saga recovery worker ──────────────────────────────────────────────────
	// Resolves sagas abandoned by a crashed process, which would otherwise sit
	// in PENDING forever with funds held and ledger entries unreversed.
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
		// Sagas make several sequential downstream calls, so they need more
		// headroom than the default.
		Timeout: 60 * time.Second,
	})...)
	txnv1.RegisterTransactionServiceServer(grpcSrv, svc)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("transaction.v1.TransactionService", grpc_health_v1.HealthCheckResponse_SERVING)

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

	// ── Admin HTTP (metrics, liveness, readiness) ─────────────────────────────
	//
	// The saga trace endpoint deliberately does NOT live here. It previously
	// served full transaction detail — accounts and amounts — on this port,
	// which carries no authentication and is scraped by Prometheus. It is now
	// exposed as the GetTransactionTrace RPC, behind the same authorization as
	// every other read.
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
		},
	})
	admin.Start()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	<-ctx.Done()
	logger.Info().Msg("shutting down")

	admin.Drain()
	healthSrv.SetServingStatus("transaction.v1.TransactionService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	grpcSrv.GracefulStop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := admin.Shutdown(shutCtx); err != nil {
		logger.Warn().Err(err).Msg("admin server shutdown")
	}

	logger.Info().Msg("stopped")
}
