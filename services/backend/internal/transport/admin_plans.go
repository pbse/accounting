package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"close.local/backend/internal/wf"
)

// Plans in the staff console (docs/admin-panel.md, phase A3): plans staff built and plans the agent proposed, the queue
// waiting for approval, and templates. A plan holds client content (node inputs, event keys, approval text, an agent's
// rationale, its title), so it is masked like a run's until a staff member with platform:content reveals it. Approving
// means reading the plan, so it needs platform:content as well as platform:plans. Staff approve with no firm
// permissions: a plan whose activities need a firm permission waits for the firm.
func registerAdminPlans(mux *http.ServeMux, db *sql.DB, token string) {
	route := func(next func(http.ResponseWriter, *http.Request, *sql.DB)) http.HandlerFunc {
		return adminGuard(token, db, platformPlans, func(w http.ResponseWriter, r *http.Request) { next(w, r, db) })
	}
	mux.HandleFunc("GET /v1/admin/plans", route(adminPlans))
	mux.HandleFunc("POST /v1/admin/plans", route(adminCreatePlan))
	mux.HandleFunc("POST /v1/admin/plans/validate", route(adminValidatePlan))
	mux.HandleFunc("POST /v1/admin/plans/{id}", route(adminUpdatePlan))
	mux.HandleFunc("GET /v1/admin/plans/{id}", route(adminPlan))
	mux.HandleFunc("POST /v1/admin/plans/{id}/propose", route(func(w http.ResponseWriter, r *http.Request, db *sql.DB) { adminPlanDecision(w, r, db, "propose") }))
	mux.HandleFunc("POST /v1/admin/plans/{id}/reject", route(func(w http.ResponseWriter, r *http.Request, db *sql.DB) { adminPlanDecision(w, r, db, "reject") }))
	mux.HandleFunc("POST /v1/admin/plans/{id}/approve", route(adminApprovePlan))
	mux.HandleFunc("POST /v1/admin/plans/{id}/instantiate", route(adminInstantiateTemplate))
}

// maskPlan hides a plan's client content: its title, node inputs, approval subject and prompt, and event keys (which
// can be built from client data). Node IDs, kinds, activities, workflows, order, conditions and durations show, enough
// to draw it.
func maskPlan(raw []byte) json.RawMessage {
	var p wf.Plan
	if json.Unmarshal(raw, &p) != nil {
		return hidden(raw)
	}
	p.Title = maskText(p.Title)
	for i := range p.Nodes {
		n := &p.Nodes[i]
		n.Input = nil
		if n.Approval != nil {
			n.Approval.Subject, n.Approval.Prompt = "", ""
		}
		if n.Wait != nil {
			n.Wait.Event = maskText(n.Wait.Event)
		}
	}
	out, _ := json.Marshal(p)
	return out
}

type adminPlanRow struct {
	ID           int64           `json:"id"`
	Organization *string         `json:"organization"`
	FirmName     *string         `json:"firm_name"`
	Title        string          `json:"title"`
	State        string          `json:"state"`
	Source       string          `json:"source"`
	Agent        string          `json:"agent"`
	Rationale    string          `json:"rationale"`
	CreatedBy    string          `json:"created_by"`
	ApprovedBy   *string         `json:"approved_by"`
	ApprovedAt   *string         `json:"approved_at"`
	IsTemplate   bool            `json:"is_template"`
	TemplateID   *int64          `json:"template_id"`
	RunID        *int64          `json:"run_id"`
	Digest       string          `json:"digest"`
	Nodes        int             `json:"nodes"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
	Plan         json.RawMessage `json:"plan,omitempty"`
}

const adminPlanColumns = `p.id,p.organization_id,o.name,p.title,p.state,p.source,p.agent,p.rationale,p.created_by,p.approved_by,p.approved_at::text,
 p.is_template,p.template_id,p.run_id,p.digest,jsonb_array_length(p.plan->'nodes'),p.created_at::text,p.updated_at::text`

func scanPlan(row interface{ Scan(...any) error }, withPlan bool) (adminPlanRow, error) {
	var p adminPlanRow
	dest := []any{&p.ID, &p.Organization, &p.FirmName, &p.Title, &p.State, &p.Source, &p.Agent, &p.Rationale, &p.CreatedBy, &p.ApprovedBy, &p.ApprovedAt,
		&p.IsTemplate, &p.TemplateID, &p.RunID, &p.Digest, &p.Nodes, &p.CreatedAt, &p.UpdatedAt}
	if withPlan {
		dest = append(dest, &p.Plan)
	}
	return p, row.Scan(dest...)
}

// masked hides the plan's text unless revealed: its title and an agent's rationale, and the plan itself.
func (p *adminPlanRow) masked() {
	p.Title, p.Rationale = maskText(p.Title), maskText(p.Rationale)
	if p.Plan != nil {
		p.Plan = maskPlan(p.Plan)
	}
}

// adminPlans lists plans newest first, filtered by state, source, firm and whether they are templates.
func adminPlans(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	filters, ok := adminFilters(w, r, "state", "source", "firm")
	if !ok {
		return
	}
	offset, ok := adminOffset(w, r)
	if !ok {
		return
	}
	reveal, templates := r.URL.Query().Get("reveal") == "1", r.URL.Query().Get("templates") == "1"
	plans := []adminPlanRow{}
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT `+adminPlanColumns+` FROM workflow_plans p LEFT JOIN organizations o ON o.id=p.organization_id
 WHERE ($1='' OR p.state=$1) AND ($2='' OR p.source=$2) AND ($3='' OR p.organization_id=$3) AND p.is_template=$4
 ORDER BY p.id DESC LIMIT $5 OFFSET $6`, filters[0], filters[1], filters[2], templates, adminPage+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			p, err := scanPlan(rows, false)
			if err != nil {
				return err
			}
			if !reveal {
				p.masked()
			}
			plans = append(plans, p)
		}
		return rows.Err()
	})
	if err != nil {
		adminPlanProblem(w, err, "plans read")
		return
	}
	// A revealed list is recorded against every firm whose plans it shows (a shared template's at platform level), so
	// each firm's audit has it.
	if reveal {
		firms := []string{}
		for _, p := range plans {
			firm := ""
			if p.Organization != nil {
				firm = *p.Organization
			}
			if !slices.Contains(firms, firm) {
				firms = append(firms, firm)
			}
		}
		for _, firm := range firms {
			if !adminReveal(w, r, db, firm, "plans", "list") {
				return
			}
		}
	}
	more := len(plans) > adminPage
	if more {
		plans = plans[:adminPage]
	}
	jsonReply(w, 200, map[string]any{"plans": plans, "has_more": more, "next_offset": offset + len(plans), "revealed": reveal})
}

