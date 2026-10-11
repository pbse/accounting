package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

// Operations screens of the staff console (docs/admin-panel.md, phase A2): background work that is stuck or failed, and
// provider connections, across firms. Both are read-only and carry identifiers, states and times, never client names:
// the firm's own screens act on them.
func registerAdminOperations(mux *http.ServeMux, db *sql.DB, token string) {
	read := func(next func(http.ResponseWriter, *http.Request, *sql.DB)) http.HandlerFunc {
		return adminGuard(token, db, platformRead, func(w http.ResponseWriter, r *http.Request) { next(w, r, db) })
	}
	mux.HandleFunc("GET /v1/admin/jobs", read(adminJobs))
	mux.HandleFunc("GET /v1/admin/connections", read(adminConnections))
	mux.HandleFunc("GET /v1/admin/books", read(adminBooks))
	mux.HandleFunc("GET /v1/admin/ledgers", read(adminLedgers))
}

// adminFilters reads a list's short filters; a value longer than 100 characters answers 422 and returns false.
func adminFilters(w http.ResponseWriter, r *http.Request, names ...string) ([]any, bool) {
	values := make([]any, len(names))
	for i, name := range names {
		v := strings.TrimSpace(r.URL.Query().Get(name))
		if len(v) > 100 {
			problem(w, 422, "The "+name+" filter is too long.")
			return nil, false
		}
		values[i] = v
	}
	return values, true
}

// adminJobs is the firm operations screen's stuck and failed work (operationsAlerts) for every firm: counts by kind, and
// the items oldest first, filtered by kind and firm.
func adminJobs(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	filters, ok := adminFilters(w, r, "kind", "firm")
	if !ok {
		return
	}
	offset, ok := adminOffset(w, r)
	if !ok {
		return
	}
	var kinds, items []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH a AS (SELECT a.*,o.name AS firm_name,o.enabled FROM (`+operationsAlerts+`) a JOIN organizations o ON o.id=a.organization
  WHERE o.id<>'local-development' AND ($2='' OR a.organization=$2))
 SELECT COALESCE((SELECT jsonb_agg(k ORDER BY k.items DESC,k.kind) FROM (SELECT kind,count(*) AS items,count(DISTINCT organization) AS firms,max(age_minutes) AS oldest_minutes FROM a GROUP BY kind) k),'[]'::jsonb),
  COALESCE((SELECT jsonb_agg(i ORDER BY i.age_minutes DESC,i.kind,i.item_id) FROM (SELECT organization,firm_name,enabled,kind,close_id,item_id,state,age_minutes FROM a
   WHERE $3='' OR kind=$3 ORDER BY age_minutes DESC,kind,item_id LIMIT $4 OFFSET $5) i),'[]'::jsonb)`, "", filters[1], filters[0], adminPage+1, offset).Scan(&kinds, &items)
	})
	if err != nil {
		adminFirmProblem(w, err, "jobs read")
		return
	}
	var list []json.RawMessage
	json.Unmarshal(items, &list)
	more := len(list) > adminPage
	if more {
		list = list[:adminPage]
	}
	jsonReply(w, 200, map[string]any{"kinds": json.RawMessage(kinds), "items": list, "has_more": more, "next_offset": offset + len(list)})
}

// adminConnectionRows is every firm's provider connections with their state and when the app last captured through each:
// a snapshot for QuickBooks and Xero, a saved report for Stripe, an accepted email for the email sender.
const adminConnectionRows = `SELECT x.*,o.name AS firm_name FROM (
 SELECT 'quickbooks' AS provider,c.organization_id AS organization,c.close_id::text AS close_id,c.state,c.environment AS mode,c.created_at AS connected_at,c.updated_at,
  (SELECT max(s.captured_at) FROM quickbooks_snapshots s WHERE s.connection_id=c.id) AS last_capture FROM quickbooks_connections c
 UNION ALL SELECT 'xero',c.organization_id,c.close_id::text,c.state,'',c.created_at,c.updated_at,(SELECT max(s.captured_at) FROM xero_snapshots s WHERE s.connection_id=c.id) FROM xero_connections c
 UNION ALL SELECT 'stripe',c.organization_id,c.close_id::text,c.state,CASE WHEN c.livemode THEN 'live' ELSE 'test' END,c.created_at,c.updated_at,
  (SELECT max(r.finished_at) FROM stripe_report_runs r WHERE r.connection_id=c.id AND r.state='saved') FROM stripe_connections c
 UNION ALL SELECT 'email',c.organization_id,'','active',c.provider,c.created_at,c.verified_at,(SELECT max(m.finished_at) FROM email_outbox m WHERE m.connection_id=c.id AND m.state='accepted')
  FROM email_connections c WHERE c.enabled
) x JOIN organizations o ON o.id=x.organization WHERE o.id<>'local-development'`

// adminConnections is connections across firms: counts by provider and state, and the connections themselves, those
// needing a reconnect first, filtered by provider, state and firm.
func adminConnections(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	filters, ok := adminFilters(w, r, "provider", "state", "firm")
	if !ok {
		return
	}
	offset, ok := adminOffset(w, r)
	if !ok {
		return
	}
	var counts, rows []byte
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `WITH c AS (`+adminConnectionRows+`)
 SELECT COALESCE((SELECT jsonb_agg(k ORDER BY k.provider,k.state) FROM (SELECT provider,state,count(*) AS connections FROM c WHERE $3='' OR organization=$3 GROUP BY 1,2) k),'[]'::jsonb),
  COALESCE((SELECT jsonb_agg(r ORDER BY r.state='reconnect_required' DESC,r.updated_at DESC,r.provider,r.close_id) FROM (SELECT * FROM c
   WHERE ($1='' OR provider=$1) AND ($2='' OR state=$2) AND ($3='' OR organization=$3) ORDER BY state='reconnect_required' DESC,updated_at DESC,provider,close_id LIMIT $4 OFFSET $5) r),'[]'::jsonb)`,
			filters[0], filters[1], filters[2], adminPage+1, offset).Scan(&counts, &rows)
	})
	if err != nil {
		adminFirmProblem(w, err, "connections read")
		return
	}
	var list []json.RawMessage
	json.Unmarshal(rows, &list)
	more := len(list) > adminPage
	if more {
		list = list[:adminPage]
	}
	jsonReply(w, 200, map[string]any{"counts": json.RawMessage(counts), "connections": list, "has_more": more, "next_offset": offset + len(list)})
}
