package transport

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/ledgers"
	"github.com/golang-jwt/jwt/v5"
)

// A firm reads the ledger catalog with each connector's switch for it; staff count clients by the ledger firms
// recorded, the names given for other ledgers included and never a client's, and switch a connector on for one firm.
func TestTheLedgerCatalogAndItsCounts(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	t.Setenv("LEDGER_CONNECTORS", "quickbooks")
	token := strings.Repeat("g", 32)
	firm, other := "org_Ledgers"+rand.Text(), "org_Ledgers"+rand.Text()
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{firm, other} {
		must(`INSERT INTO organizations(id,name) VALUES($1,'Ledger firm')`, id)
	}
	asFirm(t, map[string]string{"user_a": "reviewer"}, firm)
	must(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, firm)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM client_ledgers WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM connector_pilots WHERE organization_id=$1`, `DELETE FROM platform_audit WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			for _, id := range []string{firm, other} {
				db.Exec(q, id)
			}
		}
	})
	// The firm records three clients: one on Xero, one on a ledger the catalog does not list, one not known yet, and
	// corrects the Xero client to QuickBooks Desktop; the other firm keeps a client on Xero.
	must(`INSERT INTO client_ledgers(organization_id,client_entity,ledger,other_name,actor) VALUES($1,'Acme Secret Co','xero','','fixture'),($1,'Beta Secret LLC','other','  Acumatica ','fixture'),
 ($1,'Gamma Secret Inc','unknown','','fixture'),($2,'Delta Secret Ltd','xero','','fixture')`, firm, other)
	must(`INSERT INTO client_ledgers(organization_id,client_entity,ledger,reason,actor) VALUES($1,'ACME SECRET CO','quickbooks_desktop','Corrected','fixture')`, firm)

	store := savedCloses(db, strings.Repeat("a", 32))
	catalog := func() map[string]bool {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/ledgers", bytes.NewReader(nil)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: firm, Permissions: []string{"workpapers:review"}}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
		}
		var out struct {
			Ledgers []catalogEntry `json:"ledgers"`
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		connected := map[string]bool{}
		for _, l := range out.Ledgers {
			connected[l.ID] = l.Connected
			if l.Exports.TrialBalance == "" {
				t.Fatalf("%s has its Trial Balance export steps", l.ID)
			}
		}
		return connected
	}
	// Only QuickBooks is switched on in this environment; Xero is served by files, with its export steps.
	if connected := catalog(); !connected["quickbooks_online"] || connected["xero"] || connected["sage_intacct"] {
		t.Fatalf("the catalog's switches: %v", connected)
	}

	h := apiWithStore(db, token)
	counts := adminJSON(t, h, token, "GET", "/v1/admin/ledgers", "", 200)
	if raw := mustString(counts); strings.Contains(raw, "Secret") {
		t.Fatalf("no client is named: %s", raw)
	}
	got := map[string]map[string]any{}
	for _, c := range counts["ledgers"].([]any) {
		c := c.(map[string]any)
		got[c["ledger"].(string)+":"+c["name"].(string)] = c
	}
	xero, desktop, acumatica, unknown := got["xero:Xero"], got["quickbooks_desktop:QuickBooks Desktop"], got["other:acumatica"], got["unknown:Not known yet"]
	if xero == nil || xero["clients"] != 1.0 || xero["connector"] != "xero" || xero["connected"] != false || desktop == nil || desktop["clients"] != 1.0 || desktop["connector"] != "" ||
		acumatica == nil || acumatica["clients"] != 1.0 || unknown == nil || unknown["clients"] != 1.0 {
		t.Fatalf("counts by ledger, the correction counted once: %v", got)
	}

	// Staff switch Xero on for the firm alone, with a reason; an unknown connector or no reason is refused.
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/connector-pilot", `{"connector":"sage","enabled":true,"reason":"Piloting the connector"}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/connector-pilot", `{"connector":"xero","enabled":true,"reason":""}`, 422)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/connector-pilot", `{"connector":"xero","enabled":true,"reason":"Piloting the Xero connector"}`, 200)
	if connected := catalog(); !connected["xero"] {
		t.Fatalf("the pilot switches Xero on for the firm: %v", connected)
	}
	var audited int
	if err := db.QueryRow(`SELECT count(*) FROM platform_audit WHERE organization_id=$1 AND action='connector.pilot'`, firm).Scan(&audited); err != nil || audited != 1 {
		t.Fatalf("the pilot is audited: %d %v", audited, err)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/connector-pilot", `{"connector":"xero","enabled":false,"reason":"Pilot finished for now"}`, 200)
	if connected := catalog(); connected["xero"] {
		t.Fatalf("switched off again: %v", connected)
	}
	// Switched off for the firm, a connector the environment has on is off for it, in the catalog and where its routes
	// and background reads ask.
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/connector-pilot", `{"connector":"quickbooks","enabled":false,"reason":"Firm asked to stop QuickBooks reads"}`, 200)
	if connected := catalog(); connected["quickbooks_online"] {
		t.Fatalf("an explicit off beats the environment's on: %v", connected)
	}
	var on, elsewhere bool
	if err := db.QueryRow(`SELECT `+ledgers.SwitchedSQL("$1", "quickbooks")+`,`+ledgers.SwitchedSQL("$2", "quickbooks"), firm, other).Scan(&on, &elsewhere); err != nil || on || !elsewhere {
		t.Fatalf("the routes' switch: %v %v %v", on, elsewhere, err)
	}
	// Xero access asks the connection's own firm: another firm's pilot, however recent, decides nothing for it.
	var closeID, connection string
	if err := db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Xero Secret Co','2026-09') RETURNING id::text`, other).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at) VALUES($1,$2,'Xero Secret Co','fixture','active',$3,now()) RETURNING id::text`, closeID, other, rand.Text()).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM xero_connections WHERE organization_id=$1`, other)
		db.Exec(`DELETE FROM client_closes WHERE organization_id=$1`, other)
	})
	t.Setenv("LEDGER_CONNECTORS", "quickbooks,xero")
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+firm+"/connector-pilot", `{"connector":"xero","enabled":false,"reason":"Another firm's switch"}`, 200)
	if _, _, err := xeroAccess(t.Context(), db, other, connection); err != nil && strings.Contains(err.Error(), "switched off") {
		t.Fatalf("another firm's pilot switched this firm off: %v", err)
	}
	adminJSON(t, h, token, "POST", "/v1/admin/firms/"+other+"/connector-pilot", `{"connector":"xero","enabled":false,"reason":"This firm's own switch"}`, 200)
	if _, _, err := xeroAccess(t.Context(), db, other, connection); err == nil || !strings.Contains(err.Error(), "switched off") {
		t.Fatalf("this firm's own pilot: %v", err)
	}
}

