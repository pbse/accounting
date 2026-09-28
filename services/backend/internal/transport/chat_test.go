package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestAssistantAnswersFromTheMembersOwnScopedReads(t *testing.T) {
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
	org := "org_Chat" + rand.Text()
	var alpha, beta, draft, foreign string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Chat fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','chat-draft','{"blockers":["Confirm the service period."],"questions":["Is the policy annual?"]}') RETURNING id::text`, alpha).Scan(&draft))
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','chat-foreign','{}') RETURNING id::text`, beta).Scan(&foreign))
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM close_routines WHERE organization_id=$1`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	type message struct {
		ID         string `json:"id"`
		Role       string `json:"role"`
		Body       string `json:"body"`
		EveryMonth bool   `json:"every_month"`
		Citations  []struct {
			Label string `json:"label"`
			Href  string `json:"href"`
		} `json:"citations"`
	}
	type conversation struct {
		Messages      []message `json:"messages"`
		Live          []string  `json:"live"`
		LiveCitations []struct {
			Href string `json:"href"`
		} `json:"live_citations"`
	}
	call := func(subject string, permissions []string, method, path string, body any, want int) conversation {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		var out conversation
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	closeChat := "/v1/closes/" + alpha + "/chat"
	ask := func(text string, paper ...string) message {
		t.Helper()
		out := call("user_a", nil, "POST", closeChat, map[string]string{"text": text, "workpaper_id": append(paper, "")[0]}, 200)
		last := out.Messages[len(out.Messages)-1]
		if out.Messages[len(out.Messages)-2].Body != text || last.Role != "assistant" {
			t.Fatalf("the question and its answer are saved in order: %d %+v", len(out.Messages), out.Messages[max(0, len(out.Messages)-3):])
		}
		return last
	}
	if got := ask("What blocks sign-off?"); !strings.Contains(got.Body, "1 thing blocks sign-off:") || !strings.Contains(got.Body, "Define the supported scope") || len(got.Citations) != 1 || got.Citations[0].Href != "/dashboard/closes/"+alpha+"?tab=signoff" {
		t.Fatalf("sign-off blockers come from readiness, with a link: %+v", got)
	}
	if got := ask("Where does this close stand?"); !strings.Contains(got.Body, "preparation:") || got.Citations[0].Href != "/dashboard/closes/"+alpha {
		t.Fatalf("the status is the close brief: %+v", got)
	}
	if got := ask("Why doesn't the prepaid balance tie?"); !strings.Contains(got.Body, "no readable month-end Trial Balance") {
		t.Fatalf("the tie-out says it has nothing to tie: %+v", got)
	}
	if got := ask("What changed from last month?"); !strings.Contains(got.Body, "aren't available yet") {
		t.Fatalf("changes wait for two months of snapshots: %+v", got)
	}
	if got := ask("And what about that one?"); !strings.Contains(got.Body, "aren't available yet") {
		t.Fatalf("a question that refers back asks the previous question's read again: %+v", got)
	}
	if got := ask("Any exceptions?"); !strings.Contains(got.Body, "no transaction capture") {
		t.Fatalf("exceptions wait for a capture: %+v", got)
	}
	if got := ask("Explain this draft", draft); !strings.Contains(got.Body, "Draft #"+draft+" (prepaid) is pending") || !strings.Contains(got.Body, "Blocker: Confirm the service period.") || !strings.Contains(got.Body, "Question: Is the policy annual?") || got.Citations[0].Href != "/dashboard/closes/"+alpha+"/workpapers/"+draft {
		t.Fatalf("a draft in context is summarized from its record: %+v", got)
	}
	if got := ask("Is this close ready to sign off?", draft); !strings.Contains(got.Body, "blocks sign-off") {
		t.Fatalf("a close question wins over the open draft: %+v", got)
	}
	call("user_a", nil, "POST", closeChat, map[string]string{"text": "Explain this draft", "workpaper_id": foreign}, 404)
	if got := ask("hello"); !strings.Contains(got.Body, "I answer from this close's saved work") {
		t.Fatalf("an unmatched question gets the help reply: %+v", got)
	}
	// The newest message stays last when ids gain a digit (99 → 100), which a text sort would break.
	var boundary int64
	must(db.QueryRow(`SELECT (10^length((GREATEST(COALESCE(pg_sequence_last_value(pg_get_serial_sequence('chat_messages','id')::regclass),0),COALESCE(max(id),0))+10)::text))::bigint FROM chat_messages`).Scan(&boundary))
	_, err = db.Exec(`INSERT INTO chat_messages(id,organization_id,author,close_id,role,body) OVERRIDING SYSTEM VALUE VALUES($1::bigint,$2,'user_a',$3::bigint,'user','Across a digit'),($1::bigint+1,$2,'user_a',$3::bigint,'assistant','The newest answer')`, boundary, org, alpha)
	must(err)
	if history := call("user_a", nil, "GET", closeChat, nil, 200); history.Messages[len(history.Messages)-1].Body != "The newest answer" {
		t.Fatalf("the newest message stays last when ids gain a digit: %+v", history.Messages[len(history.Messages)-1])
	}
	_, err = db.Exec(`DELETE FROM chat_messages WHERE id IN ($1::bigint,$1::bigint+1)`, boundary)
	must(err)
	if history := call("user_a", nil, "GET", closeChat, nil, 200); len(history.Messages) != 18 {
		t.Fatalf("the conversation keeps every question and answer: %d", len(history.Messages))
	}
	// Another member never sees it, even with access to the client; a member without access to a client cannot use its chat.
	if other := call("user_b", []string{"clients:all"}, "GET", closeChat, nil, 200); len(other.Messages) != 0 {
		t.Fatalf("a conversation is its author's alone: %+v", other.Messages)
	}
	call("user_a", nil, "GET", "/v1/closes/"+beta+"/chat", nil, 404)
	call("user_a", nil, "POST", "/v1/closes/"+beta+"/chat", map[string]string{"text": "What blocks sign-off?"}, 404)

	// Firm-wide answers show live details once and keep only a generic reply.
	firm := call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": "What needs me today?"}, 200)
	if last := firm.Messages[len(firm.Messages)-1]; !strings.HasPrefix(last.Body, "Here is what needs a person today") || strings.Contains(last.Body, "Alpha") || len(firm.Live) == 0 || !strings.Contains(firm.Live[0], "need") {
		t.Fatalf("a firm-wide answer is generic in history with live details: %+v %v", last, firm.Live)
	}
	if again := call("user_a", nil, "GET", "/v1/closes/chat", nil, 200); len(again.Messages) != 2 || len(again.Live) != 0 {
		t.Fatalf("reopening shows the stored generic reply only: %+v", again)
	}
	if got := call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": "Show the review queue"}, 200); !strings.Contains(got.Live[0], "ready for review") {
		t.Fatalf("the review queue is answered live: %v", got.Live)
	}
	if got := call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": "What is blocked?"}, 200); !strings.Contains(got.Live[0], "blocked") {
		t.Fatalf("blocked work is answered live: %v", got.Live)
	}

	// A conversation keeps its latest 200 messages, and firm-wide history expires after 30 days.
	_, err = db.Exec(`INSERT INTO chat_messages(organization_id,author,close_id,role,body) SELECT $1,'user_a',$2,'user','filler' FROM generate_series(1,200)`, org, alpha)
	must(err)
	_, err = db.Exec(`INSERT INTO chat_messages(organization_id,author,role,body,created_at) VALUES($1,'user_a','user','old',now()-interval '31 days')`, org)
	must(err)
	if all := call("user_a", nil, "GET", closeChat, nil, 200); len(all.Messages) != 200 {
		t.Fatalf("the whole retained conversation is returned: %d", len(all.Messages))
	}
	ask("What blocks sign-off?")
	for _, m := range call("user_a", nil, "GET", "/v1/closes/chat", nil, 200).Messages {
		if m.Body == "old" {
			t.Fatal("an expired firm-wide message is never shown")
		}
	}
	var kept, old int
	must(db.QueryRow(`SELECT count(*) FILTER (WHERE close_id IS NOT NULL),count(*) FILTER (WHERE body='old') FROM chat_messages WHERE organization_id=$1 AND author='user_a'`, org).Scan(&kept, &old))
	if kept != 200 || old != 1 {
		t.Fatalf("a close conversation keeps 200 messages; the old firm-wide one waits for a firm-wide message: %d %d", kept, old)
	}
	call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": "hello"}, 200)
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND body='old'`, org).Scan(&old))
	if old != 0 {
		t.Fatal("firm-wide history older than 30 days is removed")
	}
	_, err = db.Exec(`INSERT INTO chat_messages(organization_id,author,role,body,created_at) VALUES($1,'user_b','user','old',now()-interval '31 days')`, org)
	must(err)
	sweep, stop := context.WithCancel(t.Context())
	go func() { time.Sleep(300 * time.Millisecond); stop() }()
	RunChatExpiry(sweep, db)
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND body='old'`, org).Scan(&old))
	if old != 0 {
		t.Fatal("the worker removes expired firm-wide messages of members who never return")
	}

	if cleared := call("user_a", nil, "POST", closeChat, map[string]string{"action": "clear"}, 200); len(cleared.Messages) != 0 {
		t.Fatalf("clearing empties the conversation: %d", len(cleared.Messages))
	}
	var firmLeft int
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND author='user_a' AND close_id IS NULL`, org).Scan(&firmLeft))
	if firmLeft == 0 {
		t.Fatal("clearing one conversation leaves the others")
	}
	// A hand-over asked for every month makes a card that offers the monthly hand-over; confirming sets it up.
	card := ask("Get this close ready for review every month")
	if !card.EveryMonth || !strings.Contains(card.Body, "Check my plan") {
		t.Fatalf("the card keeps that every month was asked for: %+v", card)
	}
	call("user_a", []string{"workpapers:review"}, "POST", closeChat+"/"+card.ID+"/confirm", map[string]any{"every_month": true}, 200)
	var routines int
	must(db.QueryRow(`SELECT count(*) FROM close_routines WHERE organization_id=$1 AND stopped_at IS NULL`, org).Scan(&routines))
	if routines != 1 {
		t.Fatalf("confirming the card sets up the monthly hand-over: %d", routines)
	}
	// Across the firm, a listing goal and the closes handed to the agent are answered once, from the member's own reads.
	_, err = db.Exec(`UPDATE client_closes SET due_on='2026-10-02' WHERE organization_id=$1`, org)
	must(err)
	askFirm := func(text string) conversation {
		t.Helper()
		out := call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": text, "today": "2026-09-27"}, 200)
		if body := out.Messages[len(out.Messages)-1].Body; strings.Contains(body, "Alpha") {
			t.Fatalf("history keeps no client details: %q", body)
		}
		return out
	}
	if out := askFirm("Show closes due by 2026-12-31"); len(out.Live) != 2 || out.Live[0] != "1 close matches: closes you can access that are not signed off, due on or before Thu 31 Dec 2026." || out.Live[1] != "Alpha · 2026-09 · due Fri 2 Oct 2026" {
		t.Fatalf("a listing goal lists the member's closes: %q", out.Live)
	}
	if out := askFirm("How are my delegated closes doing?"); len(out.Live) != 2 || out.Live[0] != "1 close is handed to the agent, those needing a person first." || !strings.HasPrefix(out.Live[1], "Alpha · September 2026: ") || !strings.HasSuffix(out.Live[1], " · handed over by user_a") {
		t.Fatalf("the closes handed to the agent are listed in Work's words: %q", out.Live)
	}
	if out := askFirm("Get Alpha ready for review every month"); len(out.LiveCitations) != 1 || !strings.HasSuffix(out.LiveCitations[0].Href, "?ask="+url.QueryEscape("Get this close ready for review every month")) {
		t.Fatalf("a firm-wide hand-over keeps every month in the close's ask: %+v", out.LiveCitations)
	}
	// A removed member's history goes with their membership.
	call("user_admin", []string{"clients:all", "members:manage"}, "POST", "/v1/closes/members", map[string]any{"subject": "user_a", "granted": false, "previous_id": func() string {
		var id string
		must(db.QueryRow(`SELECT id::text FROM current_organization_members WHERE organization_id=$1 AND subject='user_a'`, org).Scan(&id))
		return id
	}(), "reason": "Left the firm for another role", "confirmed": true}, 200)
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND author='user_a'`, org).Scan(&firmLeft))
	if firmLeft != 0 {
		t.Fatalf("a removed member's conversations are deleted: %d", firmLeft)
	}
	// A message that passed the boundary before the removal is refused when it is saved.
	bare := http.NewServeMux()
	registerChat(bare, db)
	r := httptest.NewRequest("POST", "/v1/closes/chat", strings.NewReader(`{"text":"What needs me today?"}`)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	bare.ServeHTTP(w, r)
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND author='user_a'`, org).Scan(&firmLeft))
	if w.Code != 403 || firmLeft != 0 {
		t.Fatalf("a removed member's message is not saved: %d %d", w.Code, firmLeft)
	}
}

