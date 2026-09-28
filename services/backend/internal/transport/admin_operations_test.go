package transport

import (
	"crypto/rand"
	"strings"
	"testing"
)

func TestStaffSeeStuckWorkAndConnectionsAcrossFirms(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("o", 32)
	firm := "org_Ops" + rand.Text()
	var closeID string
	must := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO organizations(id,name) VALUES($1,'Operations firm')`, firm)
	if err := db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Secret Client','2026-09') RETURNING id::text`, firm).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM stripe_connections WHERE organization_id=$1`,
			`DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, firm)
		}
	})
	must(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','failed','fixture')`, closeID, rand.Text())
	must(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) VALUES($1,$2,'Acme Secret Client','fixture','reconnect_required',$3,now(),'123','Acme Secret Company')`, closeID, firm, rand.Text())
	must(`INSERT INTO stripe_connections(close_id,organization_id,client_entity,actor,state,livemode,key_last4,account_id,account_name) VALUES($1,$2,'Acme Secret Client','fixture','active',false,'4242','acct_x','Acme Secret Account')`, closeID, firm)
	h := apiWithStore(db, token)

	// Stuck and failed work across firms, by kind and for one firm.
	jobs := mustString(adminJSON(t, h, token, "GET", "/v1/admin/jobs?firm="+firm, "", 200))
	if !strings.Contains(jobs, `"kind":"preparation"`) || !strings.Contains(jobs, `"state":"failed"`) || !strings.Contains(jobs, `"firm_name":"Operations firm"`) || strings.Contains(jobs, "Acme") {
		t.Fatalf("jobs: %s", jobs)
	}
	if other := mustString(adminJSON(t, h, token, "GET", "/v1/admin/jobs?firm="+firm+"&kind=email", "", 200)["items"]); other != "[]" {
		t.Fatalf("the kind filter: %s", other)
	}
	if all := mustString(adminJSON(t, h, token, "GET", "/v1/admin/jobs", "", 200)); !strings.Contains(all, firm) {
		t.Fatalf("every firm's work is listed: %s", all)
	}

	// Connections: a reconnect comes first; client and company names never show.
	connections := mustString(adminJSON(t, h, token, "GET", "/v1/admin/connections?firm="+firm, "", 200))
	if strings.Contains(connections, "Acme") || !strings.Contains(connections, `"state":"reconnect_required"`) || !strings.Contains(connections, `"provider":"stripe"`) {
		t.Fatalf("connections: %s", connections)
	}
	list := adminJSON(t, h, token, "GET", "/v1/admin/connections?firm="+firm, "", 200)["connections"].([]any)
	if first := list[0].(map[string]any); first["state"] != "reconnect_required" {
		t.Fatalf("a reconnect is listed first: %v", list)
	}
	if only := adminJSON(t, h, token, "GET", "/v1/admin/connections?firm="+firm+"&provider=stripe", "", 200)["connections"].([]any); len(only) != 1 {
		t.Fatalf("the provider filter: %v", only)
	}
	adminJSON(t, h, token, "GET", "/v1/admin/connections?provider="+strings.Repeat("x", 101), "", 422)

	// The firm's own operations screen is unchanged by sharing the query.
	var kind string
	if err := db.QueryRow(`SELECT kind FROM (`+operationsAlertQuery+`) a`, firm).Scan(&kind); err != nil || kind != "preparation" {
		t.Fatalf("firm alerts: %q %v", kind, err)
	}
}
