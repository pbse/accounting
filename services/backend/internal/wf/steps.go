package wf

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Step runs fn once under key and saves its JSON result; a replay returns the saved result without
// calling fn. A step may run more than once if its worker dies mid-call, so fn must be safe to
// repeat: use idempotent writes, or StepID as an idempotency key. External writes use Effect.
func Step[T any](ctx Context, key string, fn func(context.Context) (T, error), options ...StepOption) (T, error) {
	return typed[T](ctx.exec().step(key, "step", policyOf(options), func(c context.Context) (any, error) { return fn(c) }))
}

// Effect runs an external write (ledger posting, email, provider call) under key. It records the
// attempt before calling fn and never calls fn again on its own once the outcome is uncertain: if
// fn returns an error not wrapped in NotApplied, or its worker dies mid-call, the step becomes
// unknown and the run waits until a person checks the provider and resolves it. Cancelling a run
// never interrupts an effect in flight.
func Effect[T any](ctx Context, key string, fn func(context.Context) (T, error), options ...StepOption) (T, error) {
	return typed[T](ctx.exec().step(key, "effect", policyOf(options), func(c context.Context) (any, error) { return fn(c) }))
}

// StepID identifies the current run and step, for use as an idempotency key in a step's writes.
func StepID(ctx context.Context, key string) string {
	if x, ok := ctx.(Context); ok {
		return fmt.Sprintf("wf:%d:%s", x.exec().run.id, key)
	}
	if x, ok := ctx.Value(stepKey{}).(*execution); ok {
		return fmt.Sprintf("wf:%d:%s", x.run.id, key)
	}
	return ""
}

type stepKey struct{}

// Sleep parks the run for d the first time it reaches key.
func Sleep(ctx Context, key string, d time.Duration) error {
	x := ctx.exec()
	return x.sleep(key, func() time.Time { return x.engine.now().Add(d) })
}

// SleepUntil parks the run until t.
func SleepUntil(ctx Context, key string, t time.Time) error {
	return ctx.exec().sleep(key, func() time.Time { return t.UTC() })
}

// WaitForEvent parks the run until an event with this key is recorded (Emit) for the run's firm, or
// until timeout passes (ErrTimeout). It takes the earliest such event recorded after the run started
// that this run has not already consumed, so an event that arrives before the wait still counts.
func WaitForEvent[T any](ctx Context, key, event string, timeout time.Duration) (T, error) {
	var zero T
	raw, err := ctx.exec().wait(key, event, timeout)
	if err != nil {
		return zero, err
	}
	var payload T
	if err = json.Unmarshal(raw, &payload); err != nil {
		return zero, Permanent(fmt.Errorf("event %s payload does not fit %T: %w", event, zero, err))
	}
	return payload, nil
}

// Now returns the time this point of the run was first reached, the same on every replay. Once the
// run is stopping it returns the zero time; the next step call then returns the stop.
func Now(ctx Context) time.Time {
	x := ctx.exec()
	var t time.Time
	if raw, err := x.value("now", func() any { return x.engine.now() }); err == nil {
		_ = json.Unmarshal(raw, &t)
	}
	return t
}

