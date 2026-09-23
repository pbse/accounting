package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"

	"close.local/backend/internal/auth"
)

func registerQuickBooksEntities(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/quickbooks/entities"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(item ORDER BY (item->>'id')::bigint DESC),'[]'::jsonb) FROM (SELECT jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'period',s.period,'state',s.state,'attempts',s.attempts,'error',s.error,'created_at',s.created_at,'finished_at',s.finished_at,'digest',s.digest,'evidence_id',s.evidence_id::text,'counts',e.content::jsonb->'counts') item FROM quickbooks_entity_syncs s LEFT JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id WHERE s.close_id::text=$1 ORDER BY s.id DESC LIMIT 20) recent`, r.PathValue("id")).Scan(&raw)
		if err != nil {
			problem(w, 503, "Transaction capture status unavailable.")
			return
		}
		jsonReply(w, 200, json.RawMessage(raw))
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "integrations:manage") {
			problem(w, 403, "A hosted client-authorized session with integrations:manage is required.")
			return
		}
		var in struct {
			ConnectionID string `json:"connection_id"`
			Confirmed    bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed || in.ConnectionID == "" {
			problem(w, 422, "Confirm the connected company and this close month.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Transaction refresh storage unavailable.")
			return
		}
		defer tx.Rollback()
		var period string
		err = tx.QueryRowContext(r.Context(), `SELECT c.period FROM client_closes c JOIN quickbooks_connections x ON x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) WHERE c.id::text=$1 AND c.organization_id=$2 AND x.id::text=$3 AND x.environment='sandbox' AND x.state='active' AND x.credential IS NOT NULL FOR UPDATE OF c`, r.PathValue("id"), a.Organization, in.ConnectionID).Scan(&period)
		if err != nil {
			problem(w, 409, "Confirm an active sandbox connection for this client before refreshing transactions.")
			return
		}
		var activeID string
		err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND connection_id::text=$2 AND period=$3 AND state IN ('queued','running')`, r.PathValue("id"), in.ConnectionID, period).Scan(&activeID)
		if err == nil {
			jsonReply(w, 202, map[string]string{"job_id": activeID})
			return
		}
		if err != sql.ErrNoRows {
			problem(w, 503, "Transaction refresh status unavailable.")
			return
		}
		var count int
		if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM quickbooks_entity_syncs WHERE close_id::text=$1`, r.PathValue("id")).Scan(&count); err != nil || count >= 20 {
			problem(w, 413, "This close has reached 20 transaction captures. Review its history before requesting more.")
			return
		}
		var id string
		err = tx.QueryRowContext(r.Context(), `INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by) VALUES($1,$2,$3,$4) ON CONFLICT(close_id,connection_id,period) WHERE state IN ('queued','running') DO UPDATE SET requested_by=quickbooks_entity_syncs.requested_by RETURNING id::text`, r.PathValue("id"), in.ConnectionID, period, a.Subject).Scan(&id)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Transaction refresh could not be queued. Refresh status before retrying.")
			return
		}
		jsonReply(w, 202, map[string]string{"job_id": id})
	})
	mux.HandleFunc("GET "+path+"/{sync}", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT payload FROM quickbooks_entity_syncs WHERE id::text=$1 AND close_id::text=$2 AND state='completed'`, r.PathValue("sync"), r.PathValue("id")).Scan(&raw)
		if err != nil {
			problem(w, 404, "Completed transaction capture unavailable in this close.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="quickbooks-transactions.json"`)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(raw)
	})
}
