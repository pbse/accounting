package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// workosPages answers WorkOS list requests by path and cursor.
type workosPages struct {
	next  http.RoundTripper
	pages map[string]string
}

func (w workosPages) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "api.workos.com" {
		return w.next.RoundTrip(r)
	}
	body, ok := w.pages[r.URL.Path+"?after="+r.URL.Query().Get("after")]
	if !ok {
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
}

// asFirm answers WorkOS for the firms as if each member held the role named: a partner reviews every client, a reviewer
// only those granted to them, and a board member's review comes with executive:read, which keeps them to the executive view.
func asFirm(t *testing.T, roles map[string]string, orgs ...string) {
	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	members := []string{}
	for subject, role := range roles {
		members = append(members, `{"user_id":"`+subject+`","status":"active","role":{"slug":"`+role+`"}}`)
	}
	pages := map[string]string{"/user_management/organization_memberships?after=": `{"data":[` + strings.Join(members, ",") + `]}`}
	for _, org := range orgs {
		pages["/authorization/organizations/"+org+"/roles?after="] = `{"data":[{"slug":"partner","permissions":["workpapers:review","clients:all"]},{"slug":"reviewer","permissions":["workpapers:review"]},{"slug":"board","permissions":["workpapers:review","clients:all","executive:read"]}]}`
	}
	original := http.DefaultTransport
	http.DefaultTransport = workosPages{next: original, pages: pages}
	t.Cleanup(func() { http.DefaultTransport = original })
}

