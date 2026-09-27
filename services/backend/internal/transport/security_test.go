package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestSecurityCenterShowsAccessServicesAgentsAndNoSecretsToAdministratorsOnly(t *testing.T) {
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
	org := "org_Security" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Security fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM model_calls WHERE organization_id=$1`,
			`DELETE FROM production_posting_grants WHERE organization_id=$1`, `DELETE FROM xero_posting_grants WHERE organization_id=$1`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`,
			`DELETE FROM email_connections WHERE organization_id=$1`, `DELETE FROM agent_access WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	var alpha, beta, membership, qbo, xero, betaBooks string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	must(db.QueryRow(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_admin',true,'Firm administrator','fixture'),($1,'user_a',true,'Staff accountant','user_admin'),($1,'user_b',true,'Seasonal help','user_admin') RETURNING id::text`, org).Scan(&membership))
	must(db.QueryRow(`SELECT id::text FROM organization_member_access WHERE organization_id=$1 AND subject='user_a'`, org).Scan(&membership))
	_, err = db.Exec(`INSERT INTO agent_access(organization_id,subject,state,scope,member_access_id,reason,actor) SELECT $1,'user_b','approved','read',id,'Reads with an assistant','user_admin' FROM organization_member_access WHERE organization_id=$1 AND subject='user_b'`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_b',false,'Season ended','user_admin')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Assigned','user_admin'),($1,'user_b','Beta',true,'Assigned','user_admin'),($1,'user_a','Beta',false,'Not on this client','user_admin')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO agent_access(organization_id,subject,state,scope,member_access_id,reason,actor) VALUES($1,'user_a','approved','prepare',$2,'Prepares with Claude Code','user_admin')`, org, membership)
	must(err)
	_, err = db.Exec(`INSERT INTO agent_access(organization_id,subject,state,scope,member_access_id,reason,actor) VALUES($1,'user_a','requested','read',$2,'Asking for read only','user_a')`, org, membership)
	must(err)
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,company_name,credential,environment,realm) VALUES($1,$2,'Alpha','user_a','active','SECRET-OAUTH',now(),'Alpha','SECRET-CREDENTIAL','production','realm-1') RETURNING id::text`, alpha, org).Scan(&qbo))
	_, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,company_name,environment) VALUES($1,$2,'Alpha','user_a','disconnected','old',now(),'Old Books','sandbox')`, alpha, org)
	must(err)
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,credential) VALUES($1,$2,'Alpha','user_a','disconnected','SECRET-XERO',now(),'SECRET-XERO-CREDENTIAL') RETURNING id::text`, alpha, org).Scan(&xero))
	_, err = db.Exec(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,actor,webhook_secret,enabled) VALUES($1,'close@firm.test','domain','SECRET-EMAIL','user_admin','SECRET-HOOK',true)`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,max_amount,expires_at,reason,granted_by,account_ids,kinds) VALUES($1,'Alpha',$2,'realm-1',500,now()+interval '30 days','Month-end accruals','user_admin','{"61","62"}','{"accrual"}')`, org, qbo)
	must(err)
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,company_name,environment,realm) VALUES($1,$2,'Beta','user_b','active','x',now(),'Beta','production','realm-2') RETURNING id::text`, beta, org).Scan(&betaBooks))
	_, err = db.Exec(`INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,max_amount,expires_at,reason,granted_by,account_ids,kinds) VALUES($1,'Beta',$2,'realm-2',900,now()+interval '30 days','Granted by someone who left','user_b','{"61","62"}','{"prepaid"}')`, org, betaBooks)
	must(err)
	_, err = db.Exec(`INSERT INTO xero_posting_grants(organization_id,client_entity,connection_id,tenant_id,account_ids,kinds,max_amount,expires_at,reason,granted_by,revoked_by,revoked_at) VALUES($1,'Alpha',$2,'tenant-1','{"200","400"}','{"line_recode"}',100,now()+interval '30 days','Recodes','user_admin','user_admin',now())`, org, xero)
	must(err)
	_, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by,agent) VALUES($1,'invoice.txt','security-agent','Invoice','user_a','claude-code')`, alpha)
	must(err)
	_, err = db.Exec(`INSERT INTO model_calls(organization_id,day,calls) VALUES($1,(now() AT TIME ZONE 'UTC')::date,3)`, org)
	must(err)

	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, want int) string {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/security", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %v: %d %s", subject, permissions, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	call("user_a", nil, 403)
	call("user_a", []string{"clients:all"}, 403)
	call("user_a", []string{"members:manage"}, 403)
	body := call("user_admin", []string{"clients:all", "members:manage"}, 200)
	if strings.Contains(body, "SECRET") {
		t.Fatalf("no credential, OAuth digest or webhook secret leaves the server: %s", body)
	}
	var got struct {
		OrganizationID string `json:"organization_id"`
		People         []struct {
			Subject     string
			Granted     bool
			Assignments []string
			Agent       *struct {
				State, Scope string
			}
			AgentScopeInForce *string `json:"agent_scope_in_force"`
		}
		Services []struct {
			Provider, Client, Account, Mode, State string
			CloseID                                string `json:"close_id"`
		}
		Email         *struct{ Sender string }
		PostingGrants []struct {
			Provider, Client string
			CloseID          string   `json:"close_id"`
			AccountIDs       []string `json:"account_ids"`
			Kinds            []string
		} `json:"posting_grants"`
		Activity []struct {
			Actor, Text string
		}
		Providers []struct {
			Provider, Evidence string
			InUse              bool `json:"in_use"`
		}
		Clients []string
	}
	must(json.Unmarshal([]byte(body), &got))
	people := map[string]int{}
	for i, p := range got.People {
		people[p.Subject] = i
	}
	a, b := got.People[people["user_a"]], got.People[people["user_b"]]
	if len(got.People) != 3 || !a.Granted || strings.Join(a.Assignments, ",") != "Alpha" || a.Agent == nil || a.Agent.State != "requested" || a.Agent.Scope != "read" || a.AgentScopeInForce == nil || *a.AgentScopeInForce != "prepare" || b.Granted || strings.Join(b.Assignments, ",") != "Beta" || b.Agent == nil || b.AgentScopeInForce != nil {
		t.Fatalf("each member's decision, recorded assignments and agent access in force: %+v", got.People)
	}
	alphaBooks := got.Services[len(got.Services)-1]
	if len(got.Services) != 2 || alphaBooks.Provider != "QuickBooks" || alphaBooks.Account != "Alpha" || alphaBooks.Mode != "production" || alphaBooks.CloseID != alpha || got.Email == nil || got.Email.Sender != "close@firm.test" {
		t.Fatalf("connections that are not disconnected, with where to manage them: %+v %+v", got.Services, got.Email)
	}
	if len(got.PostingGrants) != 1 || got.PostingGrants[0].Provider != "QuickBooks" || got.PostingGrants[0].CloseID != alpha || strings.Join(got.PostingGrants[0].AccountIDs, ",") != "61,62" || strings.Join(got.PostingGrants[0].Kinds, ",") != "accrual" || strings.Contains(body, "key ending") {
		t.Fatalf("posting authority in force, with its accounts and kinds; a revoked grant is not: %+v", got.PostingGrants)
	}
	texts := []string{}
	for _, e := range got.Activity {
		texts = append(texts, e.Actor+": "+e.Text)
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"user_a: Agent claude-code added evidence invoice.txt to Alpha 2026-09", "user_admin: Revoked user_b: Season ended", "user_admin: Agent access approved (prepare) for user_a", "user_admin: Revoked Xero posting for Alpha", "user_a: Connected QuickBooks (production) for Alpha"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("activity includes %q:\n%s", want, joined)
		}
	}
	providers := map[string]bool{}
	for _, p := range got.Providers {
		providers[p.Provider] = p.InUse
		if p.Provider == "OpenAI" && !strings.HasPrefix(p.Evidence, "3 model calls reserved") {
			t.Fatalf("model use is shown as the reserved budget it is: %+v", p)
		}
	}
	if !providers["OpenAI"] || !providers["Resend"] || !providers["QuickBooks"] || providers["Xero"] || providers["Stripe"] {
		t.Fatalf("which outside services are in use: %+v", got.Providers)
	}
	if got.OrganizationID != org || strings.Join(got.Clients, ",") != "Alpha,Beta" {
		t.Fatalf("the firm and every client, for the controls: %s %v", got.OrganizationID, got.Clients)
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id=$1`, qbo)
	must(err)
	got.PostingGrants = nil
	must(json.Unmarshal([]byte(call("user_admin", []string{"clients:all", "members:manage"}, 200)), &got))
	if len(got.PostingGrants) != 0 {
		t.Fatalf("a grant on a connection that needs reconnecting is not posting authority in force: %+v", got.PostingGrants)
	}
}
