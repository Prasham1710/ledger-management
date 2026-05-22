package service

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/account-svc/internal/db"
)

type AccountServer struct {
	accountv1.UnimplementedAccountServiceServer
	repo *db.Repo
	idem idempotency.Store
}

func NewAccountServer(repo *db.Repo, idem idempotency.Store) *AccountServer {
	return &AccountServer{repo: repo, idem: idem}
}

// ── CreateAccount ─────────────────────────────────────────────────────────────

func (s *AccountServer) CreateAccount(ctx context.Context, req *accountv1.CreateAccountRequest) (*accountv1.CreateAccountResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.OwnerId == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_id required")
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, "currency required")
	}

	idemKey := "account:create:" + req.IdempotencyKey
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
	}

	acc, err := s.repo.CreateAccount(ctx, req.OwnerId, req.Currency)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create account: %v", err)
	}

	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 0, CreatedAt: time.Now()})

	return &accountv1.CreateAccountResponse{Account: toProtoAccount(acc)}, nil
}

// ── GetAccount ────────────────────────────────────────────────────────────────

func (s *AccountServer) GetAccount(ctx context.Context, req *accountv1.GetAccountRequest) (*accountv1.GetAccountResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	acc, err := s.repo.GetAccount(ctx, req.AccountId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get account: %v", err)
	}
	if acc == nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}

	return &accountv1.GetAccountResponse{Account: toProtoAccount(acc)}, nil
}

// ── ListAccounts ──────────────────────────────────────────────────────────────

func (s *AccountServer) ListAccounts(ctx context.Context, req *accountv1.ListAccountsRequest) (*accountv1.ListAccountsResponse, error) {
	if req.OwnerId == "" {
		return nil, status.Error(codes.InvalidArgument, "owner_id required")
	}

	limit := int(req.PageSize)
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	offset := 0
	if req.PageToken != "" {
		n, err := strconv.Atoi(req.PageToken)
		if err != nil || n < 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid page_token")
		}
		offset = n
	}

	accounts, err := s.repo.ListAccounts(ctx, req.OwnerId, limit+1, offset)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list accounts: %v", err)
	}

	var nextToken string
	if len(accounts) > limit {
		accounts = accounts[:limit]
		nextToken = strconv.Itoa(offset + limit)
	}

	proto := make([]*accountv1.Account, len(accounts))
	for i, a := range accounts {
		proto[i] = toProtoAccount(a)
	}

	return &accountv1.ListAccountsResponse{
		Accounts:      proto,
		NextPageToken: nextToken,
	}, nil
}

// ── PlaceHold ─────────────────────────────────────────────────────────────────

func (s *AccountServer) PlaceHold(ctx context.Context, req *accountv1.PlaceHoldRequest) (*accountv1.PlaceHoldResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}
	if req.Amount == nil {
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}
	amt, err := parseAmount(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}
	ttl := req.TtlSeconds
	if ttl <= 0 {
		ttl = 172800 // 48h default (T+2 settlement)
	}

	idemKey := "account:hold:" + req.IdempotencyKey
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
	}

	hold, err := s.repo.PlaceHold(ctx, req.AccountId, amt, req.ReferenceId, ttl)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "place hold: %v", err)
	}

	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 0, CreatedAt: time.Now()})

	return &accountv1.PlaceHoldResponse{Hold: toProtoHold(hold)}, nil
}

// ── ReleaseHold ───────────────────────────────────────────────────────────────

func (s *AccountServer) ReleaseHold(ctx context.Context, req *accountv1.ReleaseHoldRequest) (*accountv1.ReleaseHoldResponse, error) {
	if req.HoldId == "" {
		return nil, status.Error(codes.InvalidArgument, "hold_id required")
	}
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	if err := s.repo.ReleaseHold(ctx, req.HoldId, req.AccountId); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "release hold: %v", err)
	}

	return &accountv1.ReleaseHoldResponse{}, nil
}

// ── CreditAccount ─────────────────────────────────────────────────────────────

func (s *AccountServer) CreditAccount(ctx context.Context, req *accountv1.CreditAccountRequest) (*accountv1.CreditAccountResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}
	if req.Amount == nil {
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}
	amt, err := parseAmount(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	idemKey := "account:credit:" + req.IdempotencyKey
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
	}

	acc, err := s.repo.CreditAccount(ctx, req.AccountId, amt)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "credit account: %v", err)
	}

	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 0, CreatedAt: time.Now()})

	return &accountv1.CreditAccountResponse{Account: toProtoAccount(acc)}, nil
}

// ── DebitAccount ──────────────────────────────────────────────────────────────

func (s *AccountServer) DebitAccount(ctx context.Context, req *accountv1.DebitAccountRequest) (*accountv1.DebitAccountResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}
	if req.Amount == nil {
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}
	amt, err := parseAmount(req.Amount)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid amount: %v", err)
	}

	idemKey := "account:debit:" + req.IdempotencyKey
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
	}

	acc, err := s.repo.DebitAccount(ctx, req.AccountId, amt, req.HoldId)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "debit account: %v", err)
	}

	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 0, CreatedAt: time.Now()})

	return &accountv1.DebitAccountResponse{Account: toProtoAccount(acc)}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func parseAmount(m *commonv1.Money) (decimal.Decimal, error) {
	if m.Amount == "" {
		return decimal.Zero, fmt.Errorf("amount string empty")
	}
	d, err := decimal.NewFromString(m.Amount)
	if err != nil {
		return decimal.Zero, err
	}
	if d.LessThanOrEqual(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("amount must be positive")
	}
	return d, nil
}

func toProtoAccount(a *db.Account) *accountv1.Account {
	return &accountv1.Account{
		Id:          a.ID,
		OwnerId:     a.OwnerID,
		Currency:    a.Currency,
		Status:      toProtoStatus(a.Status),
		Balance:     &commonv1.Money{Amount: a.Balance.StringFixed(4), Currency: a.Currency},
		HoldBalance: &commonv1.Money{Amount: a.HoldBalance.StringFixed(4), Currency: a.Currency},
		CreatedAt:   timestamppb.New(a.CreatedAt),
		UpdatedAt:   timestamppb.New(a.UpdatedAt),
	}
}

func toProtoHold(h *db.Hold) *accountv1.Hold {
	return &accountv1.Hold{
		Id:          h.ID,
		AccountId:   h.AccountID,
		Amount:      &commonv1.Money{Amount: h.Amount.StringFixed(4)},
		ReferenceId: h.ReferenceID,
		ExpiresAt:   timestamppb.New(h.ExpiresAt),
		CreatedAt:   timestamppb.New(h.CreatedAt),
	}
}

func toProtoStatus(s string) accountv1.AccountStatus {
	switch s {
	case "ACTIVE":
		return accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	case "SUSPENDED":
		return accountv1.AccountStatus_ACCOUNT_STATUS_SUSPENDED
	case "CLOSED":
		return accountv1.AccountStatus_ACCOUNT_STATUS_CLOSED
	default:
		return accountv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED
	}
}
