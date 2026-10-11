package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/llm"
)

func TestCompletionRejectsInvalidPolicyBeforeExecution(t *testing.T) {
	cfg := Config{Token: strings.Repeat("x", 32), DatabaseURL: "postgres://invalid/unused", CodexModel: "fixed", OpenAIKey: "key", Profiles: []llm.Scope{{Organization: "org", Subject: "owner", AccessProfile: "own"}}}
	handler, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	base := llm.CompletionRequest{Version: 1, Agent: "codex", Model: "fixed", Effort: "medium", Scope: cfg.Profiles[0], Deadline: time.Now().Add(time.Minute), Request: llm.Request{Purpose: "preparation", Instructions: "Complete", MaxOutputTokens: 100, MaxResponseBytes: 4096}}
	for _, tc := range []struct {
		change func(*llm.CompletionRequest)
		status int
	}{
		{func(r *llm.CompletionRequest) { r.Model = "other" }, 400},
		{func(r *llm.CompletionRequest) { r.Effort = "high" }, 400},
		{func(r *llm.CompletionRequest) { r.Request.Purpose = "unknown" }, 400},
	} {
		in := base
		tc.change(&in)
		raw, _ := json.Marshal(in)
		r := httptest.NewRequest("POST", "/v1/complete", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+cfg.Token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestInstalledCompletionCLIs(t *testing.T) {
	if os.Getenv("CONTEXT_MACHINE_CLI_SMOKE") != "1" {
		t.Skip("run inside context-machine image")
	}
	for _, agent := range []string{"codex", "claude"} {
		modes := []string{"api_key", "subscription"}
		if agent == "codex" {
			modes = append(modes, "chatgpt")
		}
		for _, mode := range modes {
			t.Run(agent+"/"+mode, func(t *testing.T) {
				want := `{"text":"","tool_calls":[{"call_id":"call","name":"read","arguments":"{}"}]}`
				schema, prompt, err := completionPrompt(llm.Request{Purpose: "investigation", Messages: []llm.Message{{Role: "user", Content: "Read"}}, Tools: []llm.Tool{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}, ToolChoice: "auto", ParallelToolCalls: true})
				if err != nil {
					t.Fatal(err)
				}
				calls := 0
				proxy := &modelProxy{images: []llm.Image{{PNG: testPageImage(t)}}, ctx: context.Background(), token: "run", key: "private", model: "gpt-6.1-sol", effort: "medium", authMode: mode, agent: agent, limit: 1, tokens: 4096, completion: true, schema: schema}
				if agent == "claude" {
					proxy.model = "claude-sonnet-4-6"
				}
				proxy.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					calls++
					var body map[string]any
					json.NewDecoder(r.Body).Decode(&body)
					key := "input"
					if agent == "claude" {
						key = "messages"
					}
					items := body[key].([]any)
					if len(items[len(items)-1].(map[string]any)["content"].([]any)) != 1 {
						t.Fatal("CLI proxy lost attached image")
					}
					if len(body["tools"].([]any)) != 0 {
						t.Fatal("CLI was given executable tools")
					}
					if agent == "codex" && body["text"].(map[string]any)["format"].(map[string]any)["type"] != "json_schema" {
						t.Fatal("lost output schema")
					}
					if agent == "claude" && body["output_config"].(map[string]any)["format"].(map[string]any)["type"] != "json_schema" {
						t.Fatal("lost output schema")
					}
					encoded, _ := json.Marshal(want)
					var response strings.Builder
					if agent == "codex" {
						fmt.Fprint(&response, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp\",\"status\":\"in_progress\"}}\n\n")
						fmt.Fprintf(&response, "data: {\"type\":\"response.output_text.delta\",\"delta\":%s}\n\n", encoded)
						fmt.Fprintf(&response, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"id\":\"msg\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":%s,\"annotations\":[]}]}}\n\n", encoded)
						fmt.Fprint(&response, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
					} else {
						events := []string{`{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"claude-sonnet-4-6","content":[],"stop_reason":null,"usage":{"input_tokens":1,"output_tokens":0}}}`, `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, fmt.Sprintf(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":%s}}`, encoded), `{"type":"content_block_stop","index":0}`, `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`, `{"type":"message_stop"}`}
						for _, e := range events {
							var v struct{ Type string }
							json.Unmarshal([]byte(e), &v)
							fmt.Fprintf(&response, "event: %s\ndata: %s\n\n", v.Type, e)
						}
					}
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(response.String()))}, nil
				})}
				front := httptest.NewServer(proxy)
				defer front.Close()
				dir := t.TempDir()
				if mode == "chatgpt" {
					os.WriteFile(dir+"/auth.json", []byte(fakeCodexAuth()), 0600)
					store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
					defer store.Close()
					proxy.personal = &codexAuthSession{file: dir + "/auth.json", endpoint: store.URL, client: store.Client()}
				}
				args, env := command(agent, proxy.model, front.URL, "run", dir, 1, proxy.effort, mode)
				args, err = completionCommand(agent, args, dir, schema)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, agent, args...)
				cmd.Env = env
				cmd.Dir = dir
				cmd.Stdin = strings.NewReader(prompt)
				var stderr strings.Builder
				cmd.Stderr = &stderr
				raw, err := cmd.Output()
				if err != nil {
					t.Fatalf("CLI: %v\n%s\n%s", err, raw, stderr.String())
				}
				text, _, err := parseOutput(agent, raw)
				if err != nil || text != want || calls != 1 {
					t.Fatalf("%q %v calls=%d\n%s\n%s", text, err, calls, raw, stderr.String())
				}
			})
		}
	}
}

