package simulator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPaymentMethodRegistration3DSWaitsForAdminApproval(t *testing.T) {
	callbackStatuses := make(chan string, 2)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(payload, &body)
		callbackStatuses <- body["status"]
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()

	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	config := testConfig(filepath.Join(t.TempDir(), "simulator.db"))
	config.Now = func() time.Time { return now }
	config.HTTPClient = callback.Client()
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	server.mu.Lock()
	server.configuration = SimulatorConfiguration{
		Provider: "stripe", Requires3DS: true, StripeCredential: "sk_test_generated",
	}
	server.mu.Unlock()

	createBody := `{"registration_id":"pmreg-3ds","user_reference":"user-1","provider":"stripe","expires_at":"` +
		now.Add(10*time.Minute).Format(time.RFC3339Nano) + `","callback_url":"` + callback.URL + `"}`
	createRequest := httptest.NewRequest(http.MethodPost, "/myscoutee/v1/payment-method-registrations", strings.NewReader(createBody))
	createRequest.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	server.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create registration returned %d: %s", createResponse.Code, createResponse.Body.String())
	}
	var created PaymentMethodRegistration
	decodeJSON(t, createResponse.Body.Bytes(), &created)
	registrationURL, err := url.Parse(created.URL)
	if err != nil {
		t.Fatal(err)
	}
	token := registrationURL.Query().Get("token")

	completeBody := `{"cardNumber":"4242 4242 4242 4242","expiryMonth":12,"expiryYear":2029,"cardholderName":"Stripe Test User 001","securityCode":"237"}`
	completeRequest := httptest.NewRequest(
		http.MethodPost,
		"/public/payment-method-registrations/pmreg-3ds/complete?token="+url.QueryEscape(token),
		strings.NewReader(completeBody),
	)
	completeRequest.Header.Set("Content-Type", "application/json")
	completeResponse := httptest.NewRecorder()
	server.ServeHTTP(completeResponse, completeRequest)
	if completeResponse.Code != http.StatusOK {
		t.Fatalf("complete card form returned %d: %s", completeResponse.Code, completeResponse.Body.String())
	}
	var pending PaymentMethodRegistration
	decodeJSON(t, completeResponse.Body.Bytes(), &pending)
	if pending.Status != "pending" || !pending.Awaiting3DS || pending.ThreeDSExpires != now.Add(3*time.Minute).Unix() {
		t.Fatalf("card registration did not enter 3DS wait: %+v", pending)
	}
	select {
	case status := <-callbackStatuses:
		t.Fatalf("pending registration emitted terminal callback %q", status)
	default:
	}

	cookie := configurationSessionCookie(t, server)
	authorizationsRequest := httptest.NewRequest(http.MethodGet, "/authorization-session", nil)
	authorizationsRequest.AddCookie(cookie)
	authorizationsResponse := httptest.NewRecorder()
	server.ServeHTTP(authorizationsResponse, authorizationsRequest)
	if authorizationsResponse.Code != http.StatusOK {
		t.Fatalf("authorization session returned %d: %s", authorizationsResponse.Code, authorizationsResponse.Body.String())
	}
	var authorizations struct {
		Pending []pendingAuthorization `json:"pending"`
	}
	decodeJSON(t, authorizationsResponse.Body.Bytes(), &authorizations)
	if len(authorizations.Pending) != 1 || authorizations.Pending[0].Kind != "card-registration" ||
		!strings.Contains(authorizations.Pending[0].ReviewURL, "/payment-method-registration-auth/") {
		t.Fatalf("card registration missing from admin 3DS list: %+v", authorizations.Pending)
	}

	reviewURL, err := url.Parse(authorizations.Pending[0].ReviewURL)
	if err != nil {
		t.Fatal(err)
	}
	reviewResponse := httptest.NewRecorder()
	server.ServeHTTP(reviewResponse, httptest.NewRequest(http.MethodGet, reviewURL.RequestURI(), nil))
	if reviewResponse.Code != http.StatusOK || !strings.Contains(reviewResponse.Body.String(), "Approve card registration") {
		t.Fatalf("card registration confirmation returned %d: %s", reviewResponse.Code, reviewResponse.Body.String())
	}
	if csp := reviewResponse.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self'") {
		t.Fatalf("card registration confirmation CSP blocks the provider logo: %q", csp)
	}

	approvePath := "/test/payment-method-registrations/pmreg-3ds/approve?token=" + url.QueryEscape(token)
	approveResponse := httptest.NewRecorder()
	server.ServeHTTP(approveResponse, httptest.NewRequest(http.MethodPost, approvePath, nil))
	if approveResponse.Code != http.StatusSeeOther {
		t.Fatalf("approve registration returned %d: %s", approveResponse.Code, approveResponse.Body.String())
	}
	server.mu.RLock()
	approved := clonePaymentMethodRegistration(server.registrations["pmreg-3ds"], true)
	server.mu.RUnlock()
	if approved.Status != "completed" || approved.Awaiting3DS || approved.ProviderToken == "" {
		t.Fatalf("approved registration is not complete: %+v", approved)
	}
	select {
	case status := <-callbackStatuses:
		if status != "completed" {
			t.Fatalf("approval callback status = %q", status)
		}
	case <-time.After(time.Second):
		t.Fatal("approval did not emit the terminal callback")
	}
}

