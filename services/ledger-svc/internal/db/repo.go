package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Entry is a single ledger row (one side of a double-entry pair).
type Entry struct {
	ID          string
	AccountID   string
	Type        string // "DEBIT" or "CREDIT"
	Amount      decimal.Decimal
	Currency    string
	ReferenceID string
	Description string
	CreatedAt   time.Time
}

// Repo owns all SQL for the ledger schema.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// PostDoubleEntry inserts a debit and a credit row in a single transaction.
// Both rows share the same amount, currency, reference_id, and description.
func (r *Repo) PostDoubleEntry(
	ctx context.Context,
	debitAccID, creditAccID string,
	amount decimal.Decimal,
	currency, referenceID, description string,
) (debit, credit Entry, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Entry{}, Entry{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const q = `
		INSERT INTO ledger.entries (account_id, type, amount, currency, reference_id, description)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, account_id, type, amount::TEXT, currency, reference_id, description, created_at`

	debit, err = scanEntry(tx.QueryRow(ctx, q, debitAccID, "DEBIT", amount, currency, referenceID, description))
	if err != nil {
		return Entry{}, Entry{}, fmt.Errorf("insert debit: %w", err)
	}
	credit, err = scanEntry(tx.QueryRow(ctx, q, creditAccID, "CREDIT", amount, currency, referenceID, description))
	if err != nil {
		return Entry{}, Entry{}, fmt.Errorf("insert credit: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return Entry{}, Entry{}, fmt.Errorf("commit: %w", err)
	}
	return debit, credit, nil
}

// GetBalance projects the balance for one account by summing credits minus debits.
func (r *Repo) GetBalance(ctx context.Context, accountID string) (decimal.Decimal, string, error) {
	const q = `
		SELECT
			COALESCE(SUM(CASE WHEN type = 'CREDIT' THEN amount ELSE -amount END), 0)::TEXT,
			COALESCE(MAX(currency), '')
		FROM ledger.entries
		WHERE account_id = $1`

	var balStr, currency string
	if err := r.pool.QueryRow(ctx, q, accountID).Scan(&balStr, &currency); err != nil {
		return decimal.Zero, "", fmt.Errorf("get balance: %w", err)
	}
	bal, err := decimal.NewFromString(balStr)
	return bal, currency, err
}

// ListEntries returns the most recent entries for an account, newest first.
func (r *Repo) ListEntries(ctx context.Context, accountID string, limit int) ([]Entry, error) {
	const q = `
		SELECT id, account_id, type, amount::TEXT, currency, reference_id, description, created_at
		FROM ledger.entries
		WHERE account_id = $1
		ORDER BY created_at DESC
		LIMIT $2`

	rows, err := r.pool.Query(ctx, q, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	var amtStr string
	err := row.Scan(&e.ID, &e.AccountID, &e.Type, &amtStr, &e.Currency,
		&e.ReferenceID, &e.Description, &e.CreatedAt)
	if err != nil {
		return Entry{}, err
	}
	e.Amount, err = decimal.NewFromString(amtStr)
	return e, err
}
