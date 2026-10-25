package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/internal/idemguard"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/money"
	"github.com/prasham/ledger/internal/outbox"
	"github.com/prasham/ledger/services/transaction-svc/internal/db"
)

// sagaName labels this saga in metrics.
const sagaName = "payment"

// compensationTimeout bounds each compensating call. Compensation runs on a
// context detached from the client's, so it needs its own deadline.
const compensationTimeout = 20 * time.Second

type TransactionServer struct {
	txnv1.UnimplementedTransactionServiceServer
	repo    *db.Repo
	idem    idempotency.Store
	ledger  ledgerv1.LedgerServiceClient
	account accountv1.AccountServiceClient
	metrics *metrics.SagaMetrics
	holdTTL time.Duration
	log     zerolog.Logger
}

func NewTransactionServer(
	repo *db.Repo,
	idem idempotency.Store,
	ledger ledgerv1.LedgerServiceClient,
	account accountv1.AccountServiceClient,
	sagaMetrics *metrics.SagaMetrics,
	holdTTL time.Duration,
	log zerolog.Logger,
) *TransactionServer {
	if holdTTL <= 0 {
		holdTTL = time.Hour
	}
	return &TransactionServer{
		repo:    repo,
		idem:    idem,
		ledger:  ledger,
		account: account,
		metrics: sagaMetrics,
		holdTTL: holdTTL,
		log:     log,
	}
}

// InitiatePayment runs the payment saga.
//
// Forward path:
//
//  1. PlaceHold        — reserve source funds
//  2. DebitAccount     — consume the hold, money leaves the source
//  3. CreditAccount    — money arrives at the destination
//  4. PostEntry        — record the completed movement in the ledger
//  5. FinishTransaction— mark COMPLETED and emit payment.completed
//
// The ledger write is deliberately step 4, not step 2. Previously it ran before
// the balances moved, so a failure in either balance step left the ledger
// permanently asserting a transfer that never happened — and there was no
// reversal path to correct it. Recording the movement only once both balances
// have moved means the ledger never claims more than actually occurred.
//
// Every step that takes effect is recorded before the next begins, so
// compensation (here or later, from the recovery worker) knows precisely what
// to undo.
func (s *TransactionServer) InitiatePayment(ctx context.Context, req *txnv1.InitiatePaymentRequest) (*txnv1.InitiatePaymentResponse, error) {
	amount, err := validateInitiatePayment(req)
	if err != nil {
		return nil, err
	}

	return idemguard.Do(ctx, s.idem, s.log, "txn:initiate:"+req.IdempotencyKey,
		func() *txnv1.InitiatePaymentResponse { return &txnv1.InitiatePaymentResponse{} },
		func(ctx context.Context) (*txnv1.InitiatePaymentResponse, error) {
			return s.runPaymentSaga(ctx, req, amount)
		})
}

