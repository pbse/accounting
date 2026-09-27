package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestChatDelegateIsOnlyAnAskToHandOverTheClose(t *testing.T) {
	sunday := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	for text, want := range map[string]string{
		"Get this close ready for review by Friday":                    "2026-10-02",
		"Get Acme's September close ready for review by Friday":        "2026-10-02",
		"Please hand this close to the agent":                          "",
		"delegate":                                                     "",
		"Can you get this ready for review before 2026-10-05?":         "2026-10-04",
		"Have the agent get it ready for review, due on tomorrow":      "2026-09-28",
		"Make the Acme Services books ready for review due this week":  "2026-09-27",
		"Get this close ready for review by Friday, please, thank you": "2026-10-02",
	} {
		if due, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); !ok || len(extra) > 0 || due != want {
			t.Errorf("%q: got %q %v %v, want %q", text, due, extra, ok, want)
		}
	}
	// A request with words the agent would not act on keeps them, so the reply can name them instead of dropping them.
	for text, want := range map[string]string{
		"Get Acme's September close ready for review by Friday. Use our approved procedures.": "use approved procedures",
		"Get Acme's August close ready for review":                                            "august",
		"Get this close ready for review by Friday but skip the loan":                         "but skip loan",
		"Get this close ready for review by Friday by Monday":                                 "by monday",
		"Hand me the report": "me report",
	} {
		if _, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); !ok || strings.Join(extra, " ") != want {
			t.Errorf("%q: got %v %v, want the extra words %q", text, extra, ok, want)
		}
	}
	for _, text := range []string{
		"Is this close ready for review?",
		"Get this ready",
		"Get this close ready for review by someday",
		"Prepare this close",
	} {
		if due, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); ok {
			t.Errorf("%q is not a request to hand over the close (due %q, extra %v)", text, due, extra)
		}
	}
}

