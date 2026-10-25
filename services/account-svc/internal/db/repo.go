package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Hold lifecycle states.
const (
	HoldActive   = "ACTIVE"
	HoldConsumed = "CONSUMED"
	HoldReleased = "RELEASED"
	HoldExpired  = "EXPIRED"
)

// Balance mutation kinds.
const (
	MutationCredit = "CREDIT"
	MutationDebit  = "DEBIT"
)

// Sentinel errors, mapped to gRPC status codes by the service layer.
var (
	ErrAccountNotFound     = errors.New("account not found")
	ErrAccountNotActive    = errors.New("account is not active")
	ErrInsufficientBalance = errors.New("insufficient available balance")
	ErrCurrencyMismatch    = errors.New("amount currency does not match account currency")
	ErrHoldNotFound        = errors.New("hold not found")
	ErrHoldNotActive       = errors.New("hold is no longer active")
	ErrHoldExpired         = errors.New("hold has expired")
	ErrHoldAmountMismatch  = errors.New("debit amount does not match the hold amount")
	ErrDuplicateReference  = errors.New("a hold already exists for this reference")
	ErrDuplicateMutation   = errors.New("this mutation has already been applied")
	ErrAccountExists       = errors.New("an active account already exists for this owner and currency")
)

type Account struct {
	ID          string
	OwnerID     string
	Currency    string
	Status      string
	Balance     decimal.Decimal
	HoldBalance decimal.Decimal
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Hold struct {
	ID          string
	AccountID   string
	Amount      decimal.Decimal
	Currency    string
	ReferenceID string
	Status      string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// ExpiredHold describes a hold the sweeper reclaimed, for event emission.
type ExpiredHold struct {
	HoldID      string
	AccountID   string
	ReferenceID string
	Amount      decimal.Decimal
	Currency    string
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

const accountColumns = `id, owner_id, currency, status, balance, hold_balance, created_at, updated_at`

// CreateAccount opens an account. The partial unique index on
// (owner_id, currency) for active accounts means a second attempt for the same
// pair fails with ErrAccountExists rather than a raw constraint error.
func (r *Repo) CreateAccount(ctx context.Context, ownerID, currency string) (*Account, error) {
	a := &Account{}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO account.accounts (owner_id, currency)
		VALUES ($1, $2)
		RETURNING `+accountColumns,
		ownerID, currency,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt)
	if isUniqueViolation(err) {
		return nil, ErrAccountExists
	}
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	return a, nil
}

func (r *Repo) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	a := &Account{}
	err := r.pool.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM account.accounts WHERE id = $1`, accountID,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	return a, nil
}

func (r *Repo) ListAccounts(ctx context.Context, ownerID string, limit, offset int) ([]*Account, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+accountColumns+`
		 FROM account.accounts
		 WHERE owner_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2 OFFSET $3`,
		ownerID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		a := &Account{}
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
			&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate accounts: %w", err)
	}
	return accounts, nil
}

// PlaceHold reserves funds, moving them from available balance to hold balance.
//
// currency must match the account's own currency: without that check a USD
// amount could reserve against an INR balance at parity. The reference is
// unique among active holds, so a retry returns the existing hold instead of
// reserving the money twice.
func (r *Repo) PlaceHold(
	ctx context.Context,
	accountID string,
	amount decimal.Decimal,
	currency, referenceID string,
	ttl time.Duration,
) (*Hold, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck // no-op once committed

	// An existing active hold for this reference means the caller is retrying.
	// Return it rather than creating a second reservation.
	if existing, err := activeHoldByReference(ctx, tx, referenceID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.AccountID != accountID {
			return nil, fmt.Errorf("%w: reference is held against a different account", ErrDuplicateReference)
		}
		if !existing.Amount.Equal(amount) {
			return nil, fmt.Errorf("%w: reference already holds %s", ErrDuplicateReference, existing.Amount)
		}
		return existing, nil
	}

	var balance decimal.Decimal
	var status, accountCurrency string
	err = tx.QueryRow(ctx, `
		SELECT balance, status, currency
		FROM account.accounts WHERE id = $1 FOR UPDATE`,
		accountID,
	).Scan(&balance, &status, &accountCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock account: %w", err)
	}
	if status != "ACTIVE" {
		return nil, fmt.Errorf("%w: account is %s", ErrAccountNotActive, status)
	}
	if accountCurrency != currency {
		return nil, fmt.Errorf("%w: account is %s, request used %s",
			ErrCurrencyMismatch, accountCurrency, currency)
	}
	if balance.LessThan(amount) {
		return nil, fmt.Errorf("%w: have %s, need %s", ErrInsufficientBalance, balance, amount)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE account.accounts
		SET balance      = balance - $1,
		    hold_balance = hold_balance + $1,
		    updated_at   = NOW()
		WHERE id = $2`,
		amount, accountID,
	); err != nil {
		return nil, fmt.Errorf("move funds to hold: %w", err)
	}

	h := &Hold{Currency: accountCurrency}
	err = tx.QueryRow(ctx, `
		INSERT INTO account.holds (account_id, amount, reference_id, expires_at, status)
		VALUES ($1, $2, $3, $4, 'ACTIVE')
		RETURNING id, account_id, amount, reference_id, status, expires_at, created_at`,
		accountID, amount, referenceID, time.Now().UTC().Add(ttl),
	).Scan(&h.ID, &h.AccountID, &h.Amount, &h.ReferenceID, &h.Status, &h.ExpiresAt, &h.CreatedAt)
	if isUniqueViolation(err) {
		return nil, ErrDuplicateReference
	}
	if err != nil {
		return nil, fmt.Errorf("insert hold: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit place hold: %w", err)
	}
	return h, nil
}

func activeHoldByReference(ctx context.Context, tx pgx.Tx, referenceID string) (*Hold, error) {
	h := &Hold{}
	err := tx.QueryRow(ctx, `
		SELECT h.id, h.account_id, h.amount, a.currency, h.reference_id, h.status, h.expires_at, h.created_at
		FROM account.holds h
		JOIN account.accounts a ON a.id = h.account_id
		WHERE h.reference_id = $1 AND h.status = 'ACTIVE'`,
		referenceID,
	).Scan(&h.ID, &h.AccountID, &h.Amount, &h.Currency, &h.ReferenceID, &h.Status, &h.ExpiresAt, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up hold by reference: %w", err)
	}
	return h, nil
}

// ReleaseHold returns reserved funds to available balance.
//
// Releasing an already-resolved hold is reported rather than silently accepted,
// so a saga cannot double-release and inflate the balance. Marking the row
// RELEASED instead of deleting it keeps the audit trail intact.
func (r *Repo) ReleaseHold(ctx context.Context, holdID, accountID string) error {
	return r.resolveHold(ctx, holdID, accountID, HoldReleased, "released by caller", true)
}

// resolveHold transitions an active hold to a terminal state. When
// returnFunds is true the amount goes back to available balance; when false the
// funds leave the account (the hold was consumed by a debit).
func (r *Repo) resolveHold(
	ctx context.Context,
	holdID, accountID, newStatus, resolution string,
	returnFunds bool,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	var amount decimal.Decimal
	var status string
	err = tx.QueryRow(ctx, `
		SELECT amount, status FROM account.holds
		WHERE id = $1 AND account_id = $2 FOR UPDATE`,
		holdID, accountID,
	).Scan(&amount, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrHoldNotFound
	}
	if err != nil {
		return fmt.Errorf("lock hold: %w", err)
	}
	if status != HoldActive {
		return fmt.Errorf("%w: hold is %s", ErrHoldNotActive, status)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE account.holds
		SET status = $1, resolution = $2, resolved_at = NOW()
		WHERE id = $3`,
		newStatus, resolution, holdID,
	); err != nil {
		return fmt.Errorf("resolve hold: %w", err)
	}

	// Either way the reservation is lifted; returnFunds decides whether the
	// money becomes spendable again or leaves the account entirely.
	q := `UPDATE account.accounts
	      SET hold_balance = hold_balance - $1, updated_at = NOW()
	      WHERE id = $2`
	if returnFunds {
		q = `UPDATE account.accounts
		     SET balance      = balance + $1,
		         hold_balance = hold_balance - $1,
		         updated_at   = NOW()
		     WHERE id = $2`
	}
	if _, err := tx.Exec(ctx, q, amount, accountID); err != nil {
		return fmt.Errorf("adjust balances: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit resolve hold: %w", err)
	}
	return nil
}

// CreditAccount adds funds to an account.
//
// currency is verified against the account, and the mutation is recorded under
// (CREDIT, referenceID) so a replay is rejected by the database rather than
// relying on the idempotency cache being up.
func (r *Repo) CreditAccount(
	ctx context.Context,
	accountID string,
	amount decimal.Decimal,
	currency, referenceID string,
) (*Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	if err := lockActiveAccount(ctx, tx, accountID, currency); err != nil {
		return nil, err
	}

	if err := recordMutation(ctx, tx, accountID, MutationCredit, amount, currency, referenceID); err != nil {
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		UPDATE account.accounts
		SET balance = balance + $1, updated_at = NOW()
		WHERE id = $2`,
		amount, accountID,
	); err != nil {
		return nil, fmt.Errorf("credit balance: %w", err)
	}

	updated, err := reloadAccount(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit credit: %w", err)
	}
	return updated, nil
}

// DebitAccount removes funds, either by consuming a hold or directly.
//
// When holdID is set, the hold amount must equal the requested amount. The
// previous implementation ignored the requested amount entirely and deducted
// whatever the hold happened to be, so a debit of 100 against a hold of 10
// "succeeded" while moving 10 — after the ledger had already recorded 100.
// Expired holds are rejected rather than honoured.
func (r *Repo) DebitAccount(
	ctx context.Context,
	accountID string,
	amount decimal.Decimal,
	currency, holdID, referenceID string,
) (*Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	if err := lockActiveAccount(ctx, tx, accountID, currency); err != nil {
		return nil, err
	}

	if err := recordMutation(ctx, tx, accountID, MutationDebit, amount, currency, referenceID); err != nil {
		return nil, err
	}

	if holdID != "" {
		if err := consumeHold(ctx, tx, holdID, accountID, amount); err != nil {
			return nil, err
		}
	} else {
		var balance decimal.Decimal
		if err := tx.QueryRow(ctx,
			`SELECT balance FROM account.accounts WHERE id = $1 FOR UPDATE`, accountID,
		).Scan(&balance); err != nil {
			return nil, fmt.Errorf("read balance: %w", err)
		}
		if balance.LessThan(amount) {
			return nil, fmt.Errorf("%w: have %s, need %s", ErrInsufficientBalance, balance, amount)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE account.accounts
			SET balance = balance - $1, updated_at = NOW()
			WHERE id = $2`,
			amount, accountID,
		); err != nil {
			return nil, fmt.Errorf("debit balance: %w", err)
		}
	}

	updated, err := reloadAccount(ctx, tx, accountID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit debit: %w", err)
	}
	return updated, nil
}

// consumeHold settles a hold as part of a debit, verifying amount and expiry.
func consumeHold(ctx context.Context, tx pgx.Tx, holdID, accountID string, amount decimal.Decimal) error {
	var holdAmount decimal.Decimal
	var status string
	var expiresAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT amount, status, expires_at FROM account.holds
		WHERE id = $1 AND account_id = $2 FOR UPDATE`,
		holdID, accountID,
	).Scan(&holdAmount, &status, &expiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrHoldNotFound
	}
	if err != nil {
		return fmt.Errorf("lock hold: %w", err)
	}
	if status != HoldActive {
		return fmt.Errorf("%w: hold is %s", ErrHoldNotActive, status)
	}
	if time.Now().UTC().After(expiresAt) {
		// The sweeper may not have reclaimed it yet; refuse regardless so an
		// expired reservation can never be spent.
		return fmt.Errorf("%w: expired at %s", ErrHoldExpired, expiresAt.Format(time.RFC3339))
	}
	if !holdAmount.Equal(amount) {
		return fmt.Errorf("%w: hold is for %s, debit requested %s",
			ErrHoldAmountMismatch, holdAmount, amount)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE account.holds
		SET status = 'CONSUMED', resolution = 'consumed by debit', resolved_at = NOW()
		WHERE id = $1`, holdID,
	); err != nil {
		return fmt.Errorf("mark hold consumed: %w", err)
	}
	// The funds already left available balance when the hold was placed, so
	// settling only clears the reservation.
	if _, err := tx.Exec(ctx, `
		UPDATE account.accounts
		SET hold_balance = hold_balance - $1, updated_at = NOW()
		WHERE id = $2`, holdAmount, accountID,
	); err != nil {
		return fmt.Errorf("clear hold balance: %w", err)
	}
	return nil
}