func (s *TransactionServer) runPaymentSaga(
	ctx context.Context,
	req *txnv1.InitiatePaymentRequest,
	amount money.Amount,
) (*txnv1.InitiatePaymentResponse, error) {
	// Step 0: persist saga state. The unique idempotency_key constraint is the
	// durable backstop behind the cache.
	txn, err := s.repo.CreateTransaction(ctx, &db.Transaction{
		SourceAccountID: req.SourceAccountId,
		DestAccountID:   req.DestAccountId,
		Amount:          amount.StringFixed(),
		Currency:        string(amount.Currency()),
		IdempotencyKey:  req.IdempotencyKey,
		Description:     req.Description,
	})
	if err != nil {
		if errors.Is(err, db.ErrDuplicateIdempotencyKey) {
			// Another attempt already created this saga. Return its current state
			// rather than a bare Internal error.
			if existing, getErr := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey); getErr == nil && existing != nil {
				return &txnv1.InitiatePaymentResponse{Transaction: toProto(existing)}, nil
			}
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		s.log.Error().Err(err).Msg("create transaction failed")
		return nil, status.Error(codes.Internal, "create transaction failed")
	}

	// Reuse the validated proto amount for every downstream call.
	protoAmount := req.Amount

	// ── Step 1: reserve source funds ──────────────────────────────────────────
	holdRes, err := runStep(ctx, s, txn.ID, "PlaceHold", 1, func(ctx context.Context) (*accountv1.PlaceHoldResponse, error) {
		return s.account.PlaceHold(ctx, &accountv1.PlaceHoldRequest{
			IdempotencyKey: "hold:" + txn.ID,
			AccountId:      req.SourceAccountId,
			Amount:         protoAmount,
			ReferenceId:    txn.ID,
			TtlSeconds:     int64(s.holdTTL.Seconds()),
		})
	})
	if err != nil {
		// Nothing has moved: fail without compensation.
		s.failTransaction(ctx, txn.ID, err.Error())
		return nil, wrapStep("place hold", err)
	}
	if holdRes == nil || holdRes.Hold == nil {
		s.failTransaction(ctx, txn.ID, "account service returned no hold")
		return nil, status.Error(codes.Internal, "account service returned no hold")
	}
	holdID := holdRes.Hold.Id

	// Persist the hold id before doing anything else, so a crash here still
	// leaves a record the recovery worker can act on.
	if err := s.repo.SetHoldID(ctx, txn.ID, holdID); err != nil {
		s.log.Error().Err(err).Str("txn_id", txn.ID).Msg("persist hold_id failed")
		s.compensate(ctx, txn.ID, "could not persist hold_id")
		return nil, status.Error(codes.Internal, "persist hold_id failed")
	}
	txn.HoldID = holdID

	// ── Step 2: debit the source by consuming the hold ────────────────────────
	if _, err := runStep(ctx, s, txn.ID, "DebitAccount", 2, func(ctx context.Context) (*accountv1.DebitAccountResponse, error) {
		return s.account.DebitAccount(ctx, &accountv1.DebitAccountRequest{
			IdempotencyKey: "debit:" + txn.ID,
			AccountId:      req.SourceAccountId,
			Amount:         protoAmount,
			HoldId:         holdID,
			ReferenceId:    txn.ID,
		})
	}); err != nil {
		s.compensate(ctx, txn.ID, "debit source: "+err.Error())
		return nil, wrapStep("debit source", err)
	}
	if err := s.repo.MarkStepDone(ctx, txn.ID, "source_debited"); err != nil {
		s.log.Error().Err(err).Str("txn_id", txn.ID).Msg("record source_debited failed")
	}
	txn.SourceDebited = true

	// ── Step 3: credit the destination ────────────────────────────────────────
	if _, err := runStep(ctx, s, txn.ID, "CreditAccount", 3, func(ctx context.Context) (*accountv1.CreditAccountResponse, error) {
		return s.account.CreditAccount(ctx, &accountv1.CreditAccountRequest{
			IdempotencyKey: "credit:" + txn.ID,
			AccountId:      req.DestAccountId,
			Amount:         protoAmount,
			ReferenceId:    txn.ID,
		})
	}); err != nil {
		s.compensate(ctx, txn.ID, "credit destination: "+err.Error())
		return nil, wrapStep("credit destination", err)
	}
	if err := s.repo.MarkStepDone(ctx, txn.ID, "dest_credited"); err != nil {
		s.log.Error().Err(err).Str("txn_id", txn.ID).Msg("record dest_credited failed")
	}
	txn.DestCredited = true

	// ── Step 4: record the completed movement in the ledger ───────────────────
	if _, err := runStep(ctx, s, txn.ID, "PostEntry", 4, func(ctx context.Context) (*ledgerv1.PostEntryResponse, error) {
		return s.ledger.PostEntry(ctx, &ledgerv1.PostEntryRequest{
			IdempotencyKey:  "ledger:" + txn.ID,
			DebitAccountId:  req.SourceAccountId,
			CreditAccountId: req.DestAccountId,
			Amount:          protoAmount,
			ReferenceId:     txn.ID,
			Description:     req.Description,
		})
	}); err != nil {
		s.compensate(ctx, txn.ID, "post ledger entry: "+err.Error())
		return nil, wrapStep("post ledger entry", err)
	}
	if err := s.repo.MarkStepDone(ctx, txn.ID, "ledger_posted"); err != nil {
		s.log.Error().Err(err).Str("txn_id", txn.ID).Msg("record ledger_posted failed")
	}

	// ── Step 5: terminal state + event ────────────────────────────────────────
	if err := s.repo.FinishTransaction(ctx, txn.ID, db.StatusCompleted,
		string(outbox.EventPaymentCompleted), map[string]any{
			"transaction_id":    txn.ID,
			"source_account_id": req.SourceAccountId,
			"dest_account_id":   req.DestAccountId,
			"amount":            amount.StringMinor(),
			"currency":          string(amount.Currency()),
		}); err != nil {
		// The money has moved but the terminal status did not commit. Leave the
		// saga in-flight so the recovery worker resolves it, and report failure
		// rather than claiming a success we did not durably record.
		s.log.Error().Err(err).Str("txn_id", txn.ID).
			Msg("money moved but finalisation failed — recovery worker will resolve")
		s.recordStuck("finalise_failed")
		return nil, status.Error(codes.Internal,
			"payment applied but could not be finalised — query the transaction for its final state")
	}

	// The saga is committed at this point. A failed read-back must not be reported
	// as a failed payment, so return the state we know and log the read error
	// rather than discarding it.
	final, readErr := s.repo.GetTransaction(ctx, txn.ID)
	if readErr != nil || final == nil {
		if readErr != nil {
			s.log.Warn().Err(readErr).Str("txn_id", txn.ID).
				Msg("payment committed but could not be re-read; returning locally known state")
		}
		txn.Status = db.StatusCompleted
		return &txnv1.InitiatePaymentResponse{Transaction: toProto(txn)}, nil
	}
	return &txnv1.InitiatePaymentResponse{Transaction: toProto(final)}, nil
}

