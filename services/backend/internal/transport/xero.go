package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/secrets"
	"close.local/backend/internal/xero"
)

// xeroReturn sends the browser back to the close that started the sign-in; the close then
// posts the code, so the exchange runs under that accountant's own session.
func xeroReturn(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	q := r.URL.Query()
	if len(q.Get("state")) > 100 || len(q.Get("code")) > 4096 || q.Get("code") == "" {
		problem(w, 422, "Xero sign-in was cancelled or invalid. Return to your close and connect again.")
		return
	}
	var closeID string
	err := db.QueryRowContext(r.Context(), `SELECT x.close_id::text FROM xero_connections x JOIN organizations o ON o.id=x.organization_id WHERE x.oauth_digest=$1 AND x.state='authorizing' AND x.oauth_expires_at>now() AND o.enabled`, preparation.Digest([]byte(q.Get("state")))).Scan(&closeID)
	if err != nil {
		problem(w, 410, "Connection attempt expired or unavailable. Start again from your close.")
		return
	}
	origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" {
		problem(w, 503, "Configure the exact HTTPS application origin.")
		return
	}
	// The Xero panel lives on the Evidence tab.
	origin.Path, origin.RawQuery = "/dashboard/closes/"+closeID, "tab=evidence"
	origin.Fragment = url.Values{"xero_code": {q.Get("code")}, "xero_state": {q.Get("state")}}.Encode()
	http.Redirect(w, r, origin.String(), http.StatusSeeOther)
}

