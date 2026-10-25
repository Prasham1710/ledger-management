// Package sweeper reclaims expired fund reservations.
//
// Holds carry an expires_at, but nothing in the platform ever read it: a saga
// that died between PlaceHold and settlement left the caller's money reserved
// permanently, invisible to both the owner and the reconciliation worker. The
// hold.matured event was mapped by the outbox relay and subscribed to by the
// consumer, yet no code path emitted it.
//
// This worker closes that gap. It periodically finds holds past their expiry,
// returns the funds to available balance, marks the hold EXPIRED, and emits
// hold.matured — all inside one transaction per batch, so funds and event either
// both happen or neither does.
package sweeper

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"

	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/services/account-svc/internal/db"
)

// Config controls sweep cadence and batch size.
type Config struct {
	// Interval between sweeps.
	Interval time.Duration
	// BatchSize bounds how many holds one pass reclaims, so a large backlog is
	// worked through incrementally rather than in one long transaction.
	BatchSize int
}

// Metrics instruments the sweeper.
type Metrics struct {
	Reclaimed   prometheus.Counter
	Failures    prometheus.Counter
	ActiveHolds prometheus.Gauge
}

// NewMetrics registers sweeper metrics.
func NewMetrics(reg *metrics.Registry) *Metrics {
	return &Metrics{
		Reclaimed: reg.NewCounter(prometheus.CounterOpts{
			Name: "account_holds_expired_reclaimed_total",
			Help: "Expired holds whose funds were returned to available balance.",
		}),
		Failures: reg.NewCounter(prometheus.CounterOpts{
			Name: "account_hold_sweep_failures_total",
			Help: "Failed hold sweep passes. Sustained failures mean funds stay reserved.",
		}),
		ActiveHolds: reg.NewGauge(prometheus.GaugeOpts{
			Name: "account_holds_active",
			Help: "Holds currently reserving funds.",
		}),
	}
}

// Sweeper reclaims expired holds on an interval.
type Sweeper struct {
	repo    *db.Repo
	cfg     Config
	metrics *Metrics
	log     zerolog.Logger
}

// New returns a Sweeper. metrics may be nil to disable instrumentation.
func New(repo *db.Repo, cfg Config, m *Metrics, log zerolog.Logger) *Sweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 100
	}
	return &Sweeper{repo: repo, cfg: cfg, metrics: m, log: log}
}

// Run sweeps until ctx is cancelled.
func (s *Sweeper) Run(ctx context.Context) {
	s.log.Info().
		Dur("interval", s.cfg.Interval).
		Int("batch_size", s.cfg.BatchSize).
		Msg("hold expiry sweeper started")

	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()

	// Sweep immediately: a restart is exactly when a backlog of holds abandoned
	// by the previous process is waiting.
	s.runOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			s.log.Info().Msg("hold expiry sweeper stopped")
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *Sweeper) runOnce(ctx context.Context) {
	// Keep draining while full batches come back, so a large backlog clears
	// within one interval rather than one batch per tick.
	for {
		expired, err := s.repo.SweepExpiredHolds(ctx, s.cfg.BatchSize)
		if err != nil {
			if ctx.Err() != nil {
				return // shutting down
			}
			s.log.Error().Err(err).Msg("hold sweep failed — funds remain reserved")
			if s.metrics != nil {
				s.metrics.Failures.Inc()
			}
			return
		}

		for _, h := range expired {
			// Log every reclaim: an expiring hold means a saga did not finish,
			// which is worth investigating even though the funds are now safe.
			s.log.Warn().
				Str("hold_id", h.HoldID).
				Str("account_id", h.AccountID).
				Str("reference_id", h.ReferenceID).
				Str("amount", h.Amount.String()).
				Str("currency", h.Currency).
				Msg("hold expired — funds returned to available balance")
		}

		if s.metrics != nil {
			s.metrics.Reclaimed.Add(float64(len(expired)))
		}

		if len(expired) < s.cfg.BatchSize {
			break
		}
	}

	if s.metrics != nil {
		if n, err := s.repo.CountActiveHolds(ctx); err != nil {
			s.log.Debug().Err(err).Msg("count active holds")
		} else {
			s.metrics.ActiveHolds.Set(float64(n))
		}
	}
}
