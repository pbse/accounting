package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	return reviewerName(r), true
}

// reviewerName is the name review decisions are recorded under.
func reviewerName(r *http.Request) string {
	if a := auth.Actor(r.Context()); a != nil && a.Organization != "" {
		return a.Subject
	}
	return "local-workspace (development)"
}

// Bulk review only covers drafts that change nothing in the ledger and carry no reviewer judgment.
const bulkReason = `CASE
 WHEN COALESCE(w.paper->'proposal','null'::jsonb)<>'null'::jsonb OR jsonb_array_length(COALESCE(w.paper->'proposals','[]'::jsonb))>0 THEN 'Proposes a ledger adjustment'
 WHEN jsonb_array_length(COALESCE(w.paper->'questions','[]'::jsonb))>0 THEN 'Has open questions'
 WHEN EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(w.paper->'payouts','[]'::jsonb)) p WHERE p->>'status'<>'matched') THEN 'Includes reviewer-chosen or in-transit payouts'
 WHEN jsonb_array_length(COALESCE(w.paper->'input'->'reviewed_matches','[]'::jsonb))+jsonb_array_length(COALESCE(w.paper->'input'->'prior_outstanding','[]'::jsonb))+jsonb_array_length(COALESCE(w.paper->'input'->'current_outstanding','[]'::jsonb))+jsonb_array_length(COALESCE(w.paper->'input'->'transfers','[]'::jsonb))>0 THEN 'Includes reviewer classifications'
 ELSE '' END`

// decideReview applies one decision inside the caller's transaction; the caller has locked the close.
const decisionMessage = "Choose approve or request changes and explain your decision (1–2,000 bytes)."

func validDecision(decision, note string) bool {
	note = strings.TrimSpace(note)
	return (decision == "approved" || decision == "changes_requested") && len(note) > 0 && len(note) <= 2000
}

