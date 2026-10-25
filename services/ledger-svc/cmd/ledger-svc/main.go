package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	"github.com/prasham/ledger/internal/grpcmw"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/ledger-svc/internal/config"
	"github.com/prasham/ledger/services/ledger-svc/internal/db"
	"github.com/prasham/ledger/services/ledger-svc/internal/service"
)

const serviceName = "ledger-svc"

func main() {
	log := logging.New(logging.Config{
		ServiceName: serviceName,
		Level:       zerolog.InfoLevel,
		Pretty:      os.Getenv("LOG_PRETTY") == "true",
	})

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── Tracing ───────────────────────────────────────────────────────────────
	// Previously the OTLP endpoint was read from config and never used, so no
	// span was ever exported despite a fully deployed collector and Tempo.
	shutdownTracing, tracingErr := tracing.Setup(ctx, tracing.Config{
		ServiceName:    serviceName,
		ServiceVersion: cfg.Version,
	}, cfg.OTLPEndpoint)
	if tracingErr != nil {
		log.Fatal().Err(tracingErr).Msg("init tracing")
	}
	defer func() {
		flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(flushCtx); err != nil {
			log.Warn().Err(err).Msg("flush traces")
		}
	}()

	// ── PostgreSQL ────────────────────────────────────────────────────────────
	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("ping postgres")
	}

	if err := db.Migrate(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("migrate")
	}
	log.Info().Msg("migrations applied")

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(cfg.Redis)
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("ping redis")
	}

	idemStore := idempotency.NewRedisStore(rdb)

	// ── Metrics ───────────────────────────────────────────────────────────────
	reg := metrics.New()
	rpcMetrics := metrics.NewRPCMetrics(reg)

	// ── gRPC server ───────────────────────────────────────────────────────────
	repo := db.NewRepo(pool)
	svc := service.NewLedgerServer(repo, idemStore, log)

	grpcSrv := grpc.NewServer(grpcmw.ServerOptions(grpcmw.Deps{
		Log:     log,
		Metrics: rpcMetrics,
	})...)
	ledgerv1.RegisterLedgerServiceServer(grpcSrv, svc)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("ledger.v1.LedgerService", grpc_health_v1.HealthCheckResponse_SERVING)

	// Reflection lets grpcurl discover methods without a proto file. It also
	// advertises the full service surface, so it stays behind a flag that
	// defaults to off outside development.
	if cfg.EnableReflection {
		reflection.Register(grpcSrv)
		log.Warn().Msg("gRPC reflection enabled — do not enable in production")
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		log.Fatal().Err(err).Int("port", cfg.GRPCPort).Msg("listen grpc")
	}

	go func() {
		log.Info().Int("port", cfg.GRPCPort).Msg("gRPC listening")
		if err := grpcSrv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			log.Error().Err(err).Msg("grpc serve")
		}
	}()

	// ── Admin HTTP (metrics, liveness, readiness) ─────────────────────────────
	// Readiness consults dependencies. Previously both probes pointed at a
	// /healthz that returned 200 unconditionally, so a replica with a dead
	// database stayed in the load balancer rotation and failed every request.
	admin := serverkit.NewAdminServer(serverkit.AdminConfig{
		Addr:        fmt.Sprintf(":%d", cfg.MetricsPort),
		Registry:    reg,
		ServiceName: serviceName,
		Log:         log,
		Checks: []serverkit.Check{
			serverkit.PostgresCheck(pool),
			{
				Name: "redis",
				// Redis backs idempotency and the service fails closed without
				// it, so an unreachable Redis means this replica cannot serve
				// writes and must leave the rotation.
				Func: func(ctx context.Context) error { return rdb.Ping(ctx).Err() },
			},
		},
	})
	admin.Start()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	// Report unready before draining so peers stop routing new work here.
	admin.Drain()
	healthSrv.SetServingStatus("ledger.v1.LedgerService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	grpcSrv.GracefulStop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := admin.Shutdown(shutCtx); err != nil {
		log.Warn().Err(err).Msg("admin server shutdown")
	}

	log.Info().Msg("stopped")
}
