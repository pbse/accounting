package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestSetupFollowsTheFirmsOwnRecords(t *testing.T) {
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
	exec := func(q string, args ...any) {
		t.Helper()
		_, e := db.Exec(q, args...)
		must(e)
	}
	org := "org_Setup" + rand.Text()
	exec(`INSERT INTO organizations(id,name) VALUES($1,'Setup fixture')`, org)
	exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	defer func() {
		for _, q := range []string{`DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`, `DELETE FROM procedure_versions WHERE organization_id=$1`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	type setup struct {
		Steps []struct {
			Key    string
			Done   bool
			Detail string
			Href   string
		}
		Permissions  []string
		Reconnect    []setupReconnect
		CanReconnect bool `json:"can_reconnect"`
	}
	read := func() (setup, map[string]bool) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/setup", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review"}}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("setup: %d %s", w.Code, w.Body.String())
		}
		var s setup
		must(json.Unmarshal(w.Body.Bytes(), &s))
		done := map[string]bool{}
		for _, step := range s.Steps {
			done[step.Key] = step.Done
		}
		return s, done
	}
	if s, done := read(); !done["firm"] || done["team"] || done["client"] || done["ledger"] || done["rules"] || done["prepared"] || len(s.Steps) != 6 {
		t.Fatalf("a new firm has only its enrollment done: %+v", s.Steps)
	}
	exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_b',true,'Synthetic','fixture')`, org)
	var alpha, beta string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-10') RETURNING id::text`, org).Scan(&beta))
	s, done := read()
	if !done["team"] || !done["client"] || done["rules"] || !strings.Contains(s.Steps[4].Detail, "Alpha · 2026-09") || s.Steps[4].Href != "/dashboard/closes/"+alpha+"?tab=settings" {
		t.Fatalf("a teammate and a close the member can open count; the period is the member's newest close, not another client's: %+v", s.Steps)
	}
	exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,'','prepaid','Synthetic',true,'Synthetic basis','fixture','')`, org)
	if s, done = read(); !done["rules"] || !strings.Contains(s.Steps[4].Detail, "for prepaid") {
		t.Fatalf("the firm's enabled procedure applies to the client: %+v", s.Steps[4])
	}
	exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,'Alpha','prepaid','Synthetic',false,'Client opted out','fixture','')`, org)
	if _, done = read(); done["rules"] {
		t.Fatal("a disabled client override suppresses the firm's procedure")
	}
	exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,'','basis','Synthetic',true,'Synthetic basis','fixture','')`, org)
	if s, done = read(); !done["rules"] || !strings.Contains(s.Steps[4].Detail, "for basis") {
		t.Fatalf("any kind of enabled procedure applies, including the accounting basis: %+v", s.Steps[4])
	}
	exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,environment,error) VALUES($1,$2,'Alpha','fixture','reconnect_required',$3,now(),'456','production','Token refresh did not complete; reconnect before syncing')`, alpha, org, rand.Text())
	if _, done = read(); done["ledger"] {
		t.Fatal("a connection that needs reconnecting does not connect the books")
	}
	exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,environment) VALUES($1,$2,'Alpha','fixture','active',$3,now(),'123','sandbox')`, alpha, org, rand.Text())
	if _, done = read(); done["ledger"] {
		t.Fatal("an active sandbox company does not connect books whose captures read the broken production company")
	}
	exec(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','setup-draft','{}')`, alpha)
	var paper string
	must(db.QueryRow(`SELECT id::text FROM workpapers WHERE close_id::text=$1`, alpha).Scan(&paper))
	exec(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,'Beta','fixture','reconnect_required',$3,now(),'t-1')`, beta, org, rand.Text())
	s, done = read()
	if !done["prepared"] || s.Steps[5].Href != "/dashboard/closes/"+alpha+"/workpapers/"+paper {
		t.Fatalf("a draft completes its step with its link: %+v", s.Steps)
	}
	if len(s.Reconnect) != 1 || s.Reconnect[0].Client != "Alpha" || s.Reconnect[0].Href != "/dashboard/closes/"+alpha+"?tab=settings#quickbooks-production" || !strings.Contains(s.Reconnect[0].Error, "Token refresh") || s.CanReconnect {
		t.Fatalf("only the member's client's broken connection is listed, linked to its production panel: %+v", s.Reconnect)
	}
	exec(`UPDATE quickbooks_connections SET state='disconnected' WHERE organization_id=$1 AND environment='production'`, org)
	if _, done = read(); !done["ledger"] {
		t.Fatal("once the sandbox company is the one read, the books are connected")
	}
	if !slices.Contains(s.Permissions, "Review and approve drafts, and start or stop preparation") || !slices.Contains(s.Permissions, "See only the clients assigned to you") || slices.Contains(s.Permissions, "Suspend the firm") {
		t.Fatalf("permissions in plain words: %v", s.Permissions)
	}
}
