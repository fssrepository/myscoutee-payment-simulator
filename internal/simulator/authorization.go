package simulator

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

type pendingAuthorization struct {
	ID              string  `json:"id"`
	Provider        string  `json:"provider"`
	Reference       string  `json:"reference,omitempty"`
	Amount          float64 `json:"amount"`
	Currency        string  `json:"currency"`
	Created         int64   `json:"created"`
	ApproveURL      string  `json:"approveUrl"`
	DeclineURL      string  `json:"declineUrl"`
	AmountMinorUnit bool    `json:"amountMinorUnit"`
}

func (s *Server) authorizationRoutes() {
	s.mux.HandleFunc("POST /myscoutee/v1/authorization-access", s.createAuthorizationAccess)
	s.mux.HandleFunc("GET /authorization-access/{ticket}", s.exchangeAuthorizationAccess)
	s.mux.HandleFunc("GET /simulator-ui/authorizations.html", s.authorizationDocument)
	s.mux.HandleFunc("GET /authorization-session", s.sessionAuthorizations)
}

func (s *Server) createAuthorizationAccess(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAPI(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid test API key."})
		return
	}
	now := s.now().UTC()
	expiresAt := now.Add(configurationAccessTTL)
	ticket := randomHex(24)
	s.mu.Lock()
	s.cleanupConfigurationAccessLocked(now)
	s.configurationAccessTickets[ticket] = expiresAt
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]string{
		"url":       strings.TrimRight(s.config.PublicBaseURL, "/") + "/authorization-access/" + ticket,
		"expiresAt": expiresAt.Format(time.RFC3339Nano),
	})
}

func (s *Server) exchangeAuthorizationAccess(w http.ResponseWriter, r *http.Request) {
	s.exchangeAdminAccess(w, r, "/simulator-ui/authorizations.html")
}

func (s *Server) authorizationDocument(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		http.NotFound(w, r)
		return
	}
	document, err := paymentMethodUI.ReadFile("web/authorizations.html")
	if err != nil {
		http.Error(w, "Payment authorization simulator is unavailable.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(document)))
	_, _ = w.Write(document)
}

func (s *Server) sessionAuthorizations(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Admin simulator session is missing or expired."})
		return
	}
	s.mu.RLock()
	pending := s.pendingAuthorizationsLocked()
	configuration := s.configuration
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"provider":    configuration.Provider,
		"requires3ds": configuration.Requires3DS,
		"pending":     pending,
	})
}

func (s *Server) pendingAuthorizationsLocked() []pendingAuthorization {
	result := make([]pendingAuthorization, 0)
	baseURL := strings.TrimRight(s.config.PublicBaseURL, "/")
	for _, session := range s.sessions {
		if session == nil || session.Status != "open" {
			continue
		}
		intent := s.intents[session.PaymentIntent]
		if intent == nil || intent.Status != "requires_action" {
			continue
		}
		token := url.QueryEscape(session.ControlToken)
		actionBase := baseURL + "/test/bank-auth/" + url.PathEscape(session.ID)
		result = append(result, pendingAuthorization{
			ID: session.ID, Provider: "stripe",
			Reference: firstNonBlank(intent.ClientReferenceID, session.ClientReferenceID),
			Amount:    float64(intent.Amount), Currency: strings.ToUpper(intent.Currency), Created: intent.Created,
			ApproveURL:      actionBase + "/approve?token=" + token + "&format=json",
			DeclineURL:      actionBase + "/decline?token=" + token + "&format=json",
			AmountMinorUnit: true,
		})
	}
	for _, payment := range s.barionPayments {
		if payment == nil || payment.Status != "InProgress" || payment.LastOperation != "customer_action_required" {
			continue
		}
		created, _ := time.Parse(time.RFC3339, payment.CreatedAt)
		token := url.QueryEscape(payment.ControlToken)
		actionBase := baseURL + "/test/barion/bank-auth/" + url.PathEscape(payment.PaymentID)
		result = append(result, pendingAuthorization{
			ID: payment.PaymentID, Provider: "barion", Reference: payment.PaymentRequestID,
			Amount: payment.Total, Currency: strings.ToUpper(payment.Currency), Created: created.Unix(),
			ApproveURL: actionBase + "/approve?token=" + token + "&format=json",
			DeclineURL: actionBase + "/decline?token=" + token + "&format=json",
		})
	}
	slices.SortFunc(result, func(left, right pendingAuthorization) int {
		if left.Created == right.Created {
			return strings.Compare(right.ID, left.ID)
		}
		if left.Created > right.Created {
			return -1
		}
		return 1
	})
	return result
}

func (s *Server) stripePaymentWaitPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	session := cloneSession(s.sessions[r.PathValue("sessionID")])
	var intent *PaymentIntent
	if session != nil {
		intent = clonePaymentIntent(s.intents[session.PaymentIntent])
	}
	s.mu.RUnlock()
	if session == nil || intent == nil || !constantTimeEqual(r.URL.Query().Get("token"), session.ControlToken) {
		http.Error(w, "Payment confirmation was not found.", http.StatusNotFound)
		return
	}
	s.renderPaymentWaitPage(w, "Stripe", intent.ID, float64(intent.Amount), strings.ToUpper(intent.Currency), true)
}

func (s *Server) barionPaymentWaitPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	payment := cloneBarionPayment(s.barionPayments[r.PathValue("paymentID")])
	s.mu.RUnlock()
	if payment == nil || !constantTimeEqual(r.URL.Query().Get("token"), payment.ControlToken) {
		http.Error(w, "Payment confirmation was not found.", http.StatusNotFound)
		return
	}
	s.renderPaymentWaitPage(w, "Barion", payment.PaymentID, payment.Total, strings.ToUpper(payment.Currency), false)
}

func (s *Server) renderPaymentWaitPage(
	w http.ResponseWriter,
	provider string,
	reference string,
	amount float64,
	currency string,
	minorUnits bool,
) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paymentWaitTemplate.Execute(w, map[string]any{
		"Provider": provider, "Reference": reference, "Amount": amount,
		"Currency": currency, "MinorUnits": minorUnits,
	}); err != nil {
		http.Error(w, "Could not render payment confirmation status.", http.StatusInternalServerError)
	}
}
