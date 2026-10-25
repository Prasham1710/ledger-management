package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	sharedconfig "github.com/prasham/ledger/internal/config"
	"github.com/prasham/ledger/internal/idemguard"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/internal/metrics"
	"github.com/prasham/ledger/internal/money"
	"github.com/prasham/ledger/services/payout-svc/internal/db"
	"github.com/prasham/ledger/services/payout-svc/internal/storage"
)

// CLEARINGHOUSE is the virtual ledger account representing the bank settlement
// side. Funds leaving the platform are credited here so the ledger stays
// balanced.
const CLEARINGHOUSE = "PAYOUT_CLEARINGHOUSE"

// sagaName labels this saga in metrics.
const sagaName = "payout"

// compensationTimeout bounds each compensating call. Compensation runs on a
// context detached from the client's, so it needs its own deadline.
const compensationTimeout = 20 * time.Second

type PayoutServer struct {
	payoutv1.UnimplementedPayoutServiceServer
	repo    *db.Repo
	idem    idempotency.Store
	ledger  ledgerv1.LedgerServiceClient
	account accountv1.AccountServiceClient
	storage *storage.MinIO
	metrics *metrics.SagaMetrics
	log     zerolog.Logger
}

func NewPayoutServer(
	repo *db.Repo,
	idem idempotency.Store,
	ledger ledgerv1.LedgerServiceClient,
	account accountv1.AccountServiceClient,
	store *storage.MinIO,
	sagaMetrics *metrics.SagaMetrics,
	log zerolog.Logger,
) *PayoutServer {
	return &PayoutServer{
		repo:    repo,
		idem:    idem,
		ledger:  ledger,
		account: account,
		storage: store,
		metrics: sagaMetrics,
		log:     log,
	}
}

// InitiatePayout runs the payout saga.
//
// Forward path:
//
//  1. DebitAccount  — funds leave the customer's available balance
//  2. PostEntry     — ledger records the movement to the clearinghouse
//  3. Bank file     — the instruction is written to object storage
//  4. ProcessPayout — mark PROCESSING and emit payout.initiated
//
// Every step after the debit can fail, and each failure now returns the money.
// Previously a ledger failure logged "manual reconciliation required" and left
// the debit standing, and a bank-file failure was downgraded to a warning while
// the payout advanced to PROCESSING with an empty file key — a payout the bank
// would never receive, with the customer's funds already gone.
func (s *PayoutServer) InitiatePayout(ctx context.Context, req *payoutv1.InitiatePayoutRequest) (*payoutv1.InitiatePayoutResponse, error) {
	amount, rail, err := s.validate(req)
	if err != nil {
		return nil, err
	}

	return idemguard.Do(ctx, s.idem, s.log, "payout:initiate:"+req.IdempotencyKey,
		func() *payoutv1.InitiatePayoutResponse { return &payoutv1.InitiatePayoutResponse{} },
		func(ctx context.Context) (*payoutv1.InitiatePayoutResponse, error) {
			return s.runPayoutSaga(ctx, req, amount, rail)
		})
}

