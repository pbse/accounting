package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

type cancellableJob struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Revision int    `json:"revision"`
}

func cancellableJobs(ctx context.Context, tx *sql.Tx, closeID string, lock bool) ([]cancellableJob, string, error) {
	query := `SELECT j.id::text,j.state,i.revision FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled') ORDER BY j.id`
	if lock {
		query += ` FOR UPDATE OF j,i`
	}
	rows, err := tx.QueryContext(ctx, query, closeID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	jobs := []cancellableJob{}
	for rows.Next() {
		var job cancellableJob
		if err = rows.Scan(&job.ID, &job.State, &job.Revision); err != nil {
			return nil, "", err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(jobs)
	return jobs, preparation.Digest(raw), nil
}

func recheckableJobs(ctx context.Context, tx *sql.Tx, closeID string, lock bool) ([]cancellableJob, string, error) {
	query := `SELECT j.id::text,j.state,i.revision FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND j.state IN ('failed','needs_confirmation') AND e.superseded_by IS NULL AND e.extraction_state='ready' ORDER BY j.id`
	if lock {
		query += ` FOR UPDATE OF j,i`
	}
	rows, err := tx.QueryContext(ctx, query, closeID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	jobs := []cancellableJob{}
	for rows.Next() {
		var job cancellableJob
		if err = rows.Scan(&job.ID, &job.State, &job.Revision); err != nil {
			return nil, "", err
		}
		jobs = append(jobs, job)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	raw, _ := json.Marshal(jobs)
	return jobs, preparation.Digest(raw), nil
}

// Reuse the upload-owned durable jobs. The parent fixes the evidence scope;
// progress is read from those jobs rather than stored in a second status cache.
func preparationInventory(ctx context.Context, tx *sql.Tx, closeID string) ([]byte, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('job_id',j.id::text,'source_id',e.id::text,'digest',e.digest,'name',e.name,'state',j.state,'kind',COALESCE(i.proposal->>'kind',''),'summary',COALESCE(i.proposal->>'summary',''),'questions',COALESCE(i.proposal->'questions','[]'::jsonb)) ORDER BY j.id),'[]'::jsonb) FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND e.superseded_by IS NULL`, closeID).Scan(&raw)
	return raw, err
}

// The latest ledger capture is pinned alongside the upload jobs, not disguised
// as an invoice job. Its absence is explicit and never means complete coverage.
func ledgerCoverage(ctx context.Context, tx *sql.Tx, closeID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT jsonb_build_object('snapshot_id',s.id::text,'digest',s.digest,'source_id',s.payload->>'source_id','period',s.payload->>'period','captured_at',s.captured_at,'items',s.payload->'coverage','stale',e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active') FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id=$1 AND s.payload ? 'period' ORDER BY s.id DESC LIMIT 1),'null'::jsonb)`, closeID).Scan(&raw)
	return raw, err
}
func scopeSources(inventory []byte) []byte {
	var sources []struct {
		JobID    string `json:"job_id"`
		SourceID string `json:"source_id"`
		Digest   string `json:"digest"`
		Name     string `json:"name"`
	}
	if json.Unmarshal(inventory, &sources) == nil {
		inventory, _ = json.Marshal(sources)
	}
	return inventory
}
func scopeDigest(inventory []byte, coverage json.RawMessage) string {
	inventory = scopeSources(inventory)
	if string(coverage) == "null" {
		return preparation.Digest(inventory)
	}
	return preparation.Digest(append(append([]byte{}, inventory...), coverage...))
}
func registerCoordinator(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/prepare-close", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Close preparation unavailable.")
			return
		}
		defer tx.Rollback()
		raw, err := preparationInventory(r.Context(), tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Evidence inventory unavailable.")
			return
		}
		coverage, err := ledgerCoverage(r.Context(), tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Ledger coverage unavailable.")
			return
		}
		stoppable, stopDigest, err := cancellableJobs(r.Context(), tx, r.PathValue("id"), false)
		if err != nil {
			problem(w, 503, "Could not read stoppable work.")
			return
		}
		recheckable, recheckDigest, err := recheckableJobs(r.Context(), tx, r.PathValue("id"), false)
		if err != nil {
			problem(w, 503, "Could not read recheckable work.")
			return
		}
		var run json.RawMessage
		err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT jsonb_build_object('id',p.id::text,'inventory_digest',p.inventory_digest,'actor',p.actor,'agent',p.agent,'created_at',p.created_at,'items',COALESCE((SELECT jsonb_agg(s || jsonb_build_object('state',COALESCE(j.state,'missing'),'error',COALESCE(j.error,''),'revision',i.revision,'proposal',jsonb_build_object('kind',i.proposal->'kind','questions',COALESCE(NULLIF(i.proposal->'questions','null'::jsonb),'[]'::jsonb)||jsonb_path_query_array(COALESCE(i.proposal,'{}'::jsonb),'$.facts[*].question')),'workpaper_ids',j.workpaper_ids,'stale',e.id IS NULL OR e.superseded_by IS NOT NULL OR e.digest IS DISTINCT FROM s->>'digest','reviews',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',w.id::text,'state',w.review_state,'blockers',w.paper->'blockers')) FROM workpaper_review_state w WHERE w.close_id=p.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text)),'[]'::jsonb)) ORDER BY (s->>'job_id')::bigint) FROM jsonb_array_elements(p.scope) s LEFT JOIN preparation_jobs j ON j.id::text=s->>'job_id' AND j.close_id=p.close_id LEFT JOIN preparation_intakes i ON i.job_id=j.id LEFT JOIN evidence e ON e.id::text=s->>'source_id' AND e.close_id=p.close_id),'[]'::jsonb)) FROM close_preparations p WHERE p.close_id=$1 ORDER BY p.id DESC LIMIT 1),'null'::jsonb)`, r.PathValue("id")).Scan(&run)
		if err != nil {
			problem(w, 503, "Could not read preparation progress.")
			return
		}
		jsonReply(w, 200, map[string]any{"inventory": json.RawMessage(raw), "inventory_digest": scopeDigest(raw, coverage), "coverage": coverage, "run": run, "stop": map[string]any{"count": len(stoppable), "digest": stopDigest}, "recheck": map[string]any{"count": len(recheckable), "digest": recheckDigest}})
	})
	mux.HandleFunc("POST /v1/closes/{id}/prepare-close/recheck", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		var in struct {
			Digest    string `json:"digest"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the selected close's work before rechecking.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Work storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		jobs, digest, err := recheckableJobs(r.Context(), tx, closeID, true)
		if err != nil {
			problem(w, 503, "Could not lock recheckable work.")
			return
		}
		if len(jobs) == 0 || len(jobs) > 100 || digest != in.Digest {
			problem(w, 409, "Work changed. Refresh the preview before rechecking.")
			return
		}
		for _, job := range jobs {
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO recovery_actions(organization_id,close_id,job_id,action,prior_state,actor,reason) VALUES($1,$2,$3,'retry_preparation',$4,$5,'Close-wide recheck')`, organization(r), closeID, job.ID, job.State, subject(r)); err != nil {
				break
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE request_id IN (SELECT id FROM work_requests WHERE job_id=$1) AND used_at IS NULL AND revoked_at IS NULL`, job.ID); err != nil {
				break
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL,agent_attempts=0 WHERE job_id=$1`, job.ID); err != nil {
				break
			}
			if _, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='waiting_evidence',error='',finished_at=NULL WHERE id=$1`, job.ID); err != nil {
				break
			}
		}
		if err != nil {
			jobProblem(w, err, "Recheck could not be confirmed. Refresh before retrying.")
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Recheck could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]any{"count": len(jobs)})
	})
	mux.HandleFunc("POST /v1/closes/{id}/prepare-close/cancel", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Digest    string `json:"digest"`
			Reason    string `json:"reason"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Reason = strings.TrimSpace(in.Reason)
		if !in.Confirmed || len(in.Reason) < 1 || len(in.Reason) > 500 {
			problem(w, 422, "Confirm the stop and give a reason of 1–500 characters.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Work storage unavailable.")
			return
		}
		defer tx.Rollback()
		mailRows, err := tx.QueryContext(r.Context(), `SELECT m.state FROM email_outbox m WHERE m.request_id IN (SELECT q.request_id FROM request_jobs q JOIN preparation_jobs j ON j.id=q.job_id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled')) AND m.state IN ('queued','sending') FOR UPDATE OF m`, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Could not lock approved delivery.")
			return
		}
		sending := false
		for mailRows.Next() {
			var state string
			if err = mailRows.Scan(&state); err != nil {
				break
			}
			sending = sending || state == "sending"
		}
		readErr := mailRows.Err()
		mailRows.Close()
		if err != nil || readErr != nil {
			problem(w, 503, "Could not inspect approved delivery.")
			return
		}
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		jobs, digest, err := cancellableJobs(r.Context(), tx, closeID, true)
		if err != nil {
			problem(w, 503, "Could not lock work.")
			return
		}
		if digest != in.Digest || len(jobs) == 0 {
			problem(w, 409, "Work changed. Refresh the list before stopping it.")
			return
		}
		if sending {
			problem(w, 409, "An approved client email is being sent. Check its outcome, then refresh before stopping remaining work.")
			return
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE email_outbox m SET state='cancelled',error='Close work stopped before delivery.',finished_at=now(),claim=NULL WHERE m.state='queued' AND m.request_id IN (SELECT q.request_id FROM request_jobs q JOIN preparation_jobs j ON j.id=q.job_id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled'))`, closeID)
		if err != nil {
			problem(w, 503, "Could not stop approved delivery.")
			return
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE client_request_links SET revoked_at=now() WHERE revoked_at IS NULL AND used_at IS NULL AND request_id IN (SELECT q.request_id FROM request_jobs q JOIN preparation_jobs j ON j.id=q.job_id JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled'))`, closeID)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL WHERE job_id IN (SELECT j.id FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled'))`, closeID)
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE preparation_jobs SET state='cancelled',error=$2,finished_at=now() WHERE close_id=$1 AND id IN (SELECT job_id FROM preparation_intakes) AND state NOT IN ('completed','cancelled')`, closeID, "Stopped by "+actor+": "+in.Reason)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Could not stop work. Refresh and inspect current status.")
			return
		}
		jsonReply(w, 200, map[string]int{"stopped": len(jobs)})
	})
	mux.HandleFunc("POST /v1/closes/{id}/prepare-close", func(w http.ResponseWriter, r *http.Request) {
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Digest    string `json:"inventory_digest"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the listed evidence scope. Accounting facts and methods require separate approval.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Preparation storage unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		raw, err := preparationInventory(r.Context(), tx, closeID)
		if err != nil {
			problem(w, 503, "Evidence inventory unavailable.")
			return
		}
		coverage, err := ledgerCoverage(r.Context(), tx, closeID)
		if err != nil {
			problem(w, 503, "Ledger coverage unavailable.")
			return
		}
		if scopeDigest(raw, coverage) != in.Digest {
			problemCode(w, 409, "SOURCE_STALE", "Evidence changed. Refresh and inspect the current scope.")
			return
		}
		raw = scopeSources(raw)
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 100 {
			problem(w, 422, "Upload between one and 100 source documents before preparing this close.")
			return
		}
		if string(coverage) != "null" {
			var linked []map[string]any
			if json.Unmarshal(raw, &linked) != nil {
				problem(w, 503, "Invalid scope.")
				return
			}
			var ledger struct {
				SnapshotID string `json:"snapshot_id"`
				SourceID   string `json:"source_id"`
				Digest     string `json:"digest"`
			}
			if json.Unmarshal(coverage, &ledger) != nil {
				problem(w, 503, "Invalid ledger scope.")
				return
			}
			for _, item := range linked {
				item["ledger_coverage"] = ledger
			}
			raw, _ = json.Marshal(linked)
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO close_preparations(close_id,inventory_digest,scope,actor,agent) VALUES($1,$2,$3,$4,NULLIF($5,'')) ON CONFLICT(close_id,inventory_digest) DO UPDATE SET inventory_digest=close_preparations.inventory_digest RETURNING id::text`, closeID, in.Digest, string(raw), actor, auth.Agent(r.Context())).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Preparation could not be confirmed. Retry the same scope safely.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