// adminPlanFirm reads which firm a plan belongs to ("" for a template shared by all firms).
func adminPlanFirm(ctx context.Context, db *sql.DB, id int64) (string, error) {
	var firm sql.NullString
	err := adminRead(ctx, db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT organization_id FROM workflow_plans WHERE id=$1`, id).Scan(&firm)
	})
	return firm.String, err
}

func planID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		problem(w, 404, "Plan not found.")
		return 0, false
	}
	return id, true
}

// adminPlan is one plan, masked unless revealed, with whether staff may approve it: a draft or proposal for a firm
// whose activities need no firm permission. Its problems, which quote the plan, show once revealed.
func adminPlan(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id, ok := planID(w, r)
	if !ok {
		return
	}
	reveal := r.URL.Query().Get("reveal") == "1"
	var p adminPlanRow
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) (err error) {
		p, err = scanPlan(tx.QueryRowContext(ctx, `SELECT `+adminPlanColumns+`,p.plan FROM workflow_plans p LEFT JOIN organizations o ON o.id=p.organization_id WHERE p.id=$1`, id), true)
		return err
	})
	if err != nil {
		adminPlanProblem(w, err, "plan read")
		return
	}
	firm := ""
	if p.Organization != nil {
		firm = *p.Organization
	}
	// The reveal records the version shown: approving needs that the approver read that exact version.
	if reveal && !adminRevealOf(w, r, db, firm, "plan", strconv.FormatInt(id, 10), map[string]string{"digest": p.Digest}) {
		return
	}
	problems := []string{}
	if plan, err := wf.ParsePlan(p.Plan); err != nil {
		problems = append(problems, err.Error())
	} else if found := plan.Problems([]string{}); found != nil { // staff hold no firm permissions
		problems = found
	}
	approvable := (p.State == "draft" || p.State == "proposed") && !p.IsTemplate && p.Organization != nil && len(problems) == 0
	// Each step's input exactly as stored, for the builder: a browser parsing JSON rounds integers beyond 2^53.
	var inputs map[string]string
	if reveal {
		if plan, err := wf.ParsePlan(p.Plan); err == nil {
			inputs = map[string]string{}
			for _, n := range plan.Nodes {
				if len(n.Input) > 0 {
					inputs[n.ID] = string(n.Input)
				}
			}
		}
	} else {
		p.masked()
		problems = slices.Repeat([]string{"••••"}, len(problems))
	}
	jsonReply(w, 200, map[string]any{"plan": p, "inputs": inputs, "problems": problems, "approvable": approvable, "revealed": reveal})
}

// errPlanNotReviewed is an approval of a version the staff member has not revealed, and so has not read.
var errPlanNotReviewed = errors.New("reveal and read this version of the plan before approving it")

// adminPlanProblem answers a failed plan read or action.
func adminPlanProblem(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, errReasonRequired):
		problem(w, 422, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		problem(w, 404, "Plan not found.")
	case errors.Is(err, errPlanNotReviewed):
		problem(w, 409, "Reveal and read this version of the plan before approving it.")
	case errors.Is(err, wf.ErrPlanInvalid):
		problem(w, 422, "The plan cannot be approved as it is. Reveal it to see why.")
	case errors.Is(err, wf.ErrNotAllowed):
		problem(w, 409, "The plan is not in a state that allows this, or it changed since you loaded it. Refresh and review it again.")
	case errors.Is(err, wf.ErrUnknownWorkflow):
		problem(w, 503, "This build cannot run plans.")
	default:
		log.Printf("Admin %s failed: %v", what, err)
		problem(w, 503, "The "+what+" could not be completed. Refresh and try again.")
	}
}

// adminPlanDecision proposes a draft for approval or rejects a draft or proposal, as the plan's firm.
func adminPlanDecision(w http.ResponseWriter, r *http.Request, db *sql.DB, decision string) {
	id, ok := planID(w, r)
	if !ok {
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	firm, err := adminPlanFirm(r.Context(), db, id)
	if err != nil {
		adminPlanProblem(w, err, "plan read")
		return
	}
	by := "staff:" + staffOf(r.Context()).Subject
	err = adminAct(r.Context(), db, adminAction{Firm: firm, Action: "plan." + decision, TargetType: "plan", TargetID: strconv.FormatInt(id, 10), Reason: strings.TrimSpace(in.Reason)},
		func(ctx context.Context, tx *sql.Tx) error {
			if decision == "propose" {
				return wf.ProposePlanTx(ctx, tx, id, by)
			}
			return wf.RejectPlanTx(ctx, tx, id, by)
		})
	if err != nil {
		adminPlanProblem(w, err, "plan "+decision)
		return
	}
	jsonReply(w, 200, map[string]bool{"ok": true})
}

// adminApprovePlan approves exactly the version the staff member reviewed (its digest) and starts it, as the firm.
func adminApprovePlan(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id, ok := planID(w, r)
	if !ok {
		return
	}
	member := staffOf(r.Context())
	if !slices.Contains(member.Permissions, platformContent) {
		problem(w, 403, "Approving a plan means reading it, so it needs platform:content as well as platform:plans.")
		return
	}
	var in struct {
		Digest string `json:"digest"`
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if len(in.Digest) != 64 {
		problem(w, 422, "Approve the digest of the version you reviewed.")
		return
	}
	firm, err := adminPlanFirm(r.Context(), db, id)
	if err != nil {
		adminPlanProblem(w, err, "plan read")
		return
	}
	var run int64
	err = adminAct(r.Context(), db, adminAction{Firm: firm, Action: "plan.approve", TargetType: "plan", TargetID: strconv.FormatInt(id, 10), Reason: strings.TrimSpace(in.Reason), Request: map[string]string{"digest": in.Digest}},
		func(ctx context.Context, tx *sql.Tx) (err error) {
			// The approver read this exact version: they revealed it, with this digest, within the last day.
			var read bool
			if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM platform_audit WHERE staff_user=$1 AND action='content.reveal' AND result='ok'
 AND target_type='plan' AND target_id=$2 AND request->>'digest'=$3 AND created_at>now()-interval '1 day')`, member.Subject, strconv.FormatInt(id, 10), in.Digest).Scan(&read); err != nil {
				return err
			}
			if !read {
				return errPlanNotReviewed
			}
			// Staff hold no firm permissions: a plan whose activities need one waits for the firm.
			run, err = wf.ApprovePlanTx(ctx, tx, id, in.Digest, "staff:"+member.Subject, []string{})
			return err
		})
	if err != nil {
		adminPlanProblem(w, err, "plan approval")
		return
	}
	jsonReply(w, 200, map[string]int64{"run_id": run})
}

