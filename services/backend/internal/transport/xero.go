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
				out["latest"] = map[string]any{"currency": currency, "date": date, "rows": rows}
			}
		}
		return out, nil
	}
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
		var id, state, tenant string
		var tenants json.RawMessage
		var encrypted []byte
		var expires sql.NullTime
		if err := db.QueryRowContext(ctx, `SELECT id::text,state,tenant_id,tenants,credential,access_expires_at FROM xero_connections WHERE id::text=$1 AND organization_id=$2 AND lower(client_entity)=lower($3) AND state<>'disconnected'`, in.ID, a.Organization, entity).Scan(&id, &state, &tenant, &tenants, &encrypted, &expires); err != nil {
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
			if changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='active',tenant_id=$2,tenant_name=$3,confirmed_by=$4,confirmed_at=now(),updated_at=now() WHERE id=$1 AND state='choose_tenant'`, id, list[i].ID, list[i].Name, a.Subject)) {
				return
			}
		case "snapshot":
			if state != "active" {
				problem(w, 409, "Confirm the client's Xero organisation before capturing.")
				return
			}
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(95 * time.Second))
			raw, err := secrets.Open(aad, encrypted)
			var credentials xero.Credentials
			if err != nil || json.Unmarshal(raw, &credentials) != nil {
				problem(w, 503, "Encrypted connection unavailable; check the server key.")
				return
			}
			if !expires.Valid || time.Until(expires.Time) < 2*time.Minute {
				if changed(db.ExecContext(ctx, `UPDATE xero_connections SET state='refreshing',updated_at=now() WHERE id=$1 AND state='active' AND access_expires_at IS NOT DISTINCT FROM $2`, id, expires)) {
					return
				}
				if credentials, err = xero.Refresh(ctx, credentials.Refresh); err != nil {
					fail(err.Error())
					return
				}
				raw, _ = json.Marshal(credentials)
				sealed, err := secrets.Seal(aad, raw)
				if err != nil {
					fail(err.Error())
					return
				}
				// Xero has already retired the old refresh token; the new one must be saved first.
				result, err := db.ExecContext(ctx, `UPDATE xero_connections SET credential=$2,access_expires_at=$3,state='active',updated_at=now() WHERE id=$1 AND state='refreshing'`, id, sealed, time.Now().Add(time.Duration(credentials.Expires)*time.Second))
				if err != nil {
					fail("Rotated Xero credentials could not be saved. Connect again before continuing.")
					return
				}
				if changed(result, nil) {
					return
				}
			}
			month, err := time.Parse("2006-01", period)
			if err != nil {
				problem(w, 422, "This close has no valid month.")
				return
			}
			payload, err := xero.Capture(ctx, credentials.Access, tenant, month.AddDate(0, 1, -1))
			if err != nil {
				db.ExecContext(ctx, `UPDATE xero_connections SET error=$2,updated_at=now() WHERE id=$1`, id, err.Error())
				problem(w, 422, err.Error())
				return
			}
			currency, fiscalStart, err := xero.Profile(payload)
			if err != nil {
				problem(w, 422, err.Error())
				return
			}
			if _, err = db.ExecContext(ctx, `WITH s AS (INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor) VALUES($1,$2,$3,$4,encode(sha256(convert_to($5::jsonb::text,'UTF8')),'hex'),$5,$6))
 UPDATE xero_connections SET home_currency=$7,fiscal_year_start=$8 WHERE id=$2`, r.PathValue("id"), id, tenant, period, string(payload), a.Subject, currency, fiscalStart); err != nil {
				problem(w, 503, "The Trial Balance was read but could not be saved. Capture again.")
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
