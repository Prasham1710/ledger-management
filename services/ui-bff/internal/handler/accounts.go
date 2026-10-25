package handler

import (
	"net/http"

	accountv1 "github.com/prasham/ledger/gen/account/v1"
	commonv1 "github.com/prasham/ledger/gen/common/v1"
)

func (h *Handler) listAccounts(w http.ResponseWriter, r *http.Request, _ Identity) {
	ownerID := r.URL.Query().Get("owner_id")
	if ownerID == "" {
		writeError(w, http.StatusBadRequest, "owner_id required")
		return
	}

	resp, err := h.clients.Account.ListAccounts(r.Context(), &accountv1.ListAccountsRequest{
		OwnerId:   ownerID,
		PageSize:  50,
		PageToken: r.URL.Query().Get("page_token"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) createAccount(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		OwnerID        string `json:"owner_id"`
		Currency       string `json:"currency"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	resp, err := h.clients.Account.CreateAccount(r.Context(), &accountv1.CreateAccountRequest{
		IdempotencyKey: body.IdempotencyKey,
		OwnerId:        body.OwnerID,
		Currency:       body.Currency,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Info().
		Str("actor", id.Subject).
		Str("owner_id", body.OwnerID).
		Str("currency", body.Currency).
		Msg("account created")
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) getAccount(w http.ResponseWriter, r *http.Request, _ Identity) {
	resp, err := h.clients.Account.GetAccount(r.Context(), &accountv1.GetAccountRequest{
		AccountId: r.PathValue("id"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// creditAccount adds funds outside the payment saga.
//
// This creates money, so it is admin-only and every call is logged with the
// acting subject. The route previously required no authentication at all.
func (h *Handler) creditAccount(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		ReferenceID    string `json:"reference_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	accountID := r.PathValue("id")
	resp, err := h.clients.Account.CreditAccount(r.Context(), &accountv1.CreditAccountRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      accountID,
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		ReferenceId:    body.ReferenceID,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	// Direct balance adjustments are the most privileged operation available, so
	// they always leave an audit line naming who performed them.
	h.log.Warn().
		Str("actor", id.Subject).
		Str("account_id", accountID).
		Str("amount", body.Amount).
		Str("currency", body.Currency).
		Str("reference_id", body.ReferenceID).
		Msg("account credited directly (outside the payment saga)")
	writeProto(w, http.StatusOK, resp)
}

// debitAccount removes funds outside the payment saga. Admin-only, and logged.
func (h *Handler) debitAccount(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		HoldID         string `json:"hold_id"`
		ReferenceID    string `json:"reference_id"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	accountID := r.PathValue("id")
	resp, err := h.clients.Account.DebitAccount(r.Context(), &accountv1.DebitAccountRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      accountID,
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		HoldId:         body.HoldID,
		ReferenceId:    body.ReferenceID,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Warn().
		Str("actor", id.Subject).
		Str("account_id", accountID).
		Str("amount", body.Amount).
		Str("currency", body.Currency).
		Str("reference_id", body.ReferenceID).
		Msg("account debited directly (outside the payment saga)")
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) placeHold(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		ReferenceID    string `json:"reference_id"`
		TTLSeconds     int64  `json:"ttl_seconds"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	accountID := r.PathValue("id")
	// TTL bounds are enforced by account-svc, which is the authority; passing the
	// value through unchanged keeps a single place to change the policy.
	resp, err := h.clients.Account.PlaceHold(r.Context(), &accountv1.PlaceHoldRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      accountID,
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		ReferenceId:    body.ReferenceID,
		TtlSeconds:     body.TTLSeconds,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Info().
		Str("actor", id.Subject).
		Str("account_id", accountID).
		Str("reference_id", body.ReferenceID).
		Msg("hold placed")
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) releaseHold(w http.ResponseWriter, r *http.Request, id Identity) {
	accountID := r.PathValue("id")
	holdID := r.PathValue("hold_id")

	if _, err := h.clients.Account.ReleaseHold(r.Context(), &accountv1.ReleaseHoldRequest{
		AccountId: accountID,
		HoldId:    holdID,
	}); err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Info().
		Str("actor", id.Subject).
		Str("account_id", accountID).
		Str("hold_id", holdID).
		Msg("hold released")
	w.WriteHeader(http.StatusNoContent)
}
