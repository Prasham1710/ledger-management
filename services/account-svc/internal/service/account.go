package service

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	"github.com/prasham/ledger/internal/idemguard"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/money"
	"github.com/prasham/ledger/services/account-svc/internal/db"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100

	// defaultHoldTTL matches T+2 settlement. Callers should set an explicit TTL
	// that reflects how long their saga can plausibly take.
	defaultHoldTTL = 48 * time.Hour
	// maxHoldTTL bounds how long a caller may reserve someone's funds. An
	// unbounded TTL combined with a crashed saga is indistinguishable from
	// confiscation.
	maxHoldTTL = 7 * 24 * time.Hour
)

type AccountServer struct {
	accountv1.UnimplementedAccountServiceServer
	repo *db.Repo
	idem idempotency.Store
	log  zerolog.Logger
}

func NewAccountServer(repo *db.Repo, idem idempotency.Store, log zerolog.Logger) *AccountServer {
	return &AccountServer{repo: repo, idem: idem, log: log}
}

// ── CreateAccount ─────────────────────────────────────────────────────────────

func (s *AccountServer) CreateAccount(ctx context.Context, req *accountv1.CreateAccountRequest) (*accountv1.CreateAccountResponse, error) {
	switch {
	case req.IdempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.OwnerId == "":
		return nil, status.Error(codes.InvalidArgument, "owner_id required")
	case req.Currency == "":
		return nil, status.Error(codes.InvalidArgument, "currency required")
	}
	if err := money.Currency(req.Currency).Validate(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "currency: %v", err)
	}

	return idemguard.Do(ctx, s.idem, s.log, "account:create:"+req.IdempotencyKey,
		func() *accountv1.CreateAccountResponse { return &accountv1.CreateAccountResponse{} },
		func(ctx context.Context) (*accountv1.CreateAccountResponse, error) {
			acc, err := s.repo.CreateAccount(ctx, req.OwnerId, req.Currency)
			if err != nil {
				// An owner may hold only one active account per currency. That is
				// a caller-visible conflict, not an internal fault — it was
				// previously surfaced as a raw Postgres error under codes.Internal.
				if errors.Is(err, db.ErrAccountExists) {
					return nil, status.Errorf(codes.AlreadyExists,
						"owner %s already has an active %s account", req.OwnerId, req.Currency)
				}
				s.log.Error().Err(err).Str("owner_id", req.OwnerId).Msg("create account failed")
				return nil, status.Error(codes.Internal, "create account failed")
			}
			return &accountv1.CreateAccountResponse{Account: toProtoAccount(acc)}, nil
		})
}

// ── GetAccount ────────────────────────────────────────────────────────────────

func (s *AccountServer) GetAccount(ctx context.Context, req *accountv1.GetAccountRequest) (*accountv1.GetAccountResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	acc, err := s.repo.GetAccount(ctx, req.AccountId)
	if err != nil {
		s.log.Error().Err(err).Str("account_id", req.AccountId).Msg("get account failed")
		return nil, status.Error(codes.Internal, "get account failed")
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
	switch {
	case limit <= 0:
		limit = defaultPageSize
	case limit > maxPageSize:
		limit = maxPageSize
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
		s.log.Error().Err(err).Str("owner_id", req.OwnerId).Msg("list accounts failed")
		return nil, status.Error(codes.Internal, "list accounts failed")
	}

	var nextToken string
	if len(accounts) > limit {
		accounts = accounts[:limit]
		nextToken = strconv.Itoa(offset + limit)
	}

	out := make([]*accountv1.Account, len(accounts))
	for i, a := range accounts {
		out[i] = toProtoAccount(a)
	}
	return &accountv1.ListAccountsResponse{Accounts: out, NextPageToken: nextToken}, nil
}

// ── PlaceHold ─────────────────────────────────────────────────────────────────

func (s *AccountServer) PlaceHold(ctx context.Context, req *accountv1.PlaceHoldRequest) (*accountv1.PlaceHoldResponse, error) {
	switch {
	case req.IdempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.AccountId == "":
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	case req.ReferenceId == "":
		// The reference is the durable dedup key for the reservation.
		return nil, status.Error(codes.InvalidArgument, "reference_id required")
	case req.Amount == nil:
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}

	amount, err := money.Parse(req.Amount.Amount, req.Amount.Currency)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}

	ttl := defaultHoldTTL
	if req.TtlSeconds > 0 {
		ttl = time.Duration(req.TtlSeconds) * time.Second
	}
	if ttl > maxHoldTTL {
		return nil, status.Errorf(codes.InvalidArgument,
			"ttl_seconds must not exceed %d (%s)", int64(maxHoldTTL.Seconds()), maxHoldTTL)
	}

	return idemguard.Do(ctx, s.idem, s.log, "account:hold:"+req.IdempotencyKey,
		func() *accountv1.PlaceHoldResponse { return &accountv1.PlaceHoldResponse{} },
		func(ctx context.Context) (*accountv1.PlaceHoldResponse, error) {
			hold, err := s.repo.PlaceHold(ctx, req.AccountId,
				amount.Value(), string(amount.Currency()), req.ReferenceId, ttl)
			if err != nil {
				return nil, s.holdError(err, "place hold")
			}
			return &accountv1.PlaceHoldResponse{Hold: toProtoHold(hold)}, nil
		})
}

// ── ReleaseHold ───────────────────────────────────────────────────────────────