func TestAssistantAnswersNameTheAccountsAskedAbout(t *testing.T) {
	reads := map[string]string{
		"/tie-out": `{"snapshot":{"source":"quickbooks"},"currency":"USD","rows":[{"account":"Prepaid insurance","status":"differs","balance":"1200.00","expected":"1000.00","difference":"200.00"},{"account":"Accrued rent","status":"ties","balance":"500.00","expected":"500.00","difference":"0.00"}]}`,
		"/brief":   `{"headline":"Two things need a person.","lines":[{"area":"ledger","state":"ok","text":"The ledger capture is certified against its TransactionList report."},{"area":"preparation","state":"ok","text":"All documents prepared."}]}`,
		"/flux":    `{"state":"ready","currency":"USD","changes":[{"account":"Rent expense","this_month":"900.00","last_month":"500.00","change":"400.00"},{"account":"Insurance expense","this_month":"100.00","last_month":"100.00","change":"0.00"}]}`,
	}
	read := func(path string, out any) bool {
		raw, ok := reads[path]
		return ok && json.Unmarshal([]byte(raw), out) == nil
	}
	answer := func(intent, text string, previous ...string) string {
		r := closeAnswer(read, "7", "", intent, text, append(previous, "")[0])
		return strings.Join(append([]string{r.body}, r.lines...), "\n")
	}
	if got := answer("tieout", "Which balances don't tie?"); !strings.Contains(got, "Prepaid insurance: differs; Trial Balance 1200.00 USD, supported 1000.00, difference 200.00") || strings.Contains(got, "Accrued rent") {
		t.Fatalf("without a named account only accounts that don't tie are listed: %s", got)
	}
	if got := answer("tieout", "Does accrued rent tie?"); !strings.Contains(got, "Accrued rent: ties") || strings.Contains(got, "Prepaid insurance") {
		t.Fatalf("a named account is answered even when it ties: %s", got)
	}
	if got := answer("ledger", "Is the ledger certified?"); got != "The ledger capture is certified against its TransactionList report." {
		t.Fatalf("the ledger question is the brief's ledger line: %s", got)
	}
	if got := answer("changes", "What changed in insurance expense?"); !strings.Contains(got, "Insurance expense: 0.00") || strings.Contains(got, "Rent expense") {
		t.Fatalf("a named account's change is answered: %s", got)
	}
	if got := answer("changes", "And how does that compare?", "What changed in insurance expense?"); !strings.Contains(got, "Insurance expense") || strings.Contains(got, "Rent expense") {
		t.Fatalf("a follow-up keeps the account of the previous question of the same kind: %s", got)
	}
	if got := answer("changes", "What changed in utilities?", "What changed in insurance expense?"); !strings.Contains(got, "Rent expense") {
		t.Fatalf("a question that doesn't refer back never inherits the previous account: %s", got)
	}
	if got := answer("tieout", "Does that tie?", "What changed in accrued rent?"); strings.Contains(got, "Accrued rent") {
		t.Fatalf("an account named in a different kind of question is not carried over: %s", got)
	}
	if chatQuery("Find the Zurich invoice?") != "Zurich invoice" || chatQuery("Where is the lease") != "lease" || chatQuery("What changed?") != "" || chatIntent("Search for rent", false, false) != "find" {
		t.Fatal("a find question names what to look for")
	}
	if got := answer("blockers", "What blocks sign-off?"); !strings.Contains(got, "couldn't read sign-off readiness") {
		t.Fatalf("a failed read says so: %s", got)
	}
}

