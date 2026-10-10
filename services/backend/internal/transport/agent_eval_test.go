package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/agenteval"
	"close.local/backend/internal/llm"
)

// A replay answers the loop's reads from the record and scores what the model did: a recorded read the model chose
// supports a facts answer, an off-scope prompt refused without a card passes, and a read the model makes that was not
// recorded is unavailable and counts as a missed tool.
func TestAgentEvalReplaysRecordedReads(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	record := agenteval.Record{Case: "sample", Organization: "org_AgentEval" + rand.Text(), Subject: "user_eval", CloseID: "7", Entity: "Alpha", Period: "2026-09", Today: "2026-10-08"}
	brief := `{"headline":"2 things need you: capture a month-end snapshot, add client documents.","lines":[]}`
	record.Add(agenteval.Read{Tool: "close_brief", Method: "GET", Path: "/v1/closes/7/brief", Output: brief, OK: true})
	if err = EnsureAgentEvalFirm(t.Context(), db, record); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM organizations WHERE id=$1`, record.Organization)
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	client, err := llm.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	var tool, answer string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var sent struct {
			Tools []struct{ Name string } `json:"tools"`
			Input []map[string]any        `json:"input"`
		}
		json.Unmarshal(raw, &sent)
		called := false
		for _, item := range sent.Input {
			if item["type"] == "function_call" {
				called = true
			}
		}
		body := `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":` + strconvQuote(answer) + `}]}]}`
		if len(sent.Tools) > 0 {
			body = `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Done."}]}]}`
			if tool != "" && !called {
				body = `{"status":"completed","output":[{"type":"function_call","id":"fc_1","call_id":"c1","name":"` + tool + `","arguments":"{}"}]}`
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})
	set := agenteval.Set{Questions: []agenteval.Question{
		{ID: "q1", Case: "sample", Scope: "close", Question: "What needs me?", Tools: []string{"close_brief"}, Facts: []string{"2 things"}},
		{ID: "q2", Case: "sample", Scope: "close", Question: "Should I buy bonds?", OffScope: true},
		{ID: "q3", Case: "sample", Scope: "close", Question: "Does the balance sheet tie?", Tools: []string{"tie_out"}},
	}}
	tool, answer = "close_brief", `{"kind":"facts","answer":"2 things need you.","support":[{"step":"t1","sentence":0,"path":"/headline","value":"2 things need you: capture a month-end snapshot, add client documents.","quote":""}]}`
	o1 := RunAgentEvalQuestion(t.Context(), db, set.Questions[0], record, client)
	tool, answer = "", `{"kind":"refusal","answer":"I don't give investment advice; I can read this close's status and documents.","support":[]}`
	o2 := RunAgentEvalQuestion(t.Context(), db, set.Questions[1], record, client)
	tool, answer = "tie_out", `{"kind":"help","answer":"I could not read the tie-out here.","support":[]}`
	o3 := RunAgentEvalQuestion(t.Context(), db, set.Questions[2], record, client)
	if o1.Outcome != "kept" || o1.Kind != "facts" || o1.Citations != 1 || strings.Join(o1.Tools, ",") != "close_brief" {
		t.Fatalf("the recorded read supports the answer: %+v", o1)
	}
	if o2.Outcome != "kept" || o2.Kind != "refusal" || o2.Card != "" || len(o2.Tools) != 0 {
		t.Fatalf("the refusal is kept with no card: %+v", o2)
	}
	if len(o3.Tools) != 0 || strings.Join(o3.Attempted, ",") != "tie_out" {
		t.Fatalf("an unrecorded read is unavailable, not a tool that ran, and is counted as tried: %+v", o3)
	}
	report := agenteval.Score(set, []agenteval.Observed{o1, o2, o3})
	if report.QuestionsPass != 1 || report.Questions != 2 || report.OffScopePass != 1 || report.Passed() {
		t.Fatalf("one question and the off-scope prompt pass, and one in two is below the bar: %+v", report)
	}
	if why := strings.Join(report.Results[2].Reasons, "; "); !strings.Contains(why, "did not call tie_out") {
		t.Fatalf("the missed tool is named: %q", why)
	}
	if !strings.Contains(report.Markdown(), "| q1 What needs me? | pass | close_brief | facts |") {
		t.Fatalf("the table names each question's result: %s", report.Markdown())
	}
}

// Context-machine evaluation must carry the recorded platform identity all the
// way through the real loop, including the final answer, and reject another owner.
func TestAgentEvalContextMachineKeepsIdentity(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	record := agenteval.Record{Case: "sample", Organization: "org_AgentEval" + rand.Text(), Subject: "user_eval_owner", CloseID: "7", Entity: "Alpha", Period: "2026-09", Today: "2026-10-08"}
	if err = EnsureAgentEvalFirm(t.Context(), db, record); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, record.Organization)
	defer db.Exec(`DELETE FROM model_calls WHERE organization_id=$1`, record.Organization)
	defer db.Exec(`DELETE FROM organizations WHERE id=$1`, record.Organization)
	scope := llm.Scope{Organization: record.Organization, Subject: record.Subject, AccessProfile: "local-eval"}
	calls := 0
	client, err := llm.New(llm.Config{Provider: "context_machine", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "gpt-6.1-sol", CodexEffort: "medium", Profiles: []llm.Scope{scope}, AllowUncappedOutputTokens: true, HTTP: &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		var wire llm.CompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Fatal(err)
		}
		if wire.Scope != scope || wire.Model != "gpt-6.1-sol" || wire.Effort != "medium" {
			t.Fatalf("evaluation lost caller or policy: %+v", wire)
		}
		answer := `{"kind":"refusal","answer":"I do not give investment advice.","support":[]}`
		if wire.Request.Tools != nil {
			answer = `{"text":"I do not give investment advice.","tool_calls":[]}`
		}
		raw, _ := json.Marshal(llm.CompletionResponse{Version: 1, Status: "completed", Scope: scope, Response: llm.Response{ID: "done", Model: wire.Model, ReasoningEffort: wire.Effort, AuthMode: "chatgpt", Text: answer, ModelCalls: 1}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	q := agenteval.Question{ID: "q", Case: "sample", Scope: "close", Question: "Should I buy bonds?", OffScope: true}
	out := RunAgentEvalQuestion(t.Context(), db, q, record, client)
	if out.Outcome != "kept" || out.Kind != "refusal" || calls < 1 {
		t.Fatalf("context-machine evaluation failed: %+v, calls %d", out, calls)
	}
	before := calls
	record.Subject = "user_eval_other"
	out = RunAgentEvalQuestion(t.Context(), db, q, record, client)
	if calls != before || out.Outcome == "kept" {
		t.Fatal("foreign evaluation identity reached subscription", out)
	}
}
