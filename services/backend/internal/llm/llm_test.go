package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenAIAndPurposeRouting(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["model"] != "small-model" || payload["store"] != false || r.Header.Get("Authorization") != "Bearer private-key" {
			t.Error("wrong routing, retention or credentials")
		}
		fmt.Fprint(w, `{"id":"response-1","status":"completed","model":"small-model-version","usage":{"input_tokens":10,"output_tokens":5},"output":[{"type":"message","content":[{"type":"output_text","text":"answer"}]},{"type":"function_call","call_id":"call-1","name":"read","arguments":"{}"}]}`)
	}))
	defer server.Close()
	c := Client{routes: map[string]route{"classify": {Provider: OpenAI, URL: server.URL, Token: "private-key", Model: "small-model"}}}
	result, err := c.Generate(context.Background(), Request{Purpose: "classify", Instructions: "Classify", Data: map[string]string{"text": "invoice"}, MaxOutputTokens: 100})
	if err != nil || result.Text != "answer" || result.Model != "small-model-version" || result.Usage.Input != 10 || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "read" || calls != 1 {
		t.Fatalf("%+v %v calls=%d", result, err, calls)
	}
}

func TestOpenAIFailuresAndStreaming(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		stream     bool
		limit      int64
		want       error
	}{
		{"refused", `{"status":"completed","output":[{"content":[{"type":"refusal"}]}]}`, false, 1024, ErrRefused},
		{"partial", `{"status":"incomplete"}`, false, 1024, ErrIncomplete},
		{"oversized", strings.Repeat("x", 65), false, 64, ErrLimit},
		{"trailing JSON", `{"status":"completed"} {}`, false, 1024, ErrIncomplete},
		{"stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true, 1024, nil},
		{"truncated stream", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", true, 1024, ErrIncomplete},
		{"stream refusal", "data: {\"type\":\"response.refusal.done\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", true, 1024, ErrRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			c := Client{routes: map[string]route{"test": {Provider: OpenAI, URL: server.URL, Token: "key", Model: "model"}}}
			in := Request{Purpose: "test", Instructions: "test", MaxOutputTokens: 100, MaxResponseBytes: tc.limit}
			got := ""
			if tc.stream {
				in.OnText = func(delta string) bool { got += delta; return true }
			}
			out, err := c.Generate(context.Background(), in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if err == nil && (out.Text != "hello" || got != "hello") {
				t.Fatalf("stream mismatch: %+v %q", out, got)
			}
		})
	}
}

func TestContextMachineContract(t *testing.T) {
	scope := Scope{Organization: "firm-a", Subject: "member-a", AccessProfile: "firm-a-member-a-readonly"}
	calls := 0
	wrongScope := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var in struct {
			Version       int       `json:"version"`
			Instructions  string    `json:"instructions"`
			Scope         Scope     `json:"scope"`
			ReadOnly      bool      `json:"read_only"`
			Store         bool      `json:"store"`
			Deadline      time.Time `json:"deadline"`
			MaxModelCalls int       `json:"max_model_calls"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Scope != scope || !in.ReadOnly || in.Store || in.MaxModelCalls != 4 || in.Deadline.IsZero() || in.Version != 1 || r.Header.Get("Authorization") != "Bearer service-key" {
			t.Error("invalid context machine contract")
		}
		returned := scope
		if wrongScope {
			returned.Organization = "firm-b"
		}
		json.NewEncoder(w).Encode(map[string]any{"version": 1, "status": "completed", "scope": returned, "id": "run-1", "model": "hosted-cli-model", "text": "Research findings", "model_calls": 3, "usage": Usage{Input: 20, Output: 10}})
	}))
	defer server.Close()
	c := Client{routes: map[string]route{"research": {Provider: ContextMachine, URL: server.URL, Token: "service-key", Model: "research-agent"}}}
	in := Request{Purpose: "research", Instructions: "Investigate this variance", Data: map[string]string{"close_id": "1"}, Scope: &scope, MaxOutputTokens: 2000, MaxModelCalls: 4}
	result, err := c.Generate(context.Background(), in)
	if err != nil || result.Provider != ContextMachine || result.ModelCalls != 3 || result.Text != "Research findings" {
		t.Fatalf("%+v %v", result, err)
	}
	wrongScope = true
	if _, err = c.Generate(context.Background(), in); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("cross-firm result accepted: %v", err)
	}
	for _, change := range []func(*Request){func(r *Request) { r.Scope = nil }, func(r *Request) { r.Schema = &OutputSchema{Name: "test"} }, func(r *Request) { r.MaxModelCalls = 0 }, func(r *Request) { r.Tools = []Tool{{Name: "write"}} }} {
		bad := in
		change(&bad)
		if _, err = c.Generate(context.Background(), bad); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if calls != 2 {
		t.Fatalf("invalid requests reached service: %d", calls)
	}
}

func TestNoRedirectRetryOrErrorBodyLeak(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Location", "/leak")
		w.WriteHeader(http.StatusTemporaryRedirect)
		fmt.Fprint(w, "private-data")
	}))
	defer server.Close()
	c := Client{routes: map[string]route{"test": {Provider: OpenAI, URL: server.URL, Token: "key", Model: "model"}}}
	_, err := c.Generate(context.Background(), Request{Purpose: "test", Instructions: "secret", MaxOutputTokens: 10})
	if err == nil || strings.Contains(err.Error(), "private-data") || calls != 1 {
		t.Fatalf("%v calls=%d", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.Generate(ctx, Request{Purpose: "test", Instructions: "test", MaxOutputTokens: 10})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancellation: %v calls=%d", err, calls)
	}
}

func TestStreamConsumerCancellation(t *testing.T) {
	in := Request{MaxResponseBytes: 1024, OnText: func(string) bool { return false }}
	_, err := readStream(strings.NewReader("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"), "model", in)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

// Keep provider endpoints out of feature code. Provider additions belong here.
func TestModelEndpointsStayInGateway(t *testing.T) {
	root, err := filepath.Abs("../..")
	gateway, _ := os.Getwd()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && filepath.Clean(path) == filepath.Clean(gateway) {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, endpoint := range []string{"api.openai.com", "api.anthropic.com", "generativelanguage.googleapis.com", `os.Getenv("OPENAI_`, `os.Getenv("CONTEXT_MACHINE_`, "GenerateOpenAI"} {
			if strings.Contains(string(raw), endpoint) {
				t.Errorf("direct model endpoint in %s", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
