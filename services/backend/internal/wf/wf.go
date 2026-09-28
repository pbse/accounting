// Package wf runs durable workflows whose state lives in PostgreSQL beside the records they
// coordinate (migration 094). A workflow is Go code registered with Define from its own directory
// under services/backend/workflows; a run replays that code, and every step's saved result is
// returned instead of running the step again. Each run executes as its firm, so row-level security
// confines what its steps can read and write. See docs/workflow-engine.md.
package wf

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

var (
	// ErrTimeout is returned by WaitForEvent when its timeout passes before a matching event.
	ErrTimeout = errors.New("wait timed out")
	// ErrUnknownWorkflow means Start named a workflow this build does not register.
	ErrUnknownWorkflow = errors.New("unknown workflow")
	// ErrBusy means another active run already holds the same concurrency key.
	ErrBusy = errors.New("another active run holds this concurrency key")
	// ErrNotAllowed means a control operation does not apply to the run or step in its current state.
	ErrNotAllowed = errors.New("not allowed in the current state")
)

// Context is what a workflow's Run receives. It is a context.Context carrying the run's firm, so
// everything its steps do through the tenant database is confined to that firm.
type Context interface {
	context.Context
	exec() *execution
}

// Trigger says what may start a workflow.
type Trigger struct{ Kind, Value string }

// Manual lets a person or the API start the workflow.
func Manual() Trigger { return Trigger{Kind: "manual"} }

// Schedule starts one platform run (no firm) at each slot of a five-field cron expression, in UTC.
// A slot missed while no worker ran is started late if it is less than a day old; only the latest
// missed slot runs. The workflow's input must accept {}.
func Schedule(expression string) Trigger { return Trigger{Kind: "schedule", Value: expression} }

// ScheduleEachFirm is Schedule with one run per enabled firm at each slot.
func ScheduleEachFirm(expression string) Trigger {
	return Trigger{Kind: "schedule_each_firm", Value: expression}
}

// OnEvent starts a run for each recorded event whose key equals pattern, or starts with it when the
// pattern ends in "*" ("close.signed_off:*"). The run belongs to the event's firm and its input is
// the event's payload. Matching happens in the engine, after the event commits, never inside the
// transaction that emitted it: a payload that does not fit the input is recorded as a failed run.
func OnEvent(pattern string) Trigger { return Trigger{Kind: "event", Value: pattern} }

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func (t Trigger) check(input func(json.RawMessage) error) error {
	switch t.Kind {
	case "manual":
		return nil
	case "schedule", "schedule_each_firm":
		if _, err := cronParser.Parse(t.Value); err != nil {
			return fmt.Errorf("schedule %q: %w", t.Value, err)
		}
		if err := input(json.RawMessage("{}")); err != nil {
			return fmt.Errorf("a scheduled workflow's input must accept {}: %w", err)
		}
		return nil
	case "event":
		prefix := strings.TrimSuffix(t.Value, "*")
		if prefix == "" || strings.Contains(prefix, "*") || strings.HasPrefix(t.Value, "wf.") || len(t.Value) > 200 {
			return fmt.Errorf("event pattern %q must be a key, or a prefix ending in *, not starting with wf.", t.Value)
		}
		return nil
	}
	return fmt.Errorf("unknown trigger kind %q", t.Kind)
}

// Spec declares a workflow. Name must equal the workflow's directory name.
type Spec[I, O any] struct {
	Name        string
	Title       string
	Description string
	Triggers    []Trigger
	// Concurrency, when set, names a key only one active run may hold, for example "close:"+id.
	Concurrency func(I) string
	Run         func(Context, I) (O, error)
}

// Definition describes a registered workflow.
type Definition struct {
	Name, Title, Description string
	Triggers                 []Trigger
	Input, Output            reflect.Type
	InputSchema              map[string]any // for start forms and plan inputs
	OutputSchema             map[string]any
}

type workflow struct {
	Definition
	validate    func(json.RawMessage) error
	concurrency func(json.RawMessage) (string, error)
	run         func(Context, json.RawMessage) (json.RawMessage, error)
}

var (
	registryMu  sync.RWMutex
	registry    = map[string]*workflow{}
	namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)
)

