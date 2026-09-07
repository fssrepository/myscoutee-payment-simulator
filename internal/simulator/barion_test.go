package simulator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBarionDelayedCaptureLifecycleAndCallbackStateRead(t *testing.T) {
	var callbackMu sync.Mutex
	var callbackPaymentIDs []string
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("callback method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse callback: %v", err)
		}
		callbackMu.Lock()
		callbackPaymentIDs = append(callbackPaymentIDs, r.Form.Get("PaymentId"))
		callbackMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()

	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	payment := startTestBarionPayment(t, server, callback.URL, "barion-request-001")
	if payment.Status != "Prepared" || payment.PaymentID == "" || payment.GatewayURL == "" {
		t.Fatalf("unexpected start response: %+v", payment)
	}

	authorized := applyTestBarionOutcome(t, server, payment, "authorize", false)
	if authorized.Status != "Authorized" || authorized.DelayedCaptureUntil == "" {
		t.Fatalf("payment was not authorized for delayed capture: %+v", authorized)
	}
	callbackMu.Lock()
	if len(callbackPaymentIDs) != 1 || callbackPaymentIDs[0] != payment.PaymentID {
		callbackMu.Unlock()
		t.Fatalf("unexpected callback identifiers: %v", callbackPaymentIDs)
	}
	callbackMu.Unlock()

	stateRequest := httptest.NewRequest(http.MethodGet,
		"/v4/Payment/"+url.PathEscape(payment.PaymentID)+"/PaymentState", nil)
	stateRequest.Header.Set("x-pos-key", defaultBarionPOSKey)
	stateResponse := httptest.NewRecorder()
	server.ServeHTTP(stateResponse, stateRequest)
	if stateResponse.Code != http.StatusOK {
		t.Fatalf("payment state returned %d: %s", stateResponse.Code, stateResponse.Body.String())
	}
	var state BarionPayment
	decodeJSON(t, stateResponse.Body.Bytes(), &state)
	if state.Status != "Authorized" || state.PaymentMethod != "BankCard" {
		t.Fatalf("unexpected payment state: %+v", state)
	}

	captureTransactions := make([]barionTransactionToFinish, 0, len(state.Transactions))
	for _, transaction := range state.Transactions {
		captureTransactions = append(captureTransactions, barionTransactionToFinish{
			TransactionID: transaction.TransactionID,
			Total:         transaction.Total,
		})
	}
	captureResponse := barionJSONRequest(t, server, "/v2/Payment/Capture", barionFinishRequest{
		POSKey: defaultBarionPOSKey, PaymentID: payment.PaymentID, Transactions: captureTransactions,
	})
	if captureResponse.Code != http.StatusOK {
		t.Fatalf("capture returned %d: %s", captureResponse.Code, captureResponse.Body.String())
	}
	var captured barionFinishResponse
	decodeJSON(t, captureResponse.Body.Bytes(), &captured)
	if !captured.IsSuccessful || captured.Status != "Succeeded" {
		t.Fatalf("unexpected capture response: %+v", captured)
	}

	callbackMu.Lock()
	if len(callbackPaymentIDs) != 2 {
		callbackMu.Unlock()
		t.Fatalf("authorization and capture should each callback, got %v", callbackPaymentIDs)
	}
	callbackMu.Unlock()
}

func TestBarionOptionalBankChallengeAndAuthorizationRelease(t *testing.T) {
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer callback.Close()
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	payment := startTestBarionPayment(t, server, callback.URL, "barion-3ds-001")
	requiresAction := applyTestBarionOutcome(t, server, payment, "3ds", false)
	if requiresAction.Status != "InProgress" {
		t.Fatalf("expected a bank challenge: %+v", requiresAction)
	}
	approveRequest := httptest.NewRequest(http.MethodPost,
		"/test/barion/bank-auth/"+url.PathEscape(payment.PaymentID)+"/approve?token="+
			url.QueryEscape(payment.ControlToken), nil)
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusSeeOther {
		t.Fatalf("bank approval returned %d: %s", approveResponse.Code, approveResponse.Body.String())
	}
	resultLocation := approveResponse.Header().Get("Location")
	wantedResultPrefix := "/barion/bank-auth/" + url.PathEscape(payment.PaymentID) + "?token="
	if !strings.HasPrefix(resultLocation, wantedResultPrefix) || strings.Contains(resultLocation, "/game") {
		t.Fatalf("bank approval escaped the simulator confirmation frame: %q", resultLocation)
	}
	resultResponse := httptest.NewRecorder()
	server.ServeHTTP(resultResponse, httptest.NewRequest(http.MethodGet, resultLocation, nil))
	if resultResponse.Code != http.StatusOK ||
		!strings.Contains(resultResponse.Body.String(), "Bank authentication result") ||
		!strings.Contains(resultResponse.Body.String(), "Authorized") ||
		!strings.Contains(resultResponse.Body.String(), `id="close-confirmation"`) {
		t.Fatalf("bank result did not remain simulator-owned: %d %s", resultResponse.Code, resultResponse.Body.String())
	}
	server.mu.RLock()
	authorized := cloneBarionPayment(server.barionPayments[payment.PaymentID])
	server.mu.RUnlock()
	if authorized.Status != "Authorized" {
		t.Fatalf("bank approval did not authorize the payment: %+v", authorized)
	}

	releaseResponse := barionJSONRequest(t, server, "/v2/Payment/CancelAuthorization", barionFinishRequest{
		POSKey: defaultBarionPOSKey, PaymentID: payment.PaymentID,
	})
	if releaseResponse.Code != http.StatusOK {
		t.Fatalf("cancel authorization returned %d: %s", releaseResponse.Code, releaseResponse.Body.String())
	}
	var released barionFinishResponse
	decodeJSON(t, releaseResponse.Body.Bytes(), &released)
	if !released.IsSuccessful || released.Status != "Succeeded" {
		t.Fatalf("unexpected release response: %+v", released)
	}
	for _, transaction := range released.Transactions {
		if transaction.Status != "Reversed" || transaction.Total != 0 {
			t.Fatalf("authorization was not reversed: %+v", transaction)
		}
	}
}

