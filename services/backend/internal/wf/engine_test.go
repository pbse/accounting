package wf_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"close.local/backend/internal/transport"
	"close.local/backend/internal/wf"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Test workflows. Each test passes a fresh ID so its call counters are its own.
type ref struct {
	ID       string   `json:"id"`
	Failures int      `json:"failures,omitempty"`
	Closes   []string `json:"closes,omitempty"`
}

var calls = struct {
	sync.Mutex
	n map[string]int
}{n: map[string]int{}}

func count(key string) int {
	calls.Lock()
	defer calls.Unlock()
	calls.n[key]++
	return calls.n[key]
}

func counted(key string) int {
	calls.Lock()
	defer calls.Unlock()
	return calls.n[key]
}

var tenantDB *sql.DB // the runtime-role database the tenant test's step reads through

var _ = wf.Define(wf.Spec[ref, map[string]string]{
	Name: "test_steps", Title: "Three steps",
	Run: func(ctx wf.Context, in ref) (map[string]string, error) {
		out := ""
		for _, key := range []string{"a", "b", "c"} {
			v, err := wf.Step(ctx, key, func(context.Context) (string, error) { count(in.ID + "/" + key); return strings.ToUpper(key), nil })
			if err != nil {
				return nil, err
			}
			out += v
		}
		return map[string]string{"out": out, "at": wf.Now(ctx).Format(time.RFC3339Nano)}, nil
	},
})

var _ = wf.Define(wf.Spec[ref, map[string]any]{
	Name: "test_wait", Title: "Wait for a reply",
	Concurrency: func(in ref) string {
		if strings.HasPrefix(in.ID, "single") {
			return "single"
		}
		return ""
	},
	Run: func(ctx wf.Context, in ref) (map[string]any, error) {
		reply, err := wf.WaitForEvent[map[string]string](ctx, "reply", "reply:"+in.ID, time.Hour)
		if errors.Is(err, wf.ErrTimeout) {
			return map[string]any{"timed_out": true}, nil
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"reply": reply["text"]}, nil
	},
})

var _ = wf.Define(wf.Spec[ref, []string]{
	Name: "test_wait_twice", Title: "Wait for two replies",
	Run: func(ctx wf.Context, in ref) ([]string, error) {
		var got []string
		for _, key := range []string{"first", "second"} {
			reply, err := wf.WaitForEvent[map[string]string](ctx, key, "reply:"+in.ID, time.Hour)
			if err != nil {
				return nil, err
			}
			got = append(got, reply["text"])
		}
		return got, nil
	},
})

var _ = wf.Define(wf.Spec[ref, string]{
	Name: "test_block", Title: "Block until cancelled",
	Run: func(ctx wf.Context, in ref) (string, error) {
		return wf.Step(ctx, "block", func(c context.Context) (string, error) {
			count(in.ID + "/block")
			<-c.Done()
			return "", c.Err()
		}, wf.Timeout(time.Minute))
	},
})

var _ = wf.Define(wf.Spec[ref, string]{
	Name: "test_flaky", Title: "Fail a few times",
	Run: func(ctx wf.Context, in ref) (string, error) {
		if in.Failures < 0 {
			return wf.Step(ctx, "flaky", func(context.Context) (string, error) { return "", wf.Permanent(errors.New("never")) })
		}
		return wf.Step(ctx, "flaky", func(context.Context) (string, error) {
			if n := count(in.ID + "/flaky"); n <= in.Failures {
				return "", errors.New("not yet")
			}
			return "done", nil
		}, wf.Attempts(2), wf.Backoff(time.Second))
	},
})

var _ = wf.Define(wf.Spec[ref, string]{
	Name: "test_effect", Title: "Post once",
	Run: func(ctx wf.Context, in ref) (string, error) {
		reference, err := wf.Effect(ctx, "post", func(context.Context) (string, error) {
			n := count(in.ID + "/post")
			switch {
			case in.Failures == 1 && n == 1:
				return "", errors.New("provider timed out")
			case in.Failures == 2 && n == 1:
				return "", wf.NotApplied(errors.New("provider refused the request"))
			}
			return "REF-" + in.ID, nil
		}, wf.Backoff(time.Second))
		if err != nil {
			return "", err
		}
		return wf.Step(ctx, "after", func(context.Context) (string, error) { return "posted " + reference, nil })
	},
})

