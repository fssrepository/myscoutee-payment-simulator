package simulator

import (
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var paymentMethodUI embed.FS

type createPaymentMethodRegistrationRequest struct {
	RegistrationID string `json:"registration_id"`
	UserReference  string `json:"user_reference"`
	Provider       string `json:"provider"`
	ExpiresAt      string `json:"expires_at"`
	CallbackURL    string `json:"callback_url"`
}

type completePaymentMethodRegistrationRequest struct {
	CardNumber     string `json:"cardNumber"`
	ExpiryMonth    int    `json:"expiryMonth"`
	ExpiryYear     int    `json:"expiryYear"`
	CardholderName string `json:"cardholderName"`
	SecurityCode   string `json:"securityCode"`
}

type simulatorCardProfile struct {
	Provider string
	Brand    string
	Last4    string
}

var simulatorCardProfiles = map[string]simulatorCardProfile{
	"4242424242424242": {Provider: "stripe", Brand: "Visa", Last4: "4242"},
	"4000002500003155": {Provider: "stripe", Brand: "Visa", Last4: "3155"},
	"5555555555554444": {Provider: "barion", Brand: "Mastercard", Last4: "4444"},
	"5200000000001096": {Provider: "barion", Brand: "Mastercard", Last4: "1096"},
}

func (s *Server) paymentMethodRegistrationRoutes() {
	s.mux.HandleFunc("GET /simulator-ui/{asset}", s.paymentMethodUIAsset)
	s.mux.HandleFunc("POST /myscoutee/v1/payment-method-registrations", s.createPaymentMethodRegistration)
	s.mux.HandleFunc("GET /myscoutee/v1/payment-method-registrations/{registrationID}", s.retrievePaymentMethodRegistration)
	s.mux.HandleFunc("DELETE /myscoutee/v1/payment-methods/{provider}/{providerToken}", s.revokePaymentMethod)
	s.mux.HandleFunc("GET /register/{registrationID}", s.paymentMethodRegistrationPage)
	s.mux.HandleFunc("GET /public/payment-method-registrations/{registrationID}", s.publicPaymentMethodRegistration)
	s.mux.HandleFunc("POST /public/payment-method-registrations/{registrationID}/complete", s.completePaymentMethodRegistration)
	s.mux.HandleFunc("POST /public/payment-method-registrations/{registrationID}/cancel", s.cancelPaymentMethodRegistration)
}

func (s *Server) revokePaymentMethod(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAPI(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid test API key."})
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	providerToken := strings.TrimSpace(r.PathValue("providerToken"))
	if (provider != "stripe" && provider != "barion") || providerToken == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Payment provider and token are required."})
		return
	}

	s.mu.Lock()
	var registration *PaymentMethodRegistration
	insertedSeed := false
	for _, candidate := range s.registrations {
		if candidate != nil && candidate.Provider == provider &&
			constantTimeEqual(candidate.ProviderToken, providerToken) {
			registration = candidate
			break
		}
	}
	if registration == nil {
		registration = simulatorSeedPaymentMethod(provider, providerToken)
		if registration != nil {
			registration.ID = "seed_" + providerToken
			s.registrations[registration.ID] = registration
			insertedSeed = true
		}
	}
	if registration == nil {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Saved payment method was not found."})
		return
	}
	if registration.Status == "revoked" {
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if registration.Status != "completed" {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Saved payment method is not reusable."})
		return
	}

	previous := *registration
	registration.Status = "revoked"
	if err := s.persistLocked(); err != nil {
		if insertedSeed {
			delete(s.registrations, registration.ID)
		} else {
			*registration = previous
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not revoke saved payment method."})
		return
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) paymentMethodUIAsset(w http.ResponseWriter, r *http.Request) {
	asset := strings.TrimSpace(r.PathValue("asset"))
	switch asset {
	case "config.css", "config.js", "register.css", "register.js":
		http.ServeFileFS(w, r, paymentMethodUI, "web/"+asset)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) createPaymentMethodRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAPI(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid test API key."})
		return
	}
	var request createPaymentMethodRegistrationRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid registration request."})
		return
	}
	request.RegistrationID = strings.TrimSpace(request.RegistrationID)
	request.UserReference = strings.TrimSpace(request.UserReference)
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.CallbackURL = strings.TrimSpace(request.CallbackURL)
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(request.ExpiresAt))
	if request.RegistrationID == "" || request.UserReference == "" ||
		(request.Provider != "stripe" && request.Provider != "barion") || err != nil ||
		!validAbsoluteHTTPURL(request.CallbackURL) ||
		!expiresAt.After(s.now().UTC()) || expiresAt.After(s.now().UTC().Add(10*time.Minute)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Registration id, user, provider and a short-lived expiry are required."})
		return
	}

	s.mu.Lock()
	if request.Provider != s.configuration.Provider {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Registration provider does not match the active simulator configuration."})
		return
	}
	if existing := s.registrations[request.RegistrationID]; existing != nil {
		result := clonePaymentMethodRegistration(existing, true)
		s.mu.Unlock()
		if existing.UserReference != request.UserReference || existing.Provider != request.Provider ||
			existing.ExpiresAt != expiresAt.UTC().Format(time.RFC3339Nano) || existing.CallbackURL != request.CallbackURL {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "Registration id was already used with different data."})
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	controlToken := randomHex(24)
	registration := &PaymentMethodRegistration{
		ID:            request.RegistrationID,
		Provider:      request.Provider,
		Status:        "pending",
		ExpiresAt:     expiresAt.UTC().Format(time.RFC3339Nano),
		UserReference: request.UserReference,
		ControlToken:  controlToken,
		CreatedAt:     s.now().UTC().Unix(),
		CallbackURL:   request.CallbackURL,
	}
	registration.URL = strings.TrimRight(s.config.PublicBaseURL, "/") + "/register/" +
		url.PathEscape(registration.ID) + "?token=" + url.QueryEscape(controlToken)
	s.registrations[registration.ID] = registration
	if err := s.persistLocked(); err != nil {
		delete(s.registrations, registration.ID)
		s.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not persist registration."})
		return
	}
	result := clonePaymentMethodRegistration(registration, true)
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) retrievePaymentMethodRegistration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAPI(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid test API key."})
		return
	}
	s.mu.Lock()
	registration := s.registrations[r.PathValue("registrationID")]
	changed := s.expirePaymentMethodRegistrationLocked(registration)
	if changed {
		_ = s.persistLocked()
	}
	result := clonePaymentMethodRegistration(registration, true)
	s.mu.Unlock()
	if result == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Registration not found."})
		return
	}
	writeJSON(w, http.StatusOK, result)
	if changed {
		go s.deliverPaymentMethodRegistrationCallback(result.ID, result.Status)
	}
}

