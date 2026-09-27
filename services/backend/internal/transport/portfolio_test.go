package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
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

func TestPortfolioGoalsReadDaysAndModifiers(t *testing.T) {
	monday := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	for text, want := range map[string]portfolioGoal{
		"Prepare my clients due Friday":                            {action: "prepare", mine: true, due: "2026-10-02", compare: "by"},
		"prepare closes due on Monday":                             {action: "prepare", due: "2026-09-28", compare: "on"},
		"stop work due before 2026-10-15":                          {action: "stop", due: "2026-10-15", compare: "before"},
		"prepare everything due this week":                         {action: "prepare", due: "2026-10-04", compare: "by"},
		"prepare all due tomorrow":                                 {action: "prepare", due: "2026-09-29", compare: "by"},
		"recheck failed work":                                      {action: "recheck", state: "failed"},
		"stop work waiting on the client":                          {action: "stop", state: "waiting_client"},
		"prepare all visible":                                      {action: "prepare", visible: true},
		"prepare my clients due sunday":                            {action: "prepare", mine: true, due: "2026-10-04", compare: "by"},
		"Hand over my clients due Friday":                          {action: "delegate", finish: "review", mine: true, due: "2026-10-02", compare: "by"},
		"delegate failed work for sign-off":                        {action: "delegate", finish: "signoff", state: "failed"},
		"hand my clients due friday to the agent ready for review": {action: "delegate", finish: "review", mine: true, due: "2026-10-02", compare: "by"},
		"hand over closes due on monday for sign off":              {action: "delegate", finish: "signoff", due: "2026-09-28", compare: "on"},
		"hand over work waiting on the client signed off":          {action: "delegate", finish: "signoff", state: "waiting_client"},
		"Show my clients due Friday":                               {action: "show", mine: true, due: "2026-10-02", compare: "by"},
		"show me failed work":                                      {action: "show", state: "failed"},
		"list closes waiting on the client":                        {action: "show", state: "waiting_client"},
		"show closes waiting on statements":                        {action: "show", state: "waiting_statements"},
		"list my clients waiting for bank statements":              {action: "show", mine: true, state: "waiting_statements"},
	} {
		if got, err := parsePortfolioGoal(text, monday); err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", text, got, err, want)
		}
	}
	// A hand-over's instructions are whole phrases; anything else, or two finish lines, is refused rather than dropped.
	for _, text := range []string{"show Acme", "prepare Acme", "prepare due someday", "stop waiting on statements", "prepare closes waiting on statements", "show closes waiting on bank", "stop work waiting on statements and waiting on the client",
		"hand over my clients and sign", "hand over my clients for sign", "hand over my clients for review for sign-off", "hand over my clients to the client",
		"hand over my clients and email them", "delegate over my clients", "hand over my clients ready", "hand over my clients signed review", "prepare my clients for review"} {
		if g, err := parsePortfolioGoal(text, monday); err == nil {
			t.Errorf("%q was read as %+v", text, g)
		}
	}
}

