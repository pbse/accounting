package machine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewCodexToolCarriersCannotBypassPolicy(t *testing.T) {
	for _, completion := range []bool{false, true} {
		t.Run(map[bool]string{false: "research", true: "completion"}[completion], func(t *testing.T) {
			calls := 0
			p := &modelProxy{ctx: context.Background(), token: "run", key: "private", agent: "codex", model: "gpt-6.1-sol", effort: "medium", authMode: "api_key", limit: 1, tokens: 100, completion: completion, researchTools: codexRecordTools()}
			p.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				input := body["input"].([]any)
				if len(input) != 1 || input[0].(map[string]any)["type"] != "message" {
					t.Fatal("CLI tool carrier survived", input)
				}
				tools := body["tools"].([]any)
				if completion {
					if len(tools) != 0 {
						t.Fatal("completion acquired tools")
					}
				} else {
					if len(tools) != 1 || len(tools[0].(map[string]any)["tools"].([]any)) != 2 {
						t.Fatal("research policy lost")
					}
					for _, item := range tools[0].(map[string]any)["tools"].([]any) {
						name := item.(map[string]any)["name"]
						if name != "list_closes" && name != "read_evidence" {
							t.Fatal("unscoped tool", name)
						}
					}
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))}, nil
			})}
			raw := `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"shell"}]},{"type":"configuration_update","tools":[{"type":"web_search"}]},{"type":"tool_search_output","tools":[{"type":"function","name":"write"}]},{"type":"message","role":"user","content":"literal additional_tools is data"}],"tools":[{"type":"function","name":"shell"}]}`
			req := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(raw))
			req.Header.Set("Authorization", "Bearer run")
			w := httptest.NewRecorder()
			p.ServeHTTP(w, req)
			if w.Code != 200 || calls != 1 {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestProviderCannotInvokeHiddenCLITools(t *testing.T) {
	for _, tc := range []struct {
		agent, raw string
		researchOK bool
	}{
		{"codex", `{"type":"response.output_item.done","item":{"type":"function_call","namespace":"mcp__records","name":"list_closes"}}`, true},
		{"codex", `{"type":"response.output_item.done","item":{"type":"function_call","namespace":"mcp__records","name":"read_evidence"}}`, true},
		{"codex", `{"type":"response.completed","response":{"output":[{"type":"function_call","namespace":"functions","name":"exec"}]}}`, false},
		{"codex", `{"type":"response.output_item.added","item":{"type":"custom_tool_call","name":"exec"}}`, false},
		{"codex", `{"type":"response.output_item.done","item":{"type":"function_call","namespace":"other","name":"read_evidence"}}`, false},
		{"claude", `{"type":"content_block_start","content_block":{"type":"tool_use","name":"mcp__records__list_closes"}}`, true},
		{"claude", `{"type":"content_block_start","content_block":{"type":"tool_use","name":"Bash"}}`, false},
		{"claude", `{"type":"message","content":[{"type":"tool_use","name":"Write"}]}`, false},
	} {
		for _, raw := range []string{tc.raw, "data: " + tc.raw + "\n\n", "data:" + tc.raw + "\n\n"} {
			if got := providerToolsAllowed(tc.agent, []byte(raw), false); got != tc.researchOK {
				t.Fatalf("research %s: %t", tc.raw, got)
			}
			if providerToolsAllowed(tc.agent, []byte(raw), true) {
				t.Fatal("completion accepted executable call", tc.raw)
			}
		}
	}
	for _, raw := range []string{"", "garbage", "data: not-json\n\n", "unexpected\ndata: {\"type\":\"message\"}\n\n"} {
		if providerToolsAllowed("codex", []byte(raw), true) {
			t.Fatal("accepted invalid transport")
		}
	}
	// Text containing a tool-shaped object remains ordinary data.
	if !providerToolsAllowed("codex", []byte(`{"type":"message","content":[{"type":"output_text","text":"{\"type\":\"function_call\"}"}]}`), true) {
		t.Fatal("rejected literal output text")
	}
}
