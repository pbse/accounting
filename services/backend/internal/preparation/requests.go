package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// Inspect only selected, same-close work and the evidence inventory. A new upload
// or review changes the snapshot even if no existing source was replaced.
func RequestContext(ctx context.Context, tx *sql.Tx, closeID string, ids []string) (string, string, error) {
	var entity, period string
	if err := tx.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id=$1`, closeID).Scan(&entity, &period); err != nil {
		return "", "", err
	}
	var evidence []byte
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_array(id,digest,superseded_by,extraction_state) ORDER BY id),'[]'::jsonb) FROM evidence WHERE close_id=$1`, closeID).Scan(&evidence); err != nil {
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
	body.WriteString("\nThank you. Please use the firm's agreed secure channel for sensitive documents.\n")
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

// The approved draft fixes both wording and evidence context. Reissue requires
// renewed review when a source or selected workpaper has changed.
func FreshClientRequest(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	var digest string
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT d.context_digest,d.workpaper_ids FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id=$1`, requestID).Scan(&digest, &raw); err != nil {
		return fmt.Errorf("Request context unavailable.")
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return fmt.Errorf("Request context unavailable.")
	}
	if err := FreshRequestTargets(ctx, tx, closeID, requestID); err != nil {
		return err
	}
	current, _, err := RequestContext(ctx, tx, closeID, ids)
	if err != nil || current != digest {
		return fmt.Errorf("Request evidence changed. Ask the firm to review and issue a new request.")
	}
	return nil
}

// RequestCoverageIsCurrent is SQL over work_requests q and request_drafts d: a consolidated request is still on its
// close's latest ledger capture, with a current source and active connection, and every binding is the current one.
const RequestCoverageIsCurrent = `(q.coverage_scope IS NULL OR (
 q.coverage_scope->>'snapshot_id'=(SELECT id::text FROM quickbooks_snapshots WHERE close_id=d.close_id AND payload ? 'period' ORDER BY quickbooks_snapshots.id DESC LIMIT 1)
 AND EXISTS(SELECT 1 FROM quickbooks_snapshots s JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id JOIN quickbooks_connections x ON x.id=s.connection_id WHERE s.id::text=q.coverage_scope->>'snapshot_id' AND e.superseded_by IS NULL AND x.state='active')
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(q.coverage_scope->'bindings') b WHERE b->>'id' IS DISTINCT FROM COALESCE((SELECT id::text FROM coverage_bindings c WHERE c.close_id=d.close_id AND c.snapshot_id::text=q.coverage_scope->>'snapshot_id' AND c.account_id=b->>'account_id' ORDER BY c.id DESC LIMIT 1),''))
 ))`

// Consolidated requests bind their exact jobs/revisions and ledger capture.
// Legacy single-job requests keep their existing route-level state fences.
func RequestCoverageCurrent(ctx context.Context, tx *sql.Tx, closeID, requestID string) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT d.close_id::text=$2 AND `+RequestCoverageIsCurrent+` FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id=$1`, requestID, closeID).Scan(&valid)
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