var _ = wf.Define(wf.Spec[ref, int]{
	Name: "test_tenant", Title: "Count closes as the run's firm",
	Run: func(ctx wf.Context, in ref) (int, error) {
		return wf.Step(ctx, "count", func(c context.Context) (int, error) {
			var n int
			err := tenantDB.QueryRowContext(c, `SELECT count(*) FROM client_closes WHERE id::text=ANY($1)`, in.Closes).Scan(&n)
			return n, err
		})
	},
})

var _ = wf.Define(wf.Spec[ref, string]{
	Name: "test_sleep", Title: "Sleep an hour",
	Run: func(ctx wf.Context, in ref) (string, error) {
		if err := wf.Sleep(ctx, "nap", time.Hour); err != nil {
			return "", err
		}
		return wf.Step(ctx, "wake", func(context.Context) (string, error) { return "awake", nil })
	},
})

var _ = wf.Define(wf.Spec[ref, string]{
	Name: "test_faults", Title: "Break the rules",
	Run: func(ctx wf.Context, in ref) (string, error) {
		if in.ID == "panic" {
			panic("boom")
		}
		// Reusing a key in one run is refused, even if the workflow ignores the error.
		_, _ = wf.Step(ctx, "same", func(context.Context) (int, error) { return 1, nil })
		_, err := wf.Step(ctx, "same", func(context.Context) (int, error) { return 2, nil })
		return "", err
	},
})

type clock struct {
	sync.Mutex
	at time.Time
}

func (c *clock) Now() time.Time      { c.Lock(); defer c.Unlock(); return c.at }
func (c *clock) Add(d time.Duration) { c.Lock(); defer c.Unlock(); c.at = c.at.Add(d) }

type harness struct {
	t      *testing.T
	ctx    context.Context
	db     *sql.DB
	org    string
	clock  *clock
	engine *wf.Engine
}

func setup(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, ctx: context.Background(), db: db, org: "org_wf" + rand.Text(), clock: &clock{at: time.Now().UTC().Truncate(time.Microsecond)}}
	h.exec(`INSERT INTO organizations(id,name) VALUES($1,'Workflow fixture')`, h.org)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM workflow_events WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, err := db.Exec(q, h.org); err != nil {
				t.Error(err)
			}
		}
		db.Close()
	})
	h.engine = &wf.Engine{DB: db, Now: h.clock.Now}
	wf.OnlyOrg(h.engine, h.org)
	return h
}

func (h *harness) exec(query string, args ...any) {
	h.t.Helper()
	if _, err := h.db.Exec(query, args...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) start(workflow string, in ref) int64 {
	h.t.Helper()
	id, existing, err := wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: workflow, Organization: h.org, Input: in, IdempotencyKey: in.ID, StartedBy: "test"})
	if err != nil || existing {
		h.t.Fatalf("start %s: %v (existing %v)", workflow, err, existing)
	}
	return id
}

func (h *harness) emit(key, text string) {
	h.t.Helper()
	if _, err := wf.Emit(h.ctx, h.db, h.org, key, map[string]string{"text": text}, "test"); err != nil {
		h.t.Fatal(err)
	}
}

// drain executes runnable runs until none is left and returns how many executions happened.
func (h *harness) drain() int {
	h.t.Helper()
	for n := 0; n < 50; n++ {
		ran, err := h.engine.Tick(h.ctx)
		if err != nil {
			h.t.Fatal(err)
		}
		if !ran {
			return n
		}
	}
	h.t.Fatal("runs did not settle")
	return 0
}

type runView struct {
	state, err, output string
	steps              map[string]string
}

func (h *harness) run(id int64) runView {
	h.t.Helper()
	var v runView
	var output sql.NullString
	if err := h.db.QueryRow(`SELECT state,error,output::text FROM workflow_runs WHERE id=$1`, id).Scan(&v.state, &v.err, &output); err != nil {
		h.t.Fatal(err)
	}
	v.output, v.steps = output.String, map[string]string{}
	rows, err := h.db.Query(`SELECT key,state FROM workflow_steps WHERE run_id=$1`, id)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, state string
		if err = rows.Scan(&key, &state); err != nil {
			h.t.Fatal(err)
		}
		v.steps[key] = state
	}
	return v
}

func (h *harness) expect(id int64, state string) runView {
	h.t.Helper()
	v := h.run(id)
	if v.state != state {
		h.t.Fatalf("run %d is %s (%s), want %s; steps %v", id, v.state, v.err, state, v.steps)
	}
	return v
}

