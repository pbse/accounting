package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// The firm's people and their roles (docs/sign-in.md). People who join by their company address arrive with the member
// role, which opens nothing until the firm's administrator gives them a role here or assigns them clients. WorkOS holds the role; the firm keeps
// the record of who gave which role, and why.

type firmPerson struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	Status  string `json:"status"`
	Access  string `json:"access"`
}

// peoplePage is how many people the list shows at a time, each looked up in WorkOS, at most lookups at once.
const (
	peoplePage = 50
	lookups    = 8
)

var workosCursor = regexp.MustCompile(`^[A-Za-z0-9_]{1,200}$`)

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
		org, after := organization(r), r.URL.Query().Get("after")
		if after != "" && !workosCursor.MatchString(after) {
			problem(w, 422, "That page of people is not available. Reload the list.")
			return
		}
		// One page of WorkOS's memberships at a time; the list asks for the next with its cursor.
		var page struct {
			Data         []firmMembership `json:"data"`
			ListMetadata struct {
				After string `json:"after"`
			} `json:"list_metadata"`
		}
		query := url.Values{"organization_id": {org}, "limit": {strconv.Itoa(peoplePage)}}
		if after != "" {
			query.Set("after", after)
		}
		if err := workos(r.Context(), "GET", "/user_management/organization_memberships?"+query.Encode(), nil, &page); err != nil {
			problem(w, 502, "WorkOS could not list the firm's people. Try again.")
			return
		}
		memberships := page.Data
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
		// Each person's name and address, looked up a few at a time.
		people := make([]firmPerson, len(memberships))
		failed := make([]error, len(memberships))
		slots := make(chan struct{}, lookups)
		var wg sync.WaitGroup
		for i, m := range memberships {
			wg.Add(1)
			go func() {
				defer wg.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				var user struct {
					Email     string `json:"email"`
					FirstName string `json:"first_name"`
					LastName  string `json:"last_name"`
				}
				failed[i] = workos(r.Context(), "GET", "/user_management/users/"+url.PathEscape(m.UserID), nil, &user)
				state := "none"
				if granted, decided := access[m.UserID]; decided {
					state = map[bool]string{true: "granted", false: "revoked"}[granted]
				}
				people[i] = firmPerson{m.UserID, user.Email, strings.TrimSpace(user.FirstName + " " + user.LastName), m.Role.Slug, m.Status, state}
			}()
		}
		wg.Wait()
		if errors.Join(failed...) != nil {
			problem(w, 502, "WorkOS could not list the firm's people. Try again.")
			return
		}
		if err = settleRoleChanges(r.Context(), db, org); err != nil {
			problem(w, 503, "Role history unavailable.")
			return
		}
		var changes []byte
		if err = db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(c ORDER BY c.id DESC),'[]'::jsonb) FROM (SELECT id,subject,from_role,to_role,reason,actor,state,created_at FROM member_role_changes WHERE organization_id=$1 ORDER BY id DESC LIMIT 50) c`, org).Scan(&changes); err != nil {
			problem(w, 503, "Role history unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"people": people, "roles": roles, "changes": json.RawMessage(changes), "next": page.ListMetadata.After})
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
			Subject string `json:"subject"`
			Role    string `json:"role"`
			// Current is the role the administrator saw; a different one now means someone changed it meanwhile.
			Current   string `json:"current"`
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
		// The change is recorded as pending before WorkOS is asked, so a change WorkOS makes is never left unrecorded.
		// Changes for a firm run one at a time under its row lock, and the person's role is read only once it is held.
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Role storage unavailable.")
			return
		}
		defer tx.Rollback()
		var enabled, actorStillMember, pending bool
		var targetAccess sql.NullBool
		if err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&enabled); err != nil || !enabled {
			problem(w, 403, "Organization unavailable.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted), EXISTS(SELECT 1 FROM member_role_changes WHERE organization_id=$1 AND subject=$3 AND state='pending')`, org, subject(r), in.Subject).Scan(&actorStillMember, &pending); err != nil || !actorStillMember {
			problem(w, 403, "Your firm membership changed. No role was changed.")
			return
		}
		if pending {
			problem(w, 409, "A role change for them is still being confirmed. Refresh the list in a moment.")
			return
		}
		// A role is given only to someone with firm access: one for a removed person would come back with them unseen.
		if err = tx.QueryRowContext(r.Context(), `SELECT granted FROM current_organization_members WHERE organization_id=$1 AND subject=$2`, org, in.Subject).Scan(&targetAccess); err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Member access unavailable.")
			return
		}
		if !targetAccess.Valid || !targetAccess.Bool {
			problem(w, 409, "They have no firm access. Enroll them first; then give them a role.")
			return
		}
		membership, err := activeMembership(r.Context(), org, in.Subject)
		if err != nil {
			problem(w, 502, "WorkOS could not find the membership. Try again.")
			return
		}
		if membership.ID == "" {
			problem(w, 404, "That person has no active membership in the firm.")
			return
		}
		if in.Current != "" && membership.Role.Slug != in.Current {
			problem(w, 409, "Their role changed since you loaded the list. Refresh and decide again.")
			return
		}
		if membership.Role.Slug == in.Role {
			problem(w, 409, "They already have that role.")
			return
		}
		var change int64
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO member_role_changes(organization_id,subject,from_role,to_role,reason,actor) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
			org, in.Subject, membership.Role.Slug, in.Role, in.Reason, subject(r)).Scan(&change); err != nil || tx.Commit() != nil {
			problem(w, 503, "Role storage unavailable.")
			return
		}
		settle := func(state string) error {
			_, err := db.ExecContext(r.Context(), `UPDATE member_role_changes SET state=$2,settled_at=now() WHERE id=$1 AND state='pending'`, change, state)
			return err
		}
		err = workos(r.Context(), "PUT", "/user_management/organization_memberships/"+url.PathEscape(membership.ID), map[string]string{"role_slug": in.Role}, nil)
		var refused statusError
		switch {
		case errors.As(err, &refused) && refused.code >= 400 && refused.code < 500:
			settle("failed")
			problem(w, 502, "WorkOS refused the role change; their role is unchanged.")
			return
		case err != nil:
			problem(w, 502, "WorkOS did not confirm the change. Refresh the list in a moment to see whether it took effect.")
			return
		case settle("applied") != nil:
			problem(w, 503, "WorkOS changed the role; the firm's record is confirmed when the list next loads.")
			return
		}
		jsonReply(w, 200, map[string]string{"role": in.Role, "message": "Role changed. It takes effect when their session next renews, within five minutes."})
	})
}

