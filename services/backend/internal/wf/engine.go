package wf

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// Engine claims runnable runs and executes them. DB must admit system work for the engine's own
// bookkeeping: open it with the "*" fallback (transport.OpenTenantDB(url, "*"), as cmd/worker does).
// Steps still run as the run's firm, because the engine puts that firm on their context.
type Engine struct {
	DB          *sql.DB
	Concurrency int              // runs executed at once; default 4
	Lease       time.Duration    // a claim not renewed for this long is taken over; default 2m
	Now         func() time.Time // clock for sleeps, deadlines and leases; default time.Now

	// OnlyOrganization, when set, limits the engine to one firm's runs and triggers: for tests and
	// tools that share a database. Platform runs and schedules are then left alone.
	OnlyOrganization string

	hook func(point, key string) error // tests: simulate a crash at a step boundary

	cursorMu              sync.Mutex
	cursors               map[string]int64 // event trigger scan position, per workflow and pattern
	eventPage, eventPages int              // tests: smaller event scans
}

// Run executes workflow runs until ctx ends, with WF_CONCURRENCY runs at once (default 4).
func Run(ctx context.Context, db *sql.DB) {
	concurrency, _ := strconv.Atoi(os.Getenv("WF_CONCURRENCY"))
	(&Engine{DB: db, Concurrency: concurrency}).Run(ctx)
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) lease() time.Duration {
	if e.Lease > 0 {
		return e.Lease
	}
	return 2 * time.Minute
}

// Run polls for work every 2 seconds while busy and every 10 seconds when idle.
func (e *Engine) Run(ctx context.Context) {
	concurrency := e.Concurrency
	if concurrency <= 0 {
		concurrency = 4
	}
	slots := make(chan struct{}, concurrency)
	var running sync.WaitGroup
	var swept, triggered, beat time.Time
	for {
		busy := false
		for len(slots) < cap(slots) {
			run, err := e.claim(ctx)
			if err != nil {
				if ctx.Err() == nil {
					log.Print("Workflow runs could not be claimed: ", err)
				}
				break
			}
			if run == nil {
				break
			}
			busy = true
			slots <- struct{}{}
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-slots }()
				e.execute(ctx, run)
			}()
		}
		if time.Since(beat) >= 30*time.Second && e.OnlyOrganization == "" {
			// The engine's own sign of life for the admin overview, apart from the worker's.
			if _, err := e.DB.ExecContext(ctx, `INSERT INTO worker_heartbeats(loop,beat_at) VALUES('workflow_engine',now()) ON CONFLICT (loop) DO UPDATE SET beat_at=now()`); err != nil && ctx.Err() == nil {
				log.Print("Workflow engine heartbeat could not be recorded: ", err)
			}
			beat = time.Now()
		}
		if time.Since(triggered) >= 30*time.Second {
			e.triggers(ctx)
			triggered = time.Now()
		}
		if time.Since(swept) >= 10*time.Second {
			if err := e.sweep(ctx); err != nil && ctx.Err() == nil {
				log.Print("Waiting workflow runs could not be checked for events: ", err)
			}
			swept = time.Now()
		}
		delay := 10 * time.Second
		if busy {
			delay = 2 * time.Second
		}
		select {
		case <-ctx.Done():
			running.Wait()
			return
		case <-time.After(delay):
		}
	}
}

// Tick claims and executes at most one runnable run before returning; tests and tools use it.
func (e *Engine) Tick(ctx context.Context) (bool, error) {
	run, err := e.claim(ctx)
	if err != nil || run == nil {
		return false, err
	}
	e.execute(ctx, run)
	return true, nil
}

type claimedRun struct {
	id                              int64
	org, workflow, claim, startedBy string
	agent                           string
	input                           json.RawMessage
	createdAt                       time.Time
	cancelRequested                 bool
}

