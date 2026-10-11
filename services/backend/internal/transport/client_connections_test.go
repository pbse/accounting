package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/quickbooks"
	"github.com/golang-jwt/jwt/v5"
)

// A connection kept under the name of the month it was made from, since confirmed as the client's alias, is managed
// from the client's page under its own name; a connection of another client is never reached that way.
func TestTheClientPageManagesConnectionsHeldUnderAnAlias(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
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
	org := "org_Held" + rand.Text()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Held fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	// Acme's September month was opened as Acme Services LLC, later confirmed as its alias; its QuickBooks and Xero
	// connections were made from that month. Birch is another client with its own connection.
	var september, birch, qbo, xeroID, other string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,created_at) VALUES($1,'Acme Services LLC','2026-09',now()-interval '2 days') RETURNING id::text`, org).Scan(&september))
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2026-10')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Acme Co','acme services llc','fixture',now()-interval '3 days')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,$2,'Acme Services LLC','fixture','reconnect_required',$3,now(),'901') RETURNING id::text`, september, org, rand.Text()).Scan(&qbo))
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_name) VALUES($1,$2,'Acme Services LLC','fixture','active',$3,now(),'Acme Services') RETURNING id::text`, september, org, rand.Text()).Scan(&xeroID))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Birch Dental','2026-09') RETURNING id::text`, org).Scan(&birch))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,$2,'Birch Dental','fixture','active',$3,now(),'902') RETURNING id::text`, birch, org, rand.Text()).Scan(&other))

	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}
	mux := http.NewServeMux()
	registerClientConnections(mux, db)
	h := organizationBoundary(db, mux)
	call := func(path string, body any, want int) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r.WithContext(auth.WithActor(r.Context(), actor)))
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	state := func(table, id string) string {
		t.Helper()
		var s string
		must(db.QueryRow(`SELECT state FROM `+table+` WHERE id::text=$1`, id).Scan(&s))
		return s
	}
	acme := "?name=" + url.QueryEscape("Acme Co")
	// Birch's connection is not reached through Acme's page.
	call("/v1/closes/client/quickbooks"+acme, map[string]any{"action": "disconnect", "confirmed": true, "connection_id": other}, 404)
	if state("quickbooks_connections", other) != "active" {
		t.Fatal("another client's connection was changed")
	}
	// Acme's own connections, held under its alias, are.
	call("/v1/closes/client/quickbooks"+acme, map[string]any{"action": "disconnect", "confirmed": true, "connection_id": qbo}, 200)
	call("/v1/closes/client/xero"+acme, map[string]any{"action": "disconnect", "confirmed": true, "connection_id": xeroID}, 200)
	if state("quickbooks_connections", qbo) != "disconnected" || state("xero_connections", xeroID) != "disconnected" {
		t.Fatal("the alias' connections are managed from the client's page")
	}
	// Connected again from the client's page, under its own name, the connection serves the month opened under the
	// alias: the month reads through the client's connection.
	var fresh, reading string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,'Acme Co','fixture','active',$2,now(),'903') RETURNING id::text`, org, rand.Text()).Scan(&fresh))
	must(db.QueryRow(`SELECT COALESCE(`+quickbooks.ReadingConnection("$1::bigint")+`::text,'')`, september).Scan(&reading))
	if reading != fresh {
		t.Fatalf("the alias month reads through the client's connection: %q, not %q", reading, fresh)
	}
	// A production connection, under the client's own name and its company's, comes first for every month of the
	// client; its posting grant, made on the client's page, is the alias month's grant too.
	var production string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment) VALUES($1,'Acme Co','fixture','active',$2,now(),'904','Acme Co','production') RETURNING id::text`, org, rand.Text()).Scan(&production))
	must(db.QueryRow(`SELECT COALESCE(`+quickbooks.ReadingConnection("$1::bigint")+`::text,'')`, september).Scan(&reading))
	if reading != production {
		t.Fatalf("production comes first: %q, not %q", reading, production)
	}
	_, err = db.Exec(`INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,account_ids,kinds,max_amount,expires_at,reason,granted_by) VALUES($1,'Acme Co',$2,'904',ARRAY['1','2'],ARRAY['prepaid'],100,now()+interval '1 day','Synthetic','fixture')`, org, production)
	must(err)
	t.Cleanup(func() { db.Exec(`DELETE FROM production_posting_grants WHERE organization_id=$1`, org) })
	if g, e := readProductionGrant(t.Context(), db, org, "Acme Services LLC"); e != nil || g.ConnectionID != production {
		t.Fatalf("the alias month's grant: %+v %v", g, e)
	}
}