func registerXero(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/xero"
	status := func(ctx context.Context, closeID string) (map[string]any, error) {
		var connection, snapshots json.RawMessage
		var latest []byte
		err := db.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT jsonb_build_object('id',x.id::text,'state',x.state,'tenant_name',x.tenant_name,'tenants',CASE WHEN x.state='choose_tenant' THEN x.tenants ELSE '[]'::jsonb END,'confirmed_at',x.confirmed_at,'error',x.error) FROM xero_connections x WHERE x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.state<>'disconnected'),'null'::jsonb),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',s.id::text,'period',s.period,'captured_at',s.captured_at,'digest',s.digest,'tenant_id',s.tenant_id) ORDER BY s.id DESC) FROM xero_snapshots s WHERE s.close_id=c.id),'[]'::jsonb),
 (SELECT s.payload FROM xero_snapshots s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1)
 FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&connection, &snapshots, &latest)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"configured": xero.Configured(), "connection": connection, "snapshots": snapshots, "latest": nil}
		if latest != nil {
			currency, date, rows, e := xero.Balances(latest)
			if e != nil {
				out["latest"] = map[string]any{"error": e.Error()}
			} else {
				// How many of each transaction type the snapshot holds, or why it holds none.
				var read struct {
					Transactions *struct {
						Records map[string][]json.RawMessage `json:"records"`
					} `json:"transactions"`
					Unread string `json:"transactions_error"`
				}
				json.Unmarshal(latest, &read)
				transactions := map[string]any{"unread": read.Unread}
				if read.Transactions != nil {
					counts := map[string]int{}
					for kind, records := range read.Transactions.Records {
						counts[kind] = len(records)
					}
					transactions["counts"] = counts
				}
				out["latest"] = map[string]any{"currency": currency, "date": date, "rows": rows, "transactions": transactions}
			}
		}
		return out, nil
	}
	mux.HandleFunc("GET /v1/closes/{id}/native-reconciliation", func(w http.ResponseWriter, r *http.Request) {
		out, err := nativeReconciliation(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Native reconciliation status unavailable.")
			return
		}
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		out, err := status(r.Context(), r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Xero status unavailable.")
			return
		}
		out["hosted"] = auth.Actor(r.Context()).Organization != ""
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("GET "+path+"/snapshots/{snapshot}", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if err := db.QueryRowContext(r.Context(), `SELECT payload FROM xero_snapshots WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("snapshot"), r.PathValue("id")).Scan(&raw); err != nil {
			problem(w, 404, "Snapshot not found in this close.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="xero-trial-balance.json"`)
		w.Write(raw)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "integrations:manage") {
			problem(w, 403, "A hosted session with integrations:manage is required.")
			return
		}
		var in struct {
			Action    string `json:"action"`
			ID        string `json:"connection_id"`
			Confirmed bool   `json:"confirmed"`
			Code      string `json:"code"`
			State     string `json:"state"`
			TenantID  string `json:"tenant_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm this connection action and client.")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		var entity, period string
		if err := db.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2`, r.PathValue("id"), a.Organization).Scan(&entity, &period); err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		if in.Action == "connect" {
			if !xero.Configured() {
				problem(w, 503, "Configure the Xero app first.")
				return
			}
			if _, err := secrets.Seal(a.Organization+"/xero-check", nil); err != nil {
				problem(w, 503, err.Error())
				return
			}
			state := rand.Text()
			var id string
			if err := db.QueryRowContext(ctx, `INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at) VALUES($1,$2,$3,$4,'authorizing',$5,now()+interval '10 minutes') ON CONFLICT DO NOTHING RETURNING id::text`, r.PathValue("id"), a.Organization, entity, a.Subject, preparation.Digest([]byte(state))).Scan(&id); err != nil {
				problem(w, 409, "A Xero connection or attempt already exists for this client. Disconnect it before connecting again.")
				return
			}
			jsonReply(w, 200, map[string]string{"id": id, "url": xero.AuthorizationURL(state)})
			return
		}
		var id, state string
		var tenants json.RawMessage
		if err := db.QueryRowContext(ctx, `SELECT id::text,state,tenants FROM xero_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state<>'disconnected'`, in.ID, a.Organization, entity).Scan(&id, &state, &tenants); err != nil {
			problem(w, 404, "Xero connection not found for this client.")
			return
		}
		aad := a.Organization + "/xero/" + id
		fail := func(message string) {
			persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer done()
			db.ExecContext(persist, `UPDATE xero_connections SET state='reconnect_required',error=$2,updated_at=now() WHERE id=$1 AND state IN ('exchanging','refreshing')`, id, message)
			problem(w, 422, message)
		}
		changed := func(result sql.Result, err error) bool {
			if err != nil {
				problem(w, 503, "Connection storage unavailable.")
				return true
			}
			if n, _ := result.RowsAffected(); n != 1 {
				problem(w, 409, "The connection changed. Refresh its status.")
				return true
			}
			return false
		}
		switch in.Action {
		case "disconnect":
			if changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='disconnected',credential=NULL,updated_at=now(),error='Disconnected here; remove the app under Connected Apps in Xero to revoke its access.' WHERE id=$1`, id)) {
				return
			}
		case "callback":
			if len(in.Code) == 0 || len(in.Code) > 4096 || len(in.State) > 100 {
				problem(w, 422, "Invalid Xero sign-in response.")
				return
			}
			if changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='exchanging',updated_at=now() WHERE id=$1 AND state='authorizing' AND oauth_digest=$2 AND oauth_expires_at>now() AND actor=$3`, id, preparation.Digest([]byte(in.State)), a.Subject)) {
				return
			}
			credentials, err := xero.Exchange(ctx, in.Code)
			if err != nil {
				fail(err.Error())
				return
			}
			list, err := xero.Tenants(ctx, credentials.Access)
			if err != nil {
				fail(err.Error())
				return
			}
			if len(list) == 0 {
				fail("No Xero organisation was authorised in this sign-in. Connect again and choose the client's organisation.")
				return
			}
			raw, _ := json.Marshal(credentials)
			sealed, err := secrets.Seal(aad, raw)
			if err != nil {
				fail(err.Error())
				return
			}
			choices, _ := json.Marshal(list)
			if changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='choose_tenant',tenants=$2,credential=$3,access_expires_at=$4,error='',updated_at=now() WHERE id=$1 AND state='exchanging'`, id, string(choices), sealed, time.Now().Add(time.Duration(credentials.Expires)*time.Second))) {
				return
			}
		case "confirm":
			var list []xero.Tenant
			json.Unmarshal(tenants, &list)
			i := slices.IndexFunc(list, func(t xero.Tenant) bool { return t.ID == in.TenantID })
			if state != "choose_tenant" || i < 0 {
				problem(w, 409, "Choose one of the organisations authorised in this sign-in.")
				return
			}
			if changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='active',tenant_id=$2,tenant_name=$3,confirmed_by=$4,confirmed_at=now(),updated_at=now(),changed_at=now() WHERE id=$1 AND state='choose_tenant'`, id, list[i].ID, list[i].Name, a.Subject)) {
				return
			}
		case "snapshot":
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(95 * time.Second))
			if err := captureXero(ctx, db, a.Organization, id, r.PathValue("id"), period, a.Subject); err != nil {
				refuse(w, err)
				return
			}
		default:
			problem(w, 422, fmt.Sprintf("Unsupported Xero action %q.", in.Action))
			return
		}
		out, err := status(ctx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Xero status unavailable.")
			return
		}
		out["hosted"] = true
		jsonReply(w, 200, out)
	})
}

