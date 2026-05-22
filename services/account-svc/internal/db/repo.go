package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
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
	ReferenceID string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

func (r *Repo) CreateAccount(ctx context.Context, ownerID, currency string) (*Account, error) {
	a := &Account{}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO account.accounts (owner_id, currency)
		VALUES ($1, $2)
		RETURNING id, owner_id, currency, status, balance, hold_balance, created_at, updated_at`,
		ownerID, currency,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func (r *Repo) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	a := &Account{}
	err := r.pool.QueryRow(ctx, `
		SELECT id, owner_id, currency, status, balance, hold_balance, created_at, updated_at
		FROM account.accounts WHERE id = $1`,
		accountID,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	return a, err
}

func (r *Repo) ListAccounts(ctx context.Context, ownerID string, limit int, offset int) ([]*Account, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, owner_id, currency, status, balance, hold_balance, created_at, updated_at
		FROM account.accounts
		WHERE owner_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`,
		ownerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var accounts []*Account
	for rows.Next() {
		a := &Account{}
		if err := rows.Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
			&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		accounts = append(accounts, a)
	}
	return accounts, rows.Err()
}

func (r *Repo) PlaceHold(ctx context.Context, accountID string, amount decimal.Decimal, referenceID string, ttlSeconds int64) (*Hold, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Lock account row and verify sufficient balance
	var balance, holdBalance decimal.Decimal
	var status string
	err = tx.QueryRow(ctx, `
		SELECT balance, hold_balance, status
		FROM account.accounts WHERE id = $1 FOR UPDATE`,
		accountID,
	).Scan(&balance, &holdBalance, &status)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("account not found")
	}
	if err != nil {
		return nil, err
	}
	if status != "ACTIVE" {
		return nil, fmt.Errorf("account is %s", status)
	}
	if balance.LessThan(amount) {
		return nil, fmt.Errorf("insufficient balance: have %s need %s", balance, amount)
	}

	expiresAt := time.Now().UTC().Add(time.Duration(ttlSeconds) * time.Second)

	// Deduct from available balance, add to hold_balance
	_, err = tx.Exec(ctx, `
		UPDATE account.accounts
		SET balance      = balance - $1,
		    hold_balance = hold_balance + $1,
		    updated_at   = NOW()
		WHERE id = $2`,
		amount, accountID,
	)
	if err != nil {
		return nil, err
	}

	h := &Hold{}
	err = tx.QueryRow(ctx, `
		INSERT INTO account.holds (account_id, amount, reference_id, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, account_id, amount, reference_id, expires_at, created_at`,
		accountID, amount, referenceID, expiresAt,
	).Scan(&h.ID, &h.AccountID, &h.Amount, &h.ReferenceID, &h.ExpiresAt, &h.CreatedAt)
	if err != nil {
		return nil, err
	}

	return h, tx.Commit(ctx)
}

func (r *Repo) ReleaseHold(ctx context.Context, holdID, accountID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Fetch and delete the hold
	var amount decimal.Decimal
	err = tx.QueryRow(ctx, `
		DELETE FROM account.holds WHERE id = $1 AND account_id = $2
		RETURNING amount`,
		holdID, accountID,
	).Scan(&amount)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("hold not found")
	}
	if err != nil {
		return err
	}

	// Return funds to available balance
	_, err = tx.Exec(ctx, `
		UPDATE account.accounts
		SET balance      = balance + $1,
		    hold_balance = hold_balance - $1,
		    updated_at   = NOW()
		WHERE id = $2`,
		amount, accountID,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *Repo) CreditAccount(ctx context.Context, accountID string, amount decimal.Decimal) (*Account, error) {
	a := &Account{}
	err := r.pool.QueryRow(ctx, `
		UPDATE account.accounts
		SET balance    = balance + $1,
		    updated_at = NOW()
		WHERE id = $2 AND status = 'ACTIVE'
		RETURNING id, owner_id, currency, status, balance, hold_balance, created_at, updated_at`,
		amount, accountID,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("account not found or not active")
	}
	return a, err
}

// DebitAccount consumes an existing hold (hold_id set) or does a direct debit (hold_id empty).
func (r *Repo) DebitAccount(ctx context.Context, accountID string, amount decimal.Decimal, holdID string) (*Account, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if holdID != "" {
		// Consume the hold — removes it and reduces hold_balance
		var holdAmount decimal.Decimal
		err = tx.QueryRow(ctx, `
			DELETE FROM account.holds WHERE id = $1 AND account_id = $2
			RETURNING amount`,
			holdID, accountID,
		).Scan(&holdAmount)
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("hold not found")
		}
		if err != nil {
			return nil, err
		}

		_, err = tx.Exec(ctx, `
			UPDATE account.accounts
			SET hold_balance = hold_balance - $1,
			    updated_at   = NOW()
			WHERE id = $2`,
			holdAmount, accountID,
		)
		if err != nil {
			return nil, err
		}
	} else {
		// Direct debit — deduct from available balance
		var balance decimal.Decimal
		err = tx.QueryRow(ctx, `
			SELECT balance FROM account.accounts WHERE id = $1 AND status = 'ACTIVE' FOR UPDATE`,
			accountID,
		).Scan(&balance)
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("account not found or not active")
		}
		if err != nil {
			return nil, err
		}
		if balance.LessThan(amount) {
			return nil, fmt.Errorf("insufficient balance")
		}

		_, err = tx.Exec(ctx, `
			UPDATE account.accounts
			SET balance    = balance - $1,
			    updated_at = NOW()
			WHERE id = $2`,
			amount, accountID,
		)
		if err != nil {
			return nil, err
		}
	}

	a := &Account{}
	err = tx.QueryRow(ctx, `
		SELECT id, owner_id, currency, status, balance, hold_balance, created_at, updated_at
		FROM account.accounts WHERE id = $1`,
		accountID,
	).Scan(&a.ID, &a.OwnerID, &a.Currency, &a.Status,
		&a.Balance, &a.HoldBalance, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return a, tx.Commit(ctx)
}
