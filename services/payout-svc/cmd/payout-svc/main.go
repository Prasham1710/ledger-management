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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/payout-svc/internal/config"
	"github.com/prasham/ledger/services/payout-svc/internal/db"
	"github.com/prasham/ledger/services/payout-svc/internal/service"
	"github.com/prasham/ledger/services/payout-svc/internal/storage"
)

func main() {
	cfg := config.Load()
	pretty := os.Getenv("LOG_PRETTY") != ""
	logger := logging.New(logging.Config{ServiceName: "payout-svc", Pretty: pretty})

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		logger.Fatal().Err(err).Msg("migrate")
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	idem := idempotency.NewRedisStore(rdb)

	ledgerConn, err := grpc.NewClient(cfg.LedgerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Fatal().Err(err).Msg("dial ledger-svc")
	}
	defer ledgerConn.Close()

	accountConn, err := grpc.NewClient(cfg.AccountAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Fatal().Err(err).Msg("dial account-svc")
	}
	defer accountConn.Close()

	minioClient, err := storage.NewMinIO(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOBucket)
	if err != nil {
		logger.Fatal().Err(err).Msg("init minio")
	}

	svc := service.NewPayoutServer(
		db.NewRepo(pool),
		idem,
		ledgerv1.NewLedgerServiceClient(ledgerConn),
		accountv1.NewAccountServiceClient(accountConn),
		minioClient,
		logger,
	)

	grpcSrv := grpc.NewServer()
	payoutv1.RegisterPayoutServiceServer(grpcSrv, svc)
	grpc_health_v1.RegisterHealthServer(grpcSrv, health.NewServer())
	reflection.Register(grpcSrv)

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logger.Fatal().Err(err).Msg("listen gRPC")
	}

	reg := metrics.New()
	mux := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	httpSrv := &http.Server{Addr: ":" + cfg.MetricsPort, Handler: mux}

	go func() {
		logger.Info().Str("port", cfg.GRPCPort).Msg("gRPC listening")
		if err := grpcSrv.Serve(lis); err != nil {
			logger.Fatal().Err(err).Msg("gRPC serve")
		}
	}()
	go func() {
		logger.Info().Str("port", cfg.MetricsPort).Msg("metrics listening")
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("metrics serve")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info().Msg("shutting down")
	grpcSrv.GracefulStop()
	shutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutCtx) //nolint:errcheck
}
