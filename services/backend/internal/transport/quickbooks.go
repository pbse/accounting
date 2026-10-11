package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/secrets"
)

func quickbooksReturn(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if db == nil {
		problem(w, 503, "Connection storage unavailable.")
		return
	}
	q := r.URL.Query()
	if len(q.Get("state")) > 100 || len(q.Get("code")) > 4096 || q.Get("code") == "" || !quickbooks.ValidRealm(q.Get("realmId")) {
		problem(w, 422, "QuickBooks authorization was cancelled or invalid. Return to the client's page and connect again.")
		return
	}
	var entity, environment, id string
	err := db.QueryRowContext(r.Context(), `SELECT x.client_entity,x.environment,x.id::text FROM quickbooks_connections x JOIN organizations o ON o.id=x.organization_id WHERE x.oauth_digest=$1 AND x.state='authorizing' AND x.oauth_expires_at>now() AND o.enabled`, preparation.Digest([]byte(q.Get("state")))).Scan(&entity, &environment, &id)
	if err != nil {
		problem(w, 410, "Connection attempt expired or unavailable. Start again from the client's page.")
		return
	}
	origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		problem(w, 503, "Configure the exact HTTPS application origin.")
		return
	}
	// The client's page holds its connections, whichever page the attempt was started from, and finishes it.
	origin.Path, origin.RawPath = "/dashboard/clients/"+entity, "/dashboard/clients/"+url.PathEscape(entity)
	// The attempt is named, so the page finishes this one whatever others are under way.
	origin.Fragment = url.Values{"qbo_code": {q.Get("code")}, "qbo_state": {q.Get("state")}, "qbo_realm": {q.Get("realmId")}, "qbo_env": {environment}, "qbo_connection": {id}}.Encode()
	http.Redirect(w, r, origin.String(), http.StatusSeeOther)
}

// captureLedger is the ledger side of a bank reconciliation read from the close's QuickBooks capture whose evidence entry
// is sourceID, for the one bank account named account; ok is false when sourceID is no capture of this close.
func captureLedger(ctx context.Context, tx *sql.Tx, closeID, sourceID, account string) (csv, opening, closing string, ok bool, err error) {
	var raw []byte
	var period string
	err = tx.QueryRowContext(ctx, `SELECT s.payload,c.period FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE s.close_id::text=$1 AND s.payload->>'source_id'=$2 AND s.payload ? 'period' ORDER BY s.id DESC LIMIT 1`, closeID, sourceID).Scan(&raw, &period)
	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	var payload struct {
		Accounts json.RawMessage            `json:"accounts"`
		Reports  map[string]json.RawMessage `json:"reports"`
	}
	var directory []struct {
		ID                       string `json:"Id"`
		Name, FullyQualifiedName string
		AccountType              string
	}
	if json.Unmarshal(raw, &payload) != nil || json.Unmarshal(payload.Accounts, &directory) != nil {
		return "", "", "", true, fmt.Errorf("The QuickBooks capture is unreadable")
	}
	id := ""
	for _, a := range directory {
		if a.AccountType == "Bank" && (strings.EqualFold(a.Name, account) || strings.EqualFold(a.FullyQualifiedName, account)) {
			if id != "" {
				return "", "", "", true, fmt.Errorf("More than one bank account in the capture is named %s; reconcile it from its exported ledger", account)
			}
			id = a.ID
		}
	}
	if id == "" {
		return "", "", "", true, fmt.Errorf("No bank account named %s is in the capture; credit cards and other accounts are reconciled from their exported ledger", account)
	}
	csv, opening, closing, err = quickbooks.LedgerLines(payload.Accounts, payload.Reports["GeneralLedger"], payload.Reports["TrialBalance"], id, period)
	return csv, opening, closing, true, err
}

