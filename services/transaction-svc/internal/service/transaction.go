package service

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/transaction-svc/internal/db"
)

type TransactionServer struct {
	txnv1.UnimplementedTransactionServiceServer
	repo    *db.Repo
	idem    idempotency.Store
	ledger  ledgerv1.LedgerServiceClient
	account accountv1.AccountServiceClient
	log     zerolog.Logger
}

func NewTransactionServer(
	repo *db.Repo,
	idem idempotency.Store,
	ledger ledgerv1.LedgerServiceClient,
	account accountv1.AccountServiceClient,
	log zerolog.Logger,
) *TransactionServer {
	return &TransactionServer{repo: repo, idem: idem, ledger: ledger, account: account, log: log}
}

// InitiatePayment runs the payment saga:
// PlaceHold → PostEntry → DebitAccount (via hold) → CreditAccount → COMPLETED + outbox.
// Any failure after PlaceHold attempts a compensating ReleaseHold before marking FAILED.
func (s *TransactionServer) InitiatePayment(ctx context.Context, req *txnv1.InitiatePaymentRequest) (*txnv1.InitiatePaymentResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.SourceAccountId == "" || req.DestAccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "source_account_id and dest_account_id required")
	}
	if req.Amount == nil || req.Amount.Amount == "" || req.Amount.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}
	if req.SourceAccountId == req.DestAccountId {
		return nil, status.Error(codes.InvalidArgument, "source and destination accounts must differ")
	}

	idemKey := "txn:initiate:" + req.IdempotencyKey

	// Idempotency check — replay returns the existing transaction.
	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		txn, err := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil || txn == nil {
			return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
		}
		return &txnv1.InitiatePaymentResponse{Transaction: toProto(txn)}, nil
	}

	// Step 1: persist saga row in PENDING state.
	txn, err := s.repo.CreateTransaction(ctx, &db.Transaction{
		SourceAccountID: req.SourceAccountId,
		DestAccountID:   req.DestAccountId,
		Amount:          req.Amount.Amount,
		Currency:        req.Amount.Currency,
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create transaction: %v", err)
	}

	// Step 2: reserve funds on the source account.
	t0 := time.Now()
	holdRes, err := s.account.PlaceHold(ctx, &accountv1.PlaceHoldRequest{
		IdempotencyKey: "hold:" + txn.ID,
		AccountId:      req.SourceAccountId,
		Amount:         req.Amount,
		ReferenceId:    txn.ID,
		TtlSeconds:     86400,
	})
	s.recordStep(ctx, txn.ID, "PlaceHold", 1, t0, err)
	if err != nil {
		s.fail(ctx, txn.ID, req.SourceAccountId, "", err.Error())
		return nil, status.Errorf(grpcCode(err), "place hold: %v", err)
	}
	holdID := holdRes.Hold.Id

	// Persist hold_id so a recovery process can release it on crash.
	if err := s.repo.SetHoldID(ctx, txn.ID, holdID); err != nil {
		s.releaseHold(ctx, req.SourceAccountId, holdID)
		s.fail(ctx, txn.ID, req.SourceAccountId, holdID, "persist hold_id: "+err.Error())
		return nil, status.Errorf(codes.Internal, "persist hold_id: %v", err)
	}

	// Step 3: write double-entry accounting record.
	t1 := time.Now()
	_, err = s.ledger.PostEntry(ctx, &ledgerv1.PostEntryRequest{
		IdempotencyKey:  "ledger:" + txn.ID,
		DebitAccountId:  req.SourceAccountId,
		CreditAccountId: req.DestAccountId,
		Amount:          req.Amount,
		ReferenceId:     txn.ID,
		Description:     req.Description,
	})
	s.recordStep(ctx, txn.ID, "PostEntry", 2, t1, err)
	if err != nil {
		s.releaseHold(ctx, req.SourceAccountId, holdID)
		s.fail(ctx, txn.ID, req.SourceAccountId, holdID, err.Error())
		return nil, status.Errorf(grpcCode(err), "post ledger entry: %v", err)
	}

	// Step 4: consume the hold on the source account (saga commit).
	t2 := time.Now()
	_, err = s.account.DebitAccount(ctx, &accountv1.DebitAccountRequest{
		IdempotencyKey: "debit:" + txn.ID,
		AccountId:      req.SourceAccountId,
		Amount:         req.Amount,
		HoldId:         holdID,
		ReferenceId:    txn.ID,
	})
	s.recordStep(ctx, txn.ID, "DebitAccount", 3, t2, err)
	if err != nil {
		// Hold already consumed by ledger entry; best-effort fail.
		s.fail(ctx, txn.ID, req.SourceAccountId, holdID, err.Error())
		return nil, status.Errorf(grpcCode(err), "debit source: %v", err)
	}

	// Step 5: credit the destination account.
	t3 := time.Now()
	_, err = s.account.CreditAccount(ctx, &accountv1.CreditAccountRequest{
		IdempotencyKey: "credit:" + txn.ID,
		AccountId:      req.DestAccountId,
		Amount:         req.Amount,
		ReferenceId:    txn.ID,
	})
	s.recordStep(ctx, txn.ID, "CreditAccount", 4, t3, err)
	if err != nil {
		// Source already debited; this is a partial failure requiring reconciliation.
		s.log.Error().Err(err).Str("txn_id", txn.ID).Msg("credit dest failed — saga partial failure")
		s.fail(ctx, txn.ID, req.SourceAccountId, holdID, err.Error())
		return nil, status.Errorf(grpcCode(err), "credit destination: %v", err)
	}

	// Step 6: atomically mark COMPLETED and publish outbox event.
	t4 := time.Now()
	finishErr := s.repo.FinishTransaction(ctx, txn.ID, "COMPLETED", "payment.completed", map[string]any{
		"transaction_id":    txn.ID,
		"source_account_id": req.SourceAccountId,
		"dest_account_id":   req.DestAccountId,
		"amount":            req.Amount.Amount,
		"currency":          req.Amount.Currency,
	})
	s.recordStep(ctx, txn.ID, "FinishTransaction", 5, t4, finishErr)
	if finishErr != nil {
		// Payment succeeded but state not persisted — log, don't return error.
		s.log.Error().Err(finishErr).Str("txn_id", txn.ID).Msg("finalize transaction failed")
	}

	// Cache idempotency result.
	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 200, CreatedAt: time.Now()})

	txn, _ = s.repo.GetTransaction(ctx, txn.ID)
	return &txnv1.InitiatePaymentResponse{Transaction: toProto(txn)}, nil
}

