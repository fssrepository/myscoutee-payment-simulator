package simulator

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedCard3DSUsesWaitingSurfaceAndAdminAuthorizationSurface(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	server.mu.Lock()
	server.configuration = SimulatorConfiguration{Provider: "stripe", Requires3DS: true}
	server.mu.Unlock()

	form := url.Values{
		"amount":                        {"490000"},
		"currency":                      {"huf"},
		"payment_method":                {"pm_sim_seed_alex_1881"},
		"capture_method":                {"manual"},
		"confirm":                       {"true"},
		"return_url":                    {"http://localhost/game?payment=success"},
		"metadata[checkout_session_id]": {"checkout-internal-3ds"},
	}
	create := authenticatedFormRequest(http.MethodPost, "/v1/payment_intents", form, "3ds-payment-001")
	createResponse := httptest.NewRecorder()
	server.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusOK {
		t.Fatalf("create payment intent returned %d: %s", createResponse.Code, createResponse.Body.String())
	}
	var intent PaymentIntent
	decodeJSON(t, createResponse.Body.Bytes(), &intent)
	if intent.NextAction == nil || !strings.Contains(intent.NextAction.RedirectToURL.URL, "/payment-wait/stripe/") {
		t.Fatalf("member action URL is not the waiting surface: %+v", intent.NextAction)
	}
	waitingURL, err := url.Parse(intent.NextAction.RedirectToURL.URL)
	if err != nil {
		t.Fatal(err)
	}
	waitingResponse := httptest.NewRecorder()
	server.ServeHTTP(waitingResponse, httptest.NewRequest(http.MethodGet, waitingURL.RequestURI(), nil))
	if waitingResponse.Code != http.StatusOK || !strings.Contains(waitingResponse.Body.String(), "Waiting for external confirmation") {
		t.Fatalf("waiting surface returned %d: %s", waitingResponse.Code, waitingResponse.Body.String())
	}

	direct := httptest.NewRecorder()
	server.ServeHTTP(direct, httptest.NewRequest(http.MethodGet, "/authorization-session", nil))
	if direct.Code != http.StatusUnauthorized {
		t.Fatalf("unprotected authorization session returned %d", direct.Code)
	}

	cookie := authorizationSessionCookie(t, server)
	pendingRequest := httptest.NewRequest(http.MethodGet, "/authorization-session", nil)
	pendingRequest.AddCookie(cookie)
	pendingResponse := httptest.NewRecorder()
	server.ServeHTTP(pendingResponse, pendingRequest)
	if pendingResponse.Code != http.StatusOK {
		t.Fatalf("authorization session returned %d: %s", pendingResponse.Code, pendingResponse.Body.String())
	}
	var state struct {
		Pending []pendingAuthorization `json:"pending"`
	}
	decodeJSON(t, pendingResponse.Body.Bytes(), &state)
	if len(state.Pending) != 1 || !strings.Contains(state.Pending[0].ApproveURL, "/test/bank-auth/") {
		t.Fatalf("pending authorizations = %+v", state.Pending)
	}
	if strings.Contains(state.Pending[0].ApproveURL, "/payment-wait/") {
		t.Fatalf("admin action URL points to member waiting surface: %s", state.Pending[0].ApproveURL)
	}
}

func TestAuthorizationSurfaceShowsEmptyStateAccess(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	cookie := authorizationSessionCookie(t, server)
	pageRequest := httptest.NewRequest(http.MethodGet, "/simulator-ui/authorizations.html", nil)
	pageRequest.AddCookie(cookie)
	pageResponse := httptest.NewRecorder()
	server.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), "3DS confirmations") {
		t.Fatalf("authorization page returned %d: %s", pageResponse.Code, pageResponse.Body.String())
	}

	stateRequest := httptest.NewRequest(http.MethodGet, "/authorization-session", nil)
	stateRequest.AddCookie(cookie)
	stateResponse := httptest.NewRecorder()
	server.ServeHTTP(stateResponse, stateRequest)
	if stateResponse.Code != http.StatusOK || !strings.Contains(stateResponse.Body.String(), `"pending":[]`) {
		t.Fatalf("empty authorization state returned %d: %s", stateResponse.Code, stateResponse.Body.String())
	}
}

func authorizationSessionCookie(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	accessRequest := httptest.NewRequest(http.MethodPost, "/myscoutee/v1/authorization-access", nil)
	accessRequest.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	accessResponse := httptest.NewRecorder()
	server.ServeHTTP(accessResponse, accessRequest)
	if accessResponse.Code != http.StatusCreated {
		t.Fatalf("authorization access returned %d: %s", accessResponse.Code, accessResponse.Body.String())
	}
	var access struct {
		URL string `json:"url"`
	}
	decodeJSON(t, accessResponse.Body.Bytes(), &access)
	accessURL, err := url.Parse(access.URL)
	if err != nil {
		t.Fatal(err)
	}
	exchangeResponse := httptest.NewRecorder()
	server.ServeHTTP(exchangeResponse, httptest.NewRequest(http.MethodGet, accessURL.RequestURI(), nil))
	if exchangeResponse.Code != http.StatusSeeOther {
		t.Fatalf("authorization access exchange returned %d", exchangeResponse.Code)
	}
	cookies := exchangeResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("authorization access exchange returned cookies: %+v", cookies)
	}
	return cookies[0]
}
