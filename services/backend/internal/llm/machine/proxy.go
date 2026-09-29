package machine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
)

// The subprocess has only a per-run proxy token. Provider credentials never
// enter its environment, prompts, config files or tool results.
type modelProxy struct {
	ctx                      context.Context
	token, key, model, agent string
	limit, tokens            int
	calls                    atomic.Int32
	client                   *http.Client
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
	if r.Header.Get("Authorization") != "Bearer "+p.token && r.Header.Get("X-Api-Key") != p.token {
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
	} else {
		body["max_output_tokens"] = p.tokens
		body["store"] = false
		delete(body, "previous_response_id")
	}
	// Only our two scoped MCP tools are advertised. Built-in shell, filesystem,
	// web, subagents, and arbitrary remote MCP tools are never exposed to a model.
	body["tools"] = readTools(body["tools"])

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
	if p.agent == "claude" {
		req.Header.Set("X-Api-Key", p.key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		if beta := r.Header.Get("Anthropic-Beta"); beta != "" {
			req.Header.Set("Anthropic-Beta", beta)
		}
	} else {
		req.Header.Set("Authorization", "Bearer "+p.key)
	}
	client := *p.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		http.Error(w, "model unavailable", 502)
		return
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		http.Error(w, "model request failed", res.StatusCode)
		return
	}
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	// Bound streamed provider output as well as the CLI's final output.
	reader := &io.LimitedReader{R: res.Body, N: 4 << 20}
	buf := make([]byte, 8192)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			if _, e := w.Write(buf[:n]); e != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil || reader.N == 0 {
			return
		}
	}
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
