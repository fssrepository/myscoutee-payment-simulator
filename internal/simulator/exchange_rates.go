package simulator

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// exchangeRates supplies deterministic demo observations through the same backend
// import path as a real ECB feed. It never calls an external service.
func (s *Server) exchangeRates(w http.ResponseWriter, _ *http.Request) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0"?><Envelope><Cube>`)
	today := s.now().UTC()
	for offset := 0; offset < 90; offset++ {
		day := today.AddDate(0, 0, -offset)
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		fmt.Fprintf(&body, `<Cube time="%s"><Cube currency="HUF" rate="400"/><Cube currency="USD" rate="1.10"/><Cube currency="GBP" rate="0.85"/><Cube currency="CZK" rate="25"/><Cube currency="PLN" rate="4.25"/><Cube currency="CHF" rate="0.95"/></Cube>`, day.Format("2006-01-02"))
	}
	body.WriteString(`</Cube></Envelope>`)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprint(w, body.String())
}