func registerQuickBooks(mux *http.ServeMux, db *sql.DB) {
	// A bank account's month from the close's current QuickBooks capture, as a reconciliation's ledger side, so the ledger
	// need not be exported by hand; the person still reviews and confirms the reconciliation.
	mux.HandleFunc("GET /v1/closes/{id}/reconciliation-ledger", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("account"))
		if name == "" || len(name) > 200 {
			problem(w, 422, "Name the bank account to reconcile.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent snapshot.")
			return
		}
		defer tx.Rollback()
		var period string
		if err = tx.QueryRowContext(r.Context(), `SELECT period FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		month, err := periodBooks(r.Context(), tx, r.PathValue("id"), period)
		switch {
		case err != nil:
			problem(w, 503, "Period snapshot unavailable.")
			return
		case month == nil || month.Source != "quickbooks" || month.SnapshotID == "":
			problem(w, 409, "Capture this month's QuickBooks period snapshot first.")
			return
		case month.Stale:
			problem(w, 409, "Capture this month again: this capture is no longer current.")
			return
		}
		var source string
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(payload->>'source_id','') FROM quickbooks_snapshots WHERE id::text=$1`, month.SnapshotID).Scan(&source); err != nil {
			problem(w, 503, "Period snapshot unavailable.")
			return
		}
		lines, opening, closing, _, err := captureLedger(r.Context(), tx, r.PathValue("id"), source, name)
		if err != nil {
			problem(w, 409, err.Error()+".")
			return
		}
		jsonReply(w, 200, map[string]string{"source_id": source, "location": "General Ledger · " + name, "ledger_csv": lines, "ledger_opening": opening, "ledger_closing": closing,
			"ledger_reference": "QuickBooks General Ledger for " + name + ", capture #" + month.SnapshotID + " of " + month.CapturedAt.UTC().Format("2006-01-02 15:04") + " UTC"})
	})
	path := "/v1/closes/{id}/quickbooks"
	ledgerPage := func(w http.ResponseWriter, r *http.Request, saved bool) {
		accountID := r.URL.Query().Get("account_id")
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		offset, parseErr := strconv.Atoi(r.URL.Query().Get("offset"))
		if parseErr != nil || len(accountID) > 100 {
			problem(w, 422, "Choose an account and a valid page offset.")
			return
		}
		var snapshot struct {
			ID       string                     `json:"id"`
			Digest   string                     `json:"digest"`
			Accounts json.RawMessage            `json:"accounts"`
			Reports  map[string]json.RawMessage `json:"reports"`
		}
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT payload || jsonb_build_object('id',id::text,'digest',digest) FROM quickbooks_snapshots WHERE id::text=$1 AND close_id::text=$2 AND payload ? 'period'`, r.PathValue("snapshot"), r.PathValue("id")).Scan(&raw)
		if err != nil || json.Unmarshal(raw, &snapshot) != nil {
			problem(w, 404, "Period snapshot not found in this close.")
			return
		}
		var page quickbooks.LedgerPage
		if query == "" {
			page, err = quickbooks.SelectLedger(snapshot.Accounts, snapshot.Reports["GeneralLedger"], accountID, offset)
		} else {
			page, err = quickbooks.SearchLedger(snapshot.Accounts, snapshot.Reports["GeneralLedger"], accountID, query, offset)
		}
		if err != nil {
			problem(w, 422, err.Error())
			return
		}
		if !saved {
			jsonReply(w, 200, page)
			return
		}
		actor, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Confirmed bool `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed || len(page.Rows) == 0 {
			problem(w, 422, "Confirm a page with account-matched rows before adding agent evidence.")
			return
		}
		content, _ := json.MarshalIndent(map[string]any{"snapshot_id": snapshot.ID, "snapshot_digest": snapshot.Digest, "account_id": accountID, "query": query, "offset": offset, "total_matching_rows": page.Total, "rows": page.Rows, "scope": "Account-matched General Ledger report rows; not a complete ledger or verified balance."}, "", "  ")
		if len(content) > 96*1024 {
			problem(w, 413, "This ledger page exceeds 96 KiB. Inspect the downloaded snapshot and narrow the account.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Evidence storage unavailable.")
			return
		}
		defer tx.Rollback()
		// The scheduled refresh locks the close before saving a newer snapshot, so this check and the selection see it or precede it.
		if _, err = tx.ExecContext(r.Context(), `SELECT 1 FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")); err != nil {
			problem(w, 503, "Could not verify the ledger capture.")
			return
		}
		var current bool
		err = tx.QueryRowContext(r.Context(), `SELECT e.superseded_by IS NULL AND e.extraction_state='ready' AND x.state='active' FROM quickbooks_snapshots s JOIN evidence e ON e.id::text=s.payload->>'source_id' JOIN quickbooks_connections x ON x.id=s.connection_id WHERE s.id::text=$1 AND s.close_id::text=$2
 -- Only the month's newest capture: a scheduled refresh keeps older evidence without superseding it.
 AND NOT EXISTS(SELECT 1 FROM quickbooks_snapshots n WHERE n.close_id=s.close_id AND n.payload->>'period'=s.payload->>'period' AND (n.captured_at,n.id)>(s.captured_at,s.id)) FOR UPDATE OF e`, snapshot.ID, r.PathValue("id")).Scan(&current)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Could not verify the ledger capture.")
			return
		}
		if err != nil || !current {
			problemCode(w, 409, "SOURCE_STALE", "Ledger capture changed. Refresh before selecting rows.")
			return
		}
		var sourceID string
		err = tx.QueryRowContext(r.Context(), `SELECT source_id::text FROM quickbooks_ledger_selections WHERE snapshot_id=$1 AND account_id=$2 AND offset_rows=$3 AND search_query=$4`, snapshot.ID, accountID, offset, query).Scan(&sourceID)
		if err == sql.ErrNoRows {
			var count, size int
			err = tx.QueryRowContext(r.Context(), `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, r.PathValue("id")).Scan(&count, &size)
			if err != nil || count >= 100 || size+len(content) > 20*1024*1024 {
				problem(w, 413, "Close evidence capacity is full. Review existing sources before adding another ledger page.")
				return
			}
			name := "QuickBooks account " + accountID + " ledger rows " + strconv.Itoa(offset) + "–" + strconv.Itoa(offset+len(page.Rows)-1)
			if query != "" {
				name += " matching " + strconv.Quote(query)
			}
			err = tx.QueryRowContext(r.Context(), `INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, r.PathValue("id"), name, preparation.Digest(content), string(content), actor).Scan(&sourceID)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO quickbooks_ledger_selections(snapshot_id,source_id,account_id,offset_rows,search_query) VALUES($1,$2,$3,$4,$5)`, snapshot.ID, sourceID, accountID, offset, query)
			}
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Ledger selection could not be saved. Refresh before retrying.")
			return
		}
		jsonReply(w, 200, map[string]string{"source_id": sourceID})
	}
	mux.HandleFunc("GET "+path+"/snapshots/{snapshot}/ledger", func(w http.ResponseWriter, r *http.Request) { ledgerPage(w, r, false) })
	mux.HandleFunc("POST "+path+"/snapshots/{snapshot}/ledger", func(w http.ResponseWriter, r *http.Request) { ledgerPage(w, r, true) })
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		err := db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('connection',(SELECT jsonb_build_object('id',x.id::text,'environment',x.environment,'state',x.state,'realm',x.realm,'company_name',x.company_name,'confirmed_by',x.confirmed_by,'confirmed_at',x.confirmed_at,'error',x.error,'updated_at',x.updated_at) FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("c.id")+`), 'production_connection',(SELECT jsonb_build_object('id',x.id::text,'state',x.state,'realm',x.realm,'company_name',x.company_name,'error',x.error) FROM quickbooks_connections x WHERE x.organization_id=c.organization_id AND client_identity(x.organization_id,x.client_entity)=client_identity(c.organization_id,c.entity) AND x.environment='production' AND x.state<>'disconnected' ORDER BY lower(x.client_entity)=lower(c.entity) DESC,x.id DESC LIMIT 1), 'snapshots',COALESCE((SELECT jsonb_agg(v ORDER BY (v->>'id')::bigint DESC) FROM (SELECT jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'realm',s.realm,'digest',s.digest,'started_at',s.started_at,'captured_at',s.captured_at,'account_count',jsonb_array_length(s.payload->'accounts'),'actor',s.actor,'period',s.payload->>'period','source_id',s.payload->>'source_id') v FROM quickbooks_snapshots s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 20) recent),'[]'::jsonb)) FROM client_closes c WHERE c.id::text=$1`, r.PathValue("id")).Scan(&raw)
		if err != nil {
			problem(w, 503, "QuickBooks status unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"configured": quickbooks.OAuthConfigured(), "production_configured": quickbooks.OAuthConfiguredFor("production"), "hosted": auth.Actor(r.Context()).Organization != "", "data": raw})
	})
	mux.HandleFunc("GET "+path+"/snapshots/{snapshot}", func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		err := db.QueryRowContext(r.Context(), `SELECT payload FROM quickbooks_snapshots WHERE id::text=$1 AND close_id::text=$2`, r.PathValue("snapshot"), r.PathValue("id")).Scan(&raw)
		if err != nil {
			problem(w, 404, "Snapshot not found in this close.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="quickbooks-snapshot.json"`)
		w.Write(raw)
	})
	mux.HandleFunc("POST "+path, func(w http.ResponseWriter, r *http.Request) {
		a, in, ok := quickbooksRequest(w, r)
		if !ok {
			return
		}
		// Report reads share a bounded deadline; the server default is only 30s.
		if in.Action == "snapshot" || in.Action == "period_snapshot" {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(95 * time.Second))
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		var entity, period string
		if err := db.QueryRowContext(ctx, `SELECT entity,period FROM client_closes WHERE id::text=$1 AND organization_id=$2`, r.PathValue("id"), a.Organization).Scan(&entity, &period); err != nil {
			problem(w, 404, "Client close not found.")
			return
		}
		// A connection is the client's, made, finished and confirmed on its page; a month only reads through it.
		if in.Action == "connect" || in.Action == "callback" || in.Action == "confirm" {
			problem(w, 409, "Connect QuickBooks on the client's page; a month reads its books through the client's connection.")
			return
		}
		quickbooksAct(ctx, w, db, a, entity, period, r.PathValue("id"), in)
	})
}

// quickbooksIn is a QuickBooks action, from a close (which also reads its books) or from the client's page (which
// connects, confirms, refreshes and disconnects).
type quickbooksIn struct {
	Action      string `json:"action"`
	ID          string `json:"connection_id"`
	Confirmed   bool   `json:"confirmed"`
	Code        string `json:"code"`
	State       string `json:"state"`
	Realm       string `json:"realm"`
	Environment string `json:"environment"`
}

// quickbooksRequest is the member who may act on a client's QuickBooks connection (a hosted session with
// integrations:manage) and their confirmed action.
func quickbooksRequest(w http.ResponseWriter, r *http.Request) (*auth.Claims, quickbooksIn, bool) {
	var in quickbooksIn
	a := auth.Actor(r.Context())
	if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "integrations:manage") {
		problem(w, 403, "A hosted client-authorized session with integrations:manage is required.")
		return nil, in, false
	}
	if !decode(w, r, &in) {
		return nil, in, false
	}
	if !in.Confirmed {
		problem(w, 422, "Confirm this connection action and client scope.")
		return nil, in, false
	}
	return a, in, true
}

