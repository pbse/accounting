package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStartupConfigurationAndInjection(t *testing.T) {
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_MODEL", "LLM_MODELS", "CONTEXT_MACHINE_TOKEN", "CONTEXT_MACHINE_MODEL", "CONTEXT_MACHINE_ENDPOINT"} {
		t.Setenv(name, "")
	}
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("OPENAI_MODEL", "default-model")
	t.Setenv("LLM_MODELS", `{"preparation":"careful-model"}`)
	t.Setenv("CONTEXT_MACHINE_TOKEN", "internal-key")
	t.Setenv("CONTEXT_MACHINE_MODEL", "codex")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.routes["research"].URL != contextMachineEndpoint {
		t.Fatal("service discovery default missing")
	}
	ctx := WithClient(context.Background(), c)
	t.Setenv("OPENAI_MODEL", "changed-after-start")
	if Model(ctx, "preparation") != "careful-model" || Model(ctx, "statement") != "default-model" || Model(ctx, "research") != "codex" {
		t.Fatal("routes changed after startup")
	}
	if Model(context.Background(), "preparation") != "" {
		t.Fatal("ambient model config")
	}
	if _, err := Generate(context.Background(), Request{Purpose: "preparation"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := Generate(ctx, Request{Purpose: "unknown"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	type authKey struct{}
	base, cancel := context.WithCancel(context.WithValue(context.Background(), authKey{}, "member"))
	defer cancel()
	h := c.Bind(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Model(r.Context(), "preparation") != "careful-model" || r.Context().Value(authKey{}) != "member" {
			t.Error("middleware lost dependency or authority")
		}
		cancel()
		if !errors.Is(r.Context().Err(), context.Canceled) {
			t.Error("middleware lost cancellation")
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(base))
}

func TestInvalidConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{APIKey: "key"}, {Model: "model"}, {ContextMachineToken: "key"}, {ContextMachineModel: "model"},
		{ContextMachineEndpoint: "https://elsewhere"},
		{APIKey: "key", Model: "model", Models: map[string]string{"unknown": "x"}},
		{APIKey: "key", Model: "model", Models: map[string]string{"preparation": ""}},
		{Models: map[string]string{"preparation": "model"}},
		{ContextMachineToken: "key", ContextMachineModel: "model"},
		{ContextMachineToken: "key", ContextMachineModel: "codex", ContextMachineEndpoint: "http://remote"},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	disabled, err := New(Config{})
	if err != nil || len(disabled.routes) != 0 {
		t.Fatal("optional models should remain disabled")
	}
	for _, raw := range []string{`{"preparation":123}`, `null`, `[]`} {
		t.Setenv("LLM_MODELS", raw)
		if _, err := FromEnv(); err == nil {
			t.Fatal("malformed model config accepted")
		}
	}
	models := map[string]string{"preparation": "selected"}
	c, err := New(Config{APIKey: "key", Model: "default", Models: models})
	if err != nil {
		t.Fatal(err)
	}
	models["preparation"] = "mutated"
	if c.routes["preparation"].Model != "selected" {
		t.Fatal("config map was not copied")
	}
}

func TestServiceDiscoveryAndStreaming(t *testing.T) {
	scope := Scope{Organization: "firm", Subject: "member", AccessProfile: "read-only"}
	finished := machineResponse{Version: 1, Status: "completed", Scope: scope, Response: Response{ID: "run", Model: "actual", Text: "Finding", ModelCalls: 2}}
	terminal, _ := json.Marshal(map[string]any{"type": "completed", "scope": scope, "response": finished})
	cfg := Config{ContextMachineToken: "private", ContextMachineModel: "claude", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "context-machine.internal" || r.URL.Path != "/v1/run" {
			t.Fatal("feature needed an endpoint")
		}
		var payload map[string]any
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload["stream"] != true || payload["model"] != "claude" || payload["allow_uncapped_output_tokens"] != true {
			t.Fatal("stream was not requested")
		}
		body := `{"scope":{"organization":"firm","subject":"member","access_profile":"read-only"},"type":"progress","text":"Reading evidence"}` + "\n" + `{"scope":{"organization":"firm","subject":"member","access_profile":"read-only"},"type":"text_delta","text":"Finding"}` + "\n" + string(terminal) + "\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/x-ndjson"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var progress, text string
	out, err := Generate(WithClient(context.Background(), c), Request{Purpose: "research", Instructions: "Investigate", AllowUncappedOutputTokens: true, Scope: &scope, MaxModelCalls: 3, MaxOutputTokens: 100, OnProgress: func(s string) bool { progress += s; return true }, OnText: func(s string) bool { text += s; return true }})
	if err != nil || out.ID != "run" || out.Provider != ContextMachine || text != "Finding" || progress != "Reading evidence" {
		t.Fatalf("%+v %v %q %q", out, err, text, progress)
	}
}

func TestMachineStreamFailures(t *testing.T) {
	scope := Scope{Organization: "firm", Subject: "member", AccessProfile: "read-only"}
	for _, tc := range []struct {
		name, body string
		cancel     bool
		want       error
	}{
		{"partial", `{"type":"text_delta","text":"partial"}` + "\n", false, ErrIncomplete},
		{"malformed", "not-json\n", false, ErrIncomplete},
		{"unknown", `{"type":"execute","text":"write"}` + "\n", false, ErrIncomplete},
		{"refusal", `{"type":"refused"}` + "\n", false, ErrRefused},
		{"error", `{"type":"error","text":"secret"}` + "\n", false, ErrIncomplete},
		{"cancel", `{"type":"text_delta","text":"x"}` + "\n", true, context.Canceled},
		{"cancel progress", `{"type":"progress","text":"x"}` + "\n", true, context.Canceled},
		{"oversized", strings.Repeat("x", 4097), false, ErrLimit},
		{"scope", `{"type":"completed","response":{"version":1,"status":"completed","scope":{"organization":"foreign"},"id":"run","model":"actual","text":"x","model_calls":1}}` + "\n", false, ErrIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readMachineStream(strings.NewReader(strings.ReplaceAll(tc.body, `"type":`, `"scope":{"organization":"firm","subject":"member","access_profile":"read-only"},"type":`)), Request{Scope: &scope, MaxResponseBytes: 4096, MaxModelCalls: 3, OnText: func(string) bool { return !tc.cancel }, OnProgress: func(string) bool { return !tc.cancel }})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}

func TestTypedConversationAndSchemaContract(t *testing.T) {
	requests := 0
	c, err := New(Config{APIKey: "key", Model: "default", Models: map[string]string{"investigation": "reader"}, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		requests++
		var payload struct {
			Model string
			Store bool
			Input []map[string]any
			Tools []map[string]any
			Text  struct {
				Format struct {
					Name   string
					Strict bool
				}
			}
		}
		if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Model != "reader" || payload.Store || len(payload.Input) != 3 || len(payload.Tools) != 1 || payload.Tools[0]["type"] != "function" || payload.Text.Format.Name != "answer" || !payload.Text.Format.Strict {
			t.Fatal("lost model, tool or schema contract")
		}
		if payload.Input[1]["type"] != "function_call" || payload.Input[2]["type"] != "function_call_output" || payload.Input[2]["call_id"] != "call" || payload.Input[2]["output"] != "evidence" {
			t.Fatal("tool conversation lost identity")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"content":[{"type":"output_text","text":"{}"}]}]}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	in := Request{Purpose: "investigation", MaxOutputTokens: 100, Messages: []Message{{Role: "user", Content: "Read"}, {Call: &ToolCall{CallID: "call", Name: "read", Arguments: "{}"}}, {Result: &ToolResult{CallID: "call", Output: "evidence"}}}, Tools: []Tool{{Name: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}, Schema: &OutputSchema{Name: "answer", Definition: json.RawMessage(`{"type":"object"}`)}}
	if _, err := c.Generate(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	// An ambiguous message must fail locally, never silently lose one variant.
	in.Messages[0].Call = &ToolCall{CallID: "unexpected"}
	if _, err := c.Generate(context.Background(), in); err == nil || requests != 1 {
		t.Fatal("ambiguous message dispatched")
	}
}

func TestResearchResultsFailClosed(t *testing.T) {
	scope := Scope{Organization: "firm", Subject: "member", AccessProfile: "read-only"}
	in := Request{Scope: &scope, MaxModelCalls: 3, MaxResponseBytes: 4096}
	valid := machineResponse{Version: 1, Status: "completed", Scope: scope, Response: Response{ID: "run", Model: "actual", Text: "x", ModelCalls: 1}}
	for _, change := range []func(*machineResponse){
		func(r *machineResponse) { r.Status = "incomplete" }, func(r *machineResponse) { r.ID = "" }, func(r *machineResponse) { r.Model = "" },
		func(r *machineResponse) { r.ModelCalls = 4 }, func(r *machineResponse) { r.ModelCalls = 0 }, func(r *machineResponse) { r.Usage.Input = -1 },
		func(r *machineResponse) { r.ToolCalls = []ToolCall{{Name: "write"}} },
	} {
		bad := valid
		change(&bad)
		if _, err := validateMachineResponse(bad, in); !errors.Is(err, ErrIncomplete) {
			t.Fatal("invalid research accepted", err)
		}
	}
	refused := valid
	refused.Status = "refused"
	if _, err := validateMachineResponse(refused, in); !errors.Is(err, ErrRefused) {
		t.Fatal(err)
	}
	delta, _ := json.Marshal(map[string]any{"type": "text_delta", "scope": scope, "text": "different"})
	terminal, _ := json.Marshal(map[string]any{"type": "completed", "scope": scope, "response": valid})
	if _, err := readMachineStream(strings.NewReader(string(delta)+"\n"+string(terminal)+"\n"), in); !errors.Is(err, ErrIncomplete) {
		t.Fatal("different final text accepted", err)
	}
	// Reject a foreign scope before exposing any provisional text or progress.
	shown := false
	in.OnText = func(string) bool { shown = true; return true }
	foreignScope := scope
	foreignScope.Organization = "foreign"
	foreign, _ := json.Marshal(map[string]any{"type": "text_delta", "scope": foreignScope, "text": "private"})
	if _, err := readMachineStream(strings.NewReader(string(foreign)+"\n"), in); !errors.Is(err, ErrIncomplete) || shown {
		t.Fatal("foreign stream reached the consumer")
	}
}

func TestPrivateMachineReadiness(t *testing.T) {
	for _, body := range []string{`{"status":"ok","agents":["codex","claude"]}`, `{"status":"ok","agents":["claude"]}`} {
		c, err := New(Config{ContextMachineToken: "private", ContextMachineModel: "codex", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "http://context-machine.internal/readyz" || r.Header.Get("Authorization") != "Bearer private" {
				t.Fatal("readiness bypassed binding or authentication")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		err = CheckContextMachine(WithClient(context.Background(), c))
		if (err == nil) != strings.Contains(body, "codex") {
			t.Fatal("readiness failed to verify both agents", err)
		}
	}
}

func TestReadinessCoalescesAndCachesSuccessAndFailure(t *testing.T) {
	for _, status := range []int{200, 503} {
		var calls atomic.Int32
		c, err := New(Config{ContextMachineToken: "private", ContextMachineModel: "codex", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			time.Sleep(10 * time.Millisecond)
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"status":"ok","agents":["codex","claude"]}`))}, nil
		})}})
		if err != nil {
			t.Fatal(err)
		}
		ctx := WithClient(context.Background(), c)
		var wg sync.WaitGroup
		for range 20 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := CheckContextMachine(ctx); (err == nil) != (status == 200) {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		if calls.Load() != 1 {
			t.Fatal("concurrent probes were not coalesced", calls.Load())
		}
		c.readinessExpires = time.Now().Add(-time.Second)
		_ = CheckContextMachine(ctx)
		if calls.Load() != 2 {
			t.Fatal("expired readiness was not refreshed")
		}
	}
}

func TestReadinessRecoveryWithinDeploymentRetries(t *testing.T) {
	for _, workflow := range []string{"deploy-production.yml", "deploy.yml"} {
		t.Run(workflow, func(t *testing.T) {
			raw, err := os.ReadFile("../../../../.github/workflows/" + workflow)
			if err != nil {
				t.Fatal(err)
			}
			args := regexp.MustCompile(`--retry (\d+) --retry-delay (\d+)`).FindStringSubmatch(string(raw))
			if len(args) != 3 {
				t.Fatal("readiness retry policy missing")
			}
			retries, _ := strconv.Atoi(args[1])
			seconds, _ := strconv.Atoi(args[2])
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				c, err := New(Config{ContextMachineToken: "private", ContextMachineModel: "codex", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					calls++
					status := 200
					if calls == 1 {
						time.Sleep(20 * time.Second)
						status = 503
					}
					return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"status":"ok","agents":["codex","claude"]}`))}, nil
				})}})
				if err != nil {
					t.Fatal(err)
				}
				ctx := WithClient(context.Background(), c)
				for attempt := 0; attempt <= retries; attempt++ {
					if CheckContextMachine(ctx) == nil {
						if calls != 2 {
							t.Fatal("unexpected probe count", calls)
						}
						return
					}
					if attempt < retries {
						time.Sleep(time.Duration(seconds) * time.Second)
					}
				}
				t.Fatal("deployment retries ended before cached failure could recover")
			})
		})
	}
}