func TestHandADocumentToATeammate(t *testing.T) {
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
	ctx := context.Background()
	org := "org_Hand" + rand.Text()
	// Partners review every client; reviewers only those they are assigned to; viewers review nothing. Cy is a viewer
	// who also holds the reviewer role.
	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	original, members := http.DefaultTransport, "/user_management/organization_memberships?after="
	pages := map[string]string{
		"/authorization/organizations/" + org + "/roles?after=": `{"data":[{"slug":"partner","permissions":["workpapers:review","clients:all"]},{"slug":"reviewer","permissions":["workpapers:review"]},{"slug":"viewer","permissions":[]}]}`,
		members: `{"data":[{"user_id":"user_a","status":"active","role":{"slug":"partner"}},{"user_id":"user_b","status":"active","role":{"slug":"partner"},"user":{"first_name":"Bea","last_name":"Brown","email":"bea@firm.test"}},{"user_id":"user_c","status":"active","role":{"slug":"viewer"},"roles":[{"slug":"viewer"},{"slug":"reviewer"}],"user":{"first_name":"","last_name":"","email":"cy@firm.test"}}],"list_metadata":{"after":"om_3"}}`,
		"/user_management/organization_memberships?after=om_3": `{"data":[{"user_id":"user_d","status":"active","role":{"slug":"viewer"}},{"user_id":"user_e","status":"active","role":{"slug":"reviewer"}},{"user_id":"user_f","status":"active","role":{"slug":"partner"}}],"list_metadata":{"after":null}}`,
	}
	http.DefaultTransport = workosPages{next: original, pages: pages}
	defer func() { http.DefaultTransport = original }()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Hand-off fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,s<>'user_f','Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b','user_c','user_d','user_e','user_f']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Alpha',true,'Synthetic','fixture'),($1,'user_d','Alpha',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	var alpha string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_routines WHERE organization_id=$1`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	// Each document has a fact the agent could not read, so it needs a person.
	document := func(name string) string {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		defer tx.Rollback()
		var source string
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, alpha, name, preparation.Digest([]byte(name)), "Invoice "+name).Scan(&source))
		job, err := preparation.StartIntake(ctx, tx, alpha, source, "fixture")
		must(err)
		must(tx.Commit())
		_, err = db.Exec(`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","questions":["Which months does the policy cover?"],"facts":[]}' WHERE job_id::text=$1`, job)
		must(err)
		_, err = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, job)
		must(err)
		return job
	}
	invoice, lease := document("invoice.txt"), document("lease.txt")
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	partner, reviewer := []string{"workpapers:review", "clients:all"}, []string{"workpapers:review"}
	hand := func(job, to string, want int) string {
		t.Helper()
		return string(call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal/handoffs", map[string]string{"job_id": job, "assignee": to, "note": "Check the service dates with the client"}, want))
	}
	type status struct {
		Goal *struct {
			ID     string `json:"id"`
			Events []struct {
				Text string `json:"text"`
			} `json:"events"`
		} `json:"goal"`
		Handoffs []goalHandoff `json:"handoffs"`
	}
	read := func(subject string, permissions []string) status {
		t.Helper()
		var s status
		must(json.Unmarshal(call(subject, permissions, "GET", "/v1/closes/"+alpha+"/goal", nil, 200), &s))
		return s
	}
	last := func() string {
		s := read("user_a", partner)
		return s.Goal.Events[len(s.Goal.Events)-1].Text
	}
	handed := func(subject string, permissions []string) []string {
		t.Helper()
		var inbox struct {
			Groups []struct {
				Events []struct {
					Kind  string `json:"kind"`
					Text  string `json:"text"`
					JobID string `json:"job_id"`
				} `json:"events"`
			} `json:"groups"`
		}
		must(json.Unmarshal(call(subject, permissions, "GET", "/v1/closes/attention", nil, 200), &inbox))
		out := []string{}
		for _, g := range inbox.Groups {
			for _, e := range g.Events {
				if e.Kind == "handoff" {
					out = append(out, e.JobID+" "+e.Text)
				}
			}
		}
		return out
	}

	// Only teammates who can review Alpha's work are offered: not the requester, a viewer even when assigned, a reviewer
	// without the assignment, or a member the firm removed.
	if body := hand(invoice, "user_b", 409); !strings.Contains(body, "Hand the close to the agent first") {
		t.Fatalf("a document is handed as part of a delegated close: %s", body)
	}
	call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true}, 200)
	goal := read("user_a", partner).Goal.ID
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, alpha)
	must(err)
	if body := hand(invoice, "user_b", 409); !strings.Contains(body, "signed off") {
		t.Fatalf("a signed-off close is reopened before work is handed out: %s", body)
	}
	_, err = db.Exec(`DELETE FROM close_signoffs WHERE close_id::text=$1`, alpha)
	must(err)
	var people struct {
		People []teammate `json:"people"`
	}
	must(json.Unmarshal(call("user_a", partner, "GET", "/v1/closes/"+alpha+"/people", nil, 200), &people))
	listed := []string{}
	for _, p := range people.People {
		listed = append(listed, p.Subject+"="+p.Name)
	}
	if strings.Join(listed, ",") != "user_b=Bea Brown,user_c=cy@firm.test" {
		t.Fatalf("teammates: %v", listed)
	}
	hand(invoice, "user_e", 422)

	// A handed document shows who has it and reaches only their attention list, linked to the document.
	hand(invoice, "user_c", 200)
	if body := hand(invoice, "user_b", 409); !strings.Contains(body, "cy@firm.test already has invoice.txt") {
		t.Fatalf("a document is with one teammate at a time: %s", body)
	}
	if s := read("user_c", reviewer); len(s.Handoffs) != 1 || s.Handoffs[0].JobID != invoice || !s.Handoffs[0].Mine || s.Handoffs[0].Name != "cy@firm.test" {
		t.Fatalf("the close shows the hand-off to its teammate: %+v", s.Handoffs)
	}
	if got := handed("user_c", reviewer); len(got) != 1 || got[0] != invoice+" user_a handed you invoice.txt: Check the service dates with the client" {
		t.Fatalf("the teammate is told: %v", got)
	}
	if got := handed("user_b", partner); len(got) != 0 {
		t.Fatalf("nobody else is told: %v", got)
	}

	// Losing the assignment to the client ends the hand-off. While the firm's roles can't be read the agent waits.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Alpha',false,'Moved to another client','fixture')`, org)
	must(err)
	t.Setenv("WORKOS_API_KEY", "")
	sweepCloseGoals(ctx, db)
	if len(read("user_a", partner).Handoffs) != 1 {
		t.Fatal("without the firm's roles the agent changes nothing")
	}
	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	sweepCloseGoals(ctx, db)
	if text := last(); text != "cy@firm.test can no longer act for this client, so I took invoice.txt back." || len(read("user_a", partner).Handoffs) != 0 {
		t.Fatalf("a lost assignment ends the hand-off: %q", text)
	}

	// Once the document is prepared nobody needs to look into it, so the agent closes the hand-off.
	hand(invoice, "user_b", 200)
	var paper string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,'{}') RETURNING id::text`, alpha, "handoff-"+rand.Text()).Scan(&paper))
	_, err = db.Exec(`UPDATE preparation_jobs SET state='completed',workpaper_ids=jsonb_build_array($2::text),finished_at=now() WHERE id::text=$1`, invoice, paper)
	must(err)
	sweepCloseGoals(ctx, db)
	if text := last(); text != "invoice.txt no longer needs anyone, so Bea Brown's hand-off is closed." {
		t.Fatalf("the agent closes a resolved hand-off: %q", text)
	}
	hand(invoice, "user_b", 422)

	// A hand-off that stays open is followed up: with the teammate after two days, then with the goal's owner after four,
	// or by the close's due date when that is sooner. Only the owner sees it in the plan.
	hand(lease, "user_b", 200)
	id := read("user_a", partner).Handoffs[0].ID
	age := func(hours int, due string) {
		t.Helper()
		_, err := db.Exec(`UPDATE goal_handoffs SET created_at=now()-make_interval(hours=>$2) WHERE id::text=$1`, id, hours)
		must(err)
		_, err = db.Exec(`UPDATE close_goals SET created_at=now()-interval '10 days',due_on=NULLIF($2,'')::date WHERE id::text=$1`, goal, due)
		must(err)
	}
	owner := func(subject string) (asked []string, plan []string, history []string) {
		t.Helper()
		var inbox struct {
			Groups []struct {
				Events []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"events"`
			} `json:"groups"`
		}
		must(json.Unmarshal(call(subject, partner, "GET", "/v1/closes/attention", nil, 200), &inbox))
		for _, g := range inbox.Groups {
			for _, e := range g.Events {
				if e.Kind == "goal" && strings.Contains(e.Text, "has been with") {
					asked = append(asked, e.Text)
				}
			}
		}
		var s struct {
			Progress struct {
				Plan []planStep `json:"plan"`
			} `json:"progress"`
			Activity []struct {
				Text string `json:"text"`
			} `json:"activity"`
		}
		must(json.Unmarshal(call(subject, partner, "GET", "/v1/closes/"+alpha+"/goal", nil, 200), &s))
		plan = s.Progress.Plan[1].Lines
		for _, a := range s.Activity {
			if strings.HasPrefix(a.Text, "I followed up") || strings.HasPrefix(a.Text, "I asked") {
				history = append(history, a.Text)
			}
		}
		return
	}
	age(30, "")
	if asked, plan, history := owner("user_a"); len(handed("user_b", partner)) != 1 || len(asked)+len(plan)+len(history) != 0 {
		t.Fatalf("nobody is followed up with within two days: %v %v %v", asked, plan, history)
	}
	age(50, "")
	if got := handed("user_b", partner); len(got) != 2 || got[0] != lease+" lease.txt has been with you for at least 2 days, handed by user_a." {
		t.Fatalf("after two days the teammate is followed up with, linked to the document: %v", got)
	}
	if asked, _, history := owner("user_a"); len(asked) != 0 || len(history) != 1 || history[0] != "I followed up with Bea Brown on lease.txt" {
		t.Fatalf("the follow-up is in the goal's history, and the owner is not asked yet: %v %v", asked, history)
	}
	stall := "lease.txt has been with Bea Brown for at least 4 days. Check with them, or take it back."
	age(100, "")
	if asked, plan, history := owner("user_a"); len(asked) != 1 || asked[0] != stall || !slices.Contains(plan, stall) || len(history) != 2 || history[0] != "I asked user_a to check with Bea Brown on lease.txt" {
		t.Fatalf("after four days the owner is asked, on the card and in the history: %v %v %v", asked, plan, history)
	}
	if asked, plan, _ := owner("user_e"); len(asked) != 0 || slices.Contains(plan, stall) {
		t.Fatalf("only the goal's owner is asked: %v %v", asked, plan)
	}
	// A close due soon brings the owner's follow-up forward to midday UTC the day before it is due, a day after the
	// hand-off at the earliest. The last midday UTC decides which due date has reached that point, at any hour.
	midday := time.Now().UTC().Add(-12 * time.Hour)
	age(50, midday.AddDate(0, 0, 2).Format("2006-01-02"))
	if asked, _, _ := owner("user_a"); len(asked) != 0 {
		t.Fatalf("a close whose day before starts at the next midday is not followed up yet: %v", asked)
	}
	age(50, midday.AddDate(0, 0, 1).Format("2006-01-02"))
	if asked, _, _ := owner("user_a"); len(asked) != 1 || !strings.HasPrefix(asked[0], "lease.txt has been with Bea Brown for at least ") {
		t.Fatalf("the due date brings the follow-up forward: %v", asked)
	}
	age(0, "")
	call("user_b", partner, "POST", "/v1/closes/"+alpha+"/goal/handoffs/"+id+"/take-back", nil, 200)
	call("user_b", partner, "POST", "/v1/closes/"+alpha+"/goal/handoffs/"+id+"/take-back", nil, 409)
	if text := last(); text != "Bea Brown handed lease.txt back." {
		t.Fatalf("handing back is recorded: %q", text)
	}
	hand(lease, "user_b", 200)
	call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal/stop", map[string]string{"reason": "Doing it myself"}, 200)
	var open int
	must(db.QueryRow(`SELECT count(*) FROM goal_handoffs WHERE goal_id::text=$1 AND closed_at IS NULL`, goal).Scan(&open))
	if open != 0 {
		t.Fatal("stopping the agent closes its hand-offs")
	}

	// A teammate whose role no longer reviews loses the hand-off; the author, the goal and the monthly hand-over.
	demote := func(user string) {
		pages[members] = strings.Replace(pages[members], `"user_id":"`+user+`","status":"active","role":{"slug":"partner"}`, `"user_id":"`+user+`","status":"active","role":{"slug":"viewer"}`, 1)
	}
	call("user_a", partner, "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true, "every_month": true}, 200)
	hand(lease, "user_b", 200)
	demote("user_b")
	sweepCloseGoals(ctx, db)
	if text := last(); text != "Bea Brown can no longer act for this client, so I took lease.txt back." {
		t.Fatalf("a teammate who can no longer review loses the hand-off: %q", text)
	}
	demote("user_a")
	sweepCloseGoals(ctx, db)
	sweepCloseRoutines(ctx, db, time.Now().AddDate(0, 2, 0))
	var reason string
	must(db.QueryRow(`SELECT COALESCE(stop_reason,'') FROM close_routines WHERE organization_id=$1`, org).Scan(&reason))
	if text := last(); text != "I stopped because user_a can no longer review this client's work." || reason != "Stopped because user_a can no longer act for Alpha." {
		t.Fatalf("an author who can no longer review stops the agent: %q %q", text, reason)
	}

	// Without WorkOS a hosted firm offers nobody rather than an unchecked list.
	t.Setenv("WORKOS_API_KEY", "")
	call("user_a", partner, "GET", "/v1/closes/"+alpha+"/people", nil, 502)
}

