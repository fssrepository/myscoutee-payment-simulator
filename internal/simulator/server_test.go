package simulator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCheckoutSessionIsIdempotentAndPersistsAcrossRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "simulator.db")
	config := testConfig(databasePath)
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	first := createTestSession(t, server, "checkout-001", "2500")
	second := createTestSession(t, server, "checkout-001", "2500")
	if first.ID != second.ID {
		t.Fatalf("same idempotency key created two sessions: %s != %s", first.ID, second.ID)
	}
	if first.AmountTotal != 2500 || first.Currency != "usd" || first.PaymentStatus != "unpaid" {
		t.Fatalf("unexpected session: %+v", first)
	}

	conflictForm := validSessionForm("2600")
	conflictRequest := authenticatedFormRequest(http.MethodPost, "/v1/checkout/sessions", conflictForm, "checkout-001")
	conflictResponse := httptest.NewRecorder()
	server.ServeHTTP(conflictResponse, conflictRequest)
	if conflictResponse.Code != http.StatusConflict {
		t.Fatalf("different payload with same key returned %d", conflictResponse.Code)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	retrieve := httptest.NewRequest(http.MethodGet, "/v1/checkout/sessions/"+first.ID, nil)
	retrieve.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	retrieveResponse := httptest.NewRecorder()
	restarted.ServeHTTP(retrieveResponse, retrieve)
	if retrieveResponse.Code != http.StatusOK {
		t.Fatalf("persisted session retrieval returned %d: %s", retrieveResponse.Code, retrieveResponse.Body.String())
	}
}

func TestRevokedSeedPaymentMethodRemainsAuditableAndCannotBeReused(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "simulator.db")
	config := testConfig(databasePath)
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	revoke := httptest.NewRequest(
		http.MethodDelete,
		"/myscoutee/v1/payment-methods/stripe/pm_sim_seed_alex_4242",
		nil)
	revoke.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	revokeResponse := httptest.NewRecorder()
	server.ServeHTTP(revokeResponse, revoke)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("revoke returned %d: %s", revokeResponse.Code, revokeResponse.Body.String())
	}

	server.mu.RLock()
	revoked := server.registrations["seed_pm_sim_seed_alex_4242"]
	reusable := server.reusablePaymentMethodLocked("stripe", "pm_sim_seed_alex_4242")
	server.mu.RUnlock()
	if revoked == nil || revoked.Status != "revoked" || revoked.ProviderToken != "pm_sim_seed_alex_4242" {
		t.Fatalf("revoked payment method audit was not retained: %+v", revoked)
	}
	if reusable != nil {
		t.Fatalf("revoked payment method remained reusable: %+v", reusable)
	}

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	restarted.mu.RLock()
	reusable = restarted.reusablePaymentMethodLocked("stripe", "pm_sim_seed_alex_4242")
	restarted.mu.RUnlock()
	if reusable != nil {
		t.Fatalf("revoked payment method became reusable after restart: %+v", reusable)
	}
}

