package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"time"
)

const contextMachineEndpoint = "http://context-machine.internal/v1/run"

// Config belongs to the application composition root. Features receive only the
// configured client through their execution context, never these credentials.
type Config struct {
	APIKey                                             string
	Model                                              string
	Models                                             map[string]string
	ReasoningEffort                                    string
	ReasoningEfforts                                   map[string]string
	ContextMachineToken                                string
	ContextMachineModel                                string
	ContextMachineEndpoint                             string
	Provider                                           string
	CodexModel, ClaudeModel, CodexEffort, ClaudeEffort string
	Profiles                                           []Scope
	AllowUncappedOutputTokens                          bool
	HTTP                                               *http.Client
}

var purposes = []string{"evidence_selection", "preparation", "procedure_draft", "statement", "work_command", "plan", "written_answer", "investigation", "chat"}

// AnyMember is the subject of an organization-wide caller profile: every current member of the organization may
// complete through it, and the context machine checks that member's own access on every call.
const AnyMember = "*"

// New freezes routing configuration. Unknown purposes fail closed rather than
// silently sending a new workflow's data to a default provider.
func New(cfg Config) (*Client, error) {
	if cfg.Provider == "" {
		cfg.Provider = "api"
	}
	if cfg.Provider == "api" && (cfg.APIKey == "") != (cfg.Model == "") {
		return nil, errors.New("both OPENAI_API_KEY and OPENAI_MODEL are required, or neither")
	}
	if (cfg.ContextMachineToken == "") != (cfg.ContextMachineModel == "") {
		return nil, errors.New("both CONTEXT_MACHINE_TOKEN and CONTEXT_MACHINE_MODEL are required, or neither")
	}
	if cfg.ContextMachineModel != "" && cfg.ContextMachineModel != "codex" && cfg.ContextMachineModel != "claude" {
		return nil, errors.New("CONTEXT_MACHINE_MODEL must be codex or claude")
	}
	if cfg.ReasoningEffort != "" && !validEffort(cfg.ReasoningEffort) {
		return nil, errors.New("invalid OPENAI_REASONING_EFFORT")
	}

	if cfg.Provider != "api" && cfg.Provider != "context_machine" {
		return nil, errors.New("LLM_PROVIDER must be api or context_machine")
	}
	if cfg.Provider == "context_machine" && cfg.ContextMachineToken == "" {
		return nil, errors.New("LLM_PROVIDER=context_machine requires the context machine")
	}
	seenProfiles := map[string]bool{}
	for _, p := range cfg.Profiles {
		key := p.Organization + "\x00" + p.Subject
		if p.Organization == "" || p.Subject == "" || p.AccessProfile == "" || seenProfiles[key] {
			return nil, errors.New("invalid or ambiguous context-machine caller profile")
		}
		seenProfiles[key] = true
	}
	c := &Client{http: cfg.HTTP, routes: make(map[string]route), profiles: slices.Clone(cfg.Profiles)}
	for _, purpose := range purposes {
		if cfg.Provider == "api" && cfg.Model != "" {
			c.routes[purpose] = route{Provider: OpenAI, Token: cfg.APIKey, Model: cfg.Model, Effort: cfg.ReasoningEffort}
		}
	}
	if cfg.Provider == "context_machine" {
		model, effort := cfg.CodexModel, cfg.CodexEffort
		if cfg.ContextMachineModel == "claude" {
			model, effort = cfg.ClaudeModel, cfg.ClaudeEffort
		}
		if effort == "" {
			effort = "medium"
		}
		if model == "" || !ValidAgentEffort(cfg.ContextMachineModel, effort) || len(cfg.Profiles) == 0 {
			return nil, errors.New("context-machine completion requires its model, effort and profiles")
		}
		for _, purpose := range purposes {
			c.routes[purpose] = route{Provider: ContextMachine, Model: model, Agent: cfg.ContextMachineModel, Effort: effort, Token: cfg.ContextMachineToken, Uncapped: cfg.AllowUncappedOutputTokens}
		}
	}
	for purpose, model := range cfg.Models {
		route, known := c.routes[purpose]
		if !known || model == "" {
			return nil, errors.New("LLM_MODELS requires a known accounting purpose and a nonempty model")
		}
		route.Model = model
		c.routes[purpose] = route
	}
	for purpose, effort := range cfg.ReasoningEfforts {
		r, ok := c.routes[purpose]
		if !ok || !validEffort(effort) || (r.Provider == ContextMachine && !ValidAgentEffort(r.Agent, effort)) {
			return nil, errors.New("invalid LLM_REASONING_EFFORTS")
		}
		r.Effort = effort
		c.routes[purpose] = r
	}
	if cfg.ContextMachineToken != "" {
		endpoint := cfg.ContextMachineEndpoint
		if endpoint == "" {
			endpoint = contextMachineEndpoint
		}
		if !validEndpoint(endpoint) {
			return nil, errors.New("invalid context-machine service endpoint")
		}
		for purpose, r := range c.routes {
			if r.Provider == ContextMachine {
				r.URL = endpoint
				c.routes[purpose] = r
			}
		}
		c.routes["research"] = route{Provider: ContextMachine, Token: cfg.ContextMachineToken, Model: cfg.ContextMachineModel, URL: endpoint}
	} else if cfg.ContextMachineEndpoint != "" {
		return nil, errors.New("context-machine endpoint requires credentials and a model")
	}
	return c, nil
}