func (s *AccountServer) ReleaseHold(ctx context.Context, req *accountv1.ReleaseHoldRequest) (*accountv1.ReleaseHoldResponse, error) {
	switch {
	case req.HoldId == "":
		return nil, status.Error(codes.InvalidArgument, "hold_id required")
	case req.AccountId == "":
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	if err := s.repo.ReleaseHold(ctx, req.HoldId, req.AccountId); err != nil {
		// Releasing an already-resolved hold is reported as AlreadyExists rather
		// than retried: a second release would return the funds twice.
		if errors.Is(err, db.ErrHoldNotActive) {
			return nil, status.Errorf(codes.AlreadyExists, "hold %s is already resolved", req.HoldId)
		}
		return nil, s.holdError(err, "release hold")
	}
	return &accountv1.ReleaseHoldResponse{}, nil
}

// ── CreditAccount ─────────────────────────────────────────────────────────────

func (s *AccountServer) CreditAccount(ctx context.Context, req *accountv1.CreditAccountRequest) (*accountv1.CreditAccountResponse, error) {
	switch {
	case req.IdempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.AccountId == "":
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	case req.ReferenceId == "":
		return nil, status.Error(codes.InvalidArgument, "reference_id required")
	case req.Amount == nil:
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}

	amount, err := money.Parse(req.Amount.Amount, req.Amount.Currency)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}

	return idemguard.Do(ctx, s.idem, s.log, "account:credit:"+req.IdempotencyKey,
		func() *accountv1.CreditAccountResponse { return &accountv1.CreditAccountResponse{} },
		func(ctx context.Context) (*accountv1.CreditAccountResponse, error) {
			acc, err := s.repo.CreditAccount(ctx, req.AccountId,
				amount.Value(), string(amount.Currency()), req.ReferenceId)
			if err != nil {
				return nil, s.balanceError(err, "credit account")
			}
			return &accountv1.CreditAccountResponse{Account: toProtoAccount(acc)}, nil
		})
}

// ── DebitAccount ──────────────────────────────────────────────────────────────

func (s *AccountServer) DebitAccount(ctx context.Context, req *accountv1.DebitAccountRequest) (*accountv1.DebitAccountResponse, error) {
	switch {
	case req.IdempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.AccountId == "":
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	case req.ReferenceId == "":
		return nil, status.Error(codes.InvalidArgument, "reference_id required")
	case req.Amount == nil:
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}

	amount, err := money.Parse(req.Amount.Amount, req.Amount.Currency)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}

	return idemguard.Do(ctx, s.idem, s.log, "account:debit:"+req.IdempotencyKey,
		func() *accountv1.DebitAccountResponse { return &accountv1.DebitAccountResponse{} },
		func(ctx context.Context) (*accountv1.DebitAccountResponse, error) {
			acc, err := s.repo.DebitAccount(ctx, req.AccountId,
				amount.Value(), string(amount.Currency()), req.HoldId, req.ReferenceId)
			if err != nil {
				return nil, s.balanceError(err, "debit account")
			}
			return &accountv1.DebitAccountResponse{Account: toProtoAccount(acc)}, nil
		})
}

// ── error mapping ─────────────────────────────────────────────────────────────

// holdError maps hold-related repository errors onto gRPC codes.
func (s *AccountServer) holdError(err error, op string) error {
	switch {
	case errors.Is(err, db.ErrAccountNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, db.ErrHoldNotFound):
		return status.Error(codes.NotFound, "hold not found")
	case errors.Is(err, db.ErrAccountNotActive),
		errors.Is(err, db.ErrInsufficientBalance),
		errors.Is(err, db.ErrHoldNotActive),
		errors.Is(err, db.ErrHoldExpired),
		errors.Is(err, db.ErrHoldAmountMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, db.ErrCurrencyMismatch):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, db.ErrDuplicateReference):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		s.log.Error().Err(err).Msg(op + " failed")
		return status.Error(codes.Internal, op+" failed")
	}
}

// balanceError maps credit/debit repository errors onto gRPC codes.
func (s *AccountServer) balanceError(err error, op string) error {
	if errors.Is(err, db.ErrDuplicateMutation) {
		// The mutation was already applied — the database caught a replay the
		// idempotency cache missed. Report it rather than moving money twice.
		return status.Error(codes.AlreadyExists, err.Error())
	}
	return s.holdError(err, op)
}

// ── proto conversion ──────────────────────────────────────────────────────────

func toProtoAccount(a *db.Account) *accountv1.Account {
	cur := money.Currency(a.Currency)
	return &accountv1.Account{
		Id:       a.ID,
		OwnerId:  a.OwnerID,
		Currency: a.Currency,
		Status:   toProtoStatus(a.Status),
		Balance: &commonv1.Money{
			Amount:   money.FromDecimal(a.Balance, cur).StringMinor(),
			Currency: a.Currency,
		},
		HoldBalance: &commonv1.Money{
			Amount:   money.FromDecimal(a.HoldBalance, cur).StringMinor(),
			Currency: a.Currency,
		},
		CreatedAt: timestamppb.New(a.CreatedAt),
		UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
}

func toProtoHold(h *db.Hold) *accountv1.Hold {
	return &accountv1.Hold{
		Id:        h.ID,
		AccountId: h.AccountID,
		Amount: &commonv1.Money{
			Amount: money.FromDecimal(h.Amount, money.Currency(h.Currency)).StringMinor(),
			// Previously omitted, so every hold came back with an empty currency.
			Currency: h.Currency,
		},
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
