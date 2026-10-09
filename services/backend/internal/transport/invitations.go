package transport

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
)

const workosAPI = "https://api.workos.com"

type workosRole struct {
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Permissions *[]string `json:"permissions"`
}

type workosInvitation struct {
	ID             string    `json:"id"`
	Email          string    `json:"email"`
	State          string    `json:"state"`
	OrganizationID string    `json:"organization_id"`
	AcceptedUserID string    `json:"accepted_user_id"`
	InviterUserID  string    `json:"inviter_user_id"`
	RoleSlug       string    `json:"role_slug"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
}

func workos(ctx context.Context, method, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err := driveCall(ctx, method, workosAPI+path, os.Getenv("WORKOS_API_KEY"), body, out, 1<<20)
	return err
}

func plainPermission(name string) string {
	for _, p := range plainPermissions {
		if p.name == name {
			return p.plain
		}
	}
	return name
}

// firmRoles lists the organization's WorkOS roles; only a role whose listed permissions the actor all holds is allowed.
// firmRoles reads the firm's roles from WorkOS and says which the person asking may give: what decides a role or an
// invitation reads this. shownFirmRoles is the same from a copy at most about 30 seconds behind, for what a page shows.
func firmRoles(ctx context.Context, r *http.Request) ([]map[string]any, map[string]workosRole, error) {
	list, err := readFirmRoles(ctx, organization(r))
	if err != nil {
		return nil, nil, err
	}
	out, allowed := rolesFor(list, auth.Actor(r.Context()).Permissions)
	return out, allowed, nil
}

var rolesShown = newShownCache[[]workosRole](30*time.Second, 2*time.Minute, 10*time.Minute)

func shownFirmRoles(ctx context.Context, r *http.Request) ([]map[string]any, error) {
	org := organization(r)
	list, err := rolesShown.get(ctx, org, func(ctx context.Context) ([]workosRole, error) { return readFirmRoles(ctx, org) })
	if err != nil {
		return nil, err
	}
	out, _ := rolesFor(list, auth.Actor(r.Context()).Permissions)
	return out, nil
}

func readFirmRoles(ctx context.Context, org string) ([]workosRole, error) {
	var list struct {
		Data []workosRole `json:"data"`
	}
	if err := workos(ctx, "GET", "/authorization/organizations/"+url.PathEscape(org)+"/roles", nil, &list); err != nil {
		return nil, err
	}
	return list.Data, nil
}

// rolesFor lists the roles as a page shows them, each allowed only if held holds every permission it grants.
func rolesFor(list []workosRole, held []string) ([]map[string]any, map[string]workosRole) {
	out, allowed := []map[string]any{}, map[string]workosRole{}
	for _, role := range list {
		plain, ok := []string{}, role.Permissions != nil
		if role.Permissions != nil {
			for _, p := range *role.Permissions {
				plain = append(plain, plainPermission(p))
				ok = ok && slices.Contains(held, p)
			}
		}
		if ok {
			allowed[role.Slug] = role
		}
		out = append(out, map[string]any{"slug": role.Slug, "name": role.Name, "description": role.Description, "permissions": plain, "allowed": ok})
	}
	return out, allowed
}

// invitationEnrollment enrolls a signed-in subject the app has never decided on, when WorkOS shows the firm's pending
// invitation to their verified address accepted by them and their organization membership active. Any doubt denies.
func invitationEnrollment(ctx context.Context, db *sql.DB, org, sub string) bool {
	if os.Getenv("WORKOS_API_KEY") == "" || org == "local-development" || !strings.HasPrefix(sub, "user_") {
		return false
	}
	var open bool
	if db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM member_invitations WHERE organization_id=$1 AND state='pending' AND expires_at>now()) AND NOT EXISTS(SELECT 1 FROM organization_member_access WHERE organization_id=$1 AND subject=$2)`, org, sub).Scan(&open) != nil || !open {
		return false
	}
	var user struct {
		Email    string `json:"email"`
		Verified bool   `json:"email_verified"`
	}
	if workos(ctx, "GET", "/user_management/users/"+url.PathEscape(sub), nil, &user) != nil || !user.Verified {
		return false
	}
	email := strings.ToLower(strings.TrimSpace(user.Email))
	var id, workosID, inviter, reason string
	if db.QueryRowContext(ctx, `SELECT id::text,workos_id,invited_by,reason FROM member_invitations WHERE organization_id=$1 AND email=$2 AND state='pending' AND expires_at>now()`, org, email).Scan(&id, &workosID, &inviter, &reason) != nil {
		return false
	}
	var invitation workosInvitation
	if workos(ctx, "GET", "/user_management/invitations/"+url.PathEscape(workosID), nil, &invitation) != nil || invitation.State != "accepted" || invitation.AcceptedUserID != sub || invitation.OrganizationID != org || strings.ToLower(invitation.Email) != email {
		return false
	}
	var memberships struct {
		Data []struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	if workos(ctx, "GET", "/user_management/organization_memberships?"+url.Values{"user_id": {sub}, "organization_id": {org}, "statuses": {"active"}}.Encode(), nil, &memberships) != nil || !slices.ContainsFunc(memberships.Data, func(m struct {
		Status string `json:"status"`
	}) bool {
		return m.Status == "active"
	}) {
		return false
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	defer tx.Rollback()
	// The firm row serializes this with an administrator's member decision, and the invitation row with its revocation;
	// both are checked again once held.
	var still bool
	err = tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, org).Scan(&still)
	if err == nil && still {
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM member_invitations WHERE id::text=$1 AND state='pending' AND expires_at>clock_timestamp() FOR UPDATE) AND NOT EXISTS(SELECT 1 FROM organization_member_access WHERE organization_id=$2 AND subject=$3)`, id, org, sub).Scan(&still)
	}
	if err != nil || !still {
		return false
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,$3,$4)`, org, sub, "Accepted invitation #"+id+" sent by "+inviter+": "+reason, inviter); err != nil {
		return false
	}
	if _, err = tx.ExecContext(ctx, `UPDATE member_invitations SET state='accepted',subject=$2,decided_at=now() WHERE id::text=$1`, id, sub); err != nil {
		return false
	}
	return tx.Commit() == nil
}