func (s *PayoutServer) runPayoutSaga(
	ctx context.Context,
	req *payoutv1.InitiatePayoutRequest,
	amount money.Amount,
	rail string,
) (*payoutv1.InitiatePayoutResponse, error) {
	// Step 0: persist saga state.
	payout, err := s.repo.CreatePayout(ctx, &db.Payout{
		AccountID:      req.AccountId,
		Amount:         amount.StringFixed(),
		Currency:       string(amount.Currency()),
		Rail:           rail,
		Destination:    toDestDB(req.Destination),
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		if errors.Is(err, db.ErrDuplicateIdempotencyKey) {
			if existing, getErr := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey); getErr == nil && existing != nil {
				return &payoutv1.InitiatePayoutResponse{Payout: toProto(existing)}, nil
			}
			return nil, status.Error(codes.AlreadyExists, err.Error())
		}
		s.log.Error().Err(err).Msg("create payout failed")
		return nil, status.Error(codes.Internal, "create payout failed")
	}

	protoAmount := req.Amount

	// ── Step 1: debit the customer ────────────────────────────────────────────
	if _, err := runStep(ctx, s, payout.ID, "DebitAccount", 1, func(ctx context.Context) (*accountv1.DebitAccountResponse, error) {
		return s.account.DebitAccount(ctx, &accountv1.DebitAccountRequest{
			IdempotencyKey: "payout:debit:" + payout.ID,
			AccountId:      req.AccountId,
			Amount:         protoAmount,
			ReferenceId:    payout.ID,
		})
	}); err != nil {
		// Nothing moved: fail without compensation.
		s.fail(ctx, payout.ID, "debit account: "+err.Error())
		return nil, wrapStep("debit account", err)
	}
	if err := s.repo.MarkStepDone(ctx, payout.ID, "account_debited"); err != nil {
		s.log.Error().Err(err).Str("payout_id", payout.ID).Msg("record account_debited failed")
	}
	payout.AccountDebited = true

	// ── Step 2: record the movement in the ledger ─────────────────────────────
	if _, err := runStep(ctx, s, payout.ID, "PostEntry", 2, func(ctx context.Context) (*ledgerv1.PostEntryResponse, error) {
		return s.ledger.PostEntry(ctx, &ledgerv1.PostEntryRequest{
			IdempotencyKey:  "payout:ledger:" + payout.ID,
			DebitAccountId:  req.AccountId,
			CreditAccountId: CLEARINGHOUSE,
			Amount:          protoAmount,
			ReferenceId:     payout.ID,
			Description:     fmt.Sprintf("payout via %s", rail),
		})
	}); err != nil {
		s.compensate(ctx, payout, "post ledger entry: "+err.Error())
		return nil, wrapStep("post ledger entry", err)
	}
	if err := s.repo.MarkStepDone(ctx, payout.ID, "ledger_posted"); err != nil {
		s.log.Error().Err(err).Str("payout_id", payout.ID).Msg("record ledger_posted failed")
	}
	payout.LedgerPosted = true

	// ── Step 3: write the settlement instruction ──────────────────────────────
	// This is not optional. A payout the bank never receives, whose customer has
	// already been debited, is worse than a payout that fails cleanly.
	batchID := uuid.New().String()
	fileKey, err := runStep(ctx, s, payout.ID, "UploadBankFile", 3, func(ctx context.Context) (string, error) {
		return s.uploadBankFile(ctx, payout, batchID, req.Destination)
	})
	if err != nil {
		s.log.Error().Err(err).Str("payout_id", payout.ID).
			Msg("settlement file upload failed — returning funds")
		s.compensate(ctx, payout, "bank file upload: "+err.Error())
		return nil, status.Error(codes.Unavailable,
			"could not write the settlement instruction; the payout was rolled back and may be retried")
	}

	// ── Step 4: terminal state + event ────────────────────────────────────────
	if err := s.repo.ProcessPayout(ctx, payout.ID, batchID, fileKey, map[string]any{
		"payout_id":  payout.ID,
		"account_id": req.AccountId,
		"amount":     amount.StringMinor(),
		"currency":   string(amount.Currency()),
		"rail":       rail,
		"batch_id":   batchID,
		"file_key":   fileKey,
		// The beneficiary details are deliberately omitted: this event fans out
		// to Kafka, the audit log, and analytics, none of which need bank
		// account numbers.
	}); err != nil {
		if errors.Is(err, db.ErrTerminal) {
			// Another path already resolved it; report current state.
			if current, getErr := s.repo.GetPayout(ctx, payout.ID); getErr == nil && current != nil {
				return &payoutv1.InitiatePayoutResponse{Payout: toProto(current)}, nil
			}
		}
		// Money moved and the file is written, but the status did not commit.
		// Leave it in-flight for the recovery worker rather than claiming success.
		s.log.Error().Err(err).Str("payout_id", payout.ID).
			Msg("payout applied but finalisation failed — recovery worker will resolve")
		return nil, status.Error(codes.Internal,
			"payout applied but could not be finalised — query the payout for its final state")
	}

	// The payout is committed at this point. A failed read-back is not a reason
	// to tell the caller the payout failed, so report the state we know and log
	// the read error rather than discarding it.
	final, readErr := s.repo.GetPayout(ctx, payout.ID)
	if readErr != nil || final == nil {
		if readErr != nil {
			s.log.Warn().Err(readErr).Str("payout_id", payout.ID).
				Msg("payout committed but could not be re-read; returning locally known state")
		}
		payout.Status = db.StatusProcessing
		payout.BatchID = batchID
		payout.SettlementFile = fileKey
		return &payoutv1.InitiatePayoutResponse{Payout: toProto(payout)}, nil
	}
	return &payoutv1.InitiatePayoutResponse{Payout: toProto(final)}, nil
}

