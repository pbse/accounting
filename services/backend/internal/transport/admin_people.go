package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
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

// workosUserID is the form of a WorkOS user ID.
var workosUserID = regexp.MustCompile(`^user_[A-Za-z0-9]{1,100}$`)

var (
	errInvitationNotPending = errors.New("only a pending invitation that has not expired can be resent")
	// errResendUnconfirmed is a resend WorkOS may have sent: its answer and its copy of the invitation were both lost.
	errResendUnconfirmed = errors.New("WorkOS did not confirm whether it renewed the invitation, so its email was not sent; the app keeps it open for seven days")
)

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
	case strings.Contains(q, "@") && len(q) >= 3 && len(q) <= 254:
		email = strings.ToLower(q)
	case workosUserID.MatchString(q):
		user = q
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
	// Who the search names: the user ID searched for, or, for an email, WorkOS's current account for it. When WorkOS
	// cannot answer, the IDs the app itself recorded for the address (accepted invitations, its sessions) stand in, and
	// the answer says they may include earlier owners of the address. Invitations not yet accepted are matched by the
	// searched address, or by the user's current WorkOS address.
	ids, emails := []string{}, []string{}
	if user != "" {
		ids = append(ids, user)
	}
	if email != "" {
		emails = append(emails, email)
	}
	for _, p := range people {
		if !slices.Contains(ids, p.ID) {
			ids = append(ids, p.ID)
		}
		if address := strings.ToLower(p.Email); !slices.Contains(emails, address) {
			emails = append(emails, address)
		}
	}
	system := context.WithValue(r.Context(), systemKey{}, true)
	fromRecords := email != "" && state != "ok"
	if fromRecords {
		if err := recordedIDs(system, db, email, &ids); err != nil {
			adminFirmProblem(w, err, "people search")
			return
		}
	}
	// As the firm's own list does: expire and settle the firms' invitations first, so staff act on their real state.
	if err := reconcilePersonInvitations(system, db, emails, ids); err != nil {
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
		// An accepted invitation belongs to whoever accepted it; only one not yet accepted is matched by address.
		return tx.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT jsonb_agg(jsonb_build_object('subject',m.subject,'organization_id',m.organization_id,'firm_name',o.name,'firm_enabled',o.enabled,'id',m.id::text,
   'granted',m.granted,'decided_by',m.actor,'decided_at',m.created_at,
   'assignments',(SELECT count(*) FROM current_client_access g WHERE g.organization_id=m.organization_id AND g.subject=m.subject AND g.granted),
   'agent',(SELECT a.state FROM current_agent_access a WHERE a.organization_id=m.organization_id AND a.subject=m.subject)) ORDER BY m.granted DESC,o.name)
  FROM current_organization_members m JOIN organizations o ON o.id=m.organization_id WHERE m.subject IN (SELECT jsonb_array_elements_text($1::jsonb))),'[]'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',i.id::text,'organization_id',i.organization_id,'firm_name',o.name,'email',i.email,'role',i.role_name,
   'state',CASE WHEN i.state='pending' AND i.expires_at<=now() THEN 'expired' ELSE i.state END,'invited_by',i.invited_by,'created_at',i.created_at,'expires_at',i.expires_at,
   'email_state',(SELECT e.state FROM platform_emails e WHERE e.invitation=i.id ORDER BY e.id DESC LIMIT 1)) ORDER BY i.id DESC)
  FROM (SELECT * FROM member_invitations WHERE subject IN (SELECT jsonb_array_elements_text($1::jsonb)) OR (subject IS NULL AND email IN (SELECT jsonb_array_elements_text($2::jsonb))) ORDER BY id DESC LIMIT 50) i JOIN organizations o ON o.id=i.organization_id),'[]'::jsonb),
 COALESCE((SELECT jsonb_object_agg(id,name) FROM organizations WHERE id IN (SELECT jsonb_array_elements_text($3::jsonb))),'{}'::jsonb)`,
			jsonList(ids), jsonList(emails), jsonList(orgs)).Scan(&memberships, &invitations, &firms)
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
		}
	}
	jsonReply(w, 200, map[string]any{"query": q, "workos": state, "from_app_records": fromRecords, "users": people, "memberships": json.RawMessage(memberships), "invitations": json.RawMessage(invitations)})
}

// jsonList is a list of text for a query to read with jsonb_array_elements_text: email addresses may hold any character.
func jsonList(values []string) string {
	raw, _ := json.Marshal(values)
	return string(raw)
}

// recordedIDs adds the user IDs the app has recorded for an email address: whoever accepted an invitation to it, and
// whoever holds a sign-in with it.
func recordedIDs(ctx context.Context, db *sql.DB, email string, ids *[]string) error {
	rows, err := db.QueryContext(ctx, `SELECT subject FROM member_invitations WHERE email=$1 AND subject IS NOT NULL
 UNION SELECT subject FROM auth_sessions WHERE lower(email)=$1`, email)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return err
		}
		if !slices.Contains(*ids, id) {
			*ids = append(*ids, id)
		}
	}
	return rows.Err()
}

// reconcilePersonInvitations runs reconcileInvitations for each firm that invited this person.
func reconcilePersonInvitations(ctx context.Context, db *sql.DB, emails, ids []string) error {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT organization_id FROM member_invitations WHERE email IN (SELECT jsonb_array_elements_text($1::jsonb)) OR subject IN (SELECT jsonb_array_elements_text($2::jsonb))`, jsonList(emails), jsonList(ids))
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

