// Package llm owns all model network calls. Callers own authorization, budgets,
// redaction and validation of the model's proposals before using them.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"
)

type Provider string

const (
	OpenAI         Provider = "openai"
	ContextMachine Provider = "context_machine"
)

// route is trusted server configuration, never a value supplied by a prompt.
type route struct {
	Provider Provider
	Model    string
	URL      string
	Token    string
	Effort   string
}

// Client owns immutable purpose routing configured at application startup.
// Configure once before sharing it. No automatic retries or provider fallbacks:
// either would bypass the caller's budget reservation or data consent boundary.
type Client struct {
	http             *http.Client
	routes           map[string]route
	readinessMu      sync.Mutex
	readinessExpires time.Time
	readinessErr     error
}

type Request struct {
	Purpose      string
	Instructions string
	Data         any
	// Messages is used for conversations, including tool results. With it set,
	// Instructions and Data must be empty.
	Messages          []Message
	Schema            *OutputSchema
	Tools             []Tool
	ToolChoice        string
	ParallelToolCalls bool
	// Per-call token ceiling unless research explicitly allows an uncapped transport.
	MaxOutputTokens  int
	MaxResponseBytes int64
	// MaxModelCalls bounds the agent's internal loop. The caller reserves this
	// many calls before dispatch; the service must enforce it.
	MaxModelCalls int
	// Research-only opt-in for subscription transports without a provider-side
	// output-token ceiling. Call count, deadline and byte limits still apply.
	AllowUncappedOutputTokens bool
	// OnText receives provisional deltas. A successful return is still required
	// before treating the result as complete. Returning false cancels the call.
	OnText func(string) bool
	// OnProgress receives provisional research progress, never executable actions.
	OnProgress func(string) bool
	// Scope is required for a context machine. It identifies a pre-provisioned,
	// read-only access profile; it does not itself grant database permissions.
	Scope *Scope
}

// OutputSchema requests strict JSON output; callers still validate business rules.
type OutputSchema struct {
	Name       string
	Definition json.RawMessage
}

type Scope struct {
	Organization  string `json:"organization"`
	Subject       string `json:"subject"`
	AccessProfile string `json:"access_profile"`
}

type Usage struct {
	Input  int `json:"input_tokens"`
	Output int `json:"output_tokens"`
}

// Message contains role/content, a Call, a Result, or opaque gateway continuation state.
type Message struct {
	Role         string
	Content      string
	Call         *ToolCall
	Result       *ToolResult
	providerItem json.RawMessage
}

type ToolResult struct{ CallID, Output string }

type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Strict      bool
}

type ToolCall struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Response struct {
	// Continuation must be appended unchanged before tool results on the next round.
	// It carries provider state in memory only; it must not be sent to users or persisted.
	Continuation    []Message  `json:"-"`
	ID              string     `json:"id"`
	Provider        Provider   `json:"provider"`
	Model           string     `json:"model"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	AuthMode        string     `json:"auth_mode,omitempty"`
	Text            string     `json:"text"`
	Usage           Usage      `json:"usage"`
	ToolCalls       []ToolCall `json:"tool_calls,omitempty"`
	ModelCalls      int        `json:"model_calls"`
}

var (
	ErrRefused    = errors.New("model refused the request")
	ErrIncomplete = errors.New("model response was incomplete or invalid")
	ErrLimit      = errors.New("model response exceeded its limit")
)

// Generate is the entry point for both API inference and hosted agent research.
func (c *Client) Generate(ctx context.Context, in Request) (Response, error) {
	route := c.routes[in.Purpose]
	if in.Purpose == "" || route.Token == "" || route.Model == "" {
		return Response{}, ErrUnavailable
	}
	if in.Messages != nil && (in.Instructions != "" || in.Data != nil) {
		return Response{}, errors.New("use messages or instructions and data, not both")
	}
	if in.Messages == nil && in.Instructions == "" {
		return Response{}, errors.New("model instructions or messages are required")
	}
	if in.OnText != nil && in.Tools != nil {
		return Response{}, errors.New("streaming tool calls are not supported")
	}
	if in.MaxResponseBytes == 0 {
		in.MaxResponseBytes = 1 << 20
	}
	if in.MaxResponseBytes < 1 || in.MaxResponseBytes > 4<<20 || in.MaxOutputTokens < 1 {
		return Response{}, errors.New("invalid model output limits")
	}
	// All calls, including callers without a deadline, are bounded.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	var result Response
	var err error
	switch route.Provider {
	case OpenAI:
		result, err = c.openAI(ctx, route, in)
	case ContextMachine:
		result, err = c.contextMachine(ctx, route, in)
	default:
		return Response{}, errors.New("unsupported model provider")
	}
	if ctx.Err() != nil {
		return Response{}, ctx.Err()
	}
	return result, err
}

func (c *Client) post(ctx context.Context, endpoint, token string, body any) (*http.Response, error) {
	if !validEndpoint(endpoint) {
		return nil, errors.New("model endpoint must use HTTPS (HTTP is allowed only on loopback)")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, errors.New("model request is not JSON encodable")
	}
	if len(raw) > 2<<20 {
		return nil, errors.New("model request exceeded its limit")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("could not construct model request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	httpClient := c.http
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	// Do not send prompts or credentials to a redirect destination.
	copyClient := *httpClient
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := copyClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("model service could not be reached")
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("model service returned HTTP %d", res.StatusCode)
	}
	return res, nil
}

func boundedRead(r io.Reader, limit int64) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, ErrIncomplete
	}
	if int64(len(raw)) > limit {
		return nil, ErrLimit
	}
	return raw, nil
}

func validEndpoint(endpoint string) bool {
	// This exact host/path is intercepted by Cloudflare's outbound service binding.
	// No configurable HTTP hostname or public-network fallback is accepted.
	if endpoint == "http://context-machine.internal/v1/run" {
		return true
	}
	u, err := url.Parse(endpoint)
	return err == nil && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")))
}
