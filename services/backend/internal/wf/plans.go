package wf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Plan is a workflow composed at runtime from vetted parts, stored as data (workflow_plans) and run
// by the built-in "plan" workflow once a person approves it. Nodes run in order; each is exactly one
// of an activity, a child run of a code workflow, a wait for an event or for a time, or an approval.
// See docs/workflow-engine.md#plans-staff-built-and-agent-proposed.
type Plan struct {
	Version int    `json:"version"`
	Title   string `json:"title"`
	Nodes   []Node `json:"nodes"`
}

// Node is one part of a plan. It runs when every node in After succeeded, or, with When
// ("<node>.<outcome>", naming a node in After), when that node ended that way. String values in
// Input and an event key may use {{node.field}} to use an earlier node's output.
type Node struct {
	ID       string          `json:"id"`
	After    []string        `json:"after,omitempty"`
	When     string          `json:"when,omitempty"`
	Activity string          `json:"activity,omitempty"`
	Workflow string          `json:"workflow,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Wait     *PlanWait       `json:"wait,omitempty"`
	Approval *PlanApproval   `json:"approval,omitempty"`
}

// PlanWait waits for an event (with a timeout) or for a duration. Durations use Go's form: "72h".
type PlanWait struct {
	Event    string `json:"event,omitempty"`
	Timeout  string `json:"timeout,omitempty"`
	Duration string `json:"duration,omitempty"`
}

// PlanApproval asks a person holding Permission in the firm to decide before the plan goes on.
type PlanApproval struct {
	Permission string `json:"permission"`
	Subject    string `json:"subject,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	Timeout    string `json:"timeout"`
}

const (
	maxPlanNodes = 50
	maxHorizon   = 90 * 24 * time.Hour
	// Agent proposals a firm may receive per day.
	maxAgentPlansPerDay = 20
)

var (
	// ErrPlanInvalid wraps the list of a plan's problems.
	ErrPlanInvalid = errors.New("the plan is not valid")
	nodeIDPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)
	templateRef    = regexp.MustCompile(`\{\{\s*([a-z][a-z0-9_]{0,40})((?:\.[A-Za-z0-9_]+)*)\s*\}\}`)
	succeeded      = []string{"completed", "received", "approved"}
)

