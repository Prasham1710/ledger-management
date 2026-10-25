package db_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/prasham/ledger/services/transaction-svc/internal/db"
)

func TestTransaction_IsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status string
		want   bool
	}{
		{status: db.StatusPending, want: false},
		// COMPENSATING is still in flight: rollback has not finished, so the
		// recovery worker must keep considering it.
		{status: db.StatusCompensating, want: false},
		{status: db.StatusCompleted, want: true},
		{status: db.StatusFailed, want: true},
		{status: db.StatusReversed, want: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.status, func(t *testing.T) {
			t.Parallel()
			txn := &db.Transaction{Status: tc.status}
			assert.Equal(t, tc.want, txn.IsTerminal())
		})
	}
}

// NeedsCompensation decides whether an abandoned saga has effects to undo.
// Getting this wrong either strands money or reverses a step that never ran.
func TestTransaction_NeedsCompensation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		txn  *db.Transaction
		want bool
	}{
		{
			name: "nothing happened yet",
			txn:  &db.Transaction{},
			want: false,
		},
		{
			// Funds are reserved even though no balance moved, so the hold must
			// still be released.
			name: "hold placed only",
			txn:  &db.Transaction{HoldID: "hold-1"},
			want: true,
		},
		{
			name: "source debited",
			txn:  &db.Transaction{HoldID: "hold-1", SourceDebited: true},
			want: true,
		},
		{
			name: "source debited and destination credited",
			txn:  &db.Transaction{HoldID: "hold-1", SourceDebited: true, DestCredited: true},
			want: true,
		},
		{
			name: "fully applied including ledger",
			txn: &db.Transaction{
				HoldID: "hold-1", SourceDebited: true, DestCredited: true, LedgerPosted: true,
			},
			want: true,
		},
		{
			// Defensive: a ledger entry with no recorded balance movement still
			// needs reversing, or the ledger keeps asserting a transfer.
			name: "ledger posted without balance flags",
			txn:  &db.Transaction{LedgerPosted: true},
			want: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.txn.NeedsCompensation())
		})
	}
}
