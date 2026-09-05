package simulator

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

func (s *Server) barionRoutes() {
	s.mux.HandleFunc("POST /v2/Payment/Start", s.startBarionPayment)
	s.mux.HandleFunc("GET /v4/Payment/{paymentID}/PaymentState", s.getBarionPaymentState)
	s.mux.HandleFunc("GET /v4/payment/{paymentID}/paymentstate", s.getBarionPaymentState)
	s.mux.HandleFunc("POST /v2/Payment/Capture", s.captureBarionPayment)
	s.mux.HandleFunc("POST /v2/Payment/CancelAuthorization", s.cancelBarionAuthorization)
	s.mux.HandleFunc("GET /barion/gateway/{paymentID}", s.barionGatewayPage)
	s.mux.HandleFunc("GET /barion/bank-auth/{paymentID}", s.barionBankAuthPage)
	s.mux.HandleFunc("POST /test/barion/payments/{paymentID}/{outcome}", s.applyBarionGatewayOutcome)
	s.mux.HandleFunc("POST /test/barion/bank-auth/{paymentID}/{outcome}", s.applyBarionBankOutcome)
	s.mux.HandleFunc("POST /test/barion/callbacks/{paymentID}/replay", s.replayBarionCallback)
}

func (s *Server) startBarionPayment(w http.ResponseWriter, r *http.Request) {
	var request barionStartRequest
	if err := decodeLimitedJSON(r, &request); err != nil {
		writeBarionError(w, http.StatusBadRequest, "InvalidRequest", "Invalid JSON request.")
		return
	}
	if !s.authorizeBarion(request.POSKey, r.Header.Get("x-pos-key")) {
		writeBarionError(w, http.StatusUnauthorized, "AuthenticationFailed", "Invalid test POS key.")
		return
	}
	request.POSKey = ""
	request.PaymentType = strings.TrimSpace(request.PaymentType)
	request.PaymentRequestID = strings.TrimSpace(request.PaymentRequestID)
	request.Currency = strings.ToUpper(strings.TrimSpace(request.Currency))
	request.Locale = firstNonBlank(request.Locale, "en-US")
	request.RecurrenceType = strings.TrimSpace(request.RecurrenceType)
	request.TraceID = strings.TrimSpace(request.TraceID)
	if request.PaymentType != "DelayedCapture" {
		writeBarionError(w, http.StatusBadRequest, "InvalidPaymentType", "Only DelayedCapture is supported by the MyScoutee hold contract.")
		return
	}
	if request.PaymentRequestID == "" || len(request.PaymentRequestID) > 100 {
		writeBarionError(w, http.StatusBadRequest, "InvalidPaymentRequestId", "PaymentRequestId is required and must not exceed 100 characters.")
		return
	}
	if !validAbsoluteHTTPURL(request.RedirectURL) || !validAbsoluteHTTPURL(request.CallbackURL) {
		writeBarionError(w, http.StatusBadRequest, "InvalidUrl", "RedirectUrl and CallbackUrl must be absolute HTTP(S) URLs.")
		return
	}
	delayedCapturePeriod, err := parseBarionDuration(request.DelayedCapturePeriod, 7*24*time.Hour)
	if err != nil || delayedCapturePeriod < time.Minute || delayedCapturePeriod > 21*24*time.Hour {
		writeBarionError(w, http.StatusBadRequest, "InvalidDelayedCapturePeriod", "DelayedCapturePeriod must be between one minute and 21 days.")
		return
	}
	paymentWindow, err := parseBarionDuration(request.PaymentWindow, 30*time.Minute)
	if err != nil || paymentWindow < time.Minute || paymentWindow > 7*24*time.Hour {
		writeBarionError(w, http.StatusBadRequest, "InvalidPaymentWindow", "PaymentWindow must be between one minute and seven days.")
		return
	}
	if len(request.Transactions) == 0 || request.Currency == "" {
		writeBarionError(w, http.StatusBadRequest, "InvalidTransactions", "Currency and at least one transaction are required.")
		return
	}
	var total float64
	for _, transaction := range request.Transactions {
		if strings.TrimSpace(transaction.POSTransactionID) == "" || !validMoney(transaction.Total) {
			writeBarionError(w, http.StatusBadRequest, "InvalidTransaction", "Each transaction requires a unique shop transaction ID and a non-negative finite total.")
			return
		}
		total += transaction.Total
	}
	if !validMoney(total) || total <= 0 {
		writeBarionError(w, http.StatusBadRequest, "InvalidTotal", "Payment total must be positive.")
		return
	}
	canonicalRequest, _ := json.Marshal(request)
	requestHash := sha256Hex(canonicalRequest)

	s.mu.Lock()
	var savedCard *PaymentMethodRegistration
	if request.TraceID != "" {
		if request.RecurrenceType != "OneClickPayment" {
			s.mu.Unlock()
			writeBarionError(w, http.StatusBadRequest, "InvalidRecurrence", "Saved-card TraceId requires RecurrenceType=OneClickPayment.")
			return
		}
		savedCard = s.reusablePaymentMethodLocked("barion", request.TraceID)
		if savedCard == nil {
			s.mu.Unlock()
			writeBarionError(w, http.StatusBadRequest, "InvalidTraceId", "The saved simulator payment method is unknown.")
			return
		}
	}
	if existing, found := s.barionRequestIndex[request.PaymentRequestID]; found {
		payment := cloneBarionPayment(s.barionPayments[existing.PaymentID])
		s.mu.Unlock()
		if existing.RequestHash != requestHash {
			writeBarionError(w, http.StatusConflict, "PaymentRequestIdAlreadyUsed", "PaymentRequestId was reused with different parameters.")
			return
		}
		writeJSON(w, http.StatusOK, barionStartResponseFor(payment))
		return
	}
	now := s.now().UTC()
	paymentID := randomGUID()
	controlToken := randomHex(24)
	gatewayURL := strings.TrimRight(s.config.PublicBaseURL, "/") + "/barion/gateway/" +
		url.PathEscape(paymentID) + "?token=" + url.QueryEscape(controlToken)
	transactions := make([]BarionPaymentTransaction, 0, len(request.Transactions))
	for _, item := range request.Transactions {
		transactions = append(transactions, BarionPaymentTransaction{
			TransactionID: randomGUID(), POSTransactionID: item.POSTransactionID,
			Payee: item.Payee, Total: item.Total, OriginalTotal: item.Total,
			Currency: request.Currency, Comment: item.Comment, Status: "Prepared",
			TransactionType: "CardPayment", Items: slices.Clone(item.Items),
		})
	}
	payment := &BarionPayment{
		PaymentID: paymentID, PaymentRequestID: request.PaymentRequestID,
		Status: "Prepared", PaymentType: "DelayedCapture",
		AllowedFundingSources: slices.Clone(request.FundingSources), PaymentMethod: "Unknown",
		GuestCheckout: request.GuestCheckout, CreatedAt: now.Format(time.RFC3339),
		ValidUntil: now.Add(paymentWindow).Format(time.RFC3339), Transactions: transactions,
		Total: total, Currency: request.Currency, SuggestedLocale: request.Locale,
		CallbackURL: appendURLQuery(request.CallbackURL, "paymentId", paymentID),
		RedirectURL: appendURLQuery(request.RedirectURL, "paymentId", paymentID),
		GatewayURL:  gatewayURL, ControlToken: controlToken, RequestHash: requestHash,
	}
	if savedCard != nil {
		payment.PaymentMethod = "BankCard"
		if s.configuration.Requires3DS {
			payment.Status = "InProgress"
			payment.LastOperation = "customer_action_required"
			payment.GatewayURL = strings.TrimRight(s.config.PublicBaseURL, "/") + "/barion/bank-auth/" +
				url.PathEscape(paymentID) + "?token=" + url.QueryEscape(controlToken)
			for index := range payment.Transactions {
				payment.Transactions[index].Status = "Started"
			}
		} else {
			authorizeBarionPayment(payment, now)
			payment.GatewayURL = ""
		}
	}
	s.barionPayments[paymentID] = payment
	s.barionRequestIndex[payment.PaymentRequestID] = barionRequestRecord{RequestHash: requestHash, PaymentID: paymentID}
	if err := s.persistLocked(); err != nil {
		delete(s.barionPayments, paymentID)
		delete(s.barionRequestIndex, payment.PaymentRequestID)
		s.mu.Unlock()
		writeBarionError(w, http.StatusInternalServerError, "InternalError", "Could not persist the payment.")
		return
	}
	result := cloneBarionPayment(payment)
	s.mu.Unlock()
	if result.Status == "Authorized" {
		s.deliverBarionCallback(result.PaymentID)
	}
	writeJSON(w, http.StatusOK, barionStartResponseFor(result))
}

