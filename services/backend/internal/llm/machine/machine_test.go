package machine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"close.local/backend/internal/llm"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProxyEnforcesBudgetCredentialsModelAndTools(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		t.Run(agent, func(t *testing.T) {
			requests := 0
			p := &modelProxy{ctx: context.Background(), token: "run-token", key: "provider-secret", model: "fixed-model", agent: agent, limit: 1, tokens: 100}
			p.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				requests++
				var b map[string]any
				json.NewDecoder(r.Body).Decode(&b)
				if b["model"] != "fixed-model" || len(b["tools"].([]any)) != 1 {
					t.Fatalf("policy not applied: %v", b)
				}
				if agent == "codex" && (r.Header.Get("Authorization") != "Bearer provider-secret" || b["store"] != false || b["max_output_tokens"] != float64(100)) {
					t.Fatal("OpenAI policy")
				}
				if agent == "claude" && (r.Header.Get("X-Api-Key") != "provider-secret" || b["max_tokens"] != float64(100)) {
					t.Fatal("Anthropic policy")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})}
			path := "/v1/responses"
			if agent == "claude" {
				path = "/v1/messages"
			}
			for i, want := range []int{401, 200, 429} {
				req := httptest.NewRequest("POST", path, strings.NewReader(`{"model":"attacker","store":true,"max_tokens":9999,"tools":[{"name":"Bash"},{"type":"web_search"},{"name":"mcp__records__list_closes"}]}`))
				if i > 0 {
					req.Header.Set("Authorization", "Bearer run-token")
				}
				w := httptest.NewRecorder()
				p.ServeHTTP(w, req)
				if w.Code != want {
					t.Fatalf("got %d want %d", w.Code, want)
				}
			}
			if requests != 1 || p.calls.Load() != 1 {
				t.Fatal("budget was bypassed")
			}
			req := httptest.NewRequest("GET", "/v1/secret", nil)
			w := httptest.NewRecorder()
			p.ServeHTTP(w, req)
			if w.Code != 404 {
				t.Fatal(w.Code)
			}
		})
	}
}

func TestCommandsAndOutputFailClosed(t *testing.T) {
	t.Setenv("DATABASE_URL", "must-not-leak")
	t.Setenv("OPENAI_API_KEY", "must-not-leak")
	for _, agent := range []string{"codex", "claude"} {
		args, env := command(agent, "fixed", "http://127.0.0.1:1234", "ephemeral", "/tmp/run", 2, "medium", "api_key")
		if strings.Contains(strings.Join(env, " "), "must-not-leak") {
			t.Fatal("inherited credentials")
		}
		if !strings.Contains(strings.Join(args, " "), "records") {
			t.Fatal("missing read tools")
		}
		for _, raw := range []string{"", `{}`, `{"type":"turn.failed"}`, `{"type":"result","subtype":"error","result":"unsafe partial"}`} {
			if _, _, err := parseOutput(agent, []byte(raw)); err == nil {
				t.Fatalf("accepted %s: %s", agent, raw)
			}
		}
	}
	text, u, err := parseOutput("codex", []byte("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"done\"}}\n{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}\n"))
	if err != nil || text != "done" || u.Input != 3 {
		t.Fatal(text, u, err)
	}
	for _, cached := range []bool{false, true} {
		usage := `"input_tokens":3,"output_tokens":2`
		want := 3
		if cached {
			usage += `,"cache_creation_input_tokens":11,"cache_read_input_tokens":17`
			want = 31
		}
		text, u, err = parseOutput("claude", []byte(`{"type":"result","subtype":"success","result":"done","usage":{`+usage+`}}`))
		if err != nil || text != "done" || u.Input != want || u.Output != 2 {
			t.Fatal(text, u, err)
		}
	}
	cancelled := false
	b := &limitedOutput{cancel: func() { cancelled = true }}
	if _, err = b.Write(make([]byte, (1<<20)+1)); err == nil || !cancelled {
		t.Fatal("unbounded process output")
	}
}