func TestSuccessfulOutcomeSendsSignedWebhookAndReplayIsAudited(t *testing.T) {
	var mu sync.Mutex
	var payloads [][]byte
	var signatures []string
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		mu.Lock()
		payloads = append(payloads, payload)
		signatures = append(signatures, r.Header.Get("Stripe-Signature"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer webhook.Close()

	config := testConfig(filepath.Join(t.TempDir(), "simulator.db"))
	config.WebhookURL = webhook.URL
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	session := createTestSession(t, server, "checkout-002", "1250")

	outcomeRequest := httptest.NewRequest(
		http.MethodPost,
		"/test/sessions/"+session.ID+"/complete?token="+url.QueryEscape(session.ControlToken)+"&format=json",
		nil)
	outcomeResponse := httptest.NewRecorder()
	server.ServeHTTP(outcomeResponse, outcomeRequest)
	if outcomeResponse.Code != http.StatusOK {
		t.Fatalf("complete returned %d: %s", outcomeResponse.Code, outcomeResponse.Body.String())
	}
	var outcome struct {
		EventID       string        `json:"event_id"`
		EventIDs      []string      `json:"event_ids"`
		PaymentIntent PaymentIntent `json:"payment_intent"`
	}
	decodeJSON(t, outcomeResponse.Body.Bytes(), &outcome)
	if outcome.PaymentIntent.Status != "requires_capture" || outcome.PaymentIntent.AmountCapturable != 1250 {
		t.Fatalf("checkout did not authorize for later capture: %+v", outcome.PaymentIntent)
	}

	mu.Lock()
	if len(payloads) != 2 || len(signatures) != 2 {
		mu.Unlock()
		t.Fatalf("expected checkout and authorization webhooks, got %d", len(payloads))
	}
	webhookPayloads := make([][]byte, len(payloads))
	copy(webhookPayloads, payloads)
	webhookSignatures := append([]string(nil), signatures...)
	mu.Unlock()
	expectedTypes := []string{"checkout.session.completed", "payment_intent.amount_capturable_updated"}
	for index, payload := range webhookPayloads {
		assertSignature(t, webhookSignatures[index], payload, config.WebhookSecret)
		var event WebhookEvent
		decodeJSON(t, payload, &event)
		if event.Type != expectedTypes[index] {
			t.Fatalf("unexpected webhook %d: %+v", index, event)
		}
		if strings.Contains(string(event.Data.Object), "control_token") {
			t.Fatal("control token leaked into webhook")
		}
	}

	replayRequest := httptest.NewRequest(http.MethodPost, "/test/events/"+outcome.EventID+"/replay", nil)
	replayRequest.Header.Set("X-Test-Audit-Token", config.AuditToken)
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, replayRequest)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("replay returned %d: %s", replayResponse.Code, replayResponse.Body.String())
	}
	if delivery := server.deliverySnapshot(outcome.EventID); delivery.Attempts != 2 || delivery.LastStatusCode != http.StatusNoContent {
		t.Fatalf("unexpected delivery audit: %+v", delivery)
	}
}

func TestManualCaptureAndReleaseAreIdempotent(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	capturedSession := createTestSession(t, server, "checkout-capture", "700")
	authorizeSession(t, server, capturedSession)
	capturePath := "/v1/payment_intents/" + capturedSession.PaymentIntent + "/capture"
	firstCapture := authenticatedFormRequest(http.MethodPost, capturePath, url.Values{}, "capture-001")
	firstResponse := httptest.NewRecorder()
	server.ServeHTTP(firstResponse, firstCapture)
	if firstResponse.Code != http.StatusOK {
		t.Fatalf("capture returned %d: %s", firstResponse.Code, firstResponse.Body.String())
	}
	var captured PaymentIntent
	decodeJSON(t, firstResponse.Body.Bytes(), &captured)
	if captured.Status != "succeeded" || captured.AmountReceived != 700 || captured.AmountCapturable != 0 {
		t.Fatalf("unexpected captured intent: %+v", captured)
	}
	replayCapture := authenticatedFormRequest(http.MethodPost, capturePath, url.Values{}, "capture-001")
	replayResponse := httptest.NewRecorder()
	server.ServeHTTP(replayResponse, replayCapture)
	if replayResponse.Code != http.StatusOK {
		t.Fatalf("idempotent capture replay returned %d: %s", replayResponse.Code, replayResponse.Body.String())
	}

	releasedSession := createTestSession(t, server, "checkout-release", "800")
	authorizeSession(t, server, releasedSession)
	releasePath := "/v1/payment_intents/" + releasedSession.PaymentIntent + "/cancel"
	release := authenticatedFormRequest(http.MethodPost, releasePath,
		url.Values{"cancellation_reason": {"requested_by_customer"}}, "release-001")
	releaseResponse := httptest.NewRecorder()
	server.ServeHTTP(releaseResponse, release)
	if releaseResponse.Code != http.StatusOK {
		t.Fatalf("release returned %d: %s", releaseResponse.Code, releaseResponse.Body.String())
	}
	var released PaymentIntent
	decodeJSON(t, releaseResponse.Body.Bytes(), &released)
	if released.Status != "canceled" || released.CancellationReason != "requested_by_customer" {
		t.Fatalf("unexpected released intent: %+v", released)
	}
}