func TestCrashMidStepReplaysWithoutRepeatingCompletedSteps(t *testing.T) {
	h := setup(t)
	in := ref{ID: rand.Text()}
	id := h.start("test_steps", in)
	wf.CrashOnce(h.engine, "after-call", "b")
	h.drain()
	if v := h.expect(id, "running"); v.steps["a"] != "completed" || v.steps["b"] != "running" {
		t.Fatalf("after the crash: %v", v.steps)
	}
	if h.drain() != 0 {
		t.Fatal("a live lease must not be taken over")
	}
	h.clock.Add(3 * time.Minute)
	h.drain()
	v := h.expect(id, "completed")
	var out map[string]string
	if err := json.Unmarshal([]byte(v.output), &out); err != nil || out["out"] != "ABC" || out["at"] == "" {
		t.Fatalf("output %s", v.output)
	}
	if counted(in.ID+"/a") != 1 || counted(in.ID+"/b") != 2 || counted(in.ID+"/c") != 1 {
		t.Fatalf("a completed step ran again, or an interrupted one did not: a=%d b=%d c=%d", counted(in.ID+"/a"), counted(in.ID+"/b"), counted(in.ID+"/c"))
	}
}

func TestEventsLateSignalsTimeoutsAndDuplicates(t *testing.T) {
	h := setup(t)
	// A reply recorded before the run reaches its wait still counts.
	early := ref{ID: rand.Text()}
	earlyRun := h.start("test_wait", early)
	h.emit("reply:"+early.ID, "early")
	h.drain()
	if v := h.expect(earlyRun, "completed"); !strings.Contains(v.output, "early") {
		t.Fatalf("output %s", v.output)
	}
	// A waiting run is woken by its event, and only by its own firm's.
	waiting := ref{ID: rand.Text()}
	waitRun := h.start("test_wait", waiting)
	h.drain()
	h.expect(waitRun, "waiting")
	other := "org_wfOther" + rand.Text()
	h.exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, other)
	defer h.db.Exec(`DELETE FROM organizations WHERE id=$1`, other) // deferred calls run last-first
	defer h.db.Exec(`DELETE FROM workflow_events WHERE organization_id=$1`, other)
	if _, err := wf.Emit(h.ctx, h.db, other, "reply:"+waiting.ID, map[string]string{"text": "wrong firm"}, "test"); err != nil {
		t.Fatal(err)
	}
	if h.drain() != 0 {
		t.Fatal("another firm's event woke the run")
	}
	h.emit("reply:"+waiting.ID, "answered")
	h.drain()
	if v := h.expect(waitRun, "completed"); !strings.Contains(v.output, "answered") {
		t.Fatalf("output %s", v.output)
	}
	// An event recorded before the run started belongs to someone else's wait.
	stale := ref{ID: rand.Text()}
	h.emit("reply:"+stale.ID, "before start")
	time.Sleep(10 * time.Millisecond)
	staleRun := h.start("test_wait", stale)
	h.drain()
	h.expect(staleRun, "waiting")
	h.clock.Add(2 * time.Hour)
	h.drain()
	if v := h.expect(staleRun, "completed"); !strings.Contains(v.output, "timed_out") {
		t.Fatalf("output %s", v.output)
	}
	// Two waits on one key take two distinct events, in order; a duplicate event is not reused.
	twice := ref{ID: rand.Text()}
	twiceRun := h.start("test_wait_twice", twice)
	h.emit("reply:"+twice.ID, "one")
	h.emit("reply:"+twice.ID, "two")
	h.drain()
	if v := h.expect(twiceRun, "completed"); v.output != `["one", "two"]` {
		t.Fatalf("output %s", v.output)
	}
}

func TestEventArrivingWhileParkingIsCaughtBySweep(t *testing.T) {
	h := setup(t)
	in := ref{ID: rand.Text()}
	id := h.start("test_wait", in)
	h.drain()
	h.expect(id, "waiting")
	// Simulate the race: the event exists but its wake-up was lost.
	h.exec(`INSERT INTO workflow_events(organization_id,key,payload) VALUES($1,$2,'{"text":"raced"}')`, h.org, "reply:"+in.ID)
	if h.drain() != 0 {
		t.Fatal("nothing should wake the run before the sweep")
	}
	if err := wf.Sweep(h.ctx, h.engine); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if v := h.expect(id, "completed"); !strings.Contains(v.output, "raced") {
		t.Fatalf("output %s", v.output)
	}
}