func (s *TransactionServer) GetTransaction(ctx context.Context, req *txnv1.GetTransactionRequest) (*txnv1.GetTransactionResponse, error) {
	if req.TransactionId == "" {
		return nil, status.Error(codes.InvalidArgument, "transaction_id required")
	}
	txn, err := s.repo.GetTransaction(ctx, req.TransactionId)
	if err != nil {
		s.log.Error().Err(err).Str("txn_id", req.TransactionId).Msg("get transaction failed")
		return nil, status.Error(codes.Internal, "get transaction failed")
	}
	if txn == nil {
		return nil, status.Errorf(codes.NotFound, "transaction %s not found", req.TransactionId)
	}
	return &txnv1.GetTransactionResponse{Transaction: toProto(txn)}, nil
}

// GetTransactionTrace returns a transaction together with its executed saga
// steps, for the dashboard's saga view.
//
// This replaces an unauthenticated HTTP handler that was mounted on the metrics
// port, where it exposed account identifiers and amounts to anything that could
// reach that port — including Prometheus. As an RPC it is subject to the same
// authorization as every other read.
func (s *TransactionServer) GetTransactionTrace(ctx context.Context, req *txnv1.GetTransactionTraceRequest) (*txnv1.GetTransactionTraceResponse, error) {
	if req.TransactionId == "" {
		return nil, status.Error(codes.InvalidArgument, "transaction_id required")
	}

	txn, err := s.repo.GetTransaction(ctx, req.TransactionId)
	if err != nil {
		s.log.Error().Err(err).Str("txn_id", req.TransactionId).Msg("get transaction failed")
		return nil, status.Error(codes.Internal, "get transaction failed")
	}
	if txn == nil {
		return nil, status.Errorf(codes.NotFound, "transaction %s not found", req.TransactionId)
	}

	steps, err := s.repo.ListSteps(ctx, req.TransactionId)
	if err != nil {
		s.log.Error().Err(err).Str("txn_id", req.TransactionId).Msg("list saga steps failed")
		return nil, status.Error(codes.Internal, "list saga steps failed")
	}

	out := make([]*txnv1.SagaStep, len(steps))
	for i, st := range steps {
		out[i] = &txnv1.SagaStep{
			Seq:        int32(st.Seq), //nolint:gosec // seq is a small positive step index
			StepName:   st.StepName,
			Status:     st.Status,
			DurationMs: st.DurationMs,
			ErrorMsg:   st.ErrorMsg,
			StartedAt:  timestamppb.New(st.StartedAt),
			FinishedAt: timestamppb.New(st.FinishedAt),
		}
	}

	return &txnv1.GetTransactionTraceResponse{
		Transaction: toProto(txn),
		Steps:       out,
	}, nil
}

