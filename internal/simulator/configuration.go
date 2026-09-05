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
	s.writeConfiguration(w)
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
	http.Redirect(w, r, "/simulator-ui/config.html", http.StatusSeeOther)
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
	s.writeConfiguration(w)
}

func (s *Server) updateSessionConfiguration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeConfigurationSession(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Admin configuration session is missing or expired."})
		return
	}
	var request SimulatorConfiguration
	if err := decodeStrictJSON(r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid simulator configuration."})
		return
	}
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	if request.Provider != "stripe" && request.Provider != "barion" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Provider must be stripe or barion."})
		return
	}
	s.mu.Lock()
	previous := s.configuration
	s.configuration = request
	if err := s.persistLocked(); err != nil {
		s.configuration = previous
		s.mu.Unlock()
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not save simulator configuration."})
		return
	}
	result := s.configuration
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

func (s *Server) writeConfiguration(w http.ResponseWriter) {
	s.mu.RLock()
	result := s.configuration
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, result)
}
