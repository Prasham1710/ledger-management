package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/prasham/ledger/services/gateway/internal/auth"
	"github.com/prasham/ledger/services/gateway/internal/handler"
	"github.com/prasham/ledger/services/gateway/internal/middleware"
	"github.com/prasham/ledger/services/gateway/internal/proxy"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// spaHandler serves static files and falls back to index.html for unknown paths
// so the React router can handle client-side navigation.
func spaHandler(staticDir string) http.HandlerFunc {
	fs := http.FileServer(http.Dir(staticDir))
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(staticDir, filepath.Clean("/"+r.URL.Path))
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	}
}

func main() {
	if os.Getenv("LOG_PRETTY") != "" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	port        := env("GATEWAY_PORT",         "8080")
	metricsPort := env("GATEWAY_METRICS_PORT",  "9107")
	upstream    := env("UPSTREAM_URL",          "http://localhost:8090")
	staticDir   := env("STATIC_DIR",            "ui/dist")
	jwtSecret   := env("JWT_SECRET",            "change-me-in-production")
	jwtTTL      := 1 * time.Hour
	adminUser   := env("ADMIN_USERNAME",        "admin")
	adminPass   := env("ADMIN_PASSWORD",        "admin")
	rpm := 300
	if v, err := strconv.Atoi(env("RATE_LIMIT_RPM", "300")); err == nil {
		rpm = v
	}

	jwtMgr := auth.NewManager(jwtSecret, jwtTTL)
	authH  := handler.NewAuthHandler(jwtMgr, adminUser, adminPass)
	rl     := middleware.NewRateLimiter(rpm)

	// Only proxy /api/* and /healthz to ui-bff; serve everything else locally.
	apiProxy, err := proxy.New(upstream)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid upstream URL")
	}

	mux := http.NewServeMux()

	// Auth — public, handled by gateway itself
	mux.HandleFunc("/auth/login",   authH.Login)
	mux.HandleFunc("/auth/refresh", authH.Refresh)

	// API — protected; proxied to ui-bff
	mux.Handle("/api/", apiProxy)

	// Healthz — proxy to ui-bff (aggregates all downstream services)
	mux.Handle("/healthz", apiProxy)

	// React SPA — static files served directly from ui/dist
	mux.HandleFunc("/", spaHandler(staticDir))

	chain := middleware.CORS(
		middleware.RequestID(
			middleware.Logger(
				rl.Middleware(
					middleware.Auth(jwtMgr)(mux),
				),
			),
		),
	)

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      chain,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// metrics + healthz sidecar
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"gateway"}`))
	})
	metricsSrv := &http.Server{
		Addr:    ":" + metricsPort,
		Handler: metricsMux,
	}

	go func() {
		log.Info().Str("port", metricsPort).Msg("metrics server starting")
		if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error().Err(err).Msg("metrics server error")
		}
	}()

	go func() {
		log.Info().Str("port", port).Str("upstream", upstream).Str("static", staticDir).Msg("gateway starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("gateway server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	_ = metricsSrv.Shutdown(ctx)
	log.Info().Msg("gateway stopped")
}
