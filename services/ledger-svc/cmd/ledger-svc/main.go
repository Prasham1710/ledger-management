package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
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
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/ledger-svc/internal/config"
	"github.com/prasham/ledger/services/ledger-svc/internal/db"
	"github.com/prasham/ledger/services/ledger-svc/internal/service"
)

func main() {
	cfg := config.Load()

	log := logging.New(logging.Config{
		ServiceName: "ledger-svc",
		Level:       zerolog.InfoLevel,
		Pretty:      os.Getenv("LOG_PRETTY") == "true",
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Fatal().Err(err).Msg("ping redis")
	}

	idemStore := idempotency.NewRedisStore(rdb)

	// ── Metrics ───────────────────────────────────────────────────────────────
	reg := metrics.New()

	// ── gRPC server ───────────────────────────────────────────────────────────
	repo := db.NewRepo(pool)
	svc := service.NewLedgerServer(repo, idemStore)

	grpcSrv := grpc.NewServer()
	ledgerv1.RegisterLedgerServiceServer(grpcSrv, svc)

	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcSrv, healthSrv)
	healthSrv.SetServingStatus("ledger.v1.LedgerService", grpc_health_v1.HealthCheckResponse_SERVING)

	// reflection allows grpcurl to discover methods without a proto file
	reflection.Register(grpcSrv)

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.GRPCPort))
	if err != nil {
		log.Fatal().Err(err).Int("port", cfg.GRPCPort).Msg("listen grpc")
	}

	go func() {
		log.Info().Int("port", cfg.GRPCPort).Msg("gRPC listening")
		if err := grpcSrv.Serve(lis); err != nil {
			log.Error().Err(err).Msg("grpc serve")
		}
	}()

	// ── Metrics + health HTTP ─────────────────────────────────────────────────
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	httpSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler: mux,
	}

	go func() {
		log.Info().Int("port", cfg.MetricsPort).Msg("metrics HTTP listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("metrics serve")
		}
	}()

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	grpcSrv.GracefulStop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutCtx)

	log.Info().Msg("stopped")
}
