package handler

import (
	"net/http"
	"strconv"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	ledgerv1 "github.com/prasham/ledger/gen/ledger/v1"
)

func (h *Handler) getBalance(w http.ResponseWriter, r *http.Request, _ Identity) {
	resp, err := h.clients.Ledger.GetBalance(r.Context(), &ledgerv1.GetBalanceRequest{
		AccountId: r.PathValue("account_id"),
		Currency:  r.URL.Query().Get("currency"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request, _ Identity) {
	// maxPageSize bounds the request before it is narrowed to int32. Parsing
	// straight into int32 would wrap for large inputs, turning a huge page_size
	// into a small or negative one.
	const maxPageSize = 1000

	pageSize := int32(20)
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		n, err := strconv.ParseInt(ps, 10, 32)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "page_size must be a non-negative integer")
			return
		}
		if n > maxPageSize {
			n = maxPageSize
		}
		pageSize = int32(n)
	}

	resp, err := h.clients.Ledger.ListEntries(r.Context(), &ledgerv1.ListEntriesRequest{
		AccountId: r.PathValue("account_id"),
		PageSize:  pageSize,
		PageToken: r.URL.Query().Get("page_token"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// postEntry writes a raw double-entry pair, bypassing every saga.
//
// It is admin-only and always audited: an arbitrary ledger entry can assert a
// transfer that no account balance reflects, which is exactly the discrepancy the
// reconciliation worker exists to detect.
func (h *Handler) postEntry(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey  string `json:"idempotency_key"`
		DebitAccountID  string `json:"debit_account_id"`
		CreditAccountID string `json:"credit_account_id"`
		Amount          string `json:"amount"`
		Currency        string `json:"currency"`
		ReferenceID     string `json:"reference_id"`
		Description     string `json:"description"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	resp, err := h.clients.Ledger.PostEntry(r.Context(), &ledgerv1.PostEntryRequest{
		IdempotencyKey:  body.IdempotencyKey,
		DebitAccountId:  body.DebitAccountID,
		CreditAccountId: body.CreditAccountID,
		Amount:          &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		ReferenceId:     body.ReferenceID,
		Description:     body.Description,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Warn().
		Str("actor", id.Subject).
		Str("debit_account_id", body.DebitAccountID).
		Str("credit_account_id", body.CreditAccountID).
		Str("amount", body.Amount).
		Str("currency", body.Currency).
		Str("reference_id", body.ReferenceID).
		Msg("raw ledger entry posted (outside any saga)")
	writeProto(w, http.StatusCreated, resp)
}
