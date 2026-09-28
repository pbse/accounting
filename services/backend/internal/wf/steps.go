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
	x := ctx.exec()
	return typed[T](x)(x.step(key, "step", nil, policyOf(options), func(c context.Context) (any, error) { return fn(c) }))
}

// Effect runs an external write (ledger posting, email, provider call) under key. It records the
// attempt before calling fn and never calls fn again on its own once the outcome is uncertain: if
// fn returns an error not wrapped in NotApplied, or its worker dies mid-call, the step becomes
// unknown and the run waits until a person checks the provider and resolves it. Cancelling a run
// never interrupts an effect in flight.
func Effect[T any](ctx Context, key string, fn func(context.Context) (T, error), options ...StepOption) (T, error) {
	x := ctx.exec()
	return typed[T](x)(x.step(key, "effect", nil, policyOf(options), func(c context.Context) (any, error) { return fn(c) }))
}

// Watch calls fn about every interval until it reports done, as one step under key however many calls that takes: a
// watch over something that changes in its own time (a close handed to the agent) costs one row, not one per look. fn
// must be safe to repeat, like a step's. An error is recorded on the step (and cleared by the next call that succeeds)
// and fn is called again after the interval, so a watch never fails its run; it ends when fn reports done or the run
// is cancelled. The step's attempts count the calls. Of the step options, only Timeout applies, to each call.
func Watch(ctx Context, key string, every time.Duration, fn func(context.Context) (bool, error), options ...StepOption) error {
	return ctx.exec().watch(key, every, policyOf(options), fn)
}

// StepID identifies the current run and step, for use as an idempotency key in a step's writes.
func StepID(ctx context.Context, key string) string {
	if x := executionOf(ctx); x != nil {
		return fmt.Sprintf("wf:%d:%s", x.run.id, key)
	}
	return ""
}

// DB is the engine's database, for use inside a workflow or step. Queries made with the step's
// context run as the run's firm, so row-level security confines them.
func DB(ctx context.Context) *sql.DB {
	if x := executionOf(ctx); x != nil {
		return x.engine.DB
	}
	return nil
}

// Organization is the firm the current run belongs to, or "" for a platform run.
func Organization(ctx context.Context) string {
	if x := executionOf(ctx); x != nil {
		return x.run.org
	}
	return ""
}

type stepKey struct{}
type stepIDKey struct{}

// CurrentStepID identifies the step running with ctx ("wf:<run>:<key>"), or "" outside a step: an
// idempotency key for a step's own writes, stable across the step's retries.
func CurrentStepID(ctx context.Context) string { id, _ := ctx.Value(stepIDKey{}).(string); return id }

func executionOf(ctx context.Context) *execution {
	if x, ok := ctx.(Context); ok {
		return x.exec()
	}
	x, _ := ctx.Value(stepKey{}).(*execution)
	return x
}

// Child starts workflow as a child of this run, for the same firm, and waits for its result. The
// child starts once (its idempotency key is this run and key). A child that fails or is cancelled
// fails this step; cancelling this run cancels the child.
func Child[O any](ctx Context, key, workflow string, input any) (O, error) {
	x := ctx.exec()
	return typed[O](x)(x.child(key, workflow, input))
}

// Approval asks a person holding Permission in the run's firm to decide.
type Approval struct {
	Permission string // for example "requests:send"
	Subject    string // what is being decided, shown to people
	Prompt     string
	Timeout    time.Duration
}

