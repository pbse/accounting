package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"close.local/backend/internal/quickbooks"
)

// The context is the selected work and the close's evidence less unrelated scheduled ledger captures: an upload or review changes it.
func RequestContext(ctx context.Context, tx *sql.Tx, closeID string, ids []string) (string, string, error) {
	var entity, period string
	if err := tx.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id=$1`, closeID).Scan(&entity, &period); err != nil {
		return "", "", err
	}
	var evidence []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(id,digest,superseded_by,extraction_state) ORDER BY id),'[]'::jsonb) FROM evidence WHERE close_id=$1 AND created_by NOT IN ('quickbooks-sync','scheduled-quickbooks-refresh')`, closeID).Scan(&evidence); err != nil {
		return "", "", err
	}
	contextParts := []string{entity, period, string(evidence)}
	questions := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		var kind, digest, state, reviewID, note string
		var raw []byte
		err := tx.QueryRowContext(ctx, `SELECT w.kind,w.digest,w.review_state,w.review_id,w.paper,COALESCE(r.note,'') FROM workpaper_review_state w LEFT JOIN workpaper_reviews r ON r.id::text=w.review_id WHERE w.id::text=$1 AND w.close_id=$2`, id, closeID).Scan(&kind, &digest, &state, &reviewID, &raw, &note)
		if err != nil {
			return "", "", fmt.Errorf("Selected workpaper is unavailable in this close")
		}
		contextParts = append(contextParts, id, digest, state, reviewID)
		asks, err := paperQuestions(id, kind, state, note, raw)
		if err != nil {
			return "", "", err
		}
		for _, question := range asks {
			if key := strings.ToLower(strings.TrimSpace(question)); !seen[key] {
				seen[key] = true
				questions = append(questions, question)
			}
		}
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Draft request — %s · %s\n\nPlease help us complete this month's preparation:\n", entity, period)
	for i, q := range questions {
		fmt.Fprintf(&body, "\n%d. %s\n", i+1, q)
	}
	if len(questions) == 0 {
		body.WriteString("\n[Add the specific missing fact or document and why it is needed. No request was inferred from the selected work.]\n")
	}
	body.WriteString("\nThank you. Please use the firm's agreed secure channel for sensitive documents.")
	encoded, _ := json.Marshal(contextParts)
	return Digest(encoded), body.String(), nil
}

// paperQuestions is what a request asks the client about one draft: its missing sources, a changed source, each blocker
// and a reviewer's requested change.
func paperQuestions(id, kind, state, note string, raw []byte) ([]string, error) {
	var paper struct {
		Sources  []Source `json:"sources"`
		Blockers []string `json:"blockers"`
		Input    struct {
			SourceReference string `json:"source_reference"`
			Account         string `json:"account"`
		} `json:"input"`
	}
	if err := json.Unmarshal(raw, &paper); err != nil {
		return nil, err
	}
	label := paper.Input.SourceReference
	if kind == "reconciliation" {
		label = paper.Input.Account
	}
	if label == "" {
		label = kind + " workpaper #" + id
	}
	questions := []string{}
	if len(paper.Sources) == 0 {
		questions = append(questions, "Please provide the source documents for "+label+" so we can verify the preparation.")
	}
	if state == "source_changed" {
		questions = append(questions, "Please confirm the current supporting documents for "+label+". Earlier preparation is out of date and must be rechecked.")
	}
	for _, blocker := range paper.Blockers {
		questions = append(questions, "For "+label+", please provide supporting records or an explanation to help us investigate: "+blocker)
	}
	if state == "changes_requested" && note != "" {
		questions = append(questions, "For "+label+", please clarify the following review question: "+note)
	}
	return questions, nil
}

// RequestCandidate is a current draft a consolidated request would ask the client about.
type RequestCandidate struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ReviewState string `json:"review_state"`
}