// adminInstantiateTemplate copies a template into a new draft for a firm.
func adminInstantiateTemplate(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id, ok := planID(w, r)
	if !ok {
		return
	}
	var in struct {
		Firm   string `json:"firm"`
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, in.Firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	by := "staff:" + staffOf(r.Context()).Subject
	var draft int64
	err := adminAct(r.Context(), db, adminAction{Firm: in.Firm, Action: "plan.instantiate", TargetType: "plan", TargetID: strconv.FormatInt(id, 10), Reason: strings.TrimSpace(in.Reason)},
		func(ctx context.Context, tx *sql.Tx) (err error) {
			draft, err = wf.InstantiateTemplateTx(ctx, tx, id, in.Firm, by)
			return err
		})
	if err != nil {
		adminPlanProblem(w, err, "template copy")
		return
	}
	jsonReply(w, 200, map[string]int64{"plan_id": draft})
}

// planProblems is what is wrong with a plan being written, in the author's own words, and whether staff could approve it
// as it stands (its activities need no firm permission).
func planProblems(raw json.RawMessage) (problems []string, digest string, staffApprovable bool) {
	p, err := wf.ParsePlan(raw)
	if err != nil {
		return []string{strings.TrimPrefix(err.Error(), wf.ErrPlanInvalid.Error()+": ")}, "", false
	}
	if problems = p.Problems(nil); len(problems) > 0 {
		return problems, "", false
	}
	return []string{}, p.Digest(), len(p.Problems([]string{})) == 0
}

// adminValidatePlan checks a plan as it is written, without saving it, for the builder's live problems list.
func adminValidatePlan(w http.ResponseWriter, r *http.Request, _ *sql.DB) {
	var in struct {
		Plan json.RawMessage `json:"plan"`
	}
	if !decode(w, r, &in) {
		return
	}
	problems, digest, staff := planProblems(in.Plan)
	jsonReply(w, 200, map[string]any{"problems": problems, "digest": digest, "staff_approvable": staff})
}

// adminPlanWriteProblem answers a refused save: the author's own plan is quoted back to them.
func adminPlanWriteProblem(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, wf.ErrPlanInvalid) {
		problem(w, 422, strings.TrimPrefix(err.Error(), wf.ErrPlanInvalid.Error()+": "))
		return
	}
	if errors.Is(err, wf.ErrNotAllowed) || errors.Is(err, errNotStaffPlan) {
		problem(w, 409, "Only a draft or rejected plan staff wrote can be edited; copy anyone else's into a new plan instead. Refresh and try again.")
		return
	}
	adminPlanProblem(w, err, what)
}

