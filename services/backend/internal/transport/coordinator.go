package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

type cancellableJob struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	State    string `json:"state"`
	Revision int    `json:"revision"`
}

func cancellableJobs(ctx context.Context, tx *sql.Tx, closeID string, lock bool) ([]cancellableJob, string, error) {
	query := `SELECT j.id::text,e.name,j.state,i.revision FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled') ORDER BY j.id`
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
		if err = rows.Scan(&job.ID, &job.Name, &job.State, &job.Revision); err != nil {
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
	query := `SELECT j.id::text,e.name,j.state,i.revision FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND j.state IN ('failed','needs_confirmation') AND e.superseded_by IS NULL AND e.extraction_state='ready' ORDER BY j.id`
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
		if err = rows.Scan(&job.ID, &job.Name, &job.State, &job.Revision); err != nil {
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
//
// The capture is the books', chosen as the tie-out chooses them: the latest QuickBooks period capture (by capture time)
// of the connection the books are read from (quickbooksReading), unless the close's latest Xero capture is newer. A QuickBooks capture reads exactly as it was stored, so its JSON, and the
// sign-offs whose digests include it, never change. A Xero capture's coverage is proposed from it on read
// (xero.Coverage), so every Xero capture has one; it is named "xero:<id>" (snapshot IDs of the two providers would
// otherwise collide) and is stale once its connection is no longer active, when it is of another month than the
// close's, or when it cannot be read.
func ledgerCoverage(ctx context.Context, tx *sql.Tx, closeID string) (json.RawMessage, error) {
	var raw json.RawMessage
	var quickbooksAt sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT jsonb_build_object('snapshot_id',s.id::text,'digest',s.digest,'source_id',s.payload->>'source_id','period',s.payload->>'period','captured_at',s.captured_at,'items',s.payload->'coverage','stale',e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active') FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id=$1 AND s.payload ? 'period' AND `+quickbooksReading+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1),'null'::jsonb),
 (SELECT s.captured_at FROM quickbooks_snapshots s WHERE s.close_id=$1 AND s.payload ? 'period' AND `+quickbooksReading+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1)`, closeID).Scan(&raw, &quickbooksAt)
	if err != nil {
		return raw, err
	}
	var id, digest, period string
	var at time.Time
	var stale bool
	var payload []byte
	err = tx.QueryRowContext(ctx, `SELECT s.id::text,s.digest,s.period,s.captured_at,x.state<>'active' OR s.period<>c.period,jsonb_build_object('accounts',s.payload->'accounts','trial_balance',s.payload->'trial_balance')
 FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id JOIN client_closes c ON c.id=s.close_id WHERE s.close_id=$1 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, closeID).Scan(&id, &digest, &period, &at, &stale, &payload)
	if err == sql.ErrNoRows || (err == nil && quickbooksAt.Valid && !at.After(quickbooksAt.Time)) {
		return raw, nil
	}
	if err != nil {
		return nil, err
	}
	// A capture it cannot read proposes nothing it could be taken to cover: one item, stale, that only capturing the
	// month again resolves.
	items, err := xero.Coverage(payload)
	if err != nil {
		stale = true
		items = []quickbooks.CoverageItem{{Name: "the Xero capture", Kind: "accountant_review", Reason: "The Xero capture cannot be read: " + err.Error() + ".", Question: "Capture the month from Xero again."}}
	}
	return json.Marshal(map[string]any{"source": "xero", "snapshot_id": xeroCapture + id, "digest": digest, "source_id": "", "period": period, "captured_at": at.UTC(), "items": items, "stale": stale})
}

// quickbooksReading is SQL limiting QuickBooks snapshot s to the connection the books are read from, as the tie-out does:
// the production company unless disconnected, else the sandbox one, or any while the client has none. For a client with
// one connection it chooses what it always did.
const quickbooksReading = `s.connection_id=COALESCE(` + quickbooks.ReadingConnectionBefore + `s.close_id` + quickbooks.ReadingConnectionAfter + `,s.connection_id)`

// xeroCapture prefixes a Xero capture's ID wherever coverage names its capture.
const xeroCapture = "xero:"

// bindingCapture is SQL naming the capture of coverage binding (or hand-off) t as coverage names it: a QuickBooks
// snapshot's ID, or "xero:" and a Xero snapshot's.
func bindingCapture(t string) string {
	return `COALESCE('` + xeroCapture + `'||` + t + `.xero_snapshot_id,` + t + `.snapshot_id::text)`
}

// captureColumns is SQL for the snapshot_id and xero_snapshot_id values of the capture coverage names in parameter p.
func captureColumns(p string) string {
	return `CASE WHEN starts_with(` + p + `::text,'` + xeroCapture + `') THEN NULL ELSE NULLIF(` + p + `::text,'')::bigint END,CASE WHEN starts_with(` + p + `::text,'` + xeroCapture + `') THEN substr(` + p + `::text,` + "6" + `)::bigint END`
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
		jsonReply(w, 200, map[string]any{"inventory": json.RawMessage(raw), "inventory_digest": scopeDigest(raw, coverage), "coverage": coverage, "run": run, "stop": map[string]any{"count": len(stoppable), "digest": stopDigest, "jobs": stoppable}, "recheck": map[string]any{"count": len(recheckable), "digest": recheckDigest, "jobs": recheckable}})
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
		count, refused := recheckClose(r.Context(), tx, organization(r), subject(r), r.PathValue("id"), in.Digest)
		if refused != nil {
			refused.write(w)
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Recheck could not be confirmed. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]any{"count": count})
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
		stopped, refused := stopClose(r.Context(), tx, r.PathValue("id"), in.Digest, in.Reason, actor)
		if refused != nil {
			refused.write(w)
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Could not stop work. Refresh and inspect current status.")
			return
		}
		jsonReply(w, 200, map[string]int{"stopped": stopped})
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
		id, created, refused := prepareClose(r.Context(), tx, r.PathValue("id"), in.Digest, actor, auth.Agent(r.Context()))
		if refused != nil {
			refused.write(w)
			return
		}
		if tx.Commit() != nil {
			problem(w, 503, "Preparation could not be confirmed. Retry the same scope safely.")
			return
		}
		// created is false when the same scope was already confirmed and this returns that run.
		jsonReply(w, 200, map[string]any{"id": id, "created": created})
	})
}

// closeActionError is a refused close action: the status, optional code and message its route replies with.
type closeActionError struct {
	status  int
	code    string
	message string
}

func closeRefusal(status int, message string) *closeActionError {
	return &closeActionError{status, "", message}
}

func jobRefusal(err error, message string) *closeActionError {
	if err != nil && strings.Contains(err.Error(), "Firm active job limit reached") {
		return closeRefusal(429, "This firm has reached its active preparation limit. Resolve or cancel existing work before starting more.")
	}
	return closeRefusal(503, message)
}

func (e *closeActionError) write(w http.ResponseWriter) {
	if e.code != "" {
		problemCode(w, e.status, e.code, e.message)
		return
	}
	problem(w, e.status, e.message)
}

// recheckClose, stopClose and prepareClose are the close page's actions inside the caller's transaction, so a
// portfolio run can record each close's result in the same commit.
func recheckClose(ctx context.Context, tx *sql.Tx, org, subject, closeParam, confirmed string) (int, *closeActionError) {
	var err error
	var closeID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeParam).Scan(&closeID); err != nil {
		return 0, closeRefusal(404, "Close not found.")
	}
	jobs, digest, err := recheckableJobs(ctx, tx, closeID, true)
	if err != nil {
		return 0, closeRefusal(503, "Could not lock recheckable work.")
	}
	if len(jobs) == 0 || len(jobs) > 100 || digest != confirmed {
		return 0, closeRefusal(409, "Work changed. Refresh the preview before rechecking.")
	}
	for _, job := range jobs {
		if _, err = tx.ExecContext(ctx, `INSERT INTO recovery_actions(organization_id,close_id,job_id,action,prior_state,actor,reason) VALUES($1,$2,$3,'retry_preparation',$4,$5,'Close-wide recheck')`, org, closeID, job.ID, job.State, subject); err != nil {
			break
		}
		if _, err = tx.ExecContext(ctx, `UPDATE client_request_links SET revoked_at=now() WHERE request_id IN (SELECT id FROM work_requests WHERE job_id=$1) AND revoked_at IS NULL`, job.ID); err != nil {
			break
		}
		if _, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL,agent_attempts=0 WHERE job_id=$1`, job.ID); err != nil {
			break
		}
		if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='waiting_evidence',error='',finished_at=NULL WHERE id=$1`, job.ID); err != nil {
			break
		}
	}
	if err != nil {
		return 0, jobRefusal(err, "Recheck could not be confirmed. Refresh before retrying.")
	}
	return len(jobs), nil
}

func stopClose(ctx context.Context, tx *sql.Tx, closeParam, confirmed, reason, actor string) (int, *closeActionError) {
	mailRows, err := tx.QueryContext(ctx, `SELECT m.state FROM email_outbox m WHERE m.request_id IN (SELECT q.request_id FROM request_jobs q JOIN preparation_jobs j ON j.id=q.job_id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled')) AND m.state IN ('queued','sending') FOR UPDATE OF m`, closeParam)
	if err != nil {
		return 0, closeRefusal(503, "Could not lock approved delivery.")
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
		return 0, closeRefusal(503, "Could not inspect approved delivery.")
	}
	var closeID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeParam).Scan(&closeID); err != nil {
		return 0, closeRefusal(404, "Close not found.")
	}
	jobs, digest, err := cancellableJobs(ctx, tx, closeID, true)
	if err != nil {
		return 0, closeRefusal(503, "Could not lock work.")
	}
	if digest != confirmed || len(jobs) == 0 {
		return 0, closeRefusal(409, "Work changed. Refresh the list before stopping it.")
	}
	if sending {
		return 0, closeRefusal(409, "An approved client email is being sent. Check its outcome, then refresh before stopping remaining work.")
	}
	_, err = tx.ExecContext(ctx, `UPDATE email_outbox m SET state='cancelled',error='Close work stopped before delivery.',finished_at=now(),claim=NULL WHERE m.state='queued' AND m.request_id IN (SELECT q.request_id FROM request_jobs q JOIN preparation_jobs j ON j.id=q.job_id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled'))`, closeID)
	if err != nil {
		return 0, closeRefusal(503, "Could not stop approved delivery.")
	}
	_, err = tx.ExecContext(ctx, `UPDATE client_request_links SET revoked_at=now() WHERE revoked_at IS NULL AND request_id IN (SELECT q.request_id FROM request_jobs q JOIN preparation_jobs j ON j.id=q.job_id JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled'))`, closeID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL WHERE job_id IN (SELECT j.id FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=$1 AND j.state NOT IN ('completed','cancelled'))`, closeID)
	}
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='cancelled',error=$2,finished_at=now() WHERE close_id=$1 AND id IN (SELECT job_id FROM preparation_intakes) AND state NOT IN ('completed','cancelled')`, closeID, "Stopped by "+actor+": "+reason)
	}
	if err != nil {
		return 0, closeRefusal(503, "Could not stop work. Refresh and inspect current status.")
	}
	return len(jobs), nil
}

func prepareClose(ctx context.Context, tx *sql.Tx, closeParam, confirmed, actor, agent string) (string, bool, *closeActionError) {
	var err error
	var closeID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeParam).Scan(&closeID); err != nil {
		return "", false, closeRefusal(404, "Close not found.")
	}
	raw, err := preparationInventory(ctx, tx, closeID)
	if err != nil {
		return "", false, closeRefusal(503, "Evidence inventory unavailable.")
	}
	coverage, err := ledgerCoverage(ctx, tx, closeID)
	if err != nil {
		return "", false, closeRefusal(503, "Ledger coverage unavailable.")
	}
	if scopeDigest(raw, coverage) != confirmed {
		return "", false, &closeActionError{409, "SOURCE_STALE", "Evidence changed. Refresh and inspect the current scope."}
	}
	raw = scopeSources(raw)
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil || len(items) == 0 || len(items) > 100 {
		return "", false, closeRefusal(422, "Upload between one and 100 source documents before preparing this close.")
	}
	if string(coverage) != "null" {
		var linked []map[string]any
		if json.Unmarshal(raw, &linked) != nil {
			return "", false, closeRefusal(503, "Invalid scope.")
		}
		var ledger struct {
			SnapshotID string `json:"snapshot_id"`
			SourceID   string `json:"source_id"`
			Digest     string `json:"digest"`
		}
		if json.Unmarshal(coverage, &ledger) != nil {
			return "", false, closeRefusal(503, "Invalid ledger scope.")
		}
		for _, item := range linked {
			item["ledger_coverage"] = ledger
		}
		raw, _ = json.Marshal(linked)
	}
	var id string
	var created bool
	err = tx.QueryRowContext(ctx, `INSERT INTO close_preparations(close_id,inventory_digest,scope,actor,agent) VALUES($1,$2,$3,$4,NULLIF($5,'')) ON CONFLICT(close_id,inventory_digest) DO UPDATE SET inventory_digest=close_preparations.inventory_digest RETURNING id::text,xmax::text='0'`, closeID, confirmed, string(raw), actor, agent).Scan(&id, &created)
	if err != nil {
		return "", false, closeRefusal(503, "Preparation could not be confirmed. Retry the same scope safely.")
	}
	return id, created, nil
}
