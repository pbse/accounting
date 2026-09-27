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
		if finish, due, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); !ok || len(extra) > 0 || due != want || finish != "review" {
			t.Errorf("%q: got %q %q %v %v, want %q", text, finish, due, extra, ok, want)
		}
	}
	// A request to get the close signed off finishes at sign-off; "the 10th" is the next 10th.
	for text, want := range map[string]string{
		"Get this close signed off by the 10th":              "2026-10-10",
		"Close the books by Friday":                          "2026-10-02",
		"Have Acme's September close signed-off by the 30th": "2026-09-30",
		"Please hand this close to the agent to sign off":    "",
		"Close this month by 2026-10-09":                     "2026-10-09",
	} {
		if finish, due, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); !ok || len(extra) > 0 || due != want || finish != "signoff" {
			t.Errorf("%q: got %q %q %v %v, want signoff %q", text, finish, due, extra, ok, want)
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
		if _, _, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); !ok || strings.Join(extra, " ") != want {
			t.Errorf("%q: got %v %v, want the extra words %q", text, extra, ok, want)
		}
	}
	// "Closed" describes a month, and "off" signs off only after "sign"; a review stays a review.
	for text, want := range map[string]string{
		"Get the closed month ready for review":              "review",
		"Hand this close off to the agent":                   "review",
		"Close the review by Friday":                         "review",
		"Get this close sign-off ready by 2026-10-09 please": "signoff",
	} {
		if finish, _, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); !ok || len(extra) > 0 || finish != want {
			t.Errorf("%q: got %q %v %v, want %q", text, finish, extra, ok, want)
		}
	}
	// On January 31, "the 30th" is March 30: February has none.
	if day, _, ok := parseDay([]string{"the", "30th"}, 0, time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)); !ok || day.Format("2006-01-02") != "2027-03-30" {
		t.Errorf("the next 30th: %v %v", day, ok)
	}
	for _, text := range []string{
		"Is this close ready for review?",
		"Get this ready",
		"Get this close ready for review by someday",
		"Prepare this close",
	} {
		if _, due, extra, ok := chatDelegate(text, sunday, "Acme Services", "2026-09"); ok {
			t.Errorf("%q is not a request to hand over the close (due %q, extra %v)", text, due, extra)
		}
	}
}

