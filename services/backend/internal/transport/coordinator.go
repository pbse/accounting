package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"close.local/backend/internal/preparation"
)

// Reuse the upload-owned durable jobs. The parent fixes the evidence scope;
// progress is read from those jobs rather than stored in a second status cache.
func preparationInventory(ctx context.Context, tx *sql.Tx, closeID string) ([]byte, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('job_id',j.id::text,'source_id',e.id::text,'digest',e.digest,'name',e.name) ORDER BY j.id),'[]'::jsonb) FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=$1 AND e.superseded_by IS NULL`, closeID).Scan(&raw)
	return raw, err
}

// The latest ledger capture is pinned alongside the upload jobs, not disguised
// as an invoice job. Its absence is explicit and never means complete coverage.
func ledgerCoverage(ctx context.Context, tx *sql.Tx, closeID string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT jsonb_build_object('snapshot_id',s.id::text,'digest',s.digest,'source_id',s.payload->>'source_id','period',s.payload->>'period','captured_at',s.captured_at,'items',s.payload->'coverage','stale',e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active') FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id=$1 AND s.payload ? 'period' ORDER BY s.id DESC LIMIT 1),'null'::jsonb)`, closeID).Scan(&raw)
	return raw, err
}
func scopeDigest(inventory []byte, coverage json.RawMessage) string {
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
		var run json.RawMessage
		err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT jsonb_build_object('id',p.id::text,'inventory_digest',p.inventory_digest,'actor',p.actor,'created_at',p.created_at,'items',COALESCE((SELECT jsonb_agg(s || jsonb_build_object('state',COALESCE(j.state,'missing'),'error',COALESCE(j.error,''),'revision',i.revision,'proposal',jsonb_build_object('kind',i.proposal->'kind','questions',COALESCE(NULLIF(i.proposal->'questions','null'::jsonb),'[]'::jsonb)||jsonb_path_query_array(COALESCE(i.proposal,'{}'::jsonb),'$.facts[*].question')),'workpaper_ids',j.workpaper_ids,'stale',e.id IS NULL OR e.superseded_by IS NOT NULL OR e.digest IS DISTINCT FROM s->>'digest','reviews',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',w.id::text,'state',w.review_state,'blockers',w.paper->'blockers')) FROM workpaper_review_state w WHERE w.close_id=p.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text)),'[]'::jsonb)) ORDER BY (s->>'job_id')::bigint) FROM jsonb_array_elements(p.scope) s LEFT JOIN preparation_jobs j ON j.id::text=s->>'job_id' AND j.close_id=p.close_id LEFT JOIN preparation_intakes i ON i.job_id=j.id LEFT JOIN evidence e ON e.id::text=s->>'source_id' AND e.close_id=p.close_id),'[]'::jsonb)) FROM close_preparations p WHERE p.close_id=$1 ORDER BY p.id DESC LIMIT 1),'null'::jsonb)`, r.PathValue("id")).Scan(&run)
		if err != nil {
			problem(w, 503, "Could not read preparation progress.")
			return
		}
		jsonReply(w, 200, map[string]any{"inventory": json.RawMessage(raw), "inventory_digest": scopeDigest(raw, coverage), "coverage": coverage, "run": run})
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
			problem(w, 409, "Evidence changed. Refresh and inspect the current scope.")
			return
		}
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
		err = tx.QueryRowContext(r.Context(), `INSERT INTO close_preparations(close_id,inventory_digest,scope,actor) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,inventory_digest) DO UPDATE SET inventory_digest=close_preparations.inventory_digest RETURNING id::text`, closeID, in.Digest, string(raw), actor).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Preparation could not be confirmed. Retry the same scope safely.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