// quickbooksAct does a QuickBooks action for the client entity: connecting, finishing the authorization, confirming
// the company, refreshing or disconnecting it, and, from a close (closeID, of month period), reading its books. A
// connection made from the client's page names no close.
func quickbooksAct(ctx context.Context, w http.ResponseWriter, db *sql.DB, a *auth.Claims, entity, period, closeID string, in quickbooksIn) {
	if in.Action == "connect" {
		if in.Environment == "" {
			in.Environment = "sandbox"
		}
		if !quickbooks.OAuthConfiguredFor(in.Environment) {
			problem(w, 503, "Configure the selected QuickBooks OAuth application first.")
			return
		}
		if _, err := secrets.Seal(a.Organization+"/qbo-check", nil); err != nil {
			problem(w, 503, err.Error())
			return
		}
		token := rand.Text()
		var id string
		// The client lock orders a new connection against snapshot saves that check which connection is read.
		err := db.QueryRowContext(ctx, `WITH l AS (SELECT pg_advisory_xact_lock(hashtext($2||'/'||client_identity($2,$3)))) INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,environment) SELECT NULLIF($1,'')::bigint,$2,$3,$4,'authorizing',$5,now()+interval '10 minutes',$6 FROM l ON CONFLICT DO NOTHING RETURNING id::text`, closeID, a.Organization, entity, a.Subject, preparation.Digest([]byte(token)), in.Environment).Scan(&id)
		if err != nil {
			problem(w, 409, "A connection or attempt already exists. Refresh; disconnect the old attempt before reconnecting.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id, "url": quickbooks.AuthorizationURLFor(in.Environment, token)})
		return
	}
	var id, state, realm, company, environment string
	var encrypted []byte
	var expires sql.NullTime
	// A month reads its books through any of its client's connections, whichever name it is held under; managing a
	// connection (signing in, confirming, disconnecting) takes the name it is held under.
	reads := in.Action == "refresh" || in.Action == "snapshot" || in.Action == "period_snapshot"
	err := db.QueryRowContext(ctx, `SELECT id::text,state,realm,company_name,credential,access_expires_at,environment FROM quickbooks_connections WHERE id::text=$1 AND organization_id=$2
 AND (lower(client_entity)=lower($3) OR $4 AND client_identity(organization_id,client_entity)=client_identity($2,$3)) AND state<>'disconnected'`, in.ID, a.Organization, entity, reads).Scan(&id, &state, &realm, &company, &encrypted, &expires, &environment)
	if err != nil {
		problem(w, 404, "Connection not found for this client.")
		return
	}
	aad := a.Organization + "/qbo/" + id
	fail := func(message string) {
		persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		db.ExecContext(persist, `UPDATE quickbooks_connections SET state='reconnect_required',error=$2,updated_at=now() WHERE id=$1 AND state IN ('exchanging','refreshing')`, id, message)
		problem(w, 422, message)
	}
	readFail := func(message string) {
		persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		db.ExecContext(persist, `UPDATE quickbooks_connections SET error=$2 WHERE id=$1 AND state='active'`, id, message)
		problem(w, 422, message)
	}
	switch in.Action {
	case "disconnect":
		_, err = db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='disconnected',credential=NULL,updated_at=now(),error='Disconnected locally; revoke the app in Intuit to remove provider authorization.' WHERE id=$1`, id)
	case "callback":
		if len(in.Code) == 0 || len(in.Code) > 4096 || len(in.State) > 100 || !quickbooks.ValidRealm(in.Realm) {
			problem(w, 422, "Invalid authorization response.")
			return
		}
		result, e := db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='exchanging',updated_at=now() WHERE id=$1 AND state='authorizing' AND oauth_digest=$2 AND oauth_expires_at>now() AND actor=$3`, id, preparation.Digest([]byte(in.State)), a.Subject)
		if e != nil {
			problem(w, 503, "Authorization storage unavailable.")
			return
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			problem(w, 409, "Authorization expired, belongs to another user, or was already consumed. Refresh connection status.")
			return
		}
		credentials, e := quickbooks.ExchangeFor(ctx, environment, in.Code)
		if e != nil {
			fail(e.Error())
			return
		}
		company, e = quickbooks.CompanyFor(ctx, environment, in.Realm, credentials.Access)
		if e != nil {
			fail(e.Error())
			return
		}
		currency, fiscalStart, e := quickbooks.ProfileFor(ctx, environment, in.Realm, credentials.Access)
		if e != nil {
			fail(e.Error())
			return
		}
		raw, _ := json.Marshal(credentials)
		encrypted, e = secrets.Seal(aad, raw)
		if e != nil {
			fail(e.Error())
			return
		}
		result, err = db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='pending_confirmation',realm=$2,company_name=$3,credential=$4,access_expires_at=$5,home_currency=$6,fiscal_year_start=$7,updated_at=now(),error='' WHERE id=$1 AND state='exchanging'`, id, in.Realm, company, encrypted, time.Now().Add(time.Duration(credentials.Expires)*time.Second), currency, fiscalStart)
		if err == nil {
			n, _ = result.RowsAffected()
			if n != 1 {
				err = fmt.Errorf("connection changed")
			}
		}
	case "confirm":
		if state != "pending_confirmation" || in.Realm != realm {
			problem(w, 409, "Refresh and confirm the displayed company and exact realm.")
			return
		}
		if environment == "production" && company != entity {
			problem(w, 409, "The QuickBooks company name must exactly match this client's name before production books are read or posted.")
			return
		}
		result, e := db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='active',confirmed_by=$2,confirmed_at=now(),updated_at=now() WHERE id=$1 AND state='pending_confirmation'`, id, a.Subject)
		err = e
		if err == nil {
			n, _ := result.RowsAffected()
			if n != 1 {
				err = fmt.Errorf("connection changed")
			}
		}
	case "refresh", "snapshot", "period_snapshot":
		if closeID == "" && in.Action != "refresh" {
			problem(w, 422, "A month's books are read from the month.")
			return
		}
		if state != "active" {
			problem(w, 409, "Confirm an active company before reading accounts. Incomplete token attempts require reconnecting.")
			return
		}
		if in.Action != "refresh" {
			var reading bool
			if e := db.QueryRowContext(ctx, `SELECT $1::bigint=`+quickbooks.ReadingConnection("$2::bigint"), id, closeID).Scan(&reading); e != nil || !reading {
				problem(w, 409, "Read this client's books from its production QuickBooks connection.")
				return
			}
		}
		raw, e := secrets.Open(aad, encrypted)
		var credentials quickbooks.Credentials
		if e != nil || json.Unmarshal(raw, &credentials) != nil {
			problem(w, 503, "Encrypted connection unavailable; check the server key.")
			return
		}
		if !expires.Valid || time.Until(expires.Time) < 2*time.Minute {
			result, e := db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='refreshing',updated_at=now() WHERE id=$1 AND state='active' AND access_expires_at IS NOT DISTINCT FROM $2`, id, expires)
			if e != nil {
				problem(w, 503, "Refresh storage unavailable.")
				return
			}
			n, _ := result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Another request changed the connection. Refresh status.")
				return
			}
			credentials, e = quickbooks.RefreshFor(ctx, environment, credentials.Refresh)
			if e != nil {
				fail(e.Error())
				return
			}
			raw, _ = json.Marshal(credentials)
			encrypted, e = secrets.Seal(aad, raw)
			if e != nil {
				fail(e.Error())
				return
			}
			result, e = db.ExecContext(ctx, `UPDATE quickbooks_connections SET credential=$2,access_expires_at=$3,state='active',updated_at=now() WHERE id=$1 AND state='refreshing'`, id, encrypted, time.Now().Add(time.Duration(credentials.Expires)*time.Second))
			if e != nil {
				fail("Rotated credentials could not be saved. Reconnect before continuing.")
				return
			}
			n, _ = result.RowsAffected()
			if n != 1 {
				problem(w, 409, "Connection changed during refresh.")
				return
			}
		}
		if in.Action == "refresh" {
			jsonReply(w, 200, map[string]string{"state": "active"})
			return
		}
		started := time.Now()
		reportPeriod := ""
		if in.Action == "period_snapshot" {
			reportPeriod = period
		}
		snapshot, evidence, e := quickbooks.ReadSnapshot(ctx, environment, realm, credentials.Access, company, reportPeriod, started)
		if e != nil {
			readFail(e.Error())
			return
		}
		payload, _ := json.Marshal(snapshot)
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			problem(w, 503, "Snapshot storage unavailable.")
			return
		}
		defer tx.Rollback()
		// Taken first, as the connect insert does (keyed by client identity, whichever of its names each is under), so the
		// two never wait on each other's row locks.
		if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1||'/'||client_identity($1,$2)))`, a.Organization, entity); e != nil {
			problem(w, 503, "Snapshot storage unavailable.")
			return
		}
		var active bool
		e = tx.QueryRowContext(ctx, `SELECT x.state='active' AND o.enabled FROM quickbooks_connections x JOIN organizations o ON o.id=x.organization_id WHERE x.id=$1 FOR UPDATE OF x`, id).Scan(&active)
		if e != nil || !active {
			problem(w, 409, "Connection changed while reading. No snapshot saved.")
			return
		}
		var lockedClose string
		if e = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, closeID).Scan(&lockedClose); e != nil {
			problem(w, 503, "Close storage unavailable.")
			return
		}
		var stillReading bool
		if e = tx.QueryRowContext(ctx, `SELECT $1::bigint=`+quickbooks.ReadingConnection("$2::bigint"), id, lockedClose).Scan(&stillReading); e != nil || !stillReading {
			problem(w, 409, "This client's books are now read from its production QuickBooks connection. No snapshot saved.")
			return
		}
		if evidence != nil {
			var newer bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quickbooks_snapshots WHERE close_id=$1 AND payload ? 'period' AND started_at>$2)`, lockedClose, started).Scan(&newer); e != nil || newer {
				problem(w, 409, "A newer ledger capture already exists. Refresh coverage; this older read was not saved.")
				return
			}
			var count, size int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, lockedClose).Scan(&count, &size); e != nil || count >= 100 || size+len(evidence) > 20*1024*1024 {
				problem(w, 413, "Close evidence capacity cannot hold this snapshot.")
				return
			}
			var uncertain bool
			if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_postings p JOIN workpapers w ON w.id=p.workpaper_id WHERE w.close_id=$1 AND p.state IN ('executing','unknown'))`, lockedClose).Scan(&uncertain); e != nil || uncertain {
				problem(w, 409, "Resolve uncertain sandbox attempts before replacing ledger evidence.")
				return
			}
			var sourceID string
			e = tx.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, lockedClose, "QuickBooks "+period+" accrual period evidence.txt", preparation.Digest(evidence), string(evidence), a.Subject).Scan(&sourceID)
			if e != nil {
				problem(w, 503, "Could not link accounting evidence.")
				return
			}
			_, e = tx.ExecContext(ctx, `UPDATE evidence SET superseded_by=$2,replacement_reason='New period ledger capture',replaced_by=$3,replaced_at=now() WHERE close_id=$1 AND superseded_by IS NULL AND id::text IN (SELECT payload->>'source_id' FROM quickbooks_snapshots WHERE close_id=$1 AND payload->>'period'=$4)`, lockedClose, sourceID, a.Subject, period)
			if e != nil {
				problem(w, 503, "Could not replace prior ledger evidence.")
				return
			}
			_, e = tx.ExecContext(ctx, `UPDATE evidence SET superseded_by=$2,replacement_reason='New period ledger capture',replaced_by=$3,replaced_at=now() WHERE close_id=$1 AND superseded_by IS NULL AND id IN (SELECT l.source_id FROM quickbooks_ledger_selections l JOIN quickbooks_snapshots s ON s.id=l.snapshot_id WHERE s.close_id=$1 AND s.payload->>'period'=$4)`, lockedClose, sourceID, a.Subject, period)
			if e != nil {
				problem(w, 503, "Could not replace selected ledger evidence.")
				return
			}
			_, e = tx.ExecContext(ctx, `UPDATE sandbox_postings p SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM workpapers w WHERE w.id=p.workpaper_id AND w.close_id=$1 AND p.state='approved' AND EXISTS(SELECT 1 FROM evidence e WHERE e.close_id=$1 AND e.superseded_by::text=$2 AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',e.id::text)))`, lockedClose, sourceID)
			if e != nil {
				problem(w, 503, "Could not invalidate affected posting approvals.")
				return
			}
			snapshot["source_id"] = sourceID
			payload, _ = json.Marshal(snapshot)
			_, e = tx.ExecContext(ctx, `UPDATE preparation_jobs j SET state='waiting_evidence',error='' FROM preparation_intakes i WHERE i.job_id=j.id AND j.close_id=$1 AND j.state='needs_confirmation' AND i.proposal->>'engine' IN ('openai','context_machine')`, lockedClose)
			if e != nil {
				problem(w, 503, "Could not resume evidence interpretation.")
				return
			}
		}
		var count int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM quickbooks_snapshots WHERE close_id=$1`, closeID).Scan(&count); e != nil || count >= 20 {
			problem(w, 422, "This close has reached its 20-snapshot limit, or storage is unavailable.")
			return
		}
		var sid string
		e = tx.QueryRowContext(ctx, `INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,$3,encode(sha256(convert_to($4::jsonb::text,'UTF8')),'hex'),$4,$5,$6) RETURNING id::text`, closeID, id, realm, string(payload), started, a.Subject).Scan(&sid)
		if e == nil {
			_, e = tx.ExecContext(ctx, `UPDATE quickbooks_connections SET error='' WHERE id=$1`, id)
		}
		if e != nil || tx.Commit() != nil {
			problem(w, 503, "Snapshot save could not be confirmed. Refresh before reading again.")
			return
		}
		jsonReply(w, 200, map[string]string{"snapshot_id": sid})
		return
	default:
		problem(w, 422, "Unsupported connection action.")
		return
	}
	if err != nil {
		problem(w, 503, "Connection update could not be confirmed. Refresh before retrying.")
		return
	}
	jsonReply(w, 200, map[string]bool{"saved": true})
}