// Random returns a random string fixed at the first run through this point, the same on replay.
func Random(ctx Context) string {
	var s string
	if raw, err := ctx.exec().value("random", func() any { return rand.Text() }); err == nil {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

func typed[T any](raw json.RawMessage, err error) (T, error) {
	var out T
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, Permanent(fmt.Errorf("saved step result does not fit %T: %w", out, err))
	}
	return out, nil
}

// stop makes err the run's outcome for this execution: every later primitive returns it too, so
// workflow code that ignores an error cannot run past a step that has not happened.
func (x *execution) stop(err error) error {
	if x.halt == nil {
		x.halt = err
	}
	return x.halt
}

// parkUntil suspends the run until wake (nil: until woken) or an event with waitingOn.
func (x *execution) parkUntil(waitingOn string, wake *time.Time) error {
	if x.halt == nil {
		x.waitingOn, x.wakeAt = waitingOn, wake
	}
	return x.stop(errSuspended)
}

func (x *execution) enter(key string, internal bool) error {
	if x.halt != nil {
		return x.halt
	}
	if key == "" || len(key) > 200 || (!internal && strings.HasPrefix(key, "$")) {
		return Permanent(fmt.Errorf("invalid step key %q: use 1-200 characters, not starting with $", key))
	}
	if x.visited[key] {
		return Permanent(fmt.Errorf("step key %q was reached twice in one run; give each call its own key", key))
	}
	x.visited[key] = true
	return nil
}

// checkpoint confirms this worker still holds the run and nobody asked to cancel it.
func (x *execution) checkpoint() error {
	var cancelled bool
	err := x.engine.DB.QueryRowContext(x.sys, `SELECT cancel_requested_at IS NOT NULL FROM workflow_runs WHERE id=$1 AND claim=$2`, x.run.id, x.run.claim).Scan(&cancelled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		x.lost.Store(true)
		return x.stop(errAbandoned)
	case err != nil:
		return x.stop(errAbandoned)
	case cancelled || x.cancelled.Load():
		return x.stop(errCancelled)
	}
	return nil
}

// write runs a bookkeeping statement fenced by this worker's claim; losing the claim abandons the run.
func (x *execution) write(query string, args ...any) error {
	result, err := x.engine.DB.ExecContext(x.sys, query, args...)
	if err == nil && !affected(result) {
		x.lost.Store(true)
		err = errAbandoned
	}
	if err != nil {
		return x.stop(errAbandoned)
	}
	return nil
}

const fence = ` AND EXISTS(SELECT 1 FROM workflow_runs WHERE id=$1 AND claim=$2)`

func (x *execution) insert(key, kind, state string, wake *time.Time, waitingOn string, input any) error {
	var in any
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return Permanent(err)
		}
		in = string(raw)
	}
	var wakeAt any
	if wake != nil {
		wakeAt = *wake
	}
	seq := x.nextSeq
	if err := x.write(`INSERT INTO workflow_steps(run_id,key,organization_id,seq,kind,state,attempts,wake_at,waiting_on,input)
 SELECT $1,$3,organization_id,$4,$5,$6,CASE WHEN $6='running' THEN 1 ELSE 0 END,$7,$8,$9 FROM workflow_runs WHERE id=$1 AND claim=$2`,
		x.run.id, x.run.claim, key, seq, kind, state, wakeAt, waitingOn, in); err != nil {
		return err
	}
	x.nextSeq++
	s := &stepRow{key: key, kind: kind, state: state, wakeAt: wake}
	if state == "running" {
		s.attempts = 1
	}
	x.steps[key] = s
	return nil
}

func (x *execution) complete(key string, raw json.RawMessage) error {
	if err := x.write(`UPDATE workflow_steps SET state='completed',output=$4,error='',retry_at=NULL,finished_at=now() WHERE run_id=$1 AND key=$3`+fence, x.run.id, x.run.claim, key, string(raw)); err != nil {
		return err
	}
	s := x.steps[key]
	s.state, s.output, s.errMsg = "completed", raw, ""
	return nil
}

func (x *execution) mark(key, state, message string, retry *time.Time) error {
	var at any
	if retry != nil {
		at = *retry
	}
	var finished any
	if state == "failed" || state == "unknown" {
		finished = x.engine.now()
	}
	if err := x.write(`UPDATE workflow_steps SET state=$4,error=$5,retry_at=$6,finished_at=$7 WHERE run_id=$1 AND key=$3`+fence, x.run.id, x.run.claim, key, state, truncate(message), at, finished); err != nil {
		return err
	}
	s := x.steps[key]
	s.state, s.errMsg, s.retryAt = state, message, retry
	return nil
}

