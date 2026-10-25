package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	sharedconfig "github.com/prasham/ledger/internal/config"
	"github.com/prasham/ledger/internal/logging"
	"github.com/prasham/ledger/services/gateway/internal/auth"
	"github.com/prasham/ledger/services/gateway/internal/handler"
	"github.com/prasham/ledger/services/gateway/internal/middleware"
	"github.com/prasham/ledger/services/gateway/internal/proxy"
)

const serviceName = "gateway"

// config holds gateway runtime settings.
type config struct {
	Port        string
	MetricsPort string
	Upstream    string
	StaticDir   string

	JWTSecret     string
	JWTTTL        time.Duration
	MaxSessionAge time.Duration
	AdminUser     string
	AdminPassword string
	// UpstreamSharedSecret is echoed to upstreams as proof a request was
	// authenticated here.
	UpstreamSharedSecret string

	RateLimitRPM      int
	RateLimitBurst    int
	TrustProxyHeaders bool
	TrustedProxyCIDRs []string

	AllowedOrigins []string
	IsProduction   bool
}

func loadConfig() config {
	return config{
		Port:        sharedconfig.Env("GATEWAY_PORT", "8080"),
		MetricsPort: sharedconfig.Env("GATEWAY_METRICS_PORT", "9107"),
		Upstream:    sharedconfig.Env("UPSTREAM_URL", "http://localhost:8090"),
		StaticDir:   sharedconfig.Env("STATIC_DIR", "ui/dist"),

		JWTSecret:            sharedconfig.Env("JWT_SECRET", ""),
		JWTTTL:               sharedconfig.EnvDuration("JWT_TTL", time.Hour),
		MaxSessionAge:        sharedconfig.EnvDuration("JWT_MAX_SESSION_AGE", 12*time.Hour),
		AdminUser:            sharedconfig.Env("ADMIN_USERNAME", ""),
		AdminPassword:        sharedconfig.Env("ADMIN_PASSWORD", ""),
		UpstreamSharedSecret: sharedconfig.Env("GATEWAY_UPSTREAM_SECRET", ""),

		RateLimitRPM:   sharedconfig.EnvInt("RATE_LIMIT_RPM", 300),
		RateLimitBurst: sharedconfig.EnvInt("RATE_LIMIT_BURST", 0),
		// Off by default. Trusting X-Forwarded-For unconditionally let any client
		// forge its own rate-limit bucket.
		TrustProxyHeaders: sharedconfig.EnvBool("TRUST_PROXY_HEADERS", false),
		TrustedProxyCIDRs: sharedconfig.EnvList("TRUSTED_PROXY_CIDRS", nil),

		// Defaults to same-origin only; the SPA is served from this origin.
		AllowedOrigins: sharedconfig.EnvList("CORS_ALLOWED_ORIGINS", nil),
		IsProduction:   sharedconfig.IsProduction(),
	}
}

// insecureDefaults are the credentials that shipped as compiled-in defaults and
// in committed configuration. A deployment still using one can be signed into by
// anyone who has read the repository.
var insecureDefaults = map[string]bool{
	"admin":                   true,
	"password":                true,
	"changeme":                true,
	"change-me-in-production": true,
}

// validate refuses to start with credentials that cannot be trusted.
//
// Previously the gateway defaulted to admin/admin with
// JWT_SECRET="change-me-in-production" and started silently, so an unset
// environment variable produced a fully open admin API. Absent or placeholder
// credentials are now a startup failure in production and a loud warning in
// development.
func (c config) validate(log zerolog.Logger) error {
	var problems []string

	if c.AdminUser == "" {
		problems = append(problems, "ADMIN_USERNAME is not set")
	}
	if c.AdminPassword == "" {
		problems = append(problems, "ADMIN_PASSWORD is not set")
	} else if insecureDefaults[strings.ToLower(c.AdminPassword)] {
		problems = append(problems, "ADMIN_PASSWORD is a well-known default value")
	} else if len(c.AdminPassword) < 12 {
		problems = append(problems, "ADMIN_PASSWORD is shorter than 12 characters")
	}
	if c.JWTSecret == "" {
		problems = append(problems, "JWT_SECRET is not set")
	}
	if c.UpstreamSharedSecret == "" {
		problems = append(problems,
			"GATEWAY_UPSTREAM_SECRET is not set — upstream services cannot distinguish "+
				"gateway-authenticated requests from direct ones")
	}
	if c.TrustProxyHeaders && len(c.TrustedProxyCIDRs) == 0 {
		problems = append(problems,
			"TRUST_PROXY_HEADERS is enabled but TRUSTED_PROXY_CIDRS is empty — "+
				"X-Forwarded-For would be accepted from any peer")
	}

	if len(problems) == 0 {
		return nil
	}

	if c.IsProduction {
		return errors.New("insecure configuration: " + strings.Join(problems, "; "))
	}
	for _, p := range problems {
		log.Warn().Str("problem", p).Msg("insecure configuration — refused when APP_ENV=production")
	}
	return nil
}

