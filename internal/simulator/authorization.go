package simulator

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const paymentAuthorizationTTL = 3 * time.Minute

type pendingAuthorization struct {
	ID              string  `json:"id"`
	Kind            string  `json:"kind,omitempty"`
	Provider        string  `json:"provider"`
	Reference       string  `json:"reference,omitempty"`
	UserReference   string  `json:"userReference,omitempty"`
	Last4           string  `json:"last4,omitempty"`
	Amount          float64 `json:"amount"`
	Currency        string  `json:"currency"`
	Created         int64   `json:"created"`
	ReviewURL       string  `json:"reviewUrl"`
	AmountMinorUnit bool    `json:"amountMinorUnit"`
}

func (s *Server) authorizationRoutes() {
	s.mux.HandleFunc("POST /myscoutee/v1/authorization-access", s.createAuthorizationAccess)
	s.mux.HandleFunc("GET /authorization-access/{ticket}", s.exchangeAuthorizationAccess)
	s.mux.HandleFunc("GET /simulator-ui/authorizations.html", s.authorizationDocument)
	s.mux.HandleFunc("GET /authorization-session", s.sessionAuthorizations)
	s.mux.HandleFunc("GET /public/payment-authorizations/{provider}/{authorizationID}", s.publicPaymentAuthorizationStatus)
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
		result = append(result, pendingAuthorization{
			ID: session.ID, Provider: "stripe",
			Reference:     firstNonBlank(intent.ClientReferenceID, session.ClientReferenceID),
			UserReference: firstNonBlank(intent.Metadata["user_id"], session.Metadata["user_id"]),
			Amount:        float64(intent.Amount), Currency: strings.ToUpper(intent.Currency), Created: intent.Created,
			ReviewURL:       baseURL + "/bank-auth/" + url.PathEscape(session.ID) + "?token=" + token,
			AmountMinorUnit: true,
		})
	}
	for _, payment := range s.barionPayments {
		if payment == nil || payment.Status != "InProgress" || payment.LastOperation != "customer_action_required" {
			continue
		}
		created, _ := time.Parse(time.RFC3339, payment.CreatedAt)
		token := url.QueryEscape(payment.ControlToken)
		result = append(result, pendingAuthorization{
			ID: payment.PaymentID, Provider: "barion", Reference: payment.PaymentRequestID,
			Amount: payment.Total, Currency: strings.ToUpper(payment.Currency), Created: created.Unix(),
			ReviewURL: baseURL + "/barion/bank-auth/" + url.PathEscape(payment.PaymentID) + "?token=" + token,
		})
	}
	for _, registration := range s.registrations {
		if registration == nil || registration.Status != "pending" || !registration.Awaiting3DS ||
			(registration.ThreeDSExpires > 0 && !time.Unix(registration.ThreeDSExpires, 0).After(s.now().UTC())) {
			continue
		}
		token := url.QueryEscape(registration.ControlToken)
		result = append(result, pendingAuthorization{
			ID: registration.ID, Kind: "card-registration", Provider: registration.Provider,
			Reference: registration.ID, UserReference: registration.UserReference, Last4: registration.Last4,
			Created:   registration.CreatedAt,
			ReviewURL: baseURL + "/payment-method-registration-auth/" + url.PathEscape(registration.ID) + "?token=" + token,
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
	token := url.QueryEscape(session.ControlToken)
	statusURL := "/public/payment-authorizations/stripe/" + url.PathEscape(session.ID) + "?token=" + token
	s.renderPaymentWaitPage(w, "Stripe", session.ID, intent.ID, float64(intent.Amount), strings.ToUpper(intent.Currency), true, statusURL)
}

func (s *Server) barionPaymentWaitPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	payment := cloneBarionPayment(s.barionPayments[r.PathValue("paymentID")])
	s.mu.RUnlock()
	if payment == nil || !constantTimeEqual(r.URL.Query().Get("token"), payment.ControlToken) {
		http.Error(w, "Payment confirmation was not found.", http.StatusNotFound)
		return
	}
	token := url.QueryEscape(payment.ControlToken)
	statusURL := "/public/payment-authorizations/barion/" + url.PathEscape(payment.PaymentID) + "?token=" + token
	s.renderPaymentWaitPage(w, "Barion", payment.PaymentID, payment.PaymentID, payment.Total, strings.ToUpper(payment.Currency), false, statusURL)
}

func (s *Server) renderPaymentWaitPage(
	w http.ResponseWriter,
	provider string,
	id string,
	reference string,
	amount float64,
	currency string,
	minorUnits bool,
	statusURL string,
) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := paymentWaitTemplate.Execute(w, map[string]any{
		"Provider": provider, "ProviderSlug": strings.ToLower(provider), "ID": id, "Reference": reference, "Amount": amount,
		"Currency": currency, "MinorUnits": minorUnits, "StatusURL": statusURL,
	}); err != nil {
		http.Error(w, "Could not render payment confirmation status.", http.StatusInternalServerError)
	}
}

