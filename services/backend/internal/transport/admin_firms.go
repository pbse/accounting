package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
)

// Firm management in the staff console (docs/admin-panel.md, phase A2): a firm's people, activity and staff audit, and
// the actions staff take on a firm. Actions run as the firm through adminAct and share their domain functions with the
// firm's own screens. Client text (reasons, client names, evidence names) is masked unless revealed, as on the run page;
// people's identities (user IDs, invited email addresses) are the firm's own team and show.
func registerAdminFirms(mux *http.ServeMux, db *sql.DB, token string) {
	route := func(permission string, next func(http.ResponseWriter, *http.Request, *sql.DB)) http.HandlerFunc {
		return adminGuard(token, db, permission, func(w http.ResponseWriter, r *http.Request) { next(w, r, db) })
	}
	mux.HandleFunc("GET /v1/admin/firms/{id}/people", route(platformRead, adminFirmPeople))
	mux.HandleFunc("GET /v1/admin/firms/{id}/activity", route(platformRead, adminFirmActivity))
	mux.HandleFunc("GET /v1/admin/firms/{id}/audit", route(platformRead, adminFirmAudit))
	mux.HandleFunc("POST /v1/admin/firms/{id}/suspend", route(platformOperate, func(w http.ResponseWriter, r *http.Request, db *sql.DB) { adminFirmEnabled(w, r, db, false) }))
	mux.HandleFunc("POST /v1/admin/firms/{id}/resume", route(platformOperate, func(w http.ResponseWriter, r *http.Request, db *sql.DB) { adminFirmEnabled(w, r, db, true) }))
	mux.HandleFunc("POST /v1/admin/firms/{id}/members/{subject}/revoke", route(platformOperate, adminRevokeMember))
	mux.HandleFunc("POST /v1/admin/firms/{id}/invitations/{invitation}/revoke", route(platformOperate, adminRevokeInvitation))
}

var (
	errInvitationClosed = errors.New("only an open invitation can be revoked")
	errNotAMember       = errors.New("this person is not a member of the firm")
)

// adminFirmProblem answers a failed firm read or action.
func adminFirmProblem(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, errReasonRequired):
		problem(w, 422, err.Error())
	case errors.Is(err, sql.ErrNoRows):
		problem(w, 404, "Firm not found.")
	case errors.Is(err, errAlreadyThatWay):
		problem(w, 409, "The firm is already that way. Nothing changed.")
	case errors.Is(err, errMemberChanged):
		problem(w, 409, "This person's access changed since you loaded it. Refresh and decide again.")
	case errors.Is(err, errInvitationClosed):
		problem(w, 409, "Only an open invitation can be revoked. Refresh the list.")
	case errors.Is(err, errNotAMember):
		problem(w, 409, "This person is not a member of the firm. Refresh the list.")
	default:
		log.Printf("Admin %s failed: %v", what, err)
		problem(w, 503, "The "+what+" could not be completed. Refresh and try again.")
	}
}