func TestChatActionIsOnlyAnAskForCloseWork(t *testing.T) {
	for text, want := range map[string]string{"Prepare this close": "prepare", "please prepare.": "prepare", "Re-check failed work": "recheck", "Stop this close's unresolved work": "stop", "Prepare me for sign-off": "", "Stop showing that": "", "What stops sign-off?": "", "preparing": ""} {
		if got := chatAction(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

func TestAssistantCardsRunTheClosePagesOwnRoutes(t *testing.T) {
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
	org := "org_Card" + rand.Text()
	var alpha, source, job string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Card fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	tx, err := db.BeginTx(context.Background(), nil)
	must(err)
	must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'insurance.pdf','card-invoice','Invoice amount: 1200.00','fixture') RETURNING id::text`, alpha).Scan(&source))
	job, err = preparation.StartIntake(context.Background(), tx, alpha, source, "fixture")
	must(err)
	must(tx.Commit())
	store := savedCloses(db, strings.Repeat("a", 32))
	type message struct {
		ID        string `json:"id"`
		Role      string `json:"role"`
		Action    string `json:"action"`
		Citations []struct {
			Href string `json:"href"`
		} `json:"citations"`
		Outcome *struct {
			RunID string `json:"run_id"`
			Jobs  []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"jobs"`
		} `json:"outcome"`
	}
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
	chat := "/v1/closes/" + alpha + "/chat"
	card := func(text string) message {
		t.Helper()
		var out struct{ Messages []message }
		must(json.Unmarshal(call("user_a", nil, "POST", chat, map[string]string{"text": text}, 200), &out))
		return out.Messages[len(out.Messages)-1]
	}
	var status struct {
		InventoryDigest string `json:"inventory_digest"`
		Stop            struct {
			Digest string `json:"digest"`
			Jobs   []struct {
				Name string `json:"name"`
			} `json:"jobs"`
		} `json:"stop"`
	}
	must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/"+alpha+"/prepare-close", nil, 200), &status))
	prepare := card("Prepare this close")
	if prepare.Action != "prepare" || prepare.Outcome != nil {
		t.Fatalf("a prepare request answers with an unconfirmed card: %+v", prepare)
	}
	confirm := chat + "/" + prepare.ID + "/confirm"
	call("user_a", reviewer, "POST", confirm, map[string]string{"digest": "stale"}, 409)
	call("user_a", nil, "POST", confirm, map[string]string{"digest": status.InventoryDigest}, 403)
	call("user_admin", []string{"clients:all", "workpapers:review"}, "POST", confirm, map[string]string{"digest": status.InventoryDigest}, 409)
	var runs int
	must(db.QueryRow(`SELECT count(*) FROM close_preparations WHERE close_id::text=$1`, alpha).Scan(&runs))
	if runs != 0 {
		t.Fatal("a stale, unpermitted or foreign confirmation started nothing")
	}
	call("user_a", reviewer, "POST", confirm, map[string]string{"digest": status.InventoryDigest}, 200)
	call("user_a", reviewer, "POST", confirm, map[string]string{"digest": status.InventoryDigest}, 409)
	var reopened struct{ Messages []message }
	must(json.Unmarshal(call("user_a", nil, "GET", chat, nil, 200), &reopened))
	var run, actor string
	must(db.QueryRow(`SELECT id::text,actor FROM close_preparations WHERE close_id::text=$1`, alpha).Scan(&run, &actor))
	got := reopened.Messages[len(reopened.Messages)-1]
	if got.ID != prepare.ID || got.Outcome == nil || got.Outcome.RunID != run || len(got.Outcome.Jobs) != 1 || got.Outcome.Jobs[0].ID != job || got.Outcome.Jobs[0].Name != "insurance.pdf" || actor != "user_a" {
		t.Fatalf("the reopened card keeps the run its confirmation started, by its author: %+v %s %s", got, run, actor)
	}
	var tracked struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/"+alpha+"/work?view=all&jobs="+job, nil, 200), &tracked))
	if len(tracked.Items) != 1 || tracked.Items[0].ID != job {
		t.Fatalf("Work reads the card's own jobs: %+v", tracked)
	}
	must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/"+alpha+"/work?view=all&jobs=0", nil, 200), &tracked))
	if len(tracked.Items) != 0 {
		t.Fatalf("Work reads only the named jobs: %+v", tracked)
	}
	again := card("Prepare this close")
	call("user_a", reviewer, "POST", chat+"/"+again.ID+"/confirm", map[string]string{"digest": status.InventoryDigest}, 409)
	stop := card("Stop this close's unresolved work")
	must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/"+alpha+"/prepare-close", nil, 200), &status))
	if stop.Action != "stop" || len(status.Stop.Jobs) != 1 || status.Stop.Jobs[0].Name != "insurance.pdf" {
		t.Fatalf("the stop card lists the named jobs it would stop: %+v %+v", stop, status.Stop)
	}
	call("user_a", reviewer, "POST", chat+"/"+stop.ID+"/confirm", map[string]string{"digest": status.Stop.Digest}, 422)
	var stopped struct{ Messages []message }
	must(json.Unmarshal(call("user_a", reviewer, "POST", chat+"/"+stop.ID+"/confirm", map[string]string{"digest": status.Stop.Digest, "reason": "Client withdrew the invoice"}, 200), &stopped))
	var state string
	must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id::text=$1`, job).Scan(&state))
	if last := stopped.Messages[len(stopped.Messages)-1]; state != "cancelled" || last.Outcome == nil || last.Outcome.RunID != "" || len(last.Outcome.Jobs) != 1 {
		t.Fatalf("the stop card stops the listed job and keeps it: %s %+v", state, last)
	}
	var firm struct{ Messages []message }
	must(json.Unmarshal(call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": "Prepare everything"}, 200), &firm))
	if firm.Messages[len(firm.Messages)-1].Action != "" || len(firm.Messages[len(firm.Messages)-1].Citations) != 1 || firm.Messages[len(firm.Messages)-1].Citations[0].Href != "/dashboard?goal=Prepare+everything" {
		t.Fatalf("a firm-wide conversation has no close to act on; it opens the goal in Work: %+v", firm.Messages[len(firm.Messages)-1])
	}
	must(json.Unmarshal(call("user_a", nil, "POST", "/v1/closes/chat", map[string]string{"text": "Prepare me for sign-off"}, 200), &firm))
	if len(firm.Messages[len(firm.Messages)-1].Citations) != 0 {
		t.Fatalf("a phrase that is not a goal gets the firm-wide help, not a goal link: %+v", firm.Messages[len(firm.Messages)-1])
	}
}

func TestFirmBlockedAnswerCountsEveryBlockedItem(t *testing.T) {
	// Work lists at most 100 items, so the answer takes the number from its counts.
	read := func(path string, out any) bool {
		items := strings.Repeat(`{"entity":"Acme","period":"2026-09","source_name":"invoice.pdf"},`, 100)
		return json.Unmarshal([]byte(`{"items":[`+strings.TrimSuffix(items, ",")+`],"counts":{"blocked":140}}`), out) == nil
	}
	if _, lines, _ := firmAnswer(read, "blocked", "What's blocked?"); len(lines) != 6 || lines[0] != "140 items are blocked." {
		t.Fatalf("the blocked answer undercounts: %v", lines)
	}
}

func TestAssistantShowsTheClosePlan(t *testing.T) {
	if chatIntent("What's the plan?", true, false) != "plan" || chatIntent("Explain the difference on cash", true, false) == "plan" {
		t.Fatal("only the word plan asks for the plan")
	}
	goal := `{"state":"active"}`
	read := func(path string, out any) bool {
		return path == "/goal?verify=1" && json.Unmarshal([]byte(`{"goal":`+goal+`,"progress":{"plan":[{"key":"books","title":"Read the books","state":"done","text":"Read from QuickBooks."},{"key":"documents","title":"Prepare every document","state":"you","text":"1 of 2 prepared, 1 need you.","lines":["Review 1 held email reply on the Requests tab."]}]}}`), out) == nil
	}
	if reply := closeAnswer(read, "7", "", "plan", "What's the plan?", ""); !strings.HasPrefix(reply.body, "Here's my plan") || len(reply.lines) != 3 || reply.lines[1] != "2. Prepare every document (needs you): 1 of 2 prepared, 1 need you." || reply.lines[2] != "   • Review 1 held email reply on the Requests tab." {
		t.Fatalf("the plan is listed with who each step is with, and why: %+v", reply)
	}
	goal = "null"
	if reply := closeAnswer(read, "7", "", "plan", "What's the plan?", ""); !strings.Contains(reply.body, "isn't handed to me") || len(reply.lines) != 3 {
		t.Fatalf("a close not handed over gets the plan it would follow: %+v", reply)
	}
}

func TestFirmReportsAndMonthlyRequests(t *testing.T) {
	for text, want := range map[string]bool{"Get this close ready for review every month": true, "Hand it over each month": true, "Get it signed off monthly": true, "Get this close ready for review": false} {
		if _, _, extra, ok := chatDelegate(text, time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC), "Acme", "2026-09"); !ok || len(extra) > 0 || chatMonthly(text, "Acme") != want {
			t.Errorf("%q: %v %v %v", text, ok, extra, chatMonthly(text, "Acme"))
		}
	}
	// A client's name is never a schedule.
	if chatMonthly("Get Monthly Analytics ready for review", "Monthly Analytics") || !chatMonthly("Get Monthly Analytics ready for review every month", "Monthly Analytics") || !chatMonthly("Get Monthly Analytics ready for review monthly", "Monthly Analytics") {
		t.Fatal("a client named Monthly is handed over once unless every month is asked for")
	}
	if _, _, extra, _ := chatDelegate("Get this close ready for review every week", time.Now(), "Acme", "2026-09"); strings.Join(extra, " ") != "every week" {
		t.Fatalf("another schedule is still an instruction the agent does not act on: %v", extra)
	}
	for text, want := range map[string]string{"Show me delegated closes": "delegated", "What is the agent working on?": "delegated", "How are my delegated closes doing?": "delegated", "Find agent emails": "find", "What's blocked?": "blocked",
		"Which delegated closes are due this week and still waiting on the client?": "", "Which delegated closes are blocked?": ""} {
		if got := chatIntent(text, false, false); got != want {
			t.Errorf("%q routes to %q", text, got)
		}
	}
	// A capped listing says so and links ten closes, then Work.
	items := []portfolioItem{}
	for i := range 12 {
		items = append(items, portfolioItem{CloseID: strconv.Itoa(i), Label: "Client " + strconv.Itoa(i) + " · 2026-09", Result: "no due date"})
	}
	_, lines, links := portfolioListing(portfolioOperation{Scope: "Closes you can access that are not signed off", HasMore: true, Items: items}, chatCitation{"Work", "/dashboard"})
	if lines[0] != "More than 100 closes match: closes you can access that are not signed off." || len(lines) != 12 || lines[11] != "The rest are in Work." || len(links) != 11 || links[10].Href != "/dashboard" {
		t.Fatalf("a capped listing: %q %v", lines, links)
	}
	if got := delegatedWords("review", "active", 2, false, "", "2026-09-27T00:00:00Z"); got != "2 things need a person" {
		t.Fatalf("Work's words: %q", got)
	}
	// Progress more than ten minutes old says when it was found.
	read := func(path string, out any) bool {
		return path == "/goals" && json.Unmarshal([]byte(`{"goals":[{"close_id":"7","entity":"Acme","period":"2026-09","finish":"review","state":"active","author":"user_a","summary":{"needs":1,"as_of":"2026-09-27T10:00:00Z"}}]}`), out) == nil
	}
	if _, lines, _ := firmAnswer(read, "delegated", "How are my delegated closes doing?"); len(lines) != 2 || lines[1] != "Acme · September 2026: 1 thing needs a person (as of Sep 27, 10:00 UTC) · handed over by user_a" {
		t.Fatalf("stale progress says when it was found: %q", lines)
	}
}