// Runs the actual pinned CLIs inside the release image against an in-process
// fake provider and MCP server. No credentials, external API or paid calls.
func TestInstalledCLIs(t *testing.T) {
	if os.Getenv("CONTEXT_MACHINE_CLI_SMOKE") != "1" {
		t.Skip("run inside context-machine image")
	}
	cases := []string{}
	for _, agent := range []string{"codex", "claude"} {
		levels := []string{"low", "medium", "high", "xhigh", "max"}
		if agent == "codex" {
			levels = append(levels, "ultra")
		}
		modes := []string{"api_key", "subscription"}
		if agent == "codex" {
			modes = append(modes, "chatgpt")
		}
		for _, mode := range modes {
			for _, effort := range levels {
				cases = append(cases, agent+"/"+mode+"/"+effort)
			}
		}
	}
	for _, testCase := range cases {
		parts := strings.Split(testCase, "/")
		agent, mode, effort := parts[0], parts[1], parts[2]
		t.Run(testCase, func(t *testing.T) {
			var toolCalls atomic.Int32
			records := mcp.NewServer(&mcp.Implementation{Name: "records", Version: "1"}, nil)
			records.AddTool(&mcp.Tool{Name: "list_closes", Description: "List closes", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}, InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				toolCalls.Add(1)
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "[]"}}}, nil
			})
			mux := http.NewServeMux()
			mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return records }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
			var calls atomic.Int32
			mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
				first := calls.Add(1) == 1
				var b map[string]any
				if json.NewDecoder(r.Body).Decode(&b) != nil {
					t.Error("invalid provider request")
				}
				t.Logf("%s requested %s", agent, r.URL.Path)
				if len(readTools(b["tools"])) != 1 {
					t.Errorf("scoped MCP tool missing from %s request", agent)
				}

				if agent == "codex" {
					w.Header().Set("Content-Type", "text/event-stream")
					if first {
						io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_smoke\",\"call_id\":\"call_smoke\",\"name\":\"list_closes\",\"namespace\":\"mcp__records\",\"arguments\":\"{}\",\"status\":\"completed\"}}\n\n")
						io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_tool\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
						return
					}
					io.WriteString(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_smoke\",\"status\":\"in_progress\"}}\n\n")
					io.WriteString(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg_smoke\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"smoke-ok\",\"annotations\":[]}]}}\n\n")
					io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_smoke\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
				} else {
					response := `{"id":"msg_smoke","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[{"type":"text","text":"smoke-ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
					if b["stream"] != true {
						w.Header().Set("Content-Type", "application/json")
						io.WriteString(w, response)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					events := []string{
						`{"type":"message_start","message":{"id":"msg_smoke","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
						`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
						`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"smoke-ok"}}`,
						`{"type":"content_block_stop","index":0}`,
						`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
						`{"type":"message_stop"}`,
					}
					if first {
						events = []string{
							`{"type":"message_start","message":{"id":"msg_tool","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`,
							`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_smoke","name":"mcp__records__list_closes","input":{}}}`,
							`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`,
							`{"type":"content_block_stop","index":0}`,
							`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":1}}`,
							`{"type":"message_stop"}`,
						}
					}
					for _, event := range events {
						var e struct{ Type string }
						json.Unmarshal([]byte(event), &e)
						fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, event)
					}
				}
			})
			fake := httptest.NewServer(mux)
			defer fake.Close()
			model := "gpt-6.1-sol"
			if agent == "claude" {
				model = "claude-sonnet-4-6"
			}
			// Exercise the actual policy proxy, including credential substitution, not
			// just CLI-to-fake-provider protocol compatibility.
			upstream, _ := url.Parse(fake.URL)
			proxy := &modelProxy{ctx: context.Background(), token: "fake-token", key: "upstream-secret", model: model, effort: effort, authMode: mode, agent: agent, limit: 2, tokens: 4096}
			if agent == "codex" {
				proxy.researchTools = codexRecordTools()
			}
			proxy.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if (mode == "subscription" || agent == "codex") && r.Header.Get("Authorization") != "Bearer upstream-secret" {
					t.Error("subscription credential missing")
				}
				if agent == "claude" && mode == "api_key" && r.Header.Get("X-Api-Key") != "upstream-secret" {
					t.Error("API credential missing")
				}
				if agent == "claude" && r.URL.Host != "api.anthropic.com" {
					t.Error("wrong Claude endpoint")
				}
				if agent == "codex" && (mode == "subscription" || mode == "chatgpt") && r.URL.Host != "chatgpt.com" {
					t.Error("wrong subscription endpoint")
				}
				r.URL.Scheme, r.URL.Host = upstream.Scheme, upstream.Host
				if agent == "codex" {
					r.URL.Path = "/v1/responses"
				}
				return http.DefaultTransport.RoundTrip(r)
			})}
			frontMux := http.NewServeMux()
			frontMux.Handle("/v1/", proxy)
			frontMux.Handle("/mcp", mux)
			front := httptest.NewServer(frontMux)
			defer front.Close()
			home := t.TempDir()
			if mode == "chatgpt" {
				if err := os.WriteFile(home+"/auth.json", []byte(fakeCodexAuth()), 0600); err != nil {
					t.Fatal(err)
				}
				// A local store verifies that CLI-owned credentials are saved before inference.
				store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
				defer store.Close()
				proxy.personal = &codexAuthSession{file: home + "/auth.json", endpoint: store.URL, client: store.Client()}
			}
			args, env := command(agent, model, front.URL, "fake-token", home, 2, effort, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, agent, args...)
			cmd.Env = env
			cmd.Dir = t.TempDir()
			cmd.Stdin = strings.NewReader("Reply smoke-ok.")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			raw, err := cmd.Output()
			if err != nil {
				t.Fatalf("CLI: %v\n%s\n%s", err, raw, stderr.String())
			}
			text, _, err := parseOutput(agent, raw)
			if err != nil || text != "smoke-ok" || calls.Load() != 2 || toolCalls.Load() != 1 {
				t.Fatalf("protocol: %q %v\n%s\n%s", text, err, raw, stderr.String())
			}
		})
	}
}

