package wf

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is a *sql.DB or *sql.Tx, so a run can be started or an event emitted inside the
// transaction that caused it.
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// StartRequest names a run to start.
type StartRequest struct {
	Workflow       string
	Organization   string // the firm; empty for platform work
	Input          any    // a value, or json.RawMessage; must decode into the workflow's input type
	IdempotencyKey string // starting the same firm, workflow and key again returns the first run
	StartedBy      string // "user:<id>", "staff:<email>", "schedule", "event:<id>" …
	Agent          string
}

// ErrInvalidInput means a run's input does not fit its workflow.
var ErrInvalidInput = errors.New("input does not fit the workflow")

// Start queues a run, or returns the existing run (existing=true) for the same firm, workflow and
// idempotency key, whatever its input was. ErrBusy means another active run holds the workflow's
// concurrency key; inside a transaction that error aborts the transaction.
func Start(ctx context.Context, q Querier, req StartRequest) (id int64, existing bool, err error) {
	return start(ctx, q, req, 0, "")
}

func start(ctx context.Context, q Querier, req StartRequest, parent int64, parentStep string) (id int64, existing bool, err error) {
	w := lookup(req.Workflow)
	if w == nil {
		return 0, false, fmt.Errorf("%w: %s", ErrUnknownWorkflow, req.Workflow)
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 200 || strings.TrimSpace(req.StartedBy) == "" {
		return 0, false, errors.New("a run needs an idempotency key of 1-200 characters and who started it")
	}
	raw, err := asJSON(req.Input)
	if err != nil {
		return 0, false, err
	}
	if err = w.validate(raw); err != nil {
		return 0, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	concurrency, err := w.concurrency(raw)
	if err != nil {
		return 0, false, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if len(concurrency) > 200 {
		return 0, false, errors.New("concurrency key is longer than 200 characters")
	}
	err = q.QueryRowContext(ctx, `INSERT INTO workflow_runs(organization_id,workflow,code_version,idempotency_key,concurrency_key,input,started_by,agent,wake_at,parent_run_id,parent_step)
 VALUES(NULLIF($1,''),$2,$3,$4,NULLIF($5,''),$6,$7,$8,'-infinity',NULLIF($9,0),NULLIF($10,''))
 ON CONFLICT (coalesce(organization_id,''),workflow,idempotency_key) DO NOTHING RETURNING id`,
		req.Organization, req.Workflow, CodeVersion, req.IdempotencyKey, concurrency, string(raw), req.StartedBy, req.Agent, parent, parentStep).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		err = q.QueryRowContext(ctx, `SELECT id FROM workflow_runs WHERE coalesce(organization_id,'')=$1 AND workflow=$2 AND idempotency_key=$3`, req.Organization, req.Workflow, req.IdempotencyKey).Scan(&id)
		return id, true, err
	case violates(err, "workflow_runs_concurrency"):
		return 0, false, ErrBusy
	}
	return id, false, err
}

// Emit records an event and wakes the firm's runs waiting for it. Call it inside the transaction
// that caused the event, so the event exists exactly when the change does. Keys starting with "wf."
// are the engine's own (approvals) and are refused here.
func Emit(ctx context.Context, q Querier, org, key string, payload any, actor string) (int64, error) {
	if strings.HasPrefix(key, "wf.") {
		return 0, errors.New("event keys starting with wf. are reserved for the workflow engine")
	}
	return emit(ctx, q, org, key, payload, actor)
}

// EmitForClose is Emit for the firm that owns the close, inside the transaction that changed it.
func EmitForClose(ctx context.Context, q Querier, closeID, key string, payload any, actor string) (int64, error) {
	var org sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT organization_id FROM client_closes WHERE id::text=$1`, closeID).Scan(&org); err != nil {
		return 0, err
	}
	return Emit(ctx, q, org.String, key, payload, actor)
}

func emit(ctx context.Context, q Querier, org, key string, payload any, actor string) (int64, error) {
	if key == "" || len(key) > 200 {
		return 0, errors.New("an event key needs 1-200 characters")
	}
	raw, err := asJSON(payload)
	if err != nil {
		return 0, err
	}
	var id int64
	if err = q.QueryRowContext(ctx, `INSERT INTO workflow_events(organization_id,key,payload,actor) VALUES(NULLIF($1,''),$2,$3,$4) RETURNING id`, org, key, string(raw), actor).Scan(&id); err != nil {
		return 0, err
	}
	_, err = q.ExecContext(ctx, `UPDATE workflow_runs SET wake_at='-infinity',updated_at=now() WHERE state='waiting' AND waiting_on=$2 AND organization_id IS NOT DISTINCT FROM NULLIF($1,'')`, org, key)
	return id, err
}

// Cancel stops a run with a reason. A queued or waiting run ends at once; a running run ends at its
// next step, or sooner when its current step honours cancellation, and ends cancelled even if its
// last step then succeeds. Completed steps keep their results. Effects in flight are never cut
// short, and an unknown effect stays unknown for reconciliation.
func Cancel(ctx context.Context, db *sql.DB, runID int64, by, reason string) error {
	if strings.TrimSpace(by) == "" || strings.TrimSpace(reason) == "" {
		return errors.New("cancelling a run needs who and why")
	}
	var wake bool
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		var state string
		if err := tx.QueryRowContext(ctx, `SELECT state FROM workflow_runs WHERE id=$1 FOR UPDATE`, runID).Scan(&state); err != nil {
			return notFound(err, runID)
		}
		if slices.Contains([]string{"completed", "failed", "cancelled"}, state) {
			return ErrNotAllowed
		}
		if err := cancelTree(ctx, tx, runID, true, truncate(reason), by); err != nil {
			return err
		}
		wake = state != "running"
		return nil
	})
	if err == nil && wake {
		// Best effort, after commit, never holding this run's row while taking its parent's (see
		// Engine.wakeParent); the sweep wakes the parent if this fails. The cancel itself stands.
		if _, werr := db.ExecContext(ctx, wakeParent, runID); werr != nil {
			log.Printf("Workflow run %d was cancelled but could not wake its parent yet: %v", runID, werr)
		}
	}
	return err
}

// cancelTree cancels the active descendants of root, and root too when includeRoot: a running run
// ends at its next step, the rest at once. The engine calls it again whenever a run ends cancelled,
// so a child started while an ancestor was being cancelled is caught when its parent stops.
func cancelTree(ctx context.Context, tx *sql.Tx, root int64, includeRoot bool, reason, by string) error {
	rows, err := tx.QueryContext(ctx, `WITH RECURSIVE tree(id) AS (SELECT $1::bigint UNION SELECT r.id FROM workflow_runs r JOIN tree t ON r.parent_run_id=t.id)
 UPDATE workflow_runs SET cancel_requested_at=COALESCE(cancel_requested_at,now()),cancelled_by=$3,updated_at=now(),
  cancel_reason=CASE WHEN id=$1 THEN $2 ELSE 'Parent run cancelled: '||$2 END,
  state=CASE WHEN state='running' THEN state ELSE 'cancelled' END,
  finished_at=CASE WHEN state='running' THEN finished_at ELSE now() END,
  wake_at=CASE WHEN state='running' THEN wake_at END,
  waiting_on=CASE WHEN state='running' THEN waiting_on ELSE '' END
 WHERE id IN (SELECT id FROM tree) AND ($4 OR id<>$1) AND state IN ('queued','running','waiting') RETURNING id,state`, root, reason, by, includeRoot)
	if err != nil {
		return err
	}
	var ended []int64
	for rows.Next() {
		var id int64
		var after string
		if err = rows.Scan(&id, &after); err != nil {
			rows.Close()
			return err
		}
		if after == "cancelled" {
			ended = append(ended, id)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE workflow_steps SET state='cancelled',finished_at=now() WHERE run_id=ANY($1) AND state IN ('running','waiting')`, ended)
	return err
}

// wakeParent wakes the parent of run $1, if it has one waiting or still running, after the run ended.
const wakeParent = `UPDATE workflow_runs SET wake_at='-infinity',updated_at=now() WHERE id=(SELECT parent_run_id FROM workflow_runs WHERE id=$1) AND state IN ('waiting','running')`

// Decision is a person's answer to an approval step.
type Decision struct {
	Approved bool   `json:"approved"`
	By       string `json:"by"`
	Note     string `json:"note,omitempty"`
}

// Approve records a person's decision on a run's waiting approval step and wakes the run. The
// caller passes the approver's permissions in the run's firm, from their verified session, and is
// responsible for the approver belonging to that firm; the approver must hold the permission the
// step asks for. A step takes one decision.
func Approve(ctx context.Context, db *sql.DB, runID int64, key string, d Decision, permissions []string) error {
	if strings.TrimSpace(d.By) == "" {
		return errors.New("an approval needs who decided")
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var org sql.NullString
		var runState, stepState, kind, permission string
		var decided, expired bool
		if err := tx.QueryRowContext(ctx, `SELECT r.organization_id,r.state,s.state,s.kind,COALESCE(s.input->>'permission',''),EXISTS(SELECT 1 FROM workflow_events WHERE key=$3),s.wake_at IS NOT NULL AND s.wake_at<=now()
 FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id AND s.key=$2 WHERE r.id=$1 FOR UPDATE OF r,s`, runID, key, approvalKey(runID, key)).Scan(&org, &runState, &stepState, &kind, &permission, &decided, &expired); err != nil {
			return notFound(err, runID)
		}
		if kind != "approval" || stepState != "waiting" || decided || (runState != "waiting" && runState != "running") {
			return ErrNotAllowed
		}
		if expired {
			// The wait would not count a decision recorded after its deadline; say so now.
			return fmt.Errorf("%w: the approval's deadline has passed", ErrNotAllowed)
		}
		if permission == "" || !slices.Contains(permissions, permission) {
			return fmt.Errorf("%w: approving needs the %s permission", ErrNotAllowed, permission)
		}
		if _, err := emit(ctx, tx, org.String, approvalKey(runID, key), d, d.By); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE workflow_steps SET actor=$3,note=$4 WHERE run_id=$1 AND key=$2`, runID, key, d.By, truncate(d.Note))
		return err
	})
}

func approvalKey(runID int64, key string) string { return fmt.Sprintf("wf.approval:%d:%s", runID, key) }

// RetryStep runs a failed step (or one waiting to retry) again at once, with a fresh set of
// attempts, reopening its run if the failure ended it. A failed child step starts a new child run
// (the failed one stays as history). An unknown effect is settled with Resolve.
func RetryStep(ctx context.Context, db *sql.DB, runID int64, key, by, note string) error {
	if strings.TrimSpace(by) == "" || strings.TrimSpace(note) == "" {
		return errors.New("retrying a step needs who and why")
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var runState, stepState, kind string
		if err := tx.QueryRowContext(ctx, `SELECT r.state,s.state,s.kind FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id AND s.key=$2 WHERE r.id=$1 FOR UPDATE OF r,s`, runID, key).Scan(&runState, &stepState, &kind); err != nil {
			return notFound(err, runID)
		}
		if !(stepState == "failed" && (runState == "failed" || runState == "waiting")) && !(stepState == "waiting" && runState == "waiting") {
			return ErrNotAllowed
		}
		if kind == "child" && stepState != "failed" {
			// A waiting child step's child may still be working; a second child would repeat its work.
			return fmt.Errorf("%w: a child step can be retried once its child has failed", ErrNotAllowed)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE workflow_steps SET state='waiting',retry_at=NULL,attempts=0,error='',actor=$3,note=$4,finished_at=NULL,
 child_run_id=NULL,input=CASE WHEN kind='child' THEN jsonb_set(COALESCE(input,'{}'::jsonb),'{generation}',to_jsonb(COALESCE((input->>'generation')::int,0)+1)) ELSE input END
 WHERE run_id=$1 AND key=$2`, runID, key, by, truncate(note)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET state='waiting',wake_at='-infinity',finished_at=NULL,error='',output=NULL,updated_at=now() WHERE id=$1`, runID)
		if violates(err, "workflow_runs_concurrency") {
			return ErrBusy
		}
		return err
	})
}

// Resolution settles an effect whose outcome was unknown, after a person checked the provider.
type Resolution struct {
	RunID   int64
	Key     string
	Applied bool            // the provider did act: the step completes with Output
	Output  json.RawMessage // the step's result when Applied (JSON; empty means null)
	By      string
	Note    string // what was checked, for example the provider's reference
}

// Resolve records a person's reconciliation of an unknown effect. Applied completes the step
// without calling the provider; not applied lets the effect run again, or, when the run has already
// ended (it was cancelled), closes the step as cancelled. A live run wakes, even one still running
// that has not parked yet.
func Resolve(ctx context.Context, db *sql.DB, r Resolution) error {
	if strings.TrimSpace(r.By) == "" || strings.TrimSpace(r.Note) == "" {
		return errors.New("resolving an effect needs who and what was checked")
	}
	output := r.Output
	if len(output) == 0 {
		output = json.RawMessage("null")
	}
	if !json.Valid(output) || len(output) > maxJSON {
		return errors.New("the resolved output must be JSON of at most 64 KiB")
	}
	return inTx(ctx, db, func(tx *sql.Tx) error {
		var runState, stepState, kind string
		if err := tx.QueryRowContext(ctx, `SELECT r.state,s.state,s.kind FROM workflow_runs r JOIN workflow_steps s ON s.run_id=r.id AND s.key=$2 WHERE r.id=$1 FOR UPDATE OF r,s`, r.RunID, r.Key).Scan(&runState, &stepState, &kind); err != nil {
			return notFound(err, r.RunID)
		}
		if kind != "effect" || stepState != "unknown" {
			return ErrNotAllowed
		}
		live := runState == "waiting" || runState == "running"
		var err error
		switch {
		case r.Applied:
			_, err = tx.ExecContext(ctx, `UPDATE workflow_steps SET state='completed',output=$3,error='',actor=$4,note=$5,finished_at=now() WHERE run_id=$1 AND key=$2`, r.RunID, r.Key, string(output), r.By, truncate(r.Note))
		case live:
			_, err = tx.ExecContext(ctx, `UPDATE workflow_steps SET state='waiting',retry_at=NULL,attempts=0,error='',actor=$3,note=$4,finished_at=NULL WHERE run_id=$1 AND key=$2`, r.RunID, r.Key, r.By, truncate(r.Note))
		default:
			// Nothing will run this step again: record the reconciliation and close it.
			_, err = tx.ExecContext(ctx, `UPDATE workflow_steps SET state='cancelled',error='',actor=$3,note=$4,finished_at=now() WHERE run_id=$1 AND key=$2`, r.RunID, r.Key, r.By, truncate(r.Note))
		}
		if err == nil && live {
			_, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET wake_at='-infinity',updated_at=now() WHERE id=$1`, r.RunID)
		}
		return err
	})
}

func asJSON(value any) (json.RawMessage, error) {
	var raw json.RawMessage
	switch v := value.(type) {
	case nil:
		raw = json.RawMessage("{}")
	case json.RawMessage:
		raw = v
	default:
		var err error
		if raw, err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	if !json.Valid(raw) {
		return nil, errors.New("value is not valid JSON")
	}
	if len(raw) > maxJSON {
		return nil, fmt.Errorf("value is %d bytes; the limit is %d", len(raw), maxJSON)
	}
	return raw, nil
}

func violates(err error, constraint string) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == constraint
}

func notFound(err error, runID int64) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("workflow run %d or its step was not found: %w", runID, err)
	}
	return err
}

func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