// A client's ledger is recorded when it is added and corrected on its page, under its own name, by whoever manages the
// roster or connects ledgers, against the ledger they read; Clients and the client's page show it.
func TestAClientsLedgerIsRecordedAndCorrected(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	t.Setenv("LEDGER_CONNECTORS", "")
	org := "org_ClientLedger" + rand.Text()
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name) VALUES($1,'Client ledger firm')`, org)
	asFirm(t, map[string]string{"user_a": "partner", "user_c": "reviewer"}, org)
	must(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_c',true,'Synthetic','fixture')`, org)
	must(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Maple Trading Ltd',true,'Synthetic','fixture')`, org)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM client_ledgers WHERE organization_id=$1`, `DELETE FROM client_roster WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`,
			`DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s: %d %s", subject, method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	admin := []string{"workpapers:review", "clients:all", "members:manage"}
	// Added with its ledger: Maple keeps Sage Intacct, whose connector this build has not, so it is served by files with
	// its export steps.
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Maple Co", "active": true, "since": "2026-08", "due_days": nil, "owner": nil, "reason": "", "rev": "", "ledger": "made_up", "other_name": ""}, 422)
	call("user_a", admin, "POST", "/v1/closes/roster", map[string]any{"entity": "Maple Co", "active": true, "since": "2026-08", "due_days": nil, "owner": nil, "reason": "", "rev": "", "ledger": "sage_intacct", "other_name": ""}, 200)
	read := call("user_a", admin, "GET", "/v1/closes/client?name=Maple%20Co", nil, 200)
	ledger := read["ledger"].(map[string]any)
	entry := ledger["entry"].(map[string]any)
	if ledger["ledger"] != "sage_intacct" || entry["name"] != "Sage Intacct" || entry["connected"] != false || entry["exports"].(map[string]any)["trial_balance"] == "" || ledger["reason"] != "Recorded when the client was added" {
		t.Fatalf("the client's ledger: %v", ledger)
	}
	if books := call("user_a", admin, "GET", "/v1/closes/roster", nil, 200)["connections"].(map[string]any)["maple co"].(map[string]any); books["ledger"] != "Sage Intacct" {
		t.Fatalf("Clients shows the ledger: %v", books)
	}
	// Corrected on its page, under its own name: by an alias it is still Maple; a stale read, a member assigned to the
	// alias alone, and one who neither manages the roster nor connects ledgers are refused.
	must(`INSERT INTO client_closes(organization_id,entity,period,created_at) VALUES($1,'Maple Trading Ltd','2026-09',now()-interval '1 day')`, org)
	must(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Maple Co','maple trading ltd','fixture',now()-interval '2 days')`, org)
	correct := map[string]any{"client": "Maple Trading Ltd", "ledger": "other", "other_name": "Acumatica", "reason": "The client moved off Sage last year", "rev": ledger["id"]}
	call("user_e", []string{"workpapers:review"}, "POST", "/v1/closes/client/ledger", correct, 403)
	call("user_c", []string{"workpapers:review", "integrations:manage"}, "POST", "/v1/closes/client/ledger", correct, 403)
	stale := map[string]any{"client": "Maple Co", "ledger": "xero", "other_name": "", "reason": "Wrong", "rev": ""}
	call("user_a", admin, "POST", "/v1/closes/client/ledger", stale, 409)
	saved := call("user_a", admin, "POST", "/v1/closes/client/ledger", correct, 200)["ledger"].(map[string]any)
	if saved["ledger"] != "other" || saved["other_name"] != "Acumatica" || saved["entry"] != nil {
		t.Fatalf("a named other ledger: %v", saved)
	}
	call("user_a", admin, "POST", "/v1/closes/client/ledger", map[string]any{"client": "Maple Co", "ledger": "other", "other_name": "", "reason": "No name", "rev": saved["id"]}, 422)
	// Not known yet is a choice too, and says nothing on Clients.
	call("user_a", admin, "POST", "/v1/closes/client/ledger", map[string]any{"client": "Maple Co", "ledger": "unknown", "other_name": "", "reason": "Checking with the client", "rev": saved["id"]}, 200)
	if books, ok := call("user_a", admin, "GET", "/v1/closes/roster", nil, 200)["connections"].(map[string]any)["maple co"].(map[string]any); ok && books["ledger"] != nil {
		t.Fatalf("not known yet says nothing: %v", books)
	}
	call("user_a", admin, "POST", "/v1/closes/client/ledger", map[string]any{"client": "Nobody Ltd", "ledger": "xero", "reason": "x", "rev": ""}, 404)
}
