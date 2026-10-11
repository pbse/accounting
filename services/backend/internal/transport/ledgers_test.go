package transport

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
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
}