// RequestCandidates lists the close's current drafts (no later draft revises them) that a request would ask about,
// oldest first and at most 20, with the number of others.
func RequestCandidates(ctx context.Context, tx *sql.Tx, closeID string) ([]RequestCandidate, int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT w.id::text,w.kind,w.review_state,w.paper,COALESCE(r.note,'') FROM workpaper_review_state w LEFT JOIN workpaper_reviews r ON r.id::text=w.review_id
 WHERE w.close_id::text=$1 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text) ORDER BY w.id`, closeID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, more := []RequestCandidate{}, 0
	for rows.Next() {
		var c RequestCandidate
		var raw []byte
		var note string
		if err = rows.Scan(&c.ID, &c.Kind, &c.ReviewState, &raw, &note); err != nil {
			return nil, 0, err
		}
		asks, err := paperQuestions(c.ID, c.Kind, c.ReviewState, note, raw)
		if err != nil {
			return nil, 0, err
		}
		if len(asks) > 0 && len(out) < 20 {
			out = append(out, c)
		} else if len(asks) > 0 {
			more++
		}
	}
	return out, more, rows.Err()
}

// RequestPapers is the state, as review reads it, of the drafts that request draft d was built from.
const RequestPapers = `(SELECT COALESCE(jsonb_agg(jsonb_build_array(w.id::text,w.digest,w.review_state,w.review_id) ORDER BY w.id),'[]'::jsonb) FROM workpaper_review_state w WHERE w.close_id=d.close_id AND d.workpaper_ids ? w.id::text)`

// FreshClientRequest says why an approved request cannot take its first reply: its coverage or target work changed, a
// document its work was read from was replaced, or the drafts its wording came from are no longer as approved. Other
// evidence the close gains does not matter.
func FreshClientRequest(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	if err := FreshRequestTargets(ctx, tx, closeID, requestID); err != nil {
		return err
	}
	var papers, sources bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(q.approved_papers=`+RequestPapers+`,false),
 NOT EXISTS(SELECT 1 FROM request_jobs rj JOIN preparation_intakes i ON i.job_id=rj.job_id JOIN evidence e ON e.id=i.source_id WHERE rj.request_id=q.id AND e.superseded_by IS NOT NULL)
 FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id=$1`, requestID).Scan(&papers, &sources); err != nil {
		return fmt.Errorf("Request context unavailable.")
	}
	if !sources {
		return fmt.Errorf("A document this request's work was read from was replaced. Review and issue a fresh request.")
	}
	if !papers {
		return fmt.Errorf("The drafts this request asks about changed. Review and issue a fresh request.")
	}
	return nil
}

// SourceAllows is SQL that is true when the month of the close closeExpr (a bigint expression) names may read
// provider's captures ("quickbooks" or "xero"): its books source (books_source, migration 148) is the newest capture,
// or that provider.
func SourceAllows(closeExpr, provider string) string {
	return `COALESCE((SELECT books_source(sc.organization_id,sc.entity,sc.period) FROM client_closes sc WHERE sc.id=` + closeExpr + `),'newest') IN ('newest','` + provider + `')`
}

// requestCapture is SQL over work_requests q and request_drafts d: a consolidated request's ledger capture is still the
// close's books' capture, chosen as coverage chooses it (transport's ledgerCoverage), of the month's source: the latest
// QuickBooks period capture of the connection the books are read from, unless the latest Xero capture, named
// "xero:<id>", is newer. A QuickBooks capture also needs its current
// source and an active connection; a Xero capture, an active connection.
const requestCapture = `q.coverage_scope->>'snapshot_id'=(SELECT CASE WHEN xs.id IS NOT NULL AND (qs.id IS NULL OR xs.captured_at>qs.captured_at) THEN 'xero:'||xs.id ELSE qs.id::text END
  FROM (SELECT 1) one LEFT JOIN LATERAL (SELECT s.id,s.captured_at FROM quickbooks_snapshots s WHERE s.close_id=d.close_id AND s.payload ? 'period'
   AND s.connection_id=COALESCE(` + quickbooks.ReadingConnectionBefore + `s.close_id` + quickbooks.ReadingConnectionAfter + `,s.connection_id) AND COALESCE((SELECT books_source(sc.organization_id,sc.entity,sc.period) FROM client_closes sc WHERE sc.id=s.close_id),'newest') IN ('newest','quickbooks') ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) qs ON true
  LEFT JOIN LATERAL (SELECT id,captured_at FROM xero_snapshots WHERE close_id=d.close_id AND COALESCE((SELECT books_source(sc.organization_id,sc.entity,sc.period) FROM client_closes sc WHERE sc.id=d.close_id),'newest') IN ('newest','xero') ORDER BY captured_at DESC,xero_snapshots.id DESC LIMIT 1) xs ON true)
 AND (EXISTS(SELECT 1 FROM quickbooks_snapshots s JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id JOIN quickbooks_connections x ON x.id=s.connection_id WHERE s.id::text=q.coverage_scope->>'snapshot_id' AND e.superseded_by IS NULL AND x.state='active')
  OR EXISTS(SELECT 1 FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE 'xero:'||s.id=q.coverage_scope->>'snapshot_id' AND x.state='active'))`