// FromEnv is called once by API/worker startup, not by model workflows.
func FromEnv() (*Client, error) {
	cfg := Config{APIKey: os.Getenv("OPENAI_API_KEY"), Model: os.Getenv("OPENAI_MODEL"), ContextMachineToken: os.Getenv("CONTEXT_MACHINE_TOKEN"), ContextMachineModel: os.Getenv("CONTEXT_MACHINE_MODEL"), ContextMachineEndpoint: os.Getenv("CONTEXT_MACHINE_ENDPOINT")}
	cfg.Provider = os.Getenv("LLM_PROVIDER")
	cfg.CodexModel, cfg.ClaudeModel = os.Getenv("CODEX_MODEL"), os.Getenv("CLAUDE_MODEL")
	cfg.CodexEffort, cfg.ClaudeEffort = os.Getenv("CODEX_REASONING_EFFORT"), os.Getenv("CLAUDE_REASONING_EFFORT")
	if raw := os.Getenv("LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS"); raw != "" {
		if raw != "true" && raw != "false" {
			return nil, errors.New("LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS must be true or false")
		}
		cfg.AllowUncappedOutputTokens = raw == "true"
	}
	if raw := os.Getenv("CONTEXT_MACHINE_PROFILES"); raw != "" {
		if json.Unmarshal([]byte(raw), &cfg.Profiles) != nil {
			return nil, errors.New("invalid CONTEXT_MACHINE_PROFILES")
		}
	}
	cfg.ReasoningEffort = os.Getenv("OPENAI_REASONING_EFFORT")
	if raw := os.Getenv("LLM_REASONING_EFFORTS"); raw != "" {
		if json.Unmarshal([]byte(raw), &cfg.ReasoningEfforts) != nil || cfg.ReasoningEfforts == nil {
			return nil, errors.New("LLM_REASONING_EFFORTS must be a JSON object")
		}
	}
	if raw := os.Getenv("LLM_MODELS"); raw != "" {
		if json.Unmarshal([]byte(raw), &cfg.Models) != nil || cfg.Models == nil {
			return nil, errors.New("LLM_MODELS must be a JSON object mapping purposes to models")
		}
	}
	return New(cfg)
}

type clientKey struct{}

func WithClient(ctx context.Context, c *Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// Bind preserves request cancellation and authorization context while attaching
// the application's immutable model dependency.
func (c *Client) Bind(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithClient(r.Context(), c)))
	})
}

// Model returns non-secret route metadata for provenance and availability gates.
func Model(ctx context.Context, purpose string) string {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return ""
	}
	route, ok := c.routes[purpose]
	if !ok {
		return ""
	}
	if route.Provider == ContextMachine && purpose != "research" {
		return "context_machine/" + route.Agent + "/" + route.Model
	}
	return route.Model
}

// ReasoningEffort returns the configured effort; empty means the provider default.
func ReasoningEffort(ctx context.Context, purpose string) string {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return ""
	}
	return c.routes[purpose].Effort
}

var ErrUnavailable = errors.New("model route is not configured")

// Generate is the only entry point used by workflows. No ambient/global client
// or lazy environment loading: background jobs must inherit the startup context.
func Generate(ctx context.Context, in Request) (Response, error) {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return Response{}, ErrUnavailable
	}
	return c.Generate(ctx, in)
}

// CheckContextMachine verifies the same private container-to-Worker bridge used
// by research. Existing deployments without a research route remain valid.
func CheckContextMachine(ctx context.Context) error {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return nil
	}
	route, ok := c.routes["research"]
	if !ok {
		return nil
	}
	// Cache failures too: public readiness polling must not repeatedly wake the
	// container and spawn CLI processes. Each client coalesces concurrent probes.
	c.readinessMu.Lock()
	defer c.readinessMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if time.Now().Before(c.readinessExpires) {
		return c.readinessErr
	}
	// A disconnected caller must not poison the shared readiness result.
	probeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	c.readinessErr = c.checkContextMachine(probeCtx, route)
	c.readinessExpires = time.Now().Add(time.Minute)
	return c.readinessErr
}

