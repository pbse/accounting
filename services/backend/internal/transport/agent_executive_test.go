package transport

import (
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

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// An executive asks firm-wide and gets exactly their reads (overview, published reports, cash plan, budget): the firm's
// reads and every card are refused, links lead to the executive's own pages, the pair records the close read, commands
// are not theirs, a close conversation is refused, and with the firm's agent off they get one fixed line and no model.
func TestAgentChatAnswersAnExecutive(t *testing.T) {
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
	org := "org_AgentExec" + rand.Text()
	var alpha string
	_, err = db.Exec(`INSERT INTO organizations(id,name,agent_chat) VALUES($1,'Agent executive fixture','closes')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_x',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_x','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	// A firm-work pair the same person had as a member: never shown to them as an executive, never replayed.
	_, err = db.Exec(`INSERT INTO chat_messages(organization_id,author,role,body,citations,closes,replayable) VALUES($1,'user_x','user','What is in the unpublished draft?','[]','{}',true),($1,'user_x','assistant','The unpublished draft secret.','[]','{}',true)`, org)
	must(err)
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	calls := 0
	var toolsOffered, outputsSeen []string
	seenCalls := map[string]bool{}
	var firstRound string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "unpublished draft") {
			t.Fatalf("a member's firm-work turn reached the model for the executive: %s", raw)
		}
		var sent struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Input []map[string]any `json:"input"`
		}
		json.Unmarshal(raw, &sent)
		for _, item := range sent.Input {
			if item["type"] == "function_call_output" {
				if id, _ := item["call_id"].(string); !seenCalls[id] {
					seenCalls[id] = true
					outputsSeen = append(outputsSeen, item["output"].(string))
				}
			}
		}
		var body string
		if len(sent.Tools) > 0 {
			toolsOffered = nil
			for _, tool := range sent.Tools {
				toolsOffered = append(toolsOffered, tool.Name)
			}
			if firstRound != "" {
				body, firstRound = firstRound, ""
			} else {
				body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Done."}]}]}`
			}
		} else {
			answer, _ := json.Marshal(map[string]any{"kind": "facts", "answer": "Alpha has a September close.", "support": []map[string]any{{"step": "t1", "sentence": 0, "path": "/closes/0/entity", "value": "Alpha", "quote": ""}}})
			body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + strconvQuote(string(answer)) + `}]}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})
	store := modelHandler(t, savedCloses(db, strings.Repeat("a", 32)))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		var reader io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			reader = strings.NewReader(string(raw))
		}
		r := httptest.NewRequest(method, path, reader).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_x"}, Organization: org, Permissions: []string{"executive:read"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type message struct {
		Body      string              `json:"body"`
		Action    string              `json:"action"`
		Citations []map[string]string `json:"citations"`
	}
	ask := func(text string) message {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(string(call("POST", "/v1/closes/chat", map[string]string{"text": text}, 200))), "\n")
		var out struct {
			Messages []message `json:"messages"`
		}
		must(json.Unmarshal([]byte(lines[len(lines)-1]), &out))
		return out.Messages[len(out.Messages)-1]
	}
	// The overview is read; the firm's listing and a card are not the executive's tools.
	firstRound = `{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"c1","name":"executive_overview","arguments":"{}"},{"type":"function_call","id":"fc_2","call_id":"c2","name":"list_closes","arguments":"{}"},{"type":"function_call","id":"fc_3","call_id":"c3","name":"prepare_close","arguments":"{\"close_id\":\"` + alpha + `\",\"inventory_digest\":\"x\",\"confirmed\":true}"}]}`
	last := ask("How is my business doing?")
	offered := slices.Clone(toolsOffered)
	slices.Sort(offered)
	if want := []string{"budget_comparison", "cash_plan", "executive_overview", "list_publications", "read_publication"}; !slices.Equal(offered, want) {
		t.Fatalf("an executive is offered their reads and nothing else: %v", toolsOffered)
	}
	if len(outputsSeen) != 3 || !strings.HasPrefix(outputsSeen[0], "t1: {") || !strings.Contains(outputsSeen[1], "not a tool this agent offers here") || !strings.Contains(outputsSeen[2], "not a tool this agent offers here") {
		t.Fatalf("the overview ran and the firm's tools were refused: %v", outputsSeen)
	}
	if !strings.HasPrefix(last.Body, "Alpha has") || last.Action != "" || len(last.Citations) != 1 || last.Citations[0]["href"] != "/dashboard/executive" {
		t.Fatalf("the answer is kept, proposes nothing and links the executive's own page: %+v", last)
	}
	var closes string
	must(db.QueryRow(`SELECT array_to_json(closes)::text FROM chat_messages WHERE organization_id=$1 AND role='user' ORDER BY id DESC LIMIT 1`, org).Scan(&closes))
	if closes != "["+alpha+"]" {
		t.Fatalf("the pair records the close the overview listed: %s", closes)
	}
	if body := string(call("POST", "/v1/closes/chat", map[string]string{"command": "status"}, 422)); !strings.Contains(body, "no commands here") {
		t.Fatalf("a command is not an executive's: %s", body)
	}
	call("POST", "/v1/closes/"+alpha+"/chat", map[string]string{"text": "Where does this close stand?"}, 403)
	if shown := string(call("GET", "/v1/closes/chat", nil, 200)); strings.Contains(shown, "unpublished draft") || !strings.Contains(shown, "Alpha has") {
		t.Fatalf("an executive sees their own conversation and not the member's: %s", shown)
	}
	var kept int
	must(db.QueryRow(`SELECT count(*) FROM chat_messages WHERE organization_id=$1 AND NOT executive`, org).Scan(&kept))
	if kept != 2 {
		t.Fatalf("the member's pair is kept apart, untouched: %d", kept)
	}
	// With the firm's agent off, one fixed line and no model call.
	_, err = db.Exec(`UPDATE organizations SET agent_chat='off' WHERE id=$1`, org)
	must(err)
	calls = 0
	last = ask("What does my latest report say?")
	if calls != 0 || last.Body != executiveOff || len(last.Citations) != 1 || last.Citations[0]["href"] != "/dashboard/executive" {
		t.Fatalf("at off an executive gets the fixed line without a model: calls=%d %+v", calls, last)
	}
}
