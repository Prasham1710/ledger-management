package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/payout-svc/internal/db"
	"github.com/prasham/ledger/services/payout-svc/internal/storage"
)

// CLEARINGHOUSE is the virtual ledger account that receives the debit credit
// when funds leave the platform. It represents the bank settlement side.
const CLEARINGHOUSE = "PAYOUT_CLEARINGHOUSE"

type PayoutServer struct {
	payoutv1.UnimplementedPayoutServiceServer
	repo    *db.Repo
	idem    idempotency.Store
	ledger  ledgerv1.LedgerServiceClient
	account accountv1.AccountServiceClient
	minio   *storage.MinIO
	log     zerolog.Logger
}

func NewPayoutServer(
	repo *db.Repo,
	idem idempotency.Store,
	ledger ledgerv1.LedgerServiceClient,
	account accountv1.AccountServiceClient,
	minio *storage.MinIO,
	log zerolog.Logger,
) *PayoutServer {
	return &PayoutServer{repo: repo, idem: idem, ledger: ledger, account: account, minio: minio, log: log}
}

// InitiatePayout runs the payout saga:
//  1. Debit account (direct debit — payout is the authorization)
//  2. PostEntry to ledger (debit account, credit clearinghouse)
//  3. Generate bank file → upload to MinIO
//  4. Atomically mark PROCESSING + write outbox event
func (s *PayoutServer) InitiatePayout(ctx context.Context, req *payoutv1.InitiatePayoutRequest) (*payoutv1.InitiatePayoutResponse, error) {
	if req.IdempotencyKey == "" {
		return nil, status.Error(codes.InvalidArgument, "idempotency_key required")
	}
	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id required")
	}
	if req.Amount == nil || req.Amount.Amount == "" {
		return nil, status.Error(codes.InvalidArgument, "amount required")
	}
	if req.Destination == nil {
		return nil, status.Error(codes.InvalidArgument, "destination required")
	}
	amt, err := decimal.NewFromString(req.Amount.Amount)
	if err != nil || amt.IsNegative() || amt.IsZero() {
		return nil, status.Error(codes.InvalidArgument, "invalid amount")
	}

	idemKey := "payout:initiate:" + req.IdempotencyKey

	if existing, _ := s.idem.Get(ctx, idemKey); existing != nil {
		p, err := s.repo.GetByIdempotencyKey(ctx, req.IdempotencyKey)
		if err != nil || p == nil {
			return nil, status.Error(codes.AlreadyExists, "duplicate idempotency_key")
		}
		return &payoutv1.InitiatePayoutResponse{Payout: toProto(p)}, nil
	}

	// Step 1: persist payout row in PENDING state.
	payout, err := s.repo.CreatePayout(ctx, &db.Payout{
		AccountID:      req.AccountId,
		Amount:         req.Amount.Amount,
		Currency:       req.Amount.Currency,
		Rail:           railString(req.Rail),
		Destination:    toDestDB(req.Destination),
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create payout: %v", err)
	}

	// Step 2: debit the source account (funds leave the platform).
	_, err = s.account.DebitAccount(ctx, &accountv1.DebitAccountRequest{
		IdempotencyKey: "payout:debit:" + payout.ID,
		AccountId:      req.AccountId,
		Amount:         req.Amount,
		ReferenceId:    payout.ID,
	})
	if err != nil {
		s.repo.FailPayout(ctx, payout.ID, err.Error())
		return nil, status.Errorf(grpcCode(err), "debit account: %v", err)
	}

	// Step 3: ledger — debit account, credit clearinghouse (money out of system).
	_, err = s.ledger.PostEntry(ctx, &ledgerv1.PostEntryRequest{
		IdempotencyKey:  "payout:ledger:" + payout.ID,
		DebitAccountId:  req.AccountId,
		CreditAccountId: CLEARINGHOUSE,
		Amount:          req.Amount,
		ReferenceId:     payout.ID,
		Description:     fmt.Sprintf("payout via %s", railString(req.Rail)),
	})
	if err != nil {
		// Account already debited — this is partial; log as critical.
		s.log.Error().Err(err).Str("payout_id", payout.ID).Msg("ledger entry failed after debit — manual reconciliation required")
		s.repo.FailPayout(ctx, payout.ID, err.Error())
		return nil, status.Errorf(grpcCode(err), "post ledger entry: %v", err)
	}

	// Step 4: generate bank file and upload to MinIO.
	batchID := uuid.New().String()
	fileKey, err := s.uploadBankFile(ctx, payout, batchID, req)
	if err != nil {
		s.log.Warn().Err(err).Str("payout_id", payout.ID).Msg("bank file upload failed — payout proceeds to PROCESSING anyway")
		fileKey = "" // non-fatal: bank ops team can regenerate
	}

	// Step 5: atomically mark PROCESSING + outbox.
	if err := s.repo.ProcessPayout(ctx, payout.ID, batchID, fileKey, map[string]any{
		"payout_id":  payout.ID,
		"account_id": req.AccountId,
		"amount":     req.Amount.Amount,
		"currency":   req.Amount.Currency,
		"rail":       railString(req.Rail),
		"batch_id":   batchID,
		"file_key":   fileKey,
	}); err != nil {
		s.log.Error().Err(err).Str("payout_id", payout.ID).Msg("finalize payout failed")
	}

	_ = s.idem.Set(ctx, idemKey, idempotency.Entry{StatusCode: 200, CreatedAt: time.Now()})

	payout, _ = s.repo.GetPayout(ctx, payout.ID)
	return &payoutv1.InitiatePayoutResponse{Payout: toProto(payout)}, nil
}