func TestPortfolioGoalsRunOnlyTheServerSelectedCloses(t *testing.T) {
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
	org := "org_Goal" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Goal fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM portfolio_operations WHERE organization_id=$1`, `DELETE FROM recovery_actions WHERE organization_id=$1`, `DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	close := func(entity, period, due, owner string) string {
		t.Helper()
		var id, source string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,due_on,owner) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, org, entity, period, due, owner).Scan(&id))
		tx, e := db.BeginTx(context.Background(), nil)
		must(e)
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Invoice amount: 1200.00','fixture') RETURNING id::text`, id, "goal-"+id).Scan(&source))
		_, e = preparation.StartIntake(context.Background(), tx, id, source, "fixture")
		must(e)
		must(tx.Commit())
		return id
	}
	// Dates follow the clock: the server reads a browser's date only within a day of its own.
	today := browserToday(time.Now().UTC().Format("2006-01-02"))
	friday, _, _ := parseDay([]string{"friday"}, 0, today)
	day := func(n int) string { return friday.AddDate(0, 0, n).Format("2006-01-02") }
	due := close("Alpha", "2026-09", day(0), "user_a")
	close("Alpha", "2026-08", day(7), "user_a")
	close("Alpha", "2026-07", day(-1), "user_b")
	close("Beta", "2026-09", day(0), "user_a")
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) portfolioOperation {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		var op portfolioOperation
		json.Unmarshal(w.Body.Bytes(), &op)
		return op
	}
	reviewer := []string{"workpapers:review"}
	goal := map[string]string{"text": "Prepare my clients due Friday", "today": today.Format("2006-01-02")}
	op := call("user_a", nil, "POST", "/v1/closes/portfolio", goal, 200)
	if len(op.Items) != 1 || op.Items[0].CloseID != due || op.Items[0].Count != 1 || op.Items[0].State != "pending" || !strings.Contains(op.Scope, "that you own, due on or before "+friday.Format("Mon 2 Jan 2006")) || op.HasMore {
		t.Fatalf("the preview selects only the member's accessible close due by Friday: %+v", op)
	}
	if visible := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "prepare all visible", "view": "running", "client": "2026-08"}, 200); len(visible.Items) != 1 || visible.Items[0].Label != "Alpha · 2026-08" || !strings.Contains(visible.Scope, "running view matching “2026-08”") {
		t.Fatalf("all visible uses the inbox's view and client search, not the visible list: %+v", visible)
	}
	call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "prepare all visible", "view": "everything"}, 422)
	// A listing selects the same way, saves nothing, and has nothing to run.
	listed := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "Show my clients due Friday", "today": today.Format("2006-01-02")}, 200)
	if listed.ID != "" || listed.Action != "show" || len(listed.Items) != 1 || listed.Items[0].CloseID != due || listed.Items[0].State != "listed" || !strings.HasPrefix(listed.Items[0].Result, "due ") || listed.Scope != op.Scope {
		t.Fatalf("a listing shows the goal's closes without saving an operation: %+v", listed)
	}
	if none := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "show my clients due before 2000-01-01"}, 200); none.Items == nil || len(none.Items) != 0 {
		t.Fatalf("a listing that matches nothing returns an empty list: %+v", none)
	}
	path := "/v1/closes/portfolio/run"
	call("user_b", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 404)
	call("user_a", nil, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 403)
	call("user_a", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": false}, 422)
	pending := func() int {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/portfolio", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		var out struct{ Operations []portfolioOperation }
		must(json.Unmarshal(w.Body.Bytes(), &out))
		return len(out.Operations)
	}
	if pending() != 2 {
		t.Fatal("each unfinished goal is listed to resume")
	}
	ran := call("user_a", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 200)
	var run, actor string
	must(db.QueryRow(`SELECT id::text,actor FROM close_preparations WHERE close_id::text=$1`, due).Scan(&run, &actor))
	if ran.Items[0].State != "done" || ran.Items[0].Result != "Coordinated 1 source document as run #"+run+"." || actor != "user_a" {
		t.Fatalf("the run prepares the close through its own action, as the member: %+v %s", ran.Items, actor)
	}
	if again := call("user_a", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 200); again.Items[0].Result != ran.Items[0].Result || pending() != 1 {
		t.Fatal("a finished goal runs nothing again and is no longer listed")
	}
	repeat := call("user_a", nil, "POST", "/v1/closes/portfolio", goal, 200)
	if repeat = call("user_a", reviewer, "POST", path, map[string]any{"id": repeat.ID, "confirmed": true}, 200); repeat.Items[0].Result != "Already being prepared as run #"+run+"." {
		t.Fatalf("a goal repeated over the same scope claims no new run: %+v", repeat.Items)
	}
	stop := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "stop my clients due Friday", "today": today.Format("2006-01-02")}, 200)
	if len(stop.Items) != 1 || stop.Items[0].Count != 1 {
		t.Fatalf("the stop preview counts the unresolved job: %+v", stop)
	}
	_, err = db.Exec(`UPDATE preparation_jobs SET state='failed' WHERE close_id::text=$1`, due)
	must(err)
	call("user_a", reviewer, "POST", path, map[string]any{"id": stop.ID, "confirmed": true}, 422)
	stale := call("user_a", reviewer, "POST", path, map[string]any{"id": stop.ID, "confirmed": true, "reason": "Client paused the engagement"}, 200)
	var state string
	must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE close_id::text=$1`, due).Scan(&state))
	if stale.Items[0].State != "failed" || !strings.Contains(stale.Items[0].Result, "Work changed") || state != "failed" {
		t.Fatalf("work that changed since the preview is not stopped: %+v %s", stale.Items, state)
	}
	recheck := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "recheck failed work", "today": today.Format("2006-01-02")}, 200)
	if len(recheck.Items) != 1 || recheck.Items[0].CloseID != due {
		t.Fatalf("failed work selects the close with a failed job: %+v", recheck)
	}
	// A refusal after the action has written leaves none of its writes behind.
	var revision, actions int
	must(db.QueryRow(`SELECT i.revision FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id::text=$1`, due).Scan(&revision))
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=1 WHERE id=$1`, org)
	must(err)
	limited := call("user_a", reviewer, "POST", path, map[string]any{"id": recheck.ID, "confirmed": true}, 200)
	var after int
	must(db.QueryRow(`SELECT i.revision,(SELECT count(*) FROM recovery_actions WHERE close_id=j.close_id) FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id::text=$1`, due).Scan(&after, &actions))
	if limited.Items[0].State != "failed" || !strings.Contains(limited.Items[0].Result, "active preparation limit") || after != revision || actions != 0 {
		t.Fatalf("a recheck refused mid-way records the refusal and none of its writes: %+v revision %d→%d actions %d", limited.Items, revision, after, actions)
	}
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=DEFAULT WHERE id=$1`, org)
	must(err)
	recheck = call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "recheck failed work", "today": today.Format("2006-01-02")}, 200)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',false,'Synthetic revocation','fixture')`, org)
	must(err)
	revoked := call("user_a", reviewer, "POST", path, map[string]any{"id": recheck.ID, "confirmed": true}, 200)
	if revoked.Items[0].State != "failed" || revoked.Items[0].Result != "" || revoked.Items[0].CloseID != "" || revoked.Items[0].Label != "A close you can no longer access" {
		t.Fatalf("a close the member lost access to is not rechecked, and its name is hidden: %+v", revoked.Items)
	}
	var result string
	must(db.QueryRow(`SELECT result FROM portfolio_items WHERE operation_id::text=$1`, recheck.ID).Scan(&result))
	if result != "You can no longer access this close." {
		t.Fatalf("the refusal is recorded: %q", result)
	}
}