func (c *Client) checkContextMachine(ctx context.Context, route route) error {
	endpoint, err := url.Parse(route.URL)
	if err != nil {
		return err
	}
	endpoint.Path = "/readyz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+route.Token)
	client := http.DefaultClient
	if c.http != nil {
		client = c.http
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := copyClient.Do(req)
	if err != nil {
		return errors.New("context machine unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errors.New("context machine not ready")
	}
	raw, err := boundedRead(res.Body, 4096)
	if err != nil {
		return err
	}
	var status struct {
		Status string
		Agents []string
	}
	if json.Unmarshal(raw, &status) != nil || status.Status != "ok" || !slices.Contains(status.Agents, route.Model) {
		return errors.New("context machine agents not ready")
	}
	return nil
}

func validEffort(e string) bool {
	return slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"}, e)
}

// Engine is persisted with proposals so every model route receives the same review gates.
func Engine(ctx context.Context, purpose string) string {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return ""
	}
	return string(c.routes[purpose].Provider)
}
func IsModelEngine(engine string) bool {
	return engine == string(OpenAI) || engine == string(ContextMachine)
}
func IsAccountingPurpose(purpose string) bool { return slices.Contains(purposes, purpose) }
func ValidAgentEffort(agent, effort string) bool {
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	if agent == "codex" {
		levels = append(levels, "ultra")
	}
	return slices.Contains(levels, effort)
}

type identity struct{ organization, subject string }
type identityKey struct{}

// WithIdentity accepts authority established by the HTTP or job boundary, never prompt data.
func WithIdentity(ctx context.Context, organization, subject string) context.Context {
	return context.WithValue(ctx, identityKey{}, identity{organization, subject})
}

func HasIdentity(ctx context.Context) bool { _, ok := ctx.Value(identityKey{}).(identity); return ok }
func ProviderLabel(ctx context.Context, purpose string) string {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil {
		return "the configured model provider"
	}
	r := c.routes[purpose]
	if r.Provider == ContextMachine {
		if r.Agent == "claude" {
			return "Claude (Anthropic) through the private context machine"
		}
		return "Codex (OpenAI) through the private context machine"
	}
	return "OpenAI API"
}

// CompletionProfiles supplies immutable caller eligibility for background SQL selection.
// Nil means API routing, where existing caller authorization applies.
func CompletionProfiles(ctx context.Context) []Scope {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil || c.routes["preparation"].Provider != ContextMachine {
		return nil
	}
	return slices.Clone(c.profiles)
}

// Allows checks configured caller eligibility before a workflow reserves budget.
// Background selection without identity may inspect only its configured organizations;
// each selected job must then supply its recorded creator before dispatch.
func Allows(ctx context.Context, organization string) bool {
	c, _ := ctx.Value(clientKey{}).(*Client)
	if c == nil || c.routes["preparation"].Provider != ContextMachine {
		return true
	}
	id, has := ctx.Value(identityKey{}).(identity)
	if has && (id.organization != organization || id.subject == "" || id.subject == AnyMember) {
		return false
	}
	return c.profile(organization, id.subject).AccessProfile != ""
}

// ViaWideProfile reports that the caller's eligibility rests on an organization-wide profile rather than their own:
// membership alone put them there, so a background caller must also check the client grant the work needs.
func ViaWideProfile(ctx context.Context, organization string) bool {
	c, _ := ctx.Value(clientKey{}).(*Client)
	id, has := ctx.Value(identityKey{}).(identity)
	if c == nil || !has || c.routes["preparation"].Provider != ContextMachine || id.organization != organization {
		return false
	}
	for _, p := range c.profiles {
		if p.Organization == organization && p.Subject == id.subject {
			return false
		}
	}
	return c.profile(organization, id.subject).AccessProfile != ""
}

// profile finds the configured scope for a caller: the member's own profile first, then the organization-wide one,
// which stands for the member themselves. Without a subject, any profile for the organization makes it eligible.
func (c *Client) profile(organization, subject string) Scope {
	var wide Scope
	for _, p := range c.profiles {
		if p.Organization != organization {
			continue
		}
		if subject == "" || p.Subject == subject {
			return p
		}
		if p.Subject == AnyMember {
			wide = p
		}
	}
	if wide.AccessProfile == "" {
		return Scope{}
	}
	return Scope{Organization: organization, Subject: subject, AccessProfile: wide.AccessProfile}
}
