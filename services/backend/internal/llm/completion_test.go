package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEveryPurposeCanSwitchBetweenAPIAndContextMachine(t *testing.T) {
	scope := Scope{Organization: "owner-org", Subject: "owner", AccessProfile: ""}
	for _, provider := range []string{"api", "context_machine"} {
		calls := 0
		cfg := Config{APIImages: true, Provider: provider, APIKey: "api-key", Model: "api-model", ContextMachineToken: "service-token", ContextMachineModel: "codex", CodexModel: "cli-model", AllowUncappedOutputTokens: true}
		cfg.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if provider == "api" {
				if r.URL.Host != "api.openai.com" || r.Header.Get("Authorization") != "Bearer api-key" {
					t.Fatal("API route")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"api-id","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{}"}]}]}`))}, nil
			}
			if r.URL.String() != "http://context-machine.internal/v1/complete" || r.Header.Get("Authorization") != "Bearer service-token" {
				t.Fatal("private completion route")
			}
			var in CompletionRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Agent != "codex" || in.Model != "cli-model" || in.Effort != "medium" || !in.Request.AllowUncappedOutputTokens || in.Request.Schema == nil {
				t.Fatal("lost completion contract")
			}
			raw, _ := json.Marshal(CompletionResponse{Version: 1, Status: "completed", Scope: in.Scope, Response: Response{ID: "run", Model: "cli-model", ReasoningEffort: "medium", AuthMode: "chatgpt", Text: "{}", ModelCalls: 1}})
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})}
		client, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		ctx := WithIdentity(WithClient(context.Background(), client), scope.Organization, scope.Subject)
		for _, purpose := range purposes {
			in := Request{Purpose: purpose, Instructions: "Complete", Data: "authorized data", Schema: &OutputSchema{Name: "result", Definition: json.RawMessage(`{"type":"object"}`)}, MaxOutputTokens: 100}
			if purpose == "ocr" {
				in.Images = []Image{{PNG: pagePNG(t)}}
			}
			out, err := Generate(ctx, in)
			if err != nil || out.Text != "{}" || string(out.Provider) != map[string]string{"api": "openai", "context_machine": "context_machine"}[provider] {
				t.Fatal(purpose, out, err)
			}
		}
		if calls != len(purposes) {
			t.Fatal("unexpected extra calls", calls)
		}
		if provider == "context_machine" {
			// Organization is provenance only. No identity/profile is required for inference.
			for _, caller := range []context.Context{WithIdentity(ctx, "other-org", "other-user"), WithClient(context.Background(), client)} {
				_, err = Generate(caller, Request{Purpose: "preparation", Instructions: "Read", Schema: &OutputSchema{Name: "result", Definition: json.RawMessage(`{"type":"object"}`)}, MaxOutputTokens: 100})
				if err != nil {
					t.Fatal("cross-firm/unscoped inference refused", err)
				}
			}
			// Failure must not trigger a direct API fallback.
			client.http = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("secret"))}, nil
			})}
			_, err = Generate(ctx, Request{Purpose: "preparation", Instructions: "No", MaxOutputTokens: 10})
			if err == nil || calls != len(purposes)+3 || strings.Contains(err.Error(), "secret") {
				t.Fatal("fallback or leak", err)
			}
			if Model(ctx, "preparation") != "context_machine/codex/cli-model" || Engine(ctx, "preparation") != "context_machine" {
				t.Fatal("route missing from provenance")
			}
		}
	}
}

func TestCompletionToolContinuationAndCallback(t *testing.T) {
	scope := Scope{Organization: "org", Subject: "user", AccessProfile: ""}
	requests := 0
	cfg := Config{Provider: "context_machine", ContextMachineToken: "private", ContextMachineModel: "claude", ClaudeModel: "fixed", Profiles: nil}
	cfg.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		var in CompletionRequest
		json.NewDecoder(r.Body).Decode(&in)
		out := Response{ID: "run", Model: "fixed", ReasoningEffort: "medium", AuthMode: "subscription", ModelCalls: 1}
		if requests == 1 {
			out.ToolCalls = []ToolCall{{CallID: "call", Name: "read", Arguments: "{}"}}
		} else {
			if len(in.Request.Messages) != 3 || in.Request.Messages[1].Call.CallID != "call" || in.Request.Messages[2].Result.Output != "authorized evidence" {
				t.Fatal("lost tool continuation")
			}
			out.Text = "answer"
		}
		raw, _ := json.Marshal(CompletionResponse{Version: 1, Status: "completed", Scope: scope, Response: out})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
	})}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithIdentity(WithClient(context.Background(), c), "org", "user")
	in := Request{Purpose: "investigation", Messages: []Message{{Role: "user", Content: "Read"}}, Tools: []Tool{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}, ToolChoice: "auto", MaxOutputTokens: 100}
	out, err := Generate(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Messages = append(in.Messages, out.Continuation...)
	in.Messages = append(in.Messages, Message{Result: &ToolResult{CallID: "call", Output: "authorized evidence"}})
	out, err = Generate(ctx, in)
	if err != nil || out.Text != "answer" {
		t.Fatal(out, err)
	}
	in.Tools = nil
	in.OnText = func(s string) bool { return false }
	_, err = Generate(ctx, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if ValidateCompletion(Response{ID: "id", Model: "m", ModelCalls: 1, ToolCalls: []ToolCall{{CallID: "call", Name: "write", Arguments: "{}"}}}, in) == nil {
		t.Fatal("unadvertised capability accepted")
	}
}

func TestContextMachineDoesNotRequireUnusedAPICredentials(t *testing.T) {
	cfg := Config{Provider: "context_machine", APIKey: "only-for-service", ContextMachineToken: "private", ContextMachineModel: "codex", CodexModel: "fixed", Profiles: nil}
	if _, err := New(cfg); err != nil {
		t.Fatal(err)
	}
}
