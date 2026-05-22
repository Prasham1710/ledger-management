package handler

import (
	"encoding/json"
	"net/http"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)


var pj = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}

type Handler struct {
	mux         *http.ServeMux
	ledger      ledgerv1.LedgerServiceClient
	account     accountv1.AccountServiceClient
	fx          fxv1.FXServiceClient
	txn         txnv1.TransactionServiceClient
	payout      payoutv1.PayoutServiceClient
	static      string
	txnAdminURL string
}

func New(ledger ledgerv1.LedgerServiceClient, account accountv1.AccountServiceClient, fx fxv1.FXServiceClient, txn txnv1.TransactionServiceClient, payout payoutv1.PayoutServiceClient, staticDir, txnAdminURL string) *Handler {
	h := &Handler{mux: http.NewServeMux(), ledger: ledger, account: account, fx: fx, txn: txn, payout: payout, static: staticDir, txnAdminURL: txnAdminURL}
	h.routes()
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) routes() {
	h.mux.HandleFunc("GET /api/health", h.health)

	h.mux.HandleFunc("GET /api/accounts", h.listAccounts)
	h.mux.HandleFunc("POST /api/accounts", h.createAccount)
	h.mux.HandleFunc("GET /api/accounts/{id}", h.getAccount)
	h.mux.HandleFunc("POST /api/accounts/{id}/credit", h.creditAccount)
	h.mux.HandleFunc("POST /api/accounts/{id}/debit", h.debitAccount)
	h.mux.HandleFunc("POST /api/accounts/{id}/holds", h.placeHold)
	h.mux.HandleFunc("DELETE /api/accounts/{id}/holds/{hold_id}", h.releaseHold)

	h.mux.HandleFunc("GET /api/ledger/{account_id}/balance", h.getBalance)
	h.mux.HandleFunc("GET /api/ledger/{account_id}/entries", h.listEntries)
	h.mux.HandleFunc("POST /api/ledger/entries", h.postEntry)

	h.mux.HandleFunc("POST /api/fx/quote", h.getQuote)
	h.mux.HandleFunc("POST /api/fx/quote/{id}/lock", h.lockQuote)

	h.mux.HandleFunc("POST /api/transactions", h.initiatePayment)
	h.mux.HandleFunc("GET /api/transactions/{id}", h.getTransaction)
	h.mux.HandleFunc("GET /api/transactions/{id}/trace", h.getTransactionTrace)

	h.mux.HandleFunc("POST /api/payouts", h.initiatePayout)
	h.mux.HandleFunc("GET /api/payouts/{id}", h.getPayout)

	fs := http.FileServer(http.Dir(h.static))
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Non-GET requests to unknown paths are API misses — return JSON, not HTML.
		// This prevents silent HTML responses when clients hit ui-bff directly instead
		// of going through the gateway (e.g. POST /auth/login on port 8090).
		if r.Method != http.MethodGet {
			writeError(w, http.StatusNotFound, "not found — use the gateway on :8080")
			return
		}
		http.ServeFile(w, r, h.static+"/index.html")
	})
	h.mux.Handle("/assets/", fs)
}

func writeProto(w http.ResponseWriter, code int, m proto.Message) {
	b, _ := pj.Marshal(m)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func grpcStatus(err error) int {
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.NotFound:
			return http.StatusNotFound
		case codes.InvalidArgument:
			return http.StatusBadRequest
		case codes.AlreadyExists:
			return http.StatusConflict
		case codes.FailedPrecondition:
			return http.StatusUnprocessableEntity
		case codes.Unauthenticated:
			return http.StatusUnauthorized
		case codes.PermissionDenied:
			return http.StatusForbidden
		}
	}
	return http.StatusInternalServerError
}