func (x *execution) step(key, kind string, p policy, fn func(context.Context) (any, error)) (json.RawMessage, error) {
	if err := x.enter(key, false); err != nil {
		return nil, err
	}
	s := x.steps[key]
	if s != nil {
		if s.kind != kind {
			return nil, Permanent(fmt.Errorf("step %q was a %s and is now a %s; give the new step its own key", key, s.kind, kind))
		}
		switch s.state {
		case "completed":
			return s.output, nil
		case "failed":
			return nil, &StepError{Key: key, Message: s.errMsg}
		case "cancelled":
			return nil, x.stop(errCancelled)
		case "unknown":
			return nil, x.parkUntil("", nil)
		case "waiting":
			if s.retryAt != nil && s.retryAt.After(x.engine.now()) {
				return nil, x.parkUntil("", s.retryAt)
			}
		case "running":
			if kind == "effect" {
				// The worker stopped mid-call: the provider may or may not have acted.
				if err := x.mark(key, "unknown", "The worker stopped during this call. Check the provider before resolving this step.", nil); err != nil {
					return nil, err
				}
				return nil, x.parkUntil("", nil)
			}
		}
	}
	if err := x.checkpoint(); err != nil {
		return nil, err
	}
	attempts := 1
	if s == nil {
		if err := x.insert(key, kind, "running", nil, "", nil); err != nil {
			return nil, err
		}
	} else {
		attempts = s.attempts + 1
		if err := x.write(`UPDATE workflow_steps SET state='running',attempts=$4,retry_at=NULL,error='' WHERE run_id=$1 AND key=$3`+fence, x.run.id, x.run.claim, key, attempts); err != nil {
			return nil, err
		}
		s.state, s.attempts = "running", attempts
	}
	if err := x.crash("before-call", key); err != nil {
		return nil, err
	}
	result, callErr := x.invoke(kind, key, p, fn)
	if err := x.crash("after-call", key); err != nil {
		return nil, err
	}
	if callErr == nil {
		raw, err := json.Marshal(result)
		switch {
		case err != nil:
			callErr = Permanent(fmt.Errorf("step result is not JSON: %w", err))
		case len(raw) > maxJSON:
			callErr = Permanent(fmt.Errorf("step result is %d bytes; the limit is %d", len(raw), maxJSON))
		default:
			if err = x.complete(key, raw); err != nil {
				return nil, err
			}
			return raw, nil
		}
		if kind == "effect" {
			// The provider acted, but the result cannot be saved: a person must record it.
			if err := x.mark(key, "unknown", "The call succeeded but its result could not be saved: "+callErr.Error(), nil); err != nil {
				return nil, err
			}
			return nil, x.parkUntil("", nil)
		}
	}
	if x.lost.Load() {
		return nil, x.stop(errAbandoned)
	}
	var notApplied notAppliedError
	if kind == "effect" && !errors.As(callErr, &notApplied) {
		if err := x.mark(key, "unknown", callErr.Error(), nil); err != nil {
			return nil, err
		}
		return nil, x.parkUntil("", nil)
	}
	if x.cancelled.Load() && kind != "effect" {
		return nil, x.stop(errCancelled)
	}
	if kind != "effect" && x.Err() != nil {
		// The worker is stopping; the step did not fail on its own account. Hand the run back.
		return nil, x.stop(errAbandoned)
	}
	var permanent permanentError
	if errors.As(callErr, &permanent) || attempts >= p.attempts {
		if err := x.mark(key, "failed", callErr.Error(), nil); err != nil {
			return nil, err
		}
		return nil, &StepError{Key: key, Message: callErr.Error()}
	}
	retry := x.engine.now().Add(p.backoff << min(attempts-1, 16))
	if err := x.mark(key, "waiting", callErr.Error(), &retry); err != nil {
		return nil, err
	}
	return nil, x.parkUntil("", &retry)
}

// invoke calls fn with the step's timeout. An effect's context ignores cancellation so a run being
// cancelled or a worker stopping never cuts a provider call short; its timeout still applies.
func (x *execution) invoke(kind, key string, p policy, fn func(context.Context) (any, error)) (result any, err error) {
	base := context.Context(x)
	if kind == "effect" {
		base = context.WithoutCancel(x)
	}
	c, cancel := context.WithTimeout(context.WithValue(base, stepKey{}, x), p.timeout)
	defer cancel()
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("step %s panicked: %v", key, r)
		}
	}()
	return fn(c)
}

func (x *execution) crash(point, key string) error {
	if x.engine.hook != nil {
		if err := x.engine.hook(point, key); err != nil {
			return x.stop(errCrashed)
		}
	}
	return nil
}

