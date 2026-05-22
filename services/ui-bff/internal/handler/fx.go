package handler

import (
	"encoding/json"
	"net/http"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
)

func (h *Handler) getQuote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FromCurrency string `json:"from_currency"`
		ToCurrency   string `json:"to_currency"`
		Amount       string `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.fx.GetQuote(r.Context(), &fxv1.GetQuoteRequest{
		FromCurrency: body.FromCurrency,
		ToCurrency:   body.ToCurrency,
		SourceAmount: &commonv1.Money{Amount: body.Amount, Currency: body.FromCurrency},
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) lockQuote(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.fx.LockQuote(r.Context(), &fxv1.LockQuoteRequest{
		IdempotencyKey: body.IdempotencyKey,
		QuoteId:        r.PathValue("id"),
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}