func TestPortfolioHandsClosesToTheAgent(t *testing.T) {
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
	org := "org_Over" + rand.Text()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Hand-over fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM portfolio_operations WHERE organization_id=$1`, `DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	today := browserToday(time.Now().UTC().Format("2006-01-02"))
	day := func(n int) string { return today.AddDate(0, 0, n).Format("2006-01-02") }
	open := func(entity, due string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,due_on) VALUES($1,$2,'2026-09',$3) RETURNING id::text`, org, entity, due).Scan(&id))
		return id
	}
	soon, overdue, taken := open("Alpha", day(2)), open("Beta", day(-3)), open("Gamma", day(1))
	open("Delta", day(30))
	signed := open("Epsilon", day(1))
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, signed)
	must(err)
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(method, path string, body any) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review", "clients:all"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	call("POST", "/v1/closes/"+taken+"/goal", map[string]any{"confirmed": true})

	// One request hands over every selected close not already with the agent, each keeping its due date as the
	// agent's deadline when the close page would accept it; an overdue one has none.
	text := "hand over closes due by " + day(5) + " for sign-off"
	var op portfolioOperation
	must(json.Unmarshal(call("POST", "/v1/closes/portfolio", map[string]string{"text": text, "today": day(0)}), &op))
	preview := []string{}
	for _, i := range op.Items {
		preview = append(preview, i.CloseID+" "+i.State+" "+i.DueOn+" "+i.Result)
	}
	want := []string{overdue + " pending  ", taken + " skipped  Already handed to the agent by user_a on " + time.Now().UTC().Format("2 Jan 2006") + ".", soon + " pending " + day(2) + " "}
	if op.Action != "delegate" || op.Finish != "signoff" || strings.Join(preview, "|") != strings.Join(want, "|") {
		t.Fatalf("the preview lists each close with its deadline: %s %s %q", op.Action, op.Finish, preview)
	}
	must(json.Unmarshal(call("POST", "/v1/closes/portfolio/run", map[string]any{"id": op.ID, "confirmed": true}), &op))
	for _, c := range []struct{ id, due, by string }{{soon, day(2), " by " + today.AddDate(0, 0, 2).Format("Mon 2 Jan 2006")}, {overdue, "", ""}} {
		var goal, author, finish, due, first string
		must(db.QueryRow(`SELECT id::text,author,finish,COALESCE(due_on::text,''),events->0->>'text' FROM close_goals WHERE close_id::text=$1`, c.id).Scan(&goal, &author, &finish, &due, &first))
		if author != "user_a" || finish != "signoff" || due != c.due || first != "Handed over by user_a with the goal “"+text+"”, to get it signed off"+c.by {
			t.Fatalf("the close is handed over as the member asked: %s %s %s %q", author, finish, due, first)
		}
		i := slices.IndexFunc(op.Items, func(i portfolioItem) bool { return i.CloseID == c.id })
		if i < 0 || op.Items[i].State != "done" || op.Items[i].Result != "Handed to the agent as goal #"+goal+"." {
			t.Fatalf("each close records its result: %+v", op.Items)
		}
	}
	var goals int
	must(db.QueryRow(`SELECT count(*) FROM close_goals WHERE close_id::text=$1`, taken).Scan(&goals))
	if goals != 1 {
		t.Fatal("a close already with the agent is left as it is")
	}

	// Read back without access to a close, a pending hand-over hides its deadline with its name.
	open("Zeta", day(2))
	call("POST", "/v1/closes/portfolio", map[string]string{"text": text, "today": day(0)})
	r := httptest.NewRequest("GET", "/v1/closes/portfolio", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review"}}))
	w := httptest.NewRecorder()
	store.ServeHTTP(w, r)
	var unseen struct{ Operations []portfolioOperation }
	must(json.Unmarshal(w.Body.Bytes(), &unseen))
	if len(unseen.Operations) != 1 || !slices.ContainsFunc(unseen.Operations[0].Items, func(i portfolioItem) bool { return i.State == "pending" }) || slices.ContainsFunc(unseen.Operations[0].Items, func(i portfolioItem) bool { return i.DueOn != "" || i.CloseID != "" }) {
		t.Fatalf("a close the member cannot see shows neither its name nor its deadline: %+v", unseen.Operations)
	}
}

func TestPortfolioListsClosesWaitingOnStatements(t *testing.T) {
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
	org := "org_Statements" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Statement wait fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`, `DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`, `DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM coverage_bindings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	closeFor := func(entity string) (id string) {
		t.Helper()
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&id))
		return
	}
	waitingJob := func(close, kind string) {
		t.Helper()
		var source, job string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'doc.txt',$2,'x','fixture') RETURNING id::text`, close, rand.Text()).Scan(&source))
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','waiting_client','fixture') RETURNING id::text`, close, rand.Text()).Scan(&job))
		_, err := db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,jsonb_build_object('kind',$3::text))`, job, source, kind)
		must(err)
	}
	// request binds the close's bank account in a capture; latest adds a newer capture afterwards when false.
	request := func(close string, used, current bool) {
		t.Helper()
		var connection, snapshot, draft, requestID string
		must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'1' FROM client_closes WHERE id=$1 RETURNING id::text`, close, rand.Text()).Scan(&connection))
		var ledger, binding string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger.json',$2,'{}','fixture') RETURNING id::text`, close, rand.Text()).Scan(&ledger))
		coverage := `{"period":"2026-09","source_id":"` + ledger + `","coverage":[{"account_id":"35","name":"Checking","kind":"reconciliation"},{"account_id":"41","name":"Prepaid insurance","kind":"prepaid"}]}`
		must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'1',$3,$4,now(),'fixture') RETURNING id::text`, close, connection, rand.Text(), coverage).Scan(&snapshot))
		must(db.QueryRow(`INSERT INTO coverage_bindings(close_id,snapshot_id,account_id,job_ids,reason,actor) VALUES($1,$2,'35','[]','Synthetic','fixture') RETURNING id::text`, close, snapshot).Scan(&binding))
		must(db.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send the September bank statement.','[]','d','fixture') RETURNING id::text`, close).Scan(&draft))
		must(db.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by,coverage_scope) VALUES($1,0,'client@example.test','fixture',jsonb_build_object('snapshot_id',$2::text,'bindings',jsonb_build_array(jsonb_build_object('account_id','35','id',$3::text)))) RETURNING id::text`, draft, snapshot, binding).Scan(&requestID))
		_, err := db.Exec(`INSERT INTO client_request_links(digest,request_id,issued_by,expires_at,used_at) VALUES($1,$2,'fixture',now()+interval '7 days',CASE WHEN $3 THEN now() END)`, rand.Text(), requestID, used)
		must(err)
		if !current {
			_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'1',$3,$4,now(),'fixture')`, close, connection, rand.Text(), coverage)
			must(err)
		}
	}
	statementJob, prepaidJob, openRequest, staleRequest, answered := closeFor("Statement job"), closeFor("Prepaid job"), closeFor("Open request"), closeFor("Stale request"), closeFor("Answered request")
	waitingJob(statementJob, "reconciliation")
	waitingJob(prepaidJob, "prepaid")
	request(openRequest, false, true)
	request(staleRequest, false, false)
	request(answered, true, true)
	rebound := closeFor("Changed binding")
	request(rebound, false, true)
	_, err = db.Exec(`UPDATE work_requests q SET coverage_scope=jsonb_set(q.coverage_scope,'{bindings,0,id}','"0"') FROM request_drafts d WHERE d.id=q.draft_id AND d.close_id::text=$1`, rebound)
	must(err)
	store := savedCloses(db, strings.Repeat("a", 32))
	raw, _ := json.Marshal(map[string]string{"text": "Show closes waiting on statements"})
	r := httptest.NewRequest("POST", "/v1/closes/portfolio", bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"clients:all"}}))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	store.ServeHTTP(w, r)
	var op portfolioOperation
	must(json.Unmarshal(w.Body.Bytes(), &op))
	listed := []string{}
	for _, i := range op.Items {
		listed = append(listed, i.CloseID)
	}
	if w.Code != 200 || len(listed) != 2 || !slices.Contains(listed, statementJob) || !slices.Contains(listed, openRequest) || !strings.Contains(op.Scope, "statement reconciliation work waiting on the client") {
		t.Fatalf("only statement reconciliation work waiting on the client and a current, open request for a bank account are listed: %d %v %s", w.Code, listed, op.Scope)
	}
}
