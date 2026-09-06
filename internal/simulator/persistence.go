package simulator

import (
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"strings"
)

func (s *Server) loadState() error {
	var payload []byte
	err := s.db.QueryRow(`SELECT payload FROM simulator_state WHERE id = 1`).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load simulator state: %w", err)
	}
	var state persistedState
	if err := gob.NewDecoder(strings.NewReader(string(payload))).Decode(&state); err != nil {
		return fmt.Errorf("decode simulator state: %w", err)
	}
	if state.Sessions != nil {
		s.sessions = state.Sessions
	}
	if state.Intents != nil {
		s.intents = state.Intents
	}
	if state.Idempotency != nil {
		s.idempotency = state.Idempotency
	}
	if state.Events != nil {
		s.events = state.Events
	}
	if state.BarionPayments != nil {
		s.barionPayments = state.BarionPayments
	}
	if state.BarionRequestIndex != nil {
		s.barionRequestIndex = state.BarionRequestIndex
	}
	if state.Registrations != nil {
		s.registrations = state.Registrations
	}
	if state.GeneratedCardSequences != nil {
		s.generatedCardSequences = state.GeneratedCardSequences
	}
	configurationMigrated := false
	if state.Configuration.Provider == "none" || state.Configuration.Provider == "stripe" || state.Configuration.Provider == "barion" {
		s.configuration = state.Configuration
		if s.configuration.Provider == "none" {
			s.configuration.Requires3DS = false
		} else {
			configurationMigrated = s.ensureProviderConnectionLocked(s.configuration.Provider)
		}
	}
	s.eventOrder = append([]string(nil), state.EventOrder...)
	if configurationMigrated {
		return s.persistLocked()
	}
	return nil
}

func (s *Server) persistLocked() error {
	var payload strings.Builder
	state := persistedState{
		Sessions:           s.sessions,
		Intents:            s.intents,
		Idempotency:        s.idempotency,
		Events:             s.events,
		EventOrder:         s.eventOrder,
		BarionPayments:     s.barionPayments,
		BarionRequestIndex: s.barionRequestIndex,
		Registrations:      s.registrations,
		Configuration:      s.configuration,
		GeneratedCardSequences: s.generatedCardSequences,
	}
	if err := gob.NewEncoder(&payload).Encode(state); err != nil {
		return err
	}
	_, err := s.db.Exec(`
		INSERT INTO simulator_state (id, payload, updated_at)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, updated_at = excluded.updated_at`,
		[]byte(payload.String()), s.now().UTC().Unix())
	return err
}

func (s *Server) deliverySnapshot(eventID string) DeliveryAudit {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if event := s.events[eventID]; event != nil {
		return event.Delivery
	}
	return DeliveryAudit{}
}
