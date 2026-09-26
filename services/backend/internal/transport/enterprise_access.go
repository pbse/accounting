package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
)

func canManageMembers(r *http.Request) bool {
	a := auth.Actor(r.Context())
	return a != nil && a.Organization != "" && slices.Contains(a.Permissions, "clients:all") && slices.Contains(a.Permissions, "members:manage")
}

// A WorkOS organization administrator explicitly enrolls the first member.
// Subsequent members are added through the same audited access history.
func registerEnrollment(mux *http.ServeMux, db *sql.DB) {
	verifier := workspaceVerifier()
	mux.HandleFunc("POST /v1/organizations/enroll", func(w http.ResponseWriter, r *http.Request) {
		if db == nil || verifier.ClientID == "" {
			problem(w, 503, "Hosted enrollment is unavailable.")
			return
		}
		actor, err := verifier.Authenticate(r, "")
		if err != nil || !slices.Contains(actor.Permissions, "org:enroll") || !slices.Contains(actor.Permissions, "members:manage") || !slices.Contains(actor.Permissions, "clients:all") {
			problem(w, 403, "A WorkOS organization enrollment grant is required.")
			return
		}
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		var in struct {
			Name      string `json:"name"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Name, in.Reason = strings.TrimSpace(in.Name), strings.TrimSpace(in.Reason)
		if !in.Confirmed || len(in.Name) < 1 || len(in.Name) > 200 || len(in.Reason) < 10 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm the WorkOS organization, firm name and enrollment authority.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Enrollment storage unavailable.")
			return
		}
		defer tx.Rollback()
		var enabled bool
		err = tx.QueryRowContext(r.Context(), `SELECT enabled FROM organizations WHERE id=$1 FOR UPDATE`, actor.Organization).Scan(&enabled)
		if err == sql.ErrNoRows {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO organizations(id,name) VALUES($1,$2)`, actor.Organization, in.Name)
			enabled = true
		} else if err == nil {
			var count int
			err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM organization_member_access WHERE organization_id=$1`, actor.Organization).Scan(&count)
			if count != 0 {
				problem(w, 409, "This firm is already enrolled. Ask an active firm administrator to manage members.")
				return
			}
		}
		if err != nil || !enabled {
			problem(w, 403, "This WorkOS organization cannot be enrolled.")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,$3,$2)`, actor.Organization, actor.Subject, in.Reason)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Enrollment could not be confirmed. Check firm access before retrying.")
			return
		}
		jsonReply(w, 201, map[string]string{"organization_id": actor.Organization, "subject": actor.Subject})
	})
}

func registerMembers(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/members", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		var raw []byte
		if err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(to_jsonb(m) ORDER BY id DESC),'[]'::jsonb) FROM organization_member_access m WHERE organization_id=$1`, organization(r)).Scan(&raw); err != nil {
			problem(w, 503, "Member history unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(raw))
	})
	mux.HandleFunc("POST /v1/closes/members", func(w http.ResponseWriter, r *http.Request) {
		if !canManageMembers(r) {
			problem(w, 403, "Firm member administration requires clients:all and members:manage.")
			return
		}
		var in struct {
			Subject    string `json:"subject"`
			Granted    bool   `json:"granted"`
			PreviousID string `json:"previous_id"`
			Reason     string `json:"reason"`
			Confirmed  bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Subject, in.Reason = strings.TrimSpace(in.Subject), strings.TrimSpace(in.Reason)
		if !in.Confirmed || !strings.HasPrefix(in.Subject, "user_") || len(in.Subject) > 200 || len(in.Reason) < 10 || len(in.Reason) > 2000 || (!in.Granted && in.Subject == subject(r)) {
			problem(w, 422, "Confirm the exact WorkOS user ID and reason. You cannot revoke your own membership.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Member storage unavailable.")
			return
		}
		defer tx.Rollback()
		var org string
		if err = tx.QueryRowContext(r.Context(), `SELECT id FROM organizations WHERE id=$1 AND enabled FOR UPDATE`, organization(r)).Scan(&org); err != nil {
			problem(w, 403, "Organization unavailable.")
			return
		}
		var actorStillMember bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted)`, org, subject(r)).Scan(&actorStillMember); err != nil || !actorStillMember {
			problem(w, 403, "Your firm membership changed. No member access was changed.")
			return
		}
		var prior string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM current_organization_members WHERE organization_id=$1 AND subject=$2),'')`, org, in.Subject).Scan(&prior); err != nil {
			problem(w, 503, "Current member access unavailable.")
			return
		}
		if prior != in.PreviousID {
			problem(w, 409, "Member access changed. Refresh history before deciding.")
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, org, in.Subject, in.Granted, in.Reason, subject(r)).Scan(&id)
		// A removed member's assistant history, portfolio goals and attention marks and preferences go with their membership.
		if err == nil && !in.Granted {
			_, err = tx.ExecContext(r.Context(), `DELETE FROM chat_messages WHERE organization_id=$1 AND author=$2`, org, in.Subject)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `DELETE FROM portfolio_operations WHERE organization_id=$1 AND author=$2`, org, in.Subject)
			}
			for _, table := range []string{"attention_marks", "attention_preferences"} {
				if err == nil {
					_, err = tx.ExecContext(r.Context(), `DELETE FROM `+table+` WHERE organization_id=$1 AND subject=$2`, org, in.Subject)
				}
			}
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Member access change could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}

func registerOrganizationRevocation(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/organization/revoke", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if !canManageMembers(r) || !slices.Contains(a.Permissions, "org:manage") {
			problem(w, 403, "Firm suspension requires org:manage.")
			return
		}
		var in struct {
			OrganizationID string `json:"organization_id"`
			Reason         string `json:"reason"`
			Confirmed      bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if !in.Confirmed || in.OrganizationID != organization(r) || len(in.Reason) < 10 || len(in.Reason) > 2000 {
			problem(w, 422, "Confirm this exact firm and a suspension reason.")
			return
		}
		result, err := db.ExecContext(r.Context(), `UPDATE organizations SET enabled=false,disabled_at=now(),disabled_by=$2,disabled_reason=$3 WHERE id=$1 AND enabled`, in.OrganizationID, subject(r), in.Reason)
		if err != nil {
			problem(w, 503, "Firm suspension could not be confirmed.")
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			problem(w, 409, "Firm is already suspended. No access was changed.")
			return
		}
		jsonReply(w, 200, map[string]bool{"revoked": true})
	})
}
