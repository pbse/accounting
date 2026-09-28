package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"close.local/backend/internal/wf"
)

// Workflow screens of the staff console (docs/admin-panel.md, phase A1): the catalog, runs across
// firms, a run's steps for the flow view, and the actions staff take on runs. Every action runs as
// the run's firm through adminAct, so it is confined by row-level security and audited with its
// reason. Client content in inputs and outputs is masked unless a staff member with platform:content
// reveals it with a reason, which is audited too.
func registerAdminWorkflows(mux *http.ServeMux, db *sql.DB, token string) {
	guard := func(permission string, next func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return staffOnly(token, permission, func(w http.ResponseWriter, r *http.Request) {
			if db == nil {
				problem(w, 503, "The admin panel needs a configured database.")
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("GET /v1/admin/workflows/definitions", guard(platformRead, func(w http.ResponseWriter, r *http.Request) { adminDefinitions(w, r, db) }))
	mux.HandleFunc("GET /v1/admin/workflows/activities", guard(platformRead, func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, map[string]any{"activities": wf.Activities()})
	}))
	mux.HandleFunc("GET /v1/admin/workflows/runs", guard(platformRead, func(w http.ResponseWriter, r *http.Request) { adminRuns(w, r, db) }))
	mux.HandleFunc("GET /v1/admin/workflows/runs/{id}", guard(platformRead, func(w http.ResponseWriter, r *http.Request) { adminRun(w, r, db) }))
	mux.HandleFunc("POST /v1/admin/workflows/runs", guard(platformOperate, func(w http.ResponseWriter, r *http.Request) { adminStartRun(w, r, db) }))
	mux.HandleFunc("POST /v1/admin/workflows/runs/{id}/cancel", guard(platformOperate, func(w http.ResponseWriter, r *http.Request) { adminRunAction(w, r, db, "cancel") }))
	mux.HandleFunc("POST /v1/admin/workflows/runs/{id}/retry", guard(platformOperate, func(w http.ResponseWriter, r *http.Request) { adminRunAction(w, r, db, "retry") }))
	mux.HandleFunc("POST /v1/admin/workflows/runs/{id}/resolve", guard(platformOperate, func(w http.ResponseWriter, r *http.Request) { adminRunAction(w, r, db, "resolve") }))
	mux.HandleFunc("POST /v1/admin/workflows/events", guard(platformOperate, func(w http.ResponseWriter, r *http.Request) { adminEmit(w, r, db) }))
}

// adminWorkflowProblem turns an engine or action error into the reply staff see.
func adminWorkflowProblem(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, errReasonRequired):
		problem(w, 422, err.Error())
	case errors.Is(err, wf.ErrUnknownWorkflow), errors.Is(err, sql.ErrNoRows):
		problem(w, 404, err.Error())
	case errors.Is(err, wf.ErrInvalidInput):
		problem(w, 422, err.Error())
	case errors.Is(err, wf.ErrBusy), errors.Is(err, wf.ErrNotAllowed):
		problem(w, 409, err.Error())
	default:
		log.Printf("Admin %s failed: %v", what, err)
		problem(w, 503, "The "+what+" could not be completed. Check the run and try again.")
	}
}

