package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"github.com/prasham/ledger/services/ui-bff/internal/handler"
)

func main() {
	ledgerAddr  := env("LEDGER_GRPC_ADDR",      "localhost:50051")
	accountAddr := env("ACCOUNT_GRPC_ADDR",     "localhost:50052")
	fxAddr      := env("FX_GRPC_ADDR",          "localhost:50053")
	txnAddr     := env("TRANSACTION_GRPC_ADDR", "localhost:50054")
	payoutAddr  := env("PAYOUT_GRPC_ADDR",      "localhost:50055")
	listenAddr  := env("UI_BFF_ADDR",            ":8090")
	staticDir   := env("UI_STATIC_DIR",          "./ui/dist")
	txnAdminURL := env("TXN_ADMIN_URL",           "http://localhost:9104")

	ledgerConn, err := grpc.NewClient(ledgerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial ledger-svc: %v", err)
	}
	defer ledgerConn.Close()

	accountConn, err := grpc.NewClient(accountAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial account-svc: %v", err)
	}
	defer accountConn.Close()

	fxConn, err := grpc.NewClient(fxAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial fx-svc: %v", err)
	}
	defer fxConn.Close()

	txnConn, err := grpc.NewClient(txnAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial transaction-svc: %v", err)
	}
	defer txnConn.Close()

	payoutConn, err := grpc.NewClient(payoutAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("dial payout-svc: %v", err)
	}
	defer payoutConn.Close()

	h := handler.New(
		ledgerv1.NewLedgerServiceClient(ledgerConn),
		accountv1.NewAccountServiceClient(accountConn),
		fxv1.NewFXServiceClient(fxConn),
		txnv1.NewTransactionServiceClient(txnConn),
		payoutv1.NewPayoutServiceClient(payoutConn),
		staticDir,
		txnAdminURL,
	)

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      h,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	log.Printf("ui-bff listening on %s  static=%s", listenAddr, staticDir)
	log.Fatal(srv.ListenAndServe())
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
