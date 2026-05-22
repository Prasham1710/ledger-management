package handler

import (
	"encoding/json"
	"net/http"

	commonv1 "github.com/prasham/ledger/gen/common/v1"
	payoutv1 "github.com/prasham/ledger/gen/payout/v1"
)

func (h *Handler) initiatePayout(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IdempotencyKey string `json:"idempotency_key"`
		AccountID      string `json:"account_id"`
		Amount         string `json:"amount"`
		Currency       string `json:"currency"`
		Rail           string `json:"rail"`
		// destination fields
		AccountNumber string `json:"account_number"`
		IFSCCode      string `json:"ifsc_code"`
		IBAN          string `json:"iban"`
		BIC           string `json:"bic"`
		AccountName   string `json:"account_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	resp, err := h.payout.InitiatePayout(r.Context(), &payoutv1.InitiatePayoutRequest{
		IdempotencyKey: body.IdempotencyKey,
		AccountId:      body.AccountID,
		Amount:         &commonv1.Money{Amount: body.Amount, Currency: body.Currency},
		Rail:           railProto(body.Rail),
		Destination: &payoutv1.BankAccount{
			AccountNumber: body.AccountNumber,
			IfscCode:      body.IFSCCode,
			Iban:          body.IBAN,
			Bic:           body.BIC,
			AccountName:   body.AccountName,
		},
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusCreated, resp)
}

func (h *Handler) getPayout(w http.ResponseWriter, r *http.Request) {
	resp, err := h.payout.GetPayout(r.Context(), &payoutv1.GetPayoutRequest{
		PayoutId: r.PathValue("id"),
	})
	if err != nil {
		writeError(w, grpcStatus(err), err.Error())
		return
	}
	writeProto(w, http.StatusOK, resp)
}

func railProto(s string) payoutv1.PayoutRail {
	switch s {
	case "SEPA":
		return payoutv1.PayoutRail_PAYOUT_RAIL_SEPA
	case "SWIFT":
		return payoutv1.PayoutRail_PAYOUT_RAIL_SWIFT
	default:
		return payoutv1.PayoutRail_PAYOUT_RAIL_NACH
	}
}