// captureXero saves a connected organisation's Trial Balance as at the close's month end.
func captureXero(ctx context.Context, db *sql.DB, org, id, closeID, period, actor string) error {
	access, tenant, err := xeroAccess(ctx, db, org, id)
	if err != nil {
		return err
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return refusal{422, "This close has no valid month."}
	}
	payload, err := xero.Capture(ctx, access, tenant, month.AddDate(0, 1, -1))
	if err != nil {
		db.ExecContext(ctx, `UPDATE xero_connections SET error=$2,updated_at=now() WHERE id=$1`, id, err.Error())
		return refusal{422, err.Error()}
	}
	currency, fiscalStart, err := xero.Profile(payload)
	if err != nil {
		return refusal{422, err.Error()}
	}
	// Findings are recorded with the snapshot, as each QuickBooks capture records its own, so Daily books can show what is new.
	var findings sql.NullString
	ledger := "[]"
	if rows, open, _, _, e := xero.Ledger(payload); e == nil {
		found := append(quickbooks.FindExceptions(rows), xeroWording(quickbooks.OpenItemExceptions(open, month.AddDate(0, 1, -1)))...)
		raw, _ := json.Marshal(append([]quickbooks.Exception{}, found...))
		findings = sql.NullString{String: string(raw), Valid: true}
		raw, _ = json.Marshal(append([]quickbooks.TransactionRow{}, rows...))
		ledger = string(raw)
	}
	failed := refusal{503, "The Trial Balance was read but could not be saved. Capture again."}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return failed
	}
	defer tx.Rollback()
	// The insert's foreign key waits on the close lock Daily books' Mark reviewed takes, so a review never passes over it.
	// Search reads the newest capture's ledger rows, so only those are kept.
	var snapshot string
	if err = tx.QueryRowContext(ctx, `INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,exceptions) VALUES($1,$2,$3,$4,encode(sha256(convert_to($5::jsonb::text,'UTF8')),'hex'),$5,$6,$7) RETURNING id::text`, closeID, id, tenant, period, string(payload), actor, findings).Scan(&snapshot); err != nil {
		return failed
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO xero_transaction_rows(snapshot_id,kind,provider_id,txn_date,currency,amount,record)
 SELECT $1::bigint,r."Kind",r."ProviderID",r."Date"::date,NULLIF(r."Currency",''),NULLIF(r."Amount",''),r."Record" FROM jsonb_to_recordset($2::jsonb) AS r("Kind" text,"ProviderID" text,"Date" text,"Currency" text,"Amount" text,"Record" jsonb)
 ON CONFLICT DO NOTHING`, snapshot, ledger); err != nil {
		return failed
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM xero_transaction_rows t USING xero_snapshots s WHERE t.snapshot_id=s.id AND s.close_id::text=$1 AND s.id::text<>$2`, closeID, snapshot); err != nil {
		return failed
	}
	if _, err = tx.ExecContext(ctx, `UPDATE xero_connections SET home_currency=$2,fiscal_year_start=$3 WHERE id::text=$1`, id, currency, fiscalStart); err != nil || tx.Commit() != nil {
		return failed
	}
	return nil
}