func TestCloseGoalKeepsPreparationMovingUntilReadyForReview(t *testing.T) {
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
	org := "org_Goal" + rand.Text()
	var alpha, beta string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Goal fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture'),($1,'user_a','Beta',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`,
			`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`UPDATE evidence SET superseded_by=NULL WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	document := func(closeID, name string) (string, string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		defer tx.Rollback()
		var source string
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, closeID, name, preparation.Digest([]byte(name)), "Invoice "+name).Scan(&source))
		job, err := preparation.StartIntake(ctx, tx, closeID, source, "fixture")
		must(err)
		must(tx.Commit())
		return source, job
	}
	draft := func(closeID, paper string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, "goal-"+rand.Text(), paper).Scan(&id))
		return id
	}
	complete := func(job, paper string) {
		t.Helper()
		_, err := db.Exec(`UPDATE preparation_jobs SET state='completed',workpaper_ids=jsonb_build_array($2::text),finished_at=now() WHERE id::text=$1`, job, paper)
		must(err)
	}
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
	reviewer := []string{"workpapers:review"}
	type status struct {
		Goal *struct {
			ID     string `json:"id"`
			Author string `json:"author"`
			DueOn  string `json:"due_on"`
			State  string `json:"state"`
			Events []struct {
				Actor string `json:"actor"`
				Text  string `json:"text"`
			} `json:"events"`
		} `json:"goal"`
		Progress goalProgress `json:"progress"`
		Activity []struct {
			Actor string `json:"actor"`
			Text  string `json:"text"`
		} `json:"activity"`
	}
	read := func(closeID string) status {
		t.Helper()
		var s status
		must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/"+closeID+"/goal", nil, 200), &s))
		return s
	}
	lastEvent := func(s status) string {
		return s.Goal.Events[len(s.Goal.Events)-1].Text
	}
	runs := func(closeID string) int {
		t.Helper()
		var n int
		must(db.QueryRow(`SELECT count(*) FROM close_preparations WHERE close_id::text=$1`, closeID).Scan(&n))
		return n
	}

	_, job1 := document(alpha, "insurance.pdf")
	type message struct {
		ID      string          `json:"id"`
		Action  string          `json:"action"`
		DueOn   string          `json:"due_on"`
		Outcome json.RawMessage `json:"outcome"`
	}
	var chat struct{ Messages []message }
	must(json.Unmarshal(call("user_a", nil, "POST", "/v1/closes/"+alpha+"/chat", map[string]string{"text": "Get this close ready for review by Friday", "today": "2026-09-27"}, 200), &chat))
	card := chat.Messages[len(chat.Messages)-1]
	if card.Action != "delegate" || card.DueOn != "2026-10-02" {
		t.Fatalf("the request becomes a delegate card with its deadline: %+v", card)
	}
	confirm := "/v1/closes/" + alpha + "/chat/" + card.ID + "/confirm"
	call("user_a", nil, "POST", confirm, map[string]string{"due_on": card.DueOn}, 403)
	if s := read(alpha); s.Goal != nil || runs(alpha) != 0 {
		t.Fatalf("without workpapers:review nothing is handed over: %+v", s.Goal)
	}
	must(json.Unmarshal(call("user_a", reviewer, "POST", confirm, map[string]string{"due_on": card.DueOn}, 200), &chat))
	if out := chat.Messages[len(chat.Messages)-1]; !strings.Contains(string(out.Outcome), `"goal_id"`) {
		t.Fatalf("the card records the goal it started: %s", out.Outcome)
	}
	call("user_a", reviewer, "POST", confirm, map[string]string{"due_on": card.DueOn}, 409)
	s := read(alpha)
	var actor string
	must(db.QueryRow(`SELECT actor FROM close_preparations WHERE close_id::text=$1`, alpha).Scan(&actor))
	if s.Goal == nil || s.Goal.State != "active" || s.Goal.DueOn != "2026-10-02" || s.Goal.Events[0].Text != "Handed over by user_a, to be ready for review by Fri 2 Oct 2026" || !strings.HasPrefix(lastEvent(s), "I brought 1 document into preparation as run #") || actor != "close goal #"+s.Goal.ID+" for user_a" {
		t.Fatalf("handing over records the goal and brings the documents into preparation as the agent: %+v %q", s.Goal, actor)
	}
	if len(s.Progress.Items) != 1 || s.Progress.Items[0].Status != "working" || s.Progress.Reached || len(s.Activity) != 2 || s.Activity[1].Text != s.Goal.Events[0].Text {
		t.Fatalf("the unread document is in progress and the history shows both events, newest first: %+v %+v", s.Progress, s.Activity)
	}
	call("user_a", reviewer, "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true}, 409)
	call("user_b", reviewer, "GET", "/v1/closes/"+alpha+"/goal", nil, 404)

	// New evidence is brought into preparation once; an unchanged close records nothing more.
	_, job2 := document(alpha, "rent.pdf")
	sweepCloseGoals(ctx, db)
	sweepCloseGoals(ctx, db)
	if s = read(alpha); runs(alpha) != 2 || len(s.Goal.Events) != 3 || !strings.HasPrefix(lastEvent(s), "I brought 2 documents into preparation") {
		t.Fatalf("the new document joins the scope once: %d runs %+v", runs(alpha), s.Goal.Events)
	}

	// Each document says what it waits on; a blocked draft keeps the goal open until its revision clears it.
	_, err = db.Exec(`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","questions":[],"facts":[{"field":"amount","value":"1200.00","question":""},{"field":"service_end","value":"","question":"Which months does the policy cover?"}]}' WHERE job_id::text=$1`, job1)
	must(err)
	_, err = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, job1)
	must(err)
	if s = read(alpha); s.Progress.Items[0].Status != "needs_you" || s.Progress.Items[0].Text != "Answer 1 question in Work: Which months does the policy cover?" {
		t.Fatalf("an open question is a decision for a person: %+v", s.Progress.Items)
	}
	blocked := draft(alpha, `{"blockers":["Confirm the service period."]}`)
	complete(job1, blocked)
	complete(job2, draft(alpha, `{}`))
	sweepCloseGoals(ctx, db)
	if s = read(alpha); s.Goal.State != "active" || s.Progress.Reached || s.Progress.Items[0].Text != "Draft #"+blocked+" is blocked: Confirm the service period." {
		t.Fatalf("a blocked draft is not ready for review: %+v", s.Progress.Items)
	}
	draft(alpha, `{"revises":"`+blocked+`"}`)
	sweepCloseGoals(ctx, db)
	if s = read(alpha); s.Goal.State != "reached" || lastEvent(s) != "Every uploaded document has a draft ready for review: 2 drafts from 2 documents." {
		t.Fatalf("the revised draft makes every document ready: %+v %+v", s.Goal, s.Progress.Items)
	}
	var attention struct {
		Groups []struct {
			Events []attentionEvent
		}
	}
	must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/attention", nil, 200), &attention))
	found := false
	for _, g := range attention.Groups {
		for _, e := range g.Events {
			found = found || e.Kind == "goal" && e.Text == "Every uploaded document has a draft ready for review"
		}
	}
	if !found {
		t.Fatalf("the owner is told the close is ready: %+v", attention.Groups)
	}

	// New work reopens the goal; a scope that returns to an earlier run is recorded once.
	third, _ := document(alpha, "lease.pdf")
	sweepCloseGoals(ctx, db)
	if s = read(alpha); s.Goal.State != "active" || lastEvent(s) != "New work arrived after the drafts were ready, so I'm back on it." || runs(alpha) != 3 {
		t.Fatalf("new evidence puts the agent back to work: %+v", s.Goal.Events)
	}
	// Replacing the new document with a copy that has no job of its own returns the scope to the earlier run.
	var replacement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'lease v2.pdf','goal-lease-v2','Lease','fixture') RETURNING id::text`, alpha).Scan(&replacement))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2::bigint WHERE id::text=$1`, third, replacement)
	must(err)
	sweepCloseGoals(ctx, db)
	sweepCloseGoals(ctx, db)
	if s = read(alpha); s.Goal.State != "reached" || runs(alpha) != 3 || !strings.Contains(s.Goal.Events[len(s.Goal.Events)-2].Text, "The documents match preparation run #") {
		t.Fatalf("the earlier run is reused once: %+v", s.Goal.Events)
	}

	// Stopping needs a reason and hands the close back; a new goal can be started.
	call("user_a", reviewer, "POST", "/v1/closes/"+alpha+"/goal/stop", map[string]string{"reason": " "}, 422)
	call("user_a", reviewer, "POST", "/v1/closes/"+alpha+"/goal/stop", map[string]string{"reason": "Reviewing it myself"}, 200)
	call("user_a", reviewer, "POST", "/v1/closes/"+alpha+"/goal/stop", map[string]string{"reason": "Again"}, 409)
	if s = read(alpha); s.Goal.State != "stopped" || lastEvent(s) != "Stopped by user_a: Reviewing it myself" {
		t.Fatalf("the stop is recorded: %+v", s.Goal)
	}

	// A goal on a close that is then signed off stops; a signed-off close cannot be handed over.
	call("user_a", reviewer, "POST", "/v1/closes/"+beta+"/goal", map[string]any{"due_on": ""}, 422)
	call("user_a", reviewer, "POST", "/v1/closes/"+beta+"/goal", map[string]any{"confirmed": true, "due_on": "2026-09-01"}, 422)
	call("user_a", reviewer, "POST", "/v1/closes/"+beta+"/goal", map[string]any{"confirmed": true}, 200)
	if s = read(beta); s.Goal.State != "active" || len(s.Progress.Needs) != 1 || !strings.HasPrefix(s.Progress.Needs[0], "Add the client's documents") || runs(beta) != 0 {
		t.Fatalf("a close without documents asks for them: %+v", s.Progress)
	}
	decide := func(decision string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,$2,'fixture','{}','Fixture','fixture')`, beta, decision)
		must(err)
	}
	decide("signed_off")
	sweepCloseGoals(ctx, db)
	if s = read(beta); s.Goal.State != "stopped" || lastEvent(s) != "The close was signed off, so there is nothing left for me to prepare." {
		t.Fatalf("sign-off ends the goal: %+v", s.Goal)
	}
	call("user_a", reviewer, "POST", "/v1/closes/"+beta+"/goal", map[string]any{"confirmed": true}, 409)
	decide("reopened")
	call("user_a", reviewer, "POST", "/v1/closes/"+beta+"/goal", map[string]any{"confirmed": true}, 200)

	// Work waiting on the client says whether the request still needs sending or resuming.
	source, job := document(beta, "statement.pdf")
	var request string
	must(db.QueryRow(`WITH d AS (INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'1. Send the statement.','[]','x','fixture') RETURNING id)
 INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) SELECT $2,id,0,'client@example.test','fixture' FROM d RETURNING id::text`, beta, job).Scan(&request))
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id::text=$1`, job)
	must(err)
	waiting := func() string {
		t.Helper()
		return read(beta).Progress.Items[0].Text
	}
	if got := waiting(); got != "Request #"+request+" is approved but not sent. Send it, or record how you delivered it." {
		t.Fatalf("an unsent request is a decision: %q", got)
	}
	_, err = db.Exec(`UPDATE work_requests SET delivered_at=now(),delivered_by='fixture',delivery_reference='Phoned' WHERE id::text=$1`, request)
	must(err)
	if got := waiting(); got != "Waiting on the client's reply to request #"+request+"." {
		t.Fatalf("a delivered request waits on the client: %q", got)
	}
	_, err = db.Exec(`INSERT INTO request_submissions(request_id,receipt,source_id,complete) VALUES($1,'partial',$2,false)`, request, source)
	must(err)
	if got := waiting(); got != "The client answered part of request #"+request+". Resume now from Work, or wait for the rest." {
		t.Fatalf("a partial reply is a decision: %q", got)
	}
	var history []string
	for _, a := range read(beta).Activity {
		history = append(history, a.Text)
	}
	if want := []string{"The client answered part of request #" + request, "fixture recorded delivery of request #" + request, "fixture approved request #" + request + " to the client"}; !slices.Equal(history[:3], want) {
		t.Fatalf("the history keeps the request's steps, newest first: %q", history)
	}

	// The owner leaving the firm stops the goal before it brings in anything more.
	call("user_a", reviewer, "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true}, 200)
	before := runs(alpha)
	document(alpha, "late.pdf")
	_, err = db.Exec(`UPDATE organizations SET enabled=false WHERE id=$1`, org)
	must(err)
	sweepCloseGoals(ctx, db)
	if runs(alpha) != before {
		t.Fatalf("a suspended firm's goal waits: %d/%d", runs(alpha), before)
	}
	_, err = db.Exec(`UPDATE organizations SET enabled=true WHERE id=$1`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',false,'Left the firm','fixture')`, org)
	must(err)
	sweepCloseGoals(ctx, db)
	var state, last string
	must(db.QueryRow(`SELECT state,events->-1->>'text' FROM close_goals WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, alpha).Scan(&state, &last))
	if state != "stopped" || last != "I stopped because user_a is no longer a member of the firm." || runs(alpha) != before {
		t.Fatalf("a removed owner's goal stops without acting: %s %q %d/%d", state, last, runs(alpha), before)
	}
}

func TestFirmWideRequestLinksTheCloseItNames(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_Name" + rand.Text()
	var alpha string
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Name fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha Labs',true,'Synthetic','fixture')`} {
		if _, err = db.Exec(q, org); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha Labs','2026-09') RETURNING id::text`, org).Scan(&alpha); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	ask := func(subject, text string) (string, []chatCitation) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"text": text, "today": "2026-09-27"})
		r := httptest.NewRequest("POST", "/v1/closes/chat", bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		var out struct {
			Messages []struct {
				Body string `json:"body"`
			} `json:"messages"`
			Live []chatCitation `json:"live_citations"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		return out.Messages[len(out.Messages)-1].Body, out.Live
	}
	body, links := ask("user_a", "Get Alpha Labs' September close ready for review by Friday")
	want := "/dashboard/closes/" + alpha + "?ask=" + url.QueryEscape("Get this close ready for review by 2026-10-02")
	if len(links) != 1 || links[0].Href != want || strings.Contains(body, "Alpha") {
		t.Fatalf("the named close is linked once and history keeps a generic reply: %q %+v", body, links)
	}
	if _, links = ask("user_a", "Get Alpha ready for review"); len(links) != 1 {
		t.Fatalf("one distinctive word of the client's name is enough: %+v", links)
	}
	if _, links = ask("user_a", "Get Alpha Labs' August close ready for review"); len(links) != 0 {
		t.Fatalf("another month is not this close: %+v", links)
	}
	if body, links = ask("user_a", "Get Alpha's September close ready for review by Friday. Use our approved procedures."); len(links) != 0 || !strings.Contains(body, "I don't act on other instructions, such as “use approved procedures”") {
		t.Fatalf("an instruction the agent would not follow is named, not dropped: %q %+v", body, links)
	}
	if _, links = ask("user_b", "Get Alpha Labs ready for review"); len(links) != 0 {
		t.Fatalf("a close the member cannot access is never linked: %+v", links)
	}
}
