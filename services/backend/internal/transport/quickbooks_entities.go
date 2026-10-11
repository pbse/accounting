package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

func registerQuickBooksEntities(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/quickbooks/entities"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(item ORDER BY (item->>'id')::bigint DESC),'[]'::jsonb) FROM (SELECT jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'period',s.period,'state',s.state,'attempts',s.attempts,'error',s.error,'created_at',s.created_at,'finished_at',s.finished_at,'digest',s.digest,'evidence_id',s.evidence_id::text,'counts',s.payload->'counts','started_at',s.payload->'started_at','completeness',s.payload->>'completeness','latest',s.id=(SELECT max(n.id) FROM quickbooks_entity_syncs n WHERE n.close_id=s.close_id AND n.state='completed' AND n.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,n.connection_id))) item FROM quickbooks_entity_syncs s WHERE s.close_id::text=$1 ORDER BY (s.id=(SELECT max(n.id) FROM quickbooks_entity_syncs n WHERE n.close_id=s.close_id AND n.state='completed' AND n.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,n.connection_id))) DESC,s.id DESC LIMIT 20) recent`, r.PathValue("id")).Scan(&raw)
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
		err = tx.QueryRowContext(r.Context(), `SELECT c.period FROM client_closes c JOIN quickbooks_connections x ON x.organization_id=c.organization_id AND client_identity(x.organization_id,x.client_entity)=client_identity(c.organization_id,c.entity) WHERE c.id::text=$1 AND c.organization_id=$2 AND x.id::text=$3 AND x.id=`+quickbooks.ReadingConnection("c.id")+` AND x.state='active' AND x.credential IS NOT NULL FOR UPDATE OF c`, r.PathValue("id"), a.Organization, in.ConnectionID).Scan(&period)
		if err != nil {
			problem(w, 409, "Refresh transactions from this client's current QuickBooks connection; production is used once connected.")
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
 WHERE s.close_id::text=$1 AND s.state='completed' AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.state='completed' AND z.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,z.connection_id))
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
	mux.HandleFunc("GET /v1/closes/{id}/ledger-certification", func(w http.ResponseWriter, r *http.Request) {
		out, err := ledgerCertification(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Ledger certification unavailable.")
			return
		}
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("GET /v1/closes/{id}/quickbooks/exceptions", func(w http.ResponseWriter, r *http.Request) {
		// Balances come from the most recently captured month-end snapshot, QuickBooks or Xero.
		balances := map[string]any{"snapshot": nil, "items": []any{}}
		var snapshotID, xeroID string
		var capturedAt, xeroAt time.Time
		var stale, xeroStale bool
		var accounts, trialBalance, xeroPayload []byte
		// The month's books come from its source: the newest capture, or only QuickBooks' or Xero's.
		source, err := closeBooksSource(r.Context(), db, r.PathValue("id"))
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "The month's books source is unavailable.")
			return
		}
		err = db.QueryRowContext(r.Context(), `SELECT s.id::text,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active',s.payload->'accounts',s.payload->'reports'->'TrialBalance' FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
 WHERE s.close_id::text=$1 AND s.payload ? 'period' AND s.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,s.connection_id) ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&snapshotID, &capturedAt, &stale, &accounts, &trialBalance)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Period snapshot unavailable.")
			return
		}
		if !readsQuickBooks(source) {
			err = sql.ErrNoRows
		}
		xeroErr := sql.ErrNoRows
		if readsXero(source) {
			xeroErr = db.QueryRowContext(r.Context(), `SELECT s.id::text,s.captured_at,x.state<>'active',s.payload FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE s.close_id::text=$1 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, r.PathValue("id")).Scan(&xeroID, &xeroAt, &xeroStale, &xeroPayload)
		}
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
		books, err := latestXeroBooks(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Xero snapshot unavailable.")
			return
		}
		var scanned []quickbooks.TransactionRow
		found := []quickbooks.Exception{}
		var capture map[string]string
		if books != nil {
			// A Xero snapshot carries its own account list; its checks are computed when read.
			month, _ := time.Parse("2006-01-02", books.MonthEnd)
			capture = map[string]string{"id": books.ID, "source": "xero", "finished_at": books.CapturedAt, "unread": books.Unread}
			scanned, accounts = books.Rows, books.Accounts
			found = append(append(found, quickbooks.FindExceptions(scanned)...), xeroWording(quickbooks.OpenItemExceptions(books.Open, month))...)
		} else {
			c, err := latestCapture(r.Context(), db, r.PathValue("id"))
			if err == sql.ErrNoRows {
				jsonReply(w, 200, map[string]any{"capture": nil, "items": []any{}, "balances": balances})
				return
			}
			if err != nil {
				problem(w, 503, "Transaction index unavailable.")
				return
			}
			if scanned, err = quickbooks.CapturedRows(r.Context(), db, c.RowsSync); err != nil {
				problem(w, 503, "Transaction index unavailable.")
				return
			}
			// Stored findings include open invoices and bills; a capture from before they were recorded is scanned now.
			found = append(found, quickbooks.FindExceptions(scanned)...)
			if c.Exceptions != nil && json.Unmarshal(c.Exceptions, &found) != nil {
				problem(w, 503, "Recorded exceptions are unreadable.")
				return
			}
			capture = map[string]string{"id": c.ID, "source": "quickbooks", "evidence_id": c.EvidenceID, "finished_at": c.Finished}
		}
		schedules := map[string]any{"state": "no_directory", "unknown_accounts": []string{}, "item_lines": 0}
		// Without a QuickBooks snapshot there is no account list, so the scan stays "no_directory".
		if candidates, unknown, items, e := quickbooks.ScheduleCandidates(scanned, accounts); e == nil {
			covered, e := scheduleCoverage(r.Context(), db, r.PathValue("id"))
			if e != nil {
				problem(w, 503, "Saved schedules are unreadable.")
				return
			}
			for _, c := range candidates {
				if !covered(c) {
					found = append(found, c)
				}
			}
			schedules["state"] = "checked"
			if len(unknown) > 0 || items > 0 {
				schedules["state"], schedules["unknown_accounts"], schedules["item_lines"] = "incomplete", unknown, items
			}
		}
		jsonReply(w, 200, map[string]any{"capture": capture, "scanned": len(scanned), "items": found, "balances": balances, "schedule_scan": schedules})
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

type capture struct {
	ID, EvidenceID, Finished, RowsSync, HomeCurrency string
	Exceptions                                       []byte
}

// latestCapture is the latest finished capture, as Daily Books reads it; an unchanged one reuses its predecessor's rows.
func latestCapture(ctx context.Context, db *sql.DB, closeID string) (c capture, err error) {
	err = db.QueryRowContext(ctx, `SELECT s.id::text,s.evidence_id::text,s.finished_at::text,s.exceptions,
 COALESCE((SELECT c.id::text FROM quickbooks_entity_syncs c WHERE c.close_id=s.close_id AND c.evidence_id=s.evidence_id AND c.state='completed' ORDER BY c.id DESC LIMIT 1),''),
 COALESCE((SELECT home_currency FROM quickbooks_connections WHERE id=s.connection_id),'')
 FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL
 WHERE s.close_id::text=$1 AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.state IN ('completed','unchanged') AND z.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,z.connection_id))`, closeID).Scan(&c.ID, &c.EvidenceID, &c.Finished, &c.Exceptions, &c.RowsSync, &c.HomeCurrency)
	return c, err
}

// scheduleCoverage reports whether a current schedule cites a candidate's document number with its amount, currency and account.
func scheduleCoverage(ctx context.Context, db *sql.DB, closeID string) (func(quickbooks.Exception) bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT w.paper FROM workpaper_review_state w WHERE w.close_id::text=$1 AND w.kind='prepaid' AND NOT EXISTS (SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text) ORDER BY w.id`, closeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type schedule struct {
		Input prepaids.Input `json:"input"`
	}
	latest := map[string]schedule{}
	for rows.Next() {
		var raw json.RawMessage
		var p schedule
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &p) == nil {
			latest[paperKey("prepaid", raw)] = p
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	kinds := map[string]string{"unscheduled_prepaid": "", "unscheduled_fixed_asset": prepaids.Depreciation, "unscheduled_deferred_revenue": prepaids.DeferredRevenue}
	return func(c quickbooks.Exception) bool {
		if c.Reference == "" {
			return false
		}
		cited := regexp.MustCompile(`(?i)(^|[^a-z0-9-])` + regexp.QuoteMeta(c.Reference) + `([^a-z0-9-]|$)`)
		for _, p := range latest {
			in := p.Input
			account := strings.ToLower(strings.TrimSpace(in.PrepaidAccount))
			if in.Schedule != kinds[c.Type] || !sameAmount(in.Amount, c.Amount) || c.Currency == "" || !strings.EqualFold(in.Currency, c.Currency) || !cited.MatchString(in.SourceReference) && !cited.MatchString(in.BookingReference) {
				continue
			}
			if name := strings.ToLower(c.Account); c.Type == "unscheduled_fixed_asset" || account == name || strings.HasSuffix(account, ":"+name) {
				return true
			}
		}
		return false
	}, nil
}

// ledgerCertification compares the close's newest period snapshot's TransactionList with its newest transaction capture
// of the same month and company. Agreement is between two stored, sequential reads, not an atomic ledger copy.
func ledgerCertification(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, closeID string) (map[string]any, error) {
	var snapshotID, snapshotRealm string
	var snapshotAt time.Time
	var report []byte
	err := q.QueryRowContext(ctx, `SELECT s.id::text,s.realm,s.captured_at,s.payload->'reports'->'TransactionList' FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id AND e.superseded_by IS NULL JOIN quickbooks_connections x ON x.id=s.connection_id AND x.state='active'
 WHERE s.close_id::text=$1 AND s.payload->>'period'=c.period AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, closeID).Scan(&snapshotID, &snapshotRealm, &snapshotAt, &report)
	if err == sql.ErrNoRows || err == nil && len(report) == 0 {
		return map[string]any{"status": "unavailable", "reason": "No current QuickBooks period reports for this month."}, nil
	}
	if err != nil {
		return nil, err
	}
	var captureID, rowsSync, realm string
	var finished time.Time
	var counts []byte
	var provisional bool
	err = q.QueryRowContext(ctx, `SELECT s.id::text,COALESCE((SELECT z.id::text FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.connection_id=s.connection_id AND z.period=s.period AND z.evidence_id=s.evidence_id AND z.state='completed' ORDER BY z.id DESC LIMIT 1),''),x.realm,s.finished_at,COALESCE(s.payload->'counts','{}'::jsonb),
 EXISTS(SELECT 1 FROM quickbooks_entity_syncs n WHERE n.close_id=s.close_id AND n.connection_id=s.connection_id AND n.period=s.period AND n.id>s.id AND n.state IN ('queued','running','failed'))
 FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id JOIN quickbooks_connections x ON x.id=s.connection_id JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL
 WHERE s.close_id::text=$1 AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z JOIN client_closes zc ON zc.id=z.close_id WHERE z.close_id=s.close_id AND z.period=zc.period AND z.state IN ('completed','unchanged') AND z.connection_id=`+quickbooks.ReadingConnection("s.close_id")+`)`, closeID).Scan(&captureID, &rowsSync, &realm, &finished, &counts, &provisional)
	if err == sql.ErrNoRows {
		return map[string]any{"status": "unavailable", "reason": "No current QuickBooks transaction capture for this month."}, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]any{"status": "unavailable", "provisional": provisional, "snapshot_id": snapshotID, "snapshot_captured_at": snapshotAt, "capture_id": captureID, "capture_finished_at": finished}
	if realm != snapshotRealm {
		out["reason"] = "The period reports and the transaction capture are of different QuickBooks companies."
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT kind,provider_id FROM quickbooks_transaction_rows WHERE sync_id::text=$1`, rowsSync)
	if err != nil {
		return nil, err
	}
	captured := map[string][]string{}
	for rows.Next() {
		var kind, id string
		if err = rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return nil, err
		}
		captured[kind] = append(captured[kind], id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var count map[string]int
	json.Unmarshal(counts, &count)
	certification, err := quickbooks.Certify(report, count, captured)
	if err != nil {
		out["reason"] = err.Error()
		return out, nil
	}
	out["status"], out["certification"] = "differs", certification
	if certification.Agrees {
		out["status"] = "agrees"
	}
	return out, nil
}

// certificationSentence says in one sentence what a ledger certification found.
func certificationSentence(out map[string]any) string {
	c, _ := out["certification"].(quickbooks.Certification)
	var text string
	switch out["status"] {
	case "agrees":
		text = fmt.Sprintf("The stored reads agree: all %d posting transactions in the TransactionList report were captured.", c.Reported)
	case "differs":
		text = fmt.Sprintf("The stored reads differ: %d reported transactions were not captured, %d captured ones are not reported and %d are of types the capture does not read.", len(c.Missing), len(c.Extra), len(c.Unsupported))
	default:
		text, _ = out["reason"].(string)
		text = "The ledger is not certified: " + strings.TrimSuffix(text, ".") + "."
	}
	if provisional, _ := out["provisional"].(bool); provisional {
		text += " A newer capture has not finished, so this is provisional."
	} else if out["status"] == "differs" {
		text += " Capture the transactions and period reports again."
	}
	return text
}
