package handler

import (
	"net/http"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
)

func (h *Handler) initiatePayment(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey  string `json:"idempotency_key"`
		SourceAccountID string `json:"source_account_id"`
		DestAccountID   string `json:"dest_account_id"`
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
		Description     string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	resp, err := h.clients.Txn.InitiatePayment(r.Context(), &txnv1.InitiatePaymentRequest{
		IdempotencyKey:  body.IdempotencyKey,
		SourceAccountId: body.SourceAccountID,
		DestAccountId:   body.DestAccountID,
		Amount:          &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		Description:     body.Description,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Info().
		Str("actor", id.Subject).
		Str("source_account_id", body.SourceAccountID).
		Str("dest_account_id", body.DestAccountID).
		Str("amount", body.Amount).
		Str("currency", body.Currency).
		Msg("payment initiated")
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request, _ Identity) {
	resp, err := h.clients.Txn.GetTransaction(r.Context(), &txnv1.GetTransactionRequest{
		TransactionId: r.PathValue("id"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// getTransactionTrace returns a transaction with its saga steps.
//
// This is now a gRPC call. It previously issued a bare http.Get against
// transaction-svc's *metrics* port, concatenating the path parameter into the URL
// unescaped, with no timeout and without propagating the request context — so a
// hung admin endpoint leaked connections indefinitely, and the trace data itself
// was served from an unauthenticated port.
func (h *Handler) getTransactionTrace(w http.ResponseWriter, r *http.Request, _ Identity) {
	resp, err := h.clients.Txn.GetTransactionTrace(r.Context(), &txnv1.GetTransactionTraceRequest{
		TransactionId: r.PathValue("id"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}