// ParsePlan decodes a plan, refusing unknown fields.
func ParsePlan(raw json.RawMessage) (Plan, error) {
	var p Plan
	if len(raw) > maxJSON {
		return p, fmt.Errorf("%w: the plan is larger than 64 KiB", ErrPlanInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&p); err != nil {
		return p, fmt.Errorf("%w: %v", ErrPlanInvalid, err)
	}
	return p, nil
}

// Digest identifies a plan's exact content; an approval binds to it.
func (p Plan) Digest() string {
	raw, _ := json.Marshal(p) // compacts Input, so layout never changes a digest
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// outcomes a node can end with.
func (n Node) outcomes() []string {
	switch {
	case n.Activity != "", n.Workflow != "":
		return []string{"completed", "failed"}
	case n.Wait != nil && n.Wait.Event != "":
		return []string{"received", "timed_out"}
	case n.Wait != nil:
		return []string{"completed"}
	case n.Approval != nil:
		return []string{"approved", "rejected", "timed_out"}
	}
	return nil
}

func duration(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err == nil && (d <= 0 || d > maxHorizon) {
		err = fmt.Errorf("must be more than zero and at most %s", maxHorizon)
	}
	return d, err
}

// Problems lists what is wrong with a plan; none means it may run. Given the approver's permissions
// (non-nil), it also checks they hold every activity's permission.
func (p Plan) Problems(permissions []string) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	if p.Version != 1 {
		add("version must be 1")
	}
	if t := strings.TrimSpace(p.Title); t == "" || len(t) > 200 {
		add("a plan needs a title of at most 200 characters")
	}
	if len(p.Nodes) == 0 || len(p.Nodes) > maxPlanNodes {
		add("a plan has 1 to %d nodes", maxPlanNodes)
	}
	index := map[string]int{}
	ancestors := make([]map[string]bool, len(p.Nodes))
	for i, n := range p.Nodes {
		where := fmt.Sprintf("node %q", n.ID)
		if !nodeIDPattern.MatchString(n.ID) {
			add("%s: ids are lowercase letters, digits and _, starting with a letter", where)
		}
		if _, taken := index[n.ID]; taken {
			add("%s: the id is used twice", where)
		}
		kinds := 0
		for _, set := range []bool{n.Activity != "", n.Workflow != "", n.Wait != nil, n.Approval != nil} {
			if set {
				kinds++
			}
		}
		if kinds != 1 {
			add("%s: each node is exactly one of activity, workflow, wait or approval", where)
		}
		ancestors[i] = map[string]bool{}
		for _, after := range n.After {
			j, ok := index[after]
			if !ok {
				add("%s: after names %q, which is not an earlier node", where, after)
				continue
			}
			ancestors[i][after] = true
			for a := range ancestors[j] {
				ancestors[i][a] = true
			}
		}
		if n.When != "" {
			id, outcome, _ := strings.Cut(n.When, ".")
			if !slices.Contains(n.After, id) {
				add("%s: when must name a node listed in after", where)
			} else if j, ok := index[id]; ok && !slices.Contains(p.Nodes[j].outcomes(), outcome) {
				add("%s: %q cannot end %q; it ends %s", where, id, outcome, strings.Join(p.Nodes[j].outcomes(), ", "))
			}
		}
		referenced := func(text string) {
			for _, m := range templateRef.FindAllStringSubmatch(text, -1) {
				if !ancestors[i][m[1]] {
					add("%s: {{%s…}} must name a node this one waits for (directly or through after)", where, m[1])
				}
			}
		}
		checkInput := func(validate func(json.RawMessage) error) {
			input := n.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			if !templateRef.Match(input) {
				if err := validate(input); err != nil {
					add("%s: %v", where, err)
				}
			}
			referenced(string(input))
		}
		switch {
		case n.Activity != "":
			a := lookupActivity(n.Activity)
			if a == nil {
				add("%s: activity %q does not exist", where, n.Activity)
				break
			}
			checkInput(a.validate)
			if a.Effect == External && !approvedFirst(p, n, index) {
				add("%s: %s acts outside our records, so it must follow an approval node (in after, with no when or when <approval>.approved)", where, n.Activity)
			}
			if permissions != nil && a.Permission != "" && !slices.Contains(permissions, a.Permission) {
				add("%s: approving needs the %s permission for %s", where, a.Permission, n.Activity)
			}
		case n.Workflow != "":
			w := lookup(n.Workflow)
			if w == nil || n.Workflow == "plan" {
				add("%s: workflow %q does not exist or cannot run inside a plan", where, n.Workflow)
				break
			}
			checkInput(w.validate)
		case n.Wait != nil:
			if (n.Wait.Event == "") == (n.Wait.Duration == "") {
				add("%s: a wait has either an event (and a timeout) or a duration", where)
			}
			if n.Wait.Event != "" {
				if len(n.Wait.Event) > 200 || strings.HasPrefix(n.Wait.Event, "wf.") {
					add("%s: an event key has at most 200 characters and does not start with wf.", where)
				}
				if _, err := duration(n.Wait.Timeout); err != nil {
					add("%s: timeout %q: %v", where, n.Wait.Timeout, err)
				}
				referenced(n.Wait.Event)
			}
			if n.Wait.Duration != "" {
				if _, err := duration(n.Wait.Duration); err != nil {
					add("%s: duration %q: %v", where, n.Wait.Duration, err)
				}
			}
		case n.Approval != nil:
			if strings.TrimSpace(n.Approval.Permission) == "" {
				add("%s: an approval names the permission its approver needs", where)
			}
			if _, err := duration(n.Approval.Timeout); err != nil {
				add("%s: timeout %q: %v", where, n.Approval.Timeout, err)
			}
		}
		index[n.ID] = i
	}
	return problems
}

// approvedFirst: an approval node gates n directly, so n runs only once a person approved.
func approvedFirst(p Plan, n Node, index map[string]int) bool {
	for _, after := range n.After {
		j, ok := index[after]
		if ok && p.Nodes[j].Approval != nil && (n.When == "" || n.When == after+".approved") {
			return true
		}
	}
	return false
}

func (p Plan) check(permissions []string) error {
	if problems := p.Problems(permissions); len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrPlanInvalid, strings.Join(problems, "; "))
	}
	return nil
}

