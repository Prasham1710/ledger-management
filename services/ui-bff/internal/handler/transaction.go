package handler

import (
	"encoding/json"
	"io"
	"net/http"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	txnv1 "github.com/prasham/ledger/gen/transaction/v1"
)

func (h *Handler) initiatePayment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey  string `json:"idempotency_key"`
		SourceAccountID string `json:"source_account_id"`
		DestAccountID   string `json:"dest_account_id"`
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
		Description     string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.txn.InitiatePayment(r.Context(), &txnv1.InitiatePaymentRequest{
		IdempotencyKey:  body.IdempotencyKey,
		SourceAccountId: body.SourceAccountID,
		DestAccountId:   body.DestAccountID,
		Amount:          &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		Description:     body.Description,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	resp, err := h.txn.GetTransaction(r.Context(), &txnv1.GetTransactionRequest{
		TransactionId: r.PathValue("id"),
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) getTransactionTrace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	upstream, err := http.Get(h.txnAdminURL + "/trace/" + id)
	if err != nil {
		writeError(w, http.StatusBadGateway, "trace unavailable: "+err.Error())
		return
	}
	defer upstream.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(upstream.StatusCode)
	io.Copy(w, upstream.Body) //nolint:errcheck
}