func TestOptionalBankAuthenticationBranchesBeforeAuthorization(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	approvedSession := createTestSession(t, server, "checkout-3ds-approve", "900")
	actionRequest := httptest.NewRequest(http.MethodPost,
		"/test/sessions/"+approvedSession.ID+"/3ds?token="+url.QueryEscape(approvedSession.ControlToken)+"&format=json", nil)
	actionResponse := httptest.NewRecorder()
	server.ServeHTTP(actionResponse, actionRequest)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("3DS request returned %d: %s", actionResponse.Code, actionResponse.Body.String())
	}
	var action struct {
		PaymentIntent PaymentIntent `json:"payment_intent"`
	}
	decodeJSON(t, actionResponse.Body.Bytes(), &action)
	if action.PaymentIntent.Status != "requires_action" || action.PaymentIntent.NextAction == nil {
		t.Fatalf("expected requires_action, got %+v", action.PaymentIntent)
	}
	approveRequest := httptest.NewRequest(http.MethodPost,
		"/test/bank-auth/"+approvedSession.ID+"/approve?token="+url.QueryEscape(approvedSession.ControlToken)+"&format=json", nil)
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, approveRequest)
	if approveResponse.Code != http.StatusOK {
		t.Fatalf("3DS approval returned %d: %s", approveResponse.Code, approveResponse.Body.String())
	}
	var approved struct {
		PaymentIntent PaymentIntent `json:"payment_intent"`
	}
	decodeJSON(t, approveResponse.Body.Bytes(), &approved)
	if approved.PaymentIntent.Status != "requires_capture" || approved.PaymentIntent.NextAction != nil {
		t.Fatalf("3DS approval did not authorize funds: %+v", approved.PaymentIntent)
	}

	declinedSession := createTestSession(t, server, "checkout-3ds-decline", "901")
	requireBankAction(t, server, declinedSession)
	declineRequest := httptest.NewRequest(http.MethodPost,
		"/test/bank-auth/"+declinedSession.ID+"/decline?token="+url.QueryEscape(declinedSession.ControlToken)+"&format=json", nil)
	declineResponse := httptest.NewRecorder()
	server.ServeHTTP(declineResponse, declineRequest)
	if declineResponse.Code != http.StatusOK {
		t.Fatalf("3DS decline returned %d: %s", declineResponse.Code, declineResponse.Body.String())
	}
	var declined struct {
		PaymentIntent PaymentIntent `json:"payment_intent"`
	}
	decodeJSON(t, declineResponse.Body.Bytes(), &declined)
	if declined.PaymentIntent.Status != "requires_payment_method" ||
		declined.PaymentIntent.LastPaymentError == nil ||
		declined.PaymentIntent.LastPaymentError.Code != "authentication_failed" {
		t.Fatalf("unexpected 3DS decline state: %+v", declined.PaymentIntent)
	}
}

func TestMissingIdempotencyAndLiveKeyAreRejected(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	missing := authenticatedFormRequest(http.MethodPost, "/v1/checkout/sessions", validSessionForm("100"), "")
	missingResponse := httptest.NewRecorder()
	server.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key returned %d", missingResponse.Code)
	}

	live := authenticatedFormRequest(http.MethodPost, "/v1/checkout/sessions", validSessionForm("100"), "key-1")
	live.Header.Set("Authorization", "Bearer sk_live_real_keys_are_never_valid")
	liveResponse := httptest.NewRecorder()
	server.ServeHTTP(liveResponse, live)
	if liveResponse.Code < 400 {
		t.Fatalf("live key returned %d", liveResponse.Code)
	}
}