func adminDefinitions(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	type counts struct{ Active, Completed, Failed int }
	byName := map[string]counts{}
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT workflow,count(*) FILTER (WHERE state IN ('queued','running','waiting')),
 count(*) FILTER (WHERE state='completed' AND finished_at>now()-interval '7 days'),count(*) FILTER (WHERE state='failed' AND finished_at>now()-interval '7 days')
 FROM workflow_runs GROUP BY workflow`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			var c counts
			if err = rows.Scan(&name, &c.Active, &c.Completed, &c.Failed); err != nil {
				return err
			}
			byName[name] = c
		}
		return rows.Err()
	})
	if err != nil {
		adminWorkflowProblem(w, err, "catalog")
		return
	}
	type definition struct {
		Name         string         `json:"name"`
		Title        string         `json:"title"`
		Description  string         `json:"description"`
		Triggers     []wf.Trigger   `json:"triggers"`
		InputSchema  map[string]any `json:"input_schema"`
		OutputSchema map[string]any `json:"output_schema"`
		ActiveRuns   int            `json:"active_runs"`
		Completed7d  int            `json:"completed_7d"`
		Failed7d     int            `json:"failed_7d"`
	}
	out := []definition{}
	for _, d := range wf.Definitions() {
		c := byName[d.Name]
		out = append(out, definition{d.Name, d.Title, d.Description, d.Triggers, d.InputSchema, d.OutputSchema, c.Active, c.Completed, c.Failed})
	}
	jsonReply(w, 200, map[string]any{"workflows": out})
}

// Saved views of the runs list.
var runViews = map[string]string{
	"active": `r.state IN ('queued','running','waiting')`,
	// Runnable but not picked up: queued for an hour, a lease nobody renewed, a wake time long past, or
	// woken (by an event, retry or resolution, which set updated_at) and left unclaimed.
	"stuck": `((r.state='queued' AND r.created_at<now()-interval '1 hour') OR (r.state='running' AND r.claimed_at<now()-interval '10 minutes')
 OR (r.state='waiting' AND ((r.wake_at>'-infinity' AND r.wake_at<now()-interval '10 minutes') OR (r.wake_at='-infinity' AND r.updated_at<now()-interval '10 minutes'))))`,
	"waiting_on_client": `(r.state='waiting' AND r.waiting_on LIKE 'client\_request.replied:%' AND r.updated_at<now()-interval '7 days')`,
	"unknown_effects":   `EXISTS(SELECT 1 FROM workflow_steps s WHERE s.run_id=r.id AND s.state='unknown')`,
	"failed_today":      `(r.state='failed' AND r.finished_at>now()-interval '24 hours')`,
}

type adminRunRow struct {
	ID           int64   `json:"id"`
	Organization *string `json:"organization"`
	FirmName     *string `json:"firm_name"`
	Workflow     string  `json:"workflow"`
	State        string  `json:"state"`
	WaitingOn    string  `json:"waiting_on"`
	StartedBy    string  `json:"started_by"`
	Error        string  `json:"error"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
	FinishedAt   *string `json:"finished_at"`
	ParentRunID  *int64  `json:"parent_run_id"`
	PlanID       *int64  `json:"plan_id"`
}

const adminRunColumns = `r.id,r.organization_id,o.name,r.workflow,r.state,r.waiting_on,r.started_by,r.error,r.created_at::text,r.updated_at::text,r.finished_at::text,r.parent_run_id,r.plan_id`

func scanRun(row interface{ Scan(...any) error }) (adminRunRow, error) {
	var run adminRunRow
	err := row.Scan(&run.ID, &run.Organization, &run.FirmName, &run.Workflow, &run.State, &run.WaitingOn, &run.StartedBy, &run.Error, &run.CreatedAt, &run.UpdatedAt, &run.FinishedAt, &run.ParentRunID, &run.PlanID)
	return run, err
}

// maskText hides the free text of a run until content is revealed: its error, which can quote client
// data, and what it waits for (see maskKey).
func (run *adminRunRow) maskText() {
	run.Error = maskText(run.Error)
	run.WaitingOn = run.maskKey(run.WaitingOn)
}

// maskKey masks an event key. A code workflow names the key's topic, the part before the first ":",
// and often builds the rest from its input, so only the topic shows. A plan writes the whole key,
// possibly from client data, so none of it shows.
func (run *adminRunRow) maskKey(key string) string {
	if run.PlanID != nil {
		return maskText(key)
	}
	if topic, rest, ok := strings.Cut(key, ":"); ok && rest != "" {
		return topic + ":••••"
	}
	return key
}