// settleRoleChanges settles changes WorkOS did not confirm from the role it now reports for each person: the new role
// means the change took; the old one, a minute on, means it did not; any other role (changed in WorkOS meanwhile)
// leaves the outcome unknown, which is recorded as such rather than guessed.
func settleRoleChanges(ctx context.Context, db *sql.DB, org string) error {
	rows, err := db.QueryContext(ctx, `SELECT id,subject,from_role,to_role,created_at<now()-interval '1 minute' FROM member_role_changes WHERE organization_id=$1 AND state='pending'`, org)
	if err != nil {
		return err
	}
	type pending struct {
		id                int64
		subject, from, to string
		old               bool
	}
	var open []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.subject, &p.from, &p.to, &p.old); err != nil {
			rows.Close()
			return err
		}
		open = append(open, p)
	}
	if rows.Close(); rows.Err() != nil {
		return rows.Err()
	}
	for _, p := range open {
		held, err := workosList[firmMembership](ctx, "/user_management/organization_memberships", url.Values{"user_id": {p.subject}, "organization_id": {org}, "limit": {"10"}})
		if err != nil {
			return err
		}
		state := ""
		for _, m := range held {
			switch {
			case m.Role.Slug == p.to:
				state = "applied"
			case p.old && m.Role.Slug == p.from:
				state = "failed"
			case p.old:
				state = "unknown"
			}
		}
		if p.old && len(held) == 0 {
			state = "unknown"
		}
		if state != "" {
			if _, err = db.ExecContext(ctx, `UPDATE member_role_changes SET state=$2,settled_at=now() WHERE id=$1 AND state='pending'`, p.id, state); err != nil {
				return err
			}
		}
	}
	return nil
}

// activeMembership is the person's active WorkOS membership of the firm, or an empty one when they have none.
func activeMembership(ctx context.Context, org, user string) (firmMembership, error) {
	held, err := workosList[firmMembership](ctx, "/user_management/organization_memberships", url.Values{"user_id": {user}, "organization_id": {org}, "statuses": {"active"}, "limit": {"100"}})
	if err != nil || len(held) == 0 {
		return firmMembership{}, err
	}
	return held[0], nil
}
