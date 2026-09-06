package simulator

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultListenAddress = "127.0.0.1:18082"
	defaultAPIKey        = "sk_test_myscoutee"
	defaultWebhookSecret = "whsec_myscoutee_test"
	defaultBarionPOSKey  = "11111111-1111-4111-8111-111111111111"
)

type Config struct {
	ListenAddress      string
	PublicBaseURL      string
	DatabasePath       string
	APIKey             string
	WebhookURL         string
	WebhookSecret      string
	BarionPOSKey       string
	BarionCallbackURL  string
	AuditToken         string
	RequireIdempotency bool
	HTTPClient         *http.Client
	Now                func() time.Time
}

func ConfigFromEnvironment() (Config, error) {
	listenAddress := envOrDefault("PAYMENT_SIMULATOR_LISTEN_ADDR", defaultListenAddress)
	publicBaseURL := envOrDefault("PAYMENT_SIMULATOR_PUBLIC_BASE_URL", "http://"+listenAddress)
	config := Config{
		ListenAddress:      listenAddress,
		PublicBaseURL:      publicBaseURL,
		DatabasePath:       envOrDefault("PAYMENT_SIMULATOR_DATABASE_PATH", ".state/payment-simulator.db"),
		APIKey:             envOrDefault("PAYMENT_SIMULATOR_API_KEY", defaultAPIKey),
		WebhookURL:         strings.TrimSpace(os.Getenv("PAYMENT_SIMULATOR_WEBHOOK_URL")),
		WebhookSecret:      envOrDefault("PAYMENT_SIMULATOR_WEBHOOK_SECRET", defaultWebhookSecret),
		BarionPOSKey:       envOrDefault("PAYMENT_SIMULATOR_BARION_POS_KEY", defaultBarionPOSKey),
		BarionCallbackURL:  strings.TrimSpace(os.Getenv("PAYMENT_SIMULATOR_BARION_CALLBACK_URL")),
		AuditToken:         strings.TrimSpace(os.Getenv("PAYMENT_SIMULATOR_AUDIT_TOKEN")),
		RequireIdempotency: envOrDefault("PAYMENT_SIMULATOR_REQUIRE_IDEMPOTENCY", "true") != "false",
	}
	if err := validateConfig(config); err != nil {
		return Config{}, err
	}
	return config, nil
}

func validateConfig(config Config) error {
	host, _, err := net.SplitHostPort(config.ListenAddress)
	if err != nil {
		return fmt.Errorf("invalid PAYMENT_SIMULATOR_LISTEN_ADDR: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		if strings.ToLower(strings.TrimSpace(os.Getenv("PAYMENT_SIMULATOR_ALLOW_NON_LOOPBACK"))) != "true" {
			return errors.New("non-loopback listen address requires PAYMENT_SIMULATOR_ALLOW_NON_LOOPBACK=true")
		}
	}
	parsedBaseURL, err := url.Parse(config.PublicBaseURL)
	if err != nil || parsedBaseURL.Host == "" || (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") {
		return errors.New("PAYMENT_SIMULATOR_PUBLIC_BASE_URL must be an absolute HTTP(S) URL")
	}
	if !strings.HasPrefix(config.APIKey, "sk_test_") || strings.HasPrefix(config.APIKey, "sk_live_") {
		return errors.New("PAYMENT_SIMULATOR_API_KEY must be a test key beginning with sk_test_")
	}
	if !strings.HasPrefix(config.WebhookSecret, "whsec_") {
		return errors.New("PAYMENT_SIMULATOR_WEBHOOK_SECRET must begin with whsec_")
	}
	if !validGUID(config.BarionPOSKey) {
		return errors.New("PAYMENT_SIMULATOR_BARION_POS_KEY must be a GUID")
	}
	if config.WebhookURL != "" {
		webhookURL, err := url.Parse(config.WebhookURL)
		if err != nil || webhookURL.Host == "" || (webhookURL.Scheme != "http" && webhookURL.Scheme != "https") {
			return errors.New("PAYMENT_SIMULATOR_WEBHOOK_URL must be an absolute HTTP(S) URL")
		}
	}
	if config.BarionCallbackURL != "" {
		callbackURL, err := url.Parse(config.BarionCallbackURL)
		if err != nil || callbackURL.Host == "" || (callbackURL.Scheme != "http" && callbackURL.Scheme != "https") {
			return errors.New("PAYMENT_SIMULATOR_BARION_CALLBACK_URL must be an absolute HTTP(S) URL")
		}
	}
	if strings.TrimSpace(config.DatabasePath) == "" {
		return errors.New("PAYMENT_SIMULATOR_DATABASE_PATH must not be empty")
	}
	return nil
}

