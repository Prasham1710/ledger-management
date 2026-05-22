package service_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/transaction-svc/internal/service"
)

// noopIdem satisfies idempotency.Store — always returns cache miss.
type noopIdem struct{}

func (noopIdem) Get(_ context.Context, _ string) (*idempotency.Entry, error) { return nil, nil }
func (noopIdem) Set(_ context.Context, _ string, _ idempotency.Entry) error  { return nil }

func newServer() *service.TransactionServer {
	return service.NewTransactionServer(nil, noopIdem{}, nil, nil, zerolog.Nop())
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := status.Code(err); got != want {
		t.Errorf("code: got %v, want %v (err: %v)", got, want, err)
	}
}

func TestInitiatePayment_MissingIdempotencyKey(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayment(context.Background(), &txnv1.InitiatePaymentRequest{
		SourceAccountId: "acc-1",
		DestAccountId:   "acc-2",
		Amount:          &commonv1.Money{Amount: "100", Currency: "INR"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayment_MissingSourceAccount(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayment(context.Background(), &txnv1.InitiatePaymentRequest{
		IdempotencyKey: "key-1",
		DestAccountId:  "acc-2",
		Amount:         &commonv1.Money{Amount: "100", Currency: "INR"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayment_MissingAmount(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayment(context.Background(), &txnv1.InitiatePaymentRequest{
		IdempotencyKey:  "key-2",
		SourceAccountId: "acc-1",
		DestAccountId:   "acc-2",
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayment_SameSourceAndDest(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayment(context.Background(), &txnv1.InitiatePaymentRequest{
		IdempotencyKey:  "key-3",
		SourceAccountId: "acc-same",
		DestAccountId:   "acc-same",
		Amount:          &commonv1.Money{Amount: "100", Currency: "INR"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayment_EmptyCurrency(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayment(context.Background(), &txnv1.InitiatePaymentRequest{
		IdempotencyKey:  "key-4",
		SourceAccountId: "acc-1",
		DestAccountId:   "acc-2",
		Amount:          &commonv1.Money{Amount: "100", Currency: ""},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestGetTransaction_MissingID(t *testing.T) {
	s := newServer()
	_, err := s.GetTransaction(context.Background(), &txnv1.GetTransactionRequest{})
	assertCode(t, err, codes.InvalidArgument)
}
