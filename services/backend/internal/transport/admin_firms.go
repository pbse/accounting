package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
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
	mux.HandleFunc("POST /v1/admin/firms/{id}/members/{subject}/revoke", route(platformOperate, func(w http.ResponseWriter, r *http.Request, db *sql.DB) { adminMemberAccess(w, r, db, false) }))
	mux.HandleFunc("POST /v1/admin/firms/{id}/members/{subject}/restore", route(platformOperate, func(w http.ResponseWriter, r *http.Request, db *sql.DB) { adminMemberAccess(w, r, db, true) }))
	mux.HandleFunc("POST /v1/admin/firms/{id}/limits", route(platformOperate, adminFirmLimits))
	mux.HandleFunc("POST /v1/admin/firms/{id}/file-imports", route(platformOperate, adminFileImports))
	mux.HandleFunc("POST /v1/admin/firms/{id}/connector-pilot", route(platformOperate, adminConnectorPilot))
	mux.HandleFunc("POST /v1/admin/firms/{id}/support-owner", route(platformOperate, adminSupportOwner))
	mux.HandleFunc("POST /v1/admin/firms/{id}/assistance", route(platformOperate, adminAssistance))
	mux.HandleFunc("POST /v1/admin/firms/{id}/invitations/{invitation}/revoke", route(platformOperate, adminRevokeInvitation))
	mux.HandleFunc("POST /v1/admin/firms/{id}/emails/{outbox}/settle", route(platformOperate, adminSettleEmail))
}

var (
	errInvitationClosed = errors.New("only an open invitation can be revoked")
	errNotAMember       = errors.New("this person is not a member of the firm")
	errLimitsChanged    = errors.New("the firm's limits changed")
	errCloseNotInFirm   = errors.New("no such close in the firm")
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
	case errors.Is(err, errInvitationNotPending):
		problem(w, 409, "Only a pending invitation that has not expired can be resent. Send a new one instead.")
	case errors.Is(err, errWorkOS):
		problem(w, 502, err.Error()+". Nothing changed; try again.")
	case errors.Is(err, errNotAMember):
		problem(w, 409, "This person is not a member of the firm. Refresh the list.")
	case errors.Is(err, errCloseNotInFirm):
		problem(w, 404, "No close with that ID belongs to this firm.")
	case errors.Is(err, errLimitsChanged):
		problem(w, 409, "The firm's limits changed since you loaded them. Refresh and decide again.")
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
 'created_at',created_at,'expires_at',expires_at,'decided_at',decided_at) ORDER BY i.id DESC),'[]'::jsonb)
 FROM (SELECT * FROM member_invitations WHERE organization_id=$1 ORDER BY member_invitations.id DESC LIMIT 50) i`

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
 UNION ALL SELECT disabled_at,COALESCE(disabled_by,''),'firm.suspended','Suspended: '||COALESCE(disabled_reason,''),true FROM organizations WHERE id=$1 AND disabled_at IS NOT NULL
) e WHERE at IS NOT NULL AND ($2='' OR starts_with(kind,$2)) ORDER BY e.at DESC,e.kind,e.text LIMIT $3 OFFSET $4`

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

