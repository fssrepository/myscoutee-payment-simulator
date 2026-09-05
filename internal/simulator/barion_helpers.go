package simulator

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

func authorizeBarionPayment(payment *BarionPayment, now time.Time) {
	payment.Status = "Authorized"
	payment.FundingSource = "BankCard"
	payment.PaymentMethod = "BankCard"
	payment.DelayedCaptureUntil = now.Add(7 * 24 * time.Hour).Format(time.RFC3339)
	payment.LastOperation = "authorize"
	for index := range payment.Transactions {
		payment.Transactions[index].Status = "Authorized"
	}
}

func (s *Server) authorizeBarion(bodyKey string, headerKey string) bool {
	key := firstNonBlank(headerKey, bodyKey)
	s.mu.RLock()
	credential := s.configuration.BarionCredential
	s.mu.RUnlock()
	return constantTimeEqual(key, s.config.BarionPOSKey) ||
		(strings.TrimSpace(credential) != "" && constantTimeEqual(key, credential))
}

func (s *Server) authorizedBarionBrowserPayment(r *http.Request, requiredStatus string) *BarionPayment {
	s.mu.RLock()
	payment := cloneBarionPayment(s.barionPayments[r.PathValue("paymentID")])
	s.mu.RUnlock()
	if payment == nil || !constantTimeEqual(r.URL.Query().Get("token"), payment.ControlToken) {
		return nil
	}
	if requiredStatus != "" && payment.Status != requiredStatus {
		return nil
	}
	return payment
}

func (s *Server) barionPaymentAuditsLocked() []BarionPaymentAudit {
	ids := make([]string, 0, len(s.barionPayments))
	for id := range s.barionPayments {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	result := make([]BarionPaymentAudit, 0, len(ids))
	for _, id := range ids {
		payment := s.barionPayments[id]
		result = append(result, BarionPaymentAudit{
			PaymentID: payment.PaymentID, PaymentRequestID: payment.PaymentRequestID,
			Status: payment.Status, Total: payment.Total, Currency: payment.Currency,
			DelayedCaptureUntil: payment.DelayedCaptureUntil,
			Transactions:        slices.Clone(payment.Transactions), LastOperation: payment.LastOperation,
			CallbackDelivery: payment.CallbackDelivery,
		})
	}
	return result
}

func cloneBarionPayment(payment *BarionPayment) *BarionPayment {
	if payment == nil {
		return nil
	}
	clone := *payment
	clone.AllowedFundingSources = slices.Clone(payment.AllowedFundingSources)
	clone.Transactions = slices.Clone(payment.Transactions)
	for index := range clone.Transactions {
		clone.Transactions[index].Items = slices.Clone(payment.Transactions[index].Items)
	}
	return &clone
}

func barionStartResponseFor(payment *BarionPayment) barionStartResponse {
	return barionStartResponse{
		PaymentID: payment.PaymentID, PaymentRequestID: payment.PaymentRequestID,
		Status: payment.Status, GatewayURL: payment.GatewayURL,
		CallbackURL: payment.CallbackURL, RedirectURL: payment.RedirectURL,
		Transactions: slices.Clone(payment.Transactions), Errors: []barionAPIError{},
	}
}

func barionFinishResponseFor(payment *BarionPayment) barionFinishResponse {
	return barionFinishResponse{
		IsSuccessful: true, PaymentID: payment.PaymentID,
		PaymentRequestID: payment.PaymentRequestID, Status: payment.Status,
		Transactions: slices.Clone(payment.Transactions), Errors: []barionAPIError{},
	}
}

func decodeLimitedJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return decoder.Decode(target)
}

func writeBarionError(w http.ResponseWriter, status int, code string, description string) {
	writeJSON(w, status, map[string]any{
		"Errors": []barionAPIError{{ErrorCode: code, Title: code, Description: description}},
	})
}

func parseBarionDuration(raw string, fallback time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid Barion duration")
	}
	days, err := time.ParseDuration(parts[0] + "h")
	if err != nil {
		return 0, err
	}
	days *= 24
	clock, err := time.ParseDuration(strings.Replace(parts[1], ":", "h", 1))
	if err == nil {
		return days + clock, nil
	}
	var hours, minutes, seconds int
	if _, scanErr := fmt.Sscanf(parts[1], "%d:%d:%d", &hours, &minutes, &seconds); scanErr != nil ||
		hours < 0 || hours > 23 || minutes < 0 || minutes > 59 || seconds < 0 || seconds > 59 {
		return 0, fmt.Errorf("invalid Barion duration")
	}
	return days + time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
}

func validMoney(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func validGUID(value string) bool {
	return guidPattern.MatchString(strings.TrimSpace(value))
}

func randomGUID() string {
	raw := randomHex(16)
	return fmt.Sprintf("%s-%s-4%s-8%s-%s", raw[0:8], raw[8:12], raw[13:16], raw[17:20], raw[20:32])
}

func appendURLQuery(rawURL string, key string, value string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