func TestReadinessCallerCancellationDoesNotPoisonCache(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	c, err := New(Config{ContextMachineToken: "private", ContextMachineModel: "codex", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		cancel()
		if r.Context().Err() != nil {
			t.Error("shared probe inherited caller cancellation")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"ok","agents":["codex","claude"]}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckContextMachine(WithClient(ctx, c)); err != nil {
		t.Fatal(err)
	}
	if err = CheckContextMachine(WithClient(context.Background(), c)); err != nil || calls != 1 {
		t.Fatal("cache poisoned", err, calls)
	}
}

func TestConfiguredReasoningReachesAPI(t *testing.T) {
	c, err := New(Config{APIKey: "key", Model: "model", ReasoningEffort: "low", ReasoningEfforts: map[string]string{"preparation": "high"}, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["reasoning"].(map[string]any)["effort"] != "high" {
			t.Error("purpose effort override missing")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"id","status":"completed","model":"model","output":[]}`))}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.Generate(context.Background(), Request{Purpose: "preparation", Instructions: "test", Data: "test", MaxOutputTokens: 100})
	if err != nil || result.ReasoningEffort != "high" || result.AuthMode != "api_key" {
		t.Fatal(result, err)
	}
	for _, cfg := range []Config{{ReasoningEffort: "wrong"}, {ReasoningEfforts: map[string]string{"research": "high"}}, {APIKey: "key", Model: "model", ReasoningEfforts: map[string]string{"preparation": "wrong"}}} {
		if _, err := New(cfg); err == nil {
			t.Fatal("invalid effort configuration accepted")
		}
	}
}
