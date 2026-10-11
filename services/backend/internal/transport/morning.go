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

// obligation is an accessible close up to this month, or due within a week, whose sign-off does not stand: never
// signed off, reopened by a person, changed since its sign-off (reopen_required) or not rechecked (unknown).
type obligation struct {
	CloseID string `json:"close_id"`
	Entity  string `json:"entity"`
	Period  string `json:"period"`
	DueOn   string `json:"due_on"`
	State   string `json:"state"`
}

// outstanding lists the firm's close obligations matching client (a name fragment or YYYY-MM, or ""), newest period
// first, in the caller's snapshot as of now. A recorded sign-off counts only while closeReadiness still says signed_off,
// as the sign-off goal checks it; a recheck that fails leaves the close outstanding with its state unknown.
// ponytail: rechecks every signed-off close on each read; keep each sign-off's last recheck if portfolios outgrow it.
func outstanding(ctx context.Context, tx *sql.Tx, now time.Time, org string, firm bool, who, client string) ([]obligation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.entity,c.period,COALESCE(c.due_on::text,''),COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')='signed_off'
 FROM client_closes c WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND ($4='' OR c.entity ILIKE '%'||$4||'%' OR c.period=$4) AND (c.period<=$5 OR c.due_on<=$6::date) ORDER BY c.period DESC,c.id DESC`, org, firm, who, client, now.Format("2006-01"), now.AddDate(0, 0, 7).Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	open, signed := []obligation{}, []obligation{}
	for rows.Next() {
		var o obligation
		var signedOff bool
		if err = rows.Scan(&o.CloseID, &o.Entity, &o.Period, &o.DueOn, &signedOff); err != nil {
			rows.Close()
			return nil, err
		}
		if o.State = "open"; signedOff {
			signed = append(signed, o)
		} else {
			open = append(open, o)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, o := range signed {
		if o.State, err = recheckSignOff(ctx, tx, o.CloseID); err != nil {
			return nil, err
		}
		if o.State != "signed_off" {
			open = append(open, o)
		}
	}
	return open, nil
}

// recheckSignOff is closeReadiness's state for a close with a recorded sign-off, read in the caller's snapshot:
// signed_off while it stands, reopen_required after a change, or unknown when readiness cannot be read. The error is
// only for a transaction that can no longer be used.
func recheckSignOff(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT recheck`); err != nil {
		return "", err
	}
	current, err := closeReadiness(ctx, tx, id)
	state := current.State
	if err != nil {
		state = "unknown"
		if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT recheck`); err != nil {
			return "", err
		}
	}
	_, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT recheck`)
	return state, err
}

