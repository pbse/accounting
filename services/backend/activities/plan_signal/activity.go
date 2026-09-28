// Package plan_signal records a plan.<name> event for the run's firm, so one plan or workflow can
// wake another that waits for it. Keys stay in the plan. namespace, so a plan can never forge a
// client reply, a sign-off or an approval.
package plan_signal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"close.local/backend/internal/wf"
)

type Input struct {
	Name    string          `json:"name" description:"Recorded as the event plan.<name>"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Output struct {
	Event   string `json:"event"`
	EventID int64  `json:"event_id"`
}

var name = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,100}$`)

var Activity = wf.DefineActivity(wf.ActivitySpec[Input, Output]{
	Name:        "plan_signal",
	Title:       "Send a plan signal",
	Description: "Records the event plan.<name> for this firm, waking any plan or workflow waiting for it.",
	Effect:      wf.Internal,
	Run:         signal,
})

func signal(ctx context.Context, in Input) (Output, error) {
	if !name.MatchString(in.Name) {
		return Output{}, wf.Permanent(fmt.Errorf("signal name %q: use lowercase letters, digits and _.:-", in.Name))
	}
	out := Output{Event: "plan." + in.Name}
	payload := any(in.Payload)
	if len(in.Payload) == 0 {
		payload = nil
	}
	// A step can run again after a crash, even while an earlier attempt is still running: a lock on
	// the step's ID makes checking for its signal and recording it one step, so it signals once.
	step := wf.CurrentStepID(ctx)
	tx, err := wf.DB(ctx).BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, step); err != nil {
		return out, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM workflow_events WHERE key=$1 AND actor=$2`, out.Event, step).Scan(&out.EventID)
	if errors.Is(err, sql.ErrNoRows) {
		out.EventID, err = wf.Emit(ctx, tx, wf.Organization(ctx), out.Event, payload, step)
	}
	if err == nil {
		err = tx.Commit()
	}
	return out, err
}