func TestReadOnlyDatabaseScopeAndRevocation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires local PostgreSQL")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ctx := context.Background()
	if checkDatabase(ctx, admin) == nil {
		t.Fatal("accepted application/owner role")
	}
	role := fmt.Sprintf("cm_test_%d", time.Now().UnixNano())
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Exercise the shipped provisioning SQL as a managed-style, non-superuser
	// owner with grant options, rather than hand-writing the reader's grants.
	owner := role + "_owner"
	must("CREATE ROLE " + owner + " CREATEROLE")
	defer func() { admin.Exec("DROP OWNED BY " + owner); admin.Exec("DROP ROLE " + owner) }()
	must("GRANT USAGE ON SCHEMA public TO " + owner + " WITH GRANT OPTION")
	must("GRANT SELECT ON organizations,client_closes,evidence,current_organization_members,current_client_access,organization_member_access,client_access TO " + owner + " WITH GRANT OPTION")
	conn, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "SET ROLE "+owner); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, "RESET ROLE")
	if _, err = conn.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'test-only'"); err != nil {
		t.Fatal(err)
	}
	defer func() { admin.Exec("DROP OWNED BY " + role); admin.Exec("DROP ROLE " + role) }()
	script, err := os.ReadFile("../../../../../infra/postgres/context-machine-role.sql")
	if err != nil {
		t.Fatal(err)
	}
	sqlScript := strings.ReplaceAll(string(script), `\set ON_ERROR_STOP on`, "")
	sqlScript = strings.ReplaceAll(sqlScript, `:"agent_role"`, `"`+role+`"`)
	if _, err = conn.ExecContext(ctx, sqlScript); err != nil {
		t.Fatal("managed-owner provisioning failed:", err)
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, "test-only")
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = checkDatabase(ctx, db); err != nil {
		t.Fatal(err)
	}
	org := "org" + strings.ReplaceAll(role, "_", "")
	org = "org_" + org
	scope := llm.Scope{Organization: org, Subject: "user_cm_test", AccessProfile: "research"}
	must("INSERT INTO organizations(id,name) VALUES($1,'CM test')", org)
	defer func() {
		admin.Exec("DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)", org)
		admin.Exec("DELETE FROM client_closes WHERE organization_id=$1", org)
		admin.Exec("DELETE FROM client_access WHERE organization_id=$1", org)
		admin.Exec("DELETE FROM organization_member_access WHERE organization_id=$1", org)
		admin.Exec("DELETE FROM organizations WHERE id=$1", org)
	}()
	must("INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'test','test')", org, scope.Subject)
	must("INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,'allowed',true,'test','test')", org, scope.Subject)
	must("INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'allowed','2026-01'),($1,'forbidden','2026-01')", org)
	result, err := readRecords(ctx, db, scope, 0)
	if err != nil || !strings.Contains(result, "allowed") || strings.Contains(result, "forbidden") {
		t.Fatal(result, err)
	}
	var closeID int64
	if err = admin.QueryRow("SELECT id FROM client_closes WHERE organization_id=$1 AND entity='allowed'", org).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	must("INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'statement','cm-test','password: hidden-secret','test')", closeID)
	result, err = readRecords(ctx, db, scope, closeID)
	if err != nil || strings.Contains(result, "hidden-secret") || !strings.Contains(result, "removed") || !json.Valid([]byte(result)) {
		t.Fatal(result, err)
	}
	foreign := scope
	foreign.Organization = "org_FOREIGN"
	if _, err = readRecords(ctx, db, foreign, closeID); err == nil {
		t.Fatal("cross-organization scope accepted")
	}
	if _, err = db.Exec("UPDATE client_closes SET entity='bad'"); err == nil {
		t.Fatal("read-only login could write")
	}
	must("INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,$2,'allowed',false,'test','test')", org, scope.Subject)
	result, err = readRecords(ctx, db, scope, 0)
	if err != nil || result != "[]" {
		t.Fatal(result, err)
	}
	must("INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,false,'test','test')", org, scope.Subject)
	if _, err = readRecords(ctx, db, scope, 0); err == nil {
		t.Fatal("revoked member accepted")
	}
}