// SweepExpiredHolds reclaims holds past their expiry, returning funds to
// available balance and reporting what it reclaimed so events can be emitted.
//
// Nothing previously read expires_at, so an abandoned saga reserved a caller's
// money permanently. Rows are claimed with SKIP LOCKED so several replicas can
// sweep concurrently without contending.
func (r *Repo) SweepExpiredHolds(ctx context.Context, limit int) ([]ExpiredHold, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin sweep tx: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		SELECT h.id, h.account_id, h.reference_id, h.amount, a.currency
		FROM account.holds h
		JOIN account.accounts a ON a.id = h.account_id
		WHERE h.status = 'ACTIVE' AND h.expires_at < NOW()
		ORDER BY h.expires_at
		LIMIT $1
		FOR UPDATE OF h SKIP LOCKED`, limit)
	if err != nil {
		return nil, fmt.Errorf("select expired holds: %w", err)
	}

	var expired []ExpiredHold
	for rows.Next() {
		var e ExpiredHold
		if err := rows.Scan(&e.HoldID, &e.AccountID, &e.ReferenceID, &e.Amount, &e.Currency); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan expired hold: %w", err)
		}
		expired = append(expired, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired holds: %w", err)
	}
	if len(expired) == 0 {
		return nil, nil
	}

	for _, e := range expired {
		if _, err := tx.Exec(ctx, `
			UPDATE account.holds
			SET status = 'EXPIRED', resolution = 'expired and reclaimed by sweeper', resolved_at = NOW()
			WHERE id = $1`, e.HoldID,
		); err != nil {
			return nil, fmt.Errorf("mark hold %s expired: %w", e.HoldID, err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE account.accounts
			SET balance      = balance + $1,
			    hold_balance = hold_balance - $1,
			    updated_at   = NOW()
			WHERE id = $2`, e.Amount, e.AccountID,
		); err != nil {
			return nil, fmt.Errorf("return funds for hold %s: %w", e.HoldID, err)
		}

		// Emit inside the same transaction so the event exists if and only if
		// the reclaim committed.
		if err := insertOutbox(ctx, tx, e.AccountID, "hold.matured", map[string]any{
			"hold_id":      e.HoldID,
			"account_id":   e.AccountID,
			"reference_id": e.ReferenceID,
			"amount":       e.Amount.String(),
			"currency":     e.Currency,
		}); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit sweep: %w", err)
	}
	return expired, nil
}

