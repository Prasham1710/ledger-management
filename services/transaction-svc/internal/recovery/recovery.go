// Package recovery resolves payment sagas abandoned mid-flight.
//
// A saga's forward steps run inside a single request. If the process handling it
// dies — a crash, an eviction, a rolling deploy — the saga stops wherever it was,
// leaving a transaction in PENDING with some subset of its effects applied:
// funds possibly held, the source possibly debited, the destination possibly
// credited.
//
// Nothing previously looked for these. A comment in the repository claimed "a
// recovery process can release it on crash", but no such process existed: the
// transaction sat in PENDING forever, its hold was never released, and its
// ledger entry was never reversed.
//
// This worker finds in-flight sagas that have not been touched for longer than
// StuckAfter and drives each one to a terminal state by compensating exactly the
// steps that took effect.
package recovery

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/transaction-svc/internal/db"
)

// Resolver compensates a single abandoned saga. Implemented by
// service.TransactionServer so inline and deferred compensation share one code
// path and cannot drift apart.
type Resolver interface {
	RecoverStuck(ctx context.Context, txn *db.Transaction)
}

// Config controls recovery cadence.
type Config struct {
	// Interval between sweeps.
	Interval time.Duration
	// StuckAfter is how long a saga may remain in-flight without progress
	// before it is treated as abandoned. It must comfortably exceed the longest
	// legitimate saga duration, or this worker will compensate sagas that are
	// merely slow.
	StuckAfter time.Duration
	// BatchSize bounds how many sagas one pass resolves.
	BatchSize int
}

// Metrics instruments the recovery worker.
type Metrics struct {
	InFlight prometheus.Gauge
	Failures prometheus.Counter
}

// NewMetrics registers recovery metrics.
func NewMetrics(reg *metrics.Registry) *Metrics {
	return &Metrics{
		InFlight: reg.NewGauge(prometheus.GaugeOpts{
			Name: "saga_in_flight_transactions",
			Help: "Payment sagas currently PENDING or COMPENSATING. Sustained growth means sagas are not completing.",
		}),
		Failures: reg.NewCounter(prometheus.CounterOpts{
			Name: "saga_recovery_sweep_failures_total",
			Help: "Failed recovery sweeps. Sustained failures mean abandoned sagas are not being resolved.",
		}),
	}
}

// Worker periodically resolves abandoned sagas.
type Worker struct {
	repo     *db.Repo
	resolver Resolver
	cfg      Config
	metrics  *Metrics
	log      zerolog.Logger
}

// New returns a recovery Worker. metrics may be nil to disable instrumentation.
func New(repo *db.Repo, resolver Resolver, cfg Config, m *Metrics, log zerolog.Logger) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.StuckAfter <= 0 {
		cfg.StuckAfter = 5 * time.Minute
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	return &Worker{repo: repo, resolver: resolver, cfg: cfg, metrics: m, log: log}
}

// Run sweeps until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.log.Info().
		Dur("interval", w.cfg.Interval).
		Dur("stuck_after", w.cfg.StuckAfter).
		Int("batch_size", w.cfg.BatchSize).
		Msg("saga recovery worker started")

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	// Wait one interval before the first sweep. On startup, sagas belonging to
	// the process we just replaced may still be within StuckAfter, and sweeping
	// immediately risks compensating work that is legitimately in progress
	// elsewhere.
	for {
		select {
		case <-ctx.Done():
			w.log.Info().Msg("saga recovery worker stopped")
			return
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	stuck, err := w.repo.FindStuck(ctx, w.cfg.StuckAfter, w.cfg.BatchSize)
	if err != nil {
		if ctx.Err() != nil {
			return // shutting down
		}
		w.log.Error().Err(err).Msg("recovery sweep failed — abandoned sagas remain unresolved")
		if w.metrics != nil {
			w.metrics.Failures.Inc()
		}
		return
	}

	if len(stuck) > 0 {
		w.log.Warn().Int("count", len(stuck)).Msg("found abandoned sagas — compensating")
		for _, txn := range stuck {
			if ctx.Err() != nil {
				return
			}
			w.resolver.RecoverStuck(ctx, txn)
		}
	}

	if w.metrics != nil {
		if n, err := w.repo.CountInFlight(ctx); err != nil {
			w.log.Debug().Err(err).Msg("count in-flight sagas")
		} else {
			w.metrics.InFlight.Set(float64(n))
		}
	}

	// A full batch means more work is waiting; the next tick will pick it up.
	if len(stuck) == w.cfg.BatchSize {
		w.log.Warn().Int("batch_size", w.cfg.BatchSize).
			Msg("recovery batch was full — more abandoned sagas remain, continuing next interval")
	}
}
