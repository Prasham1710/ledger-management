// Package serverkit provides the operational HTTP surface every service
// exposes: Prometheus metrics, a liveness probe, and a readiness probe.
//
// It exists to fix one bug uniformly. Every service previously served /healthz
// with an unconditional 200 and pointed both the Kubernetes liveness and
// readiness probes at it, so a replica whose database or Redis had failed still
// reported Ready, stayed in the Service's endpoint list, and failed every
// request routed to it. Readiness here actually consults dependencies.
package serverkit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/prasham/ledger/internal/metrics"
)

// Check reports whether one dependency is usable. Implementations must respect
// ctx and return promptly.
type Check struct {
	Name string
	Func func(context.Context) error
}

// AdminConfig configures the admin HTTP server.
type AdminConfig struct {
	// Addr is the listen address, e.g. ":9101".
	Addr string
	// Registry backs /metrics.
	Registry *metrics.Registry
	// Checks are consulted by /readyz. An empty list makes readiness equivalent
	// to liveness, which is only correct for services with no dependencies.
	Checks []Check
	// CheckTimeout bounds the whole readiness evaluation. Defaults to 2s.
	CheckTimeout time.Duration
	// ServiceName is reported in probe responses.
	ServiceName string
	Log         zerolog.Logger
}

// AdminServer wraps the admin HTTP server and its drain state.
type AdminServer struct {
	srv      *http.Server
	draining atomic.Bool
	cfg      AdminConfig
}

// NewAdminServer builds the admin server. Mount it on the metrics port.
func NewAdminServer(cfg AdminConfig) *AdminServer {
	if cfg.CheckTimeout <= 0 {
		cfg.CheckTimeout = 2 * time.Second
	}

	a := &AdminServer{cfg: cfg}

	mux := http.NewServeMux()
	if cfg.Registry != nil {
		mux.Handle("/metrics", cfg.Registry.Handler())
	}

	// Liveness: is the process itself healthy? A failing dependency must NOT
	// fail liveness, or Kubernetes restarts a replica that is merely waiting for
	// its database to come back.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeStatus(w, http.StatusOK, map[string]string{
			"status":  "ok",
			"service": cfg.ServiceName,
		})
	})

	// Readiness: should this replica receive traffic?
	mux.HandleFunc("/readyz", a.readyHandler)

	a.srv = &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
		// Bound header reads so a slow client cannot hold a connection open
		// indefinitely.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return a
}

func (a *AdminServer) readyHandler(w http.ResponseWriter, r *http.Request) {
	// During graceful shutdown, report unready first so load balancers stop
	// sending new work while in-flight requests drain.
	if a.draining.Load() {
		writeStatus(w, http.StatusServiceUnavailable, map[string]string{
			"status":  "draining",
			"service": a.cfg.ServiceName,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.CheckTimeout)
	defer cancel()

	failures := make(map[string]string)
	for _, check := range a.cfg.Checks {
		if err := check.Func(ctx); err != nil {
			failures[check.Name] = err.Error()
		}
	}

	if len(failures) > 0 {
		a.cfg.Log.Warn().Interface("failed_checks", failures).Msg("readiness check failed")
		writeStatus(w, http.StatusServiceUnavailable, map[string]any{
			"status":  "not ready",
			"service": a.cfg.ServiceName,
			"failed":  failures,
		})
		return
	}

	writeStatus(w, http.StatusOK, map[string]string{
		"status":  "ready",
		"service": a.cfg.ServiceName,
	})
}

// Start serves in a new goroutine, logging any non-shutdown error.
func (a *AdminServer) Start() {
	go func() {
		a.cfg.Log.Info().Str("addr", a.cfg.Addr).Msg("admin HTTP listening")
		if err := a.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.cfg.Log.Error().Err(err).Msg("admin HTTP server error")
		}
	}()
}

// Drain marks the service unready without stopping the server, so probes see
// the change before connections are closed.
func (a *AdminServer) Drain() { a.draining.Store(true) }

// Shutdown gracefully stops the admin server.
func (a *AdminServer) Shutdown(ctx context.Context) error {
	if err := a.srv.Shutdown(ctx); err != nil {
		return err //nolint:wrapcheck // caller only logs this
	}
	return nil
}

func writeStatus(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// The status code is already written, so a failed body encode cannot change
	// the response. Probe clients act on the code, not the body.
	_ = json.NewEncoder(w).Encode(body) //nolint:errcheck // status already committed
}

// PostgresCheck returns a readiness Check that pings Postgres.
func PostgresCheck(pinger interface{ Ping(context.Context) error }) Check {
	return Check{
		Name: "postgres",
		Func: func(ctx context.Context) error {
			if err := pinger.Ping(ctx); err != nil {
				return err //nolint:wrapcheck // surfaced verbatim in the probe body
			}
			return nil
		},
	}
}
