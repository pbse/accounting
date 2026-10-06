package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestATakeoverSetupIsConfirmedOnceOnTheOpeningMonth(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	entity := "Setup " + rand.Text()
	defer func() {
		for _, q := range []string{`DELETE FROM procedure_versions WHERE client_entity=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM quickbooks_connections WHERE client_entity=$1`, `DELETE FROM client_closes WHERE entity=$1`, `DELETE FROM client_roster WHERE client_entity=$1`} {
			db.Exec(q, entity)
		}
	}()
	token := strings.Repeat("s", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var opening struct{ ID string }
	must(json.Unmarshal(call("POST", "/v1/closes", map[string]string{"entity": entity, "period": "2026-08"}, 200), &opening))
	path := "/v1/closes/" + opening.ID + "/takeover-setup"
	type setup struct {
		Digest   string
		Problems []string
		Content  struct {
			Companies []struct{ Provider, Company string }
			Scope     struct {
				Unavailable string
				Items       []struct{ Name string }
			}
		}
	}
	read := func() setup {
		t.Helper()
		var out setup
		must(json.Unmarshal(call("GET", path, nil, 200), &out))
		return out
	}
	blockers := func() string {
		t.Helper()
		var out struct{ Blockers []string }
		must(json.Unmarshal(call("GET", "/v1/closes/"+opening.ID+"/readiness", nil, 200), &out))
		return strings.Join(out.Blockers, "\n")
	}
	if b := blockers(); !strings.Contains(b, "Confirm the takeover setup") {
		t.Fatalf("the opening month waits for its takeover setup: %s", b)
	}
	first := read()
	if len(first.Problems) != 2 || !strings.Contains(first.Problems[0], "Connect the client's QuickBooks or Xero company") || !strings.Contains(first.Problems[1], "accounting basis") || !strings.Contains(first.Content.Scope.Unavailable, "No QuickBooks capture") {
		t.Fatalf("a client with no company or basis cannot be confirmed: %+v", first)
	}
	for _, zone := range []string{"", "Local", "Mars/Olympus"} {
		call("POST", path, map[string]any{"digest": first.Digest, "timezone": zone, "confirmed": true}, 422)
	}
	call("POST", path, map[string]any{"digest": first.Digest, "timezone": "America/New_York", "confirmed": true}, 409)

	var sandbox, evidenceID string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) SELECT id,organization_id,entity,'fixture','active',$2,now(),'987','Acme Books' FROM client_closes WHERE id::text=$1 RETURNING id::text`, opening.ID, rand.Text()).Scan(&sandbox))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, opening.ID, rand.Text()).Scan(&evidenceID))
	payload, _ := json.Marshal(map[string]any{"period": "2026-08", "source_id": evidenceID, "coverage": []map[string]string{{"account_id": "5", "name": "Prepaid insurance", "kind": "prepaid", "question": "Which schedule supports it?"}}})
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1::bigint,$2,'987',$3,$4,now(),'fixture')`, opening.ID, sandbox, rand.Text(), string(payload))
	must(err)
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) SELECT organization_id,entity,'basis','Synthetic',true,'Synthetic basis','fixture','accrual' FROM client_closes WHERE id::text=$1`, opening.ID)
	must(err)
	current := read()
	if len(current.Problems) != 0 || len(current.Content.Companies) != 1 || current.Content.Companies[0].Company != "987" || len(current.Content.Scope.Items) != 1 || current.Content.Scope.Items[0].Name != "Prepaid insurance" {
		t.Fatalf("the company the books are read from and the basis are confirmed: %+v", current)
	}
	call("POST", path, map[string]any{"digest": first.Digest, "timezone": "America/New_York", "confirmed": true}, 409)
	call("POST", path, map[string]any{"digest": current.Digest, "timezone": "America/New_York", "confirmed": true}, 200)
	// The client taken over joins the firm's roster, owing a close each month from its opening month.
	var since string
	must(db.QueryRow(`SELECT since FROM current_client_roster WHERE client_entity=$1 AND active`, entity).Scan(&since))
	if since != "2026-08" {
		t.Fatalf("the client owes from its opening month: %s", since)
	}
	call("POST", path, map[string]any{"digest": current.Digest, "timezone": "America/New_York", "confirmed": true}, 409)
	// Confirming the setup again (here for another timezone) leaves a client an administrator marked inactive inactive.
	_, err = db.Exec(`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES('local-development',$1,false,'2026-08','Left the firm','fixture')`, entity)
	must(err)
	call("POST", path, map[string]any{"digest": current.Digest, "timezone": "Europe/London", "confirmed": true}, 200)
	var active bool
	must(db.QueryRow(`SELECT active FROM current_client_roster WHERE client_entity=$1`, entity).Scan(&active))
	if active {
		t.Fatal("a reconfirmed setup does not reactivate a client")
	}
	if b := blockers(); strings.Contains(b, "Confirm the takeover setup") {
		t.Fatalf("a confirmed setup no longer holds the opening month: %s", b)
	}
	// A confirmed setup keeps its month the opening one, even before its baseline is reviewed.
	call("POST", "/v1/closes", map[string]string{"entity": entity, "period": "2026-07"}, 200)
	var flags string
	must(db.QueryRow(`SELECT string_agg(period||'='||takeover::text,',' ORDER BY period) FROM client_closes WHERE entity=$1`, entity).Scan(&flags))
	if flags != "2026-07=false,2026-08=true" {
		t.Fatalf("the confirmed opening month stays the opening month: %s", flags)
	}
	// Once production is connected the books are read from it, so the sandbox capture no longer proposes the scope.
	_, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment) SELECT id,organization_id,entity,'fixture','active',$2,now(),'555','Acme Live','production' FROM client_closes WHERE id::text=$1`, opening.ID, rand.Text())
	must(err)
	if live := read(); len(live.Content.Companies) != 1 || live.Content.Companies[0].Company != "555" || !strings.Contains(live.Content.Scope.Unavailable, "not of the company the books are read from") {
		t.Fatalf("the scope comes only from a capture of the company the books are read from: %+v", live.Content)
	}
}