// adminFirmExists is sql.ErrNoRows for an unknown firm, and for the local development firm, which is not a firm.
func adminFirmExists(ctx context.Context, tx *sql.Tx, firm string) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE id=$1 AND id<>'local-development'`, firm).Scan(&id)
}

// adminInvitations is the firm's fifty newest invitations, open or decided.
const adminInvitations = `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'email',email,'role',role_name,'state',state,'invited_by',invited_by,
 'created_at',created_at,'expires_at',expires_at,'decided_at',decided_at) ORDER BY id DESC),'[]'::jsonb)
 FROM (SELECT * FROM member_invitations WHERE organization_id=$1 ORDER BY id DESC LIMIT 50) i`

// adminFirmPeople is the firm's members (the Security center's view of them) and invitations. Reasons and the clients a
// member is assigned to are masked unless revealed; the number of assignments shows.
func adminFirmPeople(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, reveal := r.PathValue("id"), r.URL.Query().Get("reveal") == "1"
	// As the firm's own list does: expire and settle invitations first, so staff act on their real state.
	if err := reconcileInvitations(context.WithValue(r.Context(), systemKey{}, true), db, firm); err != nil {
		adminFirmProblem(w, err, "invitation check")
		return
	}
	var people, invitations []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		if err := adminFirmExists(ctx, tx, firm); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, securityPeople, firm).Scan(&people); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, adminInvitations, firm).Scan(&invitations)
	})
	if err != nil {
		adminFirmProblem(w, err, "people read")
		return
	}
	if reveal && !adminReveal(w, r, db, firm, "firm_people", firm) {
		return
	}
	if !reveal {
		people = only(people, "id", "subject", "decided_by", "decided_at", "state", "scope", "agent_scope_in_force")
	}
	jsonReply(w, 200, map[string]any{"people": json.RawMessage(people), "invitations": json.RawMessage(invitations), "revealed": reveal})
}

// adminActivity is the firm's activity newest first: its recorded decisions and changes (activityEvents, client text),
// its workflow runs and its last suspension. $2 filters by kind prefix; $3 and $4 page.
const adminActivity = `SELECT at::text,actor,kind,text,content FROM (
 SELECT at,actor,kind,text,true AS content FROM (` + activityEvents + `
 ) s
 UNION ALL SELECT created_at,started_by,'workflow.started','Started '||workflow||' (run '||id||')',false FROM workflow_runs WHERE organization_id=$1
 UNION ALL SELECT finished_at,'','workflow.'||state,workflow||' (run '||id||') '||state,false FROM workflow_runs WHERE organization_id=$1 AND finished_at IS NOT NULL
 UNION ALL SELECT disabled_at,disabled_by,'firm.suspended','Suspended: '||disabled_reason,true FROM organizations WHERE id=$1 AND disabled_at IS NOT NULL
) e WHERE at IS NOT NULL AND ($2='' OR starts_with(kind,$2)) ORDER BY at DESC,kind,text LIMIT $3 OFFSET $4`

type adminActivityRow struct {
	At     string `json:"at"`
	Actor  string `json:"actor"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Masked bool   `json:"masked"`
}

func adminFirmActivity(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, reveal := r.PathValue("id"), r.URL.Query().Get("reveal") == "1"
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if len(kind) > 60 {
		problem(w, 422, "The kind filter is a kind or its start, such as member or workflow.")
		return
	}
	offset, ok := adminOffset(w, r)
	if !ok {
		return
	}
	events := []adminActivityRow{}
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		if err := adminFirmExists(ctx, tx, firm); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, adminActivity, firm, kind, adminPage+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e adminActivityRow
			var content bool
			if err = rows.Scan(&e.At, &e.Actor, &e.Kind, &e.Text, &content); err != nil {
				return err
			}
			if content && !reveal {
				e.Text, e.Masked = maskText(e.Text), true
			}
			events = append(events, e)
		}
		return rows.Err()
	})
	if err != nil {
		adminFirmProblem(w, err, "activity read")
		return
	}
	if reveal && !adminReveal(w, r, db, firm, "firm_activity", firm) {
		return
	}
	more := len(events) > adminPage
	if more {
		events = events[:adminPage]
	}
	jsonReply(w, 200, map[string]any{"events": events, "has_more": more, "next_offset": offset + len(events), "revealed": reveal})
}

type adminAuditRow struct {
	ID         int64           `json:"id"`
	StaffUser  string          `json:"staff_user"`
	Action     string          `json:"action"`
	TargetType string          `json:"target_type"`
	TargetID   string          `json:"target_id"`
	Reason     string          `json:"reason"`
	Request    json.RawMessage `json:"request"`
	Result     string          `json:"result"`
	Error      string          `json:"error"`
	CreatedAt  string          `json:"created_at"`
}