func (s *Server) paymentMethodRegistrationPage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	registration := s.registrations[r.PathValue("registrationID")]
	changed := s.expirePaymentMethodRegistrationLocked(registration)
	if changed {
		_ = s.persistLocked()
	}
	valid := registration != nil && constantTimeEqual(r.URL.Query().Get("token"), registration.ControlToken)
	result := clonePaymentMethodRegistration(registration, false)
	s.mu.Unlock()
	if !valid {
		http.Error(w, "Card registration not found.", http.StatusNotFound)
		return
	}
	if changed {
		go s.deliverPaymentMethodRegistrationCallback(result.ID, result.Status)
	}
	http.ServeFileFS(w, r, paymentMethodUI, "web/register.html")
}

func (s *Server) publicPaymentMethodRegistration(w http.ResponseWriter, r *http.Request) {
	registration, changed := s.publicRegistration(r)
	if registration == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Registration not found."})
		return
	}
	writeJSON(w, http.StatusOK, registration)
	if changed {
		go s.deliverPaymentMethodRegistrationCallback(registration.ID, registration.Status)
	}
}

func (s *Server) completePaymentMethodRegistration(w http.ResponseWriter, r *http.Request) {
	var request completePaymentMethodRegistrationRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid card form."})
		return
	}
	cardNumber := digitsOnly(request.CardNumber)
	securityCode := digitsOnly(request.SecurityCode)
	profile, known := simulatorCardProfiles[cardNumber]
	cardholder := normalizeCardholderName(request.CardholderName)

	s.mu.Lock()
	registration := s.registrations[r.PathValue("registrationID")]
	if registration == nil || !constantTimeEqual(r.URL.Query().Get("token"), registration.ControlToken) {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Registration not found."})
		return
	}
	if s.expirePaymentMethodRegistrationLocked(registration) {
		_ = s.persistLocked()
	}
	if registration.Status != "pending" {
		result := clonePaymentMethodRegistration(registration, false)
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, result)
		return
	}
	if !known || profile.Provider != registration.Provider {
		s.mu.Unlock()
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Use one of the simulator test cards shown on this screen."})
		return
	}
	if len(securityCode) != 3 || cardholder == "" || !validFutureExpiry(request.ExpiryMonth, request.ExpiryYear, s.now().UTC()) {
		s.mu.Unlock()
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "Enter a cardholder, a future expiry and a three-digit simulator security code."})
		return
	}
	previous := *registration
	registration.Status = "completed"
	registration.Brand = profile.Brand
	registration.Last4 = profile.Last4
	registration.ExpiryMonth = request.ExpiryMonth
	registration.ExpiryYear = request.ExpiryYear
	registration.CardholderName = cardholder
	registration.Requires3DS = s.configuration.Requires3DS
	if registration.Provider == "stripe" {
		registration.ProviderToken = "pm_sim_" + randomHex(12)
	} else {
		registration.ProviderToken = "rec_sim_" + randomHex(12)
	}
	registration.URL = ""
	if err := s.persistLocked(); err != nil {
		*registration = previous
		s.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save tokenized card."})
		return
	}
	result := clonePaymentMethodRegistration(registration, false)
	s.mu.Unlock()
	s.deliverPaymentMethodRegistrationCallback(result.ID, result.Status)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) cancelPaymentMethodRegistration(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	registration := s.registrations[r.PathValue("registrationID")]
	if registration == nil || !constantTimeEqual(r.URL.Query().Get("token"), registration.ControlToken) {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Registration not found."})
		return
	}
	if registration.Status == "pending" {
		registration.Status = "cancelled"
		registration.URL = ""
		if err := s.persistLocked(); err != nil {
			s.mu.Unlock()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not cancel registration."})
			return
		}
	}
	result := clonePaymentMethodRegistration(registration, false)
	s.mu.Unlock()
	s.deliverPaymentMethodRegistrationCallback(result.ID, result.Status)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) deliverPaymentMethodRegistrationCallback(registrationID string, status string) {
	s.mu.RLock()
	registration := s.registrations[registrationID]
	callbackURL := ""
	if registration != nil {
		callbackURL = registration.CallbackURL
	}
	s.mu.RUnlock()
	if !validAbsoluteHTTPURL(callbackURL) {
		return
	}
	payload, err := json.Marshal(map[string]string{
		"registrationId": registrationID,
		"status":         status,
	})
	if err != nil {
		return
	}
	request, err := http.NewRequest(http.MethodPost, callbackURL, strings.NewReader(string(payload)))
	if err != nil {
		return
	}
	request.Header.Set("Authorization", "Bearer "+s.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return
	}
	_ = response.Body.Close()
}