func TestBarionPaymentRequestIdIsIdempotentAndPersists(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "simulator.db")
	config := testConfig(databasePath)
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	first := startTestBarionPayment(t, server, "http://localhost/callback", "barion-idempotent-001")
	second := startTestBarionPayment(t, server, "http://localhost/callback", "barion-idempotent-001")
	if first.PaymentID != second.PaymentID {
		t.Fatalf("same PaymentRequestId created two payments: %s != %s", first.PaymentID, second.PaymentID)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	stateRequest := httptest.NewRequest(http.MethodGet,
		"/v4/payment/"+url.PathEscape(first.PaymentID)+"/paymentstate", nil)
	stateRequest.Header.Set("x-pos-key", defaultBarionPOSKey)
	stateResponse := httptest.NewRecorder()
	restarted.ServeHTTP(stateResponse, stateRequest)
	if stateResponse.Code != http.StatusOK {
		t.Fatalf("persisted Barion payment returned %d: %s", stateResponse.Code, stateResponse.Body.String())
	}
}

func startTestBarionPayment(t *testing.T, server http.Handler, callbackURL string, requestID string) BarionPayment {
	t.Helper()
	response := barionJSONRequest(t, server, "/v2/Payment/Start", barionStartRequest{
		POSKey: defaultBarionPOSKey, PaymentType: "DelayedCapture", GuestCheckout: true,
		FundingSources: []string{"BankCard"}, PaymentRequestID: requestID,
		RedirectURL: "http://localhost/game?payment=return", CallbackURL: callbackURL,
		Currency: "EUR", Locale: "en-US", DelayedCapturePeriod: "7.00:00:00",
		Transactions: []barionStartTransaction{{
			POSTransactionID: "transaction-" + requestID, Payee: "merchant@example.test", Total: 12.50,
			Items: []BarionItem{{Name: "QA event", Quantity: 1, Unit: "piece", UnitPrice: 12.50, ItemTotal: 12.50}},
		}},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("Barion start returned %d: %s", response.Code, response.Body.String())
	}
	var started barionStartResponse
	decodeJSON(t, response.Body.Bytes(), &started)
	serverValue, ok := server.(*Server)
	if !ok {
		t.Fatal("test server is not a simulator Server")
	}
	serverValue.mu.RLock()
	payment := cloneBarionPayment(serverValue.barionPayments[started.PaymentID])
	serverValue.mu.RUnlock()
	return *payment
}

func applyTestBarionOutcome(
	t *testing.T,
	server http.Handler,
	payment BarionPayment,
	outcome string,
	bankChallenge bool,
) BarionPayment {
	t.Helper()
	prefix := "/test/barion/payments/"
	if bankChallenge {
		prefix = "/test/barion/bank-auth/"
	}
	request := httptest.NewRequest(http.MethodPost,
		prefix+url.PathEscape(payment.PaymentID)+"/"+outcome+"?token="+
			url.QueryEscape(payment.ControlToken)+"&format=json", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("Barion %s returned %d: %s", outcome, response.Code, response.Body.String())
	}
	var result struct {
		Payment BarionPayment `json:"payment"`
	}
	decodeJSON(t, response.Body.Bytes(), &result)
	return result.Payment
}

func barionJSONRequest(t *testing.T, server http.Handler, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	return response
}