// runs decides whether a node runs, given the outcomes of the nodes before it.
func runs(n Node, outcomes map[string]string) bool {
	if n.When != "" {
		id, outcome, _ := strings.Cut(n.When, ".")
		return outcomes[id] == outcome
	}
	for _, after := range n.After {
		if !slices.Contains(succeeded, outcomes[after]) {
			return false
		}
	}
	return true
}

// RunPlan executes a plan's nodes in order within a run and returns each node's outcome. A node
// that does not run is recorded as skipped. It fails when a node failed and no later node handles
// that failure (when "<node>.failed").
func RunPlan(ctx Context, p Plan) (map[string]string, error) {
	outcomes, outputs := map[string]string{}, map[string]json.RawMessage{}
	for _, n := range p.Nodes {
		key := "node:" + n.ID
		if !runs(n, outcomes) {
			if _, err := Step(ctx, key, func(context.Context) (string, error) { return "skipped", nil }); err != nil {
				return outcomes, err
			}
			outcomes[n.ID] = "skipped"
			continue
		}
		outcome, output, err := runNode(ctx, key, n, outputs)
		var failed *StepError
		switch {
		case errors.As(err, &failed):
			outcome = "failed"
		case err != nil:
			return outcomes, err // suspended, stopping or cancelled
		}
		outcomes[n.ID], outputs[n.ID] = outcome, output
	}
	var unhandled []string
	for _, n := range p.Nodes {
		if outcomes[n.ID] == "failed" && !slices.ContainsFunc(p.Nodes, func(m Node) bool { return m.When == n.ID+".failed" }) {
			unhandled = append(unhandled, n.ID)
		}
	}
	if len(unhandled) > 0 {
		return outcomes, fmt.Errorf("plan nodes failed: %s", strings.Join(unhandled, ", "))
	}
	return outcomes, nil
}

func runNode(ctx Context, key string, n Node, outputs map[string]json.RawMessage) (string, json.RawMessage, error) {
	// A template that cannot be filled fails the node, recorded like any failed step.
	resolveFailure := func(err error) (string, json.RawMessage, error) {
		_, stepErr := Step(ctx, key, func(context.Context) (string, error) { return "", Permanent(err) })
		return "failed", nil, stepErr
	}
	switch {
	case n.Activity != "", n.Workflow != "":
		input, err := resolveInput(n.Input, outputs)
		if err != nil {
			return resolveFailure(err)
		}
		var output json.RawMessage
		if n.Activity != "" {
			output, err = CallActivity(ctx, key, n.Activity, input)
		} else {
			output, err = Child[json.RawMessage](ctx, key, n.Workflow, input)
		}
		return "completed", output, err
	case n.Wait != nil && n.Wait.Event != "":
		event, err := resolveText(n.Wait.Event, outputs)
		if err != nil {
			return resolveFailure(err)
		}
		timeout, _ := duration(n.Wait.Timeout)
		payload, err := WaitForEvent[json.RawMessage](ctx, key, event, timeout)
		if errors.Is(err, ErrTimeout) {
			return "timed_out", nil, nil
		}
		return "received", payload, err
	case n.Wait != nil:
		d, _ := duration(n.Wait.Duration)
		return "completed", nil, Sleep(ctx, key, d)
	case n.Approval != nil:
		timeout, _ := duration(n.Approval.Timeout)
		d, err := WaitForApproval(ctx, key, Approval{Permission: n.Approval.Permission, Subject: n.Approval.Subject, Prompt: n.Approval.Prompt, Timeout: timeout})
		if errors.Is(err, ErrTimeout) {
			return "timed_out", nil, nil
		}
		if err != nil {
			return "", nil, err
		}
		decision, _ := json.Marshal(d)
		if d.Approved {
			return "approved", decision, nil
		}
		return "rejected", decision, nil
	}
	return "", nil, Permanent(fmt.Errorf("node %q has nothing to do", n.ID))
}

// value finds a template's value: an earlier node's output, then fields or array indexes.
func value(id, path string, outputs map[string]json.RawMessage) (any, error) {
	raw, ok := outputs[id]
	if !ok || len(raw) == 0 {
		return nil, fmt.Errorf("{{%s%s}}: node %q produced no output", id, path, id)
	}
	v, err := decodeExact(raw)
	if err != nil {
		return nil, err
	}
	for _, field := range strings.Split(strings.TrimPrefix(path, "."), ".") {
		if field == "" {
			continue
		}
		switch current := v.(type) {
		case map[string]any:
			next, found := current[field]
			if !found {
				return nil, fmt.Errorf("{{%s%s}}: no field %q", id, path, field)
			}
			v = next
		case []any:
			i, err := strconv.Atoi(field)
			if err != nil || i < 0 || i >= len(current) {
				return nil, fmt.Errorf("{{%s%s}}: no element %q", id, path, field)
			}
			v = current[i]
		default:
			return nil, fmt.Errorf("{{%s%s}}: %q is not inside an object or list", id, path, field)
		}
	}
	return v, nil
}