func TestCancelWaitingAndRunningRuns(t *testing.T) {
	h := setup(t)
	waiting := h.start("test_wait", ref{ID: rand.Text()})
	h.drain()
	if err := wf.Cancel(h.ctx, h.db, waiting, "test", "client withdrew"); err != nil {
		t.Fatal(err)
	}
	if v := h.expect(waiting, "cancelled"); v.steps["reply"] != "cancelled" {
		t.Fatalf("steps %v", v.steps)
	}
	if err := wf.Cancel(h.ctx, h.db, waiting, "test", "again"); !errors.Is(err, wf.ErrNotAllowed) {
		t.Fatalf("cancelling a finished run: %v", err)
	}
	h.emit("reply:x", "late")
	if h.drain() != 0 {
		t.Fatal("a cancelled run ran")
	}

	h.engine.Lease = 400 * time.Millisecond
	in := ref{ID: rand.Text()}
	running := h.start("test_block", in)
	done := make(chan struct{})
	go func() { defer close(done); h.engine.Tick(h.ctx) }()
	for deadline := time.Now().Add(5 * time.Second); counted(in.ID+"/block") == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the blocking step never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := wf.Cancel(h.ctx, h.db, running, "test", "no longer needed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a running run did not stop after cancellation")
	}
	if v := h.expect(running, "cancelled"); v.steps["block"] != "cancelled" {
		t.Fatalf("steps %v", v.steps)
	}
}

func TestRetriesBackoffPermanentFailureAndRetryStep(t *testing.T) {
	h := setup(t)
	recovers := h.start("test_flaky", ref{ID: rand.Text(), Failures: 1})
	h.drain()
	h.expect(recovers, "waiting")
	h.clock.Add(time.Second)
	h.drain()
	h.expect(recovers, "completed")

	in := ref{ID: rand.Text(), Failures: 3}
	exhausted := h.start("test_flaky", in)
	h.drain()
	h.clock.Add(time.Second)
	h.drain()
	if v := h.expect(exhausted, "failed"); v.steps["flaky"] != "failed" || !strings.Contains(v.err, "not yet") {
		t.Fatalf("%+v", v)
	}
	if err := wf.RetryStep(h.ctx, h.db, exhausted, "flaky", "", ""); err == nil {
		t.Fatal("a retry needs who and why")
	}
	if err := wf.RetryStep(h.ctx, h.db, exhausted, "flaky", "test", "provider fixed"); err != nil {
		t.Fatal(err)
	}
	h.drain()
	h.clock.Add(time.Second)
	h.drain()
	h.expect(exhausted, "completed")
	if counted(in.ID+"/flaky") != 4 {
		t.Fatalf("attempts %d", counted(in.ID+"/flaky"))
	}

	permanent := h.start("test_flaky", ref{ID: rand.Text(), Failures: -1})
	h.drain()
	h.expect(permanent, "failed")
}

func TestDuplicateStartsAndConcurrencyKeys(t *testing.T) {
	h := setup(t)
	in := ref{ID: "single" + rand.Text()}
	first := h.start("test_wait", in)
	again, existing, err := wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: "test_wait", Organization: h.org, Input: in, IdempotencyKey: in.ID, StartedBy: "test"})
	if err != nil || !existing || again != first {
		t.Fatalf("a repeated start must return the first run: %d %v %v", again, existing, err)
	}
	second := ref{ID: "single" + rand.Text()}
	if _, _, err = wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: "test_wait", Organization: h.org, Input: second, IdempotencyKey: second.ID, StartedBy: "test"}); !errors.Is(err, wf.ErrBusy) {
		t.Fatalf("a second active run on one concurrency key: %v", err)
	}
	if err = wf.Cancel(h.ctx, h.db, first, "test", "make room"); err != nil {
		t.Fatal(err)
	}
	h.start("test_wait", second)
	if _, _, err = wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: "test_wait", Organization: h.org, Input: json.RawMessage(`{"id":"x","extra":1}`), IdempotencyKey: "extra", StartedBy: "test"}); err == nil {
		t.Fatal("unknown input fields must be refused at start")
	}
	if _, _, err = wf.Start(h.ctx, h.db, wf.StartRequest{Workflow: "test_missing", Organization: h.org, IdempotencyKey: "x", StartedBy: "test"}); !errors.Is(err, wf.ErrUnknownWorkflow) {
		t.Fatalf("unknown workflow: %v", err)
	}
}

