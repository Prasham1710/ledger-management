// Package worker reconciles recorded account balances against the ledger.
//
// The invariant
// -------------
// For every account:
//
//	balance + hold_balance  ==  SUM(ledger credits) - SUM(ledger debits)
//
// Both terms must appear on the left. `balance` is the *available* balance:
// placing a hold moves money out of it into `hold_balance` without writing any
// ledger entry, because a reservation is not a transfer. An account with an open
// hold therefore has a lower `balance` than the ledger shows, by exactly the held
// amount.
//
// The previous implementation compared `balance` alone against the ledger sum.
// That reported drift for every account with an open hold and for every account
// funded through CreditAccount, which is to say almost all of them — so the
// signal was pure noise and real drift would have been invisible inside it.
//
// Scope
// -----
// Only accounts whose every balance movement is ledger-backed can be reconciled
// this way. Movements are ledger-backed because account-svc records each one in
// account.balance_mutations and every mutation carries the reference of the
// ledger entry that accompanies it. Accounts holding entries the account service
// does not own — notably the payout clearinghouse, which exists only in the
// ledger — are reported separately as ledger-only rather than as drift.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"

	"github.com/prasham/ledger/internal/metrics"
)

// Classification of a reconciliation finding.
const (
	// ClassDrift means the account exists on both sides and the totals disagree.
	// This is the finding that matters: recorded balances and the ledger
	// disagree about real money.
	ClassDrift = "drift"
	// ClassLedgerOnly means the ledger has entries for an account the account
	// service does not know about. Expected for virtual accounts such as the
	// payout clearinghouse.
	ClassLedgerOnly = "ledger_only"
	// ClassUnbacked means the account holds a balance but has no ledger entries
	// at all — for example an account funded before ledger-backed credits.
	ClassUnbacked = "unbacked"
)

// Finding is one reconciliation discrepancy.
type Finding struct {
	AccountID string `json:"account_id"`
	Class     string `json:"class"`
	Currency  string `json:"currency"`
	// AccountTotal is balance + hold_balance as recorded by account-svc.
	AccountTotal string `json:"account_total"`
	// Available and Held break AccountTotal down for diagnosis.
	Available string `json:"available"`
	Held      string `json:"held"`
	// LedgerTotal is credits minus debits from the ledger.
	LedgerTotal string `json:"ledger_total"`
	// Drift is AccountTotal - LedgerTotal.
	Drift string `json:"drift"`
}

// Report is uploaded to object storage on every run.
type Report struct {
	RunAt          string    `json:"run_at"`
	DurationMs     int64     `json:"duration_ms"`
	DriftCount     int       `json:"drift_count"`
	LedgerOnly     int       `json:"ledger_only_count"`
	UnbackedCount  int       `json:"unbacked_count"`
	AbsoluteDrift  string    `json:"absolute_drift_total"`
	Truncated      bool      `json:"truncated"`
	FindingsCapped int       `json:"findings_cap"`
	Findings       []Finding `json:"findings"`
}

// EventEmitter publishes a recon.drift event. Implemented by the repository so
// the event is written to the outbox transactionally.
type EventEmitter interface {
	EmitDrift(ctx context.Context, payload any) error
}

// Config controls reconciliation behaviour.
type Config struct {
	Interval time.Duration
	// MaxFindings bounds how many discrepancies one report carries. Systemic
	// drift could otherwise produce an unbounded query result and report.
	MaxFindings int
	// Bucket receives the JSON reports.
	Bucket string
}

// Worker reconciles balances against the ledger on an interval.
type Worker struct {
	pool    *pgxpool.Pool
	minio   *minio.Client
	emitter EventEmitter
	cfg     Config
	metrics *metrics.ReconMetrics
	log     zerolog.Logger
}

// New returns a reconciliation Worker.
func New(
	pool *pgxpool.Pool,
	mc *minio.Client,
	emitter EventEmitter,
	cfg Config,
	m *metrics.ReconMetrics,
	log zerolog.Logger,
) *Worker {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	if cfg.MaxFindings <= 0 {
		cfg.MaxFindings = 500
	}
	return &Worker{pool: pool, minio: mc, emitter: emitter, cfg: cfg, metrics: m, log: log}
}