func (s *TransactionServer) GetTransaction(ctx context.Context, req *txnv1.GetTransactionRequest) (*txnv1.GetTransactionResponse, error) {
	if req.TransactionId == "" {
		return nil, status.Error(codes.InvalidArgument, "transaction_id required")
	}
	txn, err := s.repo.GetTransaction(ctx, req.TransactionId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get transaction: %v", err)
	}
	if txn == nil {
		return nil, status.Errorf(codes.NotFound, "transaction %s not found", req.TransactionId)
	}
	return &txnv1.GetTransactionResponse{Transaction: toProto(txn)}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *TransactionServer) recordStep(ctx context.Context, txnID, name string, seq int, start time.Time, stepErr error) {
	st := "success"
	errMsg := ""
	if stepErr != nil {
		st = "failed"
		errMsg = stepErr.Error()
	}
	now := time.Now()
	if err := s.repo.RecordStep(ctx, &db.SagaStep{
		TransactionID: txnID,
		StepName:      name,
		Status:        st,
		Seq:           seq,
		StartedAt:     start,
		FinishedAt:    now,
		DurationMs:    now.Sub(start).Milliseconds(),
		ErrorMsg:      errMsg,
	}); err != nil {
		s.log.Warn().Err(err).Str("txn_id", txnID).Str("step", name).Msg("record saga step failed")
	}
}

func (s *TransactionServer) fail(ctx context.Context, id, accountID, holdID, reason string) {
	if holdID != "" {
		s.releaseHold(ctx, accountID, holdID)
	}
	if err := s.repo.FinishTransaction(ctx, id, "FAILED", "payment.failed", map[string]any{
		"transaction_id": id,
		"reason":         reason,
	}); err != nil {
		s.log.Error().Err(err).Str("txn_id", id).Msg("failed to persist FAILED status")
	}
}

func (s *TransactionServer) releaseHold(ctx context.Context, accountID, holdID string) {
	if _, err := s.account.ReleaseHold(ctx, &accountv1.ReleaseHoldRequest{
		HoldId:    holdID,
		AccountId: accountID,
	}); err != nil {
		s.log.Warn().Err(err).Str("hold_id", holdID).Msg("compensating ReleaseHold failed")
	}
}

func toProto(t *db.Transaction) *txnv1.Transaction {
	if t == nil {
		return nil
	}
	return &txnv1.Transaction{
		Id:              t.ID,
		Status:          statusProto(t.Status),
		SourceAccountId: t.SourceAccountID,
		DestAccountId:   t.DestAccountID,
		Amount:          &commonv1.Money{Amount: t.Amount, Currency: t.Currency},
		IdempotencyKey:  t.IdempotencyKey,
		Description:     t.Description,
		CreatedAt:       timestamppb.New(t.CreatedAt),
		UpdatedAt:       timestamppb.New(t.UpdatedAt),
	}
}

func statusProto(s string) txnv1.TransactionStatus {
	switch s {
	case "PENDING":
		return txnv1.TransactionStatus_TRANSACTION_STATUS_PENDING
	case "COMPLETED":
		return txnv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED
	case "FAILED":
		return txnv1.TransactionStatus_TRANSACTION_STATUS_FAILED
	case "REVERSED":
		return txnv1.TransactionStatus_TRANSACTION_STATUS_REVERSED
	default:
		return txnv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	}
}

func grpcCode(err error) codes.Code {
	if st, ok := status.FromError(err); ok {
		return st.Code()
	}
	return codes.Internal
}