// adminRevokeSessions signs a person out everywhere, in two audited steps that outlive the request: first the app's own
// sessions (AUTH_SESSION=server) end and commit, so nothing later can undo that; then every active WorkOS session, in
// every organization, is revoked so none can be refreshed (sessions.revoke_workos: recorded as started before WorkOS is
// called, with no transaction held while it is, then ok, or refused with what ended when WorkOS fails part way). An
// access token a browser already holds (AUTH_SESSION=client) is checked locally and keeps working until it expires,
// minutes later. Their memberships are untouched.
func adminRevokeSessions(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	user := r.PathValue("user")
	if !workosUserID.MatchString(user) {
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
	ctx, reason := context.WithoutCancel(r.Context()), strings.TrimSpace(in.Reason)
	var app int64
	request := map[string]any{}
	err := adminAct(ctx, db, adminAction{Action: "sessions.revoke", TargetType: "user", TargetID: user, Reason: reason, Request: request},
		func(ctx context.Context, tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, `UPDATE auth_sessions SET revoked_at=now() WHERE subject=$1 AND revoked_at IS NULL AND expires_at>now()`, user)
			if err == nil {
				app, _ = result.RowsAffected()
				request["app_sessions"] = app
			}
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "session revoke")
		return
	}
	workosStep := adminAction{Action: "sessions.revoke_workos", TargetType: "user", TargetID: user, Reason: reason}
	if err = adminStart(ctx, db, workosStep); err != nil {
		adminFirmProblem(w, err, "session revoke")
		return
	}
	ended, revoked := endWorkOSSessions(ctx, user)
	workosStep.Request = map[string]any{"ended": ended}
	err = adminAct(ctx, db, workosStep, func(context.Context, *sql.Tx) error { return revoked })
	if err != nil {
		problem(w, 502, fmt.Sprintf("The app's own sessions for this person ended (%d). %s; end sessions again to finish.", app, strings.TrimPrefix(err.Error(), errWorkOS.Error()+": ")))
		return
	}
	jsonReply(w, 200, map[string]any{"ended": ended, "app_sessions": app})
}

// endWorkOSSessions revokes every active WorkOS session of user, reading every page and revoking the active ones as
// each arrives, so "all" means all however long the history. A page WorkOS cannot give, or a revoke it refuses, stops
// it with an error; ended counts what ended before that.
func endWorkOSSessions(ctx context.Context, user string) (ended int, err error) {
	failed := func() (int, error) {
		return ended, fmt.Errorf("%w: WorkOS ended %d active session(s), then failed", errWorkOS, ended)
	}
	query := url.Values{"limit": {"100"}}
	for range 10000 { // at most a million sessions: a longer history is refused, not ended in part
		var page struct {
			Data         []workosSession `json:"data"`
			ListMetadata struct {
				After string `json:"after"`
			} `json:"list_metadata"`
		}
		if workos(ctx, "GET", "/user_management/users/"+url.PathEscape(user)+"/sessions?"+query.Encode(), nil, &page) != nil {
			return failed()
		}
		for _, s := range page.Data {
			if s.Status != "active" {
				continue
			}
			if workos(ctx, "POST", "/user_management/sessions/revoke", map[string]string{"session_id": s.ID}, nil) != nil {
				return failed()
			}
			ended++
		}
		if page.ListMetadata.After == "" {
			return ended, nil
		}
		query.Set("after", page.ListMetadata.After)
	}
	return failed()
}