// ── saga machinery ────────────────────────────────────────────────────────────

// runStep executes one forward saga step, timing it and recording the outcome.
// A package-level function rather than a method because Go methods cannot take
// type parameters.
func runStep[T any](
	ctx context.Context,
	s *TransactionServer,
	txnID, name string,
	seq int,
	fn func(context.Context) (T, error),
) (T, error) {
	start := time.Now()
	result, err := fn(ctx)
	elapsed := time.Since(start)

	s.recordStep(ctx, txnID, name, seq, start, err)
	if s.metrics != nil {
		outcome := "success"
		if err != nil {
			outcome = "failure"
		}
		s.metrics.StepsTotal.WithLabelValues(sagaName, name, outcome).Inc()
		s.metrics.StepDuration.WithLabelValues(sagaName, name).Observe(elapsed.Seconds())
	}
	return result, err
}

// compensate undoes whatever forward steps took effect, then marks the saga
// FAILED.
//
// Two things make this correct that the previous implementation got wrong.
//
// First, it runs on a context detached from the caller's. Compensation is most
// often triggered by a client timeout or cancellation, and the old code passed
// the same cancelled context straight into the compensating RPCs — so the
// rollback failed immediately and the hold leaked forever.
//
// Second, it consults persisted progress and reverses in the opposite order to
// the forward path, so it undoes exactly the effects that exist.
func (s *TransactionServer) compensate(ctx context.Context, txnID, reason string) {
	// Detach from the caller: cancelling the request must not cancel the rollback.
	base := context.WithoutCancel(ctx)

	if err := s.repo.BeginCompensation(base, txnID, reason); err != nil {
		if errors.Is(err, db.ErrTerminal) {
			// Already resolved by another path; nothing to undo.
			return
		}
		s.log.Error().Err(err).Str("txn_id", txnID).Msg("could not mark saga compensating")
	}

	txn, err := s.repo.GetTransaction(base, txnID)
	if err != nil || txn == nil {
		s.log.Error().Err(err).Str("txn_id", txnID).
			Msg("cannot load saga state to compensate — manual intervention required")
		return
	}

	s.compensateFrom(base, txn, reason)
}

