package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestAttentionInboxIsScopedPersonalAndDerivedFromCurrentState(t *testing.T) {
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
	org := "org_Attention" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Attention fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	var alpha, beta string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM attention_marks WHERE organization_id=$1`, `DELETE FROM attention_preferences WHERE organization_id=$1`,
			`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM email_delivery_events WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`,
			`DELETE FROM email_outbox WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`,
			`DELETE FROM email_connections WHERE organization_id=$1`,
			`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`UPDATE evidence SET superseded_by=NULL WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	evidence := func(closeID, content string) string {
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(content)), content).Scan(&id))
		return id
	}
	draft := func(closeID, source, content string) string {
		raw, _ := json.Marshal(map[string]any{"sources": []map[string]string{{"id": source, "digest": preparation.Digest([]byte(content))}}})
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, "attention-"+source, string(raw)).Scan(&id))
		return id
	}
	alphaSource := evidence(alpha, "Alpha invoice")
	alphaDraft := draft(alpha, alphaSource, "Alpha invoice")
	betaSource := evidence(beta, "Beta invoice")
	draft(beta, betaSource, "Beta invoice")
	tx, _ := db.Begin()
	job, err := preparation.StartIntake(t.Context(), tx, alpha, alphaSource, "fixture")
	if err != nil || tx.Commit() != nil {
		t.Fatal(err)
	}
	var request string
	must(db.QueryRow(`WITH d AS (INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'1. Send the invoice.','[]','x','fixture') RETURNING id)
 INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) SELECT $2,id,0,'client@example.test','fixture' FROM d RETURNING id::text`, alpha, job).Scan(&request))
	submit := func(receipt string, complete bool) {
		_, err := db.Exec(`INSERT INTO request_submissions(request_id,receipt,source_id,complete) VALUES($1,$2,$3,$4)`, request, receipt, alphaSource, complete)
		must(err)
	}

	store := savedCloses(db, strings.Repeat("a", 32))
	type inbox struct {
		Groups []struct {
			CloseID string `json:"close_id"`
			Owner   string
			Events  []attentionEvent
		}
		Unread     int
		UnreadKeys []string `json:"unread_keys"`
		HasMore    bool     `json:"has_more"`
		Page       int
	}
	call := func(subject string, permissions []string, method, path string, body any, want int) inbox {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		var out inbox
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	mine := func(view string) (inbox, map[string]attentionEvent) {
		t.Helper()
		got := call("user_a", nil, "GET", "/v1/closes/attention"+view, nil, 200)
		events := map[string]attentionEvent{}
		for _, g := range got.Groups {
			if g.CloseID != alpha {
				t.Fatalf("a member sees only the clients they are assigned: %+v", got)
			}
			for _, e := range g.Events {
				events[e.Kind] = e
			}
		}
		return got, events
	}
	mark := func(action string, keys []string, until string, want int) {
		t.Helper()
		call("user_a", nil, "POST", "/v1/closes/attention", map[string]any{"action": action, "keys": keys, "until": until}, want)
	}
	got, events := mine("")
	if got.Unread != 1 || len(events) != 1 || events["approve"].Key == "" {
		t.Fatalf("work still waiting for its evidence is routine progress, not an event: %+v", got)
	}
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, job)
	must(err)
	var redated bool
	must(db.QueryRow(`SELECT state_changed_at>created_at FROM preparation_jobs WHERE id=$1`, job).Scan(&redated))
	if !redated {
		t.Fatal("a job's state change is dated when it happens")
	}
	if got, events = mine(""); events["send"].Text != "Request #"+request+" is approved; no delivery is recorded" {
		t.Fatalf("an approved request with no delivery needs a person: %+v", got)
	}
	submit("first", false)
	got, events = mine("")
	if later := call("user_a", nil, "GET", "/v1/closes/attention?page=9223372036854775807", nil, 200); later.Page != 0 || len(later.Groups) != 1 || later.HasMore || len(later.UnreadKeys) != 2 {
		t.Fatalf("a page past the end is the last page, and the unread keys cover every item: %+v", later)
	}
	if got.Unread != 2 || len(events) != 2 || events["approve"].Key != "approve:"+alphaDraft || events["answered"].Text != "Client replied to request #"+request+" (partial)" {
		t.Fatalf("the ready draft and the partial reply need a person, the waiting job does not: %+v", got)
	}
	if all := call("user_admin", []string{"clients:all"}, "GET", "/v1/closes/attention", nil, 200); len(all.Groups) != 2 || all.Unread != 3 {
		t.Fatalf("firm access sees every client's events: %+v", all)
	}
	var betaKey string
	for _, g := range call("user_admin", []string{"clients:all"}, "GET", "/v1/closes/attention", nil, 200).Groups {
		if g.CloseID == beta {
			betaKey = g.Events[0].Key
		}
	}
	mark("read", []string{betaKey}, "", 409)

	mark("read", []string{events["answered"].Key}, "", 200)
	if got, events = mine(""); got.Unread != 1 || !events["answered"].Read {
		t.Fatalf("a read event stays listed but stops counting: %+v", got)
	}
	if all := call("user_admin", []string{"clients:all"}, "GET", "/v1/closes/attention", nil, 200); all.Unread != 3 {
		t.Fatalf("one member's read mark is not another's: %+v", all)
	}
	if _, unread := mine("?view=unread"); unread["answered"].Key != "" || unread["approve"].Key == "" {
		t.Fatalf("the unread view leaves read items out before paging: %+v", unread)
	}
	submit("second", true)
	if got, events = mine(""); got.Unread != 2 || events["answered"].Read || events["answered"].Text != "Client replied to request #"+request {
		t.Fatalf("the client's next reply is a new, unread event: %+v", got)
	}
	approve := events["approve"].Key
	mark("snooze", []string{approve}, time.Now().Add(24*time.Hour).Format(time.RFC3339), 200)
	mark("snooze", []string{approve}, time.Now().Add(40*24*time.Hour).Format(time.RFC3339), 422)
	if got, events = mine(""); got.Unread != 1 || events["approve"].Key != "" {
		t.Fatalf("a snoozed event is hidden and not counted: %+v", got)
	}
	if _, snoozed := mine("?view=snoozed"); snoozed["approve"].SnoozedUntil == nil {
		t.Fatalf("snoozed events are listed on their own: %+v", snoozed)
	}
	call("user_a", nil, "POST", "/v1/closes/attention/preferences", map[string]any{"kinds": []string{"approve"}, "browser": true}, 200)
	call("user_a", nil, "POST", "/v1/closes/attention/preferences", map[string]any{"kinds": []string{"everything"}}, 422)
	if got, _ = mine(""); got.Unread != 0 {
		t.Fatalf("kinds a member turned off do not count: %+v", got)
	}
	_, err = db.Exec(`UPDATE attention_marks SET snoozed_until=now()-interval '1 minute' WHERE organization_id=$1 AND key=$2`, org, approve)
	must(err)
	if got, _ = mine(""); got.Unread != 1 {
		t.Fatalf("an expired snooze counts again: %+v", got)
	}
	mark("snooze", []string{approve}, time.Now().Add(24*time.Hour).Format(time.RFC3339), 200)
	mark("wake", []string{approve}, "", 200)
	if got, _ = mine(""); got.Unread != 1 {
		t.Fatalf("a woken event counts again: %+v", got)
	}
	if _, before := mine("?view=mine"); len(before) != 0 {
		t.Fatalf("an unassigned close is not in the member's own view: %+v", before)
	}
	call("user_a", nil, "POST", "/v1/closes/"+alpha+"/planning", map[string]string{"priority": "normal", "assignment": "take"}, 200)
	if got, _ = mine("?view=mine"); len(got.Groups) != 1 || got.Groups[0].Owner != "mine" {
		t.Fatalf("a close the member took is theirs: %+v", got)
	}
	for _, g := range call("user_admin", []string{"clients:all"}, "GET", "/v1/closes/attention", nil, 200).Groups {
		if g.CloseID == alpha && g.Owner != "other" {
			t.Fatalf("to everyone else it is someone else's: %+v", g)
		}
	}
	if all := call("user_admin", []string{"clients:all"}, "GET", "/v1/closes/attention", nil, 200); all.Unread != 3 {
		t.Fatalf("marks and preferences are each member's own: %+v", all)
	}

	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest) SELECT id,'approved','Synthetic approval','fixture',digest FROM workpapers WHERE id=$1`, alphaDraft)
	must(err)
	replacement := evidence(alpha, "Alpha invoice, reissued")
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$1 WHERE id::text=$2`, replacement, alphaSource)
	must(err)
	got, events = mine("")
	if events["changed"].Text != "A source of draft #"+alphaDraft+" changed after it was approved" || events["approve"].Key != "" {
		t.Fatalf("a replaced source on an approved draft is its own event: %+v", got)
	}
	mark("read", []string{events["changed"].Key}, "", 200)
	revision := draft(alpha, replacement, "Alpha invoice, reissued")
	_, err = db.Exec(`UPDATE workpapers SET paper=paper||jsonb_build_object('revises',$1::text) WHERE id::text=$2`, alphaDraft, revision)
	must(err)
	if got, events = mine(""); events["changed"].Key != "" || events["approve"].Key != "approve:"+revision {
		t.Fatalf("a revised draft's events give way to its revision's: %+v", got)
	}
	var stale int
	must(db.QueryRow(`SELECT count(*) FROM attention_marks WHERE organization_id=$1 AND key=$2`, org, approve).Scan(&stale))
	if stale != 0 {
		t.Fatal("marks for events that no longer exist are dropped")
	}
	waitingSource := evidence(alpha, "Alpha lease, unsigned")
	tx, _ = db.Begin()
	waiting, err := preparation.StartIntake(t.Context(), tx, alpha, waitingSource, "fixture")
	if err != nil || tx.Commit() != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, waiting)
	must(err)
	var lapsed, mailbox string
	must(db.QueryRow(`WITH d AS (INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'1. Send the signed lease.','[]','y','fixture') RETURNING id)
 INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) SELECT $2,id,0,'client@example.test','fixture' FROM d RETURNING id::text`, alpha, waiting).Scan(&lapsed))
	_, err = db.Exec(`INSERT INTO client_request_links(digest,request_id,issued_by,expires_at) VALUES($1,$2,'fixture',now()-interval '1 day')`, "attention-"+lapsed, lapsed)
	must(err)
	must(db.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,actor) VALUES($1,'firm@example.test','synthetic','dummy','fixture') RETURNING id::text`, org).Scan(&mailbox))
	_, err = db.Exec(`INSERT INTO email_outbox(id,request_id,connection_id,link_digest,payload,state,provider_id,approved_by,firm_authority,approved_until) VALUES($1,$2,$3,$4,'dummy','accepted','attention-provider','fixture',true,now()+interval '1 hour')`, "attention-mail-"+lapsed, lapsed, mailbox, "attention-"+lapsed)
	must(err)
	if got, events = mine(""); events["unanswered"].Key != "unanswered:"+lapsed+":expiring" || events["unanswered"].JobID != waiting || !strings.Contains(events["unanswered"].Text, "cannot be reminded again before its link expires") {
		t.Fatalf("a request that cannot be reminded again is escalated to the close's owner: %+v", got)
	}
	_, err = db.Exec(`UPDATE client_closes SET owner='user_colleague' WHERE id=$1`, alpha)
	must(err)
	if got, events = mine(""); events["unanswered"].Key != "" {
		t.Fatalf("the escalation goes to the close's owner, not everyone: %+v", got)
	}
	_, err = db.Exec(`UPDATE client_closes SET owner='user_a' WHERE id=$1`, alpha)
	must(err)
	_, err = db.Exec(`INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at) SELECT connection_id,'attention-bounce','x',provider_id,'client@example.test','bounced',now() FROM email_outbox WHERE id=$1`, "attention-mail-"+lapsed)
	must(err)
	if got, events = mine(""); events["unanswered"].Key != "" || events["email"].Key == "" {
		t.Fatalf("an undelivered email is its own item, not an unanswered request: %+v", got)
	}

	var previous string
	must(db.QueryRow(`SELECT id::text FROM current_organization_members WHERE organization_id=$1 AND subject='user_a'`, org).Scan(&previous))
	call("user_admin", []string{"clients:all", "members:manage"}, "POST", "/v1/closes/members", map[string]any{"subject": "user_a", "granted": false, "previous_id": previous, "reason": "Left the firm for another role", "confirmed": true}, 200)
	var left int
	must(db.QueryRow(`SELECT (SELECT count(*) FROM attention_marks WHERE organization_id=$1 AND subject='user_a')+(SELECT count(*) FROM attention_preferences WHERE organization_id=$1 AND subject='user_a')`, org).Scan(&left))
	if left != 0 {
		t.Fatalf("a removed member's marks and preferences go with their membership: %d", left)
	}
}
