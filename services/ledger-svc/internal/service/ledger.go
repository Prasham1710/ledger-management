package service

import (
	"context"
	"errors"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	"github.com/prasham/ledger/internal/idemguard"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/money"
	"github.com/prasham/ledger/services/ledger-svc/internal/db"
)

// maxPageSize bounds ListEntries responses.
const (
	defaultPageSize = 20
	maxPageSize     = 100
)

// LedgerServer implements ledgerv1.LedgerServiceServer.
type LedgerServer struct {
	ledgerv1.UnimplementedLedgerServiceServer
	repo *db.Repo
	idem idempotency.Store
	log  zerolog.Logger
}

func NewLedgerServer(repo *db.Repo, idem idempotency.Store, log zerolog.Logger) *LedgerServer {
	return &LedgerServer{repo: repo, idem: idem, log: log}
}

// PostEntry writes a balanced debit+credit pair in a single DB transaction.
//
// Duplicate suppression is layered: the idempotency store collapses concurrent
// retries and replays the original response, while a unique index on
// (reference_id, kind, type) is the authoritative backstop that holds even if
// the store is unavailable.
func (s *LedgerServer) PostEntry(ctx context.Context, req *ledgerv1.PostEntryRequest) (*ledgerv1.PostEntryResponse, error) {
	amount, err := validatePostEntry(req)
	if err != nil {
		return nil, err
	}

	return idemguard.Do(ctx, s.idem, s.log, "ledger:postentry:"+req.IdempotencyKey,
		func() *ledgerv1.PostEntryResponse { return &ledgerv1.PostEntryResponse{} },
		func(ctx context.Context) (*ledgerv1.PostEntryResponse, error) {
			debit, credit, err := s.repo.PostDoubleEntry(ctx,
				req.DebitAccountId, req.CreditAccountId,
				amount.Value(), string(amount.Currency()),
				req.ReferenceId, req.Description,
			)
			if err != nil {
				return nil, postEntryError(err)
			}
			return &ledgerv1.PostEntryResponse{
				DebitEntry:  toProto(debit),
				CreditEntry: toProto(credit),
			}, nil
		})
}

// ReverseEntry posts a mirror-image pair that neutralises an existing posting.
//
// This is the compensating action sagas invoke when a step after the ledger
// write fails. Without it a failed saga left the ledger asserting a transfer
// that never completed, and no amount of retrying could correct it.
func (s *LedgerServer) ReverseEntry(ctx context.Context, req *ledgerv1.ReverseEntryRequest) (*ledgerv1.ReverseEntryResponse, error) {
	switch {
	case req.IdempotencyKey == "":
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.ReferenceId == "":
		return nil, status.Error(codes.InvalidArgument, "reference_id required")
	}

	return idemguard.Do(ctx, s.idem, s.log, "ledger:reverse:"+req.IdempotencyKey,
		func() *ledgerv1.ReverseEntryResponse { return &ledgerv1.ReverseEntryResponse{} },
		func(ctx context.Context) (*ledgerv1.ReverseEntryResponse, error) {
			debit, credit, err := s.repo.ReverseEntry(ctx, req.ReferenceId, req.Reason)
			switch {
			case errors.Is(err, db.ErrReferenceNotFound):
				return nil, status.Errorf(codes.NotFound,
					"no posting exists for reference_id %q", req.ReferenceId)
			case errors.Is(err, db.ErrDuplicateReference):
				return nil, status.Errorf(codes.AlreadyExists,
					"reference_id %q has already been reversed", req.ReferenceId)
			case err != nil:
				s.log.Error().Err(err).Str("reference_id", req.ReferenceId).Msg("reverse entry failed")
				return nil, status.Error(codes.Internal, "reverse entry failed")
			}
			return &ledgerv1.ReverseEntryResponse{
				DebitEntry:  toProto(debit),
				CreditEntry: toProto(credit),
			}, nil
		})
}

