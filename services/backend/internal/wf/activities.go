package wf

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// EffectClass says what an activity does, which decides how it runs and what a plan needs around it.
type EffectClass string

const (
	Read     EffectClass = "read"     // reads only; runs as a step
	Internal EffectClass = "internal" // writes our own records; runs as a step (at least once, so idempotently)
	External EffectClass = "external" // acts on a provider or a person outside; runs as an Effect, and a plan must approve it first
)

// ActivitySpec declares an activity: a vetted building block that code workflows call with Call and
// plans compose. Name must equal its directory under services/backend/activities. Run always
// calls existing domain code; an activity never carries its own copy of accounting logic.
type ActivitySpec[I, O any] struct {
	Name        string
	Title       string
	Description string // plain words, read by staff choosing it and by a model proposing a plan
	Permission  string // what the person approving a plan that uses it must hold in the firm; "" for none
	Effect      EffectClass
	Run         func(context.Context, I) (O, error)
}

// Activity is a registered activity, for typed calls from code workflows.
type Activity[I, O any] struct{ name string }

// ActivityInfo describes a registered activity, with JSON Schemas of its input and output.
type ActivityInfo struct {
	Name, Title, Description, Permission string
	Effect                               EffectClass
	Input, Output                        map[string]any
}

type activity struct {
	ActivityInfo
	validate func(json.RawMessage) error // strict: unknown fields refused (plans at save time)
	fits     func(json.RawMessage) error // as the activity will decode it at run time
	run      func(context.Context, json.RawMessage) (any, error)
}

var activities = map[string]*activity{}

// DefineActivity registers an activity; call it from a package-level variable in its directory. An
// invalid or duplicate definition panics, so a broken registration stops the process at startup.
func DefineActivity[I, O any](s ActivitySpec[I, O]) *Activity[I, O] {
	if !namePattern.MatchString(s.Name) || s.Run == nil || strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Description) == "" || !slices.Contains([]EffectClass{Read, Internal, External}, s.Effect) {
		panic(fmt.Sprintf("wf: invalid activity definition %q", s.Name))
	}
	decode := func(raw json.RawMessage, strict bool) (I, error) {
		var in I
		decoder := json.NewDecoder(bytes.NewReader(raw))
		if strict {
			decoder.DisallowUnknownFields()
		}
		if err := decoder.Decode(&in); err != nil {
			return in, fmt.Errorf("invalid input for activity %s: %w", s.Name, err)
		}
		return in, nil
	}
	a := &activity{
		ActivityInfo: ActivityInfo{Name: s.Name, Title: s.Title, Description: s.Description, Permission: s.Permission, Effect: s.Effect,
			Input: Schema(reflect.TypeFor[I]()), Output: Schema(reflect.TypeFor[O]())},
		fits: func(raw json.RawMessage) error { _, err := decode(raw, false); return err },
		run: func(ctx context.Context, raw json.RawMessage) (any, error) {
			in, err := decode(raw, false)
			if err != nil {
				return nil, Permanent(err)
			}
			return s.Run(ctx, in)
		},
	}
	a.validate = func(raw json.RawMessage) error {
		if _, err := decode(raw, true); err != nil {
			return err
		}
		if err := checkRequired(a.Input, raw); err != nil {
			return fmt.Errorf("invalid input for activity %s: %w", s.Name, err)
		}
		return nil
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, taken := activities[s.Name]; taken {
		panic(fmt.Sprintf("wf: activity %q is defined twice", s.Name))
	}
	activities[s.Name] = a
	return &Activity[I, O]{name: s.Name}
}

// Activities lists the activities this build registers, by name: the catalog staff and a model
// compose plans from.
func Activities() []ActivityInfo {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ActivityInfo, 0, len(activities))
	for _, a := range activities {
		out = append(out, a.ActivityInfo)
	}
	slices.SortFunc(out, func(a, b ActivityInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func lookupActivity(name string) *activity {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return activities[name]
}

// Call runs an activity under key: as a step for reads and internal writes, as an Effect (never
// repeated when uncertain) for external ones.
func Call[I, O any](ctx Context, key string, a *Activity[I, O], input I, options ...StepOption) (O, error) {
	x := ctx.exec()
	raw, err := json.Marshal(input)
	if err != nil {
		var zero O
		return zero, x.stop(err)
	}
	return typed[O](x)(x.callActivity(key, a.name, raw, options))
}

// CallActivity is Call by name with JSON input, for the plan interpreter.
func CallActivity(ctx Context, key, name string, input json.RawMessage, options ...StepOption) (json.RawMessage, error) {
	return ctx.exec().callActivity(key, name, input, options)
}

func (x *execution) callActivity(key, name string, input json.RawMessage, options []StepOption) (json.RawMessage, error) {
	a := lookupActivity(name)
	if a == nil {
		return nil, x.stop(fmt.Errorf("activity %q is not registered in this build", name))
	}
	kind := "activity"
	if a.Effect == External {
		kind = "effect"
	}
	recorded := map[string]any{"activity": name, "input": input}
	if err := a.fits(input); err != nil {
		// Input that cannot be decoded never reaches the provider: a plain failure, not an unknown effect.
		return x.step(key, "activity", recorded, policyOf(options), func(context.Context) (any, error) { return nil, Permanent(err) })
	}
	return x.step(key, kind, recorded, policyOf(options), func(c context.Context) (any, error) {
		return a.run(c, input)
	})
}