// morning says what the agent did in the past day across the firm's outstanding closes, which closes need a person
// today and which are due within a week.
func morning(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		client := strings.TrimSpace(r.URL.Query().Get("client"))
		if len(client) > 200 {
			problem(w, 422, "Client search is too long.")
			return
		}
		// One snapshot and one clock: the cohort, its sign-off rechecks and every count below agree.
		now := time.Now().UTC()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		defer tx.Rollback()
		owed, err := outstanding(ctx, tx, now, organization(r), firmAccess(r), subject(r), client)
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		ids, byID := make([]string, len(owed)), map[string]obligation{}
		for i, o := range owed {
			ids[i], byID[o.CloseID] = o.CloseID, o
		}
		const morningScope = `c.id::text=ANY($1::text[])`
		var snapshots, drafts, standing int
		err = tx.QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE `+morningScope+` AND s.actor='scheduled-quickbooks-refresh' AND s.payload ? 'period' AND s.captured_at>now()-interval '24 hours')
 +(SELECT count(*) FROM xero_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE `+morningScope+` AND s.actor='scheduled-xero-refresh' AND s.captured_at>now()-interval '24 hours'),
 (SELECT count(*) FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE `+morningScope+` AND w.created_by IS NULL AND COALESCE(w.paper->>'revises','')='' AND w.created_at>now()-interval '24 hours'),
 (SELECT count(*) FROM workpaper_reviews v JOIN workpapers w ON w.id=v.workpaper_id JOIN client_closes c ON c.id=w.close_id JOIN standing_approvals a ON v.note='Standing approval #'||a.id||$2 AND v.actor=a.granted_by
  WHERE `+morningScope+` AND v.decision='approved' AND v.created_at>now()-interval '24 hours')`, ids, standingNote).Scan(&snapshots, &drafts, &standing)
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
		rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.entity,c.period,
 d.ready,d.attention,
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='needs_confirmation'),
 (SELECT count(*) FROM preparation_jobs j WHERE j.close_id=c.id AND j.state='failed'),
 c.period<to_char(now() AT TIME ZONE 'UTC','YYYY-MM') AND NOT EXISTS(SELECT 1 FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload ? 'period' AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+` AND `+sourceAllows("c.id", "quickbooks")+`)
 AND NOT EXISTS(SELECT 1 FROM xero_snapshots s WHERE s.close_id=c.id AND `+sourceAllows("c.id", "xero")+`)
 FROM client_closes c
 CROSS JOIN LATERAL (SELECT count(*) FILTER (WHERE x.review_state='pending' AND sourced AND NOT blocked) AS ready,count(*) FILTER (WHERE x.review_state IN ('changes_requested','source_changed') OR blocked) AS attention
  FROM (SELECT x.review_state,COALESCE(jsonb_typeof(x.paper->'sources')='array' AND x.paper->'sources'<>'[]'::jsonb,false) AS sourced,COALESCE(jsonb_typeof(x.paper->'blockers')='array' AND x.paper->'blockers'<>'[]'::jsonb,false) AS blocked
   FROM workpaper_review_state x WHERE x.close_id=c.id AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=x.close_id AND n.paper->>'revises'=x.id::text)) x) d
 WHERE `+morningScope+` ORDER BY c.id`, ids)
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		today, thisMonth := now.Format("2006-01-02"), now.Format("2006-01")
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
			o := byID[c.CloseID]
			switch o.State {
			case "reopen_required":
				c.Needs = append(c.Needs, "changed since its sign-off: reopen and review")
				c.Count++
			case "unknown":
				c.Needs = append(c.Needs, "its sign-off could not be rechecked")
				c.Count++
			}
			if o.DueOn != "" && o.DueOn < today {
				c.Needs = append(c.Needs, "overdue since "+o.DueOn)
				c.Count++
			} else if o.Period < shiftMonth(thisMonth, -1) && o.State == "open" {
				c.Needs = append(c.Needs, "not signed off")
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
		var effort *string
		var items []byte
		var checked time.Time
		err = tx.QueryRowContext(ctx, `SELECT r.model,r.close_id::text,r.items,r.created_at,r.reasoning_effort FROM replay_runs r JOIN client_closes c ON c.id=r.close_id
 WHERE r.organization_id=$1 AND r.actor='scheduled-replay' AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)) ORDER BY r.id DESC LIMIT 1`, organization(r), firmAccess(r), subject(r)).Scan(&model, &checkedClose, &items, &checked, &effort)
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
			check = map[string]any{"model": model, "reasoning_effort": effort, "close_id": checkedClose, "kept": kept, "scored": scored, "checked_at": checked}
		}
		daily, err := dailyFindings(ctx, db, organization(r), firmAccess(r), subject(r), "", nil)
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
		// Due: outstanding closes overdue or due within a week, soonest first; the Work card counts exactly this list.
		week := now.AddDate(0, 0, 7).Format("2006-01-02")
		due, overdue, undated := []obligation{}, 0, 0
		for _, o := range owed {
			if o.DueOn == "" {
				undated++
			} else if o.DueOn <= week {
				due = append(due, o)
				if o.DueOn < today {
					overdue++
				}
			}
		}
		slices.SortStableFunc(due, func(a, b obligation) int { return strings.Compare(a.DueOn, b.DueOn) })
		// Months the firm's roster says are owed and nobody has opened: they show here until opened. Those overdue or due
		// within a week are counted with the due closes, apart.
		unopened, err := owedMonths(ctx, tx, now, organization(r), firmAccess(r), canReview(r), subject(r), client)
		if err != nil {
			problem(w, 503, "Morning brief unavailable.")
			return
		}
		dueUnopened, unopenedOverdue := []owedMonth{}, 0
		for _, o := range unopened {
			if o.DueOn != "" && o.DueOn <= week {
				dueUnopened = append(dueUnopened, o)
				if o.DueOn < today {
					unopenedOverdue++
				}
			}
		}
		slices.SortStableFunc(dueUnopened, func(a, b owedMonth) int { return strings.Compare(a.DueOn, b.DueOn) })
		jsonReply(w, 200, map[string]any{"not_opened": unopened, "due_not_opened": dueUnopened, "since": now.Add(-24 * time.Hour), "as_of": now, "overnight": map[string]int{"month_end_snapshots": snapshots, "drafts_prepared": drafts, "standing_approvals": standing},
			"closes": shown, "needing": len(needing), "quiet": len(closes) - len(needing), "model_check": check,
			"due": due, "due_counts": map[string]int{"overdue": overdue, "upcoming": len(due) - overdue, "no_due_date": undated, "not_opened_overdue": unopenedOverdue, "not_opened_upcoming": len(dueUnopened) - unopenedOverdue}})
	}
}
