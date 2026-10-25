package handler

import (
	"net/http"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	fxv1 "github.com/prasham/ledger/gen/fx/v1"
)

func (h *Handler) getQuote(w http.ResponseWriter, r *http.Request, _ Identity) {
	var body struct {
		FromCurrency string `json:"from_currency"`
		ToCurrency   string `json:"to_currency"`
		Amount       string `json:"amount"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	resp, err := h.clients.FX.GetQuote(r.Context(), &fxv1.GetQuoteRequest{
		FromCurrency: body.FromCurrency,
		ToCurrency:   body.ToCurrency,
		SourceAmount: &commonv1.Money{Amount: body.Amount, Currency: body.FromCurrency},
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func (h *Handler) lockQuote(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	quoteID := r.PathValue("id")
	resp, err := h.clients.FX.LockQuote(r.Context(), &fxv1.LockQuoteRequest{
		IdempotencyKey: body.IdempotencyKey,
		QuoteId:        quoteID,
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	h.log.Info().Str("actor", id.Subject).Str("quote_id", quoteID).Msg("fx quote locked")
	writeProto(w, http.StatusOK, resp)
}