func TestPaymentMethodRegistration3DSDeclineIsCancelled(t *testing.T) {
	now := time.Date(2026, time.September, 6, 1, 0, 0, 0, time.UTC)
	config := testConfig(filepath.Join(t.TempDir(), "simulator.db"))
	config.Now = func() time.Time { return now }
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	server.mu.Lock()
	server.registrations["decline-registration"] = &PaymentMethodRegistration{
		ID:             "decline-registration",
		Provider:       "stripe",
		Status:         "pending",
		Awaiting3DS:    true,
		ThreeDSExpires: now.Add(3 * time.Minute).Unix(),
		ControlToken:   "decline-token",
		ExpiresAt:      now.Add(10 * time.Minute).Format(time.RFC3339Nano),
	}
	server.mu.Unlock()

	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
		"/test/payment-method-registrations/decline-registration/decline?token=decline-token", nil))
	if response.Code != http.StatusSeeOther {
		t.Fatalf("decline registration returned %d: %s", response.Code, response.Body.String())
	}
	server.mu.RLock()
	status := server.registrations["decline-registration"].Status
	server.mu.RUnlock()
	if status != "cancelled" {
		t.Fatalf("declined registration status = %q, want cancelled", status)
	}
}

func TestPaymentMethodRegistration3DSTimesOutAfterThreeMinutes(t *testing.T) {
	callbackStatuses := make(chan string, 1)
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		var body map[string]string
		_ = json.Unmarshal(payload, &body)
		callbackStatuses <- body["status"]
		w.WriteHeader(http.StatusNoContent)
	}))
	defer callback.Close()

	now := time.Date(2026, time.September, 5, 12, 3, 0, 0, time.UTC)
	config := testConfig(filepath.Join(t.TempDir(), "simulator.db"))
	config.Now = func() time.Time { return now }
	config.HTTPClient = callback.Client()
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	server.mu.Lock()
	server.registrations["pmreg-timeout"] = &PaymentMethodRegistration{
		ID: "pmreg-timeout", Provider: "stripe", Status: "pending",
		ExpiresAt:   now.Add(7 * time.Minute).Format(time.RFC3339Nano),
		Awaiting3DS: true, ThreeDSExpires: now.Unix(), ControlToken: "registration-token",
		CallbackURL: callback.URL,
	}
	server.mu.Unlock()

	statusRequest := httptest.NewRequest(
		http.MethodGet,
		"/public/payment-method-registrations/pmreg-timeout?token=registration-token",
		nil,
	)
	statusResponse := httptest.NewRecorder()
	server.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("registration timeout status returned %d: %s", statusResponse.Code, statusResponse.Body.String())
	}
	var expired PaymentMethodRegistration
	decodeJSON(t, statusResponse.Body.Bytes(), &expired)
	if expired.Status != "expired" || expired.Awaiting3DS || expired.ThreeDSExpires != 0 {
		t.Fatalf("registration did not time out: %+v", expired)
	}
	select {
	case status := <-callbackStatuses:
		if status != "expired" {
			t.Fatalf("timeout callback status = %q", status)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout did not emit the terminal callback")
	}
}

