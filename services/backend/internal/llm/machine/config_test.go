package machine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"close.local/backend/internal/llm"
)

func TestAgentCredentialSelection(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, tc := range []struct {
			mode, key, token, want string
			fail                   bool
		}{
			{"", "key", "token", "api_key", false}, {"auto", "", "token", "subscription", false},
			{"subscription", "key", "token", "subscription", false}, {"api_key", "key", "token", "api_key", false},
			{"subscription", "key", "", "", true}, {"api_key", "", "token", "", true},
			{"other", "key", "token", "", true}, {"auto", "", "", "", false},
		} {
			cfg := Config{CodexModel: "fixed", ClaudeModel: "fixed", OpenAIKey: tc.key, AnthropicKey: tc.key, CodexToken: tc.token, ClaudeToken: tc.token, CodexAuth: tc.mode, ClaudeAuth: tc.mode}
			selected, err := cfg.agent(agent)
			if (err != nil) != tc.fail || selected.mode != tc.want {
				t.Fatalf("%s/%s: %+v %v", agent, tc.mode, selected, err)
			}
			if tc.want != "" && selected.effort != "medium" {
				t.Fatal("missing pinned default effort")
			}
			if tc.want == "subscription" && selected.credential != "token" {
				t.Fatal("selected wrong billing credential")
			}
		}
	}
	disabled, err := (Config{OpenAIKey: "direct-api-key"}).agent("codex")
	if err != nil || disabled.credential != "" {
		t.Fatal("direct API key enabled unconfigured research agent")
	}
	cfg := Config{CodexModel: "fixed", OpenAIKey: "key", CodexEffort: "invalid"}
	if _, err := cfg.agent("codex"); err == nil {
		t.Fatal("invalid effort accepted")
	}
}

func TestUnconfiguredAgentRejectedBeforeDatabaseAccess(t *testing.T) {
	cfg := Config{Token: strings.Repeat("x", 32), DatabaseURL: "postgres://invalid/unused", CodexModel: "fixed", CodexToken: "token", Profiles: []llm.Scope{{Organization: "org_test", Subject: "user_test", AccessProfile: "read"}}}
	h, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/run", strings.NewReader(`{"version":1,"purpose":"research","model":"claude","instructions":"read","scope":{"organization":"org_test","subject":"user_test","access_profile":"read"},"read_only":true,"store":false,"deadline":"2099-01-01T00:00:00Z","max_output_tokens":100,"max_model_calls":1}`))
	r.Header.Set("Authorization", "Bearer "+cfg.Token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "agent unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/v1/run", strings.NewReader(`{"version":1,"purpose":"research","model":"codex","instructions":"read","scope":{"organization":"org_test","subject":"user_test","access_profile":"read"},"read_only":true,"store":false,"deadline":"2099-01-01T00:00:00Z","max_output_tokens":100,"max_model_calls":1}`))
	r.Header.Set("Authorization", "Bearer "+cfg.Token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "allow_uncapped_output_tokens") {
		t.Fatal("subscription silently dropped token limit", w.Code, w.Body.String())
	}

}

func TestProxyPinsReasoningAndSubscriptionCredentials(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		p := &modelProxy{ctx: context.Background(), token: "ephemeral", key: "private-token", model: "fixed", effort: "medium", authMode: "subscription", agent: agent, limit: 1, tokens: 2048}
		calls := 0
		p.client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Header.Get("Authorization") != "Bearer private-token" || r.Header.Get("X-Api-Key") != "" {
				t.Error("subscription headers incorrect")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if agent == "codex" {
				if r.URL.String() != "https://chatgpt.com/backend-api/codex/responses" || body["reasoning"].(map[string]any)["effort"] != "medium" || body["reasoning"].(map[string]any)["summary"] != "auto" || body["max_output_tokens"] != nil {
					t.Error("Codex subscription policy")
				}
			} else if r.URL.Host != "api.anthropic.com" || body["output_config"].(map[string]any)["effort"] != "medium" || body["thinking"].(map[string]any)["type"] != "adaptive" || !strings.Contains(r.Header.Get("Anthropic-Beta"), "oauth-2025-04-20") {
				t.Error("Claude subscription policy")
			}
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("private provider detail"))}, nil
		})}
		path := "/v1/responses"
		if agent == "claude" {
			path = "/v1/messages"
		}
		r := httptest.NewRequest("POST", path, strings.NewReader(`{"reasoning":{"effort":"max","summary":"auto"},"output_config":{"effort":"max"},"thinking":{"type":"enabled","budget_tokens":99999}}`))
		r.Header.Set("Authorization", "Bearer ephemeral")
		w := httptest.NewRecorder()
		p.ServeHTTP(w, r)
		if w.Code != 401 || calls != 1 || strings.Contains(w.Body.String(), "private") {
			t.Fatal("auth failure retried or leaked", w.Code, calls)
		}
	}
}