// compensateFrom performs the actual reversal for a loaded transaction. Shared
// with the recovery worker so inline and deferred compensation cannot diverge.
func (s *TransactionServer) compensateFrom(ctx context.Context, txn *db.Transaction, reason string) {
	allOK := true

	// Reverse order: ledger first, then destination, then source, then the hold.
	if txn.LedgerPosted {
		allOK = s.compensateStep(ctx, txn.ID, "ReverseLedgerEntry", func(ctx context.Context) error {
			_, err := s.ledger.ReverseEntry(ctx, &ledgerv1.ReverseEntryRequest{
				IdempotencyKey: "reverse:" + txn.ID,
				ReferenceId:    txn.ID,
				Reason:         reason,
			})
			// An already-reversed entry means a previous attempt succeeded.
			if status.Code(err) == codes.AlreadyExists {
				return nil
			}
			return err
		}) && allOK
	}

	if txn.DestCredited {
		allOK = s.compensateStep(ctx, txn.ID, "DebitDestination", func(ctx context.Context) error {
			// Take the credited funds back out of the destination.
			_, err := s.account.DebitAccount(ctx, &accountv1.DebitAccountRequest{
				IdempotencyKey: "compensate-debit-dest:" + txn.ID,
				AccountId:      txn.DestAccountID,
				Amount:         &commonv1.Money{Amount: txn.Amount, Currency: txn.Currency},
				ReferenceId:    "compensate:" + txn.ID,
			})
			if status.Code(err) == codes.AlreadyExists {
				return nil
			}
			return err
		}) && allOK
	}

	if txn.SourceDebited {
		allOK = s.compensateStep(ctx, txn.ID, "CreditSource", func(ctx context.Context) error {
			// Return the debited funds to the source.
			_, err := s.account.CreditAccount(ctx, &accountv1.CreditAccountRequest{
				IdempotencyKey: "compensate-credit-src:" + txn.ID,
				AccountId:      txn.SourceAccountID,
				Amount:         &commonv1.Money{Amount: txn.Amount, Currency: txn.Currency},
				ReferenceId:    "compensate:" + txn.ID,
			})
			if status.Code(err) == codes.AlreadyExists {
				return nil
			}
			return err
		}) && allOK
	}

	// Release the reservation only if it was never consumed by a debit. If the
	// source was debited, the hold is already CONSUMED and releasing it would
	// both fail and be wrong.
	if txn.HoldID != "" && !txn.SourceDebited {
		allOK = s.compensateStep(ctx, txn.ID, "ReleaseHold", func(ctx context.Context) error {
			_, err := s.account.ReleaseHold(ctx, &accountv1.ReleaseHoldRequest{
				HoldId:    txn.HoldID,
				AccountId: txn.SourceAccountID,
			})
			// Already released or consumed: nothing left to do.
			if status.Code(err) == codes.AlreadyExists || status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		}) && allOK
	}

	if !allOK {
		// Leave the saga in COMPENSATING so the recovery worker retries. Marking
		// it FAILED now would assert a clean rollback that did not happen.
		s.log.Error().Str("txn_id", txn.ID).
			Msg("compensation incomplete — saga left COMPENSATING for retry")
		return
	}

	s.failTransaction(ctx, txn.ID, reason)
}

// compensateStep runs one compensating action with its own deadline and records
// the outcome. Returns true on success.
func (s *TransactionServer) compensateStep(
	ctx context.Context,
	txnID, name string,
	fn func(context.Context) error,
) bool {
	stepCtx, cancel := context.WithTimeout(ctx, compensationTimeout)
	defer cancel()

	err := fn(stepCtx)
	outcome := "success"
	if err != nil {
		outcome = "failure"
		// A failed compensation means money is stranded. This is the loudest
		// signal the platform produces.
		s.log.Error().Err(err).
			Str("txn_id", txnID).
			Str("compensation", name).
			Msg("COMPENSATION FAILED — funds may be stranded, manual reconciliation required")
	} else {
		s.log.Info().Str("txn_id", txnID).Str("compensation", name).Msg("compensated")
	}
	if s.metrics != nil {
		s.metrics.CompensationsTotal.WithLabelValues(sagaName, name, outcome).Inc()
	}
	return err == nil
}

// failTransaction records the terminal FAILED state and emits payment.failed.
//
// The success path does not go through here: it calls FinishTransaction directly
// with the full payment payload, because a completed payment's event carries the
// accounts and amount while a failure carries the reason.
func (s *TransactionServer) failTransaction(ctx context.Context, txnID, reason string) {
	const txnStatus = db.StatusFailed

	err := s.repo.FinishTransaction(context.WithoutCancel(ctx), txnID, txnStatus,
		string(outbox.EventPaymentFailed), map[string]any{
			"transaction_id": txnID,
			"status":         txnStatus,
			"reason":         reason,
		})
	if err != nil && !errors.Is(err, db.ErrTerminal) {
		s.log.Error().Err(err).Str("txn_id", txnID).Str("status", txnStatus).
			Msg("could not persist terminal status")
	}
}