func decideReview(ctx context.Context, tx *sql.Tx, closeID, paperID, actor, decision, note, expectedDigest, expectedReview string, bulk bool) (string, int, string) {
	if !validDecision(decision, note) {
		return "", 422, decisionMessage
	}
	note = strings.TrimSpace(note)
	var digest, state, version, kind, reason string
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT digest,review_state,review_id,paper,kind,`+bulkReason+` FROM workpaper_review_state w WHERE id::text=$1 AND close_id::text=$2`, paperID, closeID).Scan(&digest, &state, &version, &raw, &kind, &reason)
	if err == sql.ErrNoRows {
		return "", 404, "Workpaper not found."
	}
	if err != nil {
		return "", 503, "Could not read current workpaper."
	}
	if expectedDigest != digest || expectedReview != version {
		return "", 409, "This workpaper or its review changed. Reload the review before deciding."
	}
	var paper struct {
		Sources  []preparation.Source `json:"sources"`
		Blockers []string             `json:"blockers"`
	}
	if json.Unmarshal(raw, &paper) != nil {
		return "", 500, "Invalid saved workpaper."
	}
	if decision == "approved" && (state == "source_changed" || len(paper.Sources) == 0 || len(paper.Blockers) > 0) {
		return "", 409, "Approval requires current linked evidence and no unresolved calculation blockers. Prepare a revised draft first."
	}
	if decision == "approved" {
		open, err := openComments(ctx, tx, closeID, paperID)
		if err != nil {
			return "", 503, "Could not read the draft's comments."
		}
		if open > 0 {
			return "", 409, fmt.Sprintf("Resolve the %s on this draft before approving it.", plural(open, "open comment", "open comments"))
		}
	}
	if bulk && (decision != "approved" || state != "pending" || reason != "") {
		return "", 409, "Review this draft individually: " + strings.ToLower(reason) + "."
	}
	if decision == "changes_requested" {
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM vendor_credit_postings WHERE workpaper_id::text=$1 AND state IN ('executing','unknown'))`, paperID).Scan(&active); err != nil || active {
			return "", 409, "Resolve the outstanding sandbox execution before changing review status."
		}
		if _, err = tx.ExecContext(ctx, `UPDATE sandbox_postings SET state='preview',approved_by=NULL,approval_expires_at=NULL WHERE workpaper_id::text=$1 AND state='approved'`, paperID); err != nil {
			return "", 503, "Could not invalidate posting approval."
		}
		if _, err = tx.ExecContext(ctx, `UPDATE vendor_credit_postings SET state='preview',approved_by=NULL,approval_expires_at=NULL WHERE workpaper_id::text=$1 AND state='approved'`, paperID); err != nil {
			return "", 503, "Could not invalidate supplier credit approval."
		}
	}
	if decision == "approved" {
		if issue, err := preparation.BasisIssue(ctx, tx, closeID, kind, raw); err != nil || issue != "" {
			if err != nil {
				return "", 503, "The client's accounting basis could not be checked."
			}
			return "", 409, issue
		}
		issue, err := independenceIssue(ctx, tx, closeID, paperID, actor)
		if err != nil {
			return "", 503, "Firm review policy could not be checked."
		}
		if issue != "" {
			return "", 409, issue
		}
	}
	var id string
	if err = tx.QueryRowContext(ctx, `INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, paperID, digest, decision, note, actor).Scan(&id); err != nil {
		return "", 503, "Review save could not be confirmed. Reload history before retrying."
	}
	return id, 0, ""
}

func registerReview(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/workpapers/{paper}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("record") == "1" {
			var saved savedPaper
			err := db.QueryRowContext(r.Context(), `SELECT id::text,kind,created_at,paper,digest,review_state,review_id FROM workpaper_review_state WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("paper"), r.PathValue("id")).Scan(&saved.ID, &saved.Kind, &saved.CreatedAt, &saved.Paper, &saved.Digest, &saved.ReviewState, &saved.ReviewID)
			if err == sql.ErrNoRows {
				problem(w, 404, "Workpaper not found.")
				return
			}
			if err != nil {
				problem(w, 503, "Workpaper unavailable.")
				return
			}
			jsonReply(w, 200, saved)
			return
		}
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
		if !validDecision(input.Decision, input.Note) {
			problem(w, 422, decisionMessage)
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
		id, status, message := decideReview(r.Context(), tx, closeID, r.PathValue("paper"), actor, input.Decision, input.Note, input.Digest, input.ExpectedReviewID, false)
		if status != 0 {
			problem(w, status, message)
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Review save could not be confirmed. Reload history before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "decision": input.Decision})
	})
	// Each item is its own transaction with a fresh access check, so one stale draft cannot block the rest.
	// An approval request is not an approval: it only asks a person to review this draft.
	mux.HandleFunc("POST /v1/closes/{id}/workpapers/{paper}/approval-request", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Note string `json:"note"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Note = strings.TrimSpace(in.Note)
		if len(in.Note) < 5 || len(in.Note) > 1000 {
			problem(w, 422, "Say what the reviewer should check (5–1,000 characters).")
			return
		}
		requester := "local-workspace (development)"
		if a := auth.Actor(r.Context()); a != nil && a.Organization != "" {
			requester = a.Subject
		}
		var id string
		err := db.QueryRowContext(r.Context(), `INSERT INTO approval_requests(close_id,workpaper_id,requested_by,note,agent) SELECT close_id,id,$3,$4,NULLIF($5,'') FROM workpaper_review_state WHERE close_id::text=$1 AND id::text=$2 AND review_state='pending' AND jsonb_array_length(COALESCE(paper->'sources','[]'))>0 AND jsonb_array_length(COALESCE(paper->'blockers','[]'))=0 RETURNING id::text`, r.PathValue("id"), r.PathValue("paper"), requester, in.Note, auth.Agent(r.Context())).Scan(&id)
		if err == sql.ErrNoRows {
			problem(w, 409, "Only a current draft with evidence and no blockers can be sent for approval. Resolve its blockers first.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not record the approval request.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "state": "requested"})
	})
	mux.HandleFunc("POST /v1/closes/review-batch", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var input struct {
			Note  string `json:"note"`
			Items []struct {
				CloseID          string `json:"close_id"`
				WorkpaperID      string `json:"workpaper_id"`
				Digest           string `json:"digest"`
				ExpectedReviewID string `json:"expected_review_id"`
			} `json:"items"`
		}
		if !decode(w, r, &input) {
			return
		}
		if len(input.Items) < 1 || len(input.Items) > 20 {
			problem(w, 422, "Select between 1 and 20 drafts.")
			return
		}
		results := []map[string]any{}
		for _, item := range input.Items {
			result := map[string]any{"close_id": item.CloseID, "workpaper_id": item.WorkpaperID}
			id, status, message := func() (string, int, string) {
				tx, err := db.BeginTx(r.Context(), nil)
				if err != nil {
					return "", 503, "Review storage unavailable."
				}
				defer tx.Rollback()
				var closeID string
				err = tx.QueryRowContext(r.Context(), `SELECT c.id::text FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1 AND c.organization_id=$2 AND o.enabled AND ($2='local-development' OR EXISTS(SELECT 1 FROM current_organization_members m WHERE m.organization_id=$2 AND m.subject=$3 AND m.granted)) AND ($4 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) FOR UPDATE OF c`, item.CloseID, organization(r), subject(r), firmAccess(r)).Scan(&closeID)
				if err != nil {
					return "", 404, "Close not found or no longer accessible."
				}
				id, status, message := decideReview(r.Context(), tx, closeID, item.WorkpaperID, actor, "approved", input.Note, item.Digest, item.ExpectedReviewID, true)
				if status == 0 && tx.Commit() != nil {
					return "", 503, "Review save could not be confirmed. Reload history before retrying."
				}
				return id, status, message
			}()
			if status == 0 {
				result["review_id"] = id
			} else {
				result["error"] = message
			}
			results = append(results, result)
		}
		jsonReply(w, 200, map[string]any{"results": results})
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
		err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM evidence old JOIN evidence fresh ON fresh.close_id=old.close_id AND fresh.id>old.id WHERE old.id::text=$1 AND old.close_id=$2 AND old.superseded_by IS NULL AND fresh.id::text=$3 AND fresh.superseded_by IS NULL AND fresh.extraction_state='ready' AND fresh.digest<>old.digest)`, r.PathValue("evidence"), closeID, input.Replacement).Scan(&valid)
		if err != nil || !valid {
			problem(w, 409, "Select a current, newer source from this close. Replaced sources cannot be replaced again or form a cycle.")
			return
		}
		source, _ := json.Marshal([]map[string]string{{"id": r.PathValue("evidence")}})
		var active bool
		err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM sandbox_postings p JOIN workpapers w ON w.id=p.workpaper_id WHERE w.close_id=$1 AND w.paper->'sources' @> $2::jsonb AND p.state IN ('executing','unknown')) OR EXISTS(SELECT 1 FROM vendor_credit_postings p JOIN workpapers w ON w.id=p.workpaper_id WHERE w.close_id=$1 AND (w.paper->'sources' @> $2::jsonb OR p.evidence_id::text=$3) AND p.state IN ('executing','unknown'))`, closeID, string(source), r.PathValue("evidence")).Scan(&active)
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
		if _, err = tx.ExecContext(r.Context(), `UPDATE vendor_credit_postings p SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM workpapers w WHERE w.id=p.workpaper_id AND w.close_id=$1 AND (w.paper->'sources' @> $2::jsonb OR p.evidence_id::text=$3) AND p.state='approved'`, closeID, string(source), r.PathValue("evidence")); err != nil {
			problem(w, 503, "Could not invalidate affected supplier credit approval.")
			return
		}
		// The replacement's own intake may have stopped for a person while the replaced source was still current; the agent
		// reads it again now that it is not.
		if _, err = tx.ExecContext(r.Context(), `WITH resumed AS (UPDATE preparation_jobs j SET state='waiting_evidence',error='' FROM preparation_intakes i WHERE i.job_id=j.id AND j.close_id=$1 AND i.source_id::text=$2 AND j.state='needs_confirmation' AND i.proposal->>'engine' IN ('openai','context_machine') RETURNING j.id)
 UPDATE preparation_intakes SET agent_attempts=0,agent_retry_at=NULL WHERE job_id IN (SELECT id FROM resumed)`, closeID, input.Replacement); err != nil {
			problem(w, 503, "Could not resume the replacement's interpretation.")
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
			if prefix == "" && (k == "revises" || k == "status" || k == "straight_through") {
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