func text(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// decodeExact decodes JSON keeping numbers as written (json.Number), so an integer above 2^53 is
// not rounded on its way through a plan.
func decodeExact(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var v any
	err := decoder.Decode(&v)
	return v, err
}

// resolveText fills every template in s with its value's text.
func resolveText(s string, outputs map[string]json.RawMessage) (string, error) {
	var failure error
	out := templateRef.ReplaceAllStringFunc(s, func(match string) string {
		m := templateRef.FindStringSubmatch(match)
		v, err := value(m[1], m[2], outputs)
		if err != nil && failure == nil {
			failure = err
		}
		return text(v)
	})
	return out, failure
}

// resolveInput fills templates in an input's string values: a string that is exactly one template
// takes that value as it is (a number stays a number); otherwise templates become text.
func resolveInput(raw json.RawMessage, outputs map[string]json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	v, err := decodeExact(raw)
	if err != nil {
		return nil, err
	}
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch current := v.(type) {
		case string:
			if m := templateRef.FindStringSubmatch(current); m != nil && m[0] == current {
				return value(m[1], m[2], outputs)
			}
			return resolveText(current, outputs)
		case map[string]any:
			for k, item := range current {
				resolved, err := walk(item)
				if err != nil {
					return nil, err
				}
				current[k] = resolved
			}
		case []any:
			for i, item := range current {
				resolved, err := walk(item)
				if err != nil {
					return nil, err
				}
				current[i] = resolved
			}
		}
		return v, nil
	}
	resolved, err := walk(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(resolved)
}

// LoadApprovedPlan reads the plan a run was started for, inside a step, and refuses it unless it
// is still the approved version (same digest), belongs to the run's firm, and this run is the one
// the approval started: starting the plan workflow again never re-runs an approved plan.
func LoadApprovedPlan(ctx context.Context, id int64, digest string) (Plan, error) {
	x := executionOf(ctx)
	if x == nil {
		return Plan{}, Permanent(errors.New("an approved plan is loaded inside its run"))
	}
	var raw []byte
	var state string
	var approved, org sql.NullString
	var runID sql.NullInt64
	err := DB(ctx).QueryRowContext(ctx, `SELECT plan,state,approved_digest,organization_id,run_id FROM workflow_plans WHERE id=$1`, id).Scan(&raw, &state, &approved, &org, &runID)
	if err != nil {
		return Plan{}, err
	}
	if state != "approved" || approved.String != digest || org.String != Organization(ctx) {
		return Plan{}, Permanent(fmt.Errorf("plan %d is not approved as %s for this firm", id, digest))
	}
	if runID.Int64 != x.run.id {
		return Plan{}, Permanent(fmt.Errorf("plan %d was approved to run as run %d, not this one; approve it again to run it again", id, runID.Int64))
	}
	return ParsePlan(raw)
}

// PlanDraft is a new plan.
type PlanDraft struct {
	Organization string          // the firm it runs for; empty only for a template shared by all firms
	Plan         json.RawMessage // see Plan
	Source       string          // "staff", "member" or "agent"
	CreatedBy    string
	Agent        string // for agent proposals: which agent
	Rationale    string // for agent proposals: why
	IsTemplate   bool
}

// CreatePlan saves a plan after checking its structure. A staff or member plan starts as a draft; an
// agent's plan can only be created as a proposal (at most 20 a day per firm), so nothing an agent
// writes runs until a person approves it.
func CreatePlan(ctx context.Context, db *sql.DB, d PlanDraft) (int64, error) {
	var id int64
	err := inTx(ctx, db, func(tx *sql.Tx) (err error) {
		id, err = CreatePlanTx(ctx, tx, d)
		return err
	})
	return id, err
}

// CreatePlanTx is CreatePlan in the caller's transaction.
func CreatePlanTx(ctx context.Context, tx *sql.Tx, d PlanDraft) (int64, error) {
	p, err := ParsePlan(d.Plan)
	if err == nil {
		err = p.check(nil)
	}
	if err != nil {
		return 0, err
	}
	state := "draft"
	switch {
	case strings.TrimSpace(d.CreatedBy) == "" || !slices.Contains([]string{"staff", "member", "agent"}, d.Source):
		return 0, errors.New("a plan needs who created it and a source of staff, member or agent")
	case d.Organization == "" && !d.IsTemplate:
		return 0, errors.New("a plan that runs needs a firm; only templates are shared")
	case d.Source == "agent":
		if d.IsTemplate || d.Organization == "" || strings.TrimSpace(d.Rationale) == "" {
			return 0, errors.New("an agent proposes a plan for one firm and says why")
		}
		state = "proposed"
	}
	canonical, _ := json.Marshal(p)
	if d.Source == "agent" {
		// Lock the firm first, then count in a new statement, whose snapshot includes a concurrent
		// proposal that committed while this one waited: concurrent proposals stay within the limit.
		var firm string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 FOR UPDATE`, d.Organization).Scan(&firm); err != nil {
			return 0, err
		}
		var today int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM workflow_plans WHERE organization_id=$1 AND source='agent' AND created_at>now()-interval '1 day'`, d.Organization).Scan(&today); err != nil {
			return 0, err
		}
		if today >= maxAgentPlansPerDay {
			return 0, fmt.Errorf("%w: this firm already has %d agent proposals today", ErrNotAllowed, today)
		}
	}
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO workflow_plans(organization_id,title,plan,digest,state,source,rationale,agent,created_by,is_template)
 VALUES(NULLIF($1,''),$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`, d.Organization, p.Title, string(canonical), p.Digest(), state, d.Source, truncate(d.Rationale), d.Agent, d.CreatedBy, d.IsTemplate).Scan(&id)
	return id, err
}

// UpdatePlan replaces a draft or rejected plan's content; it becomes a draft again. An agent's
// proposal is not edited: copy it into a new plan instead.
func UpdatePlan(ctx context.Context, db *sql.DB, id int64, raw json.RawMessage, by string) error {
	return inTx(ctx, db, func(tx *sql.Tx) error { return UpdatePlanTx(ctx, tx, id, raw, by) })
}

// UpdatePlanTx is UpdatePlan in the caller's transaction.
func UpdatePlanTx(ctx context.Context, tx *sql.Tx, id int64, raw json.RawMessage, by string) error {
	p, err := ParsePlan(raw)
	if err == nil {
		err = p.check(nil)
	}
	if err != nil {
		return err
	}
	canonical, _ := json.Marshal(p)
	return planChangeTx(ctx, tx, id, func(tx *sql.Tx, state, source string) error {
		if source == "agent" || (state != "draft" && state != "rejected") {
			return ErrNotAllowed
		}
		_, err := tx.ExecContext(ctx, `UPDATE workflow_plans SET plan=$2,digest=$3,title=$4,state='draft',updated_at=now() WHERE id=$1`, id, string(canonical), p.Digest(), p.Title)
		return err
	})
}

// ProposePlanTx is ProposePlan in the caller's transaction. A template is never proposed: it is copied.
func ProposePlanTx(ctx context.Context, tx *sql.Tx, id int64, by string) error {
	return planChangeTx(ctx, tx, id, func(tx *sql.Tx, state, _ string) error {
		if state != "draft" || isTemplate(ctx, tx, id) {
			return ErrNotAllowed
		}
		_, err := tx.ExecContext(ctx, `UPDATE workflow_plans SET state='proposed',updated_at=now() WHERE id=$1`, id)
		return err
	})
}

// RejectPlanTx is RejectPlan in the caller's transaction. A template is never rejected: it is archived.
func RejectPlanTx(ctx context.Context, tx *sql.Tx, id int64, by string) error {
	return planChangeTx(ctx, tx, id, func(tx *sql.Tx, state, _ string) error {
		if (state != "draft" && state != "proposed") || isTemplate(ctx, tx, id) {
			return ErrNotAllowed
		}
		_, err := tx.ExecContext(ctx, `UPDATE workflow_plans SET state='rejected',updated_at=now() WHERE id=$1`, id)
		return err
	})
}

// ApprovePlan approves the exact version (digest) the approver saw and starts it. permissions are
// the approver's in the plan's firm, from their verified session; they must hold every activity's
// permission, and the plan is checked again in full. Templates are copied first, never run.
func ApprovePlan(ctx context.Context, db *sql.DB, id int64, digest, approver string, permissions []string) (int64, error) {
	var runID int64
	err := inTx(ctx, db, func(tx *sql.Tx) (err error) {
		runID, err = ApprovePlanTx(ctx, tx, id, digest, approver, permissions)
		return err
	})
	return runID, err
}

// ApprovePlanTx is ApprovePlan in the caller's transaction, which also holds the run it starts.
func ApprovePlanTx(ctx context.Context, tx *sql.Tx, id int64, digest, approver string, permissions []string) (int64, error) {
	if strings.TrimSpace(approver) == "" {
		return 0, errors.New("an approval needs who approved")
	}
	if permissions == nil {
		permissions = []string{} // an approval always checks permissions: nil holds none
	}
	var runID int64
	err := planChangeTx(ctx, tx, id, func(tx *sql.Tx, state, _ string) error {
		var raw []byte
		var current string
		var template bool
		var org sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT plan,digest,is_template,organization_id FROM workflow_plans WHERE id=$1`, id).Scan(&raw, &current, &template, &org); err != nil {
			return err
		}
		if (state != "draft" && state != "proposed") || template || !org.Valid {
			return ErrNotAllowed
		}
		if current != digest {
			return fmt.Errorf("%w: the plan changed since it was reviewed; review it again", ErrNotAllowed)
		}
		p, err := ParsePlan(raw)
		if err == nil {
			err = p.check(permissions)
		}
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE workflow_plans SET state='approved',approved_by=$2,approved_at=now(),approved_digest=digest,updated_at=now() WHERE id=$1`, id, approver); err != nil {
			return err
		}
		runID, _, err = Start(ctx, tx, StartRequest{Workflow: "plan", Organization: org.String, Input: map[string]any{"plan_id": id, "digest": digest},
			IdempotencyKey: fmt.Sprintf("plan:%d:%s", id, digest[:16]), StartedBy: approver})
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE workflow_runs SET plan_id=$1 WHERE id=$2`, id, runID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE workflow_plans SET run_id=$2 WHERE id=$1`, id, runID)
		return err
	})
	return runID, err
}

// InstantiateTemplate copies a template into a new draft for one firm.
func InstantiateTemplate(ctx context.Context, db *sql.DB, templateID int64, organization, by string) (int64, error) {
	var id int64
	err := inTx(ctx, db, func(tx *sql.Tx) (err error) {
		id, err = InstantiateTemplateTx(ctx, tx, templateID, organization, by)
		return err
	})
	return id, err
}

// InstantiateTemplateTx is InstantiateTemplate in the caller's transaction.
func InstantiateTemplateTx(ctx context.Context, tx *sql.Tx, templateID int64, organization, by string) (int64, error) {
	if organization == "" || strings.TrimSpace(by) == "" {
		return 0, errors.New("a copy of a template needs a firm and who made it")
	}
	var id int64
	err := tx.QueryRowContext(ctx, `INSERT INTO workflow_plans(organization_id,title,plan,digest,state,source,created_by,template_id)
 SELECT $2,title,plan,digest,'draft','staff',$3,id FROM workflow_plans WHERE id=$1 AND is_template AND (organization_id IS NULL OR organization_id=$2) RETURNING id`, templateID, organization, by).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: no such template for this firm", ErrNotAllowed)
	}
	return id, err
}

// isTemplate says whether a plan the caller has locked is a template; an unreadable one counts as one, so it is refused.
func isTemplate(ctx context.Context, tx *sql.Tx, id int64) bool {
	template := true
	tx.QueryRowContext(ctx, `SELECT is_template FROM workflow_plans WHERE id=$1`, id).Scan(&template)
	return template
}

// planChangeTx locks the plan and hands its state to change.
func planChangeTx(ctx context.Context, tx *sql.Tx, id int64, change func(tx *sql.Tx, state, source string) error) error {
	var state, source string
	if err := tx.QueryRowContext(ctx, `SELECT state,source FROM workflow_plans WHERE id=$1 FOR UPDATE`, id).Scan(&state, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("plan %d was not found: %w", id, err)
		}
		return err
	}
	return change(tx, state, source)
}
