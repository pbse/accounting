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

// The support gate: every cited step ran, every pointer or quote resolves to its value, every figure and every
// sentence of a facts answer is bound, and a help answer states no fact.
func TestAgentSupportBindsEveryFigureAndSentence(t *testing.T) {
	brief := `{"headline":"2 things need you: capture a month-end snapshot, add client documents.","lines":["ledger: no capture"]}`
	tie := `{"state":"ready","rows":[{"account":"Cash","ledger_balance":"-1,250.00","status":"differs"},{"account":"AR","ledger_balance":"8000.00","status":"ties"}]}`
	decode := func(raw string) any {
		var v any
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		d.Decode(&v)
		return v
	}
	reads := map[string]agentRead{
		"t1": {text: brief, value: decode(brief), link: chatCitation{"Close overview", "/dashboard/closes/7"}, title: "Where a close stands"},
		"t2": {text: tie, value: decode(tie), link: chatCitation{"Balance sheet tie-out", "/dashboard/closes/7"}, title: "Balance sheet tie-out"},
		"t3": {text: "Acme Insurance Co\nAnnual premium USD 1,200.00 for coverage 2026-09-01 to 2027-08-31", doc: true, link: chatCitation{"Evidence #4", "/dashboard/closes/7?tab=evidence"}, title: "Read evidence text"},
	}
	ok := func(kind, text string, support []agentSupportEntry) []chatCitation {
		t.Helper()
		cited, why := agentSupport(kind, text, support, reads, []string{"Alpha"})
		if why != "" {
			t.Fatalf("%q rejected: %s", text, why)
		}
		return cited
	}
	rejected := func(kind, text string, support []agentSupportEntry, reason string) {
		t.Helper()
		if _, why := agentSupport(kind, text, support, reads, []string{"Alpha"}); !strings.Contains(why, reason) {
			t.Fatalf("%q was not rejected for %q: %q", text, reason, why)
		}
	}
	cited := ok("facts", "Cash differs by (1,250.00). The premium is USD 1,200.00 a year.", []agentSupportEntry{{"t2", 0, "/rows/0/ledger_balance", "-1250", ""}, {"t3", 1, "", "", "Annual premium USD 1,200.00"}})
	if len(cited) != 2 || cited[0].Label != "Balance sheet tie-out › rows/0/ledger_balance" || cited[1].Href != "/dashboard/closes/7?tab=evidence" {
		t.Fatalf("citations name the field and link the page: %v", cited)
	}
	ok("facts", "Two things need you.", []agentSupportEntry{{"t1", 0, "/headline", "2 things need you: capture a month-end snapshot, add client documents.", ""}})
	ok("facts", "2 things need you.", []agentSupportEntry{{"t1", 0, "/headline", "2 things need you: capture a month-end snapshot, add client documents.", ""}})
	rejected("facts", "Cash differs by 1,250.00.", []agentSupportEntry{{"t2", 0, "/rows/0/ledger_balance", "1250", ""}}, "does not hold the cited value")
	rejected("facts", "Cash differs by 1,250.00.", []agentSupportEntry{{"t2", 0, "/rows/0/ledger_balance", "-1250", ""}}, "bound to no read")
	rejected("facts", "AR ties at 8,000.00.", []agentSupportEntry{{"t9", 0, "/rows/1/ledger_balance", "8000", ""}}, "did not run")
	rejected("facts", "AR ties at 8,000.00.", []agentSupportEntry{{"t2", 0, "/rows/1/status", "ties", ""}}, "bound to no read")
	rejected("facts", "AR ties. Cash differs.", []agentSupportEntry{{"t2", 0, "/rows/1/status", "ties", ""}}, "sentence 1 has no support")
	rejected("facts", "The premium is 1,200.00.", []agentSupportEntry{{"t3", 0, "", "", "premium"}}, "not in the read")
	rejected("facts", "AR ties.", nil, "no support")
	rejected("facts", "AR ties at 8,000.00.", []agentSupportEntry{{"t2", 0, "", "", "\"ledger_balance\":\"8000.00\""}}, "not a document")
	rejected("facts", "See https://example.com/report for AR.", []agentSupportEntry{{"t2", 0, "/rows/1/status", "ties", ""}}, "no read supplied")
	ok("help", "I can read this close's status, tie-out and documents, or search it.", nil)
	rejected("help", "I can see this close is ready for sign-off.", nil, "stated a close's state")
	rejected("help", "This close is ready for sign-off.", nil, "stated something about the work")
	rejected("help", "The cash balance ties.", nil, "stated something about the work")
	rejected("card", "See https://example.com for the card.", nil, "no read supplied")
	ok("card", "Here is what preparing this close would coordinate. Confirm the card to start.", nil)
	rejected("facts", "AR ties at 8,000.00. Cash differs by 8,000.00.", []agentSupportEntry{{"t2", 0, "/rows/1/ledger_balance", "8000", ""}, {"t2", 1, "/rows/0/status", "differs", ""}}, "no read in its sentence")
	rejected("help", "I can look at Alpha for you.", nil, "named a client")
	var listing any
	json.Unmarshal([]byte(`{"closes":[{"id":"1","entity":"Gamma Ltd","period":"2026-09","owner":{"name":"Pat"}}],"results":[{"kind":"document","title":"Zurich invoice.pdf","close":"Gamma Ltd · 2026-09"}],"has_more":false}`), &listing)
	if names := collectNames(listing, 0); len(names) != 4 || !slices.Contains(names, "Zurich invoice.pdf") || !slices.Contains(names, "Gamma Ltd") {
		t.Fatalf("names are collected from nested rows: %v", names)
	}
	rejected("refusal", "I don't give tax advice; the balance is 1,250.00 anyway.", nil, "held a figure")
	if figures := agentFigures("Coverage runs 2026-09-01 to 2027-08-31 at 1,200.00."); len(figures) != 3 || figures[0] != "2026-09-01" || figures[1] != "2027-08-31" || figures[2] != "1200" {
		t.Fatalf("a calendar day is one figure: %v", figures)
	}
	ok("facts", "Coverage starts 2026-09-01.", []agentSupportEntry{{"t3", 0, "", "", "coverage 2026-09-01 to"}})
	if figures := agentFigures("September 2026-09 ran at 50% of 50."); len(figures) != 3 || figures[0] != "2026-09" || figures[1] != "50%" || figures[2] != "50" {
		t.Fatalf("a close month is one figure and a percentage keeps its unit: %v", figures)
	}
	if sameValue("50", "50%") || !sameValue("50.0%", "50%") || !sameValue("2026-09", "2026-09") {
		t.Fatal("a percentage is never supported by a plain number")
	}
	if n := normalizeFigure("(1,250.00)"); n != "-1250" {
		t.Fatalf("parentheses are the sign: %s", n)
	}
	if n := normalizeFigure("$8,000"); n != "8000" {
		t.Fatalf("currency marks drop: %s", n)
	}
}