// adminFirmAudit is what staff did to the firm (readAudit), newest first.
func adminFirmAudit(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "audit read")
		return
	}
	if q, ok := auditFilters(w, r); ok {
		q.firm = firm
		adminAuditPage(w, r, db, q)
	}
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
// adminMemberAccess revokes a current member or restores a revoked one; it never creates a membership for anyone else.
// Restoring is how a firm whose administrators were all revoked gets one back, after staff verify the person in WorkOS.
func adminMemberAccess(w http.ResponseWriter, r *http.Request, db *sql.DB, granted bool) {
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
	action := map[bool]string{false: "member.revoke", true: "member.restore"}[granted]
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: action, TargetType: "member", TargetID: member, Reason: strings.TrimSpace(in.Reason), Request: map[string]string{"previous_id": in.PreviousID}},
		func(ctx context.Context, tx *sql.Tx) error {
			// The firm's row lock serializes this with the firm's own member changes.
			var enabled, now bool
			if err := tx.QueryRowContext(ctx, `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, firm).Scan(&enabled); err != nil {
				return err
			}
			err := tx.QueryRowContext(ctx, `SELECT granted FROM current_organization_members WHERE organization_id=$1 AND subject=$2`, firm, member).Scan(&now)
			switch {
			case errors.Is(err, sql.ErrNoRows) || (err == nil && !now && !granted):
				return errNotAMember
			case err != nil:
				return err
			case now && granted:
				return errAlreadyThatWay
			}
			_, err = decideMemberAccess(ctx, tx, firm, member, granted, in.PreviousID, strings.TrimSpace(in.Reason), by)
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, map[bool]string{false: "member revoke", true: "member restore"}[granted])
		return
	}
	jsonReply(w, 200, map[string]bool{map[bool]string{false: "revoked", true: "restored"}[granted]: true})
}

// adminSupportOwner names the person on our team who supports the firm; the firm sees the name in its settings.
func adminSupportOwner(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	var in struct {
		Owner  string `json:"owner"`
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if in.Owner = strings.TrimSpace(in.Owner); utf8.RuneCountInString(in.Owner) > 200 {
		problem(w, 422, "Name the support owner in up to 200 characters.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "firm.support_owner", TargetType: "firm", TargetID: firm, Reason: strings.TrimSpace(in.Reason), Request: map[string]string{"owner": in.Owner}},
		func(ctx context.Context, tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, `UPDATE organizations SET support_owner=$2 WHERE id=$1 AND support_owner<>$2`, firm, in.Owner)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return errAlreadyThatWay
			}
			return nil
		})
	if err != nil {
		adminFirmProblem(w, err, "support owner change")
		return
	}
	jsonReply(w, 200, map[string]string{"owner": in.Owner})
}

// adminAssistance records our team's setup or service time for a firm: on one of its closes, or on the firm itself
// before or beside any close. It is reported apart from the firm's own time.
func adminAssistance(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	var in struct {
		CloseID string `json:"close_id"`
		Minutes int    `json:"minutes"`
		Note    string `json:"note"`
	}
	if !adminReason(w, r, &in, func() string { return in.Note }) {
		return
	}
	in.CloseID, in.Note = strings.TrimSpace(in.CloseID), strings.TrimSpace(in.Note)
	if id, err := strconv.ParseInt(in.CloseID, 10, 64); (in.CloseID != "" && (err != nil || id < 1)) || in.Minutes < 1 || in.Minutes > 480 || utf8.RuneCountInString(in.Note) < 10 || utf8.RuneCountInString(in.Note) > 500 {
		problem(w, 422, "Record 1–480 minutes with a note of 10–500 characters, on the firm or on one of its closes by ID.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	who := "staff:" + staffOf(r.Context()).Subject
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "effort.assistance", TargetType: "firm", TargetID: firm, Reason: in.Note, Request: map[string]any{"close_id": in.CloseID, "minutes": in.Minutes}},
		func(ctx context.Context, tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, `INSERT INTO close_effort_entries(close_id,organization_id,actor,activity,minutes,note)
 SELECT NULLIF($2,'')::bigint,$1,$3,'assistance',$4,$5 WHERE $2='' OR EXISTS(SELECT 1 FROM client_closes c WHERE c.id::text=$2 AND c.organization_id=$1)`, firm, in.CloseID, who, in.Minutes, in.Note)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return errCloseNotInFirm
			}
			return nil
		})
	if err != nil {
		adminFirmProblem(w, err, "time record")
		return
	}
	jsonReply(w, 200, map[string]bool{"recorded": true})
}

// adminFirmLimits sets a firm's capacity: closes, active preparation jobs and model calls per UTC day, within the
// database's bounds, from the values staff saw.
func adminFirmLimits(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	type limits struct {
		MaxCloses           int `json:"max_closes"`
		MaxPendingJobs      int `json:"max_pending_jobs"`
		MaxModelCallsPerDay int `json:"max_model_calls_per_day"`
	}
	var in struct {
		Before limits `json:"before"`
		After  limits `json:"after"`
		Reason string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if a := in.After; a.MaxCloses < 1 || a.MaxCloses > 10000 || a.MaxPendingJobs < 1 || a.MaxPendingJobs > 10000 || a.MaxModelCallsPerDay < 1 || a.MaxModelCallsPerDay > 100000 {
		problem(w, 422, "Closes and active preparation jobs are 1–10,000 each; model calls are 1–100,000 a day.")
		return
	}
	if in.After == in.Before {
		problem(w, 409, "These are the firm's limits already. Nothing changed.")
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "firm.limits", TargetType: "firm", TargetID: firm, Reason: strings.TrimSpace(in.Reason), Request: map[string]limits{"before": in.Before, "after": in.After}},
		func(ctx context.Context, tx *sql.Tx) error {
			b, a := in.Before, in.After
			result, err := tx.ExecContext(ctx, `UPDATE organizations SET max_closes=$5,max_pending_jobs=$6,max_model_calls_per_day=$7 WHERE id=$1 AND max_closes=$2 AND max_pending_jobs=$3 AND max_model_calls_per_day=$4`,
				firm, b.MaxCloses, b.MaxPendingJobs, b.MaxModelCallsPerDay, a.MaxCloses, a.MaxPendingJobs, a.MaxModelCallsPerDay)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n == 0 {
				return errLimitsChanged
			}
			return nil
		})
	if err != nil {
		adminFirmProblem(w, err, "limit change")
		return
	}
	jsonReply(w, 200, map[string]bool{"changed": true})
}

// adminRevokeInvitation revokes an open invitation in the app, then in WorkOS once that committed (best effort: the answer
// says when WorkOS did not confirm, or the server has no key to ask).
func adminRevokeInvitation(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm, invitation := r.PathValue("id"), r.PathValue("invitation")
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

// adminFileImports switches file imports on or off for one firm, with a reason (docs/books-sources.md, Delivery: off for
// every firm until internal validation; the demonstration firm and internal firms first).
func adminFileImports(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	firm := r.PathValue("id")
	var in struct {
		Enabled bool   `json:"enabled"`
		Reason  string `json:"reason"`
	}
	if !adminReason(w, r, &in, func() string { return in.Reason }) {
		return
	}
	if err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error { return adminFirmExists(ctx, tx, firm) }); err != nil {
		adminFirmProblem(w, err, "firm read")
		return
	}
	err := adminAct(r.Context(), db, adminAction{Firm: firm, Action: "firm.file_imports", TargetType: "firm", TargetID: firm, Reason: strings.TrimSpace(in.Reason), Request: map[string]bool{"enabled": in.Enabled}},
		func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE organizations SET file_imports=$2 WHERE id=$1`, firm, in.Enabled)
			return err
		})
	if err != nil {
		adminFirmProblem(w, err, "file imports")
		return
	}
	jsonReply(w, 200, map[string]bool{"file_imports": in.Enabled})
}