func New(config Config) (*Server, error) {
	if config.ListenAddress == "" {
		config.ListenAddress = defaultListenAddress
	}
	if config.PublicBaseURL == "" {
		config.PublicBaseURL = "http://" + config.ListenAddress
	}
	if config.APIKey == "" {
		config.APIKey = defaultAPIKey
	}
	if config.WebhookSecret == "" {
		config.WebhookSecret = defaultWebhookSecret
	}
	if config.BarionPOSKey == "" {
		config.BarionPOSKey = defaultBarionPOSKey
	}
	if config.DatabasePath == "" {
		config.DatabasePath = ".state/payment-simulator.db"
	}
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if err := os.MkdirAll(filepath.Dir(config.DatabasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create simulator database directory: %w", err)
	}
	db, err := sql.Open("sqlite", config.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("open simulator database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS simulator_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			payload BLOB NOT NULL,
			updated_at INTEGER NOT NULL
		)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize simulator database: %w", err)
	}
	server := &Server{
		config:                     config,
		now:                        now,
		client:                     client,
		db:                         db,
		mux:                        http.NewServeMux(),
		sessions:                   make(map[string]*CheckoutSession),
		intents:                    make(map[string]*PaymentIntent),
		idempotency:                make(map[string]idempotencyRecord),
		events:                     make(map[string]*WebhookEvent),
		barionPayments:             make(map[string]*BarionPayment),
		barionRequestIndex:         make(map[string]barionRequestRecord),
		registrations:              make(map[string]*PaymentMethodRegistration),
		configuration:              SimulatorConfiguration{Provider: "none", Requires3DS: false},
		configurationAccessTickets: make(map[string]time.Time),
		configurationSessions:      make(map[string]time.Time),
	}
	if err := server.loadState(); err != nil {
		_ = db.Close()
		return nil, err
	}
	server.routes()
	return server, nil
}

func (s *Server) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/register/") ||
		strings.HasPrefix(r.URL.Path, "/configuration-access/") ||
		strings.HasPrefix(r.URL.Path, "/authorization-access/") ||
		strings.HasPrefix(r.URL.Path, "/payment-method-registration-auth/") ||
		strings.HasPrefix(r.URL.Path, "/payment-wait/") ||
		strings.HasPrefix(r.URL.Path, "/simulator-ui/") {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; form-action 'self'; base-uri 'none'; frame-ancestors 'self' http://localhost:* http://127.0.0.1:*")
	} else {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.configurationPage)
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /v1/checkout/sessions", s.createSession)
	s.mux.HandleFunc("POST /v1/payment_intents", s.createPaymentIntent)
	s.mux.HandleFunc("GET /v1/checkout/sessions/{sessionID}", s.retrieveSession)
	s.mux.HandleFunc("GET /v1/payment_intents/{intentID}", s.retrievePaymentIntent)
	s.mux.HandleFunc("POST /v1/payment_intents/{intentID}/capture", s.capturePaymentIntent)
	s.mux.HandleFunc("POST /v1/payment_intents/{intentID}/cancel", s.cancelPaymentIntent)
	s.mux.HandleFunc("GET /checkout/{sessionID}", s.checkoutPage)
	s.mux.HandleFunc("GET /bank-auth/{sessionID}", s.bankAuthPage)
	s.mux.HandleFunc("GET /payment-wait/stripe/{sessionID}", s.stripePaymentWaitPage)
	s.mux.HandleFunc("POST /test/sessions/{sessionID}/{outcome}", s.applyOutcome)
	s.mux.HandleFunc("POST /test/bank-auth/{sessionID}/{outcome}", s.applyBankOutcome)
	s.mux.HandleFunc("POST /test/events/{eventID}/replay", s.replayEvent)
	s.mux.HandleFunc("GET /test/audit", s.audit)
	s.configurationRoutes()
	s.authorizationRoutes()
	s.paymentMethodRegistrationRoutes()
	s.barionRoutes()
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "myscoutee-payment-simulator"})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStripeProvider(r) {
		writeStripeError(w, http.StatusUnauthorized, "authentication_error", "Invalid test API key.")
		return
	}
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk_live_") {
		writeStripeError(w, http.StatusForbidden, "authentication_error", "Live keys are never accepted by this simulator.")
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
	requestHash := sha256Hex([]byte(r.Form.Encode()))
	if idempotencyKey != "" {
		s.mu.RLock()
		existing, found := s.idempotency[idempotencyKey]
		var session *CheckoutSession
		if found {
			session = cloneSession(s.sessions[existing.SessionID])
		}
		s.mu.RUnlock()
		if found {
			if existing.RequestHash != requestHash {
				writeStripeError(w, http.StatusConflict, "idempotency_error", "The same idempotency key was used with different parameters.")
				return
			}
			writeJSON(w, http.StatusOK, session)
			return
		}
	}

	mode := strings.TrimSpace(r.Form.Get("mode"))
	if mode != "payment" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Only mode=payment is supported.")
		return
	}
	if strings.TrimSpace(r.Form.Get("payment_intent_data[capture_method]")) != "manual" {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "payment_intent_data[capture_method]=manual is required by the MyScoutee hold contract.")
		return
	}
	successURL := strings.TrimSpace(r.Form.Get("success_url"))
	cancelURL := strings.TrimSpace(r.Form.Get("cancel_url"))
	if !validAbsoluteHTTPURL(successURL) || !validAbsoluteHTTPURL(cancelURL) {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "success_url and cancel_url must be absolute HTTP(S) URLs.")
		return
	}
	amountTotal, currency, lineItems, err := parseLineItems(r.Form)
	if err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	metadata := parseBracketMap(r.Form, "metadata")
	now := s.now().UTC()
	sessionID := "cs_test_" + randomHex(12)
	intentID := "pi_test_" + randomHex(12)
	controlToken := randomHex(24)
	checkoutURL := strings.TrimRight(s.config.PublicBaseURL, "/") + "/checkout/" + url.PathEscape(sessionID) + "?token=" + url.QueryEscape(controlToken)
	session := &CheckoutSession{
		ID:                sessionID,
		Object:            "checkout.session",
		URL:               checkoutURL,
		Status:            "open",
		PaymentStatus:     "unpaid",
		Mode:              "payment",
		AmountTotal:       amountTotal,
		Currency:          currency,
		SuccessURL:        successURL,
		CancelURL:         cancelURL,
		ClientReferenceID: strings.TrimSpace(r.Form.Get("client_reference_id")),
		Metadata:          metadata,
		Created:           now.Unix(),
		Livemode:          false,
		PaymentIntent:     intentID,
		LineItems:         lineItems,
		ControlToken:      controlToken,
		IdempotencyKey:    idempotencyKey,
	}
	intent := &PaymentIntent{
		ID:                intentID,
		Object:            "payment_intent",
		Amount:            amountTotal,
		Currency:          currency,
		Status:            "requires_payment_method",
		CaptureMethod:     "manual",
		ClientReferenceID: session.ClientReferenceID,
		Metadata:          cloneMap(metadata),
		Created:           now.Unix(),
		Livemode:          false,
		IdempotencyKey:    idempotencyKey,
	}

	s.mu.Lock()
	if idempotencyKey != "" {
		if existing, found := s.idempotency[idempotencyKey]; found {
			s.mu.Unlock()
			if existing.RequestHash != requestHash {
				writeStripeError(w, http.StatusConflict, "idempotency_error", "The same idempotency key was used with different parameters.")
				return
			}
			s.mu.RLock()
			existingSession := cloneSession(s.sessions[existing.SessionID])
			s.mu.RUnlock()
			writeJSON(w, http.StatusOK, existingSession)
			return
		}
	}
	s.sessions[sessionID] = session
	s.intents[intentID] = intent
	if idempotencyKey != "" {
		s.idempotency[idempotencyKey] = idempotencyRecord{RequestHash: requestHash, SessionID: sessionID}
	}
	if err := s.persistLocked(); err != nil {
		delete(s.sessions, sessionID)
		delete(s.intents, intentID)
		delete(s.idempotency, idempotencyKey)
		s.mu.Unlock()
		writeStripeError(w, http.StatusInternalServerError, "api_error", "Could not persist checkout session.")
		return
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) createPaymentIntent(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStripeProvider(r) || strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk_live_") {
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
	requestHash := sha256Hex([]byte(r.Form.Encode()))
	if idempotencyKey != "" {
		s.mu.RLock()
		existing, found := s.idempotency[idempotencyKey]
		var intent *PaymentIntent
		if found {
			if session := s.sessions[existing.SessionID]; session != nil {
				intent = clonePaymentIntent(s.intents[session.PaymentIntent])
			}
		}
		s.mu.RUnlock()
		if found {
			if existing.RequestHash != requestHash {
				writeStripeError(w, http.StatusConflict, "idempotency_error", "The same idempotency key was used with different parameters.")
				return
			}
			writeJSON(w, http.StatusOK, intent)
			return
		}
	}
	amount, err := strconv.ParseInt(strings.TrimSpace(r.Form.Get("amount")), 10, 64)
	currency := strings.ToLower(strings.TrimSpace(r.Form.Get("currency")))
	paymentMethod := strings.TrimSpace(r.Form.Get("payment_method"))
	returnURL := strings.TrimSpace(r.Form.Get("return_url"))
	if err != nil || amount <= 0 || currency == "" || paymentMethod == "" ||
		strings.TrimSpace(r.Form.Get("capture_method")) != "manual" ||
		strings.TrimSpace(r.Form.Get("confirm")) != "true" || !validAbsoluteHTTPURL(returnURL) {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "A positive amount, currency, saved payment_method, manual capture, confirm=true and return_url are required.")
		return
	}

	s.mu.RLock()
	requires3DS := s.configuration.Requires3DS
	registration := s.reusablePaymentMethodLocked("stripe", paymentMethod)
	s.mu.RUnlock()
	if registration == nil {
		writeStripeError(w, http.StatusBadRequest, "card_error", "The saved simulator payment method is unknown.")
		return
	}

	now := s.now().UTC()
	intentID := "pi_test_" + randomHex(12)
	sessionID := "cs_test_" + randomHex(12)
	controlToken := randomHex(24)
	metadata := parseBracketMap(r.Form, "metadata")
	status := "requires_capture"
	sessionStatus := "complete"
	var nextAction *PaymentIntentNextAction
	if requires3DS {
		status = "requires_action"
		sessionStatus = "open"
		waitingURL := strings.TrimRight(s.config.PublicBaseURL, "/") + "/payment-wait/stripe/" +
			url.PathEscape(sessionID) + "?token=" + url.QueryEscape(controlToken)
		nextAction = &PaymentIntentNextAction{
			Type:          "redirect_to_url",
			RedirectToURL: PaymentIntentRedirectToURL{URL: waitingURL, ReturnURL: returnURL},
		}
	}
	intent := &PaymentIntent{
		ID: intentID, Object: "payment_intent", Amount: amount, Currency: currency,
		Status: status, CaptureMethod: "manual", PaymentMethod: paymentMethod,
		ClientReferenceID: metadata["checkout_session_id"], Metadata: metadata,
		Created: now.Unix(), Livemode: false, IdempotencyKey: idempotencyKey,
		NextAction: nextAction,
	}
	if status == "requires_capture" {
		intent.AmountCapturable = amount
		intent.CaptureBefore = now.Add(7 * 24 * time.Hour).Unix()
	}
	session := &CheckoutSession{
		ID: sessionID, Object: "checkout.session", Status: sessionStatus,
		PaymentStatus: "unpaid", Mode: "payment", AmountTotal: amount, Currency: currency,
		SuccessURL: returnURL, CancelURL: returnURL, ClientReferenceID: metadata["checkout_session_id"],
		Metadata: cloneMap(metadata), Created: now.Unix(), PaymentIntent: intentID,
		ControlToken: controlToken, IdempotencyKey: idempotencyKey,
	}

	s.mu.Lock()
	s.sessions[sessionID] = session
	s.intents[intentID] = intent
	if idempotencyKey != "" {
		s.idempotency[idempotencyKey] = idempotencyRecord{RequestHash: requestHash, SessionID: sessionID}
	}
	var event *WebhookEvent
	if status == "requires_capture" {
		event = s.newEventLocked("payment_intent.amount_capturable_updated", intent, idempotencyKey)
	}
	if err := s.persistLocked(); err != nil {
		delete(s.sessions, sessionID)
		delete(s.intents, intentID)
		delete(s.idempotency, idempotencyKey)
		if event != nil {
			delete(s.events, event.ID)
			s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
		}
		s.mu.Unlock()
		writeStripeError(w, http.StatusInternalServerError, "api_error", "Could not persist payment intent.")
		return
	}
	result := clonePaymentIntent(intent)
	s.mu.Unlock()
	if event != nil {
		s.deliver(event)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) retrieveSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStripeProvider(r) {
		writeStripeError(w, http.StatusUnauthorized, "authentication_error", "Invalid test API key.")
		return
	}
	s.mu.RLock()
	session := cloneSession(s.sessions[r.PathValue("sessionID")])
	s.mu.RUnlock()
	if session == nil {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "No such checkout session.")
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) retrievePaymentIntent(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeStripeProvider(r) {
		writeStripeError(w, http.StatusUnauthorized, "authentication_error", "Invalid test API key.")
		return
	}
	s.mu.RLock()
	intent := clonePaymentIntent(s.intents[r.PathValue("intentID")])
	s.mu.RUnlock()
	if intent == nil {
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "No such payment intent.")
		return
	}
	writeJSON(w, http.StatusOK, intent)
}

