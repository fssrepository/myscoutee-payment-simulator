package simulator

import (
	"net/http"
	"strings"
)

func (s *Server) configurationRoutes() {
	s.mux.HandleFunc("GET /myscoutee/v1/configuration", s.privateConfiguration)
	s.mux.HandleFunc("GET /public/simulator-configuration", s.publicConfiguration)
	s.mux.HandleFunc("PUT /public/simulator-configuration", s.updatePublicConfiguration)
}

func (s *Server) configurationPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/simulator-ui/config.html", http.StatusTemporaryRedirect)
}

func (s *Server) privateConfiguration(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeAPI(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Invalid test API key."})
		return
	}
	s.writeConfiguration(w)
}

func (s *Server) publicConfiguration(w http.ResponseWriter, _ *http.Request) {
	s.writeConfiguration(w)
}

func (s *Server) updatePublicConfiguration(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) writeConfiguration(w http.ResponseWriter) {
	s.mu.RLock()
	result := s.configuration
	s.mu.RUnlock()
	writeJSON(w, http.StatusOK, result)
}