func TestCompletionWaitIsBoundedByCallerDeadline(t *testing.T) {
	scope := llm.Scope{Organization: "org", Subject: "owner", AccessProfile: "own"}
	s := &server{cfg: Config{Profiles: []llm.Scope{scope}}, agents: map[string]agentSettings{"codex": {model: "fixed", effort: "medium", mode: "api_key"}}, busy: make(chan struct{}, 1)}
	s.busy <- struct{}{}
	// A valid number beyond float64 range must survive the actual wire decoder.
	in := llm.CompletionRequest{Version: 1, Agent: "codex", Model: "fixed", Effort: "medium", Scope: scope, Deadline: time.Now().Add(40 * time.Millisecond), Request: llm.Request{Purpose: "preparation", Instructions: "Complete", Data: json.RawMessage(`{"amount":1200.00,"id":9007199254740993,"large":1e999}`), MaxOutputTokens: 100, MaxResponseBytes: 4096}}
	raw, _ := json.Marshal(in)
	w := httptest.NewRecorder()
	s.complete(w, httptest.NewRequest("POST", "/v1/complete", strings.NewReader(string(raw))))
	if w.Code != 504 {
		t.Fatal("busy completion did not wait until its deadline", w.Code, w.Body.String())
	}
	if len(s.busy) != 1 {
		t.Fatal("waiting request released another run's slot")
	}
}