// settleInvitations resolves sends WorkOS did not confirm, and sends revoked before they were confirmed, from WorkOS's
// own list. Only an invitation that matches the send's firm, address, role and inviter, created around the send, is
// taken as it; anything absent or ambiguous stays unsettled.
// reconcileInvitations brings the firm's invitations up to date before they are listed: pending ones past their expiry
// expire, and, with a WorkOS key, sends WorkOS completed but the app did not record are settled.
func reconcileInvitations(ctx context.Context, db *sql.DB, org string) error {
	_, err := db.ExecContext(ctx, `UPDATE member_invitations SET state='expired',decided_at=now() WHERE organization_id=$1 AND state='pending' AND expires_at<=now()`, org)
	if err == nil && os.Getenv("WORKOS_API_KEY") != "" {
		err = settleInvitations(ctx, db, org)
	}
	return err
}

func settleInvitations(ctx context.Context, db *sql.DB, org string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, `SELECT id::text,email,role_slug,invited_by,created_at,state FROM member_invitations WHERE organization_id=$1 AND workos_id IS NULL AND state IN ('sending','revoked') AND created_at<now()-interval '30 seconds' AND created_at>now()-interval '30 days' ORDER BY id LIMIT 10`, org)
	if err != nil {
		return err
	}
	type unsettled struct {
		id, email, role, inviter, state string
		at                              time.Time
	}
	open := []unsettled{}
	for rows.Next() {
		var u unsettled
		if err = rows.Scan(&u.id, &u.email, &u.role, &u.inviter, &u.at, &u.state); err != nil {
			break
		}
		open = append(open, u)
	}
	if rows.Close(); err != nil || rows.Err() != nil {
		return errors.Join(err, rows.Err())
	}
	for _, u := range open {
		var found struct {
			Data         []workosInvitation `json:"data"`
			ListMetadata struct {
				After string `json:"after"`
			} `json:"list_metadata"`
		}
		if workos(ctx, "GET", "/user_management/invitations?"+url.Values{"organization_id": {org}, "email": {u.email}, "limit": {"100"}}.Encode(), nil, &found) != nil || found.ListMetadata.After != "" {
			continue
		}
		matches := slices.DeleteFunc(found.Data, func(v workosInvitation) bool {
			return v.OrganizationID != org || strings.ToLower(v.Email) != u.email || v.RoleSlug != u.role || v.InviterUserID != u.inviter || v.CreatedAt.Before(u.at.Add(-time.Minute)) || v.CreatedAt.After(u.at.Add(5*time.Minute)) || (v.State != "pending" && v.State != "accepted")
		})
		if len(matches) != 1 {
			continue
		}
		m := matches[0]
		if u.state == "revoked" {
			if m.State == "pending" && workos(ctx, "POST", "/user_management/invitations/"+url.PathEscape(m.ID)+"/revoke", map[string]any{}, nil) != nil {
				continue
			}
			if _, err = db.ExecContext(ctx, `UPDATE member_invitations SET workos_id=$2 WHERE id::text=$1 AND state='revoked' AND workos_id IS NULL`, u.id, m.ID); err != nil {
				return err
			}
			continue
		}
		if _, err = db.ExecContext(ctx, `UPDATE member_invitations SET state='pending',workos_id=$2,expires_at=$3 WHERE id::text=$1 AND state='sending'`, u.id, m.ID, m.ExpiresAt); err != nil {
			return err
		}
	}
	return nil
}