// adminResendInvitation renews a pending invitation in WorkOS, keeps the new expiry, and queues Krith's email for it
// again (docs/sign-in.md#invitations). The attempt is recorded as started, and committed, before WorkOS is asked, so the
// audit shows it even if its outcome cannot be.
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
	sent := false // WorkOS renewed it: from here on, a failure is the app's. Krith's email goes only with a commit, so none went.
	// Outlives the request, so a closed browser cannot leave WorkOS's answer unrecorded.
	ctx := context.WithoutCancel(r.Context())
	action := adminAction{Firm: firm, Action: "invitation.resend", TargetType: "invitation", TargetID: invitation, Reason: strings.TrimSpace(in.Reason)}
	if err := adminStart(ctx, db, action); err != nil {
		adminFirmProblem(w, err, "invitation resend")
		return
	}
	err := adminAct(ctx, db, action,
		func(ctx context.Context, tx *sql.Tx) error {
			var workosID, email, invitedBy string
			err := tx.QueryRowContext(ctx, `SELECT workos_id,email,invited_by FROM member_invitations WHERE id::text=$1 AND organization_id=$2 AND state='pending' AND workos_id IS NOT NULL AND expires_at>now() FOR UPDATE`, invitation, firm).Scan(&workosID, &email, &invitedBy)
			if errors.Is(err, sql.ErrNoRows) {
				return errInvitationNotPending
			}
			if err != nil {
				return err
			}
			// WorkOS's own copy, just before this attempt: what a lost answer is judged against.
			var before, current workosInvitation
			path := "/user_management/invitations/" + url.PathEscape(workosID)
			if workos(ctx, "GET", path, nil, &before) != nil {
				return fmt.Errorf("%w: WorkOS did not answer", errWorkOS)
			}
			if before.State != "pending" {
				return errInvitationNotPending
			}
			if err = workos(ctx, "POST", path+"/resend", map[string]any{}, &current); err != nil {
				// A refusal (4xx) sent nothing. Any other failure may have lost the answer after sending: only an expiry in
				// WorkOS's copy later than just before this attempt proves it went; an unreadable or unchanged copy (WorkOS
				// may not show it yet) is unknown, not "unsent".
				var status statusError
				if errors.As(err, &status) && status.code >= 400 && status.code < 500 {
					return fmt.Errorf("%w: WorkOS refused to resend it", errWorkOS)
				}
				if workos(ctx, "GET", path, nil, &current) != nil || !current.ExpiresAt.After(before.ExpiresAt) {
					return errResendUnconfirmed
				}
			}
			sent = true
			// The renewal's answer may leave out what accepts the invitation; the copy just before it has it.
			if current.Token == "" {
				current.Token = before.Token
			}
			if current.AcceptURL == "" {
				current.AcceptURL = before.AcceptURL
			}
			// WorkOS sends no invitation email of its own (docs/sign-in.md#invitations): Krith's goes with the renewal.
			if err = queueInvitationEmail(ctx, tx, firm, invitation, email, invitationLink(current), inviterName(ctx, invitedBy)); err != nil {
				return fmt.Errorf("WorkOS renewed it, but its email was not queued: %w", err)
			}
			if expires = current.ExpiresAt; expires.IsZero() {
				return nil
			}
			if _, err = tx.ExecContext(ctx, `UPDATE member_invitations SET expires_at=$3 WHERE id::text=$1 AND organization_id=$2`, invitation, firm, expires); err != nil {
				return fmt.Errorf("WorkOS renewed it, but its new expiry was not saved: %w", err)
			}
			return nil
		})
	if err != nil && sent {
		log.Printf("Invitation %s was renewed but the app could not record it: %v", invitation, err)
		problem(w, 503, "WorkOS renewed the invitation, but the app could not record it, so its email was not sent. Resend it again.")
		return
	}
	if errors.Is(err, errResendUnconfirmed) {
		// Audited as refused, with why. Keep the invitation open at least as long as a resend would (WorkOS's seven
		// days), so its old date cannot expire an invitation WorkOS renewed; WorkOS's own state still decides acceptance.
		if _, hold := db.ExecContext(context.WithValue(ctx, systemKey{}, true), `UPDATE member_invitations SET expires_at=GREATEST(expires_at,now()+interval '7 days') WHERE id::text=$1 AND organization_id=$2 AND state='pending'`, invitation, firm); hold != nil {
			log.Printf("Invitation %s could not be kept open after an unconfirmed resend: %v", invitation, hold)
		}
		problem(w, 502, "WorkOS did not confirm renewing the invitation, so its email was not sent. Resend it again.")
		return
	}
	if err != nil {
		adminFirmProblem(w, err, "invitation resend")
		return
	}
	jsonReply(w, 200, map[string]any{"message": "Invitation sent again.", "expires_at": expires})
}
