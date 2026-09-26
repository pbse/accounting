package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

type closeScope struct {
	Description    string         `json:"description"`
	RequiredPapers []string       `json:"required_papers"`
	ExternalSteps  []externalStep `json:"external_steps"`
}
type externalStep struct {
	Name      string `json:"name"`
	Complete  bool   `json:"complete"`
	Reference string `json:"reference"`
}
type readiness struct {
	DecisionID        string          `json:"decision_id"`
	Scope             closeScope      `json:"scope"`
	SuggestedPapers   []string        `json:"suggested_papers"`
	SuggestionLimited bool            `json:"suggestion_limited"`
	Digest            string          `json:"digest"`
	State             string          `json:"state"`
	Blockers          []string        `json:"blockers"`
	History           json.RawMessage `json:"history"`
	Snapshot          json.RawMessage `json:"snapshot"`
}

// Read from the caller's consistent snapshot. Sign-off never relies on browser counts.
func closeReadiness(ctx context.Context, tx *sql.Tx, id string) (readiness, error) {
	out := readiness{State: "open", Blockers: []string{}, Scope: closeScope{RequiredPapers: []string{}, ExternalSteps: []externalStep{}}}
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT jsonb_build_object(
 'close',jsonb_build_object('id',c.id::text,'organization_id',c.organization_id,'entity',c.entity,'period',c.period),
 'scope',COALESCE((SELECT to_jsonb(s) FROM close_scopes s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'null'::jsonb),
 'papers',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',w.id::text,'digest',w.digest,'review_id',w.review_id,'review_state',w.review_state,'blockers',COALESCE(w.paper->'blockers','[]')) ORDER BY w.id) FROM workpaper_review_state w WHERE w.close_id=c.id),'[]'),
 'evidence',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',e.id::text,'digest',e.digest,'superseded_by',e.superseded_by,'extraction_state',e.extraction_state,'extraction_started_at',e.extraction_started_at) ORDER BY e.id) FROM evidence e WHERE e.close_id=c.id),'[]'),
 'jobs',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',j.id::text,'state',j.state,'digest',j.digest,'finished_at',j.finished_at,'plans',j.plans,'revision',i.revision) ORDER BY j.id) FROM preparation_jobs j LEFT JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=c.id),'[]'),
 'postings',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM sandbox_postings p WHERE p.close_id=c.id),'[]'),
 'vendor_credits',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM vendor_credit_postings p WHERE p.close_id=c.id),'[]'),
 'bill_corrections',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM bill_corrections p WHERE p.close_id=c.id),'[]'),
 'production_postings',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.id) FROM production_postings p WHERE p.close_id=c.id),'[]'),
 'email',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',m.id,'state',m.state,'delivery',m.delivery,'reminder_of',m.reminder_of,'provider_id',m.provider_id,'attempts',m.attempts) ORDER BY m.id) FROM email_delivery m JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=c.id),'[]'),
 'coverage',COALESCE((SELECT jsonb_agg(to_jsonb(b) ORDER BY b.id) FROM coverage_bindings b WHERE b.close_id=c.id),'[]'),
 'attachments',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'digest',a.digest,'state',a.state,'source_id',a.source_id,'scan_version',a.scan_version,'decision_note',a.decision_note) ORDER BY a.id) FROM client_attachments a WHERE a.close_id=c.id),'[]'),
 'requests',COALESCE((SELECT jsonb_agg(to_jsonb(q) ORDER BY q.id) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=c.id),'[]')
 )||COALESCE((SELECT jsonb_build_object('xero_postings',jsonb_agg(to_jsonb(p) ORDER BY p.id)) FROM xero_postings p WHERE p.close_id=c.id HAVING count(*)>0),'{}')||COALESCE((SELECT jsonb_build_object('xero_corrections',jsonb_agg(to_jsonb(p) ORDER BY p.id)) FROM xero_corrections p WHERE p.close_id=c.id HAVING count(*)>0),'{}') FROM client_closes c WHERE c.id::text=$1`, id).Scan(&raw)
	if err != nil {
		return out, err
	}
	coverage, err := currentCoverage(ctx, tx, id)
	if err != nil {
		return out, err
	}
	for _, item := range coverage.Items {
		if item.State != "resolved" && item.State != "excluded" {
			out.Blockers = append(out.Blockers, "Coverage for "+item.Name+": "+strings.ReplaceAll(item.State, "_", " ")+".")
		}
	}
	var pending int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM client_attachments WHERE close_id=$1 AND state NOT IN ('released','rejected')`, id).Scan(&pending); err != nil {
		return out, err
	}
	if pending > 0 {
		out.Blockers = append(out.Blockers, fmt.Sprintf("%d client attachments still require scanning or disposition.", pending))
	}
	// Include derived resolution and current ledger state in the signed digest.
	var snapshotMap map[string]json.RawMessage
	if err = json.Unmarshal(raw, &snapshotMap); err != nil {
		return out, err
	}
	snapshotMap["coverage_resolution"], _ = json.Marshal(coverage)
	raw, err = json.Marshal(snapshotMap)
	if err != nil {
		return out, err
	}
	out.Snapshot = json.RawMessage(raw)
	// PostgreSQL jsonb text fixes object key order for a stable, server-owned digest.
	out.Digest = preparation.Digest(raw)
	var snapshot struct {
		Scope *struct {
			Scope closeScope `json:"scope"`
		} `json:"scope"`
		Papers []struct {
			ID          string   `json:"id"`
			ReviewState string   `json:"review_state"`
			Blockers    []string `json:"blockers"`
		} `json:"papers"`
		Jobs []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"jobs"`
		Postings []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"postings"`
		VendorCredits []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"vendor_credits"`
		BillCorrections []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"bill_corrections"`
		ProductionPostings []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"production_postings"`
	}
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return out, err
	}
	// Keep suggestions out of the signed digest: UI guidance cannot reopen a
	// previously signed accounting snapshot on deployment.
	var suggested []byte
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(w.id::text ORDER BY w.id),'[]'::jsonb) FROM workpapers w WHERE w.close_id::text=$1 AND NOT EXISTS (SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text)`, id).Scan(&suggested); err != nil {
		return out, err
	}
	if err = json.Unmarshal(suggested, &out.SuggestedPapers); err != nil {
		return out, err
	}
	if len(out.SuggestedPapers) > 100 {
		out.SuggestedPapers = []string{}
		out.SuggestionLimited = true
	}
	if snapshot.Scope == nil {
		out.Blockers = append(out.Blockers, "Define the supported scope and required work before sign-off.")
	} else {
		out.Scope = snapshot.Scope.Scope
		for _, id := range out.Scope.RequiredPapers {
			found := false
			for _, p := range snapshot.Papers {
				if p.ID == id {
					found = true
					if p.ReviewState != "approved" || len(p.Blockers) > 0 {
						out.Blockers = append(out.Blockers, "Draft #"+id+" requires current approval and resolved calculation blockers.")
					}
					break
				}
			}
			if !found {
				out.Blockers = append(out.Blockers, "Required draft #"+id+" is unavailable.")
			}
		}
		for _, s := range out.Scope.ExternalSteps {
			if !s.Complete || strings.TrimSpace(s.Reference) == "" {
				out.Blockers = append(out.Blockers, "External step: "+s.Name)
			}
		}
	}
	for _, j := range snapshot.Jobs {
		if j.State != "completed" && j.State != "cancelled" {
			out.Blockers = append(out.Blockers, "Work #"+j.ID+" is "+strings.ReplaceAll(j.State, "_", " ")+".")
		}
	}
	for _, p := range snapshot.Postings {
		if p.State == "executing" || p.State == "unknown" {
			out.Blockers = append(out.Blockers, fmt.Sprintf("Sandbox attempt #%d must be resolved.", p.ID))
		}
	}
	for _, p := range snapshot.VendorCredits {
		if p.State == "executing" || p.State == "unknown" {
			out.Blockers = append(out.Blockers, fmt.Sprintf("Supplier credit attempt #%d must be resolved.", p.ID))
		}
	}
	for _, p := range snapshot.BillCorrections {
		if p.State == "executing" || p.State == "unknown" {
			out.Blockers = append(out.Blockers, fmt.Sprintf("Bill correction attempt #%d must be resolved.", p.ID))
		}
	}
	// Credit applications and Xero journals are checked apart from the signed snapshot, so adding them left existing
	// sign-offs intact.
	uncertain, err := tx.QueryContext(ctx, `SELECT 'Credit application',id FROM credit_applications WHERE close_id::text=$1 AND state IN ('executing','unknown') UNION ALL SELECT 'Xero journal',id FROM xero_postings WHERE close_id::text=$1 AND state IN ('executing','unknown') UNION ALL SELECT 'Xero bill correction',id FROM xero_corrections WHERE close_id::text=$1 AND state IN ('executing','unknown') ORDER BY 1,2`, id)
	if err != nil {
		return out, err
	}
	for uncertain.Next() {
		var kind string
		var attempt int64
		if err = uncertain.Scan(&kind, &attempt); err != nil {
			uncertain.Close()
			return out, err
		}
		out.Blockers = append(out.Blockers, fmt.Sprintf("%s attempt #%d must be resolved.", kind, attempt))
	}
	uncertain.Close()
	var recoded sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT max(id) FROM xero_corrections c WHERE close_id::text=$1 AND state='verified' AND verified_at>COALESCE((SELECT max((s.payload->>'trial_balance_requested_at')::timestamptz) FROM xero_snapshots s WHERE s.close_id=c.close_id),'-infinity')`, id).Scan(&recoded); err != nil {
		return out, err
	}
	if recoded.Valid {
		out.Blockers = append(out.Blockers, fmt.Sprintf("Capture Xero again: bill correction #%d changed the ledger after the latest capture.", recoded.Int64))
	}
	for _, p := range snapshot.ProductionPostings {
		if p.State == "executing" || p.State == "unknown" {
			out.Blockers = append(out.Blockers, fmt.Sprintf("Production attempt #%d requires canonical readback or investigation.", p.ID))
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',s.id::text,'decision',s.decision,'snapshot_digest',s.snapshot_digest,'note',s.note,'actor',s.actor,'created_at',s.created_at) ORDER BY s.id DESC),'[]') FROM close_signoffs s WHERE s.close_id::text=$1`, id).Scan(&out.History)
	if err != nil {
		return out, err
	}
	var history []struct {
		ID       string `json:"id"`
		Decision string `json:"decision"`
		Digest   string `json:"snapshot_digest"`
	}
	if err = json.Unmarshal(out.History, &history); err != nil {
		return out, err
	}
	if len(history) > 0 {
		out.DecisionID = history[0].ID
	}
	if len(history) > 0 && history[0].Decision == "signed_off" {
		out.State = "signed_off"
		if history[0].Digest != out.Digest || len(out.Blockers) > 0 {
			out.State = "reopen_required"
		}
	}
	return out, nil
}