func adminRuns(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	q := r.URL.Query()
	view := q.Get("view")
	condition, known := runViews[view]
	if view != "" && !known {
		problem(w, 422, "Unknown view.")
		return
	}
	if condition == "" {
		condition = "true"
	}
	offset, err := strconv.Atoi(q.Get("offset"))
	if q.Get("offset") == "" {
		offset, err = 0, nil
	}
	if err != nil || offset < 0 || offset > 1_000_000 {
		problem(w, 422, "The offset must be a whole number.")
		return
	}
	runs := []adminRunRow{}
	err = adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+adminRunColumns+` FROM workflow_runs r LEFT JOIN organizations o ON o.id=r.organization_id
 WHERE ($1='' OR r.organization_id=$1) AND ($2='' OR r.workflow=$2) AND ($3='' OR r.state=$3) AND `+condition+`
 ORDER BY r.id DESC LIMIT $4 OFFSET $5`, q.Get("firm"), q.Get("workflow"), q.Get("state"), adminPage+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			run, err := scanRun(rows)
			if err != nil {
				return err
			}
			run.maskText()
			runs = append(runs, run)
		}
		return rows.Err()
	})
	if err != nil {
		adminWorkflowProblem(w, err, "runs list")
		return
	}
	more := len(runs) > adminPage
	if more {
		runs = runs[:adminPage]
	}
	jsonReply(w, 200, map[string]any{"runs": runs, "has_more": more, "next_offset": offset + len(runs)})
}

type adminStep struct {
	Key        string          `json:"key"`
	Seq        int             `json:"seq"`
	Kind       string          `json:"kind"`
	State      string          `json:"state"`
	Attempts   int             `json:"attempts"`
	RetryAt    *string         `json:"retry_at"`
	WakeAt     *string         `json:"wake_at"`
	WaitingOn  string          `json:"waiting_on"`
	Error      string          `json:"error"`
	Actor      string          `json:"actor"`
	Note       string          `json:"note"`
	StartedAt  string          `json:"started_at"`
	FinishedAt *string         `json:"finished_at"`
	ChildRunID *int64          `json:"child_run_id"`
	Activity   string          `json:"activity,omitempty"`
	Input      json.RawMessage `json:"input"`
	Output     json.RawMessage `json:"output"`
}