func TestServiceRejectsUnauthorizedAndInvalidRuns(t *testing.T) {
	cfg := Config{Token: strings.Repeat("x", 32), DatabaseURL: "postgres://invalid/unused", OpenAIKey: "fake", AnthropicKey: "fake", CodexModel: "fixed", ClaudeModel: "fixed", Profiles: []llm.Scope{{Organization: "org_test", Subject: "user_test", AccessProfile: "read"}}}
	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body, auth string
		want             int
	}{
		{"/readyz", "", "", 401},
		{"/v1/run", "{}", "", 401},
		{"/v1/run", "{}", cfg.Token, 400},
		{"/v1/run", `{"version":1,"executable":"sh"}`, cfg.Token, 400},
	} {
		method := "POST"
		if tc.path == "/readyz" {
			method = "GET"
		}
		r := httptest.NewRequest(method, tc.path, strings.NewReader(tc.body))
		if tc.auth != "" {
			r.Header.Set("Authorization", "Bearer "+tc.auth)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d want %d", tc.path, w.Code, tc.want)
		}
	}
	body, _ := json.Marshal(runRequest{Version: 1, Purpose: "research", Model: "codex", Instructions: "research", Scope: llm.Scope{Organization: "org_other", Subject: "user_test", AccessProfile: "read"}, ReadOnly: true, Deadline: time.Now().Add(time.Minute), MaxOutputTokens: 100, MaxModelCalls: 1})
	r := httptest.NewRequest("POST", "/v1/run", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+cfg.Token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestExecutionDeadline(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/codex", []byte("#!/bin/sh\nsleep 30 &\nwait\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	s := &server{agents: map[string]agentSettings{"codex": {model: "fake", credential: "fake", effort: "medium", mode: "api_key"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := s.execute(ctx, runRequest{Model: "codex", Instructions: "test", MaxModelCalls: 1, MaxOutputTokens: 10}); err == nil {
		t.Fatal("deadline ignored")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("child process retained output pipes after cancellation")
	}
}
