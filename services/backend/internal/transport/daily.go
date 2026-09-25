package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
)

// exceptionKey identifies one finding across captures of the same connection.
func exceptionKey(e quickbooks.Exception) string {
	ids := slices.Clone(e.ProviderIDs)
	slices.Sort(ids)
	return e.Type + "|" + e.Kind + "|" + strings.Join(ids, ",")
}

type dailyItem struct {
	CloseID     string                 `json:"close_id"`
	Entity      string                 `json:"entity"`
	Period      string                 `json:"period"`
	SyncID      string                 `json:"sync_id"`
	CapturedAt  time.Time              `json:"captured_at"`
	RequestedBy string                 `json:"requested_by"`
	Total       int                    `json:"total"`
	New         []quickbooks.Exception `json:"new"`
	ReviewedAt  *time.Time             `json:"reviewed_at"`
}

// dailyFindings ranks every accessible unsigned current or prior-month close by the findings captured since its last review.
func dailyFindings(ctx context.Context, db *sql.DB, org string, firm bool, who string) ([]dailyItem, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id::text,c.entity,c.period,s.id::text,s.finished_at,s.requested_by,s.exceptions,
 CASE WHEN b.connection_id=s.connection_id THEN b.exceptions END,d.reviewed_at
 FROM client_closes c
 JOIN LATERAL (SELECT * FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.state IN ('completed','unchanged') AND s.exceptions IS NOT NULL AND s.connection_id=COALESCE(`+quickbooks.ReadingConnection("c.id")+`,s.connection_id) ORDER BY s.id DESC LIMIT 1) s ON true
 LEFT JOIN daily_reviews d ON d.close_id=c.id LEFT JOIN quickbooks_entity_syncs b ON b.id=d.sync_id
 WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND c.period BETWEEN to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND to_char(now() AT TIME ZONE 'UTC','YYYY-MM')
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'`, org, firm, who)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []dailyItem{}
	for rows.Next() {
		var it dailyItem
		var latest, baseline []byte
		if err = rows.Scan(&it.CloseID, &it.Entity, &it.Period, &it.SyncID, &it.CapturedAt, &it.RequestedBy, &latest, &baseline, &it.ReviewedAt); err != nil {
			break
		}
		var found, reviewed []quickbooks.Exception
		json.Unmarshal(latest, &found)
		json.Unmarshal(baseline, &reviewed)
		seen := map[string]bool{}
		for _, e := range reviewed {
			seen[exceptionKey(e)] = true
		}
		it.Total, it.New = len(found), []quickbooks.Exception{}
		for _, e := range found {
			if !seen[exceptionKey(e)] {
				it.New = append(it.New, e)
			}
		}
		items = append(items, it)
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return nil, err
	}
	// Rank before the cutoff so the clients with the most new questions are never hidden.
	slices.SortStableFunc(items, func(a, b dailyItem) int {
		if len(a.New) != len(b.New) {
			return len(b.New) - len(a.New)
		}
		return strings.Compare(a.Entity, b.Entity)
	})
	return items, nil
}

// registerDaily lists, across the firm's unsigned current and prior-month closes, the exceptions
// that transaction captures found since an accountant last reviewed each close.
func registerDaily(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/daily", func(w http.ResponseWriter, r *http.Request) {
		items, err := dailyFindings(r.Context(), db, organization(r), firmAccess(r), subject(r))
		if err != nil {
			problem(w, 503, "Daily books unavailable.")
			return
		}
		more := len(items) > 100
		if more {
			items = items[:100]
		}
		jsonReply(w, 200, map[string]any{"items": items, "has_more": more})
	})
	mux.HandleFunc("POST /v1/closes/{id}/daily-review", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			SyncID string `json:"sync_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Review storage unavailable.")
			return
		}
		defer tx.Rollback()
		// Take the lock a finishing capture takes first; the next statement then sees any capture
		// that finished while this one waited.
		var closeID, latest string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT id::text FROM quickbooks_entity_syncs WHERE close_id=$1::bigint AND state IN ('completed','unchanged') AND exceptions IS NOT NULL AND connection_id=COALESCE(`+quickbooks.ReadingConnection("$1::bigint")+`,connection_id) ORDER BY id DESC LIMIT 1),'')`, closeID).Scan(&latest); err != nil {
			problem(w, 503, "Review storage unavailable.")
			return
		}
		if latest == "" || latest != in.SyncID {
			problem(w, 409, "A newer capture finished. Refresh Daily books and review it.")
			return
		}
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO daily_reviews(close_id,sync_id,reviewed_by) VALUES($1::bigint,$2::bigint,$3) ON CONFLICT(close_id) DO UPDATE SET sync_id=excluded.sync_id,reviewed_by=excluded.reviewed_by,reviewed_at=now()`, r.PathValue("id"), latest, subject(r)); err != nil || tx.Commit() != nil {
			problem(w, 503, "Review could not be saved.")
			return
		}
		jsonReply(w, 200, map[string]string{"close_id": r.PathValue("id"), "sync_id": latest})
	})
}
