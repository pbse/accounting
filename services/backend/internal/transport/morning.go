package transport

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// morningScope is SQL for the accessible, unsigned current and prior-month closes, as Daily Books reads them.
const morningScope = `c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND c.period BETWEEN to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND to_char(now() AT TIME ZONE 'UTC','YYYY-MM')
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'`

// morning says what the agent did in the past day across the firm's open closes and which closes need a person today.
func morning(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		args := []any{organization(r), firmAccess(r), subject(r)}
		var snapshots, drafts, standing int
		err := db.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE `+morningScope+` AND s.actor='scheduled-quickbooks-refresh' AND s.payload ? 'period' AND s.captured_at>now()-interval '24 hours')
 +(SELECT count(*) FROM xero_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE `+morningScope+` AND s.actor='scheduled-xero-refresh' AND s.captured_at>now()-interval '24 hours'),
 (SELECT count(*) FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE `+morningScope+` AND w.created_by IS NULL AND COALESCE(w.paper->>'revises','')='' AND w.created_at>now()-interval '24 hours'),
 (SELECT count(*) FROM workpaper_reviews v JOIN workpapers w ON w.id=v.workpaper_id JOIN client_closes c ON c.id=w.close_id JOIN standing_approvals a ON v.note='Standing approval #'||a.id||$4 AND v.actor=a.granted_by
  WHERE `+morningScope+` AND v.decision='approved' AND v.created_at>now()-interval '24 hours')`, append(args, standingNote)...).Scan(&snapshots, &drafts, &standing)
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}

		type close struct {
			CloseID string   `json:"close_id"`
			Entity  string   `json:"entity"`
			Period  string   `json:"period"`
			Needs   []string `json:"needs"`
			Count   int      `json:"count"`
		}
		rows, err := db.QueryContext(ctx, `SELECT c.id::text,c.entity,c.period,
 d.ready,d.attention,
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='needs_confirmation'),
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='failed'),
 c.period<to_char(now() AT TIME ZONE 'UTC','YYYY-MM') AND NOT EXISTS(SELECT 1 FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+`) AND NOT EXISTS(SELECT 1 FROM xero_snapshots s WHERE s.close_id=c.id)
 FROM client_closes c
 CROSS JOIN LATERAL (SELECT count(*) FILTER (WHERE x.review_state='pending' AND sourced AND NOT blocked) AS ready,count(*) FILTER (WHERE x.review_state IN ('changes_requested','source_changed') OR blocked) AS attention
  FROM (SELECT x.review_state,COALESCE(jsonb_typeof(x.paper->'sources')='array' AND x.paper->'sources'<>'[]'::jsonb,false) AS sourced,COALESCE(jsonb_typeof(x.paper->'blockers')='array' AND x.paper->'blockers'<>'[]'::jsonb,false) AS blocked
   FROM workpaper_review_state x WHERE x.close_id=c.id AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=x.close_id AND n.paper->>'revises'=x.id::text)) x) d
 WHERE `+morningScope+` ORDER BY c.id`, args...)
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		closes := []close{}
		for rows.Next() {
			var c close
			var ready, attention, confirm, failed int
			var noSnapshot bool
			if err = rows.Scan(&c.CloseID, &c.Entity, &c.Period, &ready, &attention, &confirm, &failed, &noSnapshot); err != nil {
				break
			}
			c.Needs = []string{}
			for _, n := range []struct {
				count int
				need  string
			}{{ready, "approve " + plural(ready, "draft", "drafts")}, {confirm, "confirm " + plural(confirm, "document", "documents")}, {attention, "fix " + plural(attention, "draft", "drafts")}, {failed, "retry " + plural(failed, "failed document", "failed documents")}} {
				if n.count > 0 {
					c.Needs = append(c.Needs, n.need)
					c.Count += n.count
				}
			}
			if noSnapshot {
				c.Needs = append(c.Needs, "no month-end snapshot yet")
				c.Count++
			}
			closes = append(closes, c)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		// The latest automatic check of the current agent on a close this person can open; counts only, no client names.
		var check map[string]any
		var model, checkedClose string
		var items []byte
		var checked time.Time
		err = db.QueryRowContext(ctx, `SELECT r.model,r.close_id::text,r.items,r.created_at FROM replay_runs r JOIN client_closes c ON c.id=r.close_id
 WHERE r.organization_id=$1 AND r.actor='scheduled-replay' AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) ORDER BY r.id DESC LIMIT 1`, args...).Scan(&model, &checkedClose, &items, &checked)
		if err != nil && err != sql.ErrNoRows {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		if err == nil {
			var readings []struct {
				Kept  bool
				Error string
			}
			json.Unmarshal(items, &readings)
			kept, scored := 0, 0
			for _, reading := range readings {
				if reading.Error == "" {
					scored++
					if reading.Kept {
						kept++
					}
				}
			}
			check = map[string]any{"model": model, "close_id": checkedClose, "kept": kept, "scored": scored, "checked_at": checked}
		}
		daily, err := dailyFindings(ctx, db, organization(r), firmAccess(r), subject(r))
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		for _, d := range daily {
			if i := slices.IndexFunc(closes, func(c close) bool { return c.CloseID == d.CloseID }); i >= 0 && len(d.New) > 0 {
				closes[i].Needs = append(closes[i].Needs, plural(len(d.New), "new client question", "new client questions")+" since last review")
				closes[i].Count += len(d.New)
			}
		}
		needing := slices.DeleteFunc(slices.Clone(closes), func(c close) bool { return c.Count == 0 })
		slices.SortStableFunc(needing, func(a, b close) int {
			if a.Count != b.Count {
				return b.Count - a.Count
			}
			return strings.Compare(a.Entity, b.Entity)
		})
		shown := needing[:min(20, len(needing))]
		jsonReply(w, 200, map[string]any{"since": time.Now().UTC().Add(-24 * time.Hour), "overnight": map[string]int{"month_end_snapshots": snapshots, "drafts_prepared": drafts, "standing_approvals": standing},
			"closes": shown, "needing": len(needing), "quiet": len(closes) - len(needing), "model_check": check})
	}
}