func (s *TransactionServer) recordStep(ctx context.Context, txnID, name string, seq int, start time.Time, stepErr error) {
	st := "success"
	errMsg := ""
	if stepErr != nil {
		st = "failed"
		errMsg = stepErr.Error()
	}
	now := time.Now()
	// Detached: the step outcome must be recorded even when the failure was a
	// cancellation of the request context.
	if err := s.repo.RecordStep(context.WithoutCancel(ctx), &db.SagaStep{
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

func (s *TransactionServer) recordStuck(outcome string) {
	if s.metrics != nil {
		s.metrics.StuckTotal.WithLabelValues(sagaName, outcome).Inc()
	}
}

// ── validation and conversion ─────────────────────────────────────────────────

func validateInitiatePayment(req *txnv1.InitiatePaymentRequest) (money.Amount, error) {
	switch {
	case req.IdempotencyKey == "":
		return money.Amount{}, status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.SourceAccountId == "":
		return money.Amount{}, status.Error(codes.InvalidArgument, "source_account_id required")
	case req.DestAccountId == "":
		return money.Amount{}, status.Error(codes.InvalidArgument, "dest_account_id required")
	case req.SourceAccountId == req.DestAccountId:
		return money.Amount{}, status.Error(codes.InvalidArgument, "source and destination accounts must differ")
	case req.Amount == nil:
		return money.Amount{}, status.Error(codes.InvalidArgument, "amount required")
	}

	amount, err := money.Parse(req.Amount.Amount, req.Amount.Currency)
	if err != nil {
		return money.Amount{}, status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}
	return amount, nil
}

// wrapStep preserves the downstream status code so callers can distinguish a
// rejected request from a transient failure.
func wrapStep(step string, err error) error {
	return status.Errorf(status.Code(err), "%s: %s", step, status.Convert(err).Message())
}

func toProto(t *db.Transaction) *txnv1.Transaction {
	if t == nil {
		return nil
	}
	amountStr := t.Amount
	if amt, err := money.New(t.Amount, money.Currency(t.Currency)); err == nil {
		amountStr = amt.StringMinor()
	}
	return &txnv1.Transaction{
		Id:              t.ID,
		Status:          statusProto(t.Status),
		SourceAccountId: t.SourceAccountID,
		DestAccountId:   t.DestAccountID,
		Amount:          &commonv1.Money{Amount: amountStr, Currency: t.Currency},
		IdempotencyKey:  t.IdempotencyKey,
		Description:     t.Description,
		CreatedAt:       timestamppb.New(t.CreatedAt),
		UpdatedAt:       timestamppb.New(t.UpdatedAt),
	}
}

func statusProto(s string) txnv1.TransactionStatus {
	switch s {
	case db.StatusPending, db.StatusCompensating:
		// COMPENSATING has no distinct proto value; it is still in flight from a
		// caller's perspective.
		return txnv1.TransactionStatus_TRANSACTION_STATUS_PENDING
	case db.StatusCompleted:
		return txnv1.TransactionStatus_TRANSACTION_STATUS_COMPLETED
	case db.StatusFailed:
		return txnv1.TransactionStatus_TRANSACTION_STATUS_FAILED
	case db.StatusReversed:
		return txnv1.TransactionStatus_TRANSACTION_STATUS_REVERSED
	default:
		return txnv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED
	}
}

// RecoverStuck is called by the recovery worker for a saga abandoned mid-flight.
// It compensates whatever took effect and drives the saga to a terminal state.
func (s *TransactionServer) RecoverStuck(ctx context.Context, txn *db.Transaction) {
	s.log.Warn().
		Str("txn_id", txn.ID).
		Str("status", txn.Status).
		Bool("ledger_posted", txn.LedgerPosted).
		Bool("source_debited", txn.SourceDebited).
		Bool("dest_credited", txn.DestCredited).
		Msg("recovering abandoned saga")

	if !txn.NeedsCompensation() {
		// Nothing took effect: fail it outright.
		s.failTransaction(ctx, txn.ID, "abandoned before any effect; failed by recovery worker")
		s.recordStuck("failed_clean")
		return
	}

	reason := fmt.Sprintf("abandoned mid-flight (last update %s); compensated by recovery worker",
		txn.UpdatedAt.UTC().Format(time.RFC3339))

	if err := s.repo.BeginCompensation(ctx, txn.ID, reason); err != nil && !errors.Is(err, db.ErrTerminal) {
		s.log.Error().Err(err).Str("txn_id", txn.ID).Msg("could not mark saga compensating")
	}
	s.compensateFrom(ctx, txn, reason)
	s.recordStuck("compensated")
}