// requestBindings is each binding b a consolidated request q approved, with its account's newest binding cur.
const requestBindings = `jsonb_array_elements(q.coverage_scope->'bindings') b LEFT JOIN LATERAL (SELECT c.id::text AS id,c.excluded,c.job_ids FROM coverage_bindings c WHERE c.close_id=d.close_id AND COALESCE('xero:'||c.xero_snapshot_id,c.snapshot_id::text)=q.coverage_scope->>'snapshot_id' AND c.account_id=b->>'account_id' ORDER BY c.id DESC LIMIT 1) cur ON true`

// RequestCoverageIsCurrent is SQL over work_requests q and request_drafts d: a consolidated request is still on its
// close's latest ledger capture, with a current source and active connection, and every binding is the current one.
const RequestCoverageIsCurrent = `(q.coverage_scope IS NULL OR (` + requestCapture + `
 AND NOT EXISTS(SELECT 1 FROM ` + requestBindings + ` WHERE COALESCE(cur.id,'') IS DISTINCT FROM b->>'id')))`

// RequestReleased is SQL over request_jobs rj of work_requests q: work the request came to cover after its approval,
// from documents the client sent. The approval's own work never is, and a consolidated request approved before it
// recorded that work has none.
const RequestReleased = `(rj.job_id IS DISTINCT FROM q.job_id AND (q.coverage_scope IS NULL OR (q.coverage_scope ? 'jobs' AND NOT (q.coverage_scope->'jobs') ? rj.job_id::text)))`

// RequestCoverageFollowsReply is RequestCoverageIsCurrent once a request has a reply: an account's binding may also have
// moved to add the request's released work, keeping every job it had when the request was approved.
const RequestCoverageFollowsReply = `(q.coverage_scope IS NULL OR (` + requestCapture + `
 AND NOT EXISTS(SELECT 1 FROM ` + requestBindings + ` CROSS JOIN LATERAL (SELECT COALESCE((SELECT a.job_ids FROM coverage_bindings a WHERE a.id=NULLIF(b->>'id','')::bigint),'[]'::jsonb) AS jobs) approved
  WHERE COALESCE(cur.id,'') IS DISTINCT FROM b->>'id' AND (cur.id IS NULL OR cur.excluded OR NOT cur.job_ids @> approved.jobs
   OR EXISTS(SELECT 1 FROM jsonb_array_elements_text(cur.job_ids) added WHERE NOT approved.jobs ? added
    AND NOT EXISTS(SELECT 1 FROM request_jobs rj WHERE rj.request_id=q.id AND rj.job_id::text=added AND ` + RequestReleased + `))))))`

// Consolidated requests bind their exact jobs/revisions and ledger capture.
// Legacy single-job requests keep their existing route-level state fences.
func RequestCoverageCurrent(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	return requestCoverage(ctx, tx, closeID, requestID, RequestCoverageIsCurrent)
}

// RequestCoverageFollows is RequestCoverageCurrent for a request that has a reply, letting it take the rest of the
// client's answers and documents while the firm works on the first ones.
func RequestCoverageFollows(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	return requestCoverage(ctx, tx, closeID, requestID, RequestCoverageFollowsReply)
}

func requestCoverage(ctx context.Context, tx *sql.Tx, closeID, requestID, rule string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT d.close_id::text=$2 AND `+rule+` FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id=$1`, requestID, closeID).Scan(&valid)
	if err != nil || !valid {
		return fmt.Errorf("Request coverage changed. Review and issue a fresh request.")
	}
	return nil
}
func FreshRequestTargets(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	if err := RequestCoverageCurrent(ctx, tx, closeID, requestID); err != nil {
		return err
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT q.coverage_scope IS NULL OR (
 NOT EXISTS(SELECT 1 FROM client_request_links l WHERE l.request_id=q.id AND l.used_at IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM work_request_targets t JOIN preparation_jobs j ON j.id=t.job_id JOIN preparation_intakes i ON i.job_id=j.id WHERE t.request_id=q.id AND (j.close_id::text<>$2 OR j.state<>'waiting_client' OR i.revision<>t.revision))) FROM work_requests q WHERE q.id=$1`, requestID, closeID).Scan(&valid)
	if err != nil || !valid {
		return fmt.Errorf("Request work changed. Review and issue a fresh request.")
	}
	return nil
}
func ResumeRequestJobs(ctx context.Context, tx *sql.Tx, requestID string) error {
	_, err := tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_evidence',error='',finished_at=NULL WHERE id IN (SELECT job_id FROM request_jobs WHERE request_id=$1) AND state IN ('waiting_client','needs_confirmation','failed','waiting_evidence','interpreting')`, requestID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL,agent_attempts=0 WHERE job_id IN (SELECT j.id FROM preparation_jobs j JOIN request_jobs q ON q.job_id=j.id WHERE q.request_id=$1 AND j.state='waiting_evidence')`, requestID)
	}
	return err
}