func (s *Server) capturePaymentIntent(w http.ResponseWriter, r *http.Request) {
	s.mutatePaymentIntent(w, r, "capture")
}

func (s *Server) cancelPaymentIntent(w http.ResponseWriter, r *http.Request) {
	s.mutatePaymentIntent(w, r, "cancel")
}

func (s *Server) mutatePaymentIntent(w http.ResponseWriter, r *http.Request, operation string) {
	if !s.authorizeStripeProvider(r) {
		writeStripeError(w, http.StatusUnauthorized, "authentication_error", "Invalid test API key.")
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if s.config.RequireIdempotency && idempotencyKey == "" {
		writeStripeError(w, http.StatusBadRequest, "idempotency_error", "Idempotency-Key is required by the MyScoutee QA contract.")
		return
	}
	if err := r.ParseForm(); err != nil {
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Invalid form body.")
		return
	}
	requestHash := sha256Hex([]byte(r.Form.Encode()))
	operationKey := operation + ":" + idempotencyKey
	intentID := r.PathValue("intentID")

	s.mu.Lock()
	if previous, found := s.idempotency[operationKey]; found {
		intent := clonePaymentIntent(s.intents[previous.SessionID])
		s.mu.Unlock()
		if previous.RequestHash != requestHash || previous.SessionID != intentID {
			writeStripeError(w, http.StatusConflict, "idempotency_error", "The same idempotency key was used with different parameters.")
			return
		}
		writeJSON(w, http.StatusOK, intent)
		return
	}
	intent := s.intents[intentID]
	if intent == nil {
		s.mu.Unlock()
		writeStripeError(w, http.StatusNotFound, "invalid_request_error", "No such payment intent.")
		return
	}
	previousIntent := *clonePaymentIntent(intent)
	var eventType string
	switch operation {
	case "capture":
		if intent.Status != "requires_capture" {
			s.mu.Unlock()
			writeStripeError(w, http.StatusConflict, "invalid_request_error", "Only a requires_capture payment intent can be captured.")
			return
		}
		if intent.CaptureBefore > 0 && s.now().UTC().Unix() >= intent.CaptureBefore {
			intent.Status = "canceled"
			intent.CancellationReason = "abandoned"
			intent.AmountCapturable = 0
			eventType = "payment_intent.canceled"
		} else {
			intent.Status = "succeeded"
			intent.AmountReceived = intent.Amount
			intent.AmountCapturable = 0
			intent.NextAction = nil
			intent.LastPaymentError = nil
			eventType = "payment_intent.succeeded"
		}
	case "cancel":
		if intent.Status != "requires_capture" && intent.Status != "requires_payment_method" {
			s.mu.Unlock()
			writeStripeError(w, http.StatusConflict, "invalid_request_error", "This payment intent can no longer be canceled.")
			return
		}
		intent.Status = "canceled"
		intent.CancellationReason = firstNonBlank(r.Form.Get("cancellation_reason"), "requested_by_customer")
		intent.AmountCapturable = 0
		intent.NextAction = nil
		eventType = "payment_intent.canceled"
	default:
		s.mu.Unlock()
		writeStripeError(w, http.StatusBadRequest, "invalid_request_error", "Unsupported payment intent operation.")
		return
	}
	s.idempotency[operationKey] = idempotencyRecord{RequestHash: requestHash, SessionID: intent.ID}
	event := s.newEventLocked(eventType, intent, idempotencyKey)
	if err := s.persistLocked(); err != nil {
		*s.intents[intentID] = previousIntent
		delete(s.idempotency, operationKey)
		delete(s.events, event.ID)
		s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
		s.mu.Unlock()
		writeStripeError(w, http.StatusInternalServerError, "api_error", "Could not persist payment intent transition.")
		return
	}
	result := clonePaymentIntent(intent)
	s.mu.Unlock()

	s.deliver(event)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) checkoutPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	session := cloneSession(s.sessions[r.PathValue("sessionID")])
	s.mu.RUnlock()
	if session == nil || !constantTimeEqual(r.URL.Query().Get("token"), session.ControlToken) {
		http.Error(w, "Checkout session not found.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := checkoutTemplate.Execute(w, map[string]any{
		"Session": session,
		"Token":   session.ControlToken,
	}); err != nil {
		log.Printf("render checkout page: %v", err)
	}
}

