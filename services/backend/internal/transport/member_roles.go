package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// The firm's people and their roles (docs/sign-in.md). People who join by their company address arrive with the member
// role, which opens nothing; the firm's administrator gives each one a role here. WorkOS holds the role; the firm keeps
// the record of who gave which role, and why.

type firmPerson struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Status  string `json:"status"`
	Access  string `json:"access"`
}

// mostPeople bounds the list: each person costs a WorkOS lookup.
const mostPeople = 200

func registerMemberRoles(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/members/people", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		if os.Getenv("WORKOS_API_KEY") == "" {
			problem(w, 503, "The firm's people need the server's WorkOS API key.")
			return
		}
		org := organization(r)
		memberships, err := workosList[firmMembership](r.Context(), "/user_management/organization_memberships", url.Values{"organization_id": {org}, "limit": {"100"}})
		if err != nil {
			problem(w, 502, "WorkOS could not list the firm's people. Try again.")
			return
		}
		roles, _, err := firmRoles(r.Context(), r)
		if err != nil {
			problem(w, 502, "WorkOS roles are unavailable.")
			return
		}
		access := map[string]bool{}
		rows, err := db.QueryContext(r.Context(), `SELECT subject,granted FROM current_organization_members WHERE organization_id=$1`, org)
		if err != nil {
			problem(w, 503, "Member access unavailable.")
			return
		}
		for rows.Next() {
			var sub string
			var granted bool
			if rows.Scan(&sub, &granted) == nil {
				access[sub] = granted
			}
		}
		if rows.Close(); rows.Err() != nil {
			problem(w, 503, "Member access unavailable.")
			return
		}
		people := []firmPerson{}
		for _, m := range memberships[:min(len(memberships), mostPeople)] {
			var user struct {
				Email     string `json:"email"`
				FirstName string `json:"first_name"`
				LastName  string `json:"last_name"`
			}
			if workos(r.Context(), "GET", "/user_management/users/"+url.PathEscape(m.UserID), nil, &user) != nil {
				problem(w, 502, "WorkOS could not list the firm's people. Try again.")
				return
			}
			state := "none"
			if granted, decided := access[m.UserID]; decided {
				state = map[bool]string{true: "granted", false: "revoked"}[granted]
			}
			people = append(people, firmPerson{m.UserID, user.Email, strings.TrimSpace(user.FirstName + " " + user.LastName), m.Role.Slug, m.Status, state})
		}
		var changes []byte
		if err = db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(c ORDER BY c.id DESC),'[]'::jsonb) FROM (SELECT id,subject,from_role,to_role,reason,actor,created_at FROM member_role_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 50) c`, org).Scan(&changes); err != nil {
			problem(w, 503, "Role history unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"people": people, "roles": roles, "changes": json.RawMessage(changes), "more": len(memberships) > mostPeople})
	})
	mux.HandleFunc("POST /v1/closes/members/role", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		if os.Getenv("WORKOS_API_KEY") == "" {
			problem(w, 503, "Roles need the server's WorkOS API key.")
			return
		}
		var in struct {
			Subject   string `json:"subject"`
			Role      string `json:"role"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Subject, in.Role, in.Reason = strings.TrimSpace(in.Subject), strings.TrimSpace(in.Role), strings.TrimSpace(in.Reason)
		if !in.Confirmed || !workosUserID.MatchString(in.Subject) || in.Role == "" || len(in.Reason) < 10 || len(in.Reason) > 2000 {
			problem(w, 422, "Choose the person and the role, give a reason of 10–2,000 characters and confirm.")
			return
		}
		if in.Subject == subject(r) {
			problem(w, 422, "You cannot change your own role. Ask another administrator.")
			return
		}
		_, allowed, err := firmRoles(r.Context(), r)
		if err != nil {
			problem(w, 502, "WorkOS roles are unavailable.")
			return
		}
		if _, ok := allowed[in.Role]; !ok {
			problem(w, 403, "You can give only a role whose permissions you hold yourself.")
			return
		}
		org := organization(r)
		membership, err := activeMembership(r.Context(), org, in.Subject)
		if err != nil {
			problem(w, 502, "WorkOS could not find the membership. Try again.")
			return
		}
		if membership.ID == "" {
			problem(w, 404, "That person has no active membership in the firm.")
			return
		}
		if membership.Role.Slug == in.Role {
			problem(w, 409, "They already have that role.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Role storage unavailable.")
			return
		}
		defer tx.Rollback()
		var enabled, actorStillMember bool
		if err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&enabled); err != nil || !enabled {
			problem(w, 403, "Organization unavailable.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, org, subject(r)).Scan(&actorStillMember); err != nil || !actorStillMember {
			problem(w, 403, "Your firm membership changed. No role was changed.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO member_role_changes(organization_id,subject,from_role,to_role,reason,actor) VALUES($1,$2,$3,$4,$5,$6)`,
			org, in.Subject, membership.Role.Slug, in.Role, in.Reason, subject(r)); err != nil {
			problem(w, 503, "Role storage unavailable.")
			return
		}
		if err = workos(r.Context(), "PUT", "/user_management/organization_memberships/"+url.PathEscape(membership.ID), map[string]string{"role_slug": in.Role}, nil); err != nil {
			problem(w, 502, "WorkOS did not change the role. Nothing was recorded; try again.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "WorkOS changed the role, but the firm's record of it was not saved. Refresh before changing it again.")
			return
		}
		jsonReply(w, 200, map[string]string{"role": in.Role, "message": "Role changed. It takes effect when their session next renews, within five minutes."})
	})
}

// activeMembership is the person's active WorkOS membership of the firm, or an empty one when they have none.
func activeMembership(ctx context.Context, org, user string) (firmMembership, error) {
	held, err := workosList[firmMembership](ctx, "/user_management/organization_memberships", url.Values{"user_id": {user}, "organization_id": {org}, "statuses": {"active"}, "limit": {"100"}})
	if err != nil || len(held) == 0 {
		return firmMembership{}, err
	}
	return held[0], nil
}