func (s *Server) publicPaymentAuthorizationStatus(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	id := strings.TrimSpace(r.PathValue("authorizationID"))
	token := r.URL.Query().Get("token")
	var stripeEvent *WebhookEvent
	var barionCallbackID string
	var expiresAt int64
	var previousSession *CheckoutSession
	var previousIntent *PaymentIntent
	var previousBarion *BarionPayment

	s.mu.Lock()
	status, found, changed := "", false, false
	switch provider {
	case "stripe":
		session := s.sessions[id]
		var intent *PaymentIntent
		if session != nil {
			intent = s.intents[session.PaymentIntent]
		}
		if session != nil && intent != nil && constantTimeEqual(token, session.ControlToken) {
			found = true
			expiresAt = time.Unix(intent.Created, 0).Add(paymentAuthorizationTTL).Unix()
			if stripeAuthorizationTimedOut(intent, s.now().UTC()) {
				previousSession = cloneSession(session)
				previousIntent = clonePaymentIntent(intent)
				session.Status = "expired"
				session.PaymentStatus = "unpaid"
				intent.Status = "canceled"
				intent.CancellationReason = "abandoned"
				intent.NextAction = nil
				stripeEvent = s.newEventLocked("payment_intent.canceled", intent, session.IdempotencyKey)
				changed = true
			}
			status = stripeAuthorizationStatus(intent)
		}
	case "barion":
		payment := s.barionPayments[id]
		if payment != nil && constantTimeEqual(token, payment.ControlToken) {
			found = true
			if created, err := time.Parse(time.RFC3339, payment.CreatedAt); err == nil {
				expiresAt = created.Add(paymentAuthorizationTTL).Unix()
			}
			if barionAuthorizationTimedOut(payment, s.now().UTC()) {
				previousBarion = cloneBarionPayment(payment)
				payment.Status = "Expired"
				payment.LastOperation = "3ds_timeout"
				for index := range payment.Transactions {
					payment.Transactions[index].Status = "Failed"
				}
				barionCallbackID = payment.PaymentID
				changed = true
			}
			status = barionAuthorizationStatus(payment)
		}
	}
	if changed {
		if err := s.persistLocked(); err != nil {
			if previousSession != nil {
				*s.sessions[id] = *previousSession
			}
			if previousIntent != nil {
				*s.intents[previousIntent.ID] = *previousIntent
			}
			if stripeEvent != nil {
				delete(s.events, stripeEvent.ID)
				s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
			}
			if previousBarion != nil {
				*s.barionPayments[id] = *previousBarion
			}
			s.mu.Unlock()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not persist payment authorization timeout."})
			return
		}
	}
	s.mu.Unlock()
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Payment authorization was not found."})
		return
	}
	if stripeEvent != nil {
		s.deliver(stripeEvent)
	}
	if barionCallbackID != "" {
		s.deliverBarionCallback(barionCallbackID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": status, "expiresAt": expiresAt})
}

func stripeAuthorizationTimedOut(intent *PaymentIntent, now time.Time) bool {
	return intent != nil && intent.Status == "requires_action" &&
		time.Unix(intent.Created, 0).Add(paymentAuthorizationTTL).Before(now)
}

func barionAuthorizationTimedOut(payment *BarionPayment, now time.Time) bool {
	created, err := time.Parse(time.RFC3339, payment.CreatedAt)
	return payment != nil && err == nil && payment.Status == "InProgress" &&
		payment.LastOperation == "customer_action_required" && created.Add(paymentAuthorizationTTL).Before(now)
}

func stripeAuthorizationStatus(intent *PaymentIntent) string {
	if intent == nil {
		return "failed"
	}
	switch intent.Status {
	case "requires_action":
		return "pending"
	case "requires_capture":
		return "authorized"
	case "succeeded":
		return "captured"
	case "canceled":
		if intent.CancellationReason == "abandoned" {
			return "expired"
		}
		return "cancelled"
	default:
		return "failed"
	}
}

func barionAuthorizationStatus(payment *BarionPayment) string {
	if payment == nil {
		return "failed"
	}
	switch strings.ToLower(payment.Status) {
	case "inprogress", "prepared", "started":
		return "pending"
	case "authorized":
		return "authorized"
	case "succeeded":
		return "captured"
	case "expired":
		return "expired"
	case "canceled":
		return "cancelled"
	default:
		return "failed"
	}
}