func (s *PayoutServer) GetPayout(ctx context.Context, req *payoutv1.GetPayoutRequest) (*payoutv1.GetPayoutResponse, error) {
	if req.PayoutId == "" {
		return nil, status.Error(codes.InvalidArgument, "payout_id required")
	}
	p, err := s.repo.GetPayout(ctx, req.PayoutId)
	if err != nil {
		s.log.Error().Err(err).Str("payout_id", req.PayoutId).Msg("get payout failed")
		return nil, status.Error(codes.Internal, "get payout failed")
	}
	if p == nil {
		return nil, status.Errorf(codes.NotFound, "payout %s not found", req.PayoutId)
	}
	return &payoutv1.GetPayoutResponse{Payout: toProto(p)}, nil
}

// ── validation ────────────────────────────────────────────────────────────────

func (s *PayoutServer) validate(req *payoutv1.InitiatePayoutRequest) (money.Amount, string, error) {
	switch {
	case req.IdempotencyKey == "":
		return money.Amount{}, "", status.Error(codes.InvalidArgument, "idempotency_key required")
	case req.AccountId == "":
		return money.Amount{}, "", status.Error(codes.InvalidArgument, "account_id required")
	case req.Amount == nil:
		return money.Amount{}, "", status.Error(codes.InvalidArgument, "amount required")
	}

	amount, err := money.Parse(req.Amount.Amount, req.Amount.Currency)
	if err != nil {
		return money.Amount{}, "", status.Errorf(codes.InvalidArgument, "amount: %v", err)
	}

	rail, err := RailString(req.Rail)
	if err != nil {
		return money.Amount{}, "", status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if err := ValidateRailCurrency(rail, string(amount.Currency())); err != nil {
		return money.Amount{}, "", status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if err := ValidateDestination(rail, req.Destination); err != nil {
		return money.Amount{}, "", status.Errorf(codes.InvalidArgument, "%v", err)
	}

	return amount, rail, nil
}

// ── saga machinery ────────────────────────────────────────────────────────────

// runStep executes one forward saga step, timing it and recording the outcome.
// A package-level function because Go methods cannot take type parameters.
func runStep[T any](
	ctx context.Context,
	s *PayoutServer,
	payoutID, name string,
	seq int,
	fn func(context.Context) (T, error),
) (T, error) {
	start := time.Now()
	result, err := fn(ctx)

	if s.metrics != nil {
		outcome := "success"
		if err != nil {
			outcome = "failure"
		}
		s.metrics.StepsTotal.WithLabelValues(sagaName, name, outcome).Inc()
		s.metrics.StepDuration.WithLabelValues(sagaName, name).Observe(time.Since(start).Seconds())
	}
	if err != nil {
		s.log.Warn().Err(err).Str("payout_id", payoutID).Str("step", name).
			Int("seq", seq).Msg("payout saga step failed")
	}
	return result, err
}

// compensate returns debited funds and reverses the ledger entry, then marks the
// payout FAILED.
//
// It runs on a context detached from the caller's: compensation is most often
// triggered by a client timeout, and using the cancelled context would make
// every compensating call fail immediately, stranding the customer's money.
func (s *PayoutServer) compensate(ctx context.Context, payout *db.Payout, reason string) {
	base := context.WithoutCancel(ctx)

	if err := s.repo.BeginCompensation(base, payout.ID, reason); err != nil {
		if errors.Is(err, db.ErrTerminal) {
			return // already resolved
		}
		s.log.Error().Err(err).Str("payout_id", payout.ID).Msg("could not mark payout compensating")
	}

	s.compensateFrom(base, payout, reason)
}

// compensateFrom performs the reversal. Shared with the recovery worker so
// inline and deferred compensation cannot diverge.
func (s *PayoutServer) compensateFrom(ctx context.Context, payout *db.Payout, reason string) {
	allOK := true

	// Reverse order: undo the ledger entry, then return the money.
	if payout.LedgerPosted {
		allOK = s.compensateStep(ctx, payout.ID, "ReverseLedgerEntry", func(ctx context.Context) error {
			_, err := s.ledger.ReverseEntry(ctx, &ledgerv1.ReverseEntryRequest{
				IdempotencyKey: "payout:reverse:" + payout.ID,
				ReferenceId:    payout.ID,
				Reason:         reason,
			})
			if status.Code(err) == codes.AlreadyExists {
				return nil // a previous attempt already reversed it
			}
			return err
		}) && allOK
	}

	if payout.AccountDebited {
		allOK = s.compensateStep(ctx, payout.ID, "RefundAccount", func(ctx context.Context) error {
			_, err := s.account.CreditAccount(ctx, &accountv1.CreditAccountRequest{
				IdempotencyKey: "payout:refund:" + payout.ID,
				AccountId:      payout.AccountID,
				Amount:         &commonv1.Money{Amount: payout.Amount, Currency: payout.Currency},
				ReferenceId:    "payout-refund:" + payout.ID,
			})
			if status.Code(err) == codes.AlreadyExists {
				return nil // already refunded
			}
			return err
		}) && allOK
	}

	if !allOK {
		// Leave it COMPENSATING so the recovery worker retries. Marking FAILED
		// here would assert a refund that did not happen.
		s.log.Error().Str("payout_id", payout.ID).
			Msg("payout compensation incomplete — left COMPENSATING for retry")
		return
	}

	s.fail(ctx, payout.ID, reason)
}

func (s *PayoutServer) compensateStep(
	ctx context.Context,
	payoutID, name string,
	fn func(context.Context) error,
) bool {
	stepCtx, cancel := context.WithTimeout(ctx, compensationTimeout)
	defer cancel()

	err := fn(stepCtx)
	outcome := "success"
	if err != nil {
		outcome = "failure"
		s.log.Error().Err(err).
			Str("payout_id", payoutID).
			Str("compensation", name).
			Msg("COMPENSATION FAILED — customer funds may be stranded, manual reconciliation required")
	} else {
		s.log.Info().Str("payout_id", payoutID).Str("compensation", name).Msg("compensated")
	}
	if s.metrics != nil {
		s.metrics.CompensationsTotal.WithLabelValues(sagaName, name, outcome).Inc()
	}
	return err == nil
}

// fail records the terminal FAILED state and emits payout.failed.
func (s *PayoutServer) fail(ctx context.Context, payoutID, reason string) {
	if err := s.repo.FailPayout(context.WithoutCancel(ctx), payoutID, reason); err != nil &&
		!errors.Is(err, db.ErrTerminal) {
		// Previously this error was discarded entirely, so a payout could stay
		// PENDING with no indication anything had gone wrong.
		s.log.Error().Err(err).Str("payout_id", payoutID).
			Msg("could not persist FAILED status")
	}
}

// RecoverStuck is called by the recovery worker for a payout abandoned mid-saga.
func (s *PayoutServer) RecoverStuck(ctx context.Context, payout *db.Payout) {
	s.log.Warn().
		Str("payout_id", payout.ID).
		Str("status", payout.Status).
		Bool("account_debited", payout.AccountDebited).
		Bool("ledger_posted", payout.LedgerPosted).
		Msg("recovering abandoned payout")

	if !payout.NeedsCompensation() {
		s.fail(ctx, payout.ID, "abandoned before any effect; failed by recovery worker")
		return
	}

	reason := fmt.Sprintf("abandoned mid-saga (last update %s); compensated by recovery worker",
		payout.UpdatedAt.UTC().Format(time.RFC3339))

	if err := s.repo.BeginCompensation(ctx, payout.ID, reason); err != nil && !errors.Is(err, db.ErrTerminal) {
		s.log.Error().Err(err).Str("payout_id", payout.ID).Msg("could not mark payout compensating")
	}
	s.compensateFrom(ctx, payout, reason)
}

// ── settlement file ───────────────────────────────────────────────────────────

// uploadBankFile writes the settlement instruction to object storage.
func (s *PayoutServer) uploadBankFile(
	ctx context.Context,
	p *db.Payout,
	batchID string,
	dest *payoutv1.BankAccount,
) (string, error) {
	type bankRecord struct {
		PayoutID      string `json:"payout_id"`
		AccountID     string `json:"account_id"`
		AccountNumber string `json:"account_number,omitempty"`
		IFSCCode      string `json:"ifsc_code,omitempty"`
		IBAN          string `json:"iban,omitempty"`
		BIC           string `json:"bic,omitempty"`
		AccountName   string `json:"account_name,omitempty"`
		Amount        string `json:"amount"`
		Currency      string `json:"currency"`
		Narration     string `json:"narration"`
	}
	type bankFile struct {
		BatchID     string       `json:"batch_id"`
		Rail        string       `json:"rail"`
		GeneratedAt string       `json:"generated_at"`
		SponsorBank string       `json:"sponsor_bank"`
		Records     []bankRecord `json:"records"`
	}

	rec := bankRecord{
		PayoutID:  p.ID,
		AccountID: p.AccountID,
		Amount:    p.Amount,
		Currency:  p.Currency,
		Narration: narration(p.ID),
	}
	if dest != nil {
		rec.AccountNumber = dest.AccountNumber
		rec.IFSCCode = dest.IfscCode
		rec.IBAN = dest.Iban
		rec.BIC = dest.Bic
		rec.AccountName = dest.AccountName
	}

	file := bankFile{
		BatchID:     batchID,
		Rail:        p.Rail,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		SponsorBank: sponsorBank(),
		Records:     []bankRecord{rec},
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode bank file: %w", err)
	}

	objectKey := fmt.Sprintf("batches/%s/%s.json", batchID, p.ID)
	key, err := s.storage.Upload(ctx, objectKey, data)
	if err != nil {
		return "", fmt.Errorf("upload bank file: %w", err)
	}
	return key, nil
}

// sponsorBank returns the sponsor bank identifier stamped into settlement files.
// Configurable because it differs per environment and per banking partner; the
// previous hardcoded constant would have gone out with every real file.
func sponsorBank() string {
	return sharedconfig.Env("PAYOUT_SPONSOR_BANK", "LEDGER001")
}

// narration builds the bank narration. It uses a length-safe prefix of the id:
// the previous code sliced p.ID[:8] unconditionally, which panics on any id
// shorter than eight characters and — with no panic recovery installed — would
// have taken the whole service down.
func narration(payoutID string) string {
	const prefixLen = 8
	ref := payoutID
	if len(ref) > prefixLen {
		ref = ref[:prefixLen]
	}
	return "PAYOUT-" + ref
}

// ── proto conversion ──────────────────────────────────────────────────────────

func toProto(p *db.Payout) *payoutv1.Payout {
	if p == nil {
		return nil
	}
	amountStr := p.Amount
	if amt, err := money.New(p.Amount, money.Currency(p.Currency)); err == nil {
		amountStr = amt.StringMinor()
	}
	out := &payoutv1.Payout{
		Id:        p.ID,
		Status:    statusProto(p.Status),
		Rail:      RailProto(p.Rail),
		AccountId: p.AccountID,
		Amount:    &commonv1.Money{Amount: amountStr, Currency: p.Currency},
		BatchId:   p.BatchID,
		Destination: &payoutv1.BankAccount{
			AccountNumber: p.Destination.AccountNumber,
			IfscCode:      p.Destination.IFSCCode,
			Iban:          p.Destination.IBAN,
			Bic:           p.Destination.BIC,
			AccountName:   p.Destination.AccountName,
		},
		CreatedAt: timestamppb.New(p.CreatedAt),
	}
	if p.SettledAt != nil {
		out.SettledAt = timestamppb.New(*p.SettledAt)
	}
	return out
}

func toDestDB(d *payoutv1.BankAccount) db.Destination {
	if d == nil {
		return db.Destination{}
	}
	return db.Destination{
		AccountNumber: d.AccountNumber,
		IFSCCode:      d.IfscCode,
		IBAN:          d.Iban,
		BIC:           d.Bic,
		AccountName:   d.AccountName,
	}
}

func statusProto(s string) payoutv1.PayoutStatus {
	switch s {
	case db.StatusPending, db.StatusCompensating:
		// COMPENSATING is still in flight from a caller's perspective.
		return payoutv1.PayoutStatus_PAYOUT_STATUS_PENDING
	case db.StatusProcessing:
		return payoutv1.PayoutStatus_PAYOUT_STATUS_PROCESSING
	case db.StatusSettled:
		return payoutv1.PayoutStatus_PAYOUT_STATUS_SETTLED
	case db.StatusFailed:
		return payoutv1.PayoutStatus_PAYOUT_STATUS_FAILED
	default:
		return payoutv1.PayoutStatus_PAYOUT_STATUS_UNSPECIFIED
	}
}

// wrapStep preserves the downstream status code.
func wrapStep(step string, err error) error {
	return status.Errorf(status.Code(err), "%s: %s", step, status.Convert(err).Message())
}
