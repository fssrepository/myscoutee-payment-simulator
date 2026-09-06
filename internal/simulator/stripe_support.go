package simulator

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var lineItemFieldPattern = regexp.MustCompile(`^line_items\[(\d+)]\[price_data]\[([^]]+)](?:\[([^]]+)])?$`)

func parseLineItems(form url.Values) (int64, string, []CheckoutLineItem, error) {
	type parsedLineItem struct {
		amount      int64
		currency    string
		quantity    int64
		name        string
		description string
	}
	items := make(map[int]*parsedLineItem)
	for key, values := range form {
		matches := lineItemFieldPattern.FindStringSubmatch(key)
		if len(matches) == 0 || len(values) == 0 {
			continue
		}
		index, _ := strconv.Atoi(matches[1])
		item := items[index]
		if item == nil {
			item = &parsedLineItem{quantity: 1}
			items[index] = item
		}
		switch matches[2] {
		case "unit_amount":
			amount, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || amount < 0 {
				return 0, "", nil, errors.New("line item unit_amount must be a non-negative integer")
			}
			item.amount = amount
		case "currency":
			item.currency = strings.ToLower(strings.TrimSpace(values[0]))
		case "product_data":
			switch matches[3] {
			case "name":
				item.name = strings.TrimSpace(values[0])
			case "description":
				item.description = strings.TrimSpace(values[0])
			}
		}
	}
	for key, values := range form {
		if !strings.HasPrefix(key, "line_items[") || !strings.HasSuffix(key, "][quantity]") || len(values) == 0 {
			continue
		}
		start := len("line_items[")
		end := strings.Index(key[start:], "]")
		if end < 0 {
			continue
		}
		index, err := strconv.Atoi(key[start : start+end])
		if err != nil {
			continue
		}
		quantity, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || quantity <= 0 {
			return 0, "", nil, errors.New("line item quantity must be a positive integer")
		}
		item := items[index]
		if item == nil {
			item = &parsedLineItem{}
			items[index] = item
		}
		item.quantity = quantity
	}
	if len(items) == 0 {
		return 0, "", nil, errors.New("at least one line item is required")
	}
	var total int64
	currency := ""
	indexes := make([]int, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	lineItems := make([]CheckoutLineItem, 0, len(indexes))
	for _, index := range indexes {
		item := items[index]
		if item.currency == "" || item.amount < 0 {
			return 0, "", nil, errors.New("each line item requires currency and unit_amount")
		}
		if item.name == "" {
			return 0, "", nil, errors.New("each line item requires product_data name")
		}
		if currency == "" {
			currency = item.currency
		} else if currency != item.currency {
			return 0, "", nil, errors.New("all line items must use the same currency")
		}
		if item.quantity <= 0 || item.amount > (1<<63-1)/item.quantity || total > (1<<63-1)-(item.amount*item.quantity) {
			return 0, "", nil, errors.New("line item total is out of range")
		}
		itemTotal := item.amount * item.quantity
		total += itemTotal
		lineItems = append(lineItems, CheckoutLineItem{
			Name:        item.name,
			Description: item.description,
			Quantity:    item.quantity,
			UnitAmount:  item.amount,
			AmountTotal: itemTotal,
			Currency:    item.currency,
		})
	}
	return total, currency, lineItems, nil
}

func parseBracketMap(form url.Values, prefix string) map[string]string {
	result := make(map[string]string)
	start := prefix + "["
	for key, values := range form {
		if !strings.HasPrefix(key, start) || !strings.HasSuffix(key, "]") || len(values) == 0 {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, start), "]")
		if name != "" {
			result[name] = values[0]
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func signPayload(secret string, timestamp int64, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func validAbsoluteHTTPURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func replaceSessionPlaceholder(rawURL string, sessionID string) string {
	return strings.ReplaceAll(rawURL, "{CHECKOUT_SESSION_ID}", url.QueryEscape(sessionID))
}

func cloneSession(session *CheckoutSession) *CheckoutSession {
	if session == nil {
		return nil
	}
	clone := *session
	clone.Metadata = cloneMap(session.Metadata)
	clone.LineItems = append([]CheckoutLineItem(nil), session.LineItems...)
	return &clone
}

func clonePaymentIntent(intent *PaymentIntent) *PaymentIntent {
	if intent == nil {
		return nil
	}
	clone := *intent
	clone.Metadata = cloneMap(intent.Metadata)
	if intent.NextAction != nil {
		nextAction := *intent.NextAction
		clone.NextAction = &nextAction
	}
	if intent.LastPaymentError != nil {
		lastPaymentError := *intent.LastPaymentError
		clone.LastPaymentError = &lastPaymentError
	}
	return &clone
}

func eventObject(eventType string, session *CheckoutSession, intent *PaymentIntent) any {
	if strings.HasPrefix(eventType, "payment_intent.") {
		return intent
	}
	return session
}

func eventObjectID(payload json.RawMessage) string {
	var object struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(payload, &object)
	return object.ID
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Server) authorizeAPI(r *http.Request) bool {
	return constantTimeEqual(
		strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
		s.config.APIKey)
}

func (s *Server) authorizeStripeProvider(r *http.Request) bool {
	supplied := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.RLock()
	credential := s.configuration.StripeCredential
	s.mu.RUnlock()
	return constantTimeEqual(supplied, s.config.APIKey) ||
		(strings.TrimSpace(credential) != "" && constantTimeEqual(supplied, credential))
}

func cloneMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

func randomHex(bytes int) string {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		panic(fmt.Sprintf("secure random source failed: %v", err))
	}
	return hex.EncodeToString(buffer)
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func constantTimeEqual(actual string, expected string) bool {
	if len(actual) != len(expected) || actual == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json") || r.URL.Query().Get("format") == "json"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

func writeStripeError(w http.ResponseWriter, status int, errorType string, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": errorType, "message": message}})
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func CheckHealth(listenAddress string) error {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
