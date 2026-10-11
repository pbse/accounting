package machine

import (
	"bufio"
	"bytes"
	"close.local/backend/internal/llm"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
)

// The subprocess has only a per-run proxy token. Provider credentials never
// enter its environment, prompts, config files or tool results.
type modelProxy struct {
	ctx                      context.Context
	token, key, model, agent string
	effort, authMode         string
	limit, tokens            int
	calls                    atomic.Int32
	client                   *http.Client
	personal                 *codexAuthSession
	completion               bool
	images                   []llm.Image
	schema                   json.RawMessage
	researchTools            []any
	refused                  atomic.Bool
	upstreamStatus           atomic.Int32
	invalidResponse          atomic.Bool
}

func (p *modelProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, endpoint := "/v1/responses", "https://api.openai.com/v1/responses"
	if p.agent == "claude" {
		path, endpoint = "/v1/messages", "https://api.anthropic.com/v1/messages"
	}
	if r.Method != "POST" || r.URL.Path != path {
		http.NotFound(w, r)
		return
	}
	authorized := r.Header.Get("Authorization") == "Bearer "+p.token || r.Header.Get("X-Api-Key") == p.token
	if p.personal != nil {
		authorized = r.Header.Get("X-Context-Machine-Run") == p.token
	}
	if !authorized {
		http.Error(w, "unauthorized", 401)
		return
	}
	var body map[string]any
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if dec.Decode(&body) != nil || body == nil {
		http.Error(w, "invalid request", 400)
		return
	}
	body["model"] = p.model
	if p.agent == "claude" {
		body["max_tokens"] = p.tokens
		output, _ := body["output_config"].(map[string]any)
		if output == nil {
			output = map[string]any{}
		}
		output["effort"] = p.effort
		body["output_config"] = output
		// Adaptive thinking respects max_tokens; a CLI's fixed thinking budget may exceed it.
		body["thinking"] = map[string]any{"type": "adaptive"}
	} else {
		body["max_output_tokens"] = p.tokens
		body["store"] = false
		reasoning, _ := body["reasoning"].(map[string]any)
		if reasoning == nil {
			reasoning = map[string]any{}
		}
		reasoning["effort"] = p.effort
		body["reasoning"] = reasoning
		if p.authMode == "subscription" || p.authMode == "chatgpt" {
			endpoint = "https://chatgpt.com/backend-api/codex/responses"
			// Codex subscription transport does not accept max_output_tokens. The
			// response byte cap, call count and run deadline still apply.
			delete(body, "max_output_tokens")
			body["stream"] = true
		}
		delete(body, "previous_response_id")
	}
	// Only our two scoped MCP tools are advertised. Built-in shell, filesystem,
	// web, subagents, and arbitrary remote MCP tools are never exposed to a model.
	body["tools"] = readTools(body["tools"])
	if p.researchTools != nil {
		body["tools"] = p.researchTools
	}
	// New Codex models put tool definitions inside Responses input items.
	// Remove every such carrier; only the trusted top-level policy may expose tools.
	input, ok := body["input"].([]any)
	if ok {
		filtered := []any{}
		for _, value := range input {
			item, _ := value.(map[string]any)
			kind, _ := item["type"].(string)
			if kind == "additional_tools" || kind == "configuration_update" || kind == "tool_search_output" {
				continue
			}
			filtered = append(filtered, value)
		}
		body["input"] = filtered
	}
	if p.completion {
		body["tools"] = []any{}
		if len(p.schema) > 0 {
			if p.agent == "claude" {
				body["output_config"].(map[string]any)["format"] = map[string]any{"type": "json_schema", "schema": p.schema}
			} else {
				body["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": "completion", "strict": true, "schema": p.schema}}
			}
		}
	}

	if len(p.images) > 0 {
		if !p.completion {
			http.Error(w, "invalid images", 400)
			return
		}
		if err := attachImages(body, p.agent, p.images); err != nil {
			http.Error(w, "invalid images", 400)
			return
		}
	}
	delete(body, "tool_choice")
	if int(p.calls.Add(1)) > p.limit {
		p.calls.Add(-1)
		http.Error(w, "model call budget exhausted", 429)
		return
	}
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	req, err := http.NewRequestWithContext(p.ctx, "POST", endpoint, bytes.NewReader(raw))
	if err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Preserve CLI protocol negotiation/identity, never caller credentials or host.
	for _, name := range []string{"User-Agent", "OpenAI-Beta", "X-App"} {
		if value := r.Header.Get(name); value != "" {
			req.Header.Set(name, value)
		}
	}
	if p.agent == "claude" {
		if p.authMode == "subscription" {
			req.Header.Set("Authorization", "Bearer "+p.key)
		} else {
			req.Header.Set("X-Api-Key", p.key)
		}
		req.Header.Set("Anthropic-Version", "2023-06-01")
		beta := r.Header.Get("Anthropic-Beta")
		if p.authMode == "subscription" && !strings.Contains(beta, "oauth-2025-04-20") {
			if beta != "" {
				beta += ","
			}
			beta += "oauth-2025-04-20"
		}
		if beta != "" {
			req.Header.Set("Anthropic-Beta", beta)
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+p.key)
		if p.personal != nil {
			auth, err := p.personal.read()
			if err != nil || p.personal.save(false) != nil {
				http.Error(w, "credential storage unavailable", 503)
				return
			}
			req.Header.Set("Authorization", "Bearer "+auth.Tokens.Access)
			req.Header.Set("ChatGPT-Account-Id", auth.Tokens.Account)
		}
		if p.authMode == "subscription" || p.authMode == "chatgpt" {
			req.Header.Set("Originator", "codex_cli_rs")
		}
	}
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Clear an earlier outcome only when a new upstream call actually starts.
	// A local budget/policy refusal must not overwrite the provider failure.
	p.upstreamStatus.Store(0)
	p.invalidResponse.Store(false)
	res, err := client.Do(req)
	if err != nil {
		http.Error(w, "model unavailable", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		p.upstreamStatus.Store(int32(res.StatusCode))
		http.Error(w, "model request failed", res.StatusCode)
		return
	}
	// Validate a whole bounded provider turn before the CLI can execute a call.
	// Research text is surfaced after the CLI turn, so buffering does not change its callback contract.
	raw, err = io.ReadAll(io.LimitReader(res.Body, (4<<20)+1))
	if err != nil || len(raw) > 4<<20 || !providerToolsAllowed(p.agent, raw, p.completion) {
		code := "tool_policy"
		if err != nil {
			code = "response_read"
		} else if len(raw) > 4<<20 {
			code = "response_limit"
		}
		p.invalidResponse.Store(true)
		log.Printf("context_machine_provider_failure code=%s", code)
		http.Error(w, "invalid model response", 502)
		return
	}
	if p.completion {
		err = completionProviderStatus(p.agent, raw)
		if errors.Is(err, llm.ErrRefused) {
			p.refused.Store(true)
		}
		if err != nil {
			p.invalidResponse.Store(true)
			log.Print("context_machine_provider_failure code=incomplete")
			http.Error(w, "incomplete model response", 502)
			return
		}
	}
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	w.Write(raw)
}