// adminFirmAudit is what staff did to the firm, newest first: who, which action, on what, and whether it went through.
// Reasons, the request's details and a refusal's error are free text that can name a client, so they are masked until
// revealed.
func adminFirmAudit(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, reveal := r.PathValue("id"), r.URL.Query().Get("reveal") == "1"
	offset, ok := adminOffset(w, r)
	if !ok {
		return
	}
	entries := []adminAuditRow{}
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		if err := adminFirmExists(ctx, tx, firm); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT id,staff_user,action,target_type,target_id,reason,request,result,error,created_at::text
 FROM platform_audit WHERE organization_id=$1 ORDER BY id DESC LIMIT $2 OFFSET $3`, firm, adminPage+1, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e adminAuditRow
			if err = rows.Scan(&e.ID, &e.StaffUser, &e.Action, &e.TargetType, &e.TargetID, &e.Reason, &e.Request, &e.Result, &e.Error, &e.CreatedAt); err != nil {
				return err
			}
			if !reveal {
				e.Reason, e.Request, e.Error = maskText(e.Reason), only(e.Request), maskText(e.Error)
			}
			entries = append(entries, e)
		}
		return rows.Err()
	})
	if err != nil {
		adminFirmProblem(w, err, "audit read")
		return
	}
	if reveal && !adminReveal(w, r, db, firm, "firm_audit", firm) {
		return
	}
	more := len(entries) > adminPage
	if more {
		entries = entries[:adminPage]
	}
	jsonReply(w, 200, map[string]any{"entries": entries, "has_more": more, "next_offset": offset + len(entries), "revealed": reveal})
}

// adminReason reads a staff action's body into in and checks the reason every firm-level change needs: 10–2,000
// characters, as the firm's own screens ask. It answers 422 itself and returns false when the body does not fit.
func adminReason(w http.ResponseWriter, r *http.Request, in any, reason func() string) bool {
	if !decode(w, r, in) {
		return false
	}
	if n := len(strings.TrimSpace(reason())); n < 10 || n > 2000 {
		problem(w, 422, "Give a reason of 10–2,000 characters.")
		return false
	}
	return true
}

// adminFirmEnabled suspends or resumes a firm. The staff member types the firm's ID to confirm.
func adminFirmEnabled(w http.ResponseWriter, r *http.Request, db *sql.DB, enabled bool) {
	firm := r.PathValue("id")
	var in struct {
		Confirm string `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if in.Confirm != firm {
		problem(w, 422, "Type the firm's ID to confirm.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	action := map[bool]string{false: "firm.suspend", true: "firm.resume"}[enabled]
	by := "staff:" + staffOf(r.Context()).Subject
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: action, TargetType: "firm", TargetID: firm, Reason: strings.TrimSpace(in.Reason)},
		func(ctx context.Context, tx *sql.Tx) error {
			return setOrganizationEnabled(ctx, tx, firm, enabled, by, strings.TrimSpace(in.Reason))
		})
	if err != nil {
		adminFirmProblem(w, err, action)
		return
	}
	jsonReply(w, 200, map[string]bool{"enabled": enabled})
}

// adminRevokeMember ends a person's membership in the firm's app, as a firm administrator would: previous_id is the
// decision the staff member saw, so a change made meanwhile is not overwritten. Their WorkOS membership is untouched.
func adminRevokeMember(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, member := r.PathValue("id"), r.PathValue("subject")
	var in struct {
		PreviousID string `json:"previous_id"`
		Reason     string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if !strings.HasPrefix(member, "user_") || len(member) > 200 {
		problem(w, 422, "Name the member by their WorkOS user ID.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	by := "staff:" + staffOf(r.Context()).Subject
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "member.revoke", TargetType: "member", TargetID: member, Reason: strings.TrimSpace(in.Reason), Request: map[string]string{"previous_id": in.PreviousID}},
		func(ctx context.Context, tx *sql.Tx) error {
			// The firm's row lock serializes this with the firm's own member changes.
			var enabled, granted bool
			if err := tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, firm).Scan(&enabled); err != nil {
				return err
			}
			// Only a current member can be revoked; this route never creates a membership record for anyone else.
			err := tx.QueryRowContext(ctx, `SELECT granted FROM current_organization_members WHERE organization_id=$1 AND subject=$2`, firm, member).Scan(&granted)
			if errors.Is(err, sql.ErrNoRows) || (err == nil && !granted) {
				return errNotAMember
			}
			if err != nil {
				return err
			}
			_, err = decideMemberAccess(ctx, tx, firm, member, false, in.PreviousID, strings.TrimSpace(in.Reason), by)
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "member revoke")
		return
	}
	jsonReply(w, 200, map[string]bool{"revoked": true})
}

// adminRevokeInvitation revokes an open invitation in the app, then in WorkOS once that committed.
func adminRevokeInvitation(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, invitation := r.PathValue("id"), r.PathValue("invitation")
	if os.Getenv("WORKOS_API_KEY") == "" {
		problem(w, 503, "Revoking invitations needs the server's WorkOS API key.")
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
	var workosID sql.NullString
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "invitation.revoke", TargetType: "invitation", TargetID: invitation, Reason: strings.TrimSpace(in.Reason)},
		func(ctx context.Context, tx *sql.Tx) (err error) {
			if workosID, err = closeInvitation(ctx, tx, firm, invitation); errors.Is(err, sql.ErrNoRows) {
				return errInvitationClosed
			}
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "invitation revoke")
		return
	}
	jsonReply(w, 200, map[string]string{"message": revokeInWorkOS(r.Context(), workosID)})
}