// adminCreatePlan saves a new draft for a firm, or a template (for one firm, or shared with every firm when no firm is
// named). Nothing runs until the draft is approved.
func adminCreatePlan(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var in struct {
		Firm     string          `json:"firm"`
		Template bool            `json:"template"`
		Plan     json.RawMessage `json:"plan"`
		Reason   string          `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	in.Firm = strings.TrimSpace(in.Firm)
	if in.Firm == "" && !in.Template {
		problem(w, 422, "A plan runs for one firm: name it. Only a template is shared by every firm.")
		return
	}
	if in.Firm != "" {
		if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, in.Firm) }); err != nil {
			adminFirmProblem(w, err, "firm read")
			return
		}
	}
	by := "staff:" + staffOf(r.Context()).Subject
	request := map[string]any{"template": in.Template}
	var id int64
	err := adminAct(r.Context(), db, adminAction{Firm: in.Firm, Action: "plan.create", TargetType: "plan", Reason: strings.TrimSpace(in.Reason), Request: request},
		func(ctx context.Context, tx *sql.Tx) (err error) {
			if id, err = wf.CreatePlanTx(ctx, tx, wf.PlanDraft{Organization: in.Firm, Plan: in.Plan, Source: "staff", CreatedBy: by, IsTemplate: in.Template}); err == nil {
				request["plan_id"] = id
			}
			return err
		})
	if err != nil {
		adminPlanWriteProblem(w, err, "plan save")
		return
	}
	jsonReply(w, 200, map[string]int64{"plan_id": id})
}

// errNotStaffPlan is an edit of a plan an agent or a firm member wrote: staff copy it into a plan of their own instead.
var errNotStaffPlan = errors.New("only a plan staff wrote is edited in place")

// adminUpdatePlan replaces the content of a draft or rejected plan staff wrote; it becomes a draft again. A plan an
// agent or a firm member wrote keeps their words: staff copy it instead.
func adminUpdatePlan(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	id, ok := planID(w, r)
	if !ok {
		return
	}
	var in struct {
		Plan   json.RawMessage `json:"plan"`
		Reason string          `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	firm, err := adminPlanFirm(r.Context(), db, id)
	if err != nil {
		adminPlanProblem(w, err, "plan read")
		return
	}
	by := "staff:" + staffOf(r.Context()).Subject
	err = adminAct(r.Context(), db, adminAction{Firm: firm, Action: "plan.update", TargetType: "plan", TargetID: strconv.FormatInt(id, 10), Reason: strings.TrimSpace(in.Reason)},
		func(ctx context.Context, tx *sql.Tx) error {
			var source string
			if err := tx.QueryRowContext(ctx, `SELECT source FROM workflow_plans WHERE id=$1 FOR UPDATE`, id).Scan(&source); err != nil {
				return err
			}
			if source != "staff" {
				return errNotStaffPlan
			}
			return wf.UpdatePlanTx(ctx, tx, id, in.Plan, by)
		})
	if err != nil {
		adminPlanWriteProblem(w, err, "plan save")
		return
	}
	jsonReply(w, 200, map[string]int64{"plan_id": id})
}
