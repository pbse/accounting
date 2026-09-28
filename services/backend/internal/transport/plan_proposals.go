package transport

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/wf"
)

// Plans an agent proposes (docs/workflow-engine.md, phase W4). A firm's connected agent (MCP, under the member's
// approved agent access) reads the catalog and proposes a plan with its reason; staff can also ask the app's planner to
// draft one with the firm's model (admin_planner.go). Either way the plan is saved as the agent's proposal, which
// platform staff approve or reject in the admin panel: nothing an agent writes runs before a person approves it.

// planFormat is the plan format, for an agent or a model writing one.
const planFormat = `A plan is JSON: {"version":1,"title":"…","nodes":[…]}, at most 50 nodes and 64 KiB. Each node has an "id" (lowercase letters, digits and _, starting with a letter, unique) and exactly one of:
- "activity" (a catalog activity's name) with "input" (an object fitting its input schema);
- "wait": {"event":"<key>","timeout":"<duration>"} or {"duration":"<duration>"};
- "approval": {"permission":"<firm permission>","timeout":"<duration>","subject":"…","prompt":"…"}.
Nodes run in the order listed. "after" lists earlier node ids this node waits for; without "when" it runs if they all succeeded (completed, received, approved). "when":"<node>.<outcome>" (a node in "after") runs it if that node ended that way: activities completed or failed; event waits received or timed_out; duration waits completed; approvals approved, rejected or timed_out. A node that does not run is skipped. The run fails if a node failed and no node handles "<node>.failed".
Strings in "input" and event keys may use {{node.field}} for an earlier node's output. Durations use Go's form ("72h"), at most 90 days. An activity whose effect is external must directly follow an approval node. Event keys never start with "wf.".`

// catalogActivity is one activity a plan may use.
type catalogActivity struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Permission  string         `json:"permission"`
	Effect      string         `json:"effect"`
	Input       map[string]any `json:"input"`
}

// planCatalog is what an agent's plan may be built from: the format and every activity, with its input schema, by name.
func planCatalog() map[string]any {
	activities := []catalogActivity{}
	for _, a := range wf.Activities() {
		activities = append(activities, catalogActivity{a.Name, a.Title, a.Description, a.Permission, string(a.Effect), a.Input})
	}
	slices.SortFunc(activities, func(a, b catalogActivity) int { return strings.Compare(a.Name, b.Name) })
	return map[string]any{"format": planFormat, "activities": activities}
}

// planAgent is who proposes through this request: its agent attribution, or MCP signed in with the workspace token or a
// session. "" means a person using the app directly, who does not propose plans here.
func planAgent(r *http.Request) string {
	if agent := auth.Agent(r.Context()); agent != "" {
		return agent
	}
	if r.Context().Value(mcpScope{}) != nil {
		return "MCP"
	}
	return ""
}

func registerPlanProposals(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/plans/catalog", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, 200, planCatalog())
	})
	// The firm's agent proposals, newest first, 50 at a time (before pages back), with where each stands, so an agent
	// learns what became of them. A member limited to some clients sees only the proposals made as them: others may
	// be about clients they cannot open.
	mux.HandleFunc("GET /v1/closes/plans/proposals", func(w http.ResponseWriter, r *http.Request) {
		before, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
		if r.URL.Query().Get("before") != "" && (err != nil || before < 1) {
			problem(w, 422, "before is a proposal's ID.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT id,title,state,agent,created_by,created_at,updated_at,approved_at,run_id FROM workflow_plans
 WHERE organization_id=$1 AND source='agent' AND ($2 OR created_by=$3) AND ($4=0 OR id<$4) ORDER BY id DESC LIMIT 51`, organization(r), firmAccess(r), subject(r), before)
		if err != nil {
			problem(w, 503, "Plan proposals are unavailable.")
			return
		}
		defer rows.Close()
		type proposal struct {
			ID         int64      `json:"id"`
			Title      string     `json:"title"`
			State      string     `json:"state"`
			Agent      string     `json:"agent"`
			CreatedBy  string     `json:"created_by"`
			CreatedAt  time.Time  `json:"created_at"`
			UpdatedAt  time.Time  `json:"updated_at"`
			ApprovedAt *time.Time `json:"approved_at"`
			RunID      *int64     `json:"run_id"`
		}
		proposals := []proposal{}
		for rows.Next() {
			var p proposal
			if err = rows.Scan(&p.ID, &p.Title, &p.State, &p.Agent, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt, &p.ApprovedAt, &p.RunID); err != nil {
				problem(w, 503, "Plan proposals are unavailable.")
				return
			}
			proposals = append(proposals, p)
		}
		if rows.Err() != nil {
			problem(w, 503, "Plan proposals are unavailable.")
			return
		}
		next := any(nil)
		if len(proposals) > 50 {
			proposals = proposals[:50]
			next = proposals[49].ID
		}
		jsonReply(w, 200, map[string]any{"proposals": proposals, "next_before": next})
	})
	// A connected agent proposes a plan for the firm, saying why. It needs workpapers:review, and it waits for platform
	// staff: nothing in it runs before they approve the exact version they reviewed.
	mux.HandleFunc("POST /v1/closes/plans/proposals", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		agent := planAgent(r)
		if agent == "" {
			problem(w, 403, "Plans are proposed by a connected agent, through MCP; platform staff build them in the admin panel.")
			return
		}
		var in struct {
			Plan      json.RawMessage `json:"plan"`
			Rationale string          `json:"rationale"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Rationale = strings.TrimSpace(in.Rationale); len(in.Rationale) < 10 || len(in.Rationale) > 2000 {
			problem(w, 422, "Say why this plan should run, in 10 to 2,000 characters.")
			return
		}
		id, err := wf.CreatePlan(r.Context(), db, wf.PlanDraft{Organization: organization(r), Plan: in.Plan, Source: "agent", CreatedBy: actor, Agent: agent, Rationale: in.Rationale})
		switch {
		case errors.Is(err, wf.ErrPlanInvalid):
			problem(w, 422, strings.TrimPrefix(err.Error(), wf.ErrPlanInvalid.Error()+": "))
		case errors.Is(err, wf.ErrNotAllowed):
			problem(w, 429, "This firm has had as many agent proposals as it may today. Try again tomorrow.")
		case err != nil:
			problem(w, 503, "The plan could not be proposed. Try again.")
		default:
			jsonReply(w, 200, map[string]any{"plan_id": id, "state": "proposed", "message": "Proposed to platform staff, who approve or reject it. Nothing runs before they approve it."})
		}
	})
}
