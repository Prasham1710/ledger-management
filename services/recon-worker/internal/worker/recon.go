package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"
)

// DriftRow describes an account whose stored balance diverges from ledger sum.
type DriftRow struct {
	AccountID      string `json:"account_id"`
	Currency       string `json:"currency"`
	AccountBalance string `json:"account_balance"`
	LedgerBalance  string `json:"ledger_balance"`
	Drift          string `json:"drift"`
	Coverage       string `json:"coverage"` // "covered" | "uncovered"
}

// Report is uploaded to MinIO on every recon run.
type Report struct {
	RunAt      string     `json:"run_at"`
	TotalDrift int        `json:"total_drift_count"`
	Rows       []DriftRow `json:"rows"`
}

// Worker periodically compares account.accounts balances against ledger.entries sums.
type Worker struct {
	pool     *pgxpool.Pool
	minio    *minio.Client
	bucket   string
	interval time.Duration
	log      zerolog.Logger
}

func New(pool *pgxpool.Pool, endpoint, accessKey, secretKey, bucket string, interval time.Duration, log zerolog.Logger) (*Worker, error) {
	mc, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: false,
	})
	if err != nil {
		return nil, fmt.Errorf("minio: %w", err)
	}
	return &Worker{pool: pool, minio: mc, bucket: bucket, interval: interval, log: log}, nil
}

// Run blocks until ctx is cancelled, running a recon cycle every w.interval.
func (w *Worker) Run(ctx context.Context) {
	w.log.Info().Dur("interval", w.interval).Msg("recon worker started")
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	// Run immediately on start.
	w.runOnce(ctx)

	for {
		select {
		case <-ticker.C:
			w.runOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) runOnce(ctx context.Context) {
	rows, err := w.findDrift(ctx)
	if err != nil {
		w.log.Error().Err(err).Msg("recon query failed")
		return
	}

	report := Report{
		RunAt:      time.Now().UTC().Format(time.RFC3339),
		TotalDrift: len(rows),
		Rows:       rows,
	}

	if len(rows) == 0 {
		w.log.Info().Msg("recon: no drift — all balances match ledger")
	} else {
		w.log.Warn().Int("drift_count", len(rows)).Msg("recon: drift detected")
		for _, r := range rows {
			w.log.Warn().
				Str("account_id", r.AccountID).
				Str("account_balance", r.AccountBalance).
				Str("ledger_balance", r.LedgerBalance).
				Str("drift", r.Drift).
				Str("coverage", r.Coverage).
				Msg("recon drift")
		}
	}

	w.uploadReport(ctx, report)
}

// findDrift queries for accounts whose stored balance differs from the ledger projection.
// Accounts with no ledger entries at all are reported as "uncovered" (not necessarily drift).
func (w *Worker) findDrift(ctx context.Context) ([]DriftRow, error) {
	q := `
	WITH ledger_sums AS (
		SELECT account_id,
			SUM(CASE WHEN type = 'CREDIT' THEN amount ELSE -amount END) AS ledger_balance
		FROM ledger.entries
		GROUP BY account_id
	)
	SELECT
		a.id,
		a.currency,
		a.balance::text                               AS account_balance,
		COALESCE(ls.ledger_balance, 0)::text          AS ledger_balance,
		(a.balance - COALESCE(ls.ledger_balance, 0))::text AS drift,
		CASE WHEN ls.account_id IS NULL THEN 'uncovered' ELSE 'covered' END AS coverage
	FROM account.accounts a
	LEFT JOIN ledger_sums ls ON ls.account_id = a.id::text
	WHERE a.balance != COALESCE(ls.ledger_balance, 0)
	ORDER BY ABS(a.balance - COALESCE(ls.ledger_balance, 0)) DESC
	`
	pgRows, err := w.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer pgRows.Close()

	var result []DriftRow
	for pgRows.Next() {
		var r DriftRow
		if err := pgRows.Scan(&r.AccountID, &r.Currency, &r.AccountBalance, &r.LedgerBalance, &r.Drift, &r.Coverage); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, pgRows.Err()
}

func (w *Worker) uploadReport(ctx context.Context, report Report) {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		w.log.Error().Err(err).Msg("marshal recon report")
		return
	}
	objectKey := fmt.Sprintf("%s.json", time.Now().UTC().Format("2006-01-02T15-04-05"))
	_, err = w.minio.PutObject(ctx, w.bucket, objectKey,
		bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/json"})
	if err != nil {
		w.log.Error().Err(err).Str("key", objectKey).Msg("upload recon report")
		return
	}
	w.log.Info().Str("key", objectKey).Msg("recon report uploaded")
}
