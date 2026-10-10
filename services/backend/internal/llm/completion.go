package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

// CompletionRequest is private service traffic. One completion consumes exactly
// one reserved call; the service proposes tools, and the caller executes them.
type CompletionRequest struct {
	Version  int       `json:"version"`
	Agent    string    `json:"agent"`
	Model    string    `json:"model"`
	Effort   string    `json:"reasoning_effort"`
	Scope    Scope     `json:"scope"`
	Deadline time.Time `json:"deadline"`
	Request  Request   `json:"request"`
}
type CompletionResponse struct {
	Version int    `json:"version"`
	Status  string `json:"status"`
	Scope   Scope  `json:"scope"`
	Response
}

func (c *Client) completion(ctx context.Context, r route, in Request) (Response, error) {
	if err := ValidateAgentImages(r.Agent, in.Images); err != nil {
		return Response{}, err
	}
	id, _ := ctx.Value(identityKey{}).(identity)
	var scope Scope
	if id.subject != "" && id.subject != AnyMember {
		scope = c.profile(id.organization, id.subject)
	}
	if scope.AccessProfile == "" || scope.Subject == AnyMember {
		return Response{}, errors.New("context-machine completion requires a configured caller identity")
	}
	if in.Scope != nil || in.OnProgress != nil {
		return Response{}, ErrIncomplete
	}
	// API reasoning/continuation items cannot be translated silently into a CLI prompt.
	for _, m := range in.Messages {
		if m.providerItem != nil {
			return Response{}, ErrIncomplete
		}
	}
	deadline, _ := ctx.Deadline()
	in.AllowUncappedOutputTokens = r.Uncapped
	endpoint, _ := url.Parse(r.URL)
	endpoint.Path = "/v1/complete"
	res, err := c.post(ctx, endpoint.String(), r.Token, CompletionRequest{1, r.Agent, r.Model, r.Effort, scope, deadline, in}, RequestByteLimit(in))
	if err != nil {
		return Response{}, err
	}
	defer res.Body.Close()
	raw, err := boundedRead(res.Body, in.MaxResponseBytes)
	if err != nil {
		return Response{}, err
	}
	var wire CompletionResponse
	if json.Unmarshal(raw, &wire) != nil || wire.Version != 1 || wire.Scope != scope {
		return Response{}, ErrIncomplete
	}
	if wire.Status == "refused" {
		return Response{}, ErrRefused
	}
	if wire.Status != "completed" || wire.Model != r.Model || wire.ReasoningEffort != r.Effort || wire.AuthMode == "" {
		return Response{}, ErrIncomplete
	}
	if err := ValidateCompletion(wire.Response, in); err != nil {
		return Response{}, err
	}
	out := wire.Response
	out.Provider = ContextMachine
	if out.Text != "" {
		out.Continuation = append(out.Continuation, Message{Role: "assistant", Content: out.Text})
	}
	for _, call := range out.ToolCalls {
		call := call
		out.Continuation = append(out.Continuation, Message{Call: &call})
	}
	if in.OnText != nil && !in.OnText(out.Text) {
		return Response{}, context.Canceled
	}
	return out, nil
}

// ValidateCompletion rejects partial results and unadvertised tool proposals.
// Domain code still validates accounting facts, citations and tool arguments.
func ValidateCompletion(out Response, in Request) error {
	if out.ID == "" || out.Model == "" || out.ModelCalls != 1 || out.Usage.Input < 0 || out.Usage.Output < 0 || (strings.TrimSpace(out.Text) == "" && len(out.ToolCalls) == 0) {
		return ErrIncomplete
	}
	allowed := map[string]bool{}
	for _, t := range in.Tools {
		allowed[t.Name] = true
	}
	seen := map[string]bool{}
	if !in.ParallelToolCalls && len(out.ToolCalls) > 1 {
		return ErrIncomplete
	}
	if in.ToolChoice == "required" && len(out.ToolCalls) == 0 {
		return ErrIncomplete
	}
	if len(out.ToolCalls) > 16 {
		return ErrLimit
	}
	for _, t := range out.ToolCalls {
		if t.CallID == "" || seen[t.CallID] || !allowed[t.Name] || !json.Valid([]byte(t.Arguments)) {
			return ErrIncomplete
		}
		seen[t.CallID] = true
	}
	if len(out.ToolCalls) > 0 && (in.ToolChoice == "none" || in.Schema != nil) {
		return ErrIncomplete
	}
	if in.Schema != nil && !json.Valid([]byte(out.Text)) {
		return ErrIncomplete
	}
	return nil
}
