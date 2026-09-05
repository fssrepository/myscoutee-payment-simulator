package simulator

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	configurationAccessTTL         = 10 * time.Minute
	configurationSessionCookieName = "myscoutee_payment_simulator_admin"
)

func (s *Server) configurationRoutes() {
	s.mux.HandleFunc("GET /myscoutee/v1/configuration", s.privateConfiguration)
	s.mux.HandleFunc("POST /myscoutee/v1/configuration-access", s.createConfigurationAccess)
	s.mux.HandleFunc("GET /configuration-access/{ticket}", s.exchangeConfigurationAccess)
	s.mux.HandleFunc("GET /simulator-ui/config.html", s.configurationDocument)
	s.mux.HandleFunc("GET /configuration-session", s.sessionConfiguration)
	s.mux.HandleFunc("PUT /configuration-session", s.updateSessionConfiguration)
	s.mux.HandleFunc("POST /configuration-session/providers/{provider}/connection", s.createProviderConnection)
}

type updateSimulatorConfigurationRequest struct {
	Provider    string `json:"provider"`
	Requires3DS bool   `json:"requires3ds"`
}

type providerConnectionView struct {
	Connected      bool   `json:"connected"`
	CredentialMask string `json:"credentialMask,omitempty"`
}

type simulatorConfigurationView struct {
	Provider    string                            `json:"provider"`
	Requires3DS bool                              `json:"requires3ds"`
	Connections map[string]providerConnectionView `json:"connections"`
}

type privateSimulatorConfiguration struct {
	Provider    string `json:"provider"`
	Requires3DS bool   `json:"requires3ds"`
	Connected   bool   `json:"connected"`
	Credential  string `json:"credential,omitempty"`
}

func (s *Server) configurationPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) privateConfiguration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAPI(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid test API key."})
		return
	}
	s.mu.RLock()
	result := s.privateConfigurationLocked()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) createConfigurationAccess(w http.ResponseWriter, r *http.Request) {
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
		"url":       strings.TrimRight(s.config.PublicBaseURL, "/") + "/configuration-access/" + ticket,
		"expiresAt": expiresAt.Format(time.RFC3339Nano),
	})
}

func (s *Server) exchangeConfigurationAccess(w http.ResponseWriter, r *http.Request) {
	s.exchangeAdminAccess(w, r, "/simulator-ui/config.html")
}

func (s *Server) exchangeAdminAccess(w http.ResponseWriter, r *http.Request, target string) {
	ticket := strings.TrimSpace(r.PathValue("ticket"))
	now := s.now().UTC()
	s.mu.Lock()
	s.cleanupConfigurationAccessLocked(now)
	ticketExpiry, found := s.configurationAccessTickets[ticket]
	if found {
		delete(s.configurationAccessTickets, ticket)
	}
	if !found || !ticketExpiry.After(now) {
		s.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	sessionToken := randomHex(32)
	sessionExpiry := now.Add(configurationAccessTTL)
	s.configurationSessions[sessionToken] = sessionExpiry
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name:     configurationSessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		MaxAge:   int(configurationAccessTTL.Seconds()),
		Expires:  sessionExpiry,
		HttpOnly: true,
		Secure:   strings.HasPrefix(strings.ToLower(s.config.PublicBaseURL), "https://"),
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) configurationDocument(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		http.NotFound(w, r)
		return
	}
	document, err := paymentMethodUI.ReadFile("web/config.html")
	if err != nil {
		http.Error(w, "Payment simulator configuration is unavailable.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(document)))
	_, _ = w.Write(document)
}

func (s *Server) sessionConfiguration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Admin configuration session is missing or expired."})
		return
	}
	s.writeConfigurationView(w)
}

func (s *Server) updateSessionConfiguration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Admin configuration session is missing or expired."})
		return
	}
	var request updateSimulatorConfigurationRequest
	if err := decodeStrictJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid simulator configuration."})
		return
	}
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	if request.Provider != "none" && request.Provider != "stripe" && request.Provider != "barion" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Provider must be none, stripe or barion."})
		return
	}
	s.mu.Lock()
	if request.Provider != "none" && !s.providerConnectedLocked(request.Provider) {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Generate the selected provider test connection before activating it."})
		return
	}
	previous := s.configuration
	s.configuration.Provider = request.Provider
	s.configuration.Requires3DS = request.Provider != "none" && request.Requires3DS
	if err := s.persistLocked(); err != nil {
		s.configuration = previous
		s.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save simulator configuration."})
		return
	}
	result := s.configurationViewLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) createProviderConnection(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Admin configuration session is missing or expired."})
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if provider != "stripe" && provider != "barion" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Provider must be stripe or barion."})
		return
	}
	s.mu.Lock()
	previous := s.configuration
	if !s.providerConnectedLocked(provider) {
		if provider == "stripe" {
			s.configuration.StripeCredential = "sk_test_" + randomHex(24)
		} else {
			s.configuration.BarionCredential = randomGUID()
		}
		if err := s.persistLocked(); err != nil {
			s.configuration = previous
			s.mu.Unlock()
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save the provider test connection."})
			return
		}
	}
	result := s.configurationViewLocked()
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) authorizeConfigurationSession(r *http.Request) bool {
	cookie, err := r.Cookie(configurationSessionCookieName)
	if err != nil {
		return false
	}
	token := strings.TrimSpace(cookie.Value)
	if token == "" {
		return false
	}
	now := s.now().UTC()
	s.mu.Lock()
	s.cleanupConfigurationAccessLocked(now)
	expiresAt, found := s.configurationSessions[token]
	s.mu.Unlock()
	return found && expiresAt.After(now)
}

func (s *Server) cleanupConfigurationAccessLocked(now time.Time) {
	for ticket, expiresAt := range s.configurationAccessTickets {
		if !expiresAt.After(now) {
			delete(s.configurationAccessTickets, ticket)
		}
	}
	for token, expiresAt := range s.configurationSessions {
		if !expiresAt.After(now) {
			delete(s.configurationSessions, token)
		}
	}
}

func (s *Server) writeConfigurationView(w http.ResponseWriter) {
	s.mu.RLock()
	result := s.configurationViewLocked()
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) configurationViewLocked() simulatorConfigurationView {
	return simulatorConfigurationView{
		Provider:    s.configuration.Provider,
		Requires3DS: s.configuration.Requires3DS,
		Connections: map[string]providerConnectionView{
			"stripe": connectionView(s.configuration.StripeCredential),
			"barion": connectionView(s.configuration.BarionCredential),
		},
	}
}

func (s *Server) privateConfigurationLocked() privateSimulatorConfiguration {
	provider := strings.ToLower(strings.TrimSpace(s.configuration.Provider))
	credential := s.providerCredentialLocked(provider)
	return privateSimulatorConfiguration{
		Provider:    provider,
		Requires3DS: provider != "none" && s.configuration.Requires3DS,
		Connected:   provider == "none" || credential != "",
		Credential:  credential,
	}
}

func (s *Server) providerConnectedLocked(provider string) bool {
	return s.providerCredentialLocked(provider) != ""
}

func (s *Server) providerCredentialLocked(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "stripe":
		return strings.TrimSpace(s.configuration.StripeCredential)
	case "barion":
		return strings.TrimSpace(s.configuration.BarionCredential)
	default:
		return ""
	}
}

func connectionView(credential string) providerConnectionView {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return providerConnectionView{}
	}
	maskSuffix := credential
	if len(maskSuffix) > 4 {
		maskSuffix = maskSuffix[len(maskSuffix)-4:]
	}
	return providerConnectionView{Connected: true, CredentialMask: "•••• " + maskSuffix}
}
