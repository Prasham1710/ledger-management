package db

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Entry kinds. A POSTING is an original double-entry pair; a REVERSAL is the
// mirror-image pair that neutralises it. The ledger is append-only, so undoing a
// posting means adding its inverse, never deleting or mutating rows.
const (
	KindPosting  = "POSTING"
	KindReversal = "REVERSAL"
)

// Entry types.
const (
	TypeDebit  = "DEBIT"
	TypeCredit = "CREDIT"
)

// Sentinel errors, mapped to gRPC status codes by the service layer.
var (
	// ErrDuplicateReference means an entry pair already exists for this
	// (reference_id, kind). Raised by the database uniqueness constraint, so it
	// holds even when the idempotency cache is unavailable.
	ErrDuplicateReference = errors.New("entry pair already exists for this reference")
	// ErrCurrencyMismatch means the requested currency disagrees with the
	// currency this account is already pinned to.
	ErrCurrencyMismatch = errors.New("account is denominated in a different currency")
	// ErrReferenceNotFound means no posting exists to reverse.
	ErrReferenceNotFound = errors.New("no posting found for this reference")
	// ErrAccountNotFound means the account has no entries and therefore no
	// currency or balance.
	ErrAccountNotFound = errors.New("account has no ledger entries")
	// ErrInvalidPageToken means the supplied keyset cursor could not be decoded.
	ErrInvalidPageToken = errors.New("invalid page_token")
)