func (s *Server) getBarionPaymentState(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeBarion("", r.Header.Get("x-pos-key")) {
		writeBarionError(w, http.StatusUnauthorized, "AuthenticationFailed", "Invalid test POS key.")
		return
	}
	s.mu.RLock()
	payment := cloneBarionPayment(s.barionPayments[r.PathValue("paymentID")])
	s.mu.RUnlock()
	if payment == nil {
		writeBarionError(w, http.StatusNotFound, "PaymentNotFound", "Payment was not found.")
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

func (s *Server) captureBarionPayment(w http.ResponseWriter, r *http.Request) {
	var request barionFinishRequest
	if err := decodeLimitedJSON(r, &request); err != nil {
		writeBarionError(w, http.StatusBadRequest, "InvalidRequest", "Invalid JSON request.")
		return
	}
	if !s.authorizeBarion(request.POSKey, r.Header.Get("x-pos-key")) {
		writeBarionError(w, http.StatusUnauthorized, "AuthenticationFailed", "Invalid test POS key.")
		return
	}
	s.mu.Lock()
	payment := s.barionPayments[request.PaymentID]
	if payment == nil {
		s.mu.Unlock()
		writeBarionError(w, http.StatusNotFound, "PaymentNotFound", "Payment was not found.")
		return
	}
	if payment.Status == "Succeeded" && payment.LastOperation == "capture" {
		result := cloneBarionPayment(payment)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, barionFinishResponseFor(result))
		return
	}
	if payment.Status != "Authorized" {
		s.mu.Unlock()
		writeBarionError(w, http.StatusConflict, "InvalidPaymentStatus", "Only an Authorized delayed-capture payment can be captured.")
		return
	}
	finishByID := make(map[string]barionTransactionToFinish, len(request.Transactions))
	for _, transaction := range request.Transactions {
		finishByID[transaction.TransactionID] = transaction
	}
	if len(finishByID) != len(payment.Transactions) {
		s.mu.Unlock()
		writeBarionError(w, http.StatusBadRequest, "InvalidTransactions", "All original transactions must be supplied exactly once.")
		return
	}
	previous := cloneBarionPayment(payment)
	var total float64
	for index := range payment.Transactions {
		finish, found := finishByID[payment.Transactions[index].TransactionID]
		if !found || !validMoney(finish.Total) || finish.Total > payment.Transactions[index].OriginalTotal {
			s.mu.Unlock()
			writeBarionError(w, http.StatusBadRequest, "InvalidTransaction", "Capture totals must be between zero and each authorized transaction total.")
			return
		}
		payment.Transactions[index].Total = finish.Total
		payment.Transactions[index].Status = "Succeeded"
		if strings.TrimSpace(finish.Comment) != "" {
			payment.Transactions[index].Comment = strings.TrimSpace(finish.Comment)
		}
		total += finish.Total
	}
	payment.Total = total
	payment.Status = "Succeeded"
	payment.CompletedAt = s.now().UTC().Format(time.RFC3339)
	payment.LastOperation = "capture"
	if err := s.persistLocked(); err != nil {
		*s.barionPayments[payment.PaymentID] = *previous
		s.mu.Unlock()
		writeBarionError(w, http.StatusInternalServerError, "InternalError", "Could not persist capture.")
		return
	}
	result := cloneBarionPayment(payment)
	s.mu.Unlock()
	s.deliverBarionCallback(result.PaymentID)
	writeJSON(w, http.StatusOK, barionFinishResponseFor(result))
}

func (s *Server) cancelBarionAuthorization(w http.ResponseWriter, r *http.Request) {
	var request barionFinishRequest
	if err := decodeLimitedJSON(r, &request); err != nil {
		writeBarionError(w, http.StatusBadRequest, "InvalidRequest", "Invalid JSON request.")
		return
	}
	if !s.authorizeBarion(request.POSKey, r.Header.Get("x-pos-key")) {
		writeBarionError(w, http.StatusUnauthorized, "AuthenticationFailed", "Invalid test POS key.")
		return
	}
	s.mu.Lock()
	payment := s.barionPayments[request.PaymentID]
	if payment == nil {
		s.mu.Unlock()
		writeBarionError(w, http.StatusNotFound, "PaymentNotFound", "Payment was not found.")
		return
	}
	if payment.Status == "Succeeded" && payment.LastOperation == "cancel_authorization" {
		result := cloneBarionPayment(payment)
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, barionFinishResponseFor(result))
		return
	}
	if payment.Status != "Authorized" {
		s.mu.Unlock()
		writeBarionError(w, http.StatusConflict, "InvalidPaymentStatus", "Only an Authorized delayed-capture payment can be canceled.")
		return
	}
	previous := cloneBarionPayment(payment)
	for index := range payment.Transactions {
		payment.Transactions[index].Total = 0
		payment.Transactions[index].Status = "Reversed"
	}
	payment.Total = 0
	payment.Status = "Succeeded"
	payment.CompletedAt = s.now().UTC().Format(time.RFC3339)
	payment.LastOperation = "cancel_authorization"
	if err := s.persistLocked(); err != nil {
		*s.barionPayments[payment.PaymentID] = *previous
		s.mu.Unlock()
		writeBarionError(w, http.StatusInternalServerError, "InternalError", "Could not persist authorization release.")
		return
	}
	result := cloneBarionPayment(payment)
	s.mu.Unlock()
	s.deliverBarionCallback(result.PaymentID)
	writeJSON(w, http.StatusOK, barionFinishResponseFor(result))
}

