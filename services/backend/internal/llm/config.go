package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
)

const contextMachineEndpoint = "http://context-machine.internal/v1/run"

// Config belongs to the application composition root. Features receive only the
// configured client through their execution context, never these credentials.
type Config struct {
	APIKey                 string
	Model                  string
	Models                 map[string]string
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
	c := &Client{http: cfg.HTTP, routes: make(map[string]route)}
	for _, purpose := range purposes {
		if cfg.Model != "" {
			c.routes[purpose] = route{Provider: OpenAI, Token: cfg.APIKey, Model: cfg.Model}
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

// Model returns non-secret route metadata for provenance, replay keys and gates.
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
	if json.Unmarshal(raw, &status) != nil || status.Status != "ok" || !slices.Contains(status.Agents, "codex") || !slices.Contains(status.Agents, "claude") {
		return errors.New("context machine agents not ready")
	}
	return nil
}
