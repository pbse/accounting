package transport

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
)

func actorPermissions(r *http.Request) []string {
	if a := auth.Actor(r.Context()); a != nil {
		return a.Permissions
	}
	return nil
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// independenceIssue explains why approver may not approve paperID under the firm's policy, or returns "".
func independenceIssue(ctx context.Context, q querier, closeID, paperID, approver string) (string, error) {
	var required bool
	var preparers []string
	err := q.QueryRowContext(ctx, `SELECT o.require_independent_review,COALESCE(array_agg(DISTINCT p.who) FILTER (WHERE p.who<>''),'{}') FROM client_closes c JOIN organizations o ON o.id=c.organization_id LEFT JOIN (
 SELECT w.created_by AS who FROM workpapers w WHERE w.id::text=$2 AND w.created_by IS NOT NULL
 UNION ALL SELECT j.created_by FROM preparation_jobs j WHERE j.close_id::text=$1 AND j.workpaper_ids @> jsonb_build_array($2::text)
 UNION ALL SELECT i.confirmed_by FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id::text=$1 AND j.workpaper_ids @> jsonb_build_array($2::text) AND i.confirmed_by IS NOT NULL
) p ON true WHERE c.id::text=$1 GROUP BY o.require_independent_review`, closeID, paperID).Scan(&required, &preparers)
	if err != nil || !required {
		return "", err
	}
	if len(preparers) == 0 {
		return "This firm requires an independent reviewer, and no preparer is recorded for this draft. Prepare it from linked evidence first.", nil
	}
	if slices.Contains(preparers, approver) {
		return "This firm requires an independent reviewer. You prepared or confirmed this draft, so another member must approve it.", nil
	}
	return "", nil
}

func registerIndependence(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/organization/policy", func(w http.ResponseWriter, r *http.Request) {
		var required bool
		if err := db.QueryRowContext(r.Context(), `SELECT require_independent_review FROM organizations WHERE id=$1`, organization(r)).Scan(&required); err != nil {
			problem(w, 503, "Firm policy is unavailable.")
			return
		}
		a := actorPermissions(r)
		jsonReply(w, 200, map[string]any{"independent_review": required, "hosted": organization(r) != "local-development", "can_manage": organization(r) != "local-development" && slices.Contains(a, "org:manage")})
	})
	mux.HandleFunc("POST /v1/closes/organization/policy", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			IndependentReview bool   `json:"independent_review"`
			Reason            string `json:"reason"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Reason = strings.TrimSpace(input.Reason)
		if organization(r) == "local-development" {
			problem(w, 409, "Local development shares one identity, so independent review can only be enforced for hosted firms.")
			return
		}
		if !slices.Contains(actorPermissions(r), "org:manage") {
			problem(w, 403, "Changing firm review policy requires org:manage.")
			return
		}
		if len(input.Reason) < 5 || len(input.Reason) > 500 {
			problem(w, 422, "Explain the policy change in 5–500 characters.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Firm policy is unavailable.")
			return
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(r.Context(), `UPDATE organizations SET require_independent_review=$2 WHERE id=$1`, organization(r), input.IndependentReview); err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_policy_changes(organization_id,independent_review,reason,actor) VALUES($1,$2,$3,$4)`, organization(r), input.IndependentReview, input.Reason, subject(r))
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Firm policy change could not be confirmed.")
			return
		}
		jsonReply(w, 200, map[string]bool{"independent_review": input.IndependentReview})
	})
}