func TestAuditRequiresTokenAndDoesNotExposeSecrets(t *testing.T) {
	config := testConfig(filepath.Join(t.TempDir(), "simulator.db"))
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	_ = createTestSession(t, server, "checkout-audit", "333")

	unauthorized := httptest.NewRecorder()
	server.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/test/audit", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized audit returned %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/test/audit", nil)
	request.Header.Set("X-Test-Audit-Token", config.AuditToken)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("audit returned %d", response.Code)
	}
	body := response.Body.String()
	for _, secret := range []string{config.APIKey, config.WebhookSecret, config.AuditToken, "control_token"} {
		if strings.Contains(body, secret) {
			t.Fatalf("audit leaked secret marker %q: %s", secret, body)
		}
	}
}

func testConfig(databasePath string) Config {
	return Config{
		ListenAddress:      "127.0.0.1:18082",
		PublicBaseURL:      "http://127.0.0.1:18082",
		DatabasePath:       databasePath,
		APIKey:             "sk_test_myscoutee",
		WebhookSecret:      "whsec_myscoutee_test",
		AuditToken:         "audit-test-token",
		RequireIdempotency: true,
		Now: func() time.Time {
			return time.Unix(1_800_000_000, 0).UTC()
		},
	}
}

func createTestSession(t *testing.T, server http.Handler, idempotencyKey string, amount string) CheckoutSession {
	t.Helper()
	request := authenticatedFormRequest(http.MethodPost, "/v1/checkout/sessions", validSessionForm(amount), idempotencyKey)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("create session returned %d: %s", response.Code, response.Body.String())
	}
	var session CheckoutSession
	decodeJSON(t, response.Body.Bytes(), &session)
	serverValue, ok := server.(*Server)
	if ok {
		serverValue.mu.RLock()
		session.ControlToken = serverValue.sessions[session.ID].ControlToken
		serverValue.mu.RUnlock()
	}
	return session
}

func authenticatedFormRequest(method string, target string, form url.Values, idempotencyKey string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	request.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	return request
}

func validSessionForm(amount string) url.Values {
	return url.Values{
		"mode":                                          {"payment"},
		"payment_intent_data[capture_method]":           {"manual"},
		"success_url":                                   {"http://localhost/game?payment=success&session_id={CHECKOUT_SESSION_ID}"},
		"cancel_url":                                    {"http://localhost/game?payment=cancel"},
		"client_reference_id":                           {"checkout-internal-001"},
		"metadata[source_id]":                           {"event-001"},
		"line_items[0][quantity]":                       {"1"},
		"line_items[0][price_data][currency]":           {"usd"},
		"line_items[0][price_data][unit_amount]":        {amount},
		"line_items[0][price_data][product_data][name]": {"QA event"},
	}
}

func authorizeSession(t *testing.T, server http.Handler, session CheckoutSession) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost,
		"/test/sessions/"+session.ID+"/authorize?token="+url.QueryEscape(session.ControlToken)+"&format=json", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorize returned %d: %s", response.Code, response.Body.String())
	}
}

func requireBankAction(t *testing.T, server http.Handler, session CheckoutSession) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost,
		"/test/sessions/"+session.ID+"/3ds?token="+url.QueryEscape(session.ControlToken)+"&format=json", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("require bank action returned %d: %s", response.Code, response.Body.String())
	}
}

func assertSignature(t *testing.T, header string, payload []byte, secret string) {
	t.Helper()
	parts := strings.Split(header, ",")
	if len(parts) != 2 {
		t.Fatalf("invalid signature header: %q", header)
	}
	timestamp, err := strconv.ParseInt(strings.TrimPrefix(parts[0], "t="), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	expected := signPayload(secret, timestamp, payload)
	if parts[1] != "v1="+expected {
		t.Fatalf("signature mismatch: %q", header)
	}
}

func decodeJSON(t *testing.T, payload []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(payload, target); err != nil {
		t.Fatalf("decode JSON: %v\npayload: %s", err, payload)
	}
}