func TestGeneratedTestCardsIncrementPerProviderAcrossRegistrationsAndRestart(t *testing.T) {
	now := time.Date(2026, time.September, 6, 1, 0, 0, 0, time.UTC)
	databasePath := filepath.Join(t.TempDir(), "simulator.db")
	config := testConfig(databasePath)
	config.Now = func() time.Time { return now }
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	server.mu.Lock()
	for _, registration := range []*PaymentMethodRegistration{
		{ID: "stripe-first", Provider: "stripe", Status: "pending", ControlToken: "stripe-first-token", ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano)},
		{ID: "stripe-second", Provider: "stripe", Status: "pending", ControlToken: "stripe-second-token", ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano)},
		{ID: "barion-first", Provider: "barion", Status: "pending", ControlToken: "barion-first-token", ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano)},
	} {
		server.registrations[registration.ID] = registration
	}
	if err := server.persistLocked(); err != nil {
		server.mu.Unlock()
		t.Fatal(err)
	}
	server.mu.Unlock()

	generate := func(current *Server, registrationID, token string) generatedPaymentMethodTestCard {
		request := httptest.NewRequest(http.MethodPost,
			"/public/payment-method-registrations/"+registrationID+"/generate-test-card?token="+url.QueryEscape(token),
			strings.NewReader(`{}`))
		response := httptest.NewRecorder()
		current.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("generate test card returned %d: %s", response.Code, response.Body.String())
		}
		var card generatedPaymentMethodTestCard
		decodeJSON(t, response.Body.Bytes(), &card)
		return card
	}

	firstStripe := generate(server, "stripe-first", "stripe-first-token")
	if firstStripe.CardholderName != "Stripe Test User 001" || firstStripe.SecurityCode != "237" {
		t.Fatalf("first generated Stripe card = %+v", firstStripe)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}

	server, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	secondStripe := generate(server, "stripe-second", "stripe-second-token")
	firstBarion := generate(server, "barion-first", "barion-first-token")
	if secondStripe.CardholderName != "Stripe Test User 002" || secondStripe.SecurityCode != "374" {
		t.Fatalf("second generated Stripe card after restart = %+v", secondStripe)
	}
	if firstBarion.CardholderName != "Barion Test User 001" || firstBarion.SecurityCode != "237" {
		t.Fatalf("first generated Barion card = %+v", firstBarion)
	}
}

func TestPaymentMethodFixturesReplaceEverySeededCardAndRestoreRevokedCards(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	payload := `{"payment_methods":[
		{"provider":"stripe","provider_token":"pm_sim_seed_alex_4242","brand":"Visa","last4":"4242","expiry_month":12,"expiry_year":2029,"cardholder_name":"ALEX TURNER"},
		{"provider":"stripe","provider_token":"pm_sim_seed_alex_1881","brand":"Visa","last4":"1881","expiry_month":8,"expiry_year":2030,"cardholder_name":"ALEX TURNER"},
		{"provider":"stripe","provider_token":"pm_sim_seed_alex_expired_0008","brand":"Visa","last4":"0008","expiry_month":8,"expiry_year":2026,"cardholder_name":"ALEX TURNER"}
	]}`
	replaceFixtures := func() {
		request := httptest.NewRequest(
			http.MethodPut,
			"/myscoutee/v1/test-fixtures/payment-methods",
			strings.NewReader(payload))
		request.Header.Set("Authorization", "Bearer sk_test_myscoutee")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"payment_methods":3`) {
			t.Fatalf("fixture replacement returned %d: %s", response.Code, response.Body.String())
		}
	}

	server.mu.Lock()
	server.generatedCardSequences["stripe"] = 7
	if err := server.persistLocked(); err != nil {
		server.mu.Unlock()
		t.Fatal(err)
	}
	server.mu.Unlock()
	replaceFixtures()
	server.mu.RLock()
	remainingGeneratedSequences := len(server.generatedCardSequences)
	server.mu.RUnlock()
	if remainingGeneratedSequences != 0 {
		t.Fatalf("fixture replacement retained %d generated-card sequences", remainingGeneratedSequences)
	}
	for _, token := range []string{
		"pm_sim_seed_alex_4242",
		"pm_sim_seed_alex_1881",
		"pm_sim_seed_alex_expired_0008",
	} {
		server.mu.RLock()
		method := server.reusablePaymentMethodLocked("stripe", token)
		server.mu.RUnlock()
		if method == nil || method.ProviderToken != token {
			t.Fatalf("fixture card %q was not reusable: %+v", token, method)
		}
	}

	revoke := httptest.NewRequest(
		http.MethodDelete,
		"/myscoutee/v1/payment-methods/stripe/pm_sim_seed_alex_expired_0008",
		nil)
	revoke.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	revokeResponse := httptest.NewRecorder()
	server.ServeHTTP(revokeResponse, revoke)
	if revokeResponse.Code != http.StatusNoContent {
		t.Fatalf("fixture revoke returned %d: %s", revokeResponse.Code, revokeResponse.Body.String())
	}

	replaceFixtures()
	server.mu.RLock()
	restored := server.reusablePaymentMethodLocked("stripe", "pm_sim_seed_alex_expired_0008")
	server.mu.RUnlock()
	if restored == nil || restored.Status != "completed" {
		t.Fatalf("fixture replacement did not restore revoked card: %+v", restored)
	}
}