// adminRun is one run for the flow view: the run, its steps in the order reached, its children and
// parent, and, for a plan run, the approved plan so unreached nodes can be drawn too. Unless revealed,
// free text is masked: payloads, errors, notes, and a plan's title, inputs, event keys and approval
// text. Names of workflows and steps, states and times show.
func adminRun(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		problem(w, 404, "Run not found.")
		return
	}
	reveal := r.URL.Query().Get("reveal") == "1"
	var run adminRunRow
	var input, output []byte
	steps := []adminStep{}
	var children, plan []byte
	err = adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+adminRunColumns+`,r.input,r.output FROM workflow_runs r LEFT JOIN organizations o ON o.id=r.organization_id WHERE r.id=$1`, id)
		if err := row.Scan(&run.ID, &run.Organization, &run.FirmName, &run.Workflow, &run.State, &run.WaitingOn, &run.StartedBy, &run.Error, &run.CreatedAt, &run.UpdatedAt, &run.FinishedAt, &run.ParentRunID, &run.PlanID, &input, &output); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT key,seq,kind,state,attempts,retry_at::text,wake_at::text,waiting_on,error,actor,note,started_at::text,finished_at::text,child_run_id,COALESCE(input->>'activity',''),input,output
 FROM workflow_steps WHERE run_id=$1 ORDER BY seq`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s adminStep
			var in, out []byte
			if err = rows.Scan(&s.Key, &s.Seq, &s.Kind, &s.State, &s.Attempts, &s.RetryAt, &s.WakeAt, &s.WaitingOn, &s.Error, &s.Actor, &s.Note, &s.StartedAt, &s.FinishedAt, &s.ChildRunID, &s.Activity, &in, &out); err != nil {
				return err
			}
			s.Input, s.Output = in, out
			steps = append(steps, s)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',id,'workflow',workflow,'state',state,'parent_step',parent_step) ORDER BY id) FROM workflow_runs WHERE parent_run_id=$1),'[]'::jsonb),
 COALESCE((SELECT jsonb_build_object('id',p.id,'title',CASE WHEN $3 THEN p.title ELSE '••••' END,'state',p.state,'source',p.source,'plan',CASE WHEN $3 THEN p.plan
   ELSE jsonb_set(p.plan-'title','{nodes}',(SELECT COALESCE(jsonb_agg(CASE WHEN n->'wait' ? 'event' THEN jsonb_set(n,'{wait,event}','"••••"') ELSE n END-'input' #- '{approval,subject}' #- '{approval,prompt}' ORDER BY i),'[]'::jsonb)
   FROM jsonb_array_elements(p.plan->'nodes') WITH ORDINALITY AS e(n,i))) END)
 FROM workflow_plans p WHERE p.id=$2),'null'::jsonb)`, id, run.PlanID, reveal).Scan(&children, &plan)
	})
	if err != nil {
		adminWorkflowProblem(w, err, "run")
		return
	}
	if reveal {
		// Seeing client content is a staff action: it needs the permission and a reason, and is audited.
		member := staffOf(r.Context())
		if !slices.Contains(member.Permissions, platformContent) {
			problem(w, 403, "Revealing client content needs platform:content.")
			return
		}
		firm := ""
		if run.Organization != nil {
			firm = *run.Organization
		}
		reason := strings.TrimSpace(r.Header.Get("X-Access-Reason"))
		if err = adminAct(r.Context(), db, adminAction{Firm: firm, Action: "content.reveal", TargetType: "workflow_run", TargetID: strconv.FormatInt(id, 10), Reason: reason},
			func(context.Context, *sql.Tx) error { return nil }); err != nil {
			adminWorkflowProblem(w, err, "reveal")
			return
		}
	}
	definition := lookupDefinition(run.Workflow)
	var inputSchema, outputSchema map[string]any
	if definition != nil {
		inputSchema, outputSchema = definition.InputSchema, definition.OutputSchema
	}
	activities := map[string]wf.ActivityInfo{}
	for _, a := range wf.Activities() {
		activities[a.Name] = a
	}
	if !reveal {
		run.maskText()
	}
	for i := range steps {
		s := &steps[i]
		if !reveal {
			s.Error, s.Note, s.WaitingOn = maskText(s.Error), maskText(s.Note), run.maskKey(s.WaitingOn)
		}
		switch {
		case reveal:
		case s.Kind == "value", s.Kind == "sleep", s.Kind == "step" && string(s.Output) == `"skipped"`:
			// Generated values, sleeps and a plan's skipped-node marker carry no client content.
		case s.Kind == "approval":
			// Who may decide and what was decided show; the subject, prompt and note are the firm's words.
			s.Input, s.Output = only(s.Input, "permission"), only(s.Output, "approved", "by")
		case s.Kind == "event":
			var in map[string]string
			if json.Unmarshal(s.Input, &in) == nil && in != nil {
				s.Input, _ = json.Marshal(map[string]string{"event": run.maskKey(in["event"])})
			} else {
				s.Input = hidden(s.Input)
			}
			s.Output = hidden(s.Output)
		case s.Activity != "":
			if a, ok := activities[s.Activity]; ok {
				s.Input = maskActivityInput(s.Input, a.Input)
				s.Output = mask(s.Output, a.Output)
			} else {
				s.Input, s.Output = hidden(s.Input), hidden(s.Output)
			}
		default:
			s.Output = hidden(s.Output)
		}
	}
	if !reveal {
		input, output = mask(input, inputSchema), mask(output, outputSchema)
	}
	var registered bool
	if definition != nil {
		registered = true
	}
	jsonReply(w, 200, map[string]any{"run": run, "input": json.RawMessage(nonNull(input)), "output": json.RawMessage(nonNull(output)), "steps": steps,
		"children": json.RawMessage(children), "plan": json.RawMessage(plan), "registered": registered, "revealed": reveal})
}

func lookupDefinition(name string) *wf.Definition {
	for _, d := range wf.Definitions() {
		if d.Name == name {
			return &d
		}
	}
	return nil
}

