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

// Firm-wide, at the firm level: the agent names closes by id, a card it proposes for one close is bound to that close
// and confirmed through the firm-wide route, every pair records the closes its reads touched and is deleted when one
// is lost, a read without lineage is shown once and not kept, a goal cannot be proposed without its preview, and the
// track record counts firm-wide attempts.
func TestAgentChatFirmWideLineageAndCards(t *testing.T) {
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
	org := "org_AgentFirm" + rand.Text()
	var alpha, beta string
	_, err = db.Exec(`INSERT INTO organizations(id,name,agent_chat) VALUES($1,'Agent firm fixture','firm')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture'),($1,'user_a','Beta',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM model_calls WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	var toolsOffered, outputsSeen []string
	seenCalls := map[string]bool{}
	var firstRound string
	headline := ""
	var answer func() string
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
				out := item["output"].(string)
				id, _ := item["call_id"].(string)
				if !seenCalls[id] {
					seenCalls[id] = true
					outputsSeen = append(outputsSeen, out)
				}
				if rest, found := strings.CutPrefix(out, "t4: "); found {
					var brief struct {
						Headline string `json:"headline"`
					}
					json.Unmarshal([]byte(rest), &brief)
					headline = brief.Headline
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
			body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + strconvQuote(answer()) + `}]}]}`
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
		ID          string              `json:"id"`
		Role        string              `json:"role"`
		Body        string              `json:"body"`
		Citations   []map[string]string `json:"citations"`
		Action      string              `json:"action"`
		TargetClose string              `json:"target_close_id"`
		TargetLabel string              `json:"target_close"`
		Outcome     map[string]any      `json:"outcome"`
	}
	type reply struct {
		Messages []message `json:"messages"`
		Live     []string  `json:"live"`
	}
	ask := func(text string) reply {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(string(call("POST", "/v1/closes/chat", map[string]string{"text": text}, 200))), "\n")
		var out reply
		must(json.Unmarshal([]byte(lines[len(lines)-1]), &out))
		return out
	}
	// A hand-over proposed for Beta, after reading it: the card is bound to Beta and the pair records Beta.
	firstRound = `{"status":"completed","output":[{"type":"function_call","id":"fc_9","call_id":"c9","name":"get_close","arguments":"{\"close_id\":\"999999999\"}"},{"type":"function_call","id":"fc_7","call_id":"c7","name":"select_closes_preview","arguments":"{\"text\":\"show my clients due Friday\"}"},{"type":"function_call","id":"fc_8","call_id":"c8","name":"select_closes","arguments":"{\"text\":\"show my clients due Friday\"}"},{"type":"function_call","id":"fc_1","call_id":"c1","name":"close_brief","arguments":"{\"close_id\":\"` + beta + `\"}"},{"type":"function_call","id":"fc_2","call_id":"c2","name":"select_closes","arguments":"{\"text\":\"prepare my clients due Friday\"}"},{"type":"function_call","id":"fc_0","call_id":"c0","name":"hand_over","arguments":"{\"close_id\":\"999999999\",\"finish\":\"review\"}"},{"type":"function_call","id":"fc_3","call_id":"c3","name":"hand_over","arguments":"{\"close_id\":\"` + beta + `\",\"finish\":\"review\"}"}]}`
	answer = func() string {
		raw, _ := json.Marshal(map[string]any{"kind": "facts", "answer": "Beta is where it is.", "support": []map[string]any{{"step": "t4", "sentence": 0, "path": "/headline", "value": headline, "quote": ""}}})
		return string(raw)
	}
	out := ask("How is Beta doing? Hand it over.")
	for _, name := range []string{"list_closes", "search", "select_closes_preview", "select_closes", "hand_over", "close_brief", "morning_brief"} {
		if !slices_contains(toolsOffered, name) {
			t.Fatalf("a firm-wide conversation offers %s: %v", name, toolsOffered)
		}
	}
	if slices_contains(toolsOffered, "search_close") || slices_contains(toolsOffered, "open_close") {
		t.Fatalf("search_close and open_close are not offered firm-wide: %v", toolsOffered)
	}
	if len(outputsSeen) != 7 || !strings.Contains(outputsSeen[0], "not a close you can open") || !strings.HasPrefix(outputsSeen[1], "t2: ") || !strings.Contains(outputsSeen[2], "nothing to act on") || !strings.HasPrefix(outputsSeen[3], "t4: ") || !strings.Contains(outputsSeen[4], "preview this exact goal") || !strings.Contains(outputsSeen[5], "not a close you can open") || !strings.Contains(outputsSeen[6], `"proposed"`) {
		t.Fatalf("the read ran, the goal without a preview and the card on a close the member cannot open were refused, the hand-over was proposed: %v", outputsSeen)
	}
	last := out.Messages[len(out.Messages)-1]
	if last.Action != "delegate" || last.TargetClose != beta || last.TargetLabel != "Beta · 2026-09" || !strings.HasPrefix(last.Body, "Beta is where it is.") {
		t.Fatalf("the card names the close it acts on: %+v", last)
	}
	var closes string
	must(db.QueryRow(`SELECT array_to_json(closes)::text FROM chat_messages WHERE organization_id=$1 AND role='user' ORDER BY id DESC LIMIT 1`, org).Scan(&closes))
	if closes != "["+beta+"]" {
		t.Fatalf("the pair records the close its read touched: %s", closes)
	}
	// The firm-wide confirm route hands Beta over, and only its author's own card.
	var confirmed reply
	must(json.Unmarshal(call("POST", "/v1/closes/chat/confirm/"+last.ID, map[string]any{"finish": "review"}, 200), &confirmed))
	if got := confirmed.Messages[len(confirmed.Messages)-1]; got.Outcome == nil || got.Outcome["goal_id"] == nil {
		t.Fatalf("the hand-over ran on the target close and was recorded: %+v", got)
	}
	var goals int
	must(db.QueryRow(`SELECT count(*) FROM close_goals WHERE close_id::text=$1 AND state<>'stopped'`, beta).Scan(&goals))
	if goals != 1 {
		t.Fatalf("Beta has its goal: %d", goals)
	}
	// A read without lineage: the answer is shown once and not kept, and the pair is never replayed.
	firstRound = `{"status":"completed","output":[{"type":"function_call","id":"fc_4","call_id":"c4","name":"morning_brief","arguments":"{}"}]}`
	answer = func() string {
		raw, _ := json.Marshal(map[string]any{"kind": "help", "answer": "I can list what needs a person today from the morning brief.", "support": []map[string]any{}})
		return string(raw)
	}
	out = ask("What needs a person today?")
	last = out.Messages[len(out.Messages)-1]
	if len(out.Live) != 1 || !strings.HasPrefix(out.Live[0], "I can list") || !strings.Contains(last.Body, "shown once and not kept") {
		t.Fatalf("a lineage-less answer is shown once: live=%v body=%q", out.Live, last.Body)
	}
	must(db.QueryRow(`SELECT array_to_json(closes)::text FROM chat_messages WHERE organization_id=$1 AND role='assistant' ORDER BY id DESC LIMIT 1`, org).Scan(&closes))
	if closes != "[]" {
		t.Fatalf("a lineage-less pair records no closes: %s", closes)
	}
	// Losing Beta deletes the pair that read it, question and answer alike, before the conversation is shown again.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Beta',false,'Moved','fixture')`, org)
	must(err)
	var shown reply
	must(json.Unmarshal(call("GET", "/v1/closes/chat", nil, 200), &shown))
	for _, m := range shown.Messages {
		if strings.Contains(m.Body, "Hand it over") || strings.Contains(m.Body, "Beta is where it is") {
			t.Fatalf("a pair that read a lost close is still shown: %+v", m)
		}
	}
	if len(shown.Messages) != 2 {
		t.Fatalf("only the lineage-less pair remains: %d", len(shown.Messages))
	}
	// The track record counts firm-wide attempts for the firm.
	var record struct {
		Assistant struct {
			FirmAsked int `json:"firm_asked"`
			FirmKept  int `json:"firm_kept"`
		} `json:"assistant"`
	}
	must(json.Unmarshal(call("GET", "/v1/closes/track-record", nil, 200), &record))
	if record.Assistant.FirmAsked != 2 || record.Assistant.FirmKept != 2 {
		t.Fatalf("firm-wide attempts are counted: %+v", record.Assistant)
	}
}