func (s *PayoutServer) GetPayout(ctx context.Context, req *payoutv1.GetPayoutRequest) (*payoutv1.GetPayoutResponse, error) {
	if req.PayoutId == "" {
		return nil, status.Error(codes.InvalidArgument, "payout_id required")
	}
	p, err := s.repo.GetPayout(ctx, req.PayoutId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get payout: %v", err)
	}
	if p == nil {
		return nil, status.Errorf(codes.NotFound, "payout %s not found", req.PayoutId)
	}
	return &payoutv1.GetPayoutResponse{Payout: toProto(p)}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *PayoutServer) uploadBankFile(ctx context.Context, p *db.Payout, batchID string, req *payoutv1.InitiatePayoutRequest) (string, error) {
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
		Narration: "PAYOUT-" + p.ID[:8],
	}
	if req.Destination != nil {
		rec.AccountNumber = req.Destination.AccountNumber
		rec.IFSCCode = req.Destination.IfscCode
		rec.IBAN = req.Destination.Iban
		rec.BIC = req.Destination.Bic
		rec.AccountName = req.Destination.AccountName
	}

	file := bankFile{
		BatchID:     batchID,
		Rail:        p.Rail,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		SponsorBank: "LEDGER001",
		Records:     []bankRecord{rec},
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return "", err
	}
	objectKey := fmt.Sprintf("batches/%s/%s.json", batchID, p.ID)
	return s.minio.Upload(ctx, objectKey, data)
}

func toProto(p *db.Payout) *payoutv1.Payout {
	if p == nil {
		return nil
	}
	proto := &payoutv1.Payout{
		Id:        p.ID,
		Status:    statusProto(p.Status),
		Rail:      railProto(p.Rail),
		AccountId: p.AccountID,
		Amount:    &commonv1.Money{Amount: p.Amount, Currency: p.Currency},
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
		proto.SettledAt = timestamppb.New(*p.SettledAt)
	}
	return proto
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

func railString(r payoutv1.PayoutRail) string {
	switch r {
	case payoutv1.PayoutRail_PAYOUT_RAIL_NACH:
		return "NACH"
	case payoutv1.PayoutRail_PAYOUT_RAIL_SEPA:
		return "SEPA"
	case payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT:
		return "SWIFT"
	default:
		return "NACH"
	}
}

func railProto(s string) payoutv1.PayoutRail {
	switch s {
	case "SEPA":
		return payoutv1.PayoutRail_PAYOUT_RAIL_SEPA
	case "SWIFT":
		return payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT
	default:
		return payoutv1.PayoutRail_PAYOUT_RAIL_NACH
	}
}

func statusProto(s string) payoutv1.PayoutStatus {
	switch s {
	case "PENDING":
		return payoutv1.PayoutStatus_PAYOUT_STATUS_PENDING
	case "PROCESSING":
		return payoutv1.PayoutStatus_PAYOUT_STATUS_PROCESSING
	case "SETTLED":
		return payoutv1.PayoutStatus_PAYOUT_STATUS_SETTLED
	case "FAILED":
		return payoutv1.PayoutStatus_PAYOUT_STATUS_FAILED
	default:
		return payoutv1.PayoutStatus_PAYOUT_STATUS_UNSPECIFIED
	}
}

func grpcCode(err error) codes.Code {
	if st, ok := status.FromError(err); ok {
		return st.Code()
	}
	return codes.Internal
}
