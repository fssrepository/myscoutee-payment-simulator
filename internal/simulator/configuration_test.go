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
