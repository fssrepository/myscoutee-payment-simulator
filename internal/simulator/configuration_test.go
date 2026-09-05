package simulator

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigurationUIRequiresPrivateOneTimeAccess(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	directPage := httptest.NewRecorder()
	server.ServeHTTP(directPage, httptest.NewRequest(http.MethodGet, "/simulator-ui/config.html", nil))
	if directPage.Code != http.StatusNotFound {
		t.Fatalf("direct configuration page returned %d", directPage.Code)
	}
	directRegistrationDocument := httptest.NewRecorder()
	server.ServeHTTP(directRegistrationDocument, httptest.NewRequest(http.MethodGet, "/simulator-ui/register.html", nil))
	if directRegistrationDocument.Code != http.StatusNotFound {
		t.Fatalf("direct registration document returned %d", directRegistrationDocument.Code)
	}

	directAPI := httptest.NewRecorder()
	server.ServeHTTP(directAPI, httptest.NewRequest(http.MethodGet, "/configuration-session", nil))
	if directAPI.Code != http.StatusUnauthorized {
		t.Fatalf("direct configuration API returned %d", directAPI.Code)
	}

	accessRequest := httptest.NewRequest(http.MethodPost, "/myscoutee/v1/configuration-access", nil)
	accessRequest.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	accessResponse := httptest.NewRecorder()
	server.ServeHTTP(accessResponse, accessRequest)
	if accessResponse.Code != http.StatusCreated {
		t.Fatalf("configuration access returned %d: %s", accessResponse.Code, accessResponse.Body.String())
	}
	var access struct {
		URL       string `json:"url"`
		ExpiresAt string `json:"expiresAt"`
	}
	decodeJSON(t, accessResponse.Body.Bytes(), &access)
	if access.URL == "" || access.ExpiresAt == "" {
		t.Fatalf("invalid configuration access response: %+v", access)
	}
	accessURL, err := url.Parse(access.URL)
	if err != nil {
		t.Fatal(err)
	}

	exchangeRequest := httptest.NewRequest(http.MethodGet, accessURL.RequestURI(), nil)
	exchangeResponse := httptest.NewRecorder()
	server.ServeHTTP(exchangeResponse, exchangeRequest)
	if exchangeResponse.Code != http.StatusSeeOther {
		t.Fatalf("configuration ticket exchange returned %d", exchangeResponse.Code)
	}
	result := exchangeResponse.Result()
	cookies := result.Cookies()
	_ = result.Body.Close()
	if len(cookies) != 1 || cookies[0].Name != configurationSessionCookieName || !cookies[0].HttpOnly {
		t.Fatalf("configuration exchange did not issue the protected session cookie: %+v", cookies)
	}

	reusedTicket := httptest.NewRecorder()
	server.ServeHTTP(reusedTicket, httptest.NewRequest(http.MethodGet, accessURL.RequestURI(), nil))
	if reusedTicket.Code != http.StatusNotFound {
		t.Fatalf("reused configuration ticket returned %d", reusedTicket.Code)
	}

	protectedPageRequest := httptest.NewRequest(http.MethodGet, "/simulator-ui/config.html", nil)
	protectedPageRequest.AddCookie(cookies[0])
	protectedPage := httptest.NewRecorder()
	server.ServeHTTP(protectedPage, protectedPageRequest)
	if protectedPage.Code != http.StatusOK {
		t.Fatalf("protected configuration page returned %d: %s", protectedPage.Code, protectedPage.Body.String())
	}
}

func TestConfigurationAcceptsNoneProviderForCashOnly(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	cookie := configurationSessionCookie(t, server)
	request := httptest.NewRequest(
		http.MethodPut,
		"/configuration-session",
		strings.NewReader(`{"provider":"none","requires3ds":true}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("none provider update returned %d: %s", response.Code, response.Body.String())
	}

	var configuration SimulatorConfiguration
	decodeJSON(t, response.Body.Bytes(), &configuration)
	if configuration.Provider != "none" {
		t.Fatalf("provider = %q, want none", configuration.Provider)
	}
	if configuration.Requires3DS {
		t.Fatal("cash-only configuration retained requires3ds=true")
	}
}

func TestProviderConnectionMustBeGeneratedBeforeActivationAndSecretStaysPrivate(t *testing.T) {
	server, err := New(testConfig(filepath.Join(t.TempDir(), "simulator.db")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	cookie := configurationSessionCookie(t, server)
	activate := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(
			http.MethodPut,
			"/configuration-session",
			strings.NewReader(`{"provider":"stripe","requires3ds":true}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response
	}

	if response := activate(); response.Code != http.StatusConflict {
		t.Fatalf("unconnected provider activation returned %d: %s", response.Code, response.Body.String())
	}

	connectionRequest := httptest.NewRequest(
		http.MethodPost,
		"/configuration-session/providers/stripe/connection",
		nil,
	)
	connectionRequest.AddCookie(cookie)
	connectionResponse := httptest.NewRecorder()
	server.ServeHTTP(connectionResponse, connectionRequest)
	if connectionResponse.Code != http.StatusOK {
		t.Fatalf("provider connection returned %d: %s", connectionResponse.Code, connectionResponse.Body.String())
	}
	if strings.Contains(connectionResponse.Body.String(), "sk_test_") {
		t.Fatalf("admin-safe connection response leaked the test credential: %s", connectionResponse.Body.String())
	}
	if response := activate(); response.Code != http.StatusOK {
		t.Fatalf("connected provider activation returned %d: %s", response.Code, response.Body.String())
	}

	privateRequest := httptest.NewRequest(http.MethodGet, "/myscoutee/v1/configuration", nil)
	privateRequest.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	privateResponse := httptest.NewRecorder()
	server.ServeHTTP(privateResponse, privateRequest)
	if privateResponse.Code != http.StatusOK || !strings.Contains(privateResponse.Body.String(), `"connected":true`) ||
		!strings.Contains(privateResponse.Body.String(), `"credential":"sk_test_`) {
		t.Fatalf("private server configuration returned %d: %s", privateResponse.Code, privateResponse.Body.String())
	}
}

func configurationSessionCookie(t *testing.T, server *Server) *http.Cookie {
	t.Helper()
	accessRequest := httptest.NewRequest(http.MethodPost, "/myscoutee/v1/configuration-access", nil)
	accessRequest.Header.Set("Authorization", "Bearer sk_test_myscoutee")
	accessResponse := httptest.NewRecorder()
	server.ServeHTTP(accessResponse, accessRequest)
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
	cookies := exchangeResponse.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("configuration exchange returned cookies: %+v", cookies)
	}
	return cookies[0]
}
