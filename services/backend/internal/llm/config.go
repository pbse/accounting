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
	APIKey                 string
	Model                  string
	Models                 map[string]string
	ReasoningEffort        string
	ReasoningEfforts       map[string]string
	ContextMachineToken    string
	ContextMachineModel    string
	ContextMachineEndpoint string
	HTTP                   *http.Client
}

var purposes = []string{"evidence_selection", "preparation", "procedure_draft", "statement", "work_command", "plan", "written_answer", "investigation"}

// New freezes routing configuration. Unknown purposes fail closed rather than
// silently sending a new workflow's data to a default provider.
func New(cfg Config) (*Client, error) {
	if (cfg.APIKey == "") != (cfg.Model == "") {
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
	c := &Client{http: cfg.HTTP, routes: make(map[string]route)}
	for _, purpose := range purposes {
		if cfg.Model != "" {
			c.routes[purpose] = route{Provider: OpenAI, Token: cfg.APIKey, Model: cfg.Model, Effort: cfg.ReasoningEffort}
		}
	}
	for purpose, model := range cfg.Models {
		route, known := c.routes[purpose]
		if !known || model == "" {
			return nil, errors.New("LLM_MODELS requires a known API purpose and a nonempty model")
		}
		route.Model = model
		c.routes[purpose] = route
	}
	for purpose, effort := range cfg.ReasoningEfforts {
		r, ok := c.routes[purpose]
		if !ok || !validEffort(effort) {
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
		c.routes["research"] = route{Provider: ContextMachine, Token: cfg.ContextMachineToken, Model: cfg.ContextMachineModel, URL: endpoint}
	} else if cfg.ContextMachineEndpoint != "" {
		return nil, errors.New("context-machine endpoint requires credentials and a model")
	}
	return c, nil
}

// FromEnv is called once by API/worker startup, not by model workflows.
func FromEnv() (*Client, error) {
	cfg := Config{APIKey: os.Getenv("OPENAI_API_KEY"), Model: os.Getenv("OPENAI_MODEL"), ContextMachineToken: os.Getenv("CONTEXT_MACHINE_TOKEN"), ContextMachineModel: os.Getenv("CONTEXT_MACHINE_MODEL"), ContextMachineEndpoint: os.Getenv("CONTEXT_MACHINE_ENDPOINT")}
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
