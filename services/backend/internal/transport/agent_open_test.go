package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// Firm-wide, the agent may propose opening a client's month. The card names a client and a month, not a close; it is
// refused for a client the member is not assigned to; confirming it opens the close and, in the same transaction,
// makes it the card's close and part of the pair's lineage; confirming again is refused; and the same month proposed
// again returns the open close rather than a second one.
func TestAgentChatOpensAClientMonthFromTheDashboard(t *testing.T) {
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
	org := "org_AgentOpen" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name,agent_chat) VALUES($1,'Agent open fixture','firm')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	var outputsSeen []string
	seenCalls := map[string]bool{}
	var firstRound string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
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
			if firstRound != "" {
				body, firstRound = firstRound, ""
			} else {
				body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Done."}]}]}`
			}
		} else {
			answer, _ := json.Marshal(map[string]any{"kind": "card", "answer": "Here is the card to open the month; nothing opens until you confirm it.", "support": []map[string]any{}})
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
		r := httptest.NewRequest(method, path, reader).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type message struct {
		ID          string         `json:"id"`
		Role        string         `json:"role"`
		Body        string         `json:"body"`
		Action      string         `json:"action"`
		TargetClose string         `json:"target_close_id"`
		TargetLabel string         `json:"target_close"`
		Proposal    map[string]any `json:"proposal"`
		Outcome     map[string]any `json:"outcome"`
	}
	type reply struct {
		Messages []message `json:"messages"`
	}
	ask := func(text string) message {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(string(call("POST", "/v1/closes/chat", map[string]string{"text": text}, 200))), "\n")
		var out reply
		must(json.Unmarshal([]byte(lines[len(lines)-1]), &out))
		return out.Messages[len(out.Messages)-1]
	}
	// Beta is not the member's client: refused. Alpha's October is proposed, and no close exists yet.
	firstRound = `{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"c1","name":"open_close","arguments":"{\"entity\":\"Beta\",\"period\":\"2026-10\"}"},{"type":"function_call","id":"fc_2","call_id":"c2","name":"open_close","arguments":"{\"entity\":\" Alpha \",\"period\":\"2026-10\"}"}]}`
	card := ask("Open October for Alpha.")
	if len(outputsSeen) != 2 || !strings.Contains(outputsSeen[0], "not a client you are assigned to") || !strings.Contains(outputsSeen[1], `"card":"open_close"`) {
		t.Fatalf("the unassigned client was refused and the month was proposed: %v", outputsSeen)
	}
	args, _ := card.Proposal["args"].(map[string]any)
	if card.Action != "open_close" || card.TargetClose != "" || args["entity"] != "Alpha" || args["period"] != "2026-10" {
		t.Fatalf("the card names the client and month and no close: %+v", card)
	}
	var closes int
	must(db.QueryRow(`SELECT count(*) FROM client_closes WHERE organization_id=$1 AND period='2026-10'`, org).Scan(&closes))
	if closes != 0 {
		t.Fatal("proposing opened nothing")
	}
	// Confirming opens the close; the card and its question record it, in one transaction.
	var confirmed reply
	must(json.Unmarshal(call("POST", "/v1/closes/chat/confirm/"+card.ID, map[string]any{}, 200), &confirmed))
	got := confirmed.Messages[len(confirmed.Messages)-1]
	var opened string
	must(db.QueryRow(`SELECT id::text FROM client_closes WHERE organization_id=$1 AND entity='Alpha' AND period='2026-10'`, org).Scan(&opened))
	if got.Outcome == nil || got.Outcome["close_id"] != opened || got.Outcome["created"] != true || got.TargetClose != opened || got.TargetLabel != "Alpha · 2026-10" {
		t.Fatalf("the card records the close it opened and now names it: %+v (close %s)", got, opened)
	}
	var lineage []string
	rows, err := db.Query(`SELECT array_to_json(closes)::text FROM chat_messages WHERE organization_id=$1 ORDER BY id`, org)
	must(err)
	for rows.Next() {
		var l string
		must(rows.Scan(&l))
		lineage = append(lineage, l)
	}
	rows.Close()
	if len(lineage) != 2 || lineage[0] != "["+opened+"]" || lineage[1] != "["+opened+"]" {
		t.Fatalf("both messages of the pair carry the opened close: %v", lineage)
	}
	call("POST", "/v1/closes/chat/confirm/"+card.ID, map[string]any{}, 409)
	// The same month proposed again returns the open close, created false.
	firstRound = `{"status":"completed","output":[{"type":"function_call","id":"fc_3","call_id":"c3","name":"open_close","arguments":"{\"entity\":\"alpha\",\"period\":\"2026-10\"}"}]}`
	again := ask("Open October for Alpha again.")
	must(json.Unmarshal(call("POST", "/v1/closes/chat/confirm/"+again.ID, map[string]any{}, 200), &confirmed))
	got = confirmed.Messages[len(confirmed.Messages)-1]
	if got.Outcome["close_id"] != opened || got.Outcome["created"] != false {
		t.Fatalf("the open month is returned, not opened twice: %+v", got.Outcome)
	}
	must(db.QueryRow(`SELECT count(*) FROM client_closes WHERE organization_id=$1 AND period='2026-10'`, org).Scan(&closes))
	if closes != 1 {
		t.Fatalf("one October close: %d", closes)
	}
}
