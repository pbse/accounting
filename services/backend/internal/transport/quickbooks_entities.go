package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

func registerQuickBooksEntities(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/quickbooks/entities"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(item ORDER BY (item->>'id')::bigint DESC),'[]'::jsonb) FROM (SELECT jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'period',s.period,'state',s.state,'attempts',s.attempts,'error',s.error,'created_at',s.created_at,'finished_at',s.finished_at,'digest',s.digest,'evidence_id',s.evidence_id::text,'counts',s.payload->'counts','started_at',s.payload->'started_at','completeness',s.payload->>'completeness','latest',s.id=(SELECT max(n.id) FROM quickbooks_entity_syncs n WHERE n.close_id=s.close_id AND n.state='completed')) item FROM quickbooks_entity_syncs s WHERE s.close_id::text=$1 ORDER BY (s.id=(SELECT max(n.id) FROM quickbooks_entity_syncs n WHERE n.close_id=s.close_id AND n.state='completed')) DESC,s.id DESC LIMIT 20) recent`, r.PathValue("id")).Scan(&raw)
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
		if err := tx.QueryRowContext(r.Context(), `SELECT count(*) FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND created_at>now()-interval '24 hours'`, r.PathValue("id")).Scan(&count); err != nil || count >= 20 {
			problem(w, 429, "This close has reached 20 transaction checks today. Try again tomorrow.")
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
	mux.HandleFunc("GET "+path+"/transactions", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if len(kind) > 40 {
			problem(w, 422, "Choose a supported transaction type.")
			return
		}
		offset := 0
		if r.URL.Query().Has("offset") {
			var err error
			offset, err = strconv.Atoi(r.URL.Query().Get("offset"))
			if err != nil || offset < 0 || offset > 30000 {
				problem(w, 422, "Transaction offset must be between 0 and 30000.")
				return
			}
		}
		rows, err := db.QueryContext(r.Context(), `SELECT t.kind,t.provider_id,t.revision,t.txn_date::text,t.provider_updated_at::text,COALESCE(t.currency,''),COALESCE(t.amount,''),t.record,s.id::text,s.evidence_id::text
 FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL
 JOIN quickbooks_transaction_rows t ON t.sync_id=s.id
 WHERE s.close_id::text=$1 AND s.state='completed' AND s.id=(SELECT max(id) FROM quickbooks_entity_syncs WHERE close_id=s.close_id AND state='completed')
 AND ($2='' OR t.kind=$2) ORDER BY t.txn_date DESC,t.kind,t.provider_id LIMIT 101 OFFSET $3`, r.PathValue("id"), kind, offset)
		if err != nil {
			problem(w, 503, "Transaction index unavailable.")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		more := false
		for rows.Next() {
			var name, providerID, revision, date, updated, currency, amount, captureID, evidenceID string
			var record []byte
			if err = rows.Scan(&name, &providerID, &revision, &date, &updated, &currency, &amount, &record, &captureID, &evidenceID); err != nil {
				break
			}
			if len(items) == 100 {
				more = true
				break
			}
			items = append(items, map[string]any{"kind": name, "provider_id": providerID, "revision": revision, "txn_date": date, "provider_updated_at": updated, "currency": currency, "amount": amount, "record": json.RawMessage(record), "capture_id": captureID, "evidence_id": evidenceID})
		}
		if err != nil || rows.Err() != nil {
			problem(w, 503, "Transaction index unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"items": items, "has_more": more})
	})
	mux.HandleFunc("GET /v1/closes/{id}/quickbooks/exceptions", func(w http.ResponseWriter, r *http.Request) {
		// Balances come from the most recently captured month-end snapshot, QuickBooks or Xero.
		balances := map[string]any{"snapshot": nil, "items": []any{}}
		var snapshotID, xeroID string
		var capturedAt, xeroAt time.Time
		var stale, xeroStale bool
		var accounts, trialBalance, xeroPayload []byte
		err := db.QueryRowContext(r.Context(), `SELECT s.id::text,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active',s.payload->'accounts',s.payload->'reports'->'TrialBalance' FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
 WHERE s.close_id::text=$1 AND s.payload ? 'period' ORDER BY s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&snapshotID, &capturedAt, &stale, &accounts, &trialBalance)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Period snapshot unavailable.")
			return
		}
		xeroErr := db.QueryRowContext(r.Context(), `SELECT s.id::text,s.captured_at,x.state<>'active',s.payload FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE s.close_id::text=$1 ORDER BY s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&xeroID, &xeroAt, &xeroStale, &xeroPayload)
		if xeroErr != nil && xeroErr != sql.ErrNoRows {
			problem(w, 503, "Xero snapshot unavailable.")
			return
		}
		var items any
		var checkErr error
		switch {
		case xeroErr == nil && (err == sql.ErrNoRows || xeroAt.After(capturedAt)):
			balances["snapshot"] = map[string]any{"id": xeroID, "captured_at": xeroAt, "stale": xeroStale, "source": "xero"}
			_, items, checkErr = xero.WrongSide(xeroPayload)
		case err == nil:
			balances["snapshot"] = map[string]any{"id": snapshotID, "captured_at": capturedAt, "stale": stale, "source": "quickbooks"}
			items, checkErr = quickbooks.AbnormalBalances(accounts, trialBalance)
		}
		if checkErr != nil {
			balances["error"] = checkErr.Error()
		} else if items != nil {
			balances["items"] = items
		}
		var captureID, evidenceID, finished string
		err = db.QueryRowContext(r.Context(), `SELECT s.id::text,s.evidence_id::text,s.finished_at::text FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL
 WHERE s.close_id::text=$1 AND s.state='completed' AND s.id=(SELECT max(id) FROM quickbooks_entity_syncs WHERE close_id=s.close_id AND state='completed')`, r.PathValue("id")).Scan(&captureID, &evidenceID, &finished)
		if err == sql.ErrNoRows {
			jsonReply(w, 200, map[string]any{"capture": nil, "items": []any{}, "balances": balances})
			return
		}
		if err != nil {
			problem(w, 503, "Transaction index unavailable.")
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT kind,provider_id,txn_date::text,COALESCE(currency,''),COALESCE(amount,''),record FROM quickbooks_transaction_rows WHERE sync_id=$1`, captureID)
		if err != nil {
			problem(w, 503, "Transaction index unavailable.")
			return
		}
		defer rows.Close()
		scanned := []quickbooks.TransactionRow{}
		for rows.Next() {
			var row quickbooks.TransactionRow
			if err = rows.Scan(&row.Kind, &row.ProviderID, &row.Date, &row.Currency, &row.Amount, &row.Record); err != nil {
				break
			}
			scanned = append(scanned, row)
		}
		if err != nil || rows.Err() != nil {
			problem(w, 503, "Transaction index unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"capture": map[string]string{"id": captureID, "evidence_id": evidenceID, "finished_at": finished}, "scanned": len(scanned), "items": quickbooks.FindExceptions(scanned), "balances": balances})
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
