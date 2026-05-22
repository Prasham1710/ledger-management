package main

import (
	"context"
	"encoding/json"
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
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/transaction-svc/internal/config"
	"github.com/prasham/ledger/services/transaction-svc/internal/db"
	"github.com/prasham/ledger/services/transaction-svc/internal/service"
)

func main() {
	cfg := config.Load()

	pretty := os.Getenv("LOG_PRETTY") != ""
	logger := logging.New(logging.Config{ServiceName: "transaction-svc", Pretty: pretty})

	// ── Postgres ──────────────────────────────────────────────────────────────
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DSN)
	if err != nil {
		logger.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		logger.Fatal().Err(err).Msg("migrate")
	}

	// ── Redis ─────────────────────────────────────────────────────────────────
	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	idem := idempotency.NewRedisStore(rdb)

	// ── Upstream gRPC clients ─────────────────────────────────────────────────
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

	ledgerClient  := ledgerv1.NewLedgerServiceClient(ledgerConn)
	accountClient := accountv1.NewAccountServiceClient(accountConn)

	// ── Service ───────────────────────────────────────────────────────────────
	repo   := db.NewRepo(pool)
	svc    := service.NewTransactionServer(repo, idem, ledgerClient, accountClient, logger)

	// ── gRPC server ───────────────────────────────────────────────────────────
	grpcSrv := grpc.NewServer()
	txnv1.RegisterTransactionServiceServer(grpcSrv, svc)
	grpc_health_v1.RegisterHealthServer(grpcSrv, health.NewServer())
	reflection.Register(grpcSrv)

	lis, err := net.Listen("tcp", ":"+cfg.GRPCPort)
	if err != nil {
		logger.Fatal().Err(err).Msg("listen gRPC")
	}

	// ── Metrics + healthz HTTP ────────────────────────────────────────────────
	reg  := metrics.New()
	mux  := http.NewServeMux()
	mux.Handle("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /trace/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		txn, err := repo.GetTransaction(r.Context(), id)
		if err != nil || txn == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "transaction not found"})
			return
		}
		steps, err := repo.ListSteps(r.Context(), id)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"transaction": map[string]any{
				"id":                txn.ID,
				"status":            txn.Status,
				"source_account_id": txn.SourceAccountID,
				"dest_account_id":   txn.DestAccountID,
				"amount":            txn.Amount,
				"currency":          txn.Currency,
				"description":       txn.Description,
				"created_at":        txn.CreatedAt,
				"updated_at":        txn.UpdatedAt,
			},
			"steps": steps,
		})
	})
	httpSrv := &http.Server{Addr: ":" + cfg.MetricsPort, Handler: mux}

	// ── Start ─────────────────────────────────────────────────────────────────
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

	// ── Graceful shutdown ─────────────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info().Msg("shutting down")
	grpcSrv.GracefulStop()
	shutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(shutCtx) //nolint:errcheck
}