// chatDay is the date the chat reads a day phrase as today, and today as the browser sends it.
func chatDay(words ...string) (time.Time, string) {
	today := time.Now().UTC().Format("2006-01-02")
	day, _, _ := parseDay(words, 0, browserToday(today))
	return day, today
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
	friday, today := chatDay("friday")
	must(json.Unmarshal(call("user_a", nil, "POST", "/v1/closes/"+alpha+"/chat", map[string]string{"text": "Get this close ready for review by Friday", "today": today}, 200), &chat))
	card := chat.Messages[len(chat.Messages)-1]
	if card.Action != "delegate" || card.DueOn != friday.Format("2006-01-02") {
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
	if s.Goal == nil || s.Goal.State != "active" || s.Goal.DueOn != friday.Format("2006-01-02") || s.Goal.Events[0].Text != "Handed over by user_a, to get it ready for review by "+friday.Format("Mon 2 Jan 2006") || !strings.HasPrefix(lastEvent(s), "I brought 1 document into preparation as run #") || actor != "close goal #"+s.Goal.ID+" for user_a" {
		t.Fatalf("handing over records the goal and brings the documents into preparation as the agent: %+v %q", s.Goal, actor)
	}
	if len(s.Progress.Items) != 1 || s.Progress.Items[0].Status != "working" || s.Progress.Ready || len(s.Activity) != 2 || s.Activity[1].Text != s.Goal.Events[0].Text {
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
	if s = read(alpha); s.Goal.State != "active" || s.Progress.Ready || s.Progress.Items[0].Text != "Draft #"+blocked+" is blocked: Confirm the service period." {
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
	friday, today := chatDay("friday")
	ask := func(subject, text string) (string, []chatCitation) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"text": text, "today": today})
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
	want := "/dashboard/closes/" + alpha + "?ask=" + url.QueryEscape("Get this close ready for review by "+friday.Format("2006-01-02"))
	if len(links) != 1 || links[0].Href != want || strings.Contains(body, "Alpha") {
		t.Fatalf("the named close is linked once and history keeps a generic reply: %q %+v", body, links)
	}
	if _, links = ask("user_a", "Get Alpha ready for review"); len(links) != 1 {
		t.Fatalf("one distinctive word of the client's name is enough: %+v", links)
	}
	if _, links = ask("user_a", "Get Alpha Labs' August close ready for review"); len(links) != 0 {
		t.Fatalf("another month is not this close: %+v", links)
	}
	if _, links = ask("user_a", "Get Alpha Labs signed off by Friday"); len(links) != 1 || links[0].Href != "/dashboard/closes/"+alpha+"?ask="+url.QueryEscape("Get this close signed off by "+friday.Format("2006-01-02")) {
		t.Fatalf("a firm-wide link keeps the finish line: %+v", links)
	}
	if body, links = ask("user_a", "Get Alpha's September close ready for review by Friday. Use our approved procedures."); len(links) != 0 || !strings.Contains(body, "I don't act on other instructions, such as “use approved procedures”") {
		t.Fatalf("an instruction the agent would not follow is named, not dropped: %q %+v", body, links)
	}
	if _, links = ask("user_b", "Get Alpha Labs ready for review"); len(links) != 0 {
		t.Fatalf("a close the member cannot access is never linked: %+v", links)
	}
}

func TestSignOffGoalCarriesTheCloseThroughSignOff(t *testing.T) {
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
	org := "org_Sign" + rand.Text()
	var alpha string
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Sign-off fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM sandbox_postings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
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
		return job
	}
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type status struct {
		Goal *struct {
			State   string  `json:"state"`
			Finish  string  `json:"finish"`
			ReadyAt *string `json:"ready_at"`
			Events  []struct {
				Text string `json:"text"`
			} `json:"events"`
		} `json:"goal"`
		Progress goalProgress `json:"progress"`
		Activity []struct {
			Text string `json:"text"`
		} `json:"activity"`
	}
	read := func() status {
		t.Helper()
		var s status
		must(json.Unmarshal(call("GET", "/v1/closes/"+alpha+"/goal", nil, 200), &s))
		return s
	}
	last := func(s status) string { return s.Goal.Events[len(s.Goal.Events)-1].Text }
	runs := func() int {
		t.Helper()
		var n int
		must(db.QueryRow(`SELECT count(*) FROM close_preparations WHERE close_id::text=$1`, alpha).Scan(&n))
		return n
	}

	job := document("insurance.pdf")
	call("POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true, "finish": "sometime"}, 422)
	var chat struct {
		Messages []struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			DueOn  string `json:"due_on"`
			Finish string `json:"finish"`
		}
	}
	tenth, today := chatDay("the", "10th")
	must(json.Unmarshal(call("POST", "/v1/closes/"+alpha+"/chat", map[string]string{"text": "Get this close signed off by the 10th", "today": today}, 200), &chat))
	card := chat.Messages[len(chat.Messages)-1]
	if card.Action != "delegate" || card.Finish != "signoff" || card.DueOn != tenth.Format("2006-01-02") {
		t.Fatalf("the card proposes sign-off by the 10th: %+v", card)
	}
	call("POST", "/v1/closes/"+alpha+"/chat/"+card.ID+"/confirm", map[string]string{"due_on": card.DueOn, "finish": card.Finish}, 200)
	if s := read(); s.Goal.Finish != "signoff" || s.Goal.Events[0].Text != "Handed over by user_a, to get it signed off by "+tenth.Format("Mon 2 Jan 2006") || s.Progress.Signoff != nil {
		t.Fatalf("a sign-off goal carries its finish line, and sign-off waits for the documents: %+v %+v", s.Goal, s.Progress.Signoff)
	}

	// Drafts ready for review are a milestone, recorded once; approving them is the person's.
	var paper string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','sign-draft','{"proposal":{"lines":[]}}') RETURNING id::text`, alpha).Scan(&paper))
	_, err = db.Exec(`UPDATE preparation_jobs SET state='completed',workpaper_ids=jsonb_build_array($2::text),finished_at=now() WHERE id::text=$1`, job, paper)
	must(err)
	sweepCloseGoals(ctx, db)
	sweepCloseGoals(ctx, db)
	s := read()
	milestones := 0
	for _, e := range s.Goal.Events {
		if strings.HasSuffix(e.Text, "Next: approvals and sign-off.") {
			milestones++
		}
	}
	if s.Goal.State != "active" || s.Goal.ReadyAt == nil || milestones != 1 || s.Progress.Items[0].Text != "Review and approve draft #"+paper+"." || !slices.Contains(s.Progress.Signoff.Needs, "Define the supported scope and required work before sign-off.") {
		t.Fatalf("the milestone is recorded once and the goal lists approval and scope: %+v %+v", s.Goal, s.Progress)
	}
	// Work's list of delegated closes counts what the card lists: the approval and what sign-off needs.
	summary := func() (int, string) {
		t.Helper()
		var needs int
		var signoff string
		must(db.QueryRow(`SELECT COALESCE((summary->>'needs')::int,-1),COALESCE(summary->>'signoff','') FROM close_goals WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, alpha).Scan(&needs, &signoff))
		return needs, signoff
	}
	if needs, signoff := summary(); needs != 1+len(s.Progress.Signoff.Needs) || signoff != s.Progress.Signoff.State {
		t.Fatalf("the summary counts what the card lists: %d %q %+v", needs, signoff, s.Progress.Signoff)
	}
	// Work on a replaced document is not on the list, so sign-off still names it.
	stale := document("stale.pdf")
	var replacement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'stale v2.pdf','sign-stale-v2','Replacement','fixture') RETURNING id::text`, alpha).Scan(&replacement))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2::bigint WHERE id=(SELECT source_id FROM preparation_intakes WHERE job_id::text=$1)`, stale, replacement)
	must(err)
	if s = read(); !slices.Contains(s.Progress.Signoff.Needs, "Work #"+stale+" is waiting evidence.") {
		t.Fatalf("unfinished work on a replaced document still blocks sign-off: %+v", s.Progress.Signoff)
	}
	_, err = db.Exec(`UPDATE preparation_jobs SET state='cancelled' WHERE id::text=$1`, stale)
	must(err)
	_, err = db.Exec(`INSERT INTO close_scopes(close_id,scope,actor) VALUES($1,jsonb_build_object('description','Prepaids','required_papers',jsonb_build_array($2::text),'external_steps','[]'::jsonb),'fixture')`, alpha, paper)
	must(err)
	if s = read(); len(s.Progress.Signoff.Needs) != 0 || s.Progress.Items[0].Status != "needs_you" {
		t.Fatalf("a required draft still to approve is listed once, as its document: %+v", s.Progress)
	}
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest) SELECT id,'approved','Checked','fixture',digest FROM workpapers WHERE id::text=$1`, paper)
	must(err)
	if s = read(); s.Progress.Items[0].Status != "prepared" || len(s.Progress.Signoff.Entries) != 1 || s.Progress.Signoff.Entries[0].Text != "Not posted in the app. Post it from the draft, or book it in the ledger yourself." {
		t.Fatalf("an approved draft's entry shows it is not posted: %+v", s.Progress)
	}
	_, err = db.Exec(`INSERT INTO sandbox_postings(close_id,workpaper_id,source_key,realm,digest,journal,state,attempted_by,attempted_at,external_id,verified_at) VALUES($1,$2,'fixture','realm','fixture','{}','verified','fixture',now(),'J-7',now())`, alpha, paper)
	must(err)
	if s = read(); s.Progress.Signoff.Entries[0].Text != "Posted to the QuickBooks sandbox and confirmed by its readback (J-7)." {
		t.Fatalf("a verified posting names its readback: %+v", s.Progress.Signoff.Entries)
	}

	// Sign-off reaches the goal only while it stands; a later change waits for a person to reopen the close.
	tx, err := db.BeginTx(ctx, nil)
	must(err)
	ready, err := closeReadiness(ctx, tx, alpha)
	must(err)
	tx.Rollback()
	if len(ready.Blockers) != 0 || len(read().Progress.Signoff.Needs) != 0 {
		t.Fatalf("with scope defined and the draft approved, nothing blocks sign-off: %v", ready.Blockers)
	}
	decide := func(decision, note string) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		ready, err := closeReadiness(ctx, tx, alpha)
		must(err)
		tx.Rollback()
		_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,$2,$3,$4,$5,'user_a')`, alpha, decision, ready.Digest, string(ready.Snapshot), note)
		must(err)
	}
	decide("signed_off", "Done")
	sweepCloseGoals(ctx, db)
	if s = read(); s.Goal.State != "reached" || last(s) != "Signed off by user_a." || s.Progress.Signoff.State != "signed_off" || s.Progress.Signoff.Signer != "user_a" {
		t.Fatalf("a standing sign-off reaches the goal: %+v %+v", s.Goal, s.Progress.Signoff)
	}
	if _, signoff := summary(); signoff != "signed_off" {
		t.Fatalf("the summary says the close is signed off: %q", signoff)
	}
	var next string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-10') RETURNING id::text`, org).Scan(&next))
	if s = read(); s.Progress.Signoff.NextClose != next {
		t.Fatalf("next month's close is linked once it exists: %+v", s.Progress.Signoff)
	}
	decide("reopened", "Checking a rate")
	sweepCloseGoals(ctx, db)
	if s = read(); s.Goal.State != "active" || last(s) != "The close was reopened, so I'm back on it." {
		t.Fatalf("a reopened close puts the agent back to work: %+v", s.Goal)
	}
	decide("signed_off", "Done again")
	sweepCloseGoals(ctx, db)
	before := runs()
	document("late.pdf")
	sweepCloseGoals(ctx, db)
	sweepCloseGoals(ctx, db)
	if s = read(); s.Goal.State != "active" || last(s) != "Something changed after the sign-off, so the close needs reopening before I continue." || s.Progress.Signoff.State != "reopen_required" || runs() != before {
		t.Fatalf("a change after sign-off waits for a reopen and prepares nothing: %+v %d/%d", s.Goal, runs(), before)
	}
	decide("reopened", "New invoice")
	sweepCloseGoals(ctx, db)
	if s = read(); s.Goal.State != "active" || runs() != before+1 || last(s) != "New work arrived after the drafts were ready, so I'm back on it." {
		t.Fatalf("after the reopen the agent brings the change into preparation: %+v %d/%d", s.Goal.Events, runs(), before)
	}
	history := []string{}
	for _, a := range read().Activity {
		history = append(history, a.Text)
	}
	for _, want := range []string{"user_a signed off the close: Done", "fixture set what sign-off covers: Prepaids", "fixture posted the entry for draft #" + paper + " to the QuickBooks sandbox", "the QuickBooks sandbox confirmed the entry for draft #" + paper + " (J-7)", "user_a reopened the close: New invoice"} {
		if !slices.Contains(history, want) {
			t.Fatalf("the history has %q: %q", want, history)
		}
	}
}