func (s *Server) bankAuthPage(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	session := cloneSession(s.sessions[r.PathValue("sessionID")])
	var intent *PaymentIntent
	if session != nil {
		intent = clonePaymentIntent(s.intents[session.PaymentIntent])
	}
	s.mu.RUnlock()
	if session == nil || intent == nil || intent.Status != "requires_action" ||
		!constantTimeEqual(r.URL.Query().Get("token"), session.ControlToken) {
		http.Error(w, "Bank authentication challenge not found.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := bankAuthTemplate.Execute(w, map[string]any{
		"Session": session,
		"Intent":  intent,
		"Token":   session.ControlToken,
	}); err != nil {
		log.Printf("render bank authentication page: %v", err)
	}
}

func (s *Server) applyOutcome(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	outcome := r.PathValue("outcome")
	s.mu.Lock()
	session := s.sessions[sessionID]
	if session == nil || !constantTimeEqual(r.URL.Query().Get("token"), session.ControlToken) {
		s.mu.Unlock()
		http.Error(w, "Checkout session not found.", http.StatusNotFound)
		return
	}
	if session.Status != "open" {
		s.mu.Unlock()
		http.Error(w, "Checkout session already has a terminal outcome.", http.StatusConflict)
		return
	}
	intent := s.intents[session.PaymentIntent]
	if intent == nil {
		s.mu.Unlock()
		http.Error(w, "Payment intent not found.", http.StatusInternalServerError)
		return
	}
	previousSession := *cloneSession(session)
	previousIntent := *clonePaymentIntent(intent)
	var eventType string
	var redirectURL string
	switch outcome {
	case "complete", "authorize":
		session.Status = "complete"
		session.PaymentStatus = "unpaid"
		intent.Status = "requires_capture"
		intent.AmountCapturable = intent.Amount
		intent.AmountReceived = 0
		intent.CaptureBefore = s.now().UTC().Add(7 * 24 * time.Hour).Unix()
		intent.NextAction = nil
		intent.LastPaymentError = nil
		eventType = "checkout.session.completed"
		redirectURL = replaceSessionPlaceholder(session.SuccessURL, session.ID)
	case "require_action", "3ds":
		bankURL := strings.TrimRight(s.config.PublicBaseURL, "/") + "/bank-auth/" +
			url.PathEscape(session.ID) + "?token=" + url.QueryEscape(session.ControlToken)
		intent.Status = "requires_action"
		intent.AmountCapturable = 0
		intent.NextAction = &PaymentIntentNextAction{
			Type: "redirect_to_url",
			RedirectToURL: PaymentIntentRedirectToURL{
				URL:       bankURL,
				ReturnURL: replaceSessionPlaceholder(session.SuccessURL, session.ID),
			},
		}
		intent.LastPaymentError = nil
		eventType = "payment_intent.requires_action"
		redirectURL = bankURL
	case "fail":
		session.PaymentStatus = "unpaid"
		intent.Status = "requires_payment_method"
		intent.AmountCapturable = 0
		intent.NextAction = nil
		intent.LastPaymentError = &PaymentIntentError{
			Code: "card_declined", Message: "The simulated card was declined.", Type: "card_error"}
		eventType = "payment_intent.payment_failed"
		redirectURL = session.CancelURL
	case "expire":
		session.Status = "expired"
		session.PaymentStatus = "unpaid"
		intent.Status = "canceled"
		intent.CancellationReason = "abandoned"
		intent.AmountCapturable = 0
		intent.NextAction = nil
		eventType = "checkout.session.expired"
		redirectURL = session.CancelURL
	case "cancel":
		s.mu.Unlock()
		http.Redirect(w, r, session.CancelURL, http.StatusSeeOther)
		return
	default:
		s.mu.Unlock()
		http.Error(w, "Unsupported outcome.", http.StatusBadRequest)
		return
	}
	event := s.newEventLocked(eventType, eventObject(eventType, session, intent), session.IdempotencyKey)
	var authorizationEvent *WebhookEvent
	if outcome == "complete" || outcome == "authorize" {
		authorizationEvent = s.newEventLocked(
			"payment_intent.amount_capturable_updated",
			intent,
			session.IdempotencyKey)
	}
	if err := s.persistLocked(); err != nil {
		*s.sessions[sessionID] = previousSession
		*s.intents[intent.ID] = previousIntent
		if authorizationEvent != nil {
			delete(s.events, authorizationEvent.ID)
			s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
		}
		delete(s.events, event.ID)
		s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
		s.mu.Unlock()
		http.Error(w, "Could not persist checkout outcome.", http.StatusInternalServerError)
		return
	}
	s.mu.Unlock()

	s.deliver(event)
	if authorizationEvent != nil {
		s.deliver(authorizationEvent)
	}
	if wantsJSON(r) {
		eventIDs := []string{event.ID}
		if authorizationEvent != nil {
			eventIDs = append(eventIDs, authorizationEvent.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session":        cloneSession(session),
			"payment_intent": clonePaymentIntent(intent),
			"event_id":       event.ID,
			"event_ids":      eventIDs,
			"redirect":       redirectURL,
		})
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func (s *Server) applyBankOutcome(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("sessionID")
	outcome := r.PathValue("outcome")
	s.mu.Lock()
	session := s.sessions[sessionID]
	if session == nil || !constantTimeEqual(r.URL.Query().Get("token"), session.ControlToken) {
		s.mu.Unlock()
		http.Error(w, "Bank authentication challenge not found.", http.StatusNotFound)
		return
	}
	intent := s.intents[session.PaymentIntent]
	if intent == nil || intent.Status != "requires_action" || session.Status != "open" {
		s.mu.Unlock()
		http.Error(w, "Bank authentication challenge is no longer active.", http.StatusConflict)
		return
	}
	previousSession := *cloneSession(session)
	previousIntent := *clonePaymentIntent(intent)
	if stripeAuthorizationTimedOut(intent, s.now().UTC()) {
		session.Status = "expired"
		session.PaymentStatus = "unpaid"
		intent.Status = "canceled"
		intent.CancellationReason = "abandoned"
		intent.NextAction = nil
		event := s.newEventLocked("payment_intent.canceled", intent, session.IdempotencyKey)
		if err := s.persistLocked(); err != nil {
			*session = previousSession
			*intent = previousIntent
			delete(s.events, event.ID)
			s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
			s.mu.Unlock()
			http.Error(w, "Could not persist bank authentication timeout.", http.StatusInternalServerError)
			return
		}
		s.mu.Unlock()
		s.deliver(event)
		http.Error(w, "The three-minute bank authentication window has expired.", http.StatusGone)
		return
	}
	var events []*WebhookEvent
	var redirectURL string
	switch outcome {
	case "approve":
		session.Status = "complete"
		session.PaymentStatus = "unpaid"
		intent.Status = "requires_capture"
		intent.AmountCapturable = intent.Amount
		intent.AmountReceived = 0
		intent.CaptureBefore = s.now().UTC().Add(7 * 24 * time.Hour).Unix()
		intent.NextAction = nil
		intent.LastPaymentError = nil
		events = append(events,
			s.newEventLocked("checkout.session.completed", session, session.IdempotencyKey),
			s.newEventLocked("payment_intent.amount_capturable_updated", intent, session.IdempotencyKey))
		redirectURL = replaceSessionPlaceholder(session.SuccessURL, session.ID)
	case "decline", "cancel", "timeout":
		code := map[string]string{
			"decline": "authentication_failed",
			"cancel":  "authentication_canceled",
			"timeout": "authentication_timeout",
		}[outcome]
		intent.Status = "requires_payment_method"
		intent.AmountCapturable = 0
		intent.NextAction = nil
		intent.LastPaymentError = &PaymentIntentError{
			Code: code, Message: "The simulated bank authentication did not complete.", Type: "card_error"}
		events = append(events, s.newEventLocked("payment_intent.payment_failed", intent, session.IdempotencyKey))
		if outcome == "cancel" {
			redirectURL = session.CancelURL
		} else {
			redirectURL = strings.TrimRight(s.config.PublicBaseURL, "/") + "/checkout/" +
				url.PathEscape(session.ID) + "?token=" + url.QueryEscape(session.ControlToken)
		}
	default:
		s.mu.Unlock()
		http.Error(w, "Unsupported bank authentication outcome.", http.StatusBadRequest)
		return
	}
	if err := s.persistLocked(); err != nil {
		*s.sessions[sessionID] = previousSession
		*s.intents[intent.ID] = previousIntent
		for index := len(events) - 1; index >= 0; index-- {
			delete(s.events, events[index].ID)
			s.eventOrder = s.eventOrder[:len(s.eventOrder)-1]
		}
		s.mu.Unlock()
		http.Error(w, "Could not persist bank authentication outcome.", http.StatusInternalServerError)
		return
	}
	resultSession := cloneSession(session)
	resultIntent := clonePaymentIntent(intent)
	s.mu.Unlock()
	for _, event := range events {
		s.deliver(event)
	}
	if wantsJSON(r) {
		eventIDs := make([]string, 0, len(events))
		for _, event := range events {
			eventIDs = append(eventIDs, event.ID)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"session": resultSession, "payment_intent": resultIntent,
			"event_ids": eventIDs, "redirect": redirectURL,
		})
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

func (s *Server) replayEvent(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAudit(r) {
		http.Error(w, "Audit token required.", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	event := s.events[r.PathValue("eventID")]
	s.mu.RUnlock()
	if event == nil {
		http.Error(w, "Webhook event not found.", http.StatusNotFound)
		return
	}
	s.deliver(event)
	writeJSON(w, http.StatusOK, map[string]any{"event_id": event.ID, "delivery": s.deliverySnapshot(event.ID)})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAudit(r) {
		http.Error(w, "Audit token required.", http.StatusUnauthorized)
		return
	}
	s.mu.RLock()
	sessionIDs := make([]string, 0, len(s.sessions))
	for id := range s.sessions {
		sessionIDs = append(sessionIDs, id)
	}
	slices.Sort(sessionIDs)
	response := auditResponse{
		Sessions:       make([]CheckoutSessionAudit, 0, len(sessionIDs)),
		PaymentIntents: make([]PaymentIntentAudit, 0, len(s.intents)),
		Events:         make([]WebhookEventAudit, 0, len(s.eventOrder)),
		BarionPayments: make([]BarionPaymentAudit, 0, len(s.barionPayments)),
	}
	for _, id := range sessionIDs {
		session := s.sessions[id]
		response.Sessions = append(response.Sessions, CheckoutSessionAudit{
			ID:                session.ID,
			Status:            session.Status,
			PaymentStatus:     session.PaymentStatus,
			AmountTotal:       session.AmountTotal,
			Currency:          session.Currency,
			ClientReferenceID: session.ClientReferenceID,
			Metadata:          cloneMap(session.Metadata),
			Created:           session.Created,
			PaymentIntent:     session.PaymentIntent,
			LineItems:         append([]CheckoutLineItem(nil), session.LineItems...),
		})
	}
	intentIDs := make([]string, 0, len(s.intents))
	for id := range s.intents {
		intentIDs = append(intentIDs, id)
	}
	slices.Sort(intentIDs)
	for _, id := range intentIDs {
		intent := s.intents[id]
		response.PaymentIntents = append(response.PaymentIntents, PaymentIntentAudit{
			ID:                 intent.ID,
			Status:             intent.Status,
			Amount:             intent.Amount,
			AmountCapturable:   intent.AmountCapturable,
			AmountReceived:     intent.AmountReceived,
			Currency:           intent.Currency,
			CaptureBefore:      intent.CaptureBefore,
			CancellationReason: intent.CancellationReason,
			ClientReferenceID:  intent.ClientReferenceID,
			Metadata:           cloneMap(intent.Metadata),
			Created:            intent.Created,
		})
	}
	for _, id := range s.eventOrder {
		event := s.events[id]
		response.Events = append(response.Events, WebhookEventAudit{
			ID:       event.ID,
			Type:     event.Type,
			ObjectID: eventObjectID(event.Data.Object),
			Created:  event.Created,
			Delivery: event.Delivery,
		})
	}
	response.BarionPayments = s.barionPaymentAuditsLocked()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) authorizeAudit(r *http.Request) bool {
	return s.config.AuditToken != "" && constantTimeEqual(r.Header.Get("X-Test-Audit-Token"), s.config.AuditToken)
}

func (s *Server) newEventLocked(eventType string, object any, idempotencyKey string) *WebhookEvent {
	objectJSON, err := json.Marshal(object)
	if err != nil {
		panic(fmt.Sprintf("marshal webhook object: %v", err))
	}
	event := &WebhookEvent{
		ID:              "evt_test_" + randomHex(12),
		Object:          "event",
		APIVersion:      "2023-10-16",
		Created:         s.now().UTC().Unix(),
		Data:            WebhookEventData{Object: objectJSON},
		Livemode:        false,
		PendingWebhooks: 1,
		Request: WebhookRequest{
			ID:             "req_test_" + randomHex(10),
			IdempotencyKey: idempotencyKey,
		},
		Type: eventType,
	}
	s.events[event.ID] = event
	s.eventOrder = append(s.eventOrder, event.ID)
	return event
}

func (s *Server) deliver(event *WebhookEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		s.recordDelivery(event.ID, 0, err)
		return
	}
	if s.config.WebhookURL == "" {
		s.recordDelivery(event.ID, 0, errors.New("webhook URL is not configured"))
		return
	}
	timestamp := s.now().UTC().Unix()
	signature := signPayload(s.config.WebhookSecret, timestamp, payload)
	req, err := http.NewRequest(http.MethodPost, s.config.WebhookURL, strings.NewReader(string(payload)))
	if err != nil {
		s.recordDelivery(event.ID, 0, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", fmt.Sprintf("t=%d,v1=%s", timestamp, signature))
	req.Header.Set("X-App-Session-Kind", "demo")
	response, err := s.client.Do(req)
	if err != nil {
		s.recordDelivery(event.ID, 0, err)
		return
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.recordDelivery(event.ID, response.StatusCode, fmt.Errorf("webhook returned HTTP %d", response.StatusCode))
		return
	}
	s.recordDelivery(event.ID, response.StatusCode, nil)
}

func (s *Server) recordDelivery(eventID string, statusCode int, deliveryErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event := s.events[eventID]
	if event == nil {
		return
	}
	event.Delivery.Attempts++
	event.Delivery.LastAttemptAt = s.now().UTC().Unix()
	event.Delivery.LastStatusCode = statusCode
	if deliveryErr == nil {
		event.Delivery.LastError = ""
		event.PendingWebhooks = 0
	} else {
		event.Delivery.LastError = deliveryErr.Error()
		event.PendingWebhooks = 1
	}
	if err := s.persistLocked(); err != nil {
		log.Printf("persist webhook delivery audit: %v", err)
	}
}
