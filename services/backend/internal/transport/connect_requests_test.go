package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

// A client asked to connect its books signs in to QuickBooks from the request's link; the sign-in is finished where
// Intuit returns, and the connection waits for a member to confirm the company.
func TestAClientConnectsItsBooksThroughARequest(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	entity := "Connect request synthetic " + rand.Text()
	var cid string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2027-04') RETURNING id::text`, entity).Scan(&cid))
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM quickbooks_connections WHERE client_entity=(SELECT entity FROM client_closes WHERE id=$1::bigint)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1::bigint)`, `DELETE FROM request_drafts WHERE close_id=$1::bigint`, `DELETE FROM client_closes WHERE id=$1::bigint`} {
			if _, e := db.Exec(q, cid); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	t.Setenv("QBO_CLIENT_ID", "synthetic-client")
	t.Setenv("QBO_CLIENT_SECRET", "synthetic-secret")
	t.Setenv("QBO_REDIRECT_URI", "https://api.example.test/v1/quickbooks/callback")
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	exchanges := 0
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		raw := `{}`
		switch {
		case r.URL.Host == "oauth.platform.intuit.com":
			exchanges++
			raw = `{"access_token":"access-synthetic","refresh_token":"refresh-one","expires_in":3600,"token_type":"bearer"}`
		case strings.Contains(r.URL.Path, "companyinfo"):
			raw = `{"CompanyInfo":{"Id":"1","CompanyName":"Synthetic company"}}`
		case strings.HasSuffix(r.URL.Path, "/preferences"):
			raw = `{"Preferences":{"CurrencyPrefs":{"HomeCurrency":{"value":"USD"}},"AccountingInfoPrefs":{"FiscalYearStartMonth":"January"}}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})
	token := strings.Repeat("k", 32)
	handler := apiWithStore(db, token)
	call := func(method, path, bearer string, body any, want int) (*httptest.ResponseRecorder, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w, out
	}
	ask := func() (map[string]any, string, string) {
		t.Helper()
		_, asked := call("POST", "/v1/closes/client/connect-requests?name="+url.QueryEscape(entity), token, map[string]any{"provider": "quickbooks", "environment": "sandbox", "recipient": "owner@example.com", "confirmed": true}, 201)
		if !strings.Contains(asked["body"].(string), "Connect QuickBooks button") {
			t.Fatalf("the request asks the client to connect QuickBooks: %v", asked)
		}
		link := asked["link_token"].(string)
		_, started := call("POST", "/v1/client-request/connect", link, nil, 200)
		u, err := url.Parse(started["url"].(string))
		must(err)
		return asked, link, u.Query().Get("state")
	}
	// A sign-in whose link was replaced meanwhile (by an approved email, say) finishes nothing, though the request has a
	// live link again.
	replaced, revoked, abandoned := ask()
	_, err = db.Exec(`UPDATE client_request_links SET revoked_at=now() WHERE digest=$1`, preparation.Digest([]byte(revoked)))
	must(err)
	tx, err := db.Begin()
	must(err)
	_, err = issueLink(t.Context(), tx, replaced["id"].(string), "fixture", rand.Text())
	must(err)
	must(tx.Commit())
	if w, _ := call("GET", "/v1/quickbooks/callback?state="+url.QueryEscape(abandoned)+"&code=synthetic-code&realmId=123", "", nil, 303); w.Header().Get("Location") != "https://app.example.test/request?connected=failed" || exchanges != 0 {
		t.Fatalf("a revoked link's sign-in is refused: %s %d", w.Header().Get("Location"), exchanges)
	}
	asked, link, state := ask()
	var request, attempt string
	must(db.QueryRow(`SELECT COALESCE(request_id::text,''),state FROM quickbooks_connections WHERE client_entity=$1 AND state<>'disconnected'`, entity).Scan(&request, &attempt))
	if request != asked["id"] || attempt != "authorizing" {
		t.Fatalf("the sign-in is bound to the request: %s %s", request, attempt)
	}
	// Intuit returns to the server, which finishes the sign-in and sends the client back to the request.
	returned := "/v1/quickbooks/callback?state=" + url.QueryEscape(state) + "&code=synthetic-code&realmId=123"
	w, _ := call("GET", returned, "", nil, 303)
	if w.Header().Get("Location") != "https://app.example.test/request?connected=quickbooks" {
		t.Fatalf("the client is told it is connected: %s", w.Header().Get("Location"))
	}
	var company string
	var used bool
	must(db.QueryRow(`SELECT x.state,x.company_name,l.used_at IS NOT NULL AND l.connected_at IS NOT NULL FROM quickbooks_connections x JOIN client_request_links l ON l.digest=x.request_link WHERE x.client_entity=$1 AND x.state<>'disconnected'`, entity).Scan(&attempt, &company, &used))
	if attempt != "pending_confirmation" || company != "Synthetic company" || !used || exchanges != 1 {
		t.Fatalf("the connection waits for a member to confirm the company, and the request is answered: %s %s %v %d", attempt, company, used, exchanges)
	}
	// The same answer again finishes nothing, and the link starts no other sign-in.
	call("GET", returned, "", nil, 410)
	call("POST", "/v1/client-request/connect", link, nil, 409)
	if exchanges != 1 {
		t.Fatal("a sign-in's answer was replayed")
	}
}
