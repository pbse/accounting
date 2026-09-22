package transport

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"sort"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

// Local review is attributed to the shared development workspace, never a named accountant.
func reviewActor(w http.ResponseWriter, r *http.Request) (string, bool) {
	actor := auth.Actor(r.Context())
	if actor != nil && actor.Organization != "" {
		if !slices.Contains(actor.Permissions, "workpapers:review") {
			problem(w, 403, "Your organization role lacks workpapers:review.")
			return "", false
		}
		return actor.Subject, true
	}
	return "local-workspace (development)", true
}
func registerReview(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/workpapers/{paper}", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT paper FROM workpapers WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("paper"), r.PathValue("id")).Scan(&raw)
		if err == sql.ErrNoRows {
			problem(w, 404, "Workpaper not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Workpaper unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(raw))
	})
	mux.HandleFunc("GET /v1/closes/{id}/workpapers/{paper}/changes", func(w http.ResponseWriter, r *http.Request) {
		var current, before []byte
		var kind string
		err := db.QueryRowContext(r.Context(), `SELECT paper,kind FROM workpapers WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("paper"), r.PathValue("id")).Scan(&current, &kind)
		if err == sql.ErrNoRows {
			problem(w, 404, "Workpaper not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Workpaper unavailable.")
			return
		}
		var reference struct {
			Revises string `json:"revises"`
		}
		if json.Unmarshal(current, &reference) != nil {
			problem(w, 500, "Invalid workpaper.")
			return
		}
		changes := []paperChange{}
		if reference.Revises != "" {
			err = db.QueryRowContext(r.Context(), `SELECT paper FROM workpapers WHERE id::text=$1 AND close_id::text=$2 AND kind=$3`, reference.Revises, r.PathValue("id"), kind).Scan(&before)
			if err != nil {
				problem(w, 409, "Predecessor is unavailable; inspect the full workpaper.")
				return
			}
			changes = paperChanges(before, current)
		}
		jsonReply(w, 200, map[string]any{"previous_id": reference.Revises, "changes": changes})
	})
	path := "/v1/closes/{id}/workpapers/{paper}/reviews"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		err := db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('state',w.review_state,'digest',w.digest,'review_id',w.review_id,'history',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY r.id DESC) FROM workpaper_reviews r WHERE r.workpaper_id=w.id),'[]'::jsonb)) FROM workpaper_review_state w WHERE w.id::text=$1 AND w.close_id::text=$2`, r.PathValue("paper"), r.PathValue("id")).Scan(&body)
		if err == sql.ErrNoRows {
			problem(w, 404, "Workpaper not found in this close.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not read review history.")
			return
		}
		jsonReply(w, 200, json.RawMessage(body))
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var input struct {
			Decision         string `json:"decision"`
			Note             string `json:"note"`
			Digest           string `json:"digest"`
			ExpectedReviewID string `json:"expected_review_id"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Note = strings.TrimSpace(input.Note)
		if (input.Decision != "approved" && input.Decision != "changes_requested") || len(input.Note) == 0 || len(input.Note) > 2000 {
			problem(w, 422, "Choose approve or request changes and explain your decision (1–2,000 bytes).")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Review storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var digest, state, version string
		var raw []byte
		err = tx.QueryRowContext(r.Context(), `SELECT digest,review_state,review_id,paper FROM workpaper_review_state WHERE id::text=$1 AND close_id=$2`, r.PathValue("paper"), closeID).Scan(&digest, &state, &version, &raw)
		if err == sql.ErrNoRows {
			problem(w, 404, "Workpaper not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not read current workpaper.")
			return
		}
		if input.Digest != digest || input.ExpectedReviewID != version {
			problem(w, 409, "This workpaper or its review changed. Reload the review before deciding.")
			return
		}
		var paper struct {
			Sources  []preparation.Source `json:"sources"`
			Blockers []string             `json:"blockers"`
		}
		if json.Unmarshal(raw, &paper) != nil {
			problem(w, 500, "Invalid saved workpaper.")
			return
		}
		if input.Decision == "approved" && (state == "source_changed" || len(paper.Sources) == 0 || len(paper.Blockers) > 0) {
			problem(w, 409, "Approval requires current linked evidence and no unresolved calculation blockers. Prepare a revised draft first.")
			return
		}
		if input.Decision == "changes_requested" {
			var active bool
			if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM sandbox_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown'))`, r.PathValue("paper")).Scan(&active); err != nil || active {
				problem(w, 409, "Resolve the outstanding sandbox execution before changing review status.")
				return
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE sandbox_postings SET state='preview',approved_by=NULL,approval_expires_at=NULL WHERE workpaper_id::text=$1 AND state='approved'`, r.PathValue("paper")); err != nil {
				problem(w, 503, "Could not invalidate posting approval.")
				return
			}
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, r.PathValue("paper"), digest, input.Decision, input.Note, actor).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Review save could not be confirmed. Reload history before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "decision": input.Decision})
	})
	mux.HandleFunc("POST /v1/closes/{id}/evidence/{evidence}/replace", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var input struct {
			Replacement string `json:"replacement_id"`
			Reason      string `json:"reason"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Reason = strings.TrimSpace(input.Reason)
		if input.Replacement == "" || len(input.Reason) == 0 || len(input.Reason) > 2000 {
			problem(w, 422, "Choose a newer uploaded source and explain what changed.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Evidence storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		var valid bool
		err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM evidence old JOIN evidence fresh ON fresh.close_id=old.close_id AND fresh.id>old.id WHERE old.id::text=$1 AND old.close_id=$2 AND old.superseded_by IS NULL AND fresh.id::text=$3 AND fresh.superseded_by IS NULL)`, r.PathValue("evidence"), closeID, input.Replacement).Scan(&valid)
		if err != nil || !valid {
			problem(w, 409, "Select a current, newer source from this close. Replaced sources cannot be replaced again or form a cycle.")
			return
		}
		source, _ := json.Marshal([]map[string]string{{"id": r.PathValue("evidence")}})
		var active bool
		err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM sandbox_postings p JOIN workpapers w ON w.id=p.workpaper_id WHERE w.close_id=$1 AND w.paper->'sources' @> $2::jsonb AND p.state IN ('executing','unknown'))`, closeID, string(source)).Scan(&active)
		if err != nil || active {
			problem(w, 409, "Resolve the affected sandbox execution before replacing its evidence.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE evidence SET superseded_by=$3,replacement_reason=$4,replaced_by=$5,replaced_at=now() WHERE id::text=$1 AND close_id=$2`, r.PathValue("evidence"), closeID, input.Replacement, input.Reason, actor); err != nil {
			problem(w, 503, "Could not replace source.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `UPDATE sandbox_postings p SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM workpapers w WHERE w.id=p.workpaper_id AND w.close_id=$1 AND w.paper->'sources' @> $2::jsonb AND p.state='approved'`, closeID, string(source)); err != nil {
			problem(w, 503, "Could not invalidate affected posting approvals.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Replacement could not be confirmed. Reload evidence before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"replaced": r.PathValue("evidence"), "replacement_id": input.Replacement})
	})
}

// Compare exact stored values, without floating-point arithmetic or inferred accounting meaning.
type paperChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

func paperChanges(before, after []byte) []paperChange {
	var old, fresh map[string]json.RawMessage
	json.Unmarshal(before, &old)
	json.Unmarshal(after, &fresh)
	changes := []paperChange{}
	var compare func(string, map[string]json.RawMessage, map[string]json.RawMessage)
	compare = func(prefix string, a, b map[string]json.RawMessage) {
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		ordered := []string{}
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			if prefix == "" && (k == "revises" || k == "status") {
				continue
			}
			if bytes.Equal(a[k], b[k]) {
				continue
			}
			rowKey := map[string]string{"schedule": "period", "sources": "role", "matches": "statement_id", "unmatched_statement": "id", "unmatched_ledger": "id"}[k]
			if rowKey != "" && prefix == "" {
				rows := func(raw json.RawMessage) map[string]json.RawMessage {
					var list []map[string]json.RawMessage
					if json.Unmarshal(raw, &list) != nil {
						return nil
					}
					result := map[string]json.RawMessage{}
					for _, row := range list {
						var key string
						if json.Unmarshal(row[rowKey], &key) != nil || key == "" || result[key] != nil {
							return nil
						}
						encoded, _ := json.Marshal(row)
						result[key] = encoded
					}
					return result
				}
				left, right := rows(a[k]), rows(b[k])
				if left != nil && right != nil {
					compare(k+".", left, right)
					continue
				}
			}
			var left, right map[string]json.RawMessage
			if json.Unmarshal(a[k], &left) == nil && json.Unmarshal(b[k], &right) == nil && left != nil && right != nil {
				compare(prefix+k+".", left, right)
				continue
			}
			display := func(v json.RawMessage) string {
				if len(v) == 0 {
					return "Not present"
				}
				var text string
				if json.Unmarshal(v, &text) == nil {
					return text
				}
				var lines []string
				if json.Unmarshal(v, &lines) == nil && lines != nil {
					return strings.Join(lines, "\n")
				}
				var pretty bytes.Buffer
				if json.Indent(&pretty, v, "", "  ") == nil {
					return pretty.String()
				}
				return string(v)
			}
			changes = append(changes, paperChange{prefix + k, display(a[k]), display(b[k])})
		}
	}
	compare("", old, fresh)
	return changes
}
