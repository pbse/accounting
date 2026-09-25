package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestMCPToolsUseTheSignedInPersonsAccess(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	other := "org_" + rand.Text()
	var mine, theirs string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "MCP client "+rand.Text()).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, other); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,'2026-09',$2) RETURNING id::text`, "Other client "+rand.Text(), other).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, id := range []string{mine, theirs} {
			db.Exec(`DELETE FROM approval_requests WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM preparation_intakes WHERE job_id IN (SELECT id FROM preparation_jobs WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organizations WHERE id=$1`, other)
	}()
	content := "Invoice " + rand.Text()
	var evidenceID, paperID string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, mine, preparation.Digest([]byte(content)), content).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	sources := fmt.Sprintf(`{"sources":[{"id":"%s","digest":"%s","role":"invoice","name":"invoice.txt","location":"line 1"}],"blockers":[]}`, evidenceID, preparation.Digest([]byte(content)))
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid','mcp',$2,'fixture') RETURNING id::text`, mine, sources).Scan(&paperID); err != nil {
		t.Fatal(err)
	}
	var blockedID string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid','mcp-blocked',$2,'fixture') RETURNING id::text`, mine, strings.Replace(sources, `"blockers":[]`, `"blockers":["Service end date missing"]`, 1)).Scan(&blockedID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_ALLOWED_ORIGINS", "https://agent.example, https://other.example")
	token := strings.Repeat("m", 32)
	server := httptest.NewServer(apiWithStore(db, token))
	defer server.Close()
	ctx := context.Background()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if version := session.InitializeResult().ProtocolVersion; version != "2026-07-28" {
		t.Fatalf("a current client must get the current protocol, got %s", version)
	}
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"list_closes", "get_workpaper", "find_exceptions", "close_brief", "tie_out", "explain_changes", "agent_track_record", "morning_brief", "draft_client_request", "request_approval", "prepare_close"} {
		if !slices.Contains(names, want) {
			t.Fatalf("missing tool %s in %v", want, names)
		}
	}
	for _, name := range names {
		if strings.Contains(name, "approve") && name != "request_approval" || strings.Contains(name, "post") || strings.Contains(name, "sign") {
			t.Fatalf("agents must not approve, sign off or post: %s", name)
		}
	}
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if e != nil {
			t.Fatal(e)
		}
		return result
	}
	text := func(result *mcp.CallToolResult) string { return result.Content[0].(*mcp.TextContent).Text }
	if listed := call("list_closes", map[string]any{"query": "MCP client"}); listed.IsError || !strings.Contains(text(listed), `"id":"`+mine+`"`) {
		t.Fatalf("list_closes must return the person's closes: %s", text(listed))
	}
	if denied := call("get_close", map[string]any{"close_id": theirs}); !denied.IsError || !strings.Contains(text(denied), "NOT_FOUND") {
		t.Fatalf("another firm's close must stay hidden: %s", text(denied))
	}
	if added := call("add_evidence", map[string]any{"close_id": mine, "name": "bank.csv", "text": "id,date,amount\nB1,2026-09-02,10.00\n"}); added.IsError {
		t.Fatalf("add_evidence: %s", text(added))
	}
	var jobs int
	if err = db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id=$1`, mine).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("an added document must start intake: %d %v", jobs, err)
	}
	if blocked := call("request_approval", map[string]any{"close_id": mine, "workpaper_id": blockedID, "note": "Check the service dates"}); !blocked.IsError || !strings.Contains(text(blocked), "CONFLICT") {
		t.Fatalf("a draft the queue hides must not take an approval request: %s", text(blocked))
	}
	if requested := call("request_approval", map[string]any{"close_id": mine, "workpaper_id": paperID, "note": "Check the service dates"}); requested.IsError {
		t.Fatalf("request_approval: %s", text(requested))
	}
	if queue := call("review_queue", map[string]any{"client": "MCP client"}); !strings.Contains(text(queue), `"approval_note":"Check the service dates"`) {
		t.Fatalf("the request must reach the review queue: %s", text(queue))
	}
	var reviews int
	if err = db.QueryRow(`SELECT count(*) FROM workpaper_reviews WHERE workpaper_id=$1`, paperID).Scan(&reviews); err != nil || reviews != 0 {
		t.Fatalf("an approval request must not approve: %d %v", reviews, err)
	}

	post := func(body string, headers map[string]string) *http.Response {
		t.Helper()
		r, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		response, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	auth := map[string]string{"Authorization": "Bearer " + token}
	legacy := post(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"legacy","version":"1"}}}`, auth)
	var initialized struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	json.NewDecoder(legacy.Body).Decode(&initialized)
	if legacy.StatusCode != 200 || initialized.Result.ProtocolVersion != "2025-06-18" || legacy.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("an initialize-era client must be served: %d %+v", legacy.StatusCode, initialized)
	}
	mismatch := post(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"modern","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`, map[string]string{"Authorization": "Bearer " + token, "MCP-Protocol-Version": "2026-07-28", "Mcp-Method": "tools/call"})
	if mismatch.StatusCode != 400 {
		t.Fatalf("mismatched MCP headers must be rejected: %d", mismatch.StatusCode)
	}
	if anonymous := post(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`, nil); anonymous.StatusCode != 401 || anonymous.Header.Get("WWW-Authenticate") == "" {
		t.Fatalf("MCP requires the same sign-in as the API: %d", anonymous.StatusCode)
	}
	if trusted := post(`{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"browser","version":"1"}}}`, map[string]string{"Authorization": "Bearer " + token, "Origin": "https://other.example"}); trusted.StatusCode != 200 || trusted.Header.Get("Access-Control-Allow-Origin") != "https://other.example" {
		t.Fatalf("a configured browser origin must be served: %d", trusted.StatusCode)
	}
	if foreign := post(`{"jsonrpc":"2.0","id":4,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer " + token, "Origin": "https://attacker.example"}); foreign.StatusCode != 403 {
		t.Fatalf("an untrusted browser origin must be refused: %d", foreign.StatusCode)
	}
}