// Run reconciles until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	w.log.Info().
		Dur("interval", w.cfg.Interval).
		Int("max_findings", w.cfg.MaxFindings).
		Msg("recon worker started")

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	w.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			w.runOnce(ctx)
		case <-ctx.Done():
			w.log.Info().Msg("recon worker stopped")
			return
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	start := time.Now()

	findings, truncated, err := w.findDiscrepancies(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.log.Error().Err(err).Msg("reconciliation query failed")
		if w.metrics != nil {
			w.metrics.RunsTotal.WithLabelValues("error").Inc()
		}
		return
	}

	var drift, ledgerOnly, unbacked int
	absDrift := decimal.Zero
	for _, f := range findings {
		switch f.Class {
		case ClassDrift:
			drift++
			if d, err := decimal.NewFromString(f.Drift); err == nil {
				absDrift = absDrift.Add(d.Abs())
			}
		case ClassLedgerOnly:
			ledgerOnly++
		case ClassUnbacked:
			unbacked++
		}
	}

	report := Report{
		RunAt:          start.UTC().Format(time.RFC3339),
		DurationMs:     time.Since(start).Milliseconds(),
		DriftCount:     drift,
		LedgerOnly:     ledgerOnly,
		UnbackedCount:  unbacked,
		AbsoluteDrift:  absDrift.String(),
		Truncated:      truncated,
		FindingsCapped: w.cfg.MaxFindings,
		Findings:       findings,
	}

	w.record(report, drift, absDrift, time.Since(start))
	w.logFindings(findings, drift, truncated)

	// Emit an event only for genuine drift. Publishing on every run would make
	// the signal meaningless.
	if drift > 0 && w.emitter != nil {
		w.emitDrift(ctx, report, findings)
	}

	w.uploadReport(ctx, report, start)
}

// findDiscrepancies compares recorded balances against the ledger projection.
//
// A FULL OUTER JOIN is used so accounts present on only one side are surfaced and
// classified rather than silently omitted. The join is on the text form of the
// account id because ledger.entries.account_id is TEXT (it also holds virtual
// accounts like the clearinghouse) while account.accounts.id is a UUID.
func (w *Worker) findDiscrepancies(ctx context.Context) ([]Finding, bool, error) {
	// Fetch one extra row to detect truncation.
	limit := w.cfg.MaxFindings + 1

	const q = `
	WITH ledger_totals AS (
	    SELECT account_id,
	           currency,
	           SUM(CASE WHEN type = 'CREDIT' THEN amount ELSE -amount END) AS total
	    FROM ledger.entries
	    GROUP BY account_id, currency
	),
	account_totals AS (
	    SELECT id::text AS account_id,
	           currency,
	           balance,
	           hold_balance,
	           -- Held funds are still the account's money: they have left the
	           -- available balance but no ledger entry accompanied the hold.
	           balance + hold_balance AS total
	    FROM account.accounts
	)
	SELECT
	    COALESCE(a.account_id, l.account_id)                  AS account_id,
	    COALESCE(a.currency, l.currency, '')                  AS currency,
	    COALESCE(a.total, 0)::text                            AS account_total,
	    COALESCE(a.balance, 0)::text                          AS available,
	    COALESCE(a.hold_balance, 0)::text                     AS held,
	    COALESCE(l.total, 0)::text                            AS ledger_total,
	    (COALESCE(a.total, 0) - COALESCE(l.total, 0))::text   AS drift,
	    CASE
	        WHEN a.account_id IS NULL THEN 'ledger_only'
	        WHEN l.account_id IS NULL THEN 'unbacked'
	        ELSE 'drift'
	    END                                                   AS class
	FROM account_totals a
	FULL OUTER JOIN ledger_totals l
	    ON l.account_id = a.account_id AND l.currency = a.currency
	WHERE
	    -- Only report a disagreement.
	    a.account_id IS NULL
	    OR l.account_id IS NULL
	    OR a.total <> l.total
	ORDER BY ABS(COALESCE(a.total, 0) - COALESCE(l.total, 0)) DESC
	LIMIT $1`

	rows, err := w.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, false, fmt.Errorf("reconciliation query: %w", err)
	}
	defer rows.Close()

	var findings []Finding
	for rows.Next() {
		var f Finding
		if err := rows.Scan(&f.AccountID, &f.Currency, &f.AccountTotal,
			&f.Available, &f.Held, &f.LedgerTotal, &f.Drift, &f.Class); err != nil {
			return nil, false, fmt.Errorf("scan reconciliation row: %w", err)
		}
		findings = append(findings, f)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate reconciliation rows: %w", err)
	}

	// An unbacked account with a zero balance is not a discrepancy: it has simply
	// never transacted.
	filtered := findings[:0]
	for _, f := range findings {
		if f.Class == ClassUnbacked && isZero(f.AccountTotal) {
			continue
		}
		filtered = append(filtered, f)
	}
	findings = filtered

	truncated := len(findings) > w.cfg.MaxFindings
	if truncated {
		findings = findings[:w.cfg.MaxFindings]
	}
	return findings, truncated, nil
}

