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
	var closeID string
	err := db.QueryRowContext(r.Context(), `SELECT x.close_id::text FROM quickbooks_connections x JOIN organizations o ON o.id=x.organization_id WHERE x.oauth_digest=$1 AND x.state='authorizing' AND x.oauth_expires_at>now() AND o.enabled`, preparation.Digest([]byte(q.Get("state")))).Scan(&closeID)
	if err != nil {
		problem(w, 410, "Connection attempt expired or unavailable. Start again from your close.")
		return
	}
	origin, err := url.Parse(os.Getenv("PUBLIC_APP_URL"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") {
		problem(w, 503, "Configure the exact HTTPS application origin.")
		return
	}
	origin.Path = "/dashboard/closes/" + closeID
	origin.Fragment = url.Values{"qbo_code": {q.Get("code")}, "qbo_state": {q.Get("state")}, "qbo_realm": {q.Get("realmId")}}.Encode()
	http.Redirect(w, r, origin.String(), http.StatusSeeOther)
}
func registerQuickBooks(mux *http.ServeMux, db *sql.DB) {
	path := "/v1/closes/{id}/quickbooks"
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		err := db.QueryRowContext(r.Context(), `SELECT jsonb_build_object('connection',(SELECT jsonb_build_object('id',x.id::text,'state',x.state,'realm',x.realm,'company_name',x.company_name,'confirmed_by',x.confirmed_by,'confirmed_at',x.confirmed_at,'error',x.error,'updated_at',x.updated_at) FROM quickbooks_connections x WHERE x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.state<>'disconnected'), 'snapshots',COALESCE((SELECT jsonb_agg(v ORDER BY (v->>'id')::bigint DESC) FROM (SELECT jsonb_build_object('id',s.id::text,'connection_id',s.connection_id::text,'realm',s.realm,'digest',s.digest,'started_at',s.started_at,'captured_at',s.captured_at,'account_count',jsonb_array_length(s.payload->'accounts'),'actor',s.actor,'period',s.payload->>'period','source_id',s.payload->>'source_id') v FROM quickbooks_snapshots s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 20) recent),'[]'::jsonb)) FROM client_closes c WHERE c.id::text=$1`, r.PathValue("id")).Scan(&raw)
		if err != nil {
			problem(w, 503, "QuickBooks status unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"configured": quickbooks.OAuthConfigured(), "hosted": auth.Actor(r.Context()).Organization != "", "data": raw})
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
			Action    string `json:"action"`
			ID        string `json:"connection_id"`
			Confirmed bool   `json:"confirmed"`
			Code      string `json:"code"`
			State     string `json:"state"`
			Realm     string `json:"realm"`
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
			if !quickbooks.OAuthConfigured() {
				problem(w, 503, "Configure the sandbox OAuth application first.")
				return
			}
			if _, err := secrets.Seal(a.Organization+"/qbo-check", nil); err != nil {
				problem(w, 503, err.Error())
				return
			}
			token := rand.Text()
			var id string
			err := db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at) VALUES($1,$2,$3,$4,'authorizing',$5,now()+interval '10 minutes') ON CONFLICT DO NOTHING RETURNING id::text`, r.PathValue("id"), a.Organization, entity, a.Subject, preparation.Digest([]byte(token))).Scan(&id)
			if err != nil {
				problem(w, 409, "A connection or attempt already exists. Refresh; disconnect the old attempt before reconnecting.")
				return
			}
			jsonReply(w, 200, map[string]string{"id": id, "url": quickbooks.AuthorizationURL(token)})
			return
		}
		var id, state, realm, company string
		var encrypted []byte
		var expires sql.NullTime
		err := db.QueryRowContext(ctx, `SELECT id::text,state,realm,company_name,credential,access_expires_at FROM quickbooks_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state<>'disconnected'`, in.ID, a.Organization, entity).Scan(&id, &state, &realm, &company, &encrypted, &expires)
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
			credentials, e := quickbooks.Exchange(ctx, in.Code)
			if e != nil {
				fail(e.Error())
				return
			}
			company, e = quickbooks.Company(ctx, in.Realm, credentials.Access)
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
			result, err = db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='pending_confirmation',realm=$2,company_name=$3,credential=$4,access_expires_at=$5,updated_at=now(),error='' WHERE id=$1 AND state='exchanging'`, id, in.Realm, company, encrypted, time.Now().Add(time.Duration(credentials.Expires)*time.Second))
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
			result, e := db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='active',confirmed_by=$2,confirmed_at=now(),updated_at=now() WHERE id=$1 AND state='pending_confirmation'`, id, a.Subject)
			err = e
			if err == nil {
				n, _ := result.RowsAffected()
				if n != 1 {
					err = fmt.Errorf("connection changed")
				}
			}
		case "snapshot", "period_snapshot":
			if state != "active" {
				problem(w, 409, "Confirm an active company before reading accounts. Incomplete token attempts require reconnecting.")
				return
			}
			raw, e := secrets.Open(aad, encrypted)
			var credentials quickbooks.Credentials
			if e != nil || json.Unmarshal(raw, &credentials) != nil {
				problem(w, 503, "Encrypted connection unavailable; check the server key.")
				return
			}
			if !expires.Valid || time.Until(expires.Time) < time.Minute {
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
				credentials, e = quickbooks.Refresh(ctx, credentials.Refresh)
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
			started := time.Now()
			name, e := quickbooks.Company(ctx, realm, credentials.Access)
			if e != nil {
				readFail(e.Error())
				return
			}
			if name != company {
				readFail("Company identity changed. Disconnect and confirm the company again.")
				return
			}
			accounts, e := quickbooks.Accounts(ctx, realm, credentials.Access)
			if e != nil {
				readFail(e.Error())
				return
			}
			snapshot := map[string]any{"environment": "sandbox", "realm": realm, "company_name": company, "accounts": accounts, "started_at": started, "completed_at": time.Now(), "scope": "Account records only; current balances are not a period-end ledger or atomic read."}
			var evidence []byte
			if in.Action == "period_snapshot" {
				reports, readErr := quickbooks.PeriodReports(ctx, realm, credentials.Access, period)
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
				snapshot["scope"] = "Sequential sandbox account and period report reads, not an atomic or independently verified complete ledger. TransactionList contains reported transactions, not full transaction entities. Account CurrentBalance is current, not month-end. Coverage is proposed evidence needs, never approval or completeness."
				evidence, e = json.Marshal(snapshot)
				// ponytail: one bounded evidence bundle fits the existing 128 KiB agent budget with room for invoices. No silent truncation.
				if e != nil || len(evidence) > 96*1024 {
					readFail("Period evidence exceeds 96 KiB. No snapshot saved; use scoped exports and manual preparation for this close.")
					return
				}
			}
			payload, _ := json.Marshal(snapshot)
			tx, e := db.BeginTx(ctx, nil)
			if e != nil {
				problem(w, 503, "Snapshot storage unavailable.")
				return
			}
			defer tx.Rollback()
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
			if evidence != nil {
				var newer bool
				if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quickbooks_snapshots WHERE close_id=$1 AND payload ? 'period' AND started_at>$2)`, lockedClose, started).Scan(&newer); e != nil || newer {
					problem(w, 409, "A newer ledger capture already exists. Refresh coverage; this older read was not saved.")
					return
				}
				var count, size int
				if e = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))),0) FROM evidence WHERE close_id=$1`, lockedClose).Scan(&count, &size); e != nil || count >= 100 || size+len(evidence) > 20*1024*1024 {
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
