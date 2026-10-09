package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// A member's chats are sessions: the first question titles a new one, a session continues while it is less than 12
// hours quiet, "new" starts another, each is read, renamed and deleted on its own, the model sees one session's
// turns only, a close's chat is listed with its close, and another member sees none of them.
func TestAgentChatSessionsAreManyAndKeptApart(t *testing.T) {
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
	org := "org_AgentSessions" + rand.Text()
	var alpha string
	_, err = db.Exec(`INSERT INTO organizations(id,name,agent_chat) VALUES($1,'Agent sessions fixture','off')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_sessions WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(who, method, path string, body any, want int) []byte {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = strings.NewReader(string(raw))
		}
		r := httptest.NewRequest(method, path, reader).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: who}, Organization: org, Permissions: []string{"workpapers:review"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s %v: %d %s", who, method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type reply struct {
		Messages []struct {
			Body string `json:"body"`
		} `json:"messages"`
		Session *struct {
			ID      string `json:"id"`
			Title   string `json:"title"`
			CloseID string `json:"close_id"`
			Close   string `json:"close"`
		} `json:"session"`
	}
	parse := func(raw []byte) reply {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		var out reply
		must(json.Unmarshal([]byte(lines[len(lines)-1]), &out))
		return out
	}
	type listed struct {
		Sessions []struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			CloseID  string `json:"close_id"`
			Close    string `json:"close"`
			Messages int    `json:"messages"`
		} `json:"sessions"`
	}
	list := func(who string) listed {
		t.Helper()
		var out listed
		must(json.Unmarshal(call(who, "GET", "/v1/closes/chat/sessions", nil, 200), &out))
		return out
	}
	// The first question opens a chat and names it.
	first := parse(call("user_a", "POST", "/v1/closes/chat", map[string]any{"text": "What needs me today?"}, 200))
	if first.Session == nil || first.Session.Title != "What needs me today?" || len(first.Messages) != 2 {
		t.Fatalf("the first question titles the chat: %+v", first)
	}
	s1 := first.Session.ID
	if again := parse(call("user_a", "GET", "/v1/closes/chat", nil, 200)); again.Session == nil || again.Session.ID != s1 {
		t.Fatalf("the current chat is the one just used: %+v", again)
	}
	// "new" starts another; each is read on its own.
	second := parse(call("user_a", "POST", "/v1/closes/chat", map[string]any{"text": "Is anything blocked?", "session_id": "new"}, 200))
	if second.Session == nil || second.Session.ID == s1 || len(second.Messages) != 2 || second.Messages[0].Body != "Is anything blocked?" {
		t.Fatalf("a new chat holds only its own turns: %+v", second)
	}
	s2 := second.Session.ID
	if old := parse(call("user_a", "GET", "/v1/closes/chat?session="+s1, nil, 200)); old.Session.ID != s1 || len(old.Messages) != 2 || old.Messages[0].Body != "What needs me today?" {
		t.Fatalf("an earlier chat is read by its id: %+v", old)
	}
	if l := list("user_a"); len(l.Sessions) != 2 || l.Sessions[0].ID != s2 || l.Sessions[1].ID != s1 || l.Sessions[0].Messages != 2 {
		t.Fatalf("chats list newest first with their counts: %+v", l)
	}
	if l := list("user_b"); len(l.Sessions) != 0 {
		t.Fatalf("another member sees none of them: %+v", l)
	}
	// Rename, a chat that is not the member's, and delete.
	call("user_a", "POST", "/v1/closes/chat/sessions/"+s2, map[string]any{"action": "rename", "title": "  Blocked   work "}, 200)
	if l := list("user_a"); l.Sessions[0].Title != "Blocked work" {
		t.Fatalf("the chat is renamed: %+v", l)
	}
	call("user_a", "POST", "/v1/closes/chat", map[string]any{"text": "x", "session_id": "999999999"}, 404)
	call("user_b", "POST", "/v1/closes/chat/sessions/"+s2, map[string]any{"action": "delete"}, 404)
	call("user_a", "POST", "/v1/closes/chat/sessions/"+s2, map[string]any{"action": "delete"}, 200)
	if l := list("user_a"); len(l.Sessions) != 1 || l.Sessions[0].ID != s1 {
		t.Fatalf("a deleted chat is gone with its messages: %+v", l)
	}
	var left int
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1`, org).Scan(&left))
	if left != 2 {
		t.Fatalf("the deleted chat's messages went with it: %d", left)
	}
	// After 12 quiet hours the next question starts a new chat; the old one stays listed.
	_, err = db.Exec(`UPDATE chat_sessions SET last_at=now()-interval '13 hours' WHERE id=$1::bigint`, s1)
	must(err)
	if fresh := parse(call("user_a", "GET", "/v1/closes/chat", nil, 200)); fresh.Session != nil || len(fresh.Messages) != 0 {
		t.Fatalf("a quiet chat is not continued: %+v", fresh)
	}
	third := parse(call("user_a", "POST", "/v1/closes/chat", map[string]any{"text": "Fresh question"}, 200))
	if third.Session == nil || third.Session.ID == s1 || third.Session.Title != "Fresh question" {
		t.Fatalf("a question after the quiet window opens a new chat: %+v", third)
	}
	// A close's chat is its own and lists with its close; clearing deletes the chat.
	inClose := parse(call("user_a", "POST", "/v1/closes/"+alpha+"/chat", map[string]any{"text": "Where does this close stand?"}, 200))
	if inClose.Session == nil || inClose.Session.CloseID != alpha || inClose.Session.Close != "Alpha · 2026-09" {
		t.Fatalf("a close's chat names its close: %+v", inClose)
	}
	if l := list("user_a"); len(l.Sessions) != 3 || l.Sessions[0].Close != "Alpha · 2026-09" {
		t.Fatalf("the close's chat is listed first with its label: %+v", l)
	}
	// The model is replayed one chat's turns (a close's pairs replay; a firm-wide pair without lineage does not), and a
	// fresh chat none.
	if turns, err := agentHistory(t.Context(), db, org, "user_a", alpha, false, inClose.Session.ID); err != nil || len(turns) != 2 || !strings.Contains(turns[0].Content, "Where does this close stand") {
		t.Fatalf("history is the chat's own: %v %v", turns, err)
	}
	if turns, _ := agentHistory(t.Context(), db, org, "user_a", alpha, false, ""); len(turns) != 0 {
		t.Fatalf("a fresh chat replays nothing: %v", turns)
	}
	if cleared := parse(call("user_a", "POST", "/v1/closes/chat", map[string]any{"action": "clear", "session_id": third.Session.ID}, 200)); cleared.Session != nil {
		t.Fatalf("clearing deletes the chat and starts fresh: %+v", cleared)
	}
	if l := list("user_a"); len(l.Sessions) != 2 {
		t.Fatalf("the cleared chat is gone: %+v", l)
	}
	if long := chatTitle(strings.Repeat("word ", 30)); len(long) > 84 || !strings.HasSuffix(long, "…") {
		t.Fatalf("a long first question is cut at a word: %q", long)
	}
}
