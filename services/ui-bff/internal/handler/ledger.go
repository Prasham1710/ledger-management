package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
)

func (h *Handler) getBalance(w http.ResponseWriter, r *http.Request) {
	resp, err := h.ledger.GetBalance(r.Context(), &ledgerv1.GetBalanceRequest{
		AccountId: r.PathValue("account_id"),
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	pageSize := int32(20)
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if n, err := strconv.Atoi(ps); err == nil {
			pageSize = int32(n)
		}
	}
	resp, err := h.ledger.ListEntries(r.Context(), &ledgerv1.ListEntriesRequest{
		AccountId: r.PathValue("account_id"),
		PageSize:  pageSize,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) postEntry(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey  string `json:"idempotency_key"`
		DebitAccountID  string `json:"debit_account_id"`
		CreditAccountID string `json:"credit_account_id"`
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
		ReferenceID     string `json:"reference_id"`
		Description     string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.ledger.PostEntry(r.Context(), &ledgerv1.PostEntryRequest{
		IdempotencyKey:  body.IdempotencyKey,
		DebitAccountId:  body.DebitAccountID,
		CreditAccountId: body.CreditAccountID,
		Amount:          &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		ReferenceId:     body.ReferenceID,
		Description:     body.Description,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusCreated, resp)
}
