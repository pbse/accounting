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

// Trigger says what may start a workflow. Schedules and events arrive in phase W2.
type Trigger struct{ Kind, Value string }

// Manual lets a person or the API start the workflow.
func Manual() Trigger { return Trigger{Kind: "manual"} }

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
	d := Definition{Name: s.Name, Title: s.Title, Description: s.Description, Triggers: slices.Clone(s.Triggers), Input: reflect.TypeFor[I](), Output: reflect.TypeFor[O]()}
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
