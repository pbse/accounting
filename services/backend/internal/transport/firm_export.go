package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"
)

// firmExportTables are the firm's own records outside any close's package, each ordered by its key, with the columns
// left out (secrets).
var firmExportTables = []struct{ table, order string }{
	{"organization_member_access", "id"}, {"member_role_changes", "id"}, {"client_access", "id"}, {"agent_access", "id"}, {"organization_policy_changes", "id"},
	{"procedure_versions", "id"}, {"procedure_drafts", "id"}, {"client_aliases", "confirmed_at"}, {"close_routines", "id"}, {"client_roster", "id"}, {"close_templates", "id"}, {"client_ledgers", "id"},
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