func (s *Server) publicRegistration(r *http.Request) (*PaymentMethodRegistration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	registration := s.registrations[r.PathValue("registrationID")]
	if registration == nil || !constantTimeEqual(r.URL.Query().Get("token"), registration.ControlToken) {
		return nil, false
	}
	changed := s.expirePaymentMethodRegistrationLocked(registration)
	if changed {
		_ = s.persistLocked()
	}
	return clonePaymentMethodRegistration(registration, false), changed
}

func (s *Server) expirePaymentMethodRegistrationLocked(registration *PaymentMethodRegistration) bool {
	if registration == nil || registration.Status != "pending" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, registration.ExpiresAt)
	if err != nil || !expiresAt.After(s.now().UTC()) {
		registration.Status = "expired"
		registration.URL = ""
		return true
	}
	return false
}

func clonePaymentMethodRegistration(value *PaymentMethodRegistration, includeProviderToken bool) *PaymentMethodRegistration {
	if value == nil {
		return nil
	}
	clone := *value
	clone.ControlToken = ""
	clone.UserReference = ""
	clone.Requires3DS = false
	clone.CreatedAt = 0
	if !includeProviderToken {
		clone.ProviderToken = ""
	}
	return &clone
}

func simulatorSeedPaymentMethod(provider string, providerToken string) *PaymentMethodRegistration {
	if provider != "stripe" {
		return nil
	}
	switch providerToken {
	case "pm_sim_seed_alex_4242":
		return &PaymentMethodRegistration{Provider: "stripe", ProviderToken: providerToken, Status: "completed", Brand: "Visa", Last4: "4242"}
	case "pm_sim_seed_alex_1881":
		return &PaymentMethodRegistration{Provider: "stripe", ProviderToken: providerToken, Status: "completed", Brand: "Visa", Last4: "1881"}
	default:
		return nil
	}
}

// reusablePaymentMethodLocked resolves both registered and seeded simulator
// cards. A revoked record deliberately shadows its seed so the provider keeps
// the audit data without accepting the token for another payment.
func (s *Server) reusablePaymentMethodLocked(provider string, providerToken string) *PaymentMethodRegistration {
	found := false
	for _, candidate := range s.registrations {
		if candidate == nil || candidate.Provider != provider ||
			!constantTimeEqual(candidate.ProviderToken, providerToken) {
			continue
		}
		found = true
		if candidate.Status == "completed" {
			return clonePaymentMethodRegistration(candidate, true)
		}
	}
	if found {
		return nil
	}
	return simulatorSeedPaymentMethod(provider, providerToken)
}

func decodeStrictJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) == nil {
		return errors.New("multiple JSON values")
	}
	return nil
}

func digitsOnly(value string) string {
	var result strings.Builder
	for _, character := range value {
		if character >= '0' && character <= '9' {
			result.WriteRune(character)
		}
	}
	return result.String()
}

func normalizeCardholderName(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func validFutureExpiry(month int, year int, now time.Time) bool {
	if month < 1 || month > 12 || year < now.Year() || year > now.Year()+20 {
		return false
	}
	return year > now.Year() || month >= int(now.Month())
}

func registrationDisplayExpiry(now time.Time) string {
	return strconv.Itoa(now.UTC().Year() + 3)
}
