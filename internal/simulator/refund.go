package simulator

import (
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) createStripeRefund(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStripeProvider(r) {
		writeStripeError(w, http.StatusUnauthorized, "authentication_error", "Invalid test API key.")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid form body.")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if s.config.RequireIdempotency && idempotencyKey == "" {
		writeStripeError(w, http.StatusBadRequest, "idempotency_error", "Idempotency-Key is required by the MyScoutee QA contract.")
		return
	}
	intentID := strings.TrimSpace(r.Form.Get("payment_intent"))
	requestHash := sha256Hex([]byte(r.Form.Encode()))
	operationKey := "refund:" + idempotencyKey

	s.mu.Lock()
	if previous, found := s.idempotency[operationKey]; found {
		refund := cloneStripeRefund(s.refunds[previous.SessionID])
		s.mu.Unlock()
		if previous.RequestHash != requestHash || refund == nil || refund.PaymentIntent != intentID {
			writeStripeError(w, http.StatusConflict, "idempotency_error", "The same idempotency key was used with different parameters.")
			return
		}
		writeJSON(w, http.StatusOK, refund)
		return
	}
	intent := s.intents[intentID]
	if intent == nil {
		s.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "No such payment intent.")
		return
	}
	if intent.Status != "succeeded" {
		s.mu.Unlock()
		writeStripeError(w, http.StatusConflict, "invalid_request_error", "Only a succeeded payment intent can be refunded.")
		return
	}
	remaining := intent.AmountReceived - intent.AmountRefunded
	amount := remaining
	if raw := strings.TrimSpace(r.Form.Get("amount")); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			s.mu.Unlock()
			writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Refund amount must be an integer.")
			return
		}
		amount = parsed
	}
	if amount <= 0 || amount > remaining {
		s.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Refund amount exceeds the remaining captured amount.")
		return
	}
	previousIntent := *clonePaymentIntent(intent)
	refund := &StripeRefund{
		ID:            "re_test_" + randomHex(12),
		Object:        "refund",
		Amount:        amount,
		Currency:      intent.Currency,
		PaymentIntent: intent.ID,
		Reason:        firstNonBlank(r.Form.Get("reason"), "requested_by_customer"),
		Status:        "succeeded",
		Created:       s.now().UTC().Unix(),
	}
	intent.AmountRefunded += amount
	s.refunds[refund.ID] = refund
	s.idempotency[operationKey] = idempotencyRecord{RequestHash: requestHash, SessionID: refund.ID}
	if err := s.persistLocked(); err != nil {
		*s.intents[intentID] = previousIntent
		delete(s.refunds, refund.ID)
		delete(s.idempotency, operationKey)
		s.mu.Unlock()
		writeStripeError(w, http.StatusInternalServerError, "api_error", "Could not persist refund.")
		return
	}
	result := cloneStripeRefund(refund)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) retrieveStripeRefund(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStripeProvider(r) {
		writeStripeError(w, http.StatusUnauthorized, "authentication_error", "Invalid test API key.")
		return
	}
	s.mu.RLock()
	refund := cloneStripeRefund(s.refunds[r.PathValue("refundID")])
	s.mu.RUnlock()
	if refund == nil {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "No such refund.")
		return
	}
	writeJSON(w, http.StatusOK, refund)
}

func cloneStripeRefund(refund *StripeRefund) *StripeRefund {
	if refund == nil {
		return nil
	}
	clone := *refund
	return &clone
}