// Define registers a workflow; call it from a package-level variable in the workflow's directory.
// An invalid or duplicate name panics, so a broken registration stops the process at startup.
func Define[I, O any](s Spec[I, O]) Definition {
	if !namePattern.MatchString(s.Name) || s.Run == nil || strings.TrimSpace(s.Title) == "" {
		panic(fmt.Sprintf("wf: invalid workflow definition %q", s.Name))
	}
	d := Definition{Name: s.Name, Title: s.Title, Description: s.Description, Triggers: slices.Clone(s.Triggers), Input: reflect.TypeFor[I](), Output: reflect.TypeFor[O](), InputSchema: Schema(reflect.TypeFor[I]()), OutputSchema: Schema(reflect.TypeFor[O]())}
	if len(d.Triggers) == 0 {
		d.Triggers = []Trigger{Manual()}
	}
	// Start rejects unknown fields; a replay tolerates them, so an input saved by older code still runs.
	decode := func(raw json.RawMessage, strict bool) (I, error) {
		var in I
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if strict {
			decoder.DisallowUnknownFields()
		}
		if err := decoder.Decode(&in); err != nil {
			return in, fmt.Errorf("invalid input for %s: %w", s.Name, err)
		}
		return in, nil
	}
	w := &workflow{Definition: d}
	w.validate = func(raw json.RawMessage) error { _, err := decode(raw, true); return err }
	for _, trigger := range d.Triggers {
		if err := trigger.check(w.validate); err != nil {
			panic(fmt.Sprintf("wf: workflow %q: %v", s.Name, err))
		}
	}
	w.concurrency = func(raw json.RawMessage) (string, error) {
		if s.Concurrency == nil {
			return "", nil
		}
		in, err := decode(raw, false)
		if err != nil {
			return "", err
		}
		return s.Concurrency(in), nil
	}
	w.run = func(ctx Context, raw json.RawMessage) (json.RawMessage, error) {
		in, err := decode(raw, false)
		if err != nil {
			return nil, Permanent(err)
		}
		out, err := s.Run(ctx, in)
		if err != nil {
			return nil, err
		}
		return json.Marshal(out)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, taken := registry[s.Name]; taken {
		panic(fmt.Sprintf("wf: workflow %q is defined twice", s.Name))
	}
	registry[s.Name] = w
	return d
}

// Definitions lists the workflows this build registers, by name.
func Definitions() []Definition {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Definition, 0, len(registry))
	for _, w := range registry {
		out = append(out, w.Definition)
	}
	slices.SortFunc(out, func(a, b Definition) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func lookup(name string) *workflow {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[name]
}

// StepOption tunes one step's retries and timeout.
type StepOption func(*policy)

type policy struct {
	attempts int
	backoff  time.Duration
	timeout  time.Duration
}

func policyOf(options []StepOption) policy {
	p := policy{attempts: 3, backoff: 30 * time.Second, timeout: 5 * time.Minute}
	for _, option := range options {
		option(&p)
	}
	return p
}

// Attempts sets how many times a failing step runs before it fails for good (default 3).
func Attempts(n int) StepOption { return func(p *policy) { p.attempts = max(n, 1) } }

// Backoff sets the delay before the first retry; each later retry doubles it (default 30s).
func Backoff(d time.Duration) StepOption { return func(p *policy) { p.backoff = max(d, time.Second) } }

// Timeout bounds one attempt (default 5m). An effect that times out has an unknown outcome.
func Timeout(d time.Duration) StepOption { return func(p *policy) { p.timeout = max(d, time.Second) } }

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Permanent marks err as not worth retrying: the step fails at once.
func Permanent(err error) error { return permanentError{err} }

type notAppliedError struct{ error }

func (e notAppliedError) Unwrap() error { return e.error }

// NotApplied marks an Effect error as certainly not applied (the provider refused before acting),
// so the effect may be retried. Every other Effect error leaves the outcome unknown until a person
// checks the provider and resolves the step.
func NotApplied(err error) error { return notAppliedError{err} }

// StepError reports a step that failed for good.
type StepError struct{ Key, Message string }

func (e *StepError) Error() string { return fmt.Sprintf("step %s failed: %s", e.Key, e.Message) }

// Limit for any saved input, output, step result or event payload.
const maxJSON = 64 << 10