// WaitForApproval parks the run until a person decides (Approve) or Timeout passes (ErrTimeout). A
// rejection is a Decision with Approved false, not an error.
func WaitForApproval(ctx Context, key string, a Approval) (Decision, error) {
	x := ctx.exec()
	if strings.TrimSpace(a.Permission) == "" || len(key) > 150 {
		return Decision{}, x.stop(fmt.Errorf("approval %q needs a permission and a key of at most 150 characters", key))
	}
	fits := func(raw json.RawMessage) error { var d Decision; return json.Unmarshal(raw, &d) }
	raw, err := x.wait(key, "approval", approvalKey(x.run.id, key), a.Timeout, fits, map[string]string{"permission": a.Permission, "subject": a.Subject, "prompt": a.Prompt})
	if err != nil {
		return Decision{}, err
	}
	var d Decision
	if err = json.Unmarshal(raw, &d); err != nil {
		return Decision{}, x.stop(err)
	}
	return d, nil
}

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
//
// An event whose payload does not decode into T is passed over (and listed on the step) rather than
// failing the run, so a malformed signal cannot keep a later valid one from counting.
func WaitForEvent[T any](ctx Context, key, event string, timeout time.Duration) (T, error) {
	var zero T
	fits := func(raw json.RawMessage) error { var payload T; return json.Unmarshal(raw, &payload) }
	raw, err := ctx.exec().wait(key, "event", event, timeout, fits, map[string]string{"event": event})
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

// typed decodes a step's saved result. A result that no longer fits its type means the code changed
// under a saved step; that stops the run rather than continuing with a zero value.
func typed[T any](x *execution) func(json.RawMessage, error) (T, error) {
	return func(raw json.RawMessage, err error) (T, error) {
		var out T
		if err != nil {
			return out, err
		}
		if err = json.Unmarshal(raw, &out); err != nil {
			return out, x.stop(fmt.Errorf("saved step result does not fit %T (give a changed step a new key): %w", out, err))
		}
		return out, nil
	}
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
	// Structural errors are sticky: code that ignores them cannot go on to run later steps.
	if key == "" || len(key) > 200 || (!internal && strings.HasPrefix(key, "$")) {
		return x.stop(fmt.Errorf("invalid step key %q: use 1-200 characters, not starting with $", key))
	}
	if x.visited[key] {
		return x.stop(fmt.Errorf("step key %q was reached twice in one run; give each call its own key", key))
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

func (x *execution) step(key, kind string, input any, p policy, fn func(context.Context) (any, error)) (json.RawMessage, error) {
	if err := x.enter(key, false); err != nil {
		return nil, err
	}
	s := x.steps[key]
	if s != nil {
		if s.kind != kind {
			return nil, x.stop(fmt.Errorf("step %q was a %s and is now a %s; give the new step its own key", key, s.kind, kind))
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
		if err := x.insert(key, kind, "running", nil, "", input); err != nil {
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
		if err := x.crash("marked-unknown", key); err != nil {
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
	c, cancel := context.WithTimeout(context.WithValue(context.WithValue(base, stepKey{}, x), stepIDKey{}, fmt.Sprintf("wf:%d:%s", x.run.id, key)), p.timeout)
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
		return x.stop(fmt.Errorf("step %q was a %s and is now a sleep; give the new step its own key", key, s.kind))
	case s.state == "completed":
		return nil
	case s.state == "cancelled":
		return x.stop(errCancelled)
	case s.wakeAt == nil || !s.wakeAt.After(x.engine.now()):
		return x.complete(key, json.RawMessage("null"))
	}
	return x.parkUntil("", s.wakeAt)
}

func (x *execution) watch(key string, every time.Duration, p policy, fn func(context.Context) (bool, error)) error {
	if err := x.enter(key, false); err != nil {
		return err
	}
	if every < time.Second {
		return x.stop(fmt.Errorf("watch %q needs an interval of at least a second", key))
	}
	s := x.steps[key]
	if s != nil {
		switch {
		case s.kind != "watch":
			return x.stop(fmt.Errorf("step %q was a %s and is now a watch; give the new step its own key", key, s.kind))
		case s.state == "completed":
			return nil
		case s.state == "cancelled":
			return x.stop(errCancelled)
		case s.state == "waiting" && s.wakeAt != nil && s.wakeAt.After(x.engine.now()):
			return x.parkUntil("", s.wakeAt)
		}
	}
	if err := x.checkpoint(); err != nil {
		return err
	}
	if s == nil {
		if err := x.insert(key, "watch", "running", nil, "", map[string]string{"every": every.String()}); err != nil {
			return err
		}
	} else if err := x.write(`UPDATE workflow_steps SET state='running',attempts=attempts+1,wake_at=NULL WHERE run_id=$1 AND key=$3`+fence, x.run.id, x.run.claim, key); err != nil {
		return err
	}
	result, callErr := x.invoke("watch", key, p, func(c context.Context) (any, error) { return fn(c) })
	switch {
	case x.lost.Load():
		return x.stop(errAbandoned)
	case x.cancelled.Load():
		return x.stop(errCancelled)
	case callErr != nil && x.Err() != nil:
		// The worker is stopping; the call did not fail on its own account. Hand the run back.
		return x.stop(errAbandoned)
	case callErr == nil && result == true:
		return x.complete(key, json.RawMessage("true"))
	}
	message := ""
	if callErr != nil {
		message = callErr.Error()
	}
	wake := x.engine.now().Add(every)
	if err := x.write(`UPDATE workflow_steps SET state='waiting',wake_at=$4,error=$5 WHERE run_id=$1 AND key=$3`+fence, x.run.id, x.run.claim, key, wake, truncate(message)); err != nil {
		return err
	}
	s = x.steps[key]
	s.state, s.wakeAt, s.errMsg = "waiting", &wake, message
	return x.parkUntil("", &wake)
}

type eventOutcome struct {
	EventID  int64           `json:"event_id,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
	TimedOut bool            `json:"timed_out,omitempty"`
}

func (x *execution) wait(key, kind, event string, timeout time.Duration, fits func(json.RawMessage) error, input any) (json.RawMessage, error) {
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
		if err := x.insert(key, kind, "waiting", &deadline, event, input); err != nil {
			return nil, err
		}
		s = x.steps[key]
	}
	if s.kind != kind {
		return nil, x.stop(fmt.Errorf("step %q was a %s and is now a %s; give the new step its own key", key, s.kind, kind))
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
	org := sql.NullString{String: x.run.org, Valid: x.run.org != ""}
	var deadline any
	if s.wakeAt != nil {
		deadline = *s.wakeAt
	}
	for passes := 0; passes < 20; passes++ {
		var id int64
		var payload []byte
		skipped, _ := json.Marshal(append([]int64{}, s.skipped...))
		// An event recorded after the deadline does not count, even if the run is only now looking.
		err := x.engine.DB.QueryRowContext(x.sys, `SELECT e.id,e.payload FROM workflow_events e WHERE e.key=$2 AND e.organization_id IS NOT DISTINCT FROM $3 AND e.created_at>=$4
 AND ($5::timestamptz IS NULL OR e.created_at<=$5::timestamptz) AND NOT e.id IN (SELECT jsonb_array_elements_text($6::jsonb)::bigint)
 AND NOT EXISTS(SELECT 1 FROM workflow_steps s WHERE s.run_id=$1 AND s.kind IN ('event','approval') AND s.state='completed' AND (s.output->>'event_id')::bigint=e.id)
 ORDER BY e.id LIMIT 1`, x.run.id, event, org, x.run.createdAt, deadline, string(skipped)).Scan(&id, &payload)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return nil, x.stop(errAbandoned)
		}
		if bad := fits(payload); bad != nil {
			s.skipped = append(s.skipped, id)
			list, _ := json.Marshal(s.skipped)
			if err = x.write(`UPDATE workflow_steps SET input=jsonb_set(COALESCE(input,'{}'::jsonb),'{skipped}',$4::jsonb),error=$5 WHERE run_id=$1 AND key=$3`+fence, x.run.id, x.run.claim, key, string(list), truncate(fmt.Sprintf("Passed over event %d: its payload does not fit: %v", id, bad))); err != nil {
				return nil, err
			}
			continue
		}
		raw, _ := json.Marshal(eventOutcome{EventID: id, Payload: payload})
		if err = x.complete(key, raw); err != nil {
			return nil, err
		}
		return payload, nil
	}
	switch {
	case s.wakeAt == nil || !s.wakeAt.After(x.engine.now()):
		if err := x.complete(key, json.RawMessage(`{"timed_out":true}`)); err != nil {
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
	s := x.steps[key]
	if s != nil && s.state == "completed" {
		return s.output, nil
	}
	raw, err := json.Marshal(generate())
	if err != nil {
		return nil, Permanent(err)
	}
	if s != nil {
		// Only a row an older build left incomplete gets here; a value is saved in one write now.
		return raw, x.complete(key, raw)
	}
	if err = x.write(`INSERT INTO workflow_steps(run_id,key,organization_id,seq,kind,state,output,finished_at)
 SELECT $1,$3,organization_id,$4,'value','completed',$5,now() FROM workflow_runs WHERE id=$1 AND claim=$2`, x.run.id, x.run.claim, key, x.nextSeq, string(raw)); err != nil {
		return nil, err
	}
	x.nextSeq++
	x.steps[key] = &stepRow{key: key, kind: "value", state: "completed", output: raw}
	return raw, nil
}

func (x *execution) child(key, workflow string, input any) (json.RawMessage, error) {
	if err := x.enter(key, false); err != nil {
		return nil, err
	}
	if len(key) > 150 {
		return nil, x.stop(fmt.Errorf("child step key %q is longer than 150 characters", key))
	}
	s := x.steps[key]
	if s == nil {
		if err := x.checkpoint(); err != nil {
			return nil, err
		}
		if err := x.insert(key, "child", "running", nil, "", map[string]string{"workflow": workflow}); err != nil {
			return nil, err
		}
		s = x.steps[key]
	}
	if s.kind != "child" {
		return nil, x.stop(fmt.Errorf("step %q was a %s and is now a child run; give the new step its own key", key, s.kind))
	}
	switch s.state {
	case "completed":
		return s.output, nil
	case "failed":
		return nil, &StepError{Key: key, Message: s.errMsg}
	case "cancelled":
		return nil, x.stop(errCancelled)
	}
	if s.childRunID == 0 {
		if s.retryAt != nil && s.retryAt.After(x.engine.now()) {
			return nil, x.parkUntil("", s.retryAt)
		}
		if err := x.crash("before-child-start", key); err != nil {
			return nil, err
		}
		id, err := x.startChild(key, workflow, input, s.generation)
		switch {
		case errors.Is(err, errCancelled), errors.Is(err, errAbandoned):
			return nil, x.stop(err)
		case errors.Is(err, ErrBusy):
			retry := x.engine.now().Add(time.Minute)
			if err = x.mark(key, "running", "Another run holds the child's concurrency key; trying again in a minute.", &retry); err != nil {
				return nil, err
			}
			return nil, x.parkUntil("", &retry)
		case errors.Is(err, ErrUnknownWorkflow), errors.Is(err, ErrInvalidInput):
			if e := x.mark(key, "failed", err.Error(), nil); e != nil {
				return nil, e
			}
			return nil, &StepError{Key: key, Message: err.Error()}
		case err != nil:
			return nil, x.stop(errAbandoned) // a database error: hand the run back and try again
		}
		s.state, s.childRunID, s.retryAt, s.errMsg = "waiting", id, nil, ""
	}
	var state, message string
	var output []byte
	if err := x.engine.DB.QueryRowContext(x.sys, `SELECT state,output,error FROM workflow_runs WHERE id=$1`, s.childRunID).Scan(&state, &output, &message); err != nil {
		return nil, x.stop(errAbandoned)
	}
	switch state {
	case "completed":
		if err := x.complete(key, output); err != nil {
			return nil, err
		}
		return output, nil
	case "failed", "cancelled":
		message = fmt.Sprintf("Child run %d %s: %s", s.childRunID, state, message)
		if err := x.mark(key, "failed", message, nil); err != nil {
			return nil, err
		}
		return nil, &StepError{Key: key, Message: message}
	}
	return nil, x.parkUntil("", nil) // the child's end wakes this run
}

// startChild starts (or, after a crash, finds) the child and links it to the step in one
// transaction that first locks this run's row and checks for a cancel request. Cancel locks the
// parent first too, so either the child exists when Cancel looks for descendants, or the cancel is
// seen here and no child starts.
func (x *execution) startChild(key, workflow string, input any, generation int) (int64, error) {
	tx, err := x.engine.DB.BeginTx(x.sys, nil)
	if err != nil {
		return 0, errAbandoned
	}
	defer tx.Rollback()
	var cancelled bool
	err = tx.QueryRowContext(x.sys, `SELECT cancel_requested_at IS NOT NULL FROM workflow_runs WHERE id=$1 AND claim=$2 FOR UPDATE`, x.run.id, x.run.claim).Scan(&cancelled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		x.lost.Store(true)
		return 0, errAbandoned
	case err != nil:
		return 0, errAbandoned
	case cancelled:
		return 0, errCancelled
	}
	idempotency := fmt.Sprintf("child:%d:%s", x.run.id, key)
	if generation > 0 {
		idempotency += fmt.Sprintf(":%d", generation)
	}
	// Starting again after a crash returns the child already started under this key.
	id, _, err := start(x.sys, tx, StartRequest{Workflow: workflow, Organization: x.run.org, Input: input,
		IdempotencyKey: idempotency, StartedBy: fmt.Sprintf("workflow:%s#%d", x.run.workflow, x.run.id), Agent: x.run.agent}, x.run.id, key)
	if err != nil {
		return 0, err
	}
	if _, err = tx.ExecContext(x.sys, `UPDATE workflow_steps SET state='waiting',child_run_id=$3,retry_at=NULL,error='' WHERE run_id=$1 AND key=$2`, x.run.id, key, id); err != nil {
		return 0, errAbandoned
	}
	if err = tx.Commit(); err != nil {
		return 0, errAbandoned
	}
	return id, nil
}