func registerInvitations(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/members/roles", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		if os.Getenv("WORKOS_API_KEY") == "" {
			jsonReply(w, 200, map[string]any{"configured": false, "roles": []any{}})
			return
		}
		roles, err := shownFirmRoles(r.Context(), r)
		if err != nil {
			problem(w, 502, "WorkOS roles are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"configured": true, "roles": roles})
	})
	mux.HandleFunc("GET /v1/closes/members/invitations", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		org := organization(r)
		err := reconcileInvitations(r.Context(), db, org)
		var raw []byte
		if err == nil {
			err = db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(i) - 'organization_id' - 'workos_id' ORDER BY i.state NOT IN ('sending','pending'),i.id::bigint DESC),'[]'::jsonb) FROM (SELECT member_invitations.id::text AS id,organization_id,email,role_name,workos_id,state,invited_by,reason,subject,expires_at,created_at,decided_at FROM member_invitations WHERE organization_id=$1 ORDER BY member_invitations.id DESC LIMIT 50) i`, org).Scan(&raw)
		}
		if err != nil {
			problem(w, 503, "Invitations are unavailable.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	})
	mux.HandleFunc("POST /v1/closes/members/invitations", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		if os.Getenv("WORKOS_API_KEY") == "" {
			problem(w, 503, "Email invitations need the server's WorkOS API key.")
			return
		}
		var in struct {
			Action    string `json:"action"`
			ID        string `json:"id"`
			Email     string `json:"email"`
			Role      string `json:"role"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		org, me := organization(r), subject(r)
		if !in.Confirmed {
			problem(w, 422, "Confirm the invitation decision.")
			return
		}
		if in.Action == "revoke" {
			workosID, err := closeInvitation(r.Context(), db, org, in.ID)
			if err != nil {
				problem(w, 409, "Only an open invitation can be revoked. Refresh the list.")
				return
			}
			jsonReply(w, 200, map[string]string{"message": revokeInWorkOS(r.Context(), workosID)})
			return
		}
		address, err := mail.ParseAddress(strings.TrimSpace(in.Email))
		in.Reason = strings.TrimSpace(in.Reason)
		if in.Action != "send" || err != nil || len(address.Address) > 254 || len(in.Reason) < 10 || len(in.Reason) > 2000 {
			problem(w, 422, "Give one email address, a role and a reason of 10–2,000 characters.")
			return
		}
		email := strings.ToLower(address.Address)
		if _, company := companyDomain(email); !company {
			problem(w, 422, "Krith accepts company email addresses only. Invite the person at their firm's address, not a personal one such as Gmail.")
			return
		}
		_, allowed, err := firmRoles(r.Context(), r)
		if err != nil {
			problem(w, 502, "WorkOS roles are unavailable.")
			return
		}
		role, ok := allowed[in.Role]
		if !ok {
			problem(w, 403, "You can invite only into a role whose permissions you hold.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Invitations are unavailable.")
			return
		}
		defer tx.Rollback()
		var member bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, org, me).Scan(&member); err != nil || !member {
			problem(w, 403, "Your firm membership changed. Nothing was sent.")
			return
		}
		var id string
		_, err = tx.ExecContext(r.Context(), `UPDATE member_invitations SET state='expired',decided_at=now() WHERE organization_id=$1 AND email=$2 AND state='pending' AND expires_at<=now()`, org, email)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `INSERT INTO member_invitations(organization_id,email,role_slug,role_name,state,invited_by,reason) VALUES($1,$2,$3,$4,'sending',$5,$6) ON CONFLICT DO NOTHING RETURNING id::text`, org, email, role.Slug, role.Name, me, in.Reason).Scan(&id)
		}
		if err == sql.ErrNoRows {
			problem(w, 409, "This address already has an open invitation. Revoke it to send another.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Invitations are unavailable.")
			return
		}
		var sent workosInvitation
		err = workos(r.Context(), "POST", "/user_management/invitations", map[string]string{"email": email, "organization_id": org, "role_slug": role.Slug, "inviter_user_id": me}, &sent)
		var refused statusError
		switch {
		case errors.As(err, &refused) && refused.code >= 400 && refused.code < 500:
			db.ExecContext(r.Context(), `DELETE FROM member_invitations WHERE id::text=$1 AND state='sending'`, id)
			problem(w, 422, "WorkOS refused the invitation. Check the address and whether it already belongs to the firm.")
			return
		case err != nil || sent.ID == "":
			problem(w, 502, "WorkOS did not confirm the invitation. The address stays reserved until the invitation list finds it in WorkOS, or you revoke it.")
			return
		}
		var n int64
		result, err := db.ExecContext(r.Context(), `UPDATE member_invitations SET state='pending',workos_id=$2,expires_at=$3 WHERE id::text=$1 AND state='sending'`, id, sent.ID, sent.ExpiresAt)
		if err == nil {
			n, err = result.RowsAffected()
		}
		if err == nil && n == 0 {
			// Revoked while it was being sent: revoke it in WorkOS, and record it only once WorkOS confirms, so the
			// invitation list retries otherwise.
			message := "The invitation was revoked while it was being sent, and is revoked in WorkOS."
			if workos(r.Context(), "POST", "/user_management/invitations/"+url.PathEscape(sent.ID)+"/revoke", map[string]any{}, nil) != nil {
				message = "The invitation was revoked while it was being sent. WorkOS did not confirm revoking it yet; the invitation list retries."
			} else if _, err = db.ExecContext(r.Context(), `UPDATE member_invitations SET workos_id=$2 WHERE id::text=$1 AND state='revoked' AND workos_id IS NULL`, id, sent.ID); err != nil {
				message = "The invitation was revoked while it was being sent, and is revoked in WorkOS, but its WorkOS ID could not be recorded here."
			}
			problem(w, 409, message)
			return
		}
		if err != nil {
			problem(w, 503, "WorkOS sent the invitation, but it could not be recorded yet. The invitation list settles it from WorkOS.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "message": "Invitation sent to " + email + "."})
	})
}

