// Package recovery refunds payouts abandoned mid-saga.
//
// A payout debits the customer before writing the ledger entry and the
// settlement file. If the process dies in between, the customer's money has left
// their account with no instruction sent to the bank and nothing scheduled to
// give it back. This worker finds those payouts and compensates them.
package recovery

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/payout-svc/internal/db"
)

// Resolver compensates a single abandoned payout. Implemented by
// service.PayoutServer so inline and deferred compensation share one path.
type Resolver interface {
	RecoverStuck(ctx context.Context, payout *db.Payout)
}

// Config controls recovery cadence.
type Config struct {
	Interval   time.Duration
	StuckAfter time.Duration
	BatchSize  int
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
			Name: "payout_in_flight_total",
			Help: "Payouts currently PENDING or COMPENSATING. Sustained growth means payouts are not completing.",
		}),
		Failures: reg.NewCounter(prometheus.CounterOpts{
			Name: "payout_recovery_sweep_failures_total",
			Help: "Failed payout recovery sweeps. Sustained failures mean customer refunds are not happening.",
		}),
	}
}

// Worker periodically refunds abandoned payouts.
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
		Msg("payout recovery worker started")

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	// Wait one interval before the first sweep: payouts belonging to the process
	// we replaced may still be legitimately in progress.
	for {
		select {
		case <-ctx.Done():
			w.log.Info().Msg("payout recovery worker stopped")
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
		w.log.Error().Err(err).Msg("payout recovery sweep failed — abandoned payouts remain unrefunded")
		if w.metrics != nil {
			w.metrics.Failures.Inc()
		}
		return
	}

	if len(stuck) > 0 {
		w.log.Warn().Int("count", len(stuck)).Msg("found abandoned payouts — compensating")
		for _, p := range stuck {
			if ctx.Err() != nil {
				return
			}
			w.resolver.RecoverStuck(ctx, p)
		}
	}

	if w.metrics != nil {
		if n, err := w.repo.CountInFlight(ctx); err != nil {
			w.log.Debug().Err(err).Msg("count in-flight payouts")
		} else {
			w.metrics.InFlight.Set(float64(n))
		}
	}

	if len(stuck) == w.cfg.BatchSize {
		w.log.Warn().Int("batch_size", w.cfg.BatchSize).
			Msg("payout recovery batch was full — more remain, continuing next interval")
	}
}
