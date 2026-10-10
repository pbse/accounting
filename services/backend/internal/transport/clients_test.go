package transport

import (
	"bytes"
	"fmt"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// A client's page reads the client apart from any month: its roster entry and the template it follows, every month
// opened under any of its names (with where each month's books come from), the months it owes, its connections, its
// currency and basis, and, for an administrator, who can do its work. A member reads only a client assigned to them,
// and only the months they could open.
func TestAClientPageReadsTheClient(t *testing.T) {
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
	org := "org_ClientPage" + rand.Text()
	// Ann reviews every client; Cy reviews only Acme, under the name its September close was opened with.
	asFirm(t, map[string]string{"user_a": "partner", "user_c": "reviewer", "user_d": "reviewer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Client page fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_c','user_d']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Acme Services LLC',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM request_submissions WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_export_labels WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`,
			`DELETE FROM procedure_versions WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`,
			`DELETE FROM client_roster WHERE organization_id=$1`, `DELETE FROM close_templates WHERE organization_id=$1`,
			`DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	now := time.Now().UTC().Format("2006-01")
	last, before := shiftMonth(now, -1), shiftMonth(now, -2)
	// Acme is on the roster from two months ago, due ten days after each month ends by its own entry; its August close
	// was opened under its own name and September's under a name later confirmed as its alias.
	_, err = db.Exec(`INSERT INTO client_roster(organization_id,client_entity,active,since,due_days,reason,actor) VALUES($1,'Acme Co',true,$2,10,'Synthetic','fixture')`, org, before)
	must(err)
	_, err = db.Exec(`INSERT INTO close_templates(organization_id,due_days,owner,reason,actor) VALUES($1,5,'','Synthetic','fixture')`, org)
	must(err)
	var august, september string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,created_at) VALUES($1,'Acme Co',$2,now()-interval '2 days') RETURNING id::text`, org, before).Scan(&august))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,created_at) VALUES($1,'Acme Services LLC',$2,now()-interval '2 days') RETURNING id::text`, org, last).Scan(&september))
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Acme Co','acme services llc','fixture',now()-interval '3 days')`, org)
	must(err)
	// August's books come from a QuickBooks period capture of the production connection; September's exports are labeled.
	var connection, source string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment,home_currency,fiscal_year_start)
 VALUES($1,$2,'Acme Co','fixture','active',$3,now(),'9130','Acme Co','production','USD',4) RETURNING id::text`, august, org, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'QuickBooks period evidence.txt','d','x','fixture') RETURNING id::text`, august).Scan(&source))
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'9130','d',jsonb_build_object('period',$3::text,'source_id',$4::text,'accounts','[]'::jsonb),now(),'fixture')`, august, connection, before, source)
	must(err)
	var export string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'Trial balance.csv','e','x','fixture') RETURNING id::text`, september).Scan(&export))
	_, err = db.Exec(`INSERT INTO close_export_labels(close_id,evidence_ids,exported_on,complete,final_adjustments,note,actor) VALUES($1,ARRAY[$2::bigint],current_date,true,false,'Exported by the client','fixture')`, september, export)
	must(err)
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,enabled,reason,actor) VALUES($1,'Acme Co','basis','cash','Cash basis',true,'Synthetic','fixture')`, org)
	must(err)
	// September's lease waits on a request sent to the client.
	var lease, request string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'Lease, unsigned.txt','f','x','fixture') RETURNING id::text`, september).Scan(&lease))
	tx, err := db.Begin()
	must(err)
	job, err := preparation.StartIntake(t.Context(), tx, september, lease, "fixture")
	must(err)
	must(tx.Commit())
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, job)
	must(err)
	must(db.QueryRow(`WITH d AS (INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'1. Send the signed lease.','[]','y','fixture') RETURNING id)
 INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) SELECT $2,id,0,'dana@acme.test','fixture' FROM d RETURNING id::text`, september, job).Scan(&request))

	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, path string, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", path, bytes.NewReader(nil)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", subject, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	page := func(name string) string { return "/v1/closes/client?name=" + url.QueryEscape(name) }
	admin := []string{"workpapers:review", "clients:all", "members:manage", "integrations:manage"}

	// The alias names the same client, under its own name, with both months, newest first.
	read := call("user_a", admin, page("ACME SERVICES LLC"), 200)
	if read["entity"] != "Acme Co" || read["can_manage"] != true || read["can_connect"] != true {
		t.Fatalf("client: %v", read)
	}
	months := read["months"].([]any)
	if len(months) != 2 || months[0].(map[string]any)["id"] != september || months[1].(map[string]any)["id"] != august {
		t.Fatalf("months: %v", months)
	}
	if books := months[1].(map[string]any)["books"].(map[string]any); books["source"] != "quickbooks" || books["stale"] == true {
		t.Fatalf("August books: %v", books)
	}
	if books := months[0].(map[string]any)["books"].(map[string]any); books["source"] != "exports" {
		t.Fatalf("September books: %v", books)
	}
	// It owes this month, which nobody opened, due ten days after it ends by its own entry.
	owed := read["owed"].([]any)
	if len(owed) != 1 || owed[0].(map[string]any)["period"] != now || owed[0].(map[string]any)["due_on"] != monthEnd(now).AddDate(0, 0, 10).Format("2006-01-02") {
		t.Fatalf("owed: %v", owed)
	}
	if roster := read["roster"].(map[string]any); roster["since"] != before || roster["due_days"] != float64(10) || roster["owner"] != nil {
		t.Fatalf("roster: %v", roster)
	}
	if template := read["template"].(map[string]any); template["due_days"] != float64(5) {
		t.Fatalf("template: %v", template)
	}
	qbo := read["quickbooks"].([]any)
	if len(qbo) != 1 || qbo[0].(map[string]any)["environment"] != "production" || qbo[0].(map[string]any)["state"] != "active" || qbo[0].(map[string]any)["client_entity"] != "Acme Co" || len(read["xero"].([]any)) != 0 {
		t.Fatalf("connections: %v %v", qbo, read["xero"])
	}
	if profile := read["profile"].(map[string]any); profile["currency"] != "USD" || profile["fiscal_year_start"] != float64(4) || profile["state"] != "detected" {
		t.Fatalf("profile: %v", profile)
	}
	if basis := read["basis"].(map[string]any); basis["method"] != "cash" || basis["scope"] != "client" || len(read["alias_bases"].([]any)) != 0 {
		t.Fatalf("basis: %v %v", basis, read["alias_bases"])
	}
	// A basis approved under the alias stays that name's months' basis, and is listed with it.
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,enabled,reason,actor) VALUES($1,'Acme Services LLC','basis','accrual','Accrual basis',true,'Synthetic','fixture')`, org)
	must(err)
	if held := call("user_a", admin, page("Acme Co"), 200)["alias_bases"].([]any); len(held) != 1 || held[0].(map[string]any)["held_under"] != "Acme Services LLC" || held[0].(map[string]any)["method"] != "accrual" {
		t.Fatalf("a basis approved under an alias: %v", held)
	}
	// The request waiting on the client, from the month opened under its alias: approved, not sent yet; then sent,
	// once its delivery is recorded.
	if requests := read["requests"].([]any); len(requests) != 1 || requests[0].(map[string]any)["id"] != request || requests[0].(map[string]any)["close_id"] != september ||
		requests[0].(map[string]any)["items"] != float64(1) || requests[0].(map[string]any)["state"] != "not_sent" || requests[0].(map[string]any)["sent_at"] != nil || requests[0].(map[string]any)["job"] != fmt.Sprint(job) {
		t.Fatalf("requests: %v", requests)
	}
	_, err = db.Exec(`UPDATE work_requests SET delivered_by='fixture',delivered_at=now(),delivery_reference='Handed over' WHERE id=$1`, request)
	must(err)
	if requests := call("user_a", admin, page("Acme Co"), 200)["requests"].([]any); len(requests) != 1 || requests[0].(map[string]any)["state"] != "sent" || requests[0].(map[string]any)["sent_at"] == nil || requests[0].(map[string]any)["answered_in_part"] != false {
		t.Fatalf("a recorded delivery sends the request: %v", requests)
	}
	// The client answers part of it: the request stays, answered in part, for the firm to decide on.
	_, err = db.Exec(`INSERT INTO request_submissions(request_id,receipt,complete) VALUES($1,'r1',false)`, request)
	must(err)
	if requests := call("user_a", admin, page("Acme Co"), 200)["requests"].([]any); len(requests) != 1 || requests[0].(map[string]any)["answered_in_part"] != true {
		t.Fatalf("a partial reply is shown: %v", requests)
	}
	// A Xero connection made from the September month, under the name later confirmed as Acme's alias, is Acme's too,
	// and says which name it is held under.
	_, err = db.Exec(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_name) VALUES($1,$2,'Acme Services LLC','fixture','active',$3,now(),'Acme Services')`, september, org, rand.Text())
	must(err)
	if held := call("user_a", admin, page("Acme Co"), 200)["xero"].([]any); len(held) != 1 || held[0].(map[string]any)["client_entity"] != "Acme Services LLC" {
		t.Fatalf("a connection held under an alias: %v", held)
	}
	// Who can work on Acme: Ann, who reviews every client, and Cy, assigned under the alias' name, which gives him its
	// September close (the schedule's owner still needs an assignment under its own name).
	people := read["people"].([]any)
	if len(people) != 2 || people[0].(map[string]any)["subject"] != "user_a" || people[1].(map[string]any)["subject"] != "user_c" {
		t.Fatalf("people: %v", people)
	}

	// Cy is assigned to the name September was opened with: he reads the client with that month alone, and nothing of
	// the client as a whole (its roster entry, the months it owes, its connections, currency and basis).
	cy := call("user_c", []string{"workpapers:review"}, page("Acme Co"), 200)
	if months := cy["months"].([]any); len(months) != 1 || months[0].(map[string]any)["id"] != september || len(cy["requests"].([]any)) != 1 {
		t.Fatalf("Cy's months and requests: %v %v", months, cy["requests"])
	}
	if cy["whole"] != false || cy["roster"] != nil || len(cy["owed"].([]any)) != 0 || len(cy["quickbooks"].([]any)) != 0 || len(cy["xero"].([]any)) != 0 || cy["profile"] != nil || cy["basis"] != nil ||
		cy["template"] != nil || cy["template_hidden"] != true || cy["people"] != nil || cy["can_manage"] != false || cy["can_connect"] != false {
		t.Fatalf("Cy's read: %v", cy)
	}
	// Dee is assigned to Acme under its own name: she reads all of it, its August month among them, with no template.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_d','Acme Co',true,'Synthetic','fixture')`, org)
	must(err)
	dee := call("user_d", []string{"workpapers:review"}, page("acme co"), 200)
	if dee["whole"] != true || dee["roster"] == nil || len(dee["owed"].([]any)) != 1 || len(dee["quickbooks"].([]any)) != 1 || dee["template"] != nil || dee["template_hidden"] != true {
		t.Fatalf("Dee's read: %v", dee)
	}
	if months := dee["months"].([]any); len(months) != 1 || months[0].(map[string]any)["id"] != august || len(dee["requests"].([]any)) != 0 {
		t.Fatalf("Dee's months and requests: %v %v", months, dee["requests"])
	}
	// A client on the roster with no month yet can be assigned to a member.
	_, err = db.Exec(`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Corvid Labs',true,$2,'Synthetic','fixture')`, org, now)
	must(err)
	assign := httptest.NewRequest("POST", "/v1/closes/access", strings.NewReader(`{"subject":"user_c","entity":"Corvid Labs","granted":true,"reason":"Takes Corvid on","previous_id":"","confirmed":true}`)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: admin}))
	assign.Header.Set("Content-Type", "application/json")
	assigned := httptest.NewRecorder()
	store.ServeHTTP(assigned, assign)
	if assigned.Code != 200 {
		t.Fatalf("a roster client can be assigned before its first month: %d %s", assigned.Code, assigned.Body.String())
	}
	// A client nobody has heard of, or one the reader is not assigned to, is not found; so is one whose name is too long.
	call("user_a", admin, page("Birch Dental"), 404)
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Birch Dental',$2)`, org, last)
	must(err)
	call("user_a", admin, page("Birch Dental"), 200)
	call("user_c", []string{"workpapers:review"}, page("Birch Dental"), 404)
	call("user_a", admin, page(strings.Repeat("x", 201)), 404)
	// A name is up to 200 characters, however many bytes they take.
	call("user_a", admin, page(strings.Repeat("é", 200)), 404)
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3)`, org, strings.Repeat("é", 200), last)
	must(err)
	call("user_a", admin, page(strings.Repeat("é", 200)), 200)
	// An executive reads no client.
	call("user_a", []string{"executive:read", "clients:all"}, page("Acme Co"), 403)
}
