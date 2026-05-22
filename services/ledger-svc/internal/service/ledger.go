package service

import (
	"context"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/ledger-svc/internal/db"
)

// LedgerServer implements ledgerv1.LedgerServiceServer.
type LedgerServer struct {
	ledgerv1.UnimplementedLedgerServiceServer
	repo *db.Repo
	idem idempotency.Store
}

func NewLedgerServer(repo *db.Repo, idem idempotency.Store) *LedgerServer {
	return &LedgerServer{repo: repo, idem: idem}
}

// PostEntry writes a balanced debit+credit pair in a single DB transaction.
// Idempotency is enforced via Redis SetNX on the caller-supplied key.
func (s *LedgerServer) PostEntry(ctx context.Context, req *ledgerv1.PostEntryRequest) (*ledgerv1.PostEntryResponse, error) {
	if err := validatePostEntry(req); err != nil {
		return nil, err
	}

	// First-writer-wins idempotency.
	idemKey := "ledger:postentry:" + req.IdempotencyKey
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		return nil, status.Error(codes.AlreadyExists,
			"duplicate idempotency_key — this PostEntry was already processed")
	}

	amount, _ := decimal.NewFromString(req.Amount.Amount)

	debit, credit, err := s.repo.PostDoubleEntry(ctx,
		req.DebitAccountId, req.CreditAccountId,
		amount, req.Amount.Currency,
		req.ReferenceId, req.Description,
	)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "post double entry: %v", err)
	}

	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 0, Body: []byte("ok")})

	return &ledgerv1.PostEntryResponse{
		DebitEntry:  toProto(debit),
		CreditEntry: toProto(credit),
	}, nil
}

// GetBalance projects the account balance by summing all entries.
func (s *LedgerServer) GetBalance(ctx context.Context, req *ledgerv1.GetBalanceRequest) (*ledgerv1.GetBalanceResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	bal, currency, err := s.repo.GetBalance(ctx, req.AccountId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get balance: %v", err)
	}

	return &ledgerv1.GetBalanceResponse{
		AccountId: req.AccountId,
		Balance:   &commonv1.Money{Amount: bal.StringFixed(4), Currency: currency},
	}, nil
}

// ListEntries returns the most recent entries for an account.
func (s *LedgerServer) ListEntries(ctx context.Context, req *ledgerv1.ListEntriesRequest) (*ledgerv1.ListEntriesResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	limit := int(req.PageSize)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	entries, err := s.repo.ListEntries(ctx, req.AccountId, limit)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list entries: %v", err)
	}

	out := make([]*ledgerv1.Entry, len(entries))
	for i, e := range entries {
		out[i] = toProto(e)
	}
	return &ledgerv1.ListEntriesResponse{Entries: out}, nil
}

func validatePostEntry(req *ledgerv1.PostEntryRequest) error {
	switch {
	case req.IdempotencyKey == "":
		return status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.DebitAccountId == "":
		return status.Error(codes.InvalidArgument, "debit_account_id required")
	case req.CreditAccountId == "":
		return status.Error(codes.InvalidArgument, "credit_account_id required")
	case req.DebitAccountId == req.CreditAccountId:
		return status.Error(codes.InvalidArgument, "debit and credit accounts must differ")
	case req.Amount == nil || req.Amount.Amount == "":
		return status.Error(codes.InvalidArgument, "amount required")
	case req.Amount.Currency == "":
		return status.Error(codes.InvalidArgument, "amount.currency required")
	}

	amt, err := decimal.NewFromString(req.Amount.Amount)
	if err != nil || !amt.IsPositive() {
		return status.Error(codes.InvalidArgument, "amount must be a positive decimal string")
	}
	return nil
}

func toProto(e db.Entry) *ledgerv1.Entry {
	t := ledgerv1.EntryType_ENTRY_TYPE_DEBIT
	if e.Type == "CREDIT" {
		t = ledgerv1.EntryType_ENTRY_TYPE_CREDIT
	}
	return &ledgerv1.Entry{
		Id:          e.ID,
		AccountId:   e.AccountID,
		Type:        t,
		Amount:      &commonv1.Money{Amount: e.Amount.StringFixed(4), Currency: e.Currency},
		ReferenceId: e.ReferenceID,
		Description: e.Description,
		CreatedAt:   timestamppb.New(e.CreatedAt),
	}
}