func TestCompletionProviderStatusRejectsTruncationRefusalAndMissingTerminal(t *testing.T) {
	for _, tc := range []struct {
		agent, raw string
		valid      bool
	}{
		{"claude", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"max_tokens\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", false},
		{"claude", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\"}}\n\n", false},
		{"claude", "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n", true},
		{"codex", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"incomplete\"}}\n\n", false},
		{"codex", "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"reason\"}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"text\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true},
		{"codex", "data: {\"type\":\"response.refusal.delta\"}\n\n", false},
		{"codex", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n", false},
	} {
		if err := completionProviderStatus(tc.agent, []byte(tc.raw)); (err == nil) != tc.valid {
			t.Fatal(tc.agent, err)
		}
	}
}

func TestCompletionPromptPreservesJSONNumbers(t *testing.T) {
	// Completion's wire decoder uses json.Number; the prompt must preserve it.
	dec := json.NewDecoder(strings.NewReader(`{"request":{"purpose":"preparation","instructions":"Read amounts","data":{"amount":1200.00,"rate":0.10,"id":9007199254740993}}}`))
	dec.UseNumber()
	var in llm.CompletionRequest
	if err := dec.Decode(&in); err != nil {
		t.Fatal(err)
	}
	_, prompt, err := completionPrompt(in.Request)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"amount":1200.00`, `"rate":0.10`, `"id":9007199254740993`} {
		if !strings.Contains(prompt, want) {
			t.Fatal("numeric input changed", prompt)
		}
	}
}

// Exercise the real service, subprocess and proxy with a fake CLI/provider. An
// unreachable DB proves completion never consults membership or database state.
func TestPrivateCompletionAcrossFirmsWithoutProfilesOrDatabaseReads(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("Node is required for the CLI protocol fixture", err)
	}
	dir := t.TempDir()
	script := `const fs=require('fs');
const args=process.argv.slice(2);
if(args.some(v=>v.startsWith('mcp_servers.')))process.exit(10);
const setting=args.find(v=>v.startsWith('model_providers.context_machine.base_url='));
const base=JSON.parse(setting.split('=').slice(1).join('='));
const token=process.env.CONTEXT_MACHINE_RUN_TOKEN;
(async()=>{
 const mcp=await fetch(base.replace('/v1','')+'/mcp',{method:'POST',headers:{Authorization:'Bearer '+token},body:'{}'});
 if(mcp.status!==404)process.exit(11);
 const res=await fetch(base+'/responses',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':'application/json'},body:JSON.stringify({model:'wrong-cli-model',reasoning:{effort:'high'},input:[],tools:[{type:'function',name:'shell_command'}]})});
 if(res.status!==200)process.exit(12);
 await res.text();
 const prompt=fs.readFileSync(0,'utf8');
 const packet=JSON.parse(prompt.slice(prompt.indexOf('\n\n')+2));
 console.log(JSON.stringify({type:'item.completed',item:{type:'agent_message',text:'answer:'+packet.Data.case}}));
 console.log(JSON.stringify({type:'turn.completed',usage:{input_tokens:1,output_tokens:1}}));
})().catch(()=>process.exit(13));
`
	if err = os.WriteFile(dir+"/codex", []byte("#!"+node+"\n"+script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	old := http.DefaultClient
	calls := 0
	http.DefaultClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.openai.com/v1/responses" || r.Header.Get("Authorization") != "Bearer fake-provider-key" {
			t.Error("provider route changed")
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "fixed" || body["reasoning"].(map[string]any)["effort"] != "medium" {
			t.Error("upstream model/effort pin lost")
		}
		if len(body["tools"].([]any)) != 0 {
			t.Error("completion acquired executable tools")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = old })
	cfg := Config{Token: strings.Repeat("x", 32), DatabaseURL: "postgres://unreachable.invalid/unused", CodexModel: "fixed", OpenAIKey: "fake-provider-key"}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i, scope := range []llm.Scope{{Organization: "firm-a", Subject: "user-a"}, {Organization: "firm-b", Subject: "user-b"}, {}} {
		in := llm.CompletionRequest{Version: 1, Agent: "codex", Model: "fixed", Effort: "medium", Scope: scope, Deadline: time.Now().Add(10 * time.Second), Request: llm.Request{Purpose: "written_answer", Instructions: "Answer only supplied data", Data: map[string]any{"case": i}, MaxOutputTokens: 100, MaxResponseBytes: 4096}}
		raw, _ := json.Marshal(in)
		send := func(auth string) *httptest.ResponseRecorder {
			r := httptest.NewRequest("POST", "/v1/complete", strings.NewReader(string(raw)))
			r.Header.Set("Authorization", auth)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w
		}
		for _, auth := range []string{"", "Bearer wrong-service-token"} {
			if w := send(auth); w.Code != 401 {
				t.Fatal("unauthenticated completion accepted", w.Code)
			}
		}
		w := send("Bearer " + cfg.Token)
		var out llm.CompletionResponse
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Text != fmt.Sprintf("answer:%d", i) || out.Scope != scope || out.ModelCalls != 1 {
			t.Fatal("profile-free completion failed", w.Code, w.Body.String())
		}
	}
	if calls != 3 {
		t.Fatal("unexpected provider calls", calls)
	}
	// No profiles still means no database-research authority.
	research := runRequest{Version: 1, Purpose: "research", Model: "codex", Instructions: "Read", Scope: llm.Scope{Organization: "firm-a", Subject: "user-a", AccessProfile: "invented"}, ReadOnly: true, Deadline: time.Now().Add(time.Minute), MaxModelCalls: 1, MaxOutputTokens: 100}
	raw, _ := json.Marshal(research)
	r := httptest.NewRequest("POST", "/v1/run", strings.NewReader(string(raw)))
	r.Header.Set("Authorization", "Bearer "+cfg.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || calls != 3 {
		t.Fatal("research gained database authority", w.Code)
	}
}
