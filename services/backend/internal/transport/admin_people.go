package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// People in the staff console (docs/admin-panel.md, phase A2): one person across every firm, from the app's records and,
// when the server has a WorkOS key, from WorkOS (their account, organization memberships and sessions). Staff can end
// all of a person's WorkOS sessions and resend an invitation. People's identities are the firms' own teams, so they
// show; the reasons behind decisions do not (the firm's People tab has them, masked until revealed).
func registerAdminPeople(mux *http.ServeMux, db *sql.DB, token string) {
	route := func(permission string, next func(http.ResponseWriter, *http.Request, *sql.DB)) http.HandlerFunc {
		return adminGuard(token, db, permission, func(w http.ResponseWriter, r *http.Request) { next(w, r, db) })
	}
	mux.HandleFunc("GET /v1/admin/people", route(platformRead, adminPeople))
	mux.HandleFunc("POST /v1/admin/people/{user}/sessions/revoke", route(platformOperate, adminRevokeSessions))
	mux.HandleFunc("POST /v1/admin/firms/{id}/invitations/{invitation}/resend", route(platformOperate, adminResendInvitation))
}

// errWorkOS is WorkOS not answering, or refusing, a call an action needed.
var errWorkOS = errors.New("WorkOS did not confirm")

var errInvitationNotPending = errors.New("only a pending invitation that has not expired can be resent")

type workosUser struct {
	ID           string  `json:"id"`
	Email        string  `json:"email"`
	FirstName    string  `json:"first_name"`
	LastName     string  `json:"last_name"`
	LastSignInAt *string `json:"last_sign_in_at"`
	CreatedAt    string  `json:"created_at"`
}

type workosMembership struct {
	OrganizationID string `json:"organization_id"`
	FirmName       string `json:"firm_name"`
	Status         string `json:"status"`
	Role           struct {
		Slug string `json:"slug"`
	} `json:"role"`
}

