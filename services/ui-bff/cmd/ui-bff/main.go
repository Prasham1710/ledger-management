package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	sharedconfig "github.com/prasham/ledger/internal/config"
	"github.com/prasham/ledger/internal/grpcmw"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/serverkit"
	"github.com/prasham/ledger/internal/tracing"
	"github.com/prasham/ledger/services/ui-bff/internal/handler"
)

const serviceName = "ui-bff"

type config struct {
	ListenAddr  string
	MetricsPort string
	StaticDir   string

	LedgerAddr  string
	AccountAddr string
	FXAddr      string
	TxnAddr     string
	PayoutAddr  string

	UpstreamSharedSecret string
	RequireAuth          bool
	AllowedOrigins       []string

	OTLPEndpoint string
	Version      string
	IsProduction bool
}

func loadConfig() config {
	return config{
		ListenAddr:  sharedconfig.Env("UI_BFF_ADDR", ":8090"),
		MetricsPort: sharedconfig.Env("UI_BFF_METRICS_PORT", "9109"),
		StaticDir:   sharedconfig.Env("UI_STATIC_DIR", sharedconfig.Env("STATIC_DIR", "./ui/dist")),

		LedgerAddr:  sharedconfig.Env("LEDGER_GRPC_ADDR", "localhost:50051"),
		AccountAddr: sharedconfig.Env("ACCOUNT_GRPC_ADDR", "localhost:50052"),
		FXAddr:      sharedconfig.Env("FX_GRPC_ADDR", "localhost:50053"),
		TxnAddr:     sharedconfig.Env("TRANSACTION_GRPC_ADDR", "localhost:50054"),
		PayoutAddr:  sharedconfig.Env("PAYOUT_GRPC_ADDR", "localhost:50055"),

		UpstreamSharedSecret: sharedconfig.Env("GATEWAY_UPSTREAM_SECRET", ""),
		// Authentication is required by default. This service exposes the entire
		// money-moving API and previously had none at all.
		RequireAuth:    sharedconfig.EnvBool("REQUIRE_GATEWAY_AUTH", true),
		AllowedOrigins: sharedconfig.EnvList("CORS_ALLOWED_ORIGINS", nil),

		OTLPEndpoint: sharedconfig.OTLPEndpoint(),
		Version:      sharedconfig.Version(),
		IsProduction: sharedconfig.IsProduction(),
	}
}

func main() {
	log := logging.New(logging.Config{
		ServiceName: serviceName,
		Level:       zerolog.InfoLevel,
		Pretty:      os.Getenv("LOG_PRETTY") != "",
	})

	cfg := loadConfig()

	// ui-bff serves credit, debit, transfer, payout, and raw ledger posting. It
	// must never run open, so an unset shared secret is fatal in production and a
	// loud warning elsewhere.
	switch {
	case !cfg.RequireAuth:
		if cfg.IsProduction {
			log.Fatal().Msg("REQUIRE_GATEWAY_AUTH=false is not permitted when APP_ENV=production")
		}
		log.Warn().Msg("gateway authentication DISABLED — every money-moving endpoint is open; development only")
	case cfg.UpstreamSharedSecret == "":
		log.Fatal().Msg("GATEWAY_UPSTREAM_SECRET is required: without it this service cannot verify that requests came through the gateway")
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

	// ── Upstream gRPC clients ─────────────────────────────────────────────────
	dialOpts := append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}, grpcmw.ClientOptions()...)

	dial := func(name, addr string) *grpc.ClientConn {
		conn, err := grpc.NewClient(addr, dialOpts...)
		if err != nil {
			log.Fatal().Err(err).Str("service", name).Str("addr", addr).Msg("dial upstream")
		}
		return conn
	}

	ledgerConn := dial("ledger-svc", cfg.LedgerAddr)
	defer func() { _ = ledgerConn.Close() }()
	accountConn := dial("account-svc", cfg.AccountAddr)
	defer func() { _ = accountConn.Close() }()
	fxConn := dial("fx-svc", cfg.FXAddr)
	defer func() { _ = fxConn.Close() }()
	txnConn := dial("transaction-svc", cfg.TxnAddr)
	defer func() { _ = txnConn.Close() }()
	payoutConn := dial("payout-svc", cfg.PayoutAddr)
	defer func() { _ = payoutConn.Close() }()

	h := handler.New(
		handler.Clients{
			Ledger:  ledgerv1.NewLedgerServiceClient(ledgerConn),
			Account: accountv1.NewAccountServiceClient(accountConn),
			FX:      fxv1.NewFXServiceClient(fxConn),
			Txn:     txnv1.NewTransactionServiceClient(txnConn),
			Payout:  payoutv1.NewPayoutServiceClient(payoutConn),
		},
		handler.Conns{
			Ledger:  ledgerConn,
			Account: accountConn,
			FX:      fxConn,
			Txn:     txnConn,
			Payout:  payoutConn,
		},
		handler.Config{
			StaticDir:            cfg.StaticDir,
			UpstreamSharedSecret: cfg.UpstreamSharedSecret,
			RequireAuth:          cfg.RequireAuth,
			AllowedOrigins:       cfg.AllowedOrigins,
		},
		log,
	)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// ── Admin HTTP ────────────────────────────────────────────────────────────
	// On its own port. The Helm chart previously set metricsPort == port for this
	// service, and probes hit /healthz — which fell through to the SPA catch-all
	// and returned index.html with a 200, so readiness "passed" by serving HTML.
	reg := metrics.New()
	admin := serverkit.NewAdminServer(serverkit.AdminConfig{
		Addr:        ":" + cfg.MetricsPort,
		Registry:    reg,
		ServiceName: serviceName,
		Log:         log,
	})
	admin.Start()

	go func() {
		log.Info().
			Str("addr", cfg.ListenAddr).
			Str("static", cfg.StaticDir).
			Bool("require_auth", cfg.RequireAuth).
			Msg("ui-bff listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("ui-bff server error")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received")

	admin.Drain()

	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		log.Warn().Err(err).Msg("ui-bff shutdown")
	}
	if err := admin.Shutdown(shutCtx); err != nil {
		log.Warn().Err(err).Msg("admin server shutdown")
	}

	log.Info().Msg("stopped")
}