// xeroAccess returns an active connection's access token and tenant, rotating the token first when it is about to
// expire. Xero retires a refresh token once used, so the new one is saved before anything else; a refresh that
// cannot be saved asks for a reconnection.
func xeroAccess(ctx context.Context, db *sql.DB, org, id string) (string, string, error) {
	var state, tenant string
	var encrypted []byte
	var expires sql.NullTime
	if err := db.QueryRowContext(ctx, `SELECT state,tenant_id,credential,access_expires_at FROM xero_connections WHERE id::text=$1 AND organization_id=$2`, id, org).Scan(&state, &tenant, &encrypted, &expires); err != nil {
		return "", "", refusal{404, "Xero connection not found for this client."}
	}
	if state != "active" {
		return "", "", refusal{409, "Confirm the client's Xero organisation first."}
	}
	aad := org + "/xero/" + id
	fail := func(message string) error {
		persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		db.ExecContext(persist, `UPDATE xero_connections SET state='reconnect_required',error=$2,updated_at=now() WHERE id=$1 AND state='refreshing'`, id, message)
		return refusal{422, message}
	}
	changed := func(result sql.Result, err error) error {
		if err != nil {
			return refusal{503, "Connection storage unavailable."}
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return refusal{409, "The connection changed. Refresh its status."}
		}
		return nil
	}
	raw, err := secrets.Open(aad, encrypted)
	var credentials xero.Credentials
	if err != nil || json.Unmarshal(raw, &credentials) != nil {
		return "", "", refusal{503, "Encrypted connection unavailable; check the server key."}
	}
	if !expires.Valid || time.Until(expires.Time) < 2*time.Minute {
		if err = changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='refreshing',updated_at=now() WHERE id=$1 AND state='active' AND access_expires_at IS NOT DISTINCT FROM $2`, id, expires)); err != nil {
			return "", "", err
		}
		if credentials, err = xero.Refresh(ctx, credentials.Refresh); err != nil {
			return "", "", fail(err.Error())
		}
		raw, _ = json.Marshal(credentials)
		sealed, err := secrets.Seal(aad, raw)
		if err != nil {
			return "", "", fail(err.Error())
		}
		result, err := db.ExecContext(ctx, `UPDATE xero_connections SET credential=$2,access_expires_at=$3,state='active',updated_at=now() WHERE id=$1 AND state='refreshing'`, id, sealed, time.Now().Add(time.Duration(credentials.Expires)*time.Second))
		if err != nil {
			return "", "", fail("Rotated Xero credentials could not be saved. Connect again before continuing.")
		}
		if err = changed(result, nil); err != nil {
			return "", "", err
		}
	}
	return credentials.Access, tenant, nil
}

// xeroBooks is a close's Xero transactions and open items in the QuickBooks shapes the checks read.
type xeroBooks struct {
	ID, CapturedAt, MonthEnd, Home string
	// Unread says why the newest snapshot has no transactions; an older snapshot's are never shown in its place.
	Unread   string
	Rows     []quickbooks.TransactionRow
	Open     map[string][]json.RawMessage
	Accounts json.RawMessage
}

// latestXeroBooks returns the close's newest Xero snapshot for its month, from the client's current connection, when it
// is newer than the close's latest QuickBooks transaction capture; nil otherwise.
func latestXeroBooks(ctx context.Context, db *sql.DB, closeID string) (*xeroBooks, error) {
	var b xeroBooks
	var captured time.Time
	var payload []byte
	err := db.QueryRowContext(ctx, `SELECT s.id::text,s.captured_at,s.payload FROM xero_snapshots s JOIN client_closes c ON c.id=s.close_id AND c.period=s.period
 JOIN xero_connections x ON x.id=s.connection_id AND x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.state<>'disconnected'
 WHERE s.close_id::text=$1 AND NOT EXISTS(SELECT 1 FROM quickbooks_entity_syncs q JOIN evidence e ON e.id=q.evidence_id AND e.close_id=q.close_id AND e.superseded_by IS NULL WHERE q.close_id=s.close_id AND q.state IN ('completed','unchanged') AND q.finished_at>s.captured_at AND q.connection_id=`+quickbooks.ReadingConnection("s.close_id")+`)
 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, closeID).Scan(&b.ID, &captured, &payload)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var dated struct{ Date string }
	json.Unmarshal(payload, &dated)
	b.CapturedAt, b.MonthEnd = captured.UTC().Format(time.RFC3339), dated.Date
	if b.Rows, b.Open, b.Accounts, b.Home, err = xero.Ledger(payload); err != nil {
		b.Unread = err.Error()
	}
	return &b, nil
}

// xeroWording names Xero where a QuickBooks check's wording names QuickBooks.
func xeroWording(items []quickbooks.Exception) []quickbooks.Exception {
	for i := range items {
		for _, text := range []*string{&items[i].Finding, &items[i].NextStep, &items[i].Question} {
			*text = strings.ReplaceAll(*text, "QuickBooks", "Xero")
		}
	}
	return items
}

// xeroDue is SQL: close c's firm is enabled and the close is open, in the current or prior month, and either not attempted
// and not captured in the past day, or its client's active connection reported a change after the last attempt and capture,
// with no attempt in the past 30 minutes.
const xeroDue = `EXISTS(SELECT 1 FROM organizations o WHERE o.id=c.organization_id AND o.enabled)
 AND c.period BETWEEN to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND to_char(now() AT TIME ZONE 'UTC','YYYY-MM')
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 AND (((c.xero_refresh_at IS NULL OR c.xero_refresh_at<now()-interval '24 hours')
   AND NOT EXISTS(SELECT 1 FROM xero_snapshots s WHERE s.close_id=c.id AND s.captured_at>now()-interval '24 hours'))
  OR ((c.xero_refresh_at IS NULL OR c.xero_refresh_at<now()-interval '30 minutes')
   AND EXISTS(SELECT 1 FROM xero_connections h WHERE h.organization_id=c.organization_id AND lower(h.client_entity)=lower(c.entity) AND h.state='active'
    AND h.changed_at>GREATEST(c.xero_refresh_at,(SELECT max(captured_at) FROM xero_snapshots s WHERE s.close_id=c.id)))))`