// Entry is a single ledger row (one side of a double-entry pair).
type Entry struct {
	ID          string
	AccountID   string
	Type        string // DEBIT or CREDIT
	Kind        string // POSTING or REVERSAL
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

const entryColumns = `id, account_id, type, kind, amount::TEXT, currency, reference_id, description, created_at`

// PostDoubleEntry inserts a balanced debit/credit pair in one transaction.
//
// Both accounts are pinned to the given currency on first use; a later attempt
// to post a different currency to either account fails with
// ErrCurrencyMismatch. A second call for the same reference fails with
// ErrDuplicateReference, enforced by a unique index rather than by the
// idempotency cache, so the guarantee survives a Redis outage.
func (r *Repo) PostDoubleEntry(
	ctx context.Context,
	debitAccID, creditAccID string,
	amount decimal.Decimal,
	currency, referenceID, description string,
) (debit, credit Entry, err error) {
	return r.postPair(ctx, KindPosting, debitAccID, creditAccID, amount, currency, referenceID, description)
}

// ReverseEntry posts the mirror image of an existing posting: the original
// debit account is credited and the original credit account is debited for the
// same amount and currency.
//
// It is safe to call more than once — the second call returns
// ErrDuplicateReference — which is what lets a saga retry its compensating
// action without risking a double reversal.
func (r *Repo) ReverseEntry(ctx context.Context, referenceID, reason string) (debit, credit Entry, err error) {
	original, err := r.postingFor(ctx, referenceID)
	if err != nil {
		return Entry{}, Entry{}, err
	}

	description := fmt.Sprintf("reversal of %s", referenceID)
	if reason != "" {
		description = fmt.Sprintf("%s: %s", description, reason)
	}

	// Swap the sides: crediting what was debited and vice versa cancels the
	// original pair, leaving both accounts' projected balances unchanged.
	return r.postPair(ctx, KindReversal,
		original.CreditAccountID, original.DebitAccountID,
		original.Amount, original.Currency, referenceID, description)
}

// postingPair describes an existing posting, resolved from its two rows.
type postingPair struct {
	DebitAccountID  string
	CreditAccountID string
	Amount          decimal.Decimal
	Currency        string
}

func (r *Repo) postingFor(ctx context.Context, referenceID string) (postingPair, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT type, account_id, amount::TEXT, currency
		FROM ledger.entries
		WHERE reference_id = $1 AND kind = $2`,
		referenceID, KindPosting)
	if err != nil {
		return postingPair{}, fmt.Errorf("load posting %s: %w", referenceID, err)
	}
	defer rows.Close()

	var out postingPair
	var seenDebit, seenCredit bool
	for rows.Next() {
		var entryType, accountID, amountStr, currency string
		if err := rows.Scan(&entryType, &accountID, &amountStr, &currency); err != nil {
			return postingPair{}, fmt.Errorf("scan posting %s: %w", referenceID, err)
		}
		amount, err := decimal.NewFromString(amountStr)
		if err != nil {
			return postingPair{}, fmt.Errorf("parse posting amount %q: %w", amountStr, err)
		}
		out.Amount = amount
		out.Currency = currency
		switch entryType {
		case TypeDebit:
			out.DebitAccountID, seenDebit = accountID, true
		case TypeCredit:
			out.CreditAccountID, seenCredit = accountID, true
		}
	}
	if err := rows.Err(); err != nil {
		return postingPair{}, fmt.Errorf("iterate posting %s: %w", referenceID, err)
	}
	if !seenDebit || !seenCredit {
		return postingPair{}, ErrReferenceNotFound
	}
	return out, nil
}

// postPair inserts one debit and one credit row of the given kind atomically.
func (r *Repo) postPair(
	ctx context.Context,
	kind string,
	debitAccID, creditAccID string,
	amount decimal.Decimal,
	currency, referenceID, description string,
) (debit, credit Entry, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Entry{}, Entry{}, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op once committed

	// Pin both accounts to this currency, or confirm they already agree.
	for _, accountID := range []string{debitAccID, creditAccID} {
		if err := pinCurrency(ctx, tx, accountID, currency); err != nil {
			return Entry{}, Entry{}, err
		}
	}

	const q = `
		INSERT INTO ledger.entries (account_id, type, kind, amount, currency, reference_id, description)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + entryColumns

	debit, err = scanEntry(tx.QueryRow(ctx, q, debitAccID, TypeDebit, kind, amount, currency, referenceID, description))
	if err != nil {
		if isUniqueViolation(err) {
			return Entry{}, Entry{}, ErrDuplicateReference
		}
		return Entry{}, Entry{}, fmt.Errorf("insert debit: %w", err)
	}

	credit, err = scanEntry(tx.QueryRow(ctx, q, creditAccID, TypeCredit, kind, amount, currency, referenceID, description))
	if err != nil {
		if isUniqueViolation(err) {
			return Entry{}, Entry{}, ErrDuplicateReference
		}
		return Entry{}, Entry{}, fmt.Errorf("insert credit: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return Entry{}, Entry{}, fmt.Errorf("commit: %w", err)
	}
	return debit, credit, nil
}

// pinCurrency records the account's currency on first use and verifies it
// thereafter. Concurrent first-writes are resolved by the primary key: the
// loser's ON CONFLICT DO NOTHING is followed by a read that confirms agreement.
func pinCurrency(ctx context.Context, tx pgx.Tx, accountID, currency string) error {
	var stored string
	err := tx.QueryRow(ctx, `
		INSERT INTO ledger.account_currency (account_id, currency)
		VALUES ($1, $2)
		ON CONFLICT (account_id) DO UPDATE
		    SET currency = ledger.account_currency.currency
		RETURNING currency`,
		accountID, currency,
	).Scan(&stored)
	if err != nil {
		return fmt.Errorf("pin currency for %s: %w", accountID, err)
	}
	if stored != currency {
		return fmt.Errorf("%w: account %s is in %s, request used %s",
			ErrCurrencyMismatch, accountID, stored, currency)
	}
	return nil
}

// GetBalance projects an account's balance as credits minus debits.
//
// The projection includes reversal entries, which is the point of modelling
// reversals as real entries: a reversed posting nets to zero without any
// special-case arithmetic. The currency comes from account_currency rather than
// from an aggregate over entries, so it is exact rather than arbitrary.
func (r *Repo) GetBalance(ctx context.Context, accountID, wantCurrency string) (decimal.Decimal, string, error) {
	var currency string
	err := r.pool.QueryRow(ctx,
		`SELECT currency FROM ledger.account_currency WHERE account_id = $1`, accountID).Scan(&currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, "", ErrAccountNotFound
	}
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("get account currency: %w", err)
	}
	if wantCurrency != "" && wantCurrency != currency {
		return decimal.Zero, "", fmt.Errorf("%w: account %s is in %s, request asked for %s",
			ErrCurrencyMismatch, accountID, currency, wantCurrency)
	}

	const q = `
		SELECT COALESCE(SUM(CASE WHEN type = 'CREDIT' THEN amount ELSE -amount END), 0)::TEXT
		FROM ledger.entries
		WHERE account_id = $1 AND currency = $2`

	var balStr string
	if err := r.pool.QueryRow(ctx, q, accountID, currency).Scan(&balStr); err != nil {
		return decimal.Zero, "", fmt.Errorf("get balance: %w", err)
	}
	bal, err := decimal.NewFromString(balStr)
	if err != nil {
		return decimal.Zero, "", fmt.Errorf("parse balance %q: %w", balStr, err)
	}
	return bal, currency, nil
}

// ListEntries returns entries for an account, newest first, using keyset
// pagination. An empty pageToken starts from the newest entry.
//
// Keyset rather than OFFSET: entries are append-only and read concurrently with
// writes, so an offset would skip or repeat rows as new entries arrive. The
// cursor pairs created_at with id to break ties deterministically.
func (r *Repo) ListEntries(ctx context.Context, accountID string, limit int, pageToken string) ([]Entry, string, error) {
	args := []any{accountID, limit + 1}
	// Fetch one extra row to detect whether a further page exists.
	q := `SELECT ` + entryColumns + `
		FROM ledger.entries
		WHERE account_id = $1`

	if pageToken != "" {
		cursorTime, cursorID, err := decodeCursor(pageToken)
		if err != nil {
			return nil, "", err
		}
		q += ` AND (created_at, id) < ($3, $4)`
		args = append(args, cursorTime, cursorID)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT $2`

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list entries: %w", err)
	}
	defer rows.Close()

	out := make([]Entry, 0, limit)
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan entry: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate entries: %w", err)
	}

	var nextToken string
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		nextToken = encodeCursor(last.CreatedAt, last.ID)
	}
	return out, nextToken, nil
}

// encodeCursor renders an opaque keyset cursor. Callers must treat it as
// opaque; the encoding is an implementation detail.
func encodeCursor(createdAt time.Time, id string) string {
	raw := createdAt.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(token string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: not valid base64", ErrInvalidPageToken)
	}
	createdAt, id, found := strings.Cut(string(raw), "|")
	if !found {
		return time.Time{}, "", fmt.Errorf("%w: malformed cursor", ErrInvalidPageToken)
	}
	ts, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: unparseable timestamp", ErrInvalidPageToken)
	}
	if id == "" {
		return time.Time{}, "", fmt.Errorf("%w: missing id", ErrInvalidPageToken)
	}
	return ts, id, nil
}

func scanEntry(row pgx.Row) (Entry, error) {
	var e Entry
	var amtStr string
	err := row.Scan(&e.ID, &e.AccountID, &e.Type, &e.Kind, &amtStr, &e.Currency,
		&e.ReferenceID, &e.Description, &e.CreatedAt)
	if err != nil {
		return Entry{}, err
	}
	e.Amount, err = decimal.NewFromString(amtStr)
	if err != nil {
		return Entry{}, fmt.Errorf("parse amount %q: %w", amtStr, err)
	}
	return e, nil
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
