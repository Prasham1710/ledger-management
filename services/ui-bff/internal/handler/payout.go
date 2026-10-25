package handler

import (
	"net/http"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
)

func (h *Handler) initiatePayout(w http.ResponseWriter, r *http.Request, id Identity) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		AccountID      string `json:"account_id"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		Rail           string `json:"rail"`
		// Beneficiary details.
		AccountNumber string `json:"account_number"`
		IFSCCode      string `json:"ifsc_code"`
		IBAN          string `json:"iban"`
		BIC           string `json:"bic"`
		AccountName   string `json:"account_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	rail, ok := railProto(body.Rail)
	if !ok {
		// Reject an unrecognised rail here rather than defaulting to NACH, which
		// would route a foreign-currency payout through Indian domestic clearing.
		writeError(w, http.StatusBadRequest, "rail must be one of NACH, SEPA, SWIFT")
		return
	}

	resp, err := h.clients.Payout.InitiatePayout(r.Context(), &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      body.AccountID,
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		Rail:           rail,
		Destination: &payoutv1.BankAccount{
			AccountNumber: body.AccountNumber,
			IfscCode:      body.IFSCCode,
			Iban:          body.IBAN,
			Bic:           body.BIC,
			AccountName:   body.AccountName,
		},
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}

	// Beneficiary bank details are deliberately absent from this log line: it is
	// personal financial data and the payout id is enough to correlate.
	h.log.Info().
		Str("actor", id.Subject).
		Str("account_id", body.AccountID).
		Str("amount", body.Amount).
		Str("currency", body.Currency).
		Str("rail", body.Rail).
		Msg("payout initiated")
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) getPayout(w http.ResponseWriter, r *http.Request, _ Identity) {
	resp, err := h.clients.Payout.GetPayout(r.Context(), &payoutv1.GetPayoutRequest{
		PayoutId: r.PathValue("id"),
	})
	if err != nil {
		h.writeGRPCError(w, r, err)
		return
	}
	writeProto(w, http.StatusOK, resp)
}

// railProto maps the request's rail string to its enum, reporting whether it was
// recognised. It does not default: an unspecified rail is a client error.
func railProto(s string) (payoutv1.PayoutRail, bool) {
	switch s {
	case "NACH":
		return payoutv1.PayoutRail_PAYOUT_RAIL_NACH, true
	case "SEPA":
		return payoutv1.PayoutRail_PAYOUT_RAIL_SEPA, true
	case "SWIFT":
		return payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT, true
	default:
		return payoutv1.PayoutRail_PAYOUT_RAIL_UNSPECIFIED, false
	}
}