// sweepXeroRefresh captures, for up to 10 due closes with an active Xero connection, the month-end Trial Balance.
// Claiming the close first means one attempt per due close, whichever worker makes it and however it ends.
func sweepXeroRefresh(ctx context.Context, db *sql.DB) error {
	if !xero.Configured() {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id::text,c.organization_id,c.period,x.id::text FROM client_closes c
 JOIN xero_connections x ON x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.state='active' AND x.credential IS NOT NULL
 WHERE `+xeroDue+` ORDER BY c.id LIMIT 10`)
	if err != nil {
		return err
	}
	type due struct{ close, org, period, connection string }
	closes := []due{}
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.close, &d.org, &d.period, &d.connection); err != nil {
			break
		}
		closes = append(closes, d)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range closes {
		claim, err := db.ExecContext(ctx, `UPDATE client_closes c SET xero_refresh_at=now() WHERE c.id::text=$1 AND `+xeroDue, d.close)
		if err != nil {
			return err
		}
		if n, _ := claim.RowsAffected(); n != 1 {
			continue
		}
		// The daily capture has more time than a person's request, so a large month's transactions fit.
		run, cancel := context.WithTimeout(ctx, 3*time.Minute)
		err = captureXero(run, db, d.org, d.connection, d.close, d.period, "scheduled-xero-refresh")
		cancel()
		if err != nil {
			log.Print("Scheduled Xero capture did not complete for close ", d.close, ": ", err)
		}
	}
	return nil
}

// RunXeroRefresh looks for due Xero captures every ten minutes.
func RunXeroRefresh(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := sweepXeroRefresh(ctx, db); err != nil && ctx.Err() == nil {
			log.Print("Scheduled Xero captures could not run: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// nativeReconciliation reports Xero's own reconciled flags per bank account from the close month's newest Xero capture on
// an active connection, when that capture is newer than any QuickBooks period snapshot of the month on the current
// QuickBooks reading connection. QuickBooks' API offers no verified per-record reconciliation status.
func nativeReconciliation(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID string) (map[string]any, error) {
	var quickbooksAt, xeroAt sql.NullTime
	var snapshotID string
	var payload []byte
	if err := q.QueryRowContext(ctx, `SELECT (SELECT max(s.captured_at) FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload->>'period'=c.period AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+`),
 x.captured_at,COALESCE(x.id::text,''),x.payload FROM client_closes c LEFT JOIN LATERAL (SELECT s.id,s.captured_at,s.payload FROM xero_snapshots s JOIN xero_connections k ON k.id=s.connection_id AND k.state='active' WHERE s.close_id=c.id AND s.period=c.period ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) x ON true WHERE c.id::text=$1`, closeID).Scan(&quickbooksAt, &xeroAt, &snapshotID, &payload); err != nil {
		return nil, err
	}
	if !xeroAt.Valid || quickbooksAt.Valid && !xeroAt.Time.After(quickbooksAt.Time) {
		reason := "No Xero capture for this month."
		if quickbooksAt.Valid {
			reason = "The app reads no per-record reconciliation status from QuickBooks; the reviewer's cited observation stands."
		}
		return map[string]any{"status": "unavailable", "reason": reason}, nil
	}
	accounts, err := xero.NativeReconciliation(payload)
	if err != nil {
		return map[string]any{"status": "unavailable", "source": "xero", "snapshot_id": snapshotID, "captured_at": xeroAt.Time, "reason": err.Error()}, nil
	}
	return map[string]any{"status": "read", "source": "xero", "snapshot_id": snapshotID, "captured_at": xeroAt.Time, "accounts": accounts}, nil
}

// nativeSentences states, per bank account, what Xero flagged, without claiming the account or its statement is settled.
func nativeSentences(out map[string]any) []string {
	accounts, _ := out["accounts"].([]xero.NativeStatus)
	if out["status"] != "read" {
		reason, _ := out["reason"].(string)
		return []string{"Native reconciliation status was not read: " + reason}
	}
	at, _ := out["captured_at"].(time.Time)
	sentences := []string{}
	for _, a := range accounts {
		text := fmt.Sprintf("Xero flagged %d of %d captured records for %s as reconciled at %s", a.Reconciled, a.Reconciled+a.Unreconciled+a.Unknown, a.Account, at.UTC().Format("Jan 2, 15:04 UTC"))
		if a.Unknown > 0 {
			text += fmt.Sprintf(" (%d without a flag)", a.Unknown)
		}
		sentences = append(sentences, text+"; native account completion and statement settlement were not verified.")
	}
	return sentences
}