func nonNull(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

// hidden replaces a value whose content is unknown with its size; revealing shows it.
func hidden(raw []byte) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(nonNull(raw))
	}
	out, _ := json.Marshal(map[string]any{"hidden": true, "bytes": len(raw)})
	return out
}

// only masks every text in a recorded object, at any depth, except the named fields.
func only(raw []byte, keep ...string) json.RawMessage {
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return hidden(raw)
	}
	out, _ := json.Marshal(maskExcept(fields, keep))
	return out
}

func maskExcept(v any, keep []string) any {
	switch value := v.(type) {
	case map[string]any:
		for k, item := range value {
			if !slices.Contains(keep, k) {
				value[k] = maskExcept(item, keep)
			}
		}
	case []any:
		for i, item := range value {
			value[i] = maskExcept(item, keep)
		}
	case string:
		return maskText(value)
	}
	return v
}

// maskText masks a non-empty text; other values pass through.
func maskText[T any](v T) T {
	if text, ok := any(v).(string); ok && text != "" {
		return any("••••").(T)
	}
	return v
}

// mask shows what cannot carry client content and masks the rest (see maskValue); with no schema
// the whole value is hidden.
func mask(raw []byte, schema map[string]any) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage(nonNull(raw))
	}
	if schema == nil {
		return hidden(raw)
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return hidden(raw)
	}
	out, _ := json.Marshal(maskValue(v, schema))
	return out
}

// maskActivityInput masks the recorded {"activity": name, "input": …} of an activity step.
func maskActivityInput(raw []byte, schema map[string]any) json.RawMessage {
	var recorded map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &recorded) != nil {
		return hidden(raw)
	}
	recorded["input"] = mask(recorded["input"], schema)
	out, _ := json.Marshal(recorded)
	return out
}

// maskValue masks by default: numbers, booleans and the names of fields the schema declares show;
// text, fields tagged wf:"sensitive" and anything the schema does not describe (free-form maps, raw
// JSON) are masked, since a workflow may copy client content anywhere.
func maskValue(v any, schema map[string]any) any {
	if sensitive, _ := schema["sensitive"].(bool); sensitive && v != nil {
		return "••••"
	}
	if _, described := schema["type"]; !described && v != nil {
		raw, _ := json.Marshal(v)
		return hidden(raw)
	}
	switch value := v.(type) {
	case string:
		return maskText(value)
	case map[string]any:
		properties, _ := schema["properties"].(map[string]any)
		for k, item := range value {
			property, ok := properties[k].(map[string]any)
			if !ok {
				raw, _ := json.Marshal(value)
				return hidden(raw)
			}
			value[k] = maskValue(item, property)
		}
	case []any:
		items, _ := schema["items"].(map[string]any)
		for i, item := range value {
			value[i] = maskValue(item, items)
		}
	}
	return v
}

