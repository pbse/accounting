package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
)

// firmExportTables are the firm's own records outside any close's package, each ordered by its key, with the columns
// left out (secrets).
var firmExportTables = []struct{ table, order string }{
	{"organization_member_access", "id"}, {"member_role_changes", "id"}, {"client_access", "id"}, {"agent_access", "id"}, {"organization_policy_changes", "id"},
	{"procedure_versions", "id"}, {"procedure_drafts", "id"}, {"client_aliases", "confirmed_at"}, {"close_routines", "id"}, {"client_roster", "id"}, {"close_templates", "id"}, {"client_profiles", "id"}, {"client_ledgers", "id"},
	{"takeover_setups", "id"}, {"opening_baselines", "id"}, {"opening_cleanup_resolutions", "id"},
	{"budget_versions", "id"}, {"cash_scenarios", "id"}, {"cash_obligations", "id"}, {"standing_approvals", "id"}, {"open_item_actions", "id"},
	{"customer_contact_authority", "id"}, {"collection_contact_preferences", "id"}, {"email_settlements", "id"},
	{"production_posting_grants", "id"}, {"xero_posting_grants", "id"}, {"operation_acceptances", "id"},
}

// firmExportLimit refuses an export rather than cutting it short.
const firmExportLimit = 64 << 20

// registerFirmExport gives a firm's data administrator its firm-level records in one JSON file at exit: its settings,
// people and access history, procedures, client records, and each close with where its own package downloads.
func registerFirmExport(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/firm-export", func(w http.ResponseWriter, r *http.Request) {
		if !canManageData(r) {
			problem(w, 403, "The firm export requires clients:all and data:manage.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent snapshot.")
			return
		}
		defer tx.Rollback()
		org := organization(r)
		out := map[string]json.RawMessage{}
		read := func(key, query string, args ...any) bool {
			var raw []byte
			if err := tx.QueryRowContext(r.Context(), query, args...).Scan(&raw); err != nil {
				problem(w, 503, "The firm export could not be read.")
				return false
			}
			out[key] = raw
			return true
		}
		if !read("firm", `SELECT to_jsonb(o) FROM organizations o WHERE o.id=$1`, org) ||
			!read("email_senders", `SELECT COALESCE(jsonb_agg(to_jsonb(c)-'credential'-'webhook_secret' ORDER BY c.id),'[]') FROM email_connections c WHERE c.organization_id=$1`, org) ||
			!read("connections", securityServices, org) ||
			!read("closes", `SELECT COALESCE(jsonb_agg(jsonb_build_object('id',c.id::text,'entity',c.entity,'period',c.period,'created_at',c.created_at,
 'package','/v1/closes/'||c.id||'/export','binder','/v1/closes/'||c.id||'/binder') ORDER BY c.entity,c.period,c.id),'[]') FROM client_closes c WHERE c.organization_id=$1`, org) {
			return
		}
		// Each client's files are listed without their bytes, with where each downloads as it was sent.
		files := []map[string]any{}
		rows, err := tx.QueryContext(r.Context(), `SELECT to_jsonb(i),i.id::text,i.client_entity FROM (SELECT id,organization_id,client_entity,period,name,digest,format,replaces,replaces_reason,holds,actor,created_at
 FROM books_imports WHERE organization_id=$1) i ORDER BY i.id`, org)
		if err != nil {
			problem(w, 503, "The firm export could not be read.")
			return
		}
		for rows.Next() {
			var raw []byte
			var id, client string
			if err = rows.Scan(&raw, &id, &client); err != nil {
				break
			}
			row := map[string]any{}
			json.Unmarshal(raw, &row)
			row["download"] = "/v1/closes/client/import/file?name=" + url.QueryEscape(client) + "&id=" + id
			files = append(files, row)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			problem(w, 503, "The firm export could not be read.")
			return
		}
		out["books_imports"], _ = json.Marshal(files)
		// Each confirmed layout without what it read, which can be large, with where that downloads.
		layouts := []map[string]any{}
		rows, err = tx.QueryContext(r.Context(), `SELECT to_jsonb(l),l.id::text,l.client_entity FROM (SELECT id,organization_id,client_entity,import_id,layout,reason,actor,created_at,encoding,delimiter,
 COALESCE((reading->>'balanced')::boolean,false) AS balanced,COALESCE(jsonb_array_length(reading->'problems'),0) AS problems FROM books_layouts WHERE organization_id=$1) l ORDER BY l.id`, org)
		if err != nil {
			problem(w, 503, "The firm export could not be read.")
			return
		}
		for rows.Next() {
			var raw []byte
			var id, client string
			if err = rows.Scan(&raw, &id, &client); err != nil {
				break
			}
			row := map[string]any{}
			json.Unmarshal(raw, &row)
			row["reading"] = "/v1/closes/client/layout?name=" + url.QueryEscape(client) + "&id=" + id
			layouts = append(layouts, row)
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
		if err != nil {
			problem(w, 503, "The firm export could not be read.")
			return
		}
		out["books_layouts"], _ = json.Marshal(layouts)
		for _, t := range firmExportTables {
			if !read(t.table, `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.`+t.order+`),'[]') FROM `+t.table+` t WHERE t.organization_id=$1`, org) {
				return
			}
		}
		body, err := json.Marshal(map[string]any{"version": "firm_export_v1", "exported_at": time.Now().UTC(), "records": out})
		if err != nil {
			problem(w, 503, "The firm export could not be written.")
			return
		}
		if len(body) > firmExportLimit {
			problem(w, 413, "The firm's records exceed the 64 MiB export limit. Nothing was left out silently; contact support.")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="firm-export-`+time.Now().UTC().Format("2006-01-02")+`.json"`)
		w.Write(body)
	})
}