func (w *Worker) record(report Report, drift int, absDrift decimal.Decimal, elapsed time.Duration) {
	if w.metrics == nil {
		return
	}
	w.metrics.RunsTotal.WithLabelValues("success").Inc()
	w.metrics.DriftAccounts.Set(float64(drift))
	absFloat, _ := absDrift.Float64()
	w.metrics.DriftAbsTotal.Set(absFloat)
	w.metrics.LastRunTimestmp.Set(float64(time.Now().Unix()))
	w.metrics.RunDuration.Observe(elapsed.Seconds())
	_ = report
}

func (w *Worker) logFindings(findings []Finding, drift int, truncated bool) {
	if drift == 0 {
		w.log.Info().
			Int("ledger_only", countClass(findings, ClassLedgerOnly)).
			Int("unbacked", countClass(findings, ClassUnbacked)).
			Msg("recon: no drift — recorded balances match the ledger")
	} else {
		w.log.Error().Int("drift_count", drift).
			Msg("recon: DRIFT DETECTED — recorded balances disagree with the ledger")
	}

	for _, f := range findings {
		event := w.log.Warn()
		if f.Class == ClassDrift {
			event = w.log.Error()
		}
		event.
			Str("account_id", f.AccountID).
			Str("class", f.Class).
			Str("currency", f.Currency).
			Str("account_total", f.AccountTotal).
			Str("available", f.Available).
			Str("held", f.Held).
			Str("ledger_total", f.LedgerTotal).
			Str("drift", f.Drift).
			Msg("recon finding")
	}

	if truncated {
		// Never let a cap look like clean coverage.
		w.log.Error().
			Int("cap", w.cfg.MaxFindings).
			Msg("recon: findings were TRUNCATED at the cap — more discrepancies exist than are reported")
	}
}

func (w *Worker) emitDrift(ctx context.Context, report Report, findings []Finding) {
	// Include only the drift findings, and cap what goes into the event: the
	// payload fans out to Kafka, the audit log, and analytics.
	const maxInEvent = 20
	drifting := make([]Finding, 0, maxInEvent)
	for _, f := range findings {
		if f.Class != ClassDrift {
			continue
		}
		if len(drifting) == maxInEvent {
			break
		}
		drifting = append(drifting, f)
	}

	payload := map[string]any{
		"run_at":               report.RunAt,
		"drift_count":          report.DriftCount,
		"absolute_drift_total": report.AbsoluteDrift,
		"findings_in_event":    len(drifting),
		"findings":             drifting,
	}

	// Detached: a drift event is the platform's most important alert and must not
	// be skipped because the process is shutting down.
	if err := w.emitter.EmitDrift(context.WithoutCancel(ctx), payload); err != nil {
		w.log.Error().Err(err).Msg("emitting recon.drift event failed")
	}
}

func (w *Worker) uploadReport(ctx context.Context, report Report, runAt time.Time) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		w.log.Error().Err(err).Msg("encoding recon report failed")
		return
	}

	// Include nanoseconds so two runs in the same second cannot overwrite each
	// other, and prefix by date so the bucket stays browsable.
	objectKey := fmt.Sprintf("%s/%s.json",
		runAt.UTC().Format("2006/01/02"),
		runAt.UTC().Format("150405.000000000"))

	if _, err := w.minio.PutObject(ctx, w.cfg.Bucket, objectKey,
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/json"}); err != nil {
		if ctx.Err() == nil {
			w.log.Error().Err(err).Str("key", objectKey).Msg("uploading recon report failed")
		}
		return
	}
	w.log.Info().Str("key", objectKey).Msg("recon report uploaded")
}

// ── helpers ───────────────────────────────────────────────────────────────────

func countClass(findings []Finding, class string) int {
	n := 0
	for _, f := range findings {
		if f.Class == class {
			n++
		}
	}
	return n
}

func isZero(numeric string) bool {
	d, err := decimal.NewFromString(numeric)
	return err == nil && d.IsZero()
}