func registerReadiness(mux *http.ServeMux, db *sql.DB) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ExpectedDecisionID string     `json:"expected_decision_id"`
			Digest             string     `json:"digest"`
			Scope              closeScope `json:"scope"`
			Action             string     `json:"action"`
			Note               string     `json:"note"`
			Confirmed          bool       `json:"confirmed"`
		}
		actor := ""
		if r.Method == "POST" {
			var ok bool
			if actor, ok = reviewActor(w, r); !ok {
				return
			}
			if r.PathValue("action") == "signoff" {
				if a := auth.Actor(r.Context()); a != nil && a.Organization != "" && !slices.Contains(a.Permissions, "close:signoff") {
					problem(w, 403, "Your organization role lacks close:signoff.")
					return
				}
			}
			if !decode(w, r, &in) {
				return
			}
		}
		options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
		if r.Method == "POST" {
			options = nil
		}
		tx, err := db.BeginTx(r.Context(), options)
		if err != nil {
			problem(w, 503, "Close readiness unavailable.")
			return
		}
		defer tx.Rollback()
		if r.Method == "POST" {
			var id string
			if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&id); err != nil {
				problem(w, 409, "Close changed or is unavailable. Refresh before deciding.")
				return
			}
		}
		current, err := closeReadiness(r.Context(), tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Could not read close readiness.")
			return
		}
		if r.Method == "GET" {
			jsonReply(w, 200, current)
			return
		}
		if in.Digest != current.Digest || in.ExpectedDecisionID != current.DecisionID {
			problem(w, 409, "Close changed. Refresh readiness and review the new snapshot.")
			return
		}
		signedOff := false
		switch r.PathValue("action") {
		case "scope":
			if current.State != "open" {
				problem(w, 409, "Reopen the signed scope before changing its requirements.")
				return
			}
			s := in.Scope
			if s.ExternalSteps == nil {
				s.ExternalSteps = []externalStep{}
			}
			s.Description = strings.TrimSpace(s.Description)
			if len(s.Description) < 1 || len(s.Description) > 2000 || (len(s.RequiredPapers) == 0 && len(s.ExternalSteps) == 0) || len(s.RequiredPapers) > 100 || len(s.ExternalSteps) > 20 {
				problem(w, 422, "Describe the supported scope and select at least one draft or external step (up to 100 drafts and 20 steps).")
				return
			}
			seen := map[string]bool{}
			for _, id := range s.RequiredPapers {
				var exists bool
				if e := tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM workpapers WHERE id::text=$1 AND close_id::text=$2)`, id, r.PathValue("id")).Scan(&exists); e != nil {
					problem(w, 503, "Could not verify required work.")
					return
				}
				if !exists || seen[id] {
					problem(w, 422, "Choose distinct drafts from this close.")
					return
				}
				seen[id] = true
			}
			seen = map[string]bool{}
			for i := range s.ExternalSteps {
				step := &s.ExternalSteps[i]
				step.Name = strings.TrimSpace(step.Name)
				step.Reference = strings.TrimSpace(step.Reference)
				if step.Name == "" || len(step.Name) > 200 || len(step.Reference) > 2000 || seen[step.Name] || (step.Complete && step.Reference == "") {
					problem(w, 422, "Give each external step a distinct name and completion evidence when marked done.")
					return
				}
				seen[step.Name] = true
			}
			raw, _ := json.Marshal(s)
			_, err = tx.ExecContext(r.Context(), `INSERT INTO close_scopes(close_id,scope,actor) VALUES($1,$2,$3)`, r.PathValue("id"), string(raw), actor)
		case "signoff":
			signedOff = in.Action == "sign"
			in.Note = strings.TrimSpace(in.Note)
			if !in.Confirmed || len(in.Note) < 1 || len(in.Note) > 2000 || (in.Action != "sign" && in.Action != "reopen") {
				problem(w, 422, "Confirm the scoped decision and record a reason (1–2,000 bytes).")
				return
			}
			decision := "signed_off"
			if in.Action == "sign" {
				if current.State != "open" || len(current.Blockers) > 0 {
					problem(w, 409, "Resolve readiness blockers and explicitly reopen any previous sign-off first.")
					return
				}
			} else {
				if current.State == "open" {
					problem(w, 409, "This scope is already open.")
					return
				}
				decision = "reopened"
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,$2,$3,$4,$5,$6)`, r.PathValue("id"), decision, current.Digest, string(current.Snapshot), in.Note, actor)
		default:
			problem(w, 404, "Unknown close action.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 409, "Decision could not be confirmed. Refresh readiness/history before retrying.")
			return
		}
		reply := map[string]any{"saved": true}
		// A signed-off month prepares the next one: reviewed schedules are queued for next-month preparation.
		if signedOff {
			carried, ok := autoCarry(db, r, r.PathValue("id"))
			reply["carried"] = carried
			if !ok {
				reply["carry_error"] = "Next month could not be started automatically. Open Carry work into the next month."
			}
		}
		jsonReply(w, 200, reply)
	}
	mux.HandleFunc("GET /v1/closes/{id}/readiness", handler)
	mux.HandleFunc("POST /v1/closes/{id}/{action}", handler)
}
