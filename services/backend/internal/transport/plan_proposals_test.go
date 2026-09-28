package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	_ "close.local/backend/activities/close_summary"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A connected agent reads the catalog and proposes a plan with its reason; it lands in the staff approval queue, reason
// and all, and nothing about it runs. A person using the app directly does not propose here.
func TestAgentsProposePlansThroughMCP(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("WORKOS_CLIENT_ID", "")
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Plan client "+rand.Text()).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	marker := "Plan " + rand.Text()
	defer func() {
		db.Exec(`DELETE FROM workflow_plans WHERE title LIKE $1||'%'`, marker)
		db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, closeID)
	}()
	token := strings.Repeat("p", 32)
	server := httptest.NewServer(apiWithStore(db, token))
	defer server.Close()
	ctx := context.Background()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) (string, bool) {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		return result.Content[0].(*mcp.TextContent).Text, result.IsError
	}
	plan := func(activity string) map[string]any {
		return map[string]any{"version": 1, "title": marker + " check", "nodes": []any{map[string]any{"id": "look", "activity": activity, "input": map[string]any{"close_id": closeID}}}}
	}

	if catalog, failed := call("plan_catalog", map[string]any{}); failed || !strings.Contains(catalog, `"name":"close_summary"`) || !strings.Contains(catalog, "must directly follow an approval node") {
		t.Fatalf("the catalog lists the activities and the format: %s", catalog)
	}
	if text, failed := call("propose_plan", map[string]any{"plan": plan("send_everything"), "rationale": "The close needs a look before month end."}); !failed || !strings.Contains(text, "send_everything") {
		t.Fatalf("a plan that does not validate is refused with why: %s", text)
	}
	if text, failed := call("propose_plan", map[string]any{"plan": plan("close_summary"), "rationale": "Why not"}); !failed || !strings.Contains(text, "10 to 2,000") {
		t.Fatalf("a proposal says why: %s", text)
	}
	text, failed := call("propose_plan", map[string]any{"plan": plan("close_summary"), "rationale": "The close has new documents; summarise it first."})
	var proposed struct {
		PlanID int64  `json:"plan_id"`
		State  string `json:"state"`
	}
	if failed || json.Unmarshal([]byte(text), &proposed) != nil || proposed.State != "proposed" {
		t.Fatalf("a valid plan is proposed: %s", text)
	}
	var source, state, agent, rationale string
	var runs int
	if err = db.QueryRow(`SELECT source,state,agent,rationale,(SELECT count(*) FROM workflow_runs WHERE plan_id=$1) FROM workflow_plans WHERE id=$1`, proposed.PlanID).Scan(&source, &state, &agent, &rationale, &runs); err != nil || source != "agent" || state != "proposed" || agent != "MCP" || rationale != "The close has new documents; summarise it first." || runs != 0 {
		t.Fatalf("saved as the agent's proposal, running nothing: %s %s %s %q %d %v", source, state, agent, rationale, runs, err)
	}
	if listed, failed := call("list_plan_proposals", map[string]any{}); failed || !strings.Contains(listed, `"id":`+strconv.FormatInt(proposed.PlanID, 10)) || !strings.Contains(listed, `"state":"proposed"`) {
		t.Fatalf("the agent sees what became of its proposal: %s", listed)
	}

	// The staff approval queue shows it, and its reason once revealed.
	h := apiWithStore(db, token)
	if queue := mustString(adminJSON(t, h, token, "GET", "/v1/admin/plans?state=proposed&source=agent", "", 200)); !strings.Contains(queue, `"id":`+strconv.FormatInt(proposed.PlanID, 10)) {
		t.Fatalf("the proposal waits in the approval queue: %s", queue)
	}
	if detail := mustString(adminJSON(t, h, token, "GET", "/v1/admin/plans/"+strconv.FormatInt(proposed.PlanID, 10)+"?reveal=1", "", 200, "X-Access-Reason", "Reviewing the agent's proposal")); !strings.Contains(detail, "summarise it first") {
		t.Fatalf("staff read the agent's reason: %s", detail)
	}

	// The app itself does not propose as an agent: only a connected agent does.
	r := httptest.NewRequest("POST", "/v1/closes/plans/proposals", strings.NewReader(`{"plan":{},"rationale":"Proposed without an agent"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("a direct request does not propose: %d %s", w.Code, w.Body.String())
	}
}

// The planner drafts a plan with the firm's model from staff's instruction and saves it as the agent's proposal; a draft
// that does not validate gets one more attempt, told what was wrong. It writes only for a firm that allows
// model-written answers, counts each attempt against the firm's model calls, and records every draft for its track
// record.
func TestThePlannerDraftsAProposalWithTheFirmsModel(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("OPENAI_MODEL", "test-model")
	token := strings.Repeat("q", 32)
	firm := "org_Planner" + rand.Text()
	var closeID string
	if _, err := db.Exec(`INSERT INTO organizations(id,name,assistant_model_answers) VALUES($1,'Planner firm',false)`, firm); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme','2026-09') RETURNING id::text`, firm).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM workflow_plans WHERE organization_id=$1`, firm) })

	valid := `{"title":"Look at Acme","rationale":"Summarise the close before anyone chases the client.","nodes":[{"id":"look","after":[],"when":"","kind":"activity","activity":"close_summary","input":"{\"close_id\":\"` + closeID + `\"}","event":"","timeout":"","duration":"","permission":"","subject":"","prompt":""}]}`
	invalid := strings.Replace(valid, `"activity":"close_summary"`, `"activity":"email_the_client"`, 1)
	var mu sync.Mutex
	var answers []string
	var asked []string
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.openai.com" {
			t.Errorf("unexpected call %s", r.URL)
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, string(body))
		answer := answers[0]
		answers = answers[1:]
		if answer == "down" {
			return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{}`)), Header: make(http.Header)}, nil
		}
		envelope, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]any{"type": "output_text", "text": answer}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(envelope))), Header: make(http.Header)}, nil
	})
	h := apiWithStore(db, token)
	draft := func(want int) map[string]any {
		t.Helper()
		return adminJSON(t, h, token, "POST", "/v1/admin/plans/draft", `{"firm":"`+firm+`","close_id":"`+closeID+`","instruction":"Summarise Acme's September close before we chase the client","reason":"Support asked for a plan"}`, want)
	}

	// Only for a firm that allows model-written answers.
	draft(409)
	if len(asked) != 0 {
		t.Fatal("the planner must not call the model for a firm that has not allowed it")
	}
	if _, err := db.Exec(`UPDATE organizations SET assistant_model_answers=true WHERE id=$1`, firm); err != nil {
		t.Fatal(err)
	}

	// A draft that does not validate is tried once more, told what was wrong.
	answers = []string{invalid, valid}
	got := draft(200)
	id := int64(got["plan_id"].(float64))
	if len(asked) != 2 || !strings.Contains(asked[0], `\"client\":\"Acme\"`) || !strings.Contains(asked[0], "close_summary") || !strings.Contains(asked[1], "what_was_wrong") || !strings.Contains(asked[1], "email_the_client") {
		t.Fatalf("the planner is told the close, the catalog and, the second time, what was wrong: %v", asked)
	}
	var source, state, agent, rationale, by string
	if err := db.QueryRow(`SELECT source,state,agent,rationale,created_by FROM workflow_plans WHERE id=$1`, id).Scan(&source, &state, &agent, &rationale, &by); err != nil || source != "agent" || state != "proposed" || agent != "planner (test-model)" || rationale != "Summarise the close before anyone chases the client." || by != "staff:local-development" {
		t.Fatalf("saved as the planner's proposal: %s %s %s %q %s %v", source, state, agent, rationale, by, err)
	}
	var calls int
	if err := db.QueryRow(`SELECT calls FROM model_calls WHERE organization_id=$1`, firm).Scan(&calls); err != nil || calls != 2 {
		t.Fatalf("each attempt counts against the firm's model calls: %d %v", calls, err)
	}

	// Two drafts that do not validate, and a model that is down, save nothing and are recorded as refused.
	answers = []string{invalid, invalid}
	if message := draft(422)["message"].(string); !strings.Contains(message, "after 2 attempts") || !strings.Contains(message, "email_the_client") {
		t.Fatalf("an invalid draft says what was wrong: %s", message)
	}
	answers = []string{"down"}
	draft(502)
	var audit string
	if err := db.QueryRow(`SELECT string_agg(result||':'||split_part(error,':',1),',' ORDER BY id) FROM platform_audit WHERE organization_id=$1 AND action='plan.draft_with_model'`, firm).Scan(&audit); err != nil || audit != "ok:,refused:the plan is not valid,refused:the model did not draft a plan" {
		t.Fatalf("every draft is recorded: %q %v", audit, err)
	}

	// The track record counts the planner's proposals and its drafts that failed.
	var record struct {
		Agents []struct {
			Agent    string `json:"agent"`
			Proposed int    `json:"proposed"`
		} `json:"agents"`
		Planner map[string]int `json:"planner"`
	}
	raw, _ := json.Marshal(adminJSON(t, h, token, "GET", "/v1/admin/plans/agents", "", 200))
	json.Unmarshal(raw, &record)
	found := false
	for _, a := range record.Agents {
		found = found || a.Agent == "planner (test-model)" && a.Proposed >= 1
	}
	if !found || record.Planner["drafts"] < 3 || record.Planner["invalid"] < 1 || record.Planner["failed"] < 1 {
		t.Fatalf("the track record: %s", raw)
	}
}