// GetBalance projects the account balance by summing its entries.
func (s *LedgerServer) GetBalance(ctx context.Context, req *ledgerv1.GetBalanceRequest) (*ledgerv1.GetBalanceResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}
	if req.Currency != "" {
		if err := money.Currency(req.Currency).Validate(); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "currency: %v", err)
		}
	}

	bal, currency, err := s.repo.GetBalance(ctx, req.AccountId, req.Currency)
	switch {
	case errors.Is(err, db.ErrAccountNotFound):
		return nil, status.Errorf(codes.NotFound, "account %q has no ledger entries", req.AccountId)
	case errors.Is(err, db.ErrCurrencyMismatch):
		return nil, status.Error(codes.InvalidArgument, err.Error())
	case err != nil:
		s.log.Error().Err(err).Str("account_id", req.AccountId).Msg("get balance failed")
		return nil, status.Error(codes.Internal, "get balance failed")
	}

	amount := money.FromDecimal(bal, money.Currency(currency))
	return &ledgerv1.GetBalanceResponse{
		AccountId: req.AccountId,
		Balance:   &commonv1.Money{Amount: amount.StringMinor(), Currency: currency},
	}, nil
}

// ListEntries returns entries for an account, newest first, with keyset paging.
func (s *LedgerServer) ListEntries(ctx context.Context, req *ledgerv1.ListEntriesRequest) (*ledgerv1.ListEntriesResponse, error) {
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}

	limit := int(req.PageSize)
	switch {
	case limit <= 0:
		limit = defaultPageSize
	case limit > maxPageSize:
		limit = maxPageSize
	}

	entries, nextToken, err := s.repo.ListEntries(ctx, req.AccountId, limit, req.PageToken)
	if err != nil {
		// A bad cursor is the caller's error, not ours.
		if errors.Is(err, db.ErrInvalidPageToken) {
			return nil, status.Errorf(codes.InvalidArgument, "%v", err)
		}
		s.log.Error().Err(err).Str("account_id", req.AccountId).Msg("list entries failed")
		return nil, status.Error(codes.Internal, "list entries failed")
	}

	out := make([]*ledgerv1.Entry, len(entries))
	for i, e := range entries {
		out[i] = toProto(e)
	}
	return &ledgerv1.ListEntriesResponse{Entries: out, NextPageToken: nextToken}, nil
}

// validatePostEntry checks the request and returns the parsed amount.
func validatePostEntry(req *ledgerv1.PostEntryRequest) (money.Amount, error) {
	switch {
	case req.IdempotencyKey == "":
		return money.Amount{}, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.DebitAccountId == "":
		return money.Amount{}, status.Error(codes.InvalidArgument, "debit_account_id required")
	case req.CreditAccountId == "":
		return money.Amount{}, status.Error(codes.InvalidArgument, "credit_account_id required")
	case req.DebitAccountId == req.CreditAccountId:
		return money.Amount{}, status.Error(codes.InvalidArgument, "debit and credit accounts must differ")
	case req.ReferenceId == "":
		// The reference is the deduplication key for the entry pair; without it
		// the uniqueness constraint cannot protect against double posting.
		return money.Amount{}, status.Error(codes.InvalidArgument, "reference_id required")
	case req.Amount == nil:
		return money.Amount{}, status.Error(codes.InvalidArgument, "amount required")
	}

	amount, err := money.Parse(req.Amount.Amount, req.Amount.Currency)
	if err != nil {
		return money.Amount{}, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	return amount, nil
}

// postEntryError maps repository errors onto gRPC status codes. Internal
// failures are logged by the caller and reported without detail so that SQL
// text and schema names never reach a client.
func postEntryError(err error) error {
	switch {
	case errors.Is(err, db.ErrDuplicateReference):
		return status.Error(codes.AlreadyExists,
			"an entry pair already exists for this reference_id")
	case errors.Is(err, db.ErrCurrencyMismatch):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, "post double entry failed")
	}
}

func toProto(e db.Entry) *ledgerv1.Entry {
	t := ledgerv1.EntryType_ENTRY_TYPE_DEBIT
	if e.Type == db.TypeCredit {
		t = ledgerv1.EntryType_ENTRY_TYPE_CREDIT
	}
	amount := money.FromDecimal(e.Amount, money.Currency(e.Currency))
	return &ledgerv1.Entry{
		Id:          e.ID,
		AccountId:   e.AccountID,
		Type:        t,
		Amount:      &commonv1.Money{Amount: amount.StringMinor(), Currency: e.Currency},
		ReferenceId: e.ReferenceID,
		Description: e.Description,
		CreatedAt:   timestamppb.New(e.CreatedAt),
	}
}
