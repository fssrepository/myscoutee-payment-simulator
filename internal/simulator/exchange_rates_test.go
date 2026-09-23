package simulator

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestExchangeRatesUseUTCWeekdaysAndFixedDemoValues(t *testing.T) {
	config := testConfig(filepath.Join(t.TempDir(), "simulator.db"))
	// Sunday locally, Monday in UTC.
	config.Now = func() time.Time { return time.Date(2026, 9, 20, 23, 30, 0, 0, time.FixedZone("west", -7200)) }
	server, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/exchange-rates/eurofxref.xml", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status %d", response.Code)
	}
	var feed struct {
		Days []struct {
			Date  string `xml:"time,attr"`
			Rates []struct {
				Currency string `xml:"currency,attr"`
				Rate     string `xml:"rate,attr"`
			} `xml:"Cube"`
		} `xml:"Cube>Cube"`
	}
	if err := xml.Unmarshal(response.Body.Bytes(), &feed); err != nil {
		t.Fatal(err)
	}
	if len(feed.Days) < 60 || len(feed.Days) > 65 || feed.Days[0].Date != "2026-09-21" {
		t.Fatalf("unexpected observations: %+v", feed)
	}
	for _, day := range feed.Days {
		date, err := time.Parse("2006-01-02", day.Date)
		if err != nil || date.Weekday() == time.Saturday || date.Weekday() == time.Sunday {
			t.Fatalf("invalid weekday: %s", day.Date)
		}
		if len(day.Rates) != 6 || day.Rates[0].Currency != "HUF" || day.Rates[0].Rate != "400" {
			t.Fatalf("unexpected rates: %+v", day.Rates)
		}
	}
}