// workosSession leaves out where a session signed in from (IP address, browser): staff need its state, not that.
type workosSession struct {
	ID             string  `json:"id"`
	OrganizationID string  `json:"organization_id"`
	Status         string  `json:"status"`
	AuthMethod     string  `json:"auth_method"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      string  `json:"expires_at"`
	EndedAt        *string `json:"ended_at"`
}

type adminPerson struct {
	workosUser
	Memberships []workosMembership `json:"memberships"`
	Sessions    []workosSession    `json:"sessions"`
}

// workosRecentSessions lists a user's twenty newest sessions, for staff to see.
func workosRecentSessions(ctx context.Context, user string) ([]workosSession, error) {
	var page struct {
		Data []workosSession `json:"data"`
	}
	err := workos(ctx, "GET", "/user_management/users/"+url.PathEscape(user)+"/sessions?limit=20", nil, &page)
	return page.Data, err
}

// workosPeople finds the WorkOS users a search names, with their memberships and sessions.
func workosPeople(ctx context.Context, user, email string) ([]adminPerson, error) {
	var users []workosUser
	if user != "" {
		var u workosUser
		err := workos(ctx, "GET", "/user_management/users/"+url.PathEscape(user), nil, &u)
		var status statusError
		if errors.As(err, &status) && status.code == 404 {
			return []adminPerson{}, nil
		}
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	} else {
		var err error
		if users, err = workosList[workosUser](ctx, "/user_management/users", url.Values{"email": {email}, "limit": {"10"}}); err != nil {
			return nil, err
		}
	}
	people := []adminPerson{}
	for _, u := range users {
		p := adminPerson{workosUser: u}
		var err error
		if p.Memberships, err = workosList[workosMembership](ctx, "/user_management/organization_memberships", url.Values{"user_id": {u.ID}, "limit": {"100"}}); err != nil {
			return nil, err
		}
		if p.Sessions, err = workosRecentSessions(ctx, u.ID); err != nil {
			return nil, err
		}
		people = append(people, p)
	}
	return people, nil
}

// adminPeople finds one person by WorkOS user ID or email address: their WorkOS account, memberships and sessions, and
// the app's memberships and invitations for them in every firm.
func adminPeople(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	var user, email string
	switch {
	case strings.HasPrefix(q, "user_") && len(q) <= 200:
		user = q
	case strings.Contains(q, "@") && len(q) >= 3 && len(q) <= 254:
		email = strings.ToLower(q)
	default:
		problem(w, 422, "Search by a WorkOS user ID (user_…) or an email address.")
		return
	}
	people, state := []adminPerson{}, "not_configured"
	if os.Getenv("WORKOS_API_KEY") != "" {
		state = "ok"
		found, err := workosPeople(r.Context(), user, email)
		if err != nil {
			state = "unavailable"
		} else {
			people = found
		}
	}
	ids := []string{}
	if user != "" {
		ids = append(ids, user)
	}
	for _, p := range people {
		if p.ID != user {
			ids = append(ids, p.ID)
		}
	}
	// As the firm's own list does: expire and settle the firms' invitations first, so staff act on their real state.
	if err := reconcilePersonInvitations(context.WithValue(r.Context(), systemKey{}, true), db, email, ids); err != nil {
		adminFirmProblem(w, err, "invitation check")
		return
	}
	var memberships, invitations, firms []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		orgs := []string{}
		for _, p := range people {
			for _, m := range p.Memberships {
				orgs = append(orgs, m.OrganizationID)
			}
		}
		// IDs are WorkOS's (user_…, org_…), which never contain a comma.
		return tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',m.subject,'organization_id',m.organization_id,'firm_name',o.name,'firm_enabled',o.enabled,'id',m.id::text,
   'granted',m.granted,'decided_by',m.actor,'decided_at',m.created_at,
   'assignments',(SELECT count(*) FROM current_client_access g WHERE g.organization_id=m.organization_id AND g.subject=m.subject AND g.granted),
   'agent',(SELECT a.state FROM current_agent_access a WHERE a.organization_id=m.organization_id AND a.subject=m.subject)) ORDER BY m.granted DESC,o.name)
  FROM current_organization_members m JOIN organizations o ON o.id=m.organization_id WHERE m.subject=ANY(string_to_array($1,','))),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',i.id::text,'organization_id',i.organization_id,'firm_name',o.name,'email',i.email,'role',i.role_name,
   'state',CASE WHEN i.state='pending' AND i.expires_at<=now() THEN 'expired' ELSE i.state END,'invited_by',i.invited_by,'created_at',i.created_at,'expires_at',i.expires_at) ORDER BY i.id DESC)
  FROM (SELECT * FROM member_invitations WHERE ($2<>'' AND email=$2) OR subject=ANY(string_to_array($1,',')) ORDER BY id DESC LIMIT 50) i JOIN organizations o ON o.id=i.organization_id),'[]'::jsonb),
 COALESCE((SELECT jsonb_object_agg(id,name) FROM organizations WHERE id=ANY(string_to_array($3,','))),'{}'::jsonb)`,
			strings.Join(ids, ","), email, strings.Join(orgs, ",")).Scan(&memberships, &invitations, &firms)
	})
	if err != nil {
		adminFirmProblem(w, err, "people search")
		return
	}
	var names map[string]string
	json.Unmarshal(firms, &names)
	for i := range people {
		for j := range people[i].Memberships {
			m := &people[i].Memberships[j]
			m.FirmName = names[m.OrganizationID]
			if m.OrganizationID == os.Getenv("PLATFORM_ORGANIZATION_ID") {
				m.FirmName = "Platform staff"
			}
		}
	}
	jsonReply(w, 200, map[string]any{"query": q, "workos": state, "users": people, "memberships": json.RawMessage(memberships), "invitations": json.RawMessage(invitations)})
}

