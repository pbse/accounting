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
		problem(w, 422, "QuickBooks authorization was cancelled or invalid. Return to your close and reconnect.")
		return
	}
	var closeID, environment string
	err := db.QueryRowContext(r.Context(), `SELECT x.close_id::text,x.environment FROM quickbooks_connections x JOIN organizations o ON o.id=x.organization_id WHERE x.oauth_digest=$1 AND x.state='authorizing' AND x.oauth_expires_at>now() AND o.enabled`, preparation.Digest([]byte(q.Get("state")))).Scan(&closeID, &environment)
	if err != nil {
		problem(w, 410, "Connection attempt expired or unavailable. Start again from your close.")
		return
	}
	origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		problem(w, 503, "Configure the exact HTTPS application origin.")
		return
	}
	// Sandbox evidence lives on the Evidence tab; production access on Settings.
	origin.Path, origin.RawQuery = "/dashboard/closes/"+closeID, "tab=evidence"
	if environment == "production" {
		origin.RawQuery = "tab=settings"
	}
	origin.Fragment = url.Values{"qbo_code": {q.Get("code")}, "qbo_state": {q.Get("state")}, "qbo_realm": {q.Get("realmId")}, "qbo_env": {environment}}.Encode()
	http.Redirect(w, r, origin.String(), http.StatusSeeOther)
}
func registerQuickBooks(mux *http.ServeMux, db *sql.DB) {
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
		var current bool
		err = tx.QueryRowContext(r.Context(), `SELECT e.superseded_by IS NULL AND e.extraction_state='ready' AND x.state='active' FROM quickbooks_snapshots s JOIN evidence e ON e.id::text=s.payload->>'source_id' JOIN quickbooks_connections x ON x.id=s.connection_id WHERE s.id::text=$1 AND s.close_id::text=$2 FOR UPDATE OF e`, snapshot.ID, r.PathValue("id")).Scan(&current)
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
		err := db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('connection',(SELECT jsonb_build_object('id',x.id::text,'environment',x.environment,'state',x.state,'realm',x.realm,'company_name',x.company_name,'confirmed_by',x.confirmed_by,'confirmed_at',x.confirmed_at,'error',x.error,'updated_at',x.updated_at) FROM quickbooks_connections x WHERE x.id=`+quickbooks.ReadingConnection("c.id")+`), 'production_connection',(SELECT jsonb_build_object('id',x.id::text,'state',x.state,'realm',x.realm,'company_name',x.company_name,'error',x.error) FROM quickbooks_connections x WHERE x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.environment='production' AND x.state<>'disconnected'), 'snapshots',COALESCE((SELECT jsonb_agg(v ORDER BY (v->>'id')::bigint DESC) FROM (SELECT jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'realm',s.realm,'digest',s.digest,'started_at',s.started_at,'captured_at',s.captured_at,'account_count',jsonb_array_length(s.payload->'accounts'),'actor',s.actor,'period',s.payload->>'period','source_id',s.payload->>'source_id') v FROM quickbooks_snapshots s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 20) recent),'[]'::jsonb)) FROM client_closes c WHERE c.id::text=$1`, r.PathValue("id")).Scan(&raw)
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
		a := auth.Actor(r.Context())
		if a == nil || a.Organization == "" || !slices.Contains(a.Permissions, "integrations:manage") {
			problem(w, 403, "A hosted client-authorized session with integrations:manage is required.")
			return
		}
		var in struct {
			Action      string `json:"action"`
			ID          string `json:"connection_id"`
			Confirmed   bool   `json:"confirmed"`
			Code        string `json:"code"`
			State       string `json:"state"`
			Realm       string `json:"realm"`
			Environment string `json:"environment"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm this connection action and client scope.")
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
			err := db.QueryRowContext(ctx, `WITH l AS (SELECT pg_advisory_xact_lock(hashtext($2||'/'||lower($3)))) INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,environment) SELECT $1::bigint,$2,$3,$4,'authorizing',$5,now()+interval '10 minutes',$6 FROM l ON CONFLICT DO NOTHING RETURNING id::text`, r.PathValue("id"), a.Organization, entity, a.Subject, preparation.Digest([]byte(token)), in.Environment).Scan(&id)
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
		err := db.QueryRowContext(ctx, `SELECT id::text,state,realm,company_name,credential,access_expires_at,environment FROM quickbooks_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state<>'disconnected'`, in.ID, a.Organization, entity).Scan(&id, &state, &realm, &company, &encrypted, &expires, &environment)
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
			if state != "active" {
				problem(w, 409, "Confirm an active company before reading accounts. Incomplete token attempts require reconnecting.")
				return
			}
			var reading bool
			if e := db.QueryRowContext(ctx, `SELECT $1::bigint=`+quickbooks.ReadingConnection("$2::bigint"), id, r.PathValue("id")).Scan(&reading); e != nil || (!reading && in.Action != "refresh") {
				problem(w, 409, "Read this client's books from its production QuickBooks connection.")
				return
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
			name, e := quickbooks.CompanyFor(ctx, environment, realm, credentials.Access)
			if e != nil {
				readFail(e.Error())
				return
			}
			if name != company {
				readFail("Company identity changed. Disconnect and confirm the company again.")
				return
			}
			accounts, e := quickbooks.Accounts(ctx, environment, realm, credentials.Access)
			if e != nil {
				readFail(e.Error())
				return
			}
			snapshot := map[string]any{"environment": environment, "realm": realm, "company_name": company, "accounts": accounts, "started_at": started, "completed_at": time.Now(), "scope": "Account records only; current balances are not a period-end ledger or atomic read."}
			var evidence []byte
			if in.Action == "period_snapshot" {
				reports, readErr := quickbooks.PeriodReports(ctx, environment, realm, credentials.Access, period)
				if readErr != nil {
					readFail(readErr.Error())
					return
				}
				coverage, readErr := quickbooks.Coverage(accounts, reports)
				if readErr != nil {
					readFail("Account coverage could not be read.")
					return
				}
				snapshot["period"], snapshot["basis"], snapshot["reports"], snapshot["coverage"] = period, "Accrual", reports, coverage
				snapshot["completed_at"] = time.Now()
				snapshot["scope"] = "Sequential account and period report reads, not an atomic or independently verified complete ledger. TransactionList contains reported transactions, not full transaction entities. Account CurrentBalance is current, not month-end. Coverage is proposed evidence needs, never approval or completeness."
				full, marshalErr := json.Marshal(snapshot)
				if marshalErr != nil || len(full) > 18*1024*1024 {
					readFail("Period reports exceed the 18 MiB supported snapshot size. No partial ledger was saved.")
					return
				}
				evidence = full
				if len(full) > 96*1024 {
					index := map[string]any{"environment": environment, "realm": realm, "company_name": company, "period": period, "basis": "Accrual", "coverage_item_count": len(coverage), "report_sizes": map[string]int{}, "scope": "Full reports are stored in the immutable snapshot, not this index. Account-scoped ledger pages must be selected separately; no complete ledger or balance is certified."}
					for name, report := range reports {
						index["report_sizes"].(map[string]int)[name] = len(report)
					}
					evidence, e = json.Marshal(index)
					if e != nil {
						readFail("Could not prepare the ledger evidence index.")
						return
					}
				}
			}
			payload, _ := json.Marshal(snapshot)
			tx, e := db.BeginTx(ctx, nil)
			if e != nil {
				problem(w, 503, "Snapshot storage unavailable.")
				return
			}
			defer tx.Rollback()
			// Taken first, as the connect insert does, so the two never wait on each other's row locks.
			if _, e = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1||'/'||lower($2)))`, a.Organization, entity); e != nil {
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
			if e = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&lockedClose); e != nil {
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
				_, e = tx.ExecContext(ctx, `UPDATE preparation_jobs j SET state='waiting_evidence',error='' FROM preparation_intakes i WHERE i.job_id=j.id AND j.close_id=$1 AND j.state='needs_confirmation' AND i.proposal->>'engine'='openai'`, lockedClose)
				if e != nil {
					problem(w, 503, "Could not resume evidence interpretation.")
					return
				}
			}
			var count int
			if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM quickbooks_snapshots WHERE close_id=$1`, r.PathValue("id")).Scan(&count); e != nil || count >= 20 {
				problem(w, 422, "This close has reached its 20-snapshot limit, or storage is unavailable.")
				return
			}
			var sid string
			e = tx.QueryRowContext(ctx, `INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,$3,encode(sha256(convert_to($4::jsonb::text,'UTF8')),'hex'),$4,$5,$6) RETURNING id::text`, r.PathValue("id"), id, realm, string(payload), started, a.Subject).Scan(&sid)
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
	})
}
