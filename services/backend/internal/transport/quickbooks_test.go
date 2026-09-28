package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestQuickBooksScopedOAuthRefreshAndSnapshots(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_QBO" + rand.Text()
	other := "org_QBO" + rand.Text()
	var closeID string
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Synthetic QBO'),($2,'Other firm')`, org, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture'),($1,'user_other',true,'Synthetic enrollment','fixture'),($2,'user_accountant',true,'Synthetic enrollment','fixture')`, org, other); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'QBO synthetic client','2026-09') RETURNING id::text`, org).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM close_preparations WHERE close_id=$1`, `DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			if _, e := db.Exec(q, closeID); e != nil {
				t.Error(e)
			}
		}
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id IN ($1,$2)`, org, other)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, org, other)
	}()
	t.Setenv("QBO_CLIENT_ID", "synthetic-client")
	t.Setenv("QBO_CLIENT_SECRET", "synthetic-secret")
	t.Setenv("QBO_REDIRECT_URI", "https://api.example.test/v1/quickbooks/callback")
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}
	mux := http.NewServeMux()
	registerQuickBooks(mux, db)
	registerCoordinator(mux, db)
	h := organizationBoundary(db, mux)
	base := "/v1/closes/" + closeID + "/quickbooks"
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	exchanges, refreshes, pages := 0, 0, 0
	failRefresh, failPage := false, false
	periodMode, badPeriod, largePeriod := false, false, false
	connectDuringRead := false
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		var raw string
		switch r.URL.Host {
		case "oauth.platform.intuit.com":
			if r.Method != "POST" {
				t.Fatal("incorrect OAuth method")
			}
			r.ParseForm()
			var state string
			if e := db.QueryRow(`SELECT state FROM quickbooks_connections WHERE organization_id=$1 AND state<>'disconnected'`, org).Scan(&state); e != nil {
				t.Fatal(e)
			}
			if r.Form.Get("grant_type") == "authorization_code" {
				exchanges++
				if state != "exchanging" {
					t.Fatal("exchange not durably claimed")
				}
			} else {
				refreshes++
				if state != "refreshing" || r.Form.Get("refresh_token") != "refresh-one" {
					t.Fatal("refresh not durable or wrong token")
				}
				if failRefresh {
					return nil, errors.New("synthetic lost refresh response")
				}
			}
			refresh := "refresh-one"
			if refreshes > 0 {
				refresh = "refresh-two"
			}
			raw = `{"access_token":"access-synthetic","refresh_token":"` + refresh + `","expires_in":3600,"token_type":"bearer"}`
		case "sandbox-quickbooks.api.intuit.com":
			if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/v3/company/123/") {
				t.Fatal("reader crossed realm or write boundary")
			}
			if strings.Contains(r.URL.Path, "companyinfo") {
				raw = `{"CompanyInfo":{"Id":"1","CompanyName":"Synthetic company"}}`
				if connectDuringRead {
					connectDuringRead = false
					if _, e := db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,environment) VALUES($1,$2,'QBO synthetic client','user_accountant','authorizing',$3,now()+interval '10 minutes','production')`, closeID, org, rand.Text()); e != nil {
						t.Fatal(e)
					}
				}
			} else if strings.HasSuffix(r.URL.Path, "/preferences") {
				raw = `{"Preferences":{"CurrencyPrefs":{"HomeCurrency":{"value":"CAD"}},"AccountingInfoPrefs":{"FiscalYearStartMonth":"July"}}}`
			} else if strings.Contains(r.URL.Path, "/reports/") {
				name := strings.TrimPrefix(r.URL.Path, "/v3/company/123/reports/")
				if r.URL.Query().Get("start_date") != "2026-09-01" || r.URL.Query().Get("end_date") != "2026-09-30" || r.URL.Query().Get("accounting_method") != "Accrual" {
					t.Fatal("wrong report scope")
				}
				end := "2026-09-30"
				if badPeriod {
					end = "2026-08-31"
				}
				raw = fmt.Sprintf(`{"Header":{"ReportName":%q,"StartPeriod":"2026-09-01","EndPeriod":%q,"ReportBasis":"Accrual","Currency":"USD","Time":"2026-09-22T10:00:00Z","Option":[{"Name":"NoReportData","Value":"false"}]},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Amount"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Checking"},{"value":"9007199254740993.01"}]},{"type":"Data","ColData":[{"id":"2","value":"Prepaid"},{"value":"123.45"}]}]}}`, name, end)
				if largePeriod && name == "GeneralLedger" {
					rows := make([]string, 1500)
					for n := range rows {
						rows[n] = fmt.Sprintf(`{"type":"Data","ColData":[{"id":"2","value":"Prepaid"},{"value":"%d.01"}]}`, n)
					}
					raw = strings.Replace(raw, `"Rows":{"Row":[`, `"Rows":{"Row":[`+strings.Join(rows, ",")+`,`, 1)
				}
			} else if periodMode {
				raw = `{"QueryResponse":{"startPosition":1,"maxResults":2,"Account":[{"Id":"1","Name":"Checking","AccountType":"Bank"},{"Id":"2","Name":"Prepaid","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses","Active":false}]}}`
			} else {
				pages++
				q := r.URL.Query().Get("query")
				if !strings.Contains(q, "Active IN (true,false)") {
					t.Fatal("inactive accounts excluded")
				}
				if strings.Contains(q, "STARTPOSITION 1 ") {
					var accounts []string
					for n := 1; n <= 1000; n++ {
						accounts = append(accounts, fmt.Sprintf(`{"Id":"%d","Name":"Account %d","CurrentBalance":9007199254740993.01}`, n, n))
					}
					raw = `{"QueryResponse":{"startPosition":1,"maxResults":1000,"Account":[` + strings.Join(accounts, ",") + `]}}`
				} else if strings.Contains(q, "STARTPOSITION 1001 ") {
					if failPage {
						return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
					}
					raw = `{"QueryResponse":{"startPosition":1001,"maxResults":1,"Account":[{"Id":"1001","Name":"Last account","Active":false}]}}`
				} else {
					t.Fatal("incorrect page", q)
				}
			}
		default:
			t.Fatal("unexpected live provider", r.URL.Host)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(raw)), Header: make(http.Header)}, nil
	})
	var start struct{ ID, URL string }
	json.Unmarshal(call("POST", base, map[string]any{"action": "connect", "confirmed": true}, 200), &start)
	u, _ := url.Parse(start.URL)
	state := u.Query().Get("state")
	callback := map[string]any{"action": "callback", "connection_id": start.ID, "confirmed": true, "code": "synthetic-code", "state": state, "realm": "123"}
	bad := map[string]any{"action": "callback", "connection_id": start.ID, "confirmed": true, "code": "synthetic-code", "state": "wrong", "realm": "123"}
	call("POST", base, bad, 409)
	if exchanges != 0 {
		t.Fatal("invalid state reached provider")
	}
	actor.Subject = "user_other"
	call("POST", base, callback, 409)
	actor.Subject = "user_accountant"
	r := httptest.NewRequest("GET", "/v1/quickbooks/callback?state="+url.QueryEscape(state)+"&code=synthetic-code&realmId=123", nil)
	w := httptest.NewRecorder()
	quickbooksReturn(w, r, db)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "https://app.example.test/dashboard/closes/"+closeID+"?tab=evidence#") || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("unsafe callback routing, or not returned to the Evidence tab where the sandbox panel lives")
	}
	call("POST", base, callback, 200)
	call("POST", base, callback, 409)
	if exchanges != 1 {
		t.Fatal("authorization code replayed")
	}
	productionState := rand.Text()
	if _, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,environment) VALUES($1,$2,'QBO synthetic client','user_accountant','authorizing',$3,now()+interval '10 minutes','production')`, closeID, org, preparation.Digest([]byte(productionState))); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	quickbooksReturn(w, httptest.NewRequest("GET", "/v1/quickbooks/callback?state="+url.QueryEscape(productionState)+"&code=synthetic-code&realmId=123", nil), db)
	if w.Code != 303 || !strings.HasPrefix(w.Header().Get("Location"), "https://app.example.test/dashboard/closes/"+closeID+"?tab=settings#") {
		t.Fatalf("production authorization returns to Settings, where its panel lives: %s", w.Header().Get("Location"))
	}
	if _, err = db.Exec(`DELETE FROM quickbooks_connections WHERE oauth_digest=$1`, preparation.Digest([]byte(productionState))); err != nil {
		t.Fatal(err)
	}
	var production string
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,environment,realm,company_name) VALUES($1,$2,'QBO synthetic client','user_accountant','pending_confirmation',$3,now(),'production','999','Synthetic company') RETURNING id::text`, closeID, org, rand.Text()).Scan(&production); err != nil {
		t.Fatal(err)
	}
	call("POST", base, map[string]any{"action": "confirm", "connection_id": production, "confirmed": true, "realm": "999"}, 409)
	if _, err = db.Exec(`DELETE FROM quickbooks_connections WHERE id=$1`, production); err != nil {
		t.Fatal(err)
	}
	action := func(name string, want int) []byte {
		return call("POST", base, map[string]any{"action": name, "connection_id": start.ID, "confirmed": true, "realm": "123"}, want)
	}
	action("snapshot", 409)
	status := call("GET", base, nil, 200)
	if bytes.Contains(status, []byte("access-synthetic")) {
		t.Fatal("token exposed")
	}
	action("confirm", 200)
	if p, err := preparation.ClientProfile(context.Background(), db, closeID); err != nil || p != (preparation.Profile{Currency: "CAD", FiscalStart: 7, State: "detected"}) {
		t.Fatalf("connecting reads the company's home currency and fiscal year: %+v %v", p, err)
	}
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,environment,realm,company_name) VALUES($1,$2,'QBO synthetic client','user_accountant','active',$3,now(),'production','999','QBO synthetic client') RETURNING id::text`, closeID, org, rand.Text()).Scan(&production); err != nil {
		t.Fatal(err)
	}
	if refused := action("snapshot", 409); !strings.Contains(string(refused), "production") {
		t.Fatalf("once production is connected, the sandbox company is no longer read: %s", refused)
	}
	if _, err = db.Exec(`DELETE FROM quickbooks_connections WHERE id=$1`, production); err != nil {
		t.Fatal(err)
	}
	actor.Organization = other
	call("GET", base, nil, 404)
	actor.Organization = org
	actor.Permissions = []string{"clients:all"}
	action("snapshot", 403)
	actor.Permissions = []string{"clients:all", "integrations:manage"}
	db.Exec(`UPDATE quickbooks_connections SET access_expires_at=now()-interval '1 second' WHERE id=$1`, start.ID)
	var result struct {
		ID string `json:"snapshot_id"`
	}
	json.Unmarshal(action("snapshot", 200), &result)
	if refreshes != 1 || pages != 2 || result.ID == "" {
		t.Fatal("refresh/pagination incomplete", refreshes, pages)
	}
	raw := call("GET", base+"/snapshots/"+result.ID, nil, 200)
	if !bytes.Contains(raw, []byte("9007199254740993.01")) || !bytes.Contains(raw, []byte("Last account")) {
		t.Fatal("precision or final page lost")
	}
	connectDuringRead = true
	if refused := action("snapshot", 409); !strings.Contains(string(refused), "No snapshot saved") {
		t.Fatalf("a read that finishes after production was connected is not saved: %s", refused)
	}
	if _, err = db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1 AND environment='production'`, closeID); err != nil {
		t.Fatal(err)
	}
	var digest string
	db.QueryRow(`SELECT digest FROM quickbooks_snapshots WHERE id=$1`, result.ID).Scan(&digest)
	if preparation.Digest(raw) != digest {
		t.Fatal("download does not match snapshot digest")
	}
	var encrypted []byte
	db.QueryRow(`SELECT credential FROM quickbooks_connections WHERE id=$1`, start.ID).Scan(&encrypted)
	plain, e := secrets.Open(org+"/qbo/"+start.ID, encrypted)
	if e != nil || !bytes.Contains(plain, []byte("refresh-two")) {
		t.Fatal("rotated refresh token not persisted")
	}
	if _, e = secrets.Open(other+"/qbo/"+start.ID, encrypted); e == nil {
		t.Fatal("credential moved between firms")
	}
	failPage = true
	action("snapshot", 422)
	var count int
	db.QueryRow(`SELECT count(*) FROM quickbooks_snapshots WHERE close_id=$1`, closeID).Scan(&count)
	if count != 1 {
		t.Fatal("partial read saved as snapshot")
	}
	failPage = false
	periodMode = true
	badPeriod = true
	action("period_snapshot", 422)
	db.QueryRow(`SELECT count(*) FROM quickbooks_snapshots WHERE close_id=$1`, closeID).Scan(&count)
	if count != 1 {
		t.Fatal("wrong-period report was persisted")
	}
	badPeriod = false
	json.Unmarshal(action("period_snapshot", 200), &result)
	raw = call("GET", base+"/snapshots/"+result.ID, nil, 200)
	var saved struct {
		SourceID string `json:"source_id"`
		Coverage []struct {
			Kind            string
			ReportLocations []string `json:"report_locations"`
		}
	}
	json.Unmarshal(raw, &saved)
	if saved.SourceID == "" || len(saved.Coverage) != 2 || saved.Coverage[0].Kind != "reconciliation" || saved.Coverage[1].Kind != "prepaid" || len(saved.Coverage[1].ReportLocations) != 1 || !bytes.Contains(raw, []byte("9007199254740993.01")) {
		t.Fatal("linked coverage or precision lost", string(raw))
	}
	var content, digestEvidence string
	if e = db.QueryRow(`SELECT content,digest FROM evidence WHERE id=$1 AND close_id=$2`, saved.SourceID, closeID).Scan(&content, &digestEvidence); e != nil || preparation.Digest([]byte(content)) != digestEvidence {
		t.Fatal("evidence lineage missing", e)
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	inventory, _, e := preparation.AgentInventory(context.Background(), tx, closeID, "0")
	tx.Rollback()
	if e != nil || len(inventory) != 1 || inventory[0].ID != saved.SourceID {
		t.Fatal("ledger unavailable to agent", e)
	}
	tx, e = db.BeginTx(context.Background(), nil)
	if e != nil {
		t.Fatal(e)
	}
	var invoiceID string
	e = tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'Synthetic invoice.txt','synthetic-invoice','Invoice amount: 1200.00','fixture') RETURNING id::text`, closeID).Scan(&invoiceID)
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	job, e := preparation.StartIntake(context.Background(), tx, closeID, invoiceID, "fixture")
	if e != nil {
		tx.Rollback()
		t.Fatal(e)
	}
	tx.Exec(`UPDATE preparation_jobs SET state='cancelled' WHERE id=$1`, job)
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	actor.Permissions = append(actor.Permissions, "workpapers:review")
	ledgerPath := base + "/snapshots/" + result.ID + "/ledger?account_id=2&offset=0"
	var ledger struct {
		Total int `json:"total"`
		Rows  []struct {
			Location string `json:"location"`
		} `json:"rows"`
	}
	if e = json.Unmarshal(call("GET", ledgerPath, nil, 200), &ledger); e != nil || ledger.Total != 1 || len(ledger.Rows) != 1 || ledger.Rows[0].Location != "reports.GeneralLedger.Rows.Row[1]" {
		t.Fatal("account-scoped ledger selection lost its source location", e)
	}
	call("GET", base+"/snapshots/"+result.ID+"/ledger?account_id=999&offset=0", nil, 422)
	var selected struct {
		SourceID string `json:"source_id"`
	}
	if e = json.Unmarshal(call("POST", ledgerPath, map[string]bool{"confirmed": true}, 200), &selected); e != nil || selected.SourceID == "" {
		t.Fatal("ledger selection did not create evidence", e)
	}
	var selectedContent string
	if e = db.QueryRow(`SELECT content FROM evidence WHERE id=$1 AND close_id=$2`, selected.SourceID, closeID).Scan(&selectedContent); e != nil || !strings.Contains(selectedContent, "123.45") || strings.Contains(selectedContent, "9007199254740993.01") {
		t.Fatal("ledger evidence included another account or lost selected cells", e)
	}
	var replay struct {
		SourceID string `json:"source_id"`
	}
	json.Unmarshal(call("POST", ledgerPath, map[string]bool{"confirmed": true}, 200), &replay)
	if replay.SourceID != selected.SourceID {
		t.Fatal("ledger page replay duplicated evidence")
	}
	// A newer capture of the month, such as a scheduled refresh, which supersedes nothing, closes the older one to selections.
	var newer string
	if e = db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor,captured_at) SELECT close_id,connection_id,realm,digest||'-newer',payload,started_at,'scheduled-quickbooks-refresh',now()+interval '1 minute' FROM quickbooks_snapshots WHERE id::text=$1 RETURNING id::text`, result.ID).Scan(&newer); e != nil {
		t.Fatal(e)
	}
	call("POST", ledgerPath, map[string]bool{"confirmed": true}, 409)
	if _, e = db.Exec(`DELETE FROM quickbooks_snapshots WHERE id::text=$1`, newer); e != nil {
		t.Fatal(e)
	}
	before := call("GET", "/v1/closes/"+closeID+"/prepare-close", nil, 200)
	var scope struct {
		Digest string `json:"inventory_digest"`
	}
	json.Unmarshal(before, &scope)
	call("POST", "/v1/closes/"+closeID+"/prepare-close", map[string]any{"confirmed": true, "inventory_digest": scope.Digest}, 200)
	var pinned string
	if e = db.QueryRow(`SELECT scope->0->'ledger_coverage'->>'source_id' FROM close_preparations WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&pinned); e != nil || pinned != saved.SourceID {
		t.Fatal("run lost pinned ledger source", e)
	}
	action("period_snapshot", 200)
	var replacement string
	if e = db.QueryRow(`SELECT superseded_by::text FROM evidence WHERE id=$1`, saved.SourceID).Scan(&replacement); e != nil || replacement == "" {
		t.Fatal("prior ledger not superseded", e)
	}
	if e = db.QueryRow(`SELECT superseded_by::text FROM evidence WHERE id=$1`, selected.SourceID).Scan(&replacement); e != nil || replacement == "" {
		t.Fatal("selected ledger evidence was not superseded", e)
	}
	call("POST", "/v1/closes/"+closeID+"/prepare-close", map[string]any{"confirmed": true, "inventory_digest": scope.Digest}, 409)
	after := call("GET", "/v1/closes/"+closeID+"/prepare-close", nil, 200)
	if bytes.Equal(before, after) || !bytes.Contains(after, []byte("reconciliation")) {
		t.Fatal("coverage not refreshed")
	}
	largePeriod = true
	json.Unmarshal(action("period_snapshot", 200), &result)
	fullReport := call("GET", base+"/snapshots/"+result.ID, nil, 200)
	if len(fullReport) < 96*1024 {
		t.Fatal("large report fixture did not exceed the old evidence limit")
	}
	var indexSource string
	if e = db.QueryRow(`SELECT payload->>'source_id' FROM quickbooks_snapshots WHERE id=$1`, result.ID).Scan(&indexSource); e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(`SELECT content FROM evidence WHERE id=$1`, indexSource).Scan(&content); e != nil || len(content) > 96*1024 || !strings.Contains(content, "report_sizes") || strings.Contains(content, `"rows"`) {
		t.Fatal("large report was not represented by a bounded, explicit evidence index", e)
	}
	actor.Organization = other
	call("GET", base+"/snapshots/"+result.ID, nil, 404)
	actor.Organization = org
	periodMode = false
	failRefresh = true
	db.Exec(`UPDATE quickbooks_connections SET access_expires_at=now()-interval '1 second',credential=$2 WHERE id=$1`, start.ID, mustSealQBO(t, org+"/qbo/"+start.ID))
	action("snapshot", 422)
	action("snapshot", 409)
	if refreshes != 2 {
		t.Fatal("uncertain refresh repeated")
	}
	action("disconnect", 200)
	action("snapshot", 404)
	if e = db.QueryRowContext(context.Background(), `SELECT credential FROM quickbooks_connections WHERE id=$1`, start.ID).Scan(&encrypted); e != nil || len(encrypted) != 0 {
		t.Fatal("disconnect retained credentials")
	}
}
func mustSealQBO(t *testing.T, scope string) []byte {
	t.Helper()
	v, e := secrets.Seal(scope, []byte(`{"access_token":"old","refresh_token":"refresh-one"}`))
	if e != nil {
		t.Fatal(e)
	}
	return v
}
