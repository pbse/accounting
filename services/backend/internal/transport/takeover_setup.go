package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

// takeoverContent is what a reviewer confirms when taking a client over: the close, the ledger company its books are read
// from, the firm procedures in force for it (client overrides are its exceptions, the basis among them), who is assigned to
// it, the account scope proposed from this month's capture, and the monthly hand-over. It holds no times.
func takeoverContent(ctx context.Context, tx *sql.Tx, closeID string) (map[string]any, string, []string, error) {
	var raw []byte
	err := tx.QueryRowContext(ctx, `SELECT jsonb_build_object('close',jsonb_build_object('id',c.id::text,'entity',c.entity,'period',c.period),
 'companies',(SELECT COALESCE(jsonb_agg(x ORDER BY x.provider),'[]'::jsonb) FROM (
  SELECT 'QuickBooks' AS provider,q.company_name AS name,q.realm AS company,q.environment,q.state FROM quickbooks_connections q WHERE q.id=`+quickbooks.ReadingConnection("c.id")+`
  UNION ALL SELECT 'Xero',x.tenant_name,COALESCE(x.tenant_id,''),'',x.state FROM xero_connections x WHERE x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.state<>'disconnected') x),
 'procedures',(SELECT COALESCE(jsonb_agg(jsonb_build_object('id',p.id::text,'kind',p.kind,'method',p.method,'enabled',p.enabled,'scope',CASE WHEN p.client_entity<>'' THEN 'client' ELSE 'firm' END,'expense_account',p.expense_account,'prepaid_account',p.prepaid_account) ORDER BY p.kind),'[]'::jsonb)
  FROM (SELECT DISTINCT ON (p.kind) p.* FROM procedure_versions p WHERE p.organization_id=c.organization_id AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY p.kind,(p.client_entity<>'') DESC,p.id DESC) p),
 'assigned',(SELECT COALESCE(jsonb_agg(jsonb_build_object('subject',g.subject,'member',COALESCE(m.granted,false)) ORDER BY g.subject),'[]'::jsonb)
  FROM current_client_access g LEFT JOIN current_organization_members m ON m.organization_id=g.organization_id AND m.subject=g.subject WHERE g.organization_id=c.organization_id AND lower(g.client_entity)=lower(c.entity) AND g.granted),
 'routine',(SELECT jsonb_build_object('finish',r.finish,'due_days',r.due_days) FROM close_routines r WHERE r.organization_id=c.organization_id AND lower(r.client_entity)=lower(c.entity) AND r.stopped_at IS NULL))
 FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&raw)
	if err != nil {
		return nil, "", nil, err
	}
	var content map[string]any
	var check struct {
		Companies  []struct{ Provider, State string }
		Procedures []struct{ Kind, Method string }
	}
	if err = json.Unmarshal(raw, &content); err == nil {
		err = json.Unmarshal(raw, &check)
	}
	if err != nil {
		return nil, "", nil, err
	}
	scope := map[string]any{}
	view, err := currentCoverage(ctx, tx, closeID)
	read := true
	if err == nil && view.SnapshotID != "" {
		err = tx.QueryRowContext(ctx, `SELECT COALESCE(s.connection_id=`+quickbooks.ReadingConnection("c.id")+` AND s.payload->>'period'=c.period,false) FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE s.id::text=$1`, view.SnapshotID).Scan(&read)
	}
	switch {
	case errors.Is(err, errCoverageLimit):
		scope["unavailable"] = err.Error() + "."
	case err != nil:
		return nil, "", nil, err
	case view.SnapshotID == "":
		scope["unavailable"] = "No QuickBooks capture of this month yet: the account scope is proposed from one (not available for Xero)."
	case view.Stale:
		scope["unavailable"] = "Capture this month again: the account scope is proposed from a current capture."
	case !read:
		scope["unavailable"] = "This month's capture is not of the company the books are read from: capture this month again."
	default:
		items := []map[string]string{}
		for _, item := range view.Items {
			items = append(items, map[string]string{"name": item.Name, "state": item.State})
		}
		scope["digest"], scope["items"] = view.Digest, items
	}
	content["scope"] = scope
	problems := []string{}
	switch {
	case len(check.Companies) == 0:
		problems = append(problems, "Connect the client's QuickBooks or Xero company: the takeover confirms the company its books are read from.")
	case len(check.Companies) > 1:
		problems = append(problems, "More than one ledger company is connected for this client; disconnect the one its books are not kept in.")
	case check.Companies[0].State != "active":
		problems = append(problems, "Reconnect "+check.Companies[0].Provider+": its connection is "+strings.ReplaceAll(check.Companies[0].State, "_", " ")+".")
	}
	if !slices.ContainsFunc(check.Procedures, func(p struct{ Kind, Method string }) bool { return p.Kind == "basis" && p.Method != "" }) {
		problems = append(problems, "Record the client's accounting basis under Approved procedures and client overrides.")
	}
	saved, _ := json.Marshal(content)
	return content, preparation.Digest(saved), problems, nil
}

// validTimezone is an IANA timezone name; the server's own ("Local") and the empty name are not a client's.
func validTimezone(name string) bool {
	if name == "" || name == "Local" || len(name) > 64 {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// registerTakeoverSetup confirms, once, how a client's opening month is taken over.
func registerTakeoverSetup(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/takeover-setup", func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent snapshot.")
			return
		}
		defer tx.Rollback()
		var takeover bool
		if err = tx.QueryRowContext(r.Context(), `SELECT takeover FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&takeover); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if !takeover {
			jsonReply(w, 200, map[string]any{"takeover": false})
			return
		}
		content, digest, problems, err := takeoverContent(r.Context(), tx, r.PathValue("id"))
		var setups []byte
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',id::text,'digest',digest,'timezone',timezone,'confirmed_by',confirmed_by,'confirmed_at',confirmed_at) ORDER BY id DESC),'[]'::jsonb) FROM takeover_setups WHERE close_id::text=$1`, r.PathValue("id")).Scan(&setups)
		}
		if err != nil {
			problem(w, 503, "Takeover setup unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"takeover": true, "content": content, "digest": digest, "problems": problems, "setups": json.RawMessage(setups), "you": yourPermissions(r)})
	})
	mux.HandleFunc("POST /v1/closes/{id}/takeover-setup", func(w http.ResponseWriter, r *http.Request) {
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		var in struct {
			Digest    string `json:"digest"`
			Timezone  string `json:"timezone"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !in.Confirmed {
			problem(w, 422, "Confirm the takeover setup.")
			return
		}
		if !validTimezone(in.Timezone) {
			problem(w, 422, "Choose the client's timezone by its IANA name, for example America/New_York.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Takeover setup unavailable.")
			return
		}
		defer tx.Rollback()
		// The firm, then the close: the order creating a close uses when it moves the opening month.
		var org string
		var takeover bool
		err = tx.QueryRowContext(r.Context(), `SELECT o.id FROM organizations o JOIN client_closes c ON c.organization_id=o.id WHERE c.id::text=$1 FOR SHARE OF o`, r.PathValue("id")).Scan(&org)
		if err == nil {
			err = tx.QueryRowContext(r.Context(), `SELECT takeover FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&takeover)
		}
		if err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if !takeover {
			problem(w, 409, "Only a client's first close in the firm has a takeover setup.")
			return
		}
		content, digest, problems, err := takeoverContent(r.Context(), tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "Takeover setup unavailable.")
			return
		}
		if in.Digest != digest {
			problem(w, 409, "The setup changed since you looked. Review it again.")
			return
		}
		if len(problems) > 0 {
			problem(w, 409, problems[0])
			return
		}
		var newest bool
		if err = tx.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM takeover_setups WHERE id=(SELECT max(id) FROM takeover_setups WHERE close_id::text=$1) AND digest=$2 AND timezone=$3)`, r.PathValue("id"), digest, in.Timezone).Scan(&newest); err != nil {
			problem(w, 503, "Takeover setup unavailable.")
			return
		}
		if newest {
			problem(w, 409, "This takeover setup is already confirmed.")
			return
		}
		saved, _ := json.Marshal(content)
		var id string
		if err = tx.QueryRowContext(r.Context(), `INSERT INTO takeover_setups(organization_id,close_id,digest,content,timezone,confirmed_by) VALUES($1,$2::bigint,$3,$4,$5,$6) RETURNING id::text`, org, r.PathValue("id"), digest, string(saved), in.Timezone, who).Scan(&id); err == nil {
			// A client taken over joins the firm's roster, owing a close each month from its opening month.
			_, err = tx.ExecContext(r.Context(), `INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) SELECT c.organization_id,c.entity,true,c.period,'Taken over from '||c.period,$2
 FROM client_closes c WHERE c.id::text=$1 AND NOT EXISTS(SELECT 1 FROM current_client_roster r WHERE r.organization_id=c.organization_id AND lower(r.client_entity)=lower(c.entity) AND r.active)`, r.PathValue("id"), who)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The takeover setup could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
}
