package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
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
		for _, q := range []string{`DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	type message struct {
		Role      string `json:"role"`
		Body      string `json:"body"`
		Citations []struct {
			Label string `json:"label"`
			Href  string `json:"href"`
		} `json:"citations"`
	}
	type conversation struct {
		Messages []message `json:"messages"`
		Live     []string  `json:"live"`
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
			rows, _ := db.Query(`SELECT id,role,left(body,20),created_at FROM chat_messages WHERE organization_id=$1 AND close_id::text=$2 ORDER BY id DESC LIMIT 4`, org, alpha)
			for rows.Next() {
				var id, role, body, at string
				rows.Scan(&id, &role, &body, &at)
				t.Log(id, role, body, at)
			}
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
	if history := call("user_a", nil, "GET", closeChat, nil, 200); len(history.Messages) != 16 {
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
	answer := func(intent, text string) string {
		r := closeAnswer(read, "7", "", intent, text)
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
	if got := answer("blockers", "What blocks sign-off?"); !strings.Contains(got, "couldn't read sign-off readiness") {
		t.Fatalf("a failed read says so: %s", got)
	}
}