func TestWorkListsEveryDelegatedClose(t *testing.T) {
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
	org := "org_List" + rand.Text()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Delegated list fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_b','Alpha',true,'Synthetic','fixture'),($1,'user_b','Beta',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
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
	// Each close gets one document, waiting to be read unless it needs a person, and is handed to the agent.
	handOver := func(entity string, needsPerson bool) (string, string) {
		t.Helper()
		var closeID, source string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		defer tx.Rollback()
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Invoice','fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(entity))).Scan(&source))
		job, err := preparation.StartIntake(ctx, tx, closeID, source, "fixture")
		must(err)
		must(tx.Commit())
		if needsPerson {
			_, err = db.Exec(`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","questions":["Which months does the policy cover?"],"facts":[]}' WHERE job_id::text=$1`, job)
			must(err)
			_, err = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, job)
			must(err)
		}
		call("user_a", partner, "POST", "/v1/closes/"+closeID+"/goal", map[string]any{"confirmed": true}, 200)
		return closeID, job
	}
	handOver("Alpha", false)
	_, betaJob := handOver("Beta", true)
	gamma, _ := handOver("Gamma", false)
	delta, _ := handOver("Delta", false)
	epsilon, _ := handOver("Epsilon", false)
	call("user_a", partner, "POST", "/v1/closes/"+delta+"/goal/stop", map[string]string{"reason": "Doing it myself"}, 200)
	for _, q := range []string{`UPDATE close_goals SET state='reached',reached_at=now() WHERE close_id::text=$1`,
		`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`} {
		_, err = db.Exec(q, gamma)
		must(err)
	}
	_, err = db.Exec(`UPDATE close_goals SET state='reached',reached_at=now()-interval '15 days' WHERE close_id::text=$1`, epsilon)
	must(err)
	_, err = db.Exec(`INSERT INTO goal_handoffs(goal_id,job_id,document,assignee,assignee_name,via_grant,created_by) SELECT g.id,$2::bigint,'invoice.txt','user_b','user_b',true,'user_a' FROM close_goals g JOIN client_closes c ON c.id=g.close_id WHERE c.organization_id=$1 AND c.entity='Beta'`, org, betaJob)
	must(err)
	type delegated struct {
		Entity  string `json:"entity"`
		State   string `json:"state"`
		Handed  int    `json:"handed"`
		Summary struct {
			Needs int     `json:"needs"`
			Ready bool    `json:"ready"`
			AsOf  *string `json:"as_of"`
		} `json:"summary"`
	}
	list := func(subject string, permissions []string, query string) []delegated {
		t.Helper()
		var out struct {
			Goals []delegated `json:"goals"`
		}
		must(json.Unmarshal(call(subject, permissions, "GET", "/v1/closes/goals"+query, nil, 200), &out))
		return out.Goals
	}
	names := func(goals []delegated) string {
		out := []string{}
		for _, g := range goals {
			out = append(out, g.Entity)
		}
		return strings.Join(out, ",")
	}

	// A close needing a person comes first, then those in progress, then those finished; a stopped goal and one
	// finished more than 14 days ago are not listed, and a signed-off close still is.
	goals := list("user_a", partner, "")
	if names(goals) != "Beta,Alpha,Gamma" {
		t.Fatalf("delegated closes in order: %s", names(goals))
	}
	if a := goals[0]; a.Summary.Needs != 1 || a.Summary.AsOf == nil || a.Handed != 1 || goals[1].Summary.Needs != 0 || goals[1].Summary.Ready || goals[2].State != "reached" {
		t.Fatalf("what the agent last found: %+v", goals)
	}
	if got := names(list("user_b", reviewer, "")); got != "Beta,Alpha" {
		t.Fatalf("a member sees the clients they are assigned to: %s", got)
	}
	if got := names(list("user_a", partner, "?client=bet")); got != "Beta" {
		t.Fatalf("Work's client filter narrows the list: %s", got)
	}
}
