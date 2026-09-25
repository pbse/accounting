package transport

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestIndexRatesAreLookedUpFromTheNewYorkFed(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer db.Exec(`DELETE FROM index_rates WHERE name='SOFR' AND effective_date BETWEEN '2026-09-01' AND '2026-09-06'`)
	published := `{"refRates":[{"effectiveDate":"2026-09-04","type":"SOFR","percentRate":3.65},{"effectiveDate":"2026-09-03","type":"SOFR","percentRate":3.66},{"effectiveDate":"2026-09-05","type":"EFFR","percentRate":3.33}]}`
	var asked []string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		asked = append(asked, r.URL.String())
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(published)), Header: http.Header{}}, nil
	})
	token := strings.Repeat("i", 32)
	handler := apiWithStore(db, token)
	get := func(query string, want int) map[string]string {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/index-rates?"+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		out := map[string]string{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	stored := func() (percent string) {
		t.Helper()
		if err := db.QueryRow(`SELECT percent::text FROM index_rates WHERE name='SOFR' AND effective_date='2026-09-04'`).Scan(&percent); err != nil {
			t.Fatal(err)
		}
		return percent
	}
	got := get("index=SOFR&date=2026-09-06", 200)
	if got["effective_date"] != "2026-09-04" || got["percent"] != "3.65" || stored() != "3.6500" || asked[0] != "https://markets.newyorkfed.org/api/rates/secured/sofr/search.json?endDate=2026-09-06&startDate=2026-08-27" {
		t.Fatalf("the latest SOFR published on or before a Sunday is the Friday's, and it is stored: %v %v", got, asked)
	}
	published = strings.Replace(published, "3.65", "3.70", 1)
	if get("index=SOFR&date=2026-09-06", 200); stored() != "3.7000" {
		t.Fatal("a revised publication replaces the stored value")
	}
	published = `{"refRates":[]}`
	get("index=SOFR&date=2026-09-06", 404)
	get("index=PRIME&date=2026-09-06", 422)
}