func TestEffectOutcomesAreNeverGuessed(t *testing.T) {
	h := setup(t)
	// The worker dies mid-call: the effect becomes unknown and is not called again by itself.
	crashed := ref{ID: rand.Text()}
	crashedRun := h.start("test_effect", crashed)
	wf.CrashOnce(h.engine, "after-call", "post")
	h.drain()
	h.clock.Add(3 * time.Minute)
	h.drain()
	if v := h.expect(crashedRun, "waiting"); v.steps["post"] != "unknown" {
		t.Fatalf("steps %v", v.steps)
	}
	h.clock.Add(24 * time.Hour)
	if h.drain() != 0 || counted(crashed.ID+"/post") != 1 {
		t.Fatal("an unknown effect ran again without a person")
	}
	if err := wf.RetryStep(h.ctx, h.db, crashedRun, "post", "test", "just retry"); !errors.Is(err, wf.ErrNotAllowed) {
		t.Fatalf("an unknown effect must be resolved, not retried: %v", err)
	}
	if err := wf.Resolve(h.ctx, h.db, wf.Resolution{RunID: crashedRun, Key: "post", Applied: true, Output: json.RawMessage(`"REF-FOUND"`), By: "test", Note: "found REF-FOUND in the provider"}); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if v := h.expect(crashedRun, "completed"); v.output != `"posted REF-FOUND"` || counted(crashed.ID+"/post") != 1 {
		t.Fatalf("output %s, calls %d", v.output, counted(crashed.ID+"/post"))
	}

	// An ambiguous error is unknown; a person who confirms nothing happened lets it run again.
	ambiguous := ref{ID: rand.Text(), Failures: 1}
	ambiguousRun := h.start("test_effect", ambiguous)
	h.drain()
	if v := h.expect(ambiguousRun, "waiting"); v.steps["post"] != "unknown" {
		t.Fatalf("steps %v", v.steps)
	}
	if err := wf.Resolve(h.ctx, h.db, wf.Resolution{RunID: ambiguousRun, Key: "post", Applied: false, By: "test", Note: "no such request at the provider"}); err != nil {
		t.Fatal(err)
	}
	h.drain()
	h.expect(ambiguousRun, "completed")

	// A definite refusal (NotApplied) is retried like any step.
	refused := ref{ID: rand.Text(), Failures: 2}
	refusedRun := h.start("test_effect", refused)
	h.drain()
	h.clock.Add(time.Second)
	h.drain()
	if h.expect(refusedRun, "completed"); counted(refused.ID+"/post") != 2 {
		t.Fatalf("calls %d", counted(refused.ID+"/post"))
	}
}

func TestLostLeaseWritesNothingAndRunRecovers(t *testing.T) {
	h := setup(t)
	in := ref{ID: rand.Text()}
	id := h.start("test_steps", in)
	wf.Hook(h.engine, func(point, key string) {
		if point == "after-call" && key == "b" && counted(in.ID+"/b") == 1 {
			h.exec(`UPDATE workflow_runs SET claim='another-worker' WHERE id=$1`, id)
		}
	})
	h.drain()
	if v := h.expect(id, "running"); v.steps["b"] != "running" || v.steps["c"] != "" {
		t.Fatalf("a worker without the lease wrote: %v", v.steps)
	}
	h.clock.Add(3 * time.Minute)
	h.drain()
	h.expect(id, "completed")
}

func TestSleepFaultsSuspendedFirmsAndUnregisteredWorkflows(t *testing.T) {
	h := setup(t)
	sleeper := h.start("test_sleep", ref{ID: rand.Text()})
	h.drain()
	h.expect(sleeper, "waiting")
	h.clock.Add(59 * time.Minute)
	if h.drain() != 0 {
		t.Fatal("woke early")
	}
	h.clock.Add(time.Minute)
	h.drain()
	h.expect(sleeper, "completed")

	if v := h.run(h.start("test_faults", ref{ID: "panic"})); v.state != "queued" {
		t.Fatal(v.state)
	}
	reused := h.start("test_faults", ref{ID: rand.Text()})
	h.drain()
	if v := h.expect(reused, "failed"); !strings.Contains(v.err, "reached twice") {
		t.Fatalf("error %q", v.err)
	}
	var panicked string
	h.db.QueryRow(`SELECT error FROM workflow_runs WHERE organization_id=$1 AND idempotency_key='panic'`, h.org).Scan(&panicked)
	if !strings.Contains(panicked, "boom") {
		t.Fatalf("panic error %q", panicked)
	}

	h.exec(`UPDATE organizations SET enabled=false WHERE id=$1`, h.org)
	paused := h.start("test_steps", ref{ID: rand.Text()})
	if h.drain() != 0 {
		t.Fatal("a suspended firm's run was claimed")
	}
	h.exec(`UPDATE organizations SET enabled=true WHERE id=$1`, h.org)
	h.drain()
	h.expect(paused, "completed")

	var missing int64
	if err := h.db.QueryRow(`INSERT INTO workflow_runs(organization_id,workflow,idempotency_key,started_by,wake_at) VALUES($1,'test_not_in_build','x','test','-infinity') RETURNING id`, h.org).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	h.drain()
	if v := h.expect(missing, "waiting"); !strings.Contains(v.err, "not registered") {
		t.Fatalf("error %q", v.err)
	}
}

