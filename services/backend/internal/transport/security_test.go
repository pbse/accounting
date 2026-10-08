package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/llm"
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
		for _, q := range []string{`DELETE FROM production_postings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM bill_corrections WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_entity_syncs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM model_calls WHERE organization_id=$1`,
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
	var evidenceID, captureID, paperID string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,agent) VALUES($1,'invoice.txt','security-agent','Invoice','user_a','claude-code') RETURNING id::text`, alpha).Scan(&evidenceID))
	// Operation evidence: a read of Alpha's live books, a sandbox Bill recode, a verified live accrual journal, and another
	// firm's live loan journal, which must not count here.
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at) VALUES($1,$2,'2026-09','fixture','unchanged',now()-interval '1 hour') RETURNING id::text`, alpha, qbo).Scan(&captureID))
	_, err = db.Exec(`INSERT INTO bill_corrections(close_id,capture_id,evidence_id,realm,bill_id,treatment,reason,target_account_id,target_account_name,target_line_id,before_bill,digest,state,proposed_by,verified_at) VALUES($1,$2,$3,'realm-sandbox','bill-1','inconsistent_coding','Coding rule','7','Supplies','1','{}','d','verified','user_a',now()-interval '3 days')`, alpha, captureID, evidenceID)
	must(err)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'accrual','capability-accrual','{}') RETURNING id::text`, alpha).Scan(&paperID))
	_, err = db.Exec(`INSERT INTO production_postings(close_id,workpaper_id,source_key,grant_id,connection_id,realm,digest,journal,ledger_before,state,verified_at) SELECT $1,$2,'source-1',id,$3,'realm-1','d','{}','{}','verified',now()-interval '2 hours' FROM production_posting_grants WHERE organization_id=$4 AND client_entity='Alpha'`, alpha, paperID, qbo, org)
	must(err)
	other := "org_SecurityOther" + rand.Text()
	var otherClose, otherBooks, otherPaper string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, other)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM production_postings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM production_posting_grants WHERE organization_id=$1`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, other)
		}
	}()
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Gamma','2026-09') RETURNING id::text`, other).Scan(&otherClose))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,company_name,environment,realm) VALUES($1,$2,'Gamma','user_o','active','o',now(),'Gamma','production','realm-o') RETURNING id::text`, otherClose, other).Scan(&otherBooks))
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'loan','capability-loan','{}') RETURNING id::text`, otherClose).Scan(&otherPaper))
	_, err = db.Exec(`INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,max_amount,expires_at,reason,granted_by,account_ids,kinds) VALUES($1,'Gamma',$2,'realm-o',500,now()+interval '30 days','Loans','user_o','{"1","2"}','{"loan"}')`, other, otherBooks)
	must(err)
	_, err = db.Exec(`INSERT INTO production_postings(close_id,workpaper_id,source_key,grant_id,connection_id,realm,digest,journal,ledger_before,state,verified_at) SELECT $1,$2,'source-o',id,$3,'realm-o','d','{}','{}','verified',now() FROM production_posting_grants WHERE organization_id=$4`, otherClose, otherPaper, otherBooks, other)
	must(err)
	_, err = db.Exec(`INSERT INTO model_calls(organization_id,day,calls) VALUES($1,(now() AT TIME ZONE 'UTC')::date,3)`, org)
	must(err)

	t.Setenv("PRODUCTION_POSTING_KINDS", "prepaid,accrual")
	t.Setenv("XERO_POSTING_KINDS", "accrual")
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("QBO_PRODUCTION_CLIENT_ID", "client")
	t.Setenv("QBO_PRODUCTION_CLIENT_SECRET", "secret")
	t.Setenv("QBO_PRODUCTION_REDIRECT_URI", "https://app.example.test/v1/quickbooks/callback")
	store := savedCloses(db, strings.Repeat("a", 32))
	// The firm's model calls go to Claude through the private context machine.
	models, err := llm.New(llm.Config{Provider: "context_machine", ContextMachineToken: "fixture-token", ContextMachineModel: "claude", ClaudeModel: "claude-fixture", Profiles: []llm.Scope{{Organization: org, Subject: "user_admin", AccessProfile: "fixture"}}})
	must(err)
	call := func(subject string, permissions []string, want int) string {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/security", nil).WithContext(auth.WithActor(llm.WithClient(t.Context(), models), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
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
	// Until the firm turns writing to clients' books on, no write is enabled for anyone, whatever the server and the
	// grants allow; reads are unaffected.
	off := call("user_admin", []string{"clients:all", "members:manage"}, 200)
	var offOps struct {
		Operations []struct {
			Provider, Kind, Operation, Status string
			Clients                           []string
		}
	}
	must(json.Unmarshal([]byte(off), &offOps))
	for _, o := range offOps.Operations {
		if o.Kind != "read" && (o.Status == "enabled" || o.Status == "production approved" || len(o.Clients) > 0) {
			t.Fatalf("a write is enabled while the firm's writing is off: %+v", o)
		}
		if o.Provider == "QuickBooks" && o.Operation == "Journal: accrual" && o.Status != "off in Firm settings" {
			t.Fatalf("an allowed, granted write says the firm's writing is off: %+v", o)
		}
	}
	if _, err := db.Exec(`UPDATE organizations SET allow_ledger_writes=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
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
		Clients    []string
		Operations []struct {
			Provider, Operation, Status, Owner string
			Clients                            []string
			SandboxAt                          *time.Time `json:"sandbox_verified_at"`
			LiveAt                             *time.Time `json:"live_verified_at"`
			SandboxRecorded                    bool       `json:"sandbox_recorded"`
			SandboxClose                       *int64     `json:"sandbox_close_id"`
			LiveClose                          *int64     `json:"live_close_id"`
		}
	}
	must(json.Unmarshal([]byte(body), &got))
	// Each operation says how far it is proven and enabled: a kind the server allows and a grant in force covers is
	// enabled for that client, one the server allows without a grant is production approved, a verified sandbox record
	// makes it sandbox tested, and anything else is only documented.
	operations := map[string]string{}
	for _, o := range got.Operations {
		key := o.Provider + " " + o.Operation
		operations[key] = o.Status + " " + strings.Join(o.Clients, ",") + fmt.Sprintf(" sandbox=%v live=%v recorded=%v", o.SandboxAt != nil, o.LiveAt != nil, o.SandboxRecorded)
	}
	for op, want := range map[string]string{
		"QuickBooks Journal: accrual":                           "enabled Alpha sandbox=false live=true recorded=false",
		"QuickBooks Journal: prepaid adjustment":                "production approved  sandbox=false live=false recorded=true",
		"QuickBooks Journal: loan interest and current portion": "documented  sandbox=false live=false recorded=false",
		"QuickBooks Bill line recode":                           "sandbox tested  sandbox=true live=false recorded=true",
		"QuickBooks Reports and transactions":                   "enabled Alpha,Beta sandbox=false live=true recorded=true",
		"Xero Bill line recode":                                 "documented  sandbox=false live=false recorded=false",
		"Xero Manual journal: accrual":                          "production approved  sandbox=false live=false recorded=false",
		"Stripe Payout and balance reports":                     "production approved  sandbox=false live=false recorded=true",
	} {
		if operations[op] != want {
			t.Fatalf("%s: %q, want %q\n%v", op, operations[op], want, operations)
		}
	}
	// Each date links to the close that holds the record behind it.
	for _, o := range got.Operations {
		if (o.Operation == "Journal: accrual" && (o.LiveClose == nil || fmt.Sprint(*o.LiveClose) != alpha)) || (o.Provider == "QuickBooks" && o.Operation == "Bill line recode" && (o.SandboxClose == nil || fmt.Sprint(*o.SandboxClose) != alpha)) {
			t.Fatalf("the evidence behind a date is its close: %+v", o)
		}
	}
	if len(got.Operations) != 22 {
		t.Fatalf("every write kind and each provider's reads are listed: %d", len(got.Operations))
	}
	defer db.Exec(`DELETE FROM operation_acceptances WHERE organization_id=$1`, org)
	record := func(permissions []string, body map[string]any, want int) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/closes/security/operations", strings.NewReader(string(raw))).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_admin"}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v %v: %d %s", permissions, body, w.Code, w.Body.String())
		}
	}
	admin, accountant := []string{"clients:all", "members:manage"}, []string{"clients:all", "members:manage", "workpapers:review"}
	check := map[string]any{"provider": "QuickBooks", "kind": "accrual", "stage": "sandbox_checked", "accepted": true, "evidence": "Sandbox company 9341: accrual journal JE-77 posted and read back on 2026-10-01"}
	record([]string{"clients:all"}, check, 403)
	record(admin, map[string]any{"provider": "QuickBooks", "kind": "payment_run", "stage": "sandbox_checked", "accepted": true, "evidence": "Not an operation"}, 422)
	record(admin, map[string]any{"provider": "QuickBooks", "kind": "accrual", "stage": "sandbox_checked", "accepted": true, "evidence": strings.Repeat("審", 1500)}, 200)
	record(admin, check, 200)
	accept := map[string]any{"provider": "QuickBooks", "kind": "accrual", "stage": "accountant_accepted", "accepted": true, "evidence": "Reviewed the sandbox journal and the method examples"}
	record(admin, accept, 403)
	record(accountant, accept, 200)
	accept["accepted"], accept["evidence"] = false, "Withdrawn until the reversal entry is checked too"
	record(accountant, accept, 200)
	var after struct {
		Operations []struct {
			Provider, Kind, Status string
			Checked                *struct {
				Accepted bool
				Evidence string
			} `json:"sandbox_checked"`
			Accepted *struct {
				Accepted bool
				By       string
			} `json:"accountant_accepted"`
		}
	}
	must(json.Unmarshal([]byte(call("user_admin", admin, 200)), &after))
	for _, o := range after.Operations {
		if o.Provider == "QuickBooks" && o.Kind == "accrual" && (o.Status != "enabled" || o.Checked == nil || !o.Checked.Accepted || !strings.Contains(o.Checked.Evidence, "JE-77") || o.Accepted == nil || o.Accepted.Accepted || o.Accepted.By != "user_admin") {
			t.Fatalf("the accrual journal shows its outside sandbox check and its withdrawn acceptance, with its status unchanged: %+v", o)
		}
		if o.Provider == "QuickBooks" && o.Kind == "prepaid" && (o.Checked != nil || o.Accepted != nil) {
			t.Fatalf("an operation with no record shows none: %+v", o)
		}
	}
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
	var model struct {
		Provider, Evidence string
		InUse              bool `json:"in_use"`
	}
	for _, p := range got.Providers {
		providers[p.Provider] = p.InUse
		if strings.Contains(p.Evidence, "model call") {
			model = p
		}
	}
	if !strings.HasPrefix(model.Evidence, "3 model calls reserved") || !model.InUse || model.Provider != "Claude (Anthropic) through the private context machine" {
		t.Fatalf("model use is shown as the reserved budget it is, under the configured provider: %+v", model)
	}
	if !providers["Resend"] || !providers["QuickBooks"] || providers["Xero"] || providers["Stripe"] {
		t.Fatalf("which outside services are in use: %+v", got.Providers)
	}
	if got.OrganizationID != org || strings.Join(got.Clients, ",") != "Alpha,Beta" {
		t.Fatalf("the firm and every client, for the controls: %s %v", got.OrganizationID, got.Clients)
	}
	// A grant never enables a kind the server has not approved.
	t.Setenv("PRODUCTION_POSTING_KINDS", "")
	got.Operations = nil
	must(json.Unmarshal([]byte(call("user_admin", []string{"clients:all", "members:manage"}, 200)), &got))
	for _, o := range got.Operations {
		if o.Provider == "QuickBooks" && o.Operation == "Journal: accrual" && (o.Status != "documented" || len(o.Clients) != 0) {
			t.Fatalf("an accrual the server does not allow is not enabled by its grant: %+v", o)
		}
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id=$1`, qbo)
	must(err)
	got.PostingGrants = nil
	must(json.Unmarshal([]byte(call("user_admin", []string{"clients:all", "members:manage"}, 200)), &got))
	if len(got.PostingGrants) != 0 {
		t.Fatalf("a grant on a connection that needs reconnecting is not posting authority in force: %+v", got.PostingGrants)
	}
}
