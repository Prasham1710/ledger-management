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

	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	"github.com/prasham/ledger/internal/grpcmw"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/fx-svc/internal/config"
	"github.com/prasham/ledger/services/fx-svc/internal/db"
	"github.com/prasham/ledger/services/fx-svc/internal/service"
)

const serviceName = "fx-svc"

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
	shutdownTracing, err := tracing.Setup(ctx, tracing.Config{
		ServiceName:    serviceName,
		ServiceVersion: cfg.Version,
	}, cfg.OTLPEndpoint)
	if err != nil {
		log.Fatal().Err(err).Msg("init tracing")
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

	// ── Rate seeding ──────────────────────────────────────────────────────────
	// Only when explicitly enabled, and only for pairs that have no rate yet.
	if cfg.SeedRates {
		inserted, err := db.SeedRates(ctx, pool)
		if err != nil {
			log.Fatal().Err(err).Msg("seed rates")
		}
		log.Warn().
			Int("inserted", inserted).
			Msg("development rate seeding enabled — existing rates were left untouched")
	}

	usable, err := db.CountUsableRates(ctx, pool)
	if err != nil {
		log.Warn().Err(err).Msg("could not count usable rates")
	} else if usable == 0 {
		// Better to say so at startup than to fail every quote with a confusing
		// precondition error.
		log.Error().Msg("no unexpired FX rates are present — every quote will be refused until rates are loaded")
	} else {
		log.Info().Int("usable_rates", usable).Msg("FX rates available")
	}

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
	svc := service.NewFXServer(repo, idemStore, cfg.QuoteTTL, log)

	grpcSrv := grpc.NewServer(grpcmw.ServerOptions(grpcmw.Deps{
		Log:     log,
		Metrics: rpcMetrics,
	})...)
	fxv1.RegisterFXServiceServer(grpcSrv, svc)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("fx.v1.FXService", grpc_health_v1.HealthCheckResponse_SERVING)

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

	// ── Expired quote purge ───────────────────────────────────────────────────
	// Nothing previously removed consumed or lapsed quotes, so the table only grew.
	go purgeQuotes(ctx, repo, cfg.QuotePurgeInterval, cfg.QuoteRetention, log)

	// ── Admin HTTP ────────────────────────────────────────────────────────────
	admin := serverkit.NewAdminServer(serverkit.AdminConfig{
		Addr:        fmt.Sprintf(":%d", cfg.MetricsPort),
		Registry:    reg,
		ServiceName: serviceName,
		Log:         log,
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
	log.Info().Msg("shutdown signal received")

	admin.Drain()
	healthSrv.SetServingStatus("fx.v1.FXService", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	grpcSrv.GracefulStop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := admin.Shutdown(shutCtx); err != nil {
		log.Warn().Err(err).Msg("admin server shutdown")
	}

	log.Info().Msg("stopped")
}

// purgeQuotes deletes expired unlocked quotes on an interval.
func purgeQuotes(ctx context.Context, repo *db.Repo, interval, retain time.Duration, log zerolog.Logger) {
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			deleted, err := repo.ExpireQuotes(ctx, retain)
			if err != nil {
				if ctx.Err() == nil {
					log.Warn().Err(err).Msg("purging expired quotes failed")
				}
				continue
			}
			if deleted > 0 {
				log.Debug().Int64("deleted", deleted).Msg("purged expired quotes")
			}
		}
	}
}
