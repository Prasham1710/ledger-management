package handler

import (
	"encoding/json"
	"net/http"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
)

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request) {
	ownerID := r.URL.Query().Get("owner_id")
	if ownerID == "" {
		writeError(w, http.StatusBadRequest, "owner_id required")
		return
	}
	resp, err := h.account.ListAccounts(r.Context(), &accountv1.ListAccountsRequest{
		OwnerId:  ownerID,
		PageSize: 50,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		OwnerID        string `json:"owner_id"`
		Currency       string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.account.CreateAccount(r.Context(), &accountv1.CreateAccountRequest{
		IdempotencyKey: body.IdempotencyKey,
		OwnerId:        body.OwnerID,
		Currency:       body.Currency,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request) {
	resp, err := h.account.GetAccount(r.Context(), &accountv1.GetAccountRequest{
		AccountId: r.PathValue("id"),
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) creditAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		ReferenceID    string `json:"reference_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.account.CreditAccount(r.Context(), &accountv1.CreditAccountRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      r.PathValue("id"),
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		ReferenceId:    body.ReferenceID,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) debitAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		HoldID         string `json:"hold_id"`
		ReferenceID    string `json:"reference_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.account.DebitAccount(r.Context(), &accountv1.DebitAccountRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      r.PathValue("id"),
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		HoldId:         body.HoldID,
		ReferenceId:    body.ReferenceID,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) placeHold(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		ReferenceID    string `json:"reference_id"`
		TTLSeconds     int64  `json:"ttl_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ttl := body.TTLSeconds
	if ttl <= 0 {
		ttl = 172800
	}
	resp, err := h.account.PlaceHold(r.Context(), &accountv1.PlaceHoldRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      r.PathValue("id"),
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		ReferenceId:    body.ReferenceID,
		TtlSeconds:     ttl,
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) releaseHold(w http.ResponseWriter, r *http.Request) {
	_, err := h.account.ReleaseHold(r.Context(), &accountv1.ReleaseHoldRequest{
		AccountId: r.PathValue("id"),
		HoldId:    r.PathValue("hold_id"),
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