func (s *Server) barionGatewayPage(w http.ResponseWriter, r *http.Request) {
	payment := s.authorizedBarionBrowserPayment(r, "")
	if payment == nil {
		http.Error(w, "Barion test payment not found.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := barionGatewayTemplate.Execute(w, map[string]any{"Payment": payment, "Token": payment.ControlToken}); err != nil {
		log.Printf("render Barion gateway page: %v", err)
	}
}

func (s *Server) barionBankAuthPage(w http.ResponseWriter, r *http.Request) {
	payment := s.authorizedBarionBrowserPayment(r, "InProgress")
	if payment == nil {
		http.Error(w, "Barion bank authentication challenge not found.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := barionBankAuthTemplate.Execute(w, map[string]any{"Payment": payment, "Token": payment.ControlToken}); err != nil {
		log.Printf("render Barion bank authentication page: %v", err)
	}
}

func (s *Server) applyBarionGatewayOutcome(w http.ResponseWriter, r *http.Request) {
	s.transitionBarionBrowserPayment(w, r, false)
}

func (s *Server) applyBarionBankOutcome(w http.ResponseWriter, r *http.Request) {
	s.transitionBarionBrowserPayment(w, r, true)
}

func (s *Server) transitionBarionBrowserPayment(w http.ResponseWriter, r *http.Request, bankChallenge bool) {
	paymentID := r.PathValue("paymentID")
	outcome := r.PathValue("outcome")
	s.mu.Lock()
	payment := s.barionPayments[paymentID]
	if payment == nil || !constantTimeEqual(r.URL.Query().Get("token"), payment.ControlToken) {
		s.mu.Unlock()
		http.Error(w, "Barion test payment not found.", http.StatusNotFound)
		return
	}
	if bankChallenge && payment.Status != "InProgress" {
		s.mu.Unlock()
		http.Error(w, "Bank authentication challenge is no longer active.", http.StatusConflict)
		return
	}
	if !bankChallenge && payment.Status != "Prepared" && payment.Status != "Started" {
		s.mu.Unlock()
		http.Error(w, "Payment is no longer awaiting checkout.", http.StatusConflict)
		return
	}
	previous := cloneBarionPayment(payment)
	redirectURL := payment.RedirectURL
	switch outcome {
	case "authorize":
		if bankChallenge {
			s.mu.Unlock()
			http.Error(w, "Unsupported bank outcome.", http.StatusBadRequest)
			return
		}
		authorizeBarionPayment(payment, s.now().UTC())
	case "3ds", "require_action":
		if bankChallenge {
			s.mu.Unlock()
			http.Error(w, "Unsupported bank outcome.", http.StatusBadRequest)
			return
		}
		payment.Status = "InProgress"
		payment.PaymentMethod = "BankCard"
		payment.LastOperation = "customer_action_required"
		for index := range payment.Transactions {
			payment.Transactions[index].Status = "Started"
		}
		redirectURL = strings.TrimRight(s.config.PublicBaseURL, "/") + "/barion/bank-auth/" +
			url.PathEscape(payment.PaymentID) + "?token=" + url.QueryEscape(payment.ControlToken)
	case "approve":
		if !bankChallenge {
			s.mu.Unlock()
			http.Error(w, "Unsupported checkout outcome.", http.StatusBadRequest)
			return
		}
		authorizeBarionPayment(payment, s.now().UTC())
	case "decline", "cancel":
		payment.Status = "Canceled"
		payment.CompletedAt = s.now().UTC().Format(time.RFC3339)
		payment.LastOperation = "customer_action_" + outcome
		for index := range payment.Transactions {
			payment.Transactions[index].Status = "Rejected"
		}
	case "timeout", "expire":
		payment.Status = "Expired"
		payment.CompletedAt = s.now().UTC().Format(time.RFC3339)
		payment.LastOperation = "customer_action_timeout"
		for index := range payment.Transactions {
			payment.Transactions[index].Status = "Expired"
		}
	default:
		s.mu.Unlock()
		http.Error(w, "Unsupported Barion test outcome.", http.StatusBadRequest)
		return
	}
	if err := s.persistLocked(); err != nil {
		*s.barionPayments[paymentID] = *previous
		s.mu.Unlock()
		http.Error(w, "Could not persist Barion payment outcome.", http.StatusInternalServerError)
		return
	}
	result := cloneBarionPayment(payment)
	s.mu.Unlock()
	s.deliverBarionCallback(paymentID)
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"payment": result, "redirect": redirectURL})
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}