// Codex uses Responses namespaces; Claude uses flat MCP function names.
func readTools(value any) []any {
	allowed := []any{}
	tools, _ := value.([]any)
	for _, value := range tools {
		tool, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tool["name"].(string)
		if name == "mcp__records__list_closes" || name == "mcp__records__read_evidence" {
			if tool["type"] == nil || tool["type"] == "function" {
				allowed = append(allowed, tool)
			}
		} else if name == "mcp__records" && tool["type"] == "namespace" {
			children := []any{}
			nested, _ := tool["tools"].([]any)
			for _, v := range nested {
				t, ok := v.(map[string]any)
				if ok && t["type"] == "function" && (t["name"] == "list_closes" || t["name"] == "read_evidence") {
					children = append(children, t)
				}
			}
			if len(children) > 0 {
				tool["tools"] = children
				allowed = append(allowed, tool)
			}
		}
	}
	return allowed
}

// Inspect terminal provider status before passing a completion to the CLI. A
// syntactically valid JSON fragment can still be a truncated or refused result.
func completionProviderStatus(agent string, raw []byte) error {
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 4<<20)
	complete, stopped := false, false
	for scan.Scan() {
		data, ok := strings.CutPrefix(scan.Text(), "data:")
		data = strings.TrimSpace(data)
		if !ok || data == "[DONE]" {
			continue
		}
		var e struct {
			Type     string
			Response struct{ Status string }
			Delta    json.RawMessage
		}
		if json.Unmarshal([]byte(data), &e) != nil {
			return llm.ErrIncomplete
		}
		var delta struct {
			StopReason string `json:"stop_reason"`
		}
		if agent == "claude" && e.Type == "message_delta" && json.Unmarshal(e.Delta, &delta) != nil {
			return llm.ErrIncomplete
		}
		if strings.HasPrefix(e.Type, "response.refusal.") || delta.StopReason == "refusal" {
			return llm.ErrRefused
		}
		if e.Type == "error" || e.Type == "response.incomplete" || e.Type == "response.failed" || delta.StopReason == "max_tokens" {
			return llm.ErrIncomplete
		}
		if agent == "codex" && e.Type == "response.completed" {
			if e.Response.Status != "completed" || complete {
				return llm.ErrIncomplete
			}
			complete = true
		}
		if agent == "claude" {
			if e.Type == "message_delta" {
				stopped = delta.StopReason == "end_turn" || delta.StopReason == "stop_sequence"
			}
			if e.Type == "message_stop" {
				if !stopped || complete {
					return llm.ErrIncomplete
				}
				complete = true
			}
		}
	}
	if scan.Err() != nil || !complete {
		return llm.ErrIncomplete
	}
	return nil
}