// A run is runnable when it is queued or waiting and its wake time has come, or when it is running
// under a lease nobody renewed (its worker died). Suspended firms' runs are left alone.
func (e *Engine) claim(ctx context.Context) (*claimedRun, error) {
	now := e.now()
	run := claimedRun{claim: rand.Text()}
	var org sql.NullString
	var input []byte
	// wake_at is cleared while a run executes, so a wake requested meanwhile (Resolve) survives parking.
	err := e.DB.QueryRowContext(ctx, `UPDATE workflow_runs SET state='running',claim=$1,claimed_at=$2,attempts=attempts+1,started_at=COALESCE(started_at,$2),updated_at=now(),wake_at=NULL
 WHERE id=(SELECT r.id FROM workflow_runs r LEFT JOIN organizations o ON o.id=r.organization_id
  WHERE ((r.state IN ('queued','waiting') AND r.wake_at<=$2) OR (r.state='running' AND r.claimed_at<$3))
   AND (r.organization_id IS NULL OR o.enabled) AND ($4='' OR r.organization_id=$4)
  ORDER BY r.wake_at NULLS LAST,r.id FOR UPDATE OF r SKIP LOCKED LIMIT 1)
 RETURNING id,organization_id,workflow,input,created_at,cancel_requested_at IS NOT NULL,started_by,agent`,
		run.claim, now, now.Add(-e.lease()), e.OnlyOrganization).Scan(&run.id, &org, &run.workflow, &input, &run.createdAt, &run.cancelRequested, &run.startedBy, &run.agent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	run.org, run.input = org.String, input
	return &run, nil
}

func (e *Engine) execute(ctx context.Context, run *claimedRun) {
	// Bookkeeping must outlive a stopping worker, so an effect that finishes is still recorded;
	// the claim token, not the context, keeps a worker that lost its lease from writing.
	sys := context.WithoutCancel(ctx)
	w := lookup(run.workflow)
	if w == nil {
		// During a rolling deploy another build may register it; hand the run back.
		e.release(sys, run, e.now().Add(time.Minute), fmt.Sprintf("Workflow %s is not registered in build %s", run.workflow, CodeVersion))
		return
	}
	steps, next, err := e.loadSteps(sys, run.id)
	if err != nil {
		log.Printf("Workflow run %d could not load its steps: %v", run.id, err)
		e.release(sys, run, e.now().Add(time.Minute), "")
		return
	}
	runCtx, stopRun := context.WithCancel(ctx)
	defer stopRun()
	firm := runCtx
	if run.org != "" {
		firm = auth.WithActor(firm, &auth.Claims{Organization: run.org, RegisteredClaims: jwt.RegisteredClaims{Subject: fmt.Sprintf("workflow:%s#%d", run.workflow, run.id)}})
	}
	firm = auth.WithAgent(firm, fmt.Sprintf("workflow:%s#%d", run.workflow, run.id))
	x := &execution{Context: firm, sys: sys, engine: e, run: run, steps: steps, nextSeq: next, visited: map[string]bool{}, counters: map[string]int{}}
	if run.cancelRequested {
		x.halt = errCancelled
	}
	stopHeartbeat := e.heartbeat(sys, x, stopRun)
	out, runErr := x.call(w)
	stopHeartbeat()
	e.settle(x, out, runErr)
}

func (e *Engine) loadSteps(ctx context.Context, runID int64) (map[string]*stepRow, int, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT key,kind,state,attempts,retry_at,wake_at,output,error,seq,COALESCE(input->'skipped','[]'::jsonb),COALESCE(child_run_id,0),COALESCE((input->>'generation')::int,0) FROM workflow_steps WHERE run_id=$1`, runID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	steps, next := map[string]*stepRow{}, 1
	for rows.Next() {
		var s stepRow
		var retry, wake sql.NullTime
		var output, skipped []byte
		var seq int
		if err = rows.Scan(&s.key, &s.kind, &s.state, &s.attempts, &retry, &wake, &output, &s.errMsg, &seq, &skipped, &s.childRunID, &s.generation); err != nil {
			return nil, 0, err
		}
		if err = json.Unmarshal(skipped, &s.skipped); err != nil {
			return nil, 0, err
		}
		if retry.Valid {
			s.retryAt = &retry.Time
		}
		if wake.Valid {
			s.wakeAt = &wake.Time
		}
		s.output = output
		steps[s.key] = &s
		next = max(next, seq+1)
	}
	return steps, next, rows.Err()
}

// heartbeat renews the lease every quarter lease. It stops the run's context when a person asks
// to cancel or when another worker took the lease over.
func (e *Engine) heartbeat(ctx context.Context, x *execution, stopRun context.CancelFunc) func() {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(e.lease() / 4)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var cancelled bool
				err := e.DB.QueryRowContext(ctx, `UPDATE workflow_runs SET claimed_at=$3 WHERE id=$1 AND claim=$2 RETURNING cancel_requested_at IS NOT NULL`, x.run.id, x.run.claim, e.now()).Scan(&cancelled)
				if errors.Is(err, sql.ErrNoRows) {
					x.lost.Store(true)
					stopRun()
					return
				}
				if err == nil && cancelled {
					x.cancelled.Store(true)
					stopRun()
				}
			}
		}
	}()
	return func() { close(done); wg.Wait() }
}

// settle records how the run ended this time: parked, finished or handed back.
func (e *Engine) settle(x *execution, out json.RawMessage, runErr error) {
	run := x.run
	switch {
	case errors.Is(x.halt, errCrashed), x.lost.Load():
		// Simulated crash, or another worker owns the run now: write nothing.
	case errors.Is(x.halt, errAbandoned):
		e.release(x.sys, run, time.Time{}, "")
	case errors.Is(x.halt, errCancelled) || (x.cancelled.Load() && runErr != nil && x.halt == nil):
		e.finish(x, "cancelled", nil, "")
	case errors.Is(x.halt, errSuspended):
		e.park(x)
	case x.halt != nil:
		// A structural error (a reused key, a step that changed kind, a result that no longer fits)
		// fails the run even if the workflow code ignored it.
		e.finish(x, "failed", nil, x.halt.Error())
	case runErr != nil:
		e.finish(x, "failed", nil, runErr.Error())
	default:
		if len(out) > maxJSON {
			e.finish(x, "failed", nil, fmt.Sprintf("Run output is %d bytes; the limit is %d", len(out), maxJSON))
			return
		}
		e.finish(x, "completed", out, "")
	}
}

func (e *Engine) finish(x *execution, state string, out json.RawMessage, message string) {
	tx, err := e.DB.BeginTx(x.sys, nil)
	if err != nil {
		log.Printf("Workflow run %d could not finish: %v", x.run.id, err)
		return
	}
	defer tx.Rollback()
	var output any
	if out != nil {
		output = string(out)
	}
	// A cancel requested while the run was executing wins, even over a last step that succeeded:
	// Cancel promised the run would stop. Completed steps keep their results.
	err = tx.QueryRowContext(x.sys, `UPDATE workflow_runs SET finished_at=now(),updated_at=now(),claim=NULL,wake_at=NULL,waiting_on='',
 state=CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE $3 END,
 output=CASE WHEN cancel_requested_at IS NOT NULL THEN NULL ELSE $4::jsonb END,
 error=CASE WHEN cancel_requested_at IS NOT NULL THEN '' ELSE $5 END
 WHERE id=$1 AND claim=$2 RETURNING state`, x.run.id, x.run.claim, state, output, truncate(message)).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return // another worker holds the run now
	}
	if err == nil && state == "cancelled" {
		// An unknown effect stays unknown: cancelling a run never settles what a provider did.
		_, err = tx.ExecContext(x.sys, `UPDATE workflow_steps SET state='cancelled',finished_at=now() WHERE run_id=$1 AND state IN ('running','waiting')`, x.run.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("Workflow run %d could not be marked %s: %v", x.run.id, state, err)
		return
	}
	e.ended(x.sys, x.run.id, state == "cancelled")
}

// ended does what a run's end requires after its transaction commits: a cancelled run's remaining
// descendants are cancelled (one may have started while an ancestor was being cancelled), and the
// parent is woken.
func (e *Engine) ended(ctx context.Context, runID int64, cancelled bool) {
	if cancelled {
		err := inTx(ctx, e.DB, func(tx *sql.Tx) error {
			return cancelTree(ctx, tx, runID, false, "the parent run was cancelled", "workflow engine")
		})
		if err != nil {
			log.Printf("Workflow run %d was cancelled but its children could not be yet: %v", runID, err)
		}
	}
	e.wakeParent(ctx, runID)
}

// wakeParent wakes the parent of a run that ended. It runs after the run's own transaction commits,
// so no transaction holds a child's row while taking its parent's (Cancel takes them the other way
// round); the sweep catches a wake-up lost in between.
func (e *Engine) wakeParent(ctx context.Context, runID int64) {
	if _, err := e.DB.ExecContext(ctx, wakeParent, runID); err != nil {
		log.Printf("Workflow run %d could not wake its parent: %v", runID, err)
	}
}

// park releases the claim until the run's wake time (nil: until a person or event wakes it). An
// event committed while the run was still running is caught here or by the next sweep. A cancel
// requested while the run was running is applied here, in the same statement, so it cannot slip
// between this check and Cancel (which only records the request while the run is running). Likewise
// a wake requested while it ran (Resolve sets wake_at to -infinity) is kept. Waiting for the row
// lock, Postgres re-reads the row's latest values, so either order of the two is safe.
func (e *Engine) park(x *execution) {
	var wake any
	if x.wakeAt != nil {
		wake = *x.wakeAt
	}
	tx, err := e.DB.BeginTx(x.sys, nil)
	if err != nil {
		log.Printf("Workflow run %d could not be parked: %v", x.run.id, err)
		return
	}
	defer tx.Rollback()
	// SET expressions see the row's old values, so the event check names the new key ($4) itself.
	var state string
	err = tx.QueryRowContext(x.sys, `UPDATE workflow_runs r SET claim=NULL,updated_at=now(),
 state=CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'waiting' END,
 finished_at=CASE WHEN cancel_requested_at IS NOT NULL THEN now() END,
 waiting_on=CASE WHEN cancel_requested_at IS NOT NULL THEN '' ELSE $4 END,
 wake_at=CASE WHEN cancel_requested_at IS NOT NULL THEN NULL
  WHEN r.wake_at='-infinity' THEN '-infinity'::timestamptz
  WHEN $4<>'' AND EXISTS(`+unconsumed("$4", "$3::timestamptz")+`) THEN '-infinity'::timestamptz ELSE $3::timestamptz END
 WHERE id=$1 AND claim=$2 RETURNING state`, x.run.id, x.run.claim, wake, x.waitingOn).Scan(&state)
	if err == nil && state == "cancelled" {
		// An unknown effect stays unknown for reconciliation.
		_, err = tx.ExecContext(x.sys, `UPDATE workflow_steps SET state='cancelled',finished_at=now() WHERE run_id=$1 AND state IN ('running','waiting')`, x.run.id)
	}
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("Workflow run %d could not be parked: %v", x.run.id, err)
		return
	}
	if state == "cancelled" {
		e.ended(x.sys, x.run.id, true)
	}
}

// release hands a claimed run back without recording a result; a zero wake means at once. A cancel
// requested meanwhile is applied instead, as in park.
func (e *Engine) release(ctx context.Context, run *claimedRun, wake time.Time, message string) {
	var at any
	if !wake.IsZero() {
		at = wake
	}
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("Workflow run %d could not be released: %v", run.id, err)
		return
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `UPDATE workflow_runs SET claim=NULL,updated_at=now(),
 state=CASE WHEN cancel_requested_at IS NOT NULL THEN 'cancelled' ELSE 'waiting' END,
 finished_at=CASE WHEN cancel_requested_at IS NOT NULL THEN now() END,
 waiting_on=CASE WHEN cancel_requested_at IS NOT NULL THEN '' ELSE waiting_on END,
 wake_at=CASE WHEN cancel_requested_at IS NOT NULL THEN NULL ELSE COALESCE($3::timestamptz,'-infinity'::timestamptz) END,
 error=CASE WHEN cancel_requested_at IS NOT NULL OR $4='' THEN error ELSE $4 END
 WHERE id=$1 AND claim=$2 RETURNING state`, run.id, run.claim, at, truncate(message)).Scan(&state)
	if err == nil && state == "cancelled" {
		_, err = tx.ExecContext(ctx, `UPDATE workflow_steps SET state='cancelled',finished_at=now() WHERE run_id=$1 AND state IN ('running','waiting')`, run.id)
	}
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("Workflow run %d could not be released: %v", run.id, err)
		return
	}
	if state == "cancelled" {
		e.ended(ctx, run.id, true)
	}
}

// unconsumed matches an event with the given key for run r that the run has neither consumed nor
// passed over as malformed, that was recorded after the run started and, when the wait has a
// deadline, no later than the deadline.
func unconsumed(key, deadline string) string {
	return `SELECT 1 FROM workflow_events e WHERE e.key=` + key + ` AND e.organization_id IS NOT DISTINCT FROM r.organization_id AND e.created_at>=r.created_at
 AND (` + deadline + ` IS NULL OR e.created_at<=` + deadline + `)
 AND NOT EXISTS(SELECT 1 FROM workflow_steps s WHERE s.run_id=r.id AND s.kind='event' AND s.state='completed' AND (s.output->>'event_id')::bigint=e.id)
 AND NOT EXISTS(SELECT 1 FROM workflow_steps s WHERE s.run_id=r.id AND s.input->'skipped' @> to_jsonb(e.id))`
}

// sweep wakes waiting runs whose event arrived in the moment between their check and their parking,
// and parents whose child ended without its wake-up reaching them.
func (e *Engine) sweep(ctx context.Context) error {
	_, err := e.DB.ExecContext(ctx, `UPDATE workflow_runs r SET wake_at='-infinity',updated_at=now() WHERE r.state='waiting' AND r.waiting_on<>'' AND r.wake_at>'-infinity' AND ($1='' OR r.organization_id=$1) AND EXISTS(`+unconsumed("r.waiting_on", "r.wake_at")+`)`, e.OnlyOrganization)
	if err == nil {
		_, err = e.DB.ExecContext(ctx, `UPDATE workflow_runs r SET wake_at='-infinity',updated_at=now() WHERE r.state='waiting' AND r.wake_at IS DISTINCT FROM '-infinity' AND ($1='' OR r.organization_id=$1)
 AND EXISTS(SELECT 1 FROM workflow_steps s JOIN workflow_runs c ON c.id=s.child_run_id WHERE s.run_id=r.id AND s.kind='child' AND s.state='waiting' AND c.state IN ('completed','failed','cancelled'))`, e.OnlyOrganization)
	}
	return err
}

func affected(result sql.Result) bool {
	n, err := result.RowsAffected()
	return err == nil && n > 0
}

func truncate(message string) string {
	if len(message) > 2000 {
		return message[:2000]
	}
	return message
}

// call runs the workflow function; a panic outside any step fails the run.
func (x *execution) call(w *workflow) (out json.RawMessage, err error) {
	defer func() {
		if p := recover(); p != nil {
			log.Printf("Workflow %s run %d panicked: %v\n%s", w.Name, x.run.id, p, debug.Stack())
			out, err = nil, fmt.Errorf("workflow panicked: %v", p)
		}
	}()
	if x.halt != nil {
		return nil, x.halt
	}
	return w.run(x, x.run.input)
}

type stepRow struct {
	key, kind, state string
	attempts         int
	retryAt, wakeAt  *time.Time
	output           json.RawMessage
	errMsg           string
	skipped          []int64 // events a wait passed over because their payload did not fit
	childRunID       int64
	generation       int // a child step retried by a person starts a new child under a new key
}

type execution struct {
	context.Context                 // the run's firm: what workflow code and steps run under
	sys             context.Context // system work for the engine's own bookkeeping
	engine          *Engine
	run             *claimedRun
	steps           map[string]*stepRow
	nextSeq         int
	visited         map[string]bool
	counters        map[string]int
	halt            error // sticky once set: every later primitive returns it
	waitingOn       string
	wakeAt          *time.Time
	lost, cancelled atomic.Bool
}

func (x *execution) exec() *execution { return x }

var (
	errSuspended = errors.New("workflow run suspended until it is woken")
	errAbandoned = errors.New("workflow run abandoned: the worker is stopping or lost its lease")
	errCancelled = errors.New("workflow run cancelled")
	errCrashed   = errors.New("workflow worker crashed (simulated)")
)
