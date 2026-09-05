package simulator

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) replayBarionCallback(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAudit(r) {
		http.Error(w, "Audit token required.", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	payment := s.barionPayments[r.PathValue("paymentID")]
	s.mu.RUnlock()
	if payment == nil {
		http.Error(w, "Barion test payment not found.", http.StatusNotFound)
		return
	}
	s.deliverBarionCallback(payment.PaymentID)
	s.mu.RLock()
	delivery := s.barionPayments[payment.PaymentID].CallbackDelivery
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{"payment_id": payment.PaymentID, "delivery": delivery})
}

func (s *Server) deliverBarionCallback(paymentID string) {
	s.mu.RLock()
	payment := cloneBarionPayment(s.barionPayments[paymentID])
	s.mu.RUnlock()
	if payment == nil {
		return
	}
	callbackURL := payment.CallbackURL
	if strings.TrimSpace(s.config.BarionCallbackURL) != "" {
		callbackURL = appendURLQuery(s.config.BarionCallbackURL, "paymentId", payment.PaymentID)
	}
	form := url.Values{"PaymentId": {payment.PaymentID}}
	request, err := http.NewRequest(http.MethodPost, callbackURL, strings.NewReader(form.Encode()))
	if err != nil {
		s.recordBarionCallback(paymentID, 0, err)
		return
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-App-Session-Kind", "demo")
	response, err := s.client.Do(request)
	if err != nil {
		s.recordBarionCallback(paymentID, 0, err)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode != http.StatusOK {
		s.recordBarionCallback(paymentID, response.StatusCode, fmt.Errorf("callback returned HTTP %d", response.StatusCode))
		return
	}
	s.recordBarionCallback(paymentID, response.StatusCode, nil)
}

func (s *Server) recordBarionCallback(paymentID string, statusCode int, callbackErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	payment := s.barionPayments[paymentID]
	if payment == nil {
		return
	}
	payment.CallbackDelivery.Attempts++
	payment.CallbackDelivery.LastAttemptAt = s.now().UTC().Unix()
	payment.CallbackDelivery.LastStatusCode = statusCode
	if callbackErr == nil {
		payment.CallbackDelivery.LastError = ""
	} else {
		payment.CallbackDelivery.LastError = callbackErr.Error()
	}
	if err := s.persistLocked(); err != nil {
		log.Printf("persist Barion callback audit: %v", err)
	}
}