// The parent attaches validated pages directly to the provider request. Images
// never enter CLI prompts, tool results or a file accessible to the agent.
func attachImages(body map[string]any, agent string, images []llm.Image) error {
	key := "input"
	if agent == "claude" {
		key = "messages"
	}
	input, ok := body[key].([]any)
	if !ok {
		return llm.ErrIncomplete
	}
	parts := []any{}
	for _, image := range images {
		if agent == "claude" {
			parts = append(parts, map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": "image/png", "data": base64.StdEncoding.EncodeToString(image.PNG)}})
		} else {
			parts = append(parts, llm.OpenAIImage(image))
		}
	}
	body[key] = append(input, map[string]any{"role": "user", "content": parts})
	return nil
}

// Tool calls are never executable in completion mode. Research may invoke only
// the two scoped record tools, even if a model hallucinates a hidden CLI tool.
func providerToolsAllowed(agent string, raw []byte, completion bool) bool {
	var check func(any) bool
	check = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			kind, _ := value["type"].(string)
			if strings.HasSuffix(kind, "_call") || strings.HasSuffix(kind, "tool_use") {
				if completion {
					return false
				}
				name, _ := value["name"].(string)
				namespace, _ := value["namespace"].(string)
				allowed := agent == "codex" && kind == "function_call" && namespace == "mcp__records" && (name == "list_closes" || name == "read_evidence")
				allowed = allowed || (namespace == "" && (kind == "function_call" || agent == "claude" && kind == "tool_use") && (name == "mcp__records__list_closes" || name == "mcp__records__read_evidence"))
				if !allowed {
					return false
				}
			}
			if kind == "additional_tools" || kind == "tool_search_output" {
				return false
			}
			for _, child := range value {
				if !check(child) {
					return false
				}
			}
		case []any:
			for _, child := range value {
				if !check(child) {
					return false
				}
			}
		}
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) == nil {
		return check(value)
	}
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 4<<20)
	seen := false
	for scan.Scan() {
		data, ok := strings.CutPrefix(scan.Text(), "data:")
		data = strings.TrimSpace(data)
		if !ok {
			line := scan.Text()
			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") || strings.HasPrefix(line, "id:") || strings.HasPrefix(line, "retry:") {
				continue
			}
			return false
		}
		if data == "[DONE]" {
			continue
		}
		seen = true
		if json.Unmarshal([]byte(data), &value) != nil || !check(value) {
			return false
		}
	}
	return seen && scan.Err() == nil
}