// adminRunFirm is the firm a run belongs to ("" for platform runs).
func adminRunFirm(ctx context.Context, db *sql.DB, id int64) (string, error) {
	var org sql.NullString
	err := adminRead(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT organization_id FROM workflow_runs WHERE id=$1`, id).Scan(&org)
	})
	return org.String, err
}

func adminStartRun(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var in struct {
		Workflow       string          `json:"workflow"`
		Organization   string          `json:"organization"`
		Input          json.RawMessage `json:"input"`
		IdempotencyKey string          `json:"idempotency_key"`
		Reason         string          `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = "staff:" + rand.Text()
	}
	member := staffOf(r.Context())
	var id int64
	var existing bool
	err := adminAct(r.Context(), db, adminAction{Firm: in.Organization, Action: "workflow.start", TargetType: "workflow", TargetID: in.Workflow, Reason: in.Reason,
		Request: map[string]string{"workflow": in.Workflow, "organization": in.Organization, "idempotency_key": in.IdempotencyKey}},
		func(ctx context.Context, tx *sql.Tx) error {
			d := lookupDefinition(in.Workflow)
			if d == nil {
				return fmt.Errorf("%w: %q", wf.ErrUnknownWorkflow, in.Workflow)
			}
			switch byHand, firmOnly := d.StartsByHand(); {
			case !byHand:
				return fmt.Errorf("%w: %s is not started by hand", wf.ErrInvalidInput, in.Workflow)
			case firmOnly && in.Organization == "":
				return fmt.Errorf("%w: %s works on one firm; name it", wf.ErrInvalidInput, in.Workflow)
			}
			if in.Organization != "" {
				var enabled bool
				if err := tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1`, in.Organization).Scan(&enabled); err != nil {
					return err
				}
				if !enabled {
					return fmt.Errorf("%w: the firm is suspended, so its runs would not be claimed", wf.ErrNotAllowed)
				}
			}
			var err error
			id, existing, err = wf.Start(ctx, tx, wf.StartRequest{Workflow: in.Workflow, Organization: in.Organization, Input: in.Input, IdempotencyKey: in.IdempotencyKey, StartedBy: "staff:" + member.Subject})
			return err
		})
	if err != nil {
		adminWorkflowProblem(w, err, "start")
		return
	}
	jsonReply(w, 200, map[string]any{"id": id, "existing": existing})
}

// adminRunAction cancels a run, retries a step or resolves an unknown effect, as the run's firm.
func adminRunAction(w http.ResponseWriter, r *http.Request, db *sql.DB, action string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		problem(w, 404, "Run not found.")
		return
	}
	var in struct {
		Key     string          `json:"key"`
		Reason  string          `json:"reason"`
		Applied bool            `json:"applied"`
		Output  json.RawMessage `json:"output"`
	}
	if !decode(w, r, &in) {
		return
	}
	firm, err := adminRunFirm(r.Context(), db, id)
	if err != nil {
		adminWorkflowProblem(w, err, action)
		return
	}
	by := "staff:" + staffOf(r.Context()).Subject
	request := map[string]any{"key": in.Key}
	if action == "resolve" {
		request["applied"] = in.Applied
	}
	err = adminAct(r.Context(), db, adminAction{Firm: firm, Action: "workflow." + action, TargetType: "workflow_run", TargetID: strconv.FormatInt(id, 10), Reason: in.Reason, Request: request},
		func(ctx context.Context, tx *sql.Tx) error {
			switch action {
			case "cancel":
				_, err := wf.CancelTx(ctx, tx, id, by, in.Reason)
				return err
			case "retry":
				return wf.RetryStepTx(ctx, tx, id, in.Key, by, in.Reason)
			default:
				return wf.ResolveTx(ctx, tx, wf.Resolution{RunID: id, Key: in.Key, Applied: in.Applied, Output: in.Output, By: by, Note: in.Reason})
			}
		})
	if err != nil {
		adminWorkflowProblem(w, err, action)
		return
	}
	jsonReply(w, 200, map[string]any{"ok": true})
}

// adminEmit records an event for a firm, waking its runs waiting for it. Engine events (wf.) are refused.
func adminEmit(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var in struct {
		Organization string          `json:"organization"`
		Key          string          `json:"key"`
		Payload      json.RawMessage `json:"payload"`
		Reason       string          `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Key == "" || len(in.Key) > 200 || strings.HasPrefix(in.Key, "wf.") {
		problem(w, 422, "An event key has 1–200 characters and does not start with wf. (the engine's own events).")
		return
	}
	var id int64
	err := adminAct(r.Context(), db, adminAction{Firm: in.Organization, Action: "workflow.event", TargetType: "event", TargetID: in.Key, Reason: in.Reason, Request: map[string]string{"key": in.Key}},
		func(ctx context.Context, tx *sql.Tx) error {
			var payload any = in.Payload
			if len(in.Payload) == 0 {
				payload = nil
			}
			var err error
			id, err = wf.Emit(ctx, tx, in.Organization, in.Key, payload, "staff:"+staffOf(r.Context()).Subject)
			return err
		})
	if err != nil {
		adminWorkflowProblem(w, err, "event")
		return
	}
	jsonReply(w, 200, map[string]any{"event_id": id})
}