// closeInvitation revokes an open invitation in the app, returning its WorkOS ID (null while it is still being sent). The
// app's authority goes first, so a failed WorkOS revoke can never leave it ready to enroll; see revokeInWorkOS.
func closeInvitation(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, org, id string) (sql.NullString, error) {
	var workosID sql.NullString
	err := q.QueryRowContext(ctx, `UPDATE member_invitations SET state='revoked',decided_at=now() WHERE id::text=$1 AND organization_id=$2 AND state IN ('sending','pending') RETURNING workos_id`, id, org).Scan(&workosID)
	return workosID, err
}

// revokeInWorkOS revokes an invitation closeInvitation closed, after that change committed, and says what happened. A send
// still unconfirmed is revoked in WorkOS when it completes or is settled.
func revokeInWorkOS(ctx context.Context, workosID sql.NullString) string {
	if !workosID.Valid {
		return "Invitation revoked. It was still being sent; the app revokes it in WorkOS once WorkOS confirms it."
	}
	if os.Getenv("WORKOS_API_KEY") == "" {
		return "The app will not enroll this invitation. The server has no WorkOS key, so revoke it in WorkOS too."
	}
	if workos(ctx, "POST", "/user_management/invitations/"+url.PathEscape(workosID.String)+"/revoke", map[string]any{}, nil) != nil {
		return "The app will not enroll this invitation, but WorkOS did not confirm revoking it. Check the firm's invitations in WorkOS."
	}
	return "Invitation revoked."
}