func TestHandAnAccountToATeammate(t *testing.T) {
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
	ctx := context.Background()
	org := "org_Acct" + rand.Text()
	t.Setenv("WORKOS_API_KEY", "sk_test_synthetic")
	original := http.DefaultTransport
	http.DefaultTransport = workosPages{next: original, pages: map[string]string{
		"/authorization/organizations/" + org + "/roles?after=": `{"data":[{"slug":"partner","permissions":["workpapers:review","clients:all"]}]}`,
		"/user_management/organization_memberships?after=":      `{"data":[{"user_id":"user_a","status":"active","role":{"slug":"partner"}},{"user_id":"user_b","status":"active","role":{"slug":"partner"},"user":{"first_name":"Bea","last_name":"Brown"}}]}`,
	}}
	defer func() { http.DefaultTransport = original }()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Account hand-off fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM quickbooks_connections WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	// A capture of three accounts; office supplies is excluded with a reason.
	var alpha, ledger, conn string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger.txt','account-ledger','Synthetic ledger','fixture') RETURNING id::text`, alpha).Scan(&ledger))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, alpha, rand.Text()).Scan(&conn))
	capture := func() string {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": ledger, "coverage": []map[string]string{
			{"account_id": "a", "name": "Prepaid insurance", "kind": "prepaid", "question": "Provide the policy invoice."},
			{"account_id": "b", "name": "Operating bank", "kind": "reconciliation", "question": "Provide the statement."},
			{"account_id": "c", "name": "Office supplies", "kind": "accountant_review", "question": "Review activity."}}})
		var snapshot string
		must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture') RETURNING id::text`, alpha, conn, rand.Text(), string(payload)).Scan(&snapshot))
		_, err := db.Exec(`INSERT INTO coverage_bindings(close_id,snapshot_id,account_id,job_ids,excluded,reason,actor) VALUES($1,$2,'c','[]',true,'Immaterial office supplies','user_a')`, alpha, snapshot)
		must(err)
		return snapshot
	}
	first := capture()
	store := savedCloses(db, strings.Repeat("a", 32))
	partner := []string{"workpapers:review", "clients:all"}
	call := func(subject, method, path string, body any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: partner}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	hand := func(account string, want int) string {
		t.Helper()
		return call("user_a", "POST", "/v1/closes/"+alpha+"/goal/handoffs", map[string]string{"account_id": account, "assignee": "user_b", "note": "Get the policy invoice"}, want)
	}
	var goal string
	last := func() string {
		t.Helper()
		var text string
		must(db.QueryRow(`SELECT events->-1->>'text' FROM close_goals WHERE id::text=$1`, goal).Scan(&text))
		return text
	}
	open := func() int {
		t.Helper()
		var n int
		must(db.QueryRow(`SELECT count(*) FROM goal_handoffs WHERE goal_id::text=$1 AND closed_at IS NULL`, goal).Scan(&n))
		return n
	}
	call("user_a", "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true}, 200)
	must(db.QueryRow(`SELECT id::text FROM close_goals WHERE close_id::text=$1`, alpha).Scan(&goal))

	// An account whose coverage still needs a decision goes to one teammate at a time; an excluded one, or a request
	// naming both a document and an account, is refused.
	hand("a", 200)
	if body := hand("a", 409); !strings.Contains(body, "Bea Brown already has coverage for Prepaid insurance") {
		t.Fatalf("an account is with one teammate at a time: %s", body)
	}
	hand("c", 422)
	if body := call("user_a", "POST", "/v1/closes/"+alpha+"/goal/handoffs", map[string]string{"job_id": "1", "account_id": "a", "assignee": "user_b"}, 422); !strings.Contains(body, "one document or one account") {
		t.Fatalf("a hand-off is one document or one account: %s", body)
	}
	var s struct {
		Handoffs []goalHandoff `json:"handoffs"`
	}
	must(json.Unmarshal([]byte(call("user_b", "GET", "/v1/closes/"+alpha+"/goal", nil, 200)), &s))
	if len(s.Handoffs) != 1 || s.Handoffs[0].AccountID != "a" || s.Handoffs[0].JobID != "" || !s.Handoffs[0].Mine {
		t.Fatalf("the account's hand-off shows as the teammate's: %+v", s.Handoffs)
	}
	var inbox struct {
		Groups []struct {
			Events []struct {
				Kind      string `json:"kind"`
				Text      string `json:"text"`
				AccountID string `json:"account_id"`
			} `json:"events"`
		} `json:"groups"`
	}
	must(json.Unmarshal([]byte(call("user_b", "GET", "/v1/closes/attention", nil, 200)), &inbox))
	told := false
	for _, g := range inbox.Groups {
		for _, e := range g.Events {
			told = told || e.Kind == "handoff" && e.AccountID == "a" && e.Text == "user_a handed you coverage for Prepaid insurance: Get the policy invoice"
		}
	}
	if !told {
		t.Fatalf("the teammate is told, with the account: %+v", inbox.Groups)
	}

	// Excluding the account ends the hand-off.
	_, err = db.Exec(`INSERT INTO coverage_bindings(close_id,snapshot_id,account_id,job_ids,excluded,reason,actor) VALUES($1,$2,'a','[]',true,'Policy expensed','user_b')`, alpha, first)
	must(err)
	must(stepGoal(ctx, db, goal, nil))
	if text := last(); text != "coverage for Prepaid insurance no longer needs anyone, so Bea Brown's hand-off is closed." || open() != 0 {
		t.Fatalf("a covered account ends its hand-off: %q", text)
	}

	// A new capture ends a hand-off made on the old one, even for the same account.
	hand("b", 200)
	capture()
	must(stepGoal(ctx, db, goal, nil))
	if text := last(); text != "The ledger capture changed, so Bea Brown's hand-off of coverage for Operating bank is closed." || open() != 0 {
		t.Fatalf("a hand-off belongs to its capture: %q", text)
	}

	// A stale capture is recaptured before its accounts are handed; unreadable coverage leaves a hand-off open.
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='disconnected' WHERE id::text=$1`, conn)
	must(err)
	hand("b", 422)
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='active' WHERE id::text=$1`, conn)
	must(err)
	hand("b", 200)
	_, err = db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id::text=$1`, alpha)
	must(err)
	must(stepGoal(ctx, db, goal, nil))
	if open() != 1 {
		t.Fatal("a hand-off stays open while its coverage cannot be read")
	}
}