// Steps run as the run's firm: through the runtime role, row-level security hides other firms.
func TestStepsAreConfinedToTheirFirm(t *testing.T) {
	h := setup(t)
	if _, err := h.db.Exec(`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='close_wf_probe') THEN CREATE ROLE close_wf_probe LOGIN PASSWORD 'probe-only' NOSUPERUSER NOBYPASSRLS; END IF; END $$;
		GRANT USAGE ON SCHEMA public TO close_wf_probe; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO close_wf_probe; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA public TO close_wf_probe`); err != nil {
		t.Fatal(err)
	}
	other := "org_wfB" + rand.Text()
	h.exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, other)
	var mine, theirs string
	if err := h.db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Same client','2026-09') RETURNING id::text`, h.org).Scan(&mine); err != nil {
		t.Fatal(err)
	}
	if err := h.db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Same client','2026-09') RETURNING id::text`, other).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	defer h.db.Exec(`DELETE FROM organizations WHERE id=$1`, other)
	defer h.db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, theirs)

	probe, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	probe.User = url.UserPassword("close_wf_probe", "probe-only")
	runtime, err := transport.OpenTenantDB(probe.String(), "*")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	tenantDB = runtime
	engine := &wf.Engine{DB: runtime, Now: h.clock.Now}
	wf.OnlyOrg(engine, h.org)
	id := h.start("test_tenant", ref{ID: rand.Text(), Closes: []string{mine, theirs}})
	if ran, err := engine.Tick(h.ctx); err != nil || !ran {
		t.Fatalf("tick: %v %v", ran, err)
	}
	if v := h.expect(id, "completed"); v.output != "1" {
		t.Fatalf("the step saw %s of the two firms' closes; it must see only its own", v.output)
	}
}

// Review regressions.

func TestCancelDuringAnUncertainEffectEndsTheRunAndKeepsTheEffectUnknown(t *testing.T) {
	h := setup(t)
	in := ref{ID: rand.Text(), Failures: 1}
	id := h.start("test_effect", in)
	wf.Hook(h.engine, func(point, key string) {
		if point == "before-call" && key == "post" && counted(in.ID+"/post") == 0 {
			if err := wf.Cancel(h.ctx, h.db, id, "test", "client withdrew mid-call"); err != nil {
				t.Error(err)
			}
		}
	})
	h.drain()
	if v := h.expect(id, "cancelled"); v.steps["post"] != "unknown" {
		t.Fatalf("the effect's outcome must stay unknown for reconciliation: %v", v.steps)
	}
}

func TestEventAfterTheDeadlineDoesNotSatisfyTheWait(t *testing.T) {
	h := setup(t)
	h.clock.Add(-3 * time.Hour) // the wait's deadline falls before the event is recorded
	in := ref{ID: rand.Text()}
	id := h.start("test_wait", in)
	h.drain()
	h.emit("reply:"+in.ID, "too late")
	if n := h.drain(); n > 1 {
		t.Fatalf("a late event must wake the run at most once, not keep it spinning (%d executions)", n)
	}
	h.expect(id, "waiting")
	h.clock.Add(2 * time.Hour)
	h.drain()
	if v := h.expect(id, "completed"); !strings.Contains(v.output, "timed_out") {
		t.Fatalf("output %s", v.output)
	}
}

func TestIncompleteSavedValueIsRegenerated(t *testing.T) {
	h := setup(t)
	id := h.start("test_steps", ref{ID: rand.Text()})
	h.exec(`INSERT INTO workflow_steps(run_id,key,organization_id,seq,kind,state) VALUES($1,'$now/1',$2,99,'value','running')`, id, h.org)
	h.drain()
	var out map[string]string
	if err := json.Unmarshal([]byte(h.expect(id, "completed").output), &out); err != nil || out["at"] == "" || strings.HasPrefix(out["at"], "0001-") {
		t.Fatalf("Now replayed an unsaved value: %v %v", out, err)
	}
}
