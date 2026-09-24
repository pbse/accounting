package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"close.local/backend/internal/auth"
)

// agentGrant returns the member's approved agent scope while their membership is unchanged.
// Agents see only the member's assigned clients; firm-wide access is never extended to them.
func agentGrant(ctx context.Context, db *sql.DB, org, subject string) (scope string, err error) {
	err = db.QueryRowContext(ctx, `SELECT scope FROM effective_agent_access WHERE organization_id=$1 AND subject=$2`, org, subject).Scan(&scope)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return scope, err
}

func registerAgentAccess(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/agent-access", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" {
			problem(w, 409, "Agent access is approved per member in a hosted firm. Locally, agents use the workspace token.")
			return
		}
		var mine, members []byte
		const row, from = `to_jsonb(a)||jsonb_build_object('effective_scope',e.scope)`, ` FROM current_agent_access a LEFT JOIN effective_agent_access e ON e.organization_id=a.organization_id AND e.subject=a.subject`
		if err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT `+row+from+` WHERE a.organization_id=$1 AND a.subject=$2),'null'::jsonb)`, a.Organization, a.Subject).Scan(&mine); err != nil {
			problem(w, 503, "Agent access unavailable.")
			return
		}
		members = []byte("[]")
		if canManageMembers(r) {
			if err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(`+row+` ORDER BY a.id DESC),'[]'::jsonb)`+from+` WHERE a.organization_id=$1`, a.Organization).Scan(&members); err != nil {
				problem(w, 503, "Agent access unavailable.")
				return
			}
		}
		jsonReply(w, 200, map[string]any{"mine": json.RawMessage(mine), "members": json.RawMessage(members)})
	})
	mux.HandleFunc("POST /v1/closes/agent-access", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" {
			problem(w, 409, "Agent access is approved per member in a hosted firm.")
			return
		}
		var in struct {
			Action     string `json:"action"`
			Subject    string `json:"subject"`
			Scope      string `json:"scope"`
			PreviousID string `json:"previous_id"`
			Reason     string `json:"reason"`
			Confirmed  bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		target := strings.TrimSpace(in.Subject)
		if in.Action == "request" {
			target = a.Subject
		}
		admin := in.Action == "approve" || in.Action == "decline"
		if !in.Confirmed || len(in.Reason) < 10 || len(in.Reason) > 2000 || !strings.HasPrefix(target, "user_") || (in.Action != "request" && !admin && in.Action != "revoke") {
			problem(w, 422, "Confirm the action and give a reason (10–2,000 characters).")
			return
		}
		if (admin || (in.Action == "revoke" && target != a.Subject)) && !canManageMembers(r) {
			problem(w, 403, "Approving or revoking another member's agent access requires clients:all and members:manage.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Agent access storage unavailable.")
			return
		}
		defer tx.Rollback()
		// One decision at a time per firm, so a stale approval cannot land after a revocation.
		var org string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, a.Organization).Scan(&org); err != nil {
			problem(w, 403, "Organization unavailable.")
			return
		}
		var member string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted`, a.Organization, target).Scan(&member); err != nil {
			problem(w, 409, "Agent access is only for current firm members.")
			return
		}
		var current struct{ ID, State, Scope, Member string }
		err = tx.QueryRowContext(r.Context(), `SELECT id::text,state,scope,member_access_id::text FROM current_agent_access WHERE organization_id=$1 AND subject=$2`, a.Organization, target).Scan(&current.ID, &current.State, &current.Scope, &current.Member)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Agent access unavailable.")
			return
		}
		if current.ID != in.PreviousID {
			problem(w, 409, "Agent access changed. Refresh before deciding.")
			return
		}
		state, scope := "", in.Scope
		switch in.Action {
		case "request":
			if scope != "read" && scope != "prepare" {
				problem(w, 422, "Choose read or prepare.")
				return
			}
			state = "requested"
		case "approve", "decline":
			if current.State != "requested" || current.Member != member {
				problem(w, 409, "There is no current request from this member to decide.")
				return
			}
			// An administrator can narrow a request, never widen it.
			if (scope != "read" && scope != "prepare") || (scope == "prepare" && current.Scope == "read") {
				problem(w, 422, "An approval can narrow the member's request but not widen it.")
				return
			}
			state = map[string]string{"approve": "approved", "decline": "declined"}[in.Action]
		case "revoke":
			if current.ID == "" || current.State == "revoked" {
				problem(w, 409, "There is no agent access to revoke.")
				return
			}
			state, scope = "revoked", current.Scope
		}
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO agent_access(organization_id,subject,state,scope,member_access_id,reason,actor) VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id::text`, a.Organization, target, state, scope, member, in.Reason, a.Subject).Scan(&id); err != nil || tx.Commit() != nil {
			problem(w, 503, "Agent access change could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "state": state})
	})
}
