package service_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	"github.com/prasham/ledger/internal/idempotency"
	"github.com/prasham/ledger/services/payout-svc/internal/service"
)

type noopIdem struct{}

func (noopIdem) Get(_ context.Context, _ string) (*idempotency.Entry, error) { return nil, nil }
func (noopIdem) Set(_ context.Context, _ string, _ idempotency.Entry) error  { return nil }

func newServer() *service.PayoutServer {
	return service.NewPayoutServer(nil, noopIdem{}, nil, nil, nil, zerolog.Nop())
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

func TestInitiatePayout_MissingIdempotencyKey(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayout(context.Background(), &payoutv1.InitiatePayoutRequest{
		AccountId:   "acc-1",
		Amount:      &commonv1.Money{Amount: "500", Currency: "INR"},
		Destination: &payoutv1.BankAccount{AccountNumber: "123456"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayout_MissingAccountID(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayout(context.Background(), &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: "pkey-1",
		Amount:         &commonv1.Money{Amount: "500", Currency: "INR"},
		Destination:    &payoutv1.BankAccount{AccountNumber: "123456"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayout_MissingAmount(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayout(context.Background(), &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: "pkey-2",
		AccountId:      "acc-1",
		Destination:    &payoutv1.BankAccount{AccountNumber: "123456"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayout_MissingDestination(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayout(context.Background(), &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: "pkey-3",
		AccountId:      "acc-1",
		Amount:         &commonv1.Money{Amount: "500", Currency: "INR"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestInitiatePayout_NegativeAmount(t *testing.T) {
	s := newServer()
	_, err := s.InitiatePayout(context.Background(), &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: "pkey-4",
		AccountId:      "acc-1",
		Amount:         &commonv1.Money{Amount: "-100", Currency: "INR"},
		Destination:    &payoutv1.BankAccount{AccountNumber: "123456"},
	})
	assertCode(t, err, codes.InvalidArgument)
}

func TestGetPayout_MissingID(t *testing.T) {
	s := newServer()
	_, err := s.GetPayout(context.Background(), &payoutv1.GetPayoutRequest{})
	assertCode(t, err, codes.InvalidArgument)
}