// CountActiveHolds reports how many holds are currently reserving funds, for
// the metrics gauge.
func (r *Repo) CountActiveHolds(ctx context.Context) (int64, error) {
	var n int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM account.holds WHERE status = 'ACTIVE'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count active holds: %w", err)
	}
	return n, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// lockActiveAccount row-locks an account and verifies it is active and
// denominated in the expected currency.
//
// The lock is the point: holding it for the rest of the transaction serialises
// concurrent mutations of the same account. Callers re-read the account after
// mutating it, so no value is returned.
func lockActiveAccount(ctx context.Context, tx pgx.Tx, accountID, currency string) error {
	var status, accountCurrency string
	err := tx.QueryRow(ctx,
		`SELECT status, currency FROM account.accounts WHERE id = $1 FOR UPDATE`, accountID,
	).Scan(&status, &accountCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountNotFound
	}
	if err != nil {
		return fmt.Errorf("lock account: %w", err)
	}
	if status != "ACTIVE" {
		return fmt.Errorf("%w: account is %s", ErrAccountNotActive, status)
	}
	if currency != "" && accountCurrency != currency {
		return fmt.Errorf("%w: account is %s, request used %s",
			ErrCurrencyMismatch, accountCurrency, currency)
	}
	return nil
}

// recordMutation writes the durable dedup record for a balance change.
func recordMutation(
	ctx context.Context,
	tx pgx.Tx,
	accountID, kind string,
	amount decimal.Decimal,
	currency, referenceID string,
) error {
	if referenceID == "" {
		return fmt.Errorf("reference_id is required to make a %s idempotent", kind)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO account.balance_mutations (account_id, kind, amount, currency, reference_id)
		VALUES ($1, $2, $3, $4, $5)`,
		accountID, kind, amount, currency, referenceID)
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: %s for reference %s", ErrDuplicateMutation, kind, referenceID)
	}
	if err != nil {
		return fmt.Errorf("record %s mutation: %w", kind, err)
	}
	return nil
}

func reloadAccount(ctx context.Context, tx pgx.Tx, accountID string) (*Account, error) {
	a := &Account{}
	if err := tx.QueryRow(ctx,
		`SELECT `+accountColumns+` FROM account.accounts WHERE id = $1`, accountID,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, fmt.Errorf("reload account: %w", err)
	}
	return a, nil
}

func insertOutbox(ctx context.Context, tx pgx.Tx, aggregateID, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO account.outbox_messages (aggregate_id, aggregate_type, event_type, payload)
		VALUES ($1, 'account', $2, $3)`,
		aggregateID, eventType, raw,
	); err != nil {
		return fmt.Errorf("insert outbox message: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}