func (x *execution) sleep(key string, until func() time.Time) error {
	if err := x.enter(key, false); err != nil {
		return err
	}
	s := x.steps[key]
	if s == nil {
		if err := x.checkpoint(); err != nil {
			return err
		}
		wake := until()
		if err := x.insert(key, "sleep", "waiting", &wake, "", nil); err != nil {
			return err
		}
		s = x.steps[key]
	}
	switch {
	case s.kind != "sleep":
		return Permanent(fmt.Errorf("step %q was a %s and is now a sleep; give the new step its own key", key, s.kind))
	case s.state == "completed":
		return nil
	case s.state == "cancelled":
		return x.stop(errCancelled)
	case s.wakeAt == nil || !s.wakeAt.After(x.engine.now()):
		return x.complete(key, json.RawMessage("null"))
	}
	return x.parkUntil("", s.wakeAt)
}

type eventOutcome struct {
	EventID  int64           `json:"event_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	TimedOut bool            `json:"timed_out,omitempty"`
}

func (x *execution) wait(key, event string, timeout time.Duration) (json.RawMessage, error) {
	if err := x.enter(key, false); err != nil {
		return nil, err
	}
	if event == "" || len(event) > 200 || timeout <= 0 {
		return nil, Permanent(fmt.Errorf("wait %q needs an event key of 1-200 characters and a positive timeout", key))
	}
	s := x.steps[key]
	if s == nil {
		if err := x.checkpoint(); err != nil {
			return nil, err
		}
		deadline := x.engine.now().Add(timeout)
		if err := x.insert(key, "event", "waiting", &deadline, event, map[string]string{"event": event}); err != nil {
			return nil, err
		}
		s = x.steps[key]
	}
	if s.kind != "event" {
		return nil, Permanent(fmt.Errorf("step %q was a %s and is now an event wait; give the new step its own key", key, s.kind))
	}
	switch s.state {
	case "completed":
		var outcome eventOutcome
		if err := json.Unmarshal(s.output, &outcome); err != nil {
			return nil, Permanent(err)
		}
		if outcome.TimedOut {
			return nil, ErrTimeout
		}
		return outcome.Payload, nil
	case "cancelled":
		return nil, x.stop(errCancelled)
	}
	var id int64
	var payload []byte
	org := sql.NullString{String: x.run.org, Valid: x.run.org != ""}
	err := x.engine.DB.QueryRowContext(x.sys, `SELECT e.id,e.payload FROM workflow_events e WHERE e.key=$2 AND e.organization_id IS NOT DISTINCT FROM $3 AND e.created_at>=$4
 AND NOT EXISTS(SELECT 1 FROM workflow_steps s WHERE s.run_id=$1 AND s.kind='event' AND s.state='completed' AND (s.output->>'event_id')::bigint=e.id)
 ORDER BY e.id LIMIT 1`, x.run.id, event, org, x.run.createdAt).Scan(&id, &payload)
	switch {
	case err == nil:
		raw, _ := json.Marshal(eventOutcome{EventID: id, Payload: payload})
		if err = x.complete(key, raw); err != nil {
			return nil, err
		}
		return payload, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, x.stop(errAbandoned)
	case s.wakeAt == nil || !s.wakeAt.After(x.engine.now()):
		if err = x.complete(key, json.RawMessage(`{"timed_out":true}`)); err != nil {
			return nil, err
		}
		return nil, ErrTimeout
	}
	return nil, x.parkUntil(event, s.wakeAt)
}

// value saves a generated value the first time a run reaches this point, keyed by call order.
func (x *execution) value(name string, generate func() any) (json.RawMessage, error) {
	x.counters[name]++
	key := fmt.Sprintf("$%s/%d", name, x.counters[name])
	if err := x.enter(key, true); err != nil {
		return nil, err
	}
	if s := x.steps[key]; s != nil {
		return s.output, nil
	}
	raw, err := json.Marshal(generate())
	if err != nil {
		return nil, Permanent(err)
	}
	if err = x.insert(key, "value", "running", nil, "", nil); err != nil {
		return nil, err
	}
	if err = x.complete(key, raw); err != nil {
		return nil, err
	}
	return raw, nil
}