// In a close conversation the agent reads the close through the registry under the member, refuses a tool it does
// not offer, proposes an action as a card that runs nothing, and answers in a separate tool-free call bound to what
// it read; the events, the trace and the record are kept, and the firm's level governs everything.
func TestAgentChatReadsProposesAndAnswersInAClose(t *testing.T) {
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
	org := "org_AgentChat" + rand.Text()
	var closeID, otherID string
	_, err = db.Exec(`INSERT INTO organizations(id,name,agent_chat) VALUES($1,'Agent chat fixture','off')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture'),($1,'user_a','Beta',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&otherID))
	_, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'card.txt','card-digest','Card 4111 1111 1111 1111 on file','fixture')`, closeID)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM assistant_answers WHERE organization_id=$1`, `DELETE FROM chat_messages WHERE organization_id=$1`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organization_policy_changes WHERE organization_id=$1`, `DELETE FROM model_calls WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	calls, toolRounds := 0, 0
	var toolsOffered []string
	var outputsSeen []string
	var finalInput []map[string]any
	headline := ""
	var answer func(headline string) string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var sent struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
			Input []map[string]any `json:"input"`
			Text  json.RawMessage  `json:"text"`
		}
		json.Unmarshal(raw, &sent)
		if strings.Contains(string(raw), "4111 1111 1111 1111") {
			t.Fatalf("a card number reached the model: %s", raw)
		}
		for _, item := range sent.Input {
			if item["type"] == "function_call_output" {
				out := item["output"].(string)
				if !strings.Contains(strings.Join(outputsSeen, "\n"), out) {
					outputsSeen = append(outputsSeen, out)
				}
				if rest, found := strings.CutPrefix(out, "t1: "); found {
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
			toolRounds++
			toolsOffered = nil
			for _, tool := range sent.Tools {
				toolsOffered = append(toolsOffered, tool.Name)
			}
			if toolRounds == 1 {
				body = `{"status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"Reading."}]},{"type":"function_call","id":"fc_1","call_id":"c1","name":"close_brief","arguments":"{\"close_id\":\"` + otherID + `\"}"},{"type":"function_call","id":"fc_2","call_id":"c2","name":"export_close_package","arguments":"{}"},{"type":"function_call","id":"fc_3","call_id":"c3","name":"prepare_close","arguments":"{\"inventory_digest\":\"abc\",\"confirmed\":true}"},{"type":"function_call","id":"fc_4","call_id":"c4","name":"stop_work","arguments":"{\"digest\":\"x\",\"reason\":\"y\"}"}]}`
			} else {
				body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Done reading."}]}]}`
			}
		} else {
			finalInput = sent.Input
			if sent.Text == nil || !strings.Contains(string(sent.Text), "agent_answer") {
				t.Fatalf("the answer call carries the answer schema and no tools: %s", raw)
			}
			body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + strconvQuote(answer(headline)) + `}]}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})
	store := modelHandler(t, savedCloses(db, strings.Repeat("a", 32)))
	call := func(subject string, permissions []string, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, strings.NewReader(string(raw))).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %v: %d %s", path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type message struct {
		ID          string              `json:"id"`
		Role        string              `json:"role"`
		Body        string              `json:"body"`
		Citations   []map[string]string `json:"citations"`
		Action      string              `json:"action"`
		Proposal    map[string]any      `json:"proposal"`
		TargetClose string              `json:"target_close_id"`
		Steps       []agentStep         `json:"steps"`
	}
	ask := func(body map[string]string) (events []map[string]any, last message) {
		t.Helper()
		lines := strings.Split(strings.TrimSpace(string(call("user_a", []string{"workpapers:review"}, "/v1/closes/"+closeID+"/chat", body, 200))), "\n")
		for _, line := range lines {
			var item map[string]any
			must(json.Unmarshal([]byte(line), &item))
			if item["messages"] == nil {
				events = append(events, item)
				continue
			}
			var out struct {
				Messages []message `json:"messages"`
			}
			must(json.Unmarshal([]byte(line), &out))
			last = out.Messages[len(out.Messages)-1]
		}
		return events, last
	}
	// Off: free text is answered by the fixed reads from its words, as before, and never sent; a command runs its read.
	if _, last := ask(map[string]string{"text": "Why is cash lower than last month?"}); calls != 0 || !strings.Contains(last.Body, "Changes from last month") {
		t.Fatalf("with the agent off free text is not sent: calls=%d %q", calls, last.Body)
	}
	if _, last := ask(map[string]string{"text": "hello there"}); calls != 0 || !strings.HasPrefix(last.Body, "I answer from this close") {
		t.Fatalf("with the agent off an unmatched question gets the fixed help: calls=%d %q", calls, last.Body)
	}
	if _, last := ask(map[string]string{"command": "status"}); calls != 0 || len(last.Citations) == 0 || last.Citations[0]["label"] != "Close overview" {
		t.Fatalf("a command runs its fixed read without a model: calls=%d %v", calls, last)
	}
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"agent_chat": "closes", "reason": "Pilot the agent"}, 200)
	answer = func(headline string) string {
		raw, _ := json.Marshal(map[string]any{"kind": "facts", "answer": "Here is where the close stands.", "support": []map[string]any{{"step": "t1", "sentence": 0, "path": "/headline", "value": headline, "quote": ""}}})
		return string(raw)
	}
	calls, toolRounds = 0, 0
	events, last := ask(map[string]string{"text": "Where does this close stand, and prepare it?"})
	if calls != 3 || toolRounds != 2 {
		t.Fatalf("two tool rounds then one answer call: calls=%d rounds=%d", calls, toolRounds)
	}
	for _, name := range []string{"export_close_package", "add_evidence", "list_closes", "search", "select_closes", "open_close", "propose_plan"} {
		if slices_contains(toolsOffered, name) {
			t.Fatalf("a close conversation at the closes level offers no %s: %v", name, toolsOffered)
		}
	}
	for _, name := range []string{"close_brief", "read_evidence_text", "search_close", "prepare_close", "hand_over", "request_candidates", "list_procedures"} {
		if !slices_contains(toolsOffered, name) {
			t.Fatalf("a close conversation offers %s: %v", name, toolsOffered)
		}
	}
	if len(outputsSeen) != 4 || !strings.HasPrefix(outputsSeen[0], "t1: {") || !strings.HasPrefix(outputsSeen[1], "refused: not a tool") || !strings.Contains(outputsSeen[2], `"proposed"`) || !strings.Contains(outputsSeen[3], "one proposal at a time") {
		t.Fatalf("the model is told each result: the pinned read, the refusal, the proposal and the second proposal refused: %v", outputsSeen)
	}
	if !strings.Contains(outputsSeen[0], "need") || strings.Contains(outputsSeen[0], "Beta") {
		t.Fatalf("the close is pinned whatever close_id the model sent: %s", outputsSeen[0])
	}
	kinds := []string{}
	for _, e := range events {
		kinds = append(kinds, e["type"].(string))
	}
	if strings.Join(kinds, " ") != "step step step step step card step step step text" {
		t.Fatalf("events: a running and a finished step per call, the card when proposed, then the text: %v", kinds)
	}
	if last.Action != "prepare" || last.Proposal["action"] != "prepare" || last.TargetClose != "" || len(last.Steps) != 4 || last.Steps[0].Status != "ok" || last.Steps[0].Link.Label != "Where a close stands" || last.Steps[2].Status != "proposed" {
		t.Fatalf("the answer carries the card, its proposal and the trace: %+v", last)
	}
	if !strings.HasPrefix(last.Body, "Here is where the close stands.") || len(last.Citations) != 1 || last.Citations[0]["label"] != "Where a close stands › headline" {
		t.Fatalf("a kept answer cites the field it was bound to: %q %v", last.Body, last.Citations)
	}
	var runs int
	must(db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id::text=$1`, closeID).Scan(&runs))
	if runs != 0 {
		t.Fatal("a proposed action ran without confirmation")
	}
	var outcome, scope string
	var rounds, toolCalls int
	var closes string
	must(db.QueryRow(`SELECT outcome,scope,rounds,tool_calls FROM assistant_answers WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&outcome, &scope, &rounds, &toolCalls))
	must(db.QueryRow(`SELECT array_to_json(closes)::text FROM chat_messages WHERE organization_id=$1 AND role='user' ORDER BY id DESC LIMIT 1`, org).Scan(&closes))
	if outcome != "kept" || scope != "close" || rounds != 2 || toolCalls != 2 || closes != "["+closeID+"]" {
		t.Fatalf("the attempt and the pair's lineage are recorded: %s %s %d %d %s", outcome, scope, rounds, toolCalls, closes)
	}
	// The next message replays the kept pair, redacted, and an unsupported answer is not kept.
	answer = func(string) string {
		raw, _ := json.Marshal(map[string]any{"kind": "facts", "answer": "Cash is 9,999.00.", "support": []map[string]any{{"step": "t1", "sentence": 0, "path": "/headline", "value": "x", "quote": ""}}})
		return string(raw)
	}
	_, last = ask(map[string]string{"text": "And my card 4111 1111 1111 1111 is on file?"})
	replayed := 0
	for _, item := range finalInput {
		if item["role"] == "user" && strings.Contains(fmtAny(item["content"]), "and prepare it") {
			replayed++
		}
		if strings.Contains(fmtAny(item["content"]), "4111 1111 1111 1111") {
			t.Fatalf("a prompt reached the model unredacted: %v", item["content"])
		}
	}
	if replayed != 1 || !strings.HasPrefix(last.Body, "I couldn't support an answer") || last.Action != "" {
		t.Fatalf("the earlier pair is replayed and an unsupported answer is not kept: replayed=%d %q", replayed, last.Body)
	}
	must(db.QueryRow(`SELECT outcome FROM assistant_answers WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&outcome))
	if outcome != "not_kept" {
		t.Fatalf("the attempt is recorded as not kept: %s", outcome)
	}
	// Turning the agent off stops every call at once.
	call("user_admin", []string{"org:manage"}, "/v1/closes/organization/policy", map[string]any{"agent_chat": "off", "reason": "Pause"}, 200)
	spent := calls
	if _, last := ask(map[string]string{"text": "Why is cash lower than last month?"}); calls != spent || !strings.Contains(last.Body, "Changes from last month") {
		t.Fatalf("with the agent off again nothing is sent: %q", last.Body)
	}
}

func slices_contains(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

func fmtAny(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func strconvQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