// mustRandomSecret generates a cryptographically random secret for development
// use when none is configured. Using crypto/rand rather than a fixed fallback
// means an unset variable never produces a guessable secret.
func mustRandomSecret() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic("gateway: cannot generate a random secret: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

// spaHandler serves static files, falling back to index.html so the React router
// can handle client-side navigation.
func spaHandler(staticDir string) http.HandlerFunc {
	fileServer := http.FileServer(http.Dir(staticDir))
	indexPath := filepath.Join(staticDir, "index.html")

	return func(w http.ResponseWriter, r *http.Request) {
		// filepath.Clean on a rooted path neutralises any traversal segments
		// before they are joined to the static root.
		candidate := filepath.Join(staticDir, filepath.Clean("/"+r.URL.Path))
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			http.ServeFile(w, r, indexPath)
			return
		}
		fileServer.ServeHTTP(w, r)
	}
}

func main() {
	log := logging.New(logging.Config{
		ServiceName: serviceName,
		Level:       zerolog.InfoLevel,
		Pretty:      os.Getenv("LOG_PRETTY") != "",
	})

	cfg := loadConfig()
	if err := cfg.validate(log); err != nil {
		log.Fatal().Err(err).Msg("refusing to start")
	}

	// Development convenience: generate ephemeral secrets rather than falling back
	// to published placeholders. A restart invalidates existing tokens, which is
	// the correct trade-off for a value that must never be a known constant.
	jwtSecret := cfg.JWTSecret
	allowWeak := false
	if jwtSecret == "" {
		jwtSecret = mustRandomSecret()
		allowWeak = true
		log.Warn().Msg("JWT_SECRET unset — generated an ephemeral secret; tokens will not survive a restart")
	}
	upstreamSecret := cfg.UpstreamSharedSecret
	if upstreamSecret == "" {
		upstreamSecret = mustRandomSecret()
		log.Warn().Msg("GATEWAY_UPSTREAM_SECRET unset — generated an ephemeral value; set it so upstreams can verify requests")
	}

	jwtMgr, err := auth.NewManager(auth.Config{
		Secret:          jwtSecret,
		TTL:             cfg.JWTTTL,
		MaxSessionAge:   cfg.MaxSessionAge,
		AllowWeakSecret: allowWeak,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("invalid token configuration")
	}

	authH := handler.NewAuthHandler(jwtMgr, cfg.AdminUser, cfg.AdminPassword, log)

	rl := middleware.NewRateLimiter(middleware.RateLimiterConfig{
		RequestsPerMinute: cfg.RateLimitRPM,
		Burst:             cfg.RateLimitBurst,
		TrustProxyHeaders: cfg.TrustProxyHeaders,
		TrustedProxyCIDRs: cfg.TrustedProxyCIDRs,
	})
	defer rl.Close()

	apiProxy, err := proxy.New(cfg.Upstream)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid upstream URL")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", authH.Login)
	mux.HandleFunc("/auth/refresh", authH.Refresh)
	mux.Handle("/api/", apiProxy)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"gateway"}`))
	})
	mux.HandleFunc("/", spaHandler(cfg.StaticDir))

	// Order, outermost first: security headers → CORS → request id → logging →
	// rate limit → auth. The rate limiter sits outside auth so unauthenticated
	// login attempts are limited too.
	chain := middleware.SecurityHeaders(
		middleware.CORS(middleware.CORSConfig{AllowedOrigins: cfg.AllowedOrigins},
			middleware.RequestID(
				middleware.Logger(
					rl.Middleware(
						middleware.Auth(jwtMgr, upstreamSecret)(mux),
					),
				),
			),
		),
	)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           chain,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Metrics stay on a separate port so they are not exposed on the public
	// listener.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","service":"gateway"}`))
	})
	metricsSrv := &http.Server{
		Addr:              ":" + cfg.MetricsPort,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info().Str("port", cfg.MetricsPort).Msg("metrics server starting")
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("metrics server error")
		}
	}()

	go func() {
		log.Info().
			Str("port", cfg.Port).
			Str("upstream", cfg.Upstream).
			Str("static", cfg.StaticDir).
			Strs("cors_allowed_origins", cfg.AllowedOrigins).
			Bool("trust_proxy_headers", cfg.TrustProxyHeaders).
			Msg("gateway starting")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("gateway server error")
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info().Msg("shutdown signal received")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Warn().Err(err).Msg("gateway shutdown")
	}
	if err := metricsSrv.Shutdown(ctx); err != nil {
		log.Warn().Err(err).Msg("metrics shutdown")
	}
	log.Info().Msg("gateway stopped")
}