// reconcilePersonInvitations runs reconcileInvitations for each firm that invited this person.
func reconcilePersonInvitations(ctx context.Context, db *sql.DB, email string, ids []string) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT organization_id FROM member_invitations WHERE ($1<>'' AND email=$1) OR subject=ANY(string_to_array($2,','))`, email, strings.Join(ids, ","))
	if err != nil {
		return err
	}
	firms := []string{}
	for rows.Next() {
		var firm string
		if err = rows.Scan(&firm); err != nil {
			rows.Close()
			return err
		}
		firms = append(firms, firm)
	}
	if rows.Close(); rows.Err() != nil {
		return rows.Err()
	}
	for _, firm := range firms {
		if err = reconcileInvitations(ctx, db, firm); err != nil {
			return err
		}
	}
	return nil
}

// adminRevokeSessions signs a person out everywhere: the app's own sessions (AUTH_SESSION=server) end with the audited
// action, whatever WorkOS then does, and every active WorkOS session, in every organization, is revoked so none can be
// refreshed. Their memberships are untouched. When WorkOS fails part way, the answer says exactly what ended.
func adminRevokeSessions(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	user := r.PathValue("user")
	if !strings.HasPrefix(user, "user_") || len(user) > 200 {
		problem(w, 422, "Name the person by their WorkOS user ID.")
		return
	}
	if os.Getenv("WORKOS_API_KEY") == "" {
		problem(w, 503, "Ending sessions needs the server's WorkOS API key.")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	request := map[string]any{}
	var app int64
	ended, active, listed := 0, 0, false
	err := adminAct(r.Context(), db, adminAction{Action: "sessions.revoke", TargetType: "user", TargetID: user, Reason: strings.TrimSpace(in.Reason), Request: request},
		func(ctx context.Context, tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE subject=$1 AND revoked_at IS NULL`, user)
			if err != nil {
				return err
			}
			app, _ = result.RowsAffected()
			// Every page of sessions, so "all" means all; a list WorkOS cannot finish is a failure, not a partial success.
			sessions, err := workosList[workosSession](ctx, "/user_management/users/"+url.PathEscape(user)+"/sessions", url.Values{"limit": {"100"}})
			if err == nil {
				listed = true
				for _, s := range sessions {
					if s.Status != "active" {
						continue
					}
					active++
					if err = workos(ctx, "POST", "/user_management/sessions/revoke", map[string]string{"session_id": s.ID}, nil); err != nil {
						break
					}
					ended++
				}
			}
			request["app_sessions"], request["workos_ended"], request["workos_complete"] = app, ended, err == nil
			return nil
		})
	if err != nil {
		adminFirmProblem(w, err, "session revoke")
		return
	}
	if !listed || ended < active {
		what := "WorkOS did not list their sessions"
		if listed {
			what = fmt.Sprintf("WorkOS ended %d of their %d active sessions, then failed", ended, active)
		}
		problem(w, 502, fmt.Sprintf("The app's own sessions for this person ended (%d). %s; end sessions again to finish.", app, what))
		return
	}
	jsonReply(w, 200, map[string]any{"ended": ended, "app_sessions": app})
}

// adminResendInvitation asks WorkOS to send a pending invitation's email again, and keeps the new expiry.
func adminResendInvitation(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, invitation := r.PathValue("id"), r.PathValue("invitation")
	if os.Getenv("WORKOS_API_KEY") == "" {
		problem(w, 503, "Resending invitations needs the server's WorkOS API key.")
		return
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	var expires time.Time
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "invitation.resend", TargetType: "invitation", TargetID: invitation, Reason: strings.TrimSpace(in.Reason)},
		func(ctx context.Context, tx *sql.Tx) error {
			var workosID string
			err := tx.QueryRowContext(ctx, `SELECT workos_id FROM member_invitations WHERE id::text=$1 AND organization_id=$2 AND state='pending' AND workos_id IS NOT NULL AND expires_at>now() FOR UPDATE`, invitation, firm).Scan(&workosID)
			if errors.Is(err, sql.ErrNoRows) {
				return errInvitationNotPending
			}
			if err != nil {
				return err
			}
			var sent workosInvitation
			if err = workos(ctx, "POST", "/user_management/invitations/"+url.PathEscape(workosID)+"/resend", map[string]any{}, &sent); err != nil {
				return fmt.Errorf("%w: the invitation was not resent", errWorkOS)
			}
			if expires = sent.ExpiresAt; expires.IsZero() {
				return nil
			}
			_, err = tx.ExecContext(ctx, `UPDATE member_invitations SET expires_at=$3 WHERE id::text=$1 AND organization_id=$2`, invitation, firm, expires)
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "invitation resend")
		return
	}
	jsonReply(w, 200, map[string]any{"message": "Invitation sent again.", "expires_at": expires})
}
