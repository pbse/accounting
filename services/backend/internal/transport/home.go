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
	"close.local/backend/internal/xero"
)

// registerHome serves the home page's four outcomes and its trust strip, counted in one snapshot over the closes the
// reader can open that are owed through this month or due within a week (the obligation cohort): how many stand signed
// off, how many decisions wait, which deadlines are missed or close, and how far the agent's preparation has come. The
// decision list itself is the attention inbox; the months owed and not opened come from the roster and count among
// the closes owed; owed lists the cohort itself, each with where its sign-off stands and whose it is, as the sign-off
// card's own list; inbox is page one of the decisions in this same snapshot, so the card and its list agree. A close's books
// count as captured only when its newest month-end capture, from either provider, is usable: of its own period, from
// an active connection, its evidence standing (the test the changes-from-last-month read applies) and readable by the
// same coverage and balances reads the close's tie-out uses; an older usable capture under a newer unusable one does
// not count.
func registerHome(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/home", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		now := time.Now().UTC()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		defer tx.Rollback()
		today, week := now.Format("2006-01-02"), now.AddDate(0, 0, 7).Format("2006-01-02")
		rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.entity,c.period,COALESCE(c.due_on::text,''),CASE WHEN c.owner='' THEN 'none' WHEN c.owner=$3 THEN 'mine' ELSE 'other' END,
 COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')='signed_off',
 q.captured_at,COALESCE(q.usable,false),q.accounts,q.trial_balance,x.captured_at,COALESCE(x.active,false),x.payload
 FROM client_closes c
 LEFT JOIN LATERAL (SELECT s.captured_at,x.state='active' AND e.id IS NOT NULL AND e.superseded_by IS NULL AS usable,s.payload->'accounts' AS accounts,s.payload->'reports'->'TrialBalance' AS trial_balance FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
  WHERE s.close_id=c.id AND s.payload->>'period'=c.period AND s.connection_id=`+quickbooks.ReadingConnection("c.id")+` ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) q ON true
 LEFT JOIN LATERAL (SELECT s.captured_at,x.state='active' AS active,s.payload FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id AND x.state<>'disconnected' WHERE s.close_id=c.id AND s.period=c.period ORDER BY s.captured_at DESC,s.id DESC LIMIT 1) x ON true
 WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND (c.period<=$4 OR c.due_on<=$5::date) ORDER BY c.period DESC,c.id DESC`, organization(r), firmAccess(r), subject(r), now.Format("2006-01"), week)
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		type member struct {
			obligation
			// Owner is mine, other or none, as the attention events say it, so the Mine view can keep to the reader's.
			Owner       string `json:"owner"`
			signed      bool
			booksUsable bool
		}
		var cohort []member
		captured := 0
		for rows.Next() {
			var m member
			var qboAt, xeroAt sql.NullTime
			var qboUsable, xeroActive bool
			var qboAccounts, qboTrialBalance, xeroPayload []byte
			if err = rows.Scan(&m.CloseID, &m.Entity, &m.Period, &m.DueOn, &m.Owner, &m.signed, &qboAt, &qboUsable, &qboAccounts, &qboTrialBalance, &xeroAt, &xeroActive, &xeroPayload); err != nil {
				break
			}
			// The newest capture of either provider decides, and counts only when the close's own reads can use it: the
			// coverage read for its account list and the tie-out's balances read for its Trial Balance.
			usable := false
			switch {
			case xeroAt.Valid && (!qboAt.Valid || xeroAt.Time.After(qboAt.Time)):
				usable = xeroActive && xeroBooksUsable(xeroPayload)
			case qboAt.Valid && qboUsable:
				usable = qboBooksUsable(qboAccounts, qboTrialBalance)
			}
			if usable {
				captured++
			}
			m.booksUsable = usable
			cohort = append(cohort, m)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		// A recorded sign-off counts only while it still stands, as the morning brief and the sign-off goal check it. The
		// open closes overdue or due within the week are listed, soonest first, as the deadline card's own population.
		signed, overdue, dueSoon, undated := 0, 0, 0, 0
		due, owed := []obligation{}, []member{}
		for _, m := range cohort {
			m.State = "open"
			if m.signed {
				if m.State, err = recheckSignOff(ctx, tx, m.CloseID); err != nil {
					problem(w, 503, "Home is unavailable.")
					return
				}
			}
			owed = append(owed, m)
			if m.State == "signed_off" {
				signed++
				continue
			}
			switch {
			case m.DueOn == "":
				undated++
			case m.DueOn < today:
				overdue++
				due = append(due, m.obligation)
			case m.DueOn <= week:
				dueSoon++
				due = append(due, m.obligation)
			}
		}
		slices.SortStableFunc(due, func(a, b obligation) int { return strings.Compare(a.DueOn, b.DueOn) })
		// Preparation is counted over every close the reader can open: the current work (every job but a cancelled one)
		// and, apart, the cancelled jobs, which together are the Preparation page's "All" view the card opens. A prepared job whose
		// source was since replaced is not done (the page marks it "Source replaced"). The closes count says whether the
		// firm has any client work at all.
		var closes, total, done, failed, waitingPerson, running, waitingClient, cancelled, noDraft int
		if err = tx.QueryRowContext(ctx, `WITH mine AS (SELECT c.id FROM client_closes c WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted)))
 SELECT (SELECT count(*) FROM mine),count(*) FILTER (WHERE state<>'cancelled'),count(*) FILTER (WHERE state='completed' AND jsonb_array_length(COALESCE(workpaper_ids,'[]'::jsonb))>0 AND NOT EXISTS(SELECT 1 FROM preparation_intakes i JOIN evidence e ON e.id=i.source_id WHERE i.job_id=preparation_jobs.id AND e.superseded_by IS NOT NULL)),count(*) FILTER (WHERE state='failed'),
 count(*) FILTER (WHERE state='needs_confirmation'),count(*) FILTER (WHERE state IN ('queued','interpreting','waiting_evidence')),count(*) FILTER (WHERE state='waiting_client'),count(*) FILTER (WHERE state='cancelled'),count(*) FILTER (WHERE state='completed' AND jsonb_array_length(COALESCE(workpaper_ids,'[]'::jsonb))=0)
 FROM preparation_jobs WHERE close_id IN (SELECT id FROM mine)`, organization(r), firmAccess(r), subject(r)).Scan(&closes, &total, &done, &failed, &waitingPerson, &running, &waitingClient, &cancelled, &noDraft); err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		// The months owed and not opened run through the current month, as the Clients page lists them, so a client
		// added today with its first month running, or a month due within the week before it ends, is an obligation here.
		unopened, err := owedMonthsThrough(ctx, tx, now.Format("2006-01"), organization(r), firmAccess(r), canReview(r), subject(r), "")
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		unopenedOverdue, unopenedSoon := 0, 0
		for _, o := range unopened {
			switch {
			case o.DueOn == "":
				undated++
			case o.DueOn < today:
				unopenedOverdue++
			case o.DueOn <= week:
				unopenedSoon++
			}
		}
		// An ended month still open whose books have no usable month-end capture needs a person to capture them (or
		// label historical exports): each is listed by itself, as the morning brief listed it, under the books strip.
		// Current, complete historical exports a person labeled are the other way to give a close its books.
		uncaptured, exported := []member{}, 0
		ended := []string{}
		for _, m := range owed {
			if m.State != "signed_off" && !m.booksUsable && m.Period < now.Format("2006-01") {
				ended = append(ended, m.CloseID)
			}
		}
		labels, labelErr := latestExportLabels(ctx, tx, ended)
		if labelErr != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		for _, m := range owed {
			if m.State == "signed_off" || m.booksUsable || m.Period >= now.Format("2006-01") {
				continue
			}
			label := labels[m.CloseID]
			// Only a complete set does; a partial one still leaves the month's books to capture.
			if label != nil && label.Current && label.Complete {
				exported++
				continue
			}
			uncaptured = append(uncaptured, m)
		}
		// A month opened early (a later period, due beyond the week) is outside the cohort, but a sign-off of it that no
		// longer stands is still a decision: it is listed with the reopened ones, apart from the owed closes.
		inCohort := map[string]bool{}
		for _, m := range cohort {
			inCohort[m.CloseID] = true
		}
		early, err := reopenedBetween(ctx, tx, r, shiftMonth(now.Format("2006-01"), 1), "9999-12")
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		later := []member{}
		for id, state := range early {
			if inCohort[id] {
				continue
			}
			var m member
			if err = tx.QueryRowContext(ctx, `SELECT id::text,entity,period,COALESCE(due_on::text,''),CASE WHEN owner='' THEN 'none' WHEN owner=$2 THEN 'mine' ELSE 'other' END FROM client_closes WHERE id::text=$1`, id, subject(r)).Scan(&m.CloseID, &m.Entity, &m.Period, &m.DueOn, &m.Owner); err != nil {
				problem(w, 503, "Home is unavailable.")
				return
			}
			m.State = state
			later = append(later, m)
		}
		// The decisions, their preferences and the daily findings are read in the same snapshot as the counts above, so
		// every number on Home is as of one moment; no read takes a second connection while this one is held.
		events, err := attentionEvents(ctx, tx, r)
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		// Decisions span every close the reader can open, a month opened early among them: the same population the
		// attention inbox, the bar's badge and a notification use, so each of them names the same items.
		kinds, browser, err := attentionPreferences(ctx, tx, r)
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		// A close whose recorded sign-off no longer stands has no attention event (those stop at the recorded decision), so
		// it is a decision here in its own right, listed first among the decisions: reopen and review, or find out why its
		// sign-off could not be rechecked. It has no read mark, so it is never counted as new.
		// Decisions are what the reader can decide; what waits on a teammate's authority is counted apart.
		decisions, team, unread := 0, 0, 0
		for _, o := range append(slices.Clone(owed), later...) {
			if (o.State == "reopen_required" || o.State == "unknown") && canReview(r) {
				decisions++
			} else if o.State == "reopen_required" || o.State == "unknown" {
				team++
			}
		}
		for _, e := range events {
			if e.SnoozedUntil != nil {
				continue
			}
			if !e.Yours {
				team++
				continue
			}
			decisions++
			if !e.Read && contains(kinds, e.Kind) {
				unread++
			}
		}
		reopened := []string{}
		for _, o := range owed {
			if o.State == "reopen_required" || o.State == "unknown" {
				reopened = append(reopened, o.CloseID)
			}
		}
		daily, err := dailyFindings(ctx, tx, organization(r), firmAccess(r), subject(r), "", reopened)
		if err != nil {
			problem(w, 503, "Home is unavailable.")
			return
		}
		// A capture whose transaction check did not finish is reported apart, never as zero exceptions.
		exceptions, unchecked := 0, 0
		for _, d := range daily {
			exceptions += len(d.New)
			if d.Unchecked != "" {
				unchecked++
			}
		}
		jsonReply(w, 200, map[string]any{
			"as_of":          now,
			"closes":         closes,
			"can_add":        canManageRoster(r),
			"can_review":     canReview(r),
			"signed_off":     map[string]int{"done": signed, "total": len(cohort) + len(unopened)},
			"decisions":      map[string]int{"count": decisions, "unread": unread, "team": team},
			"inbox":          attentionInbox(events, kinds, browser, "all", 0, true),
			"deadlines":      map[string]int{"overdue": overdue, "due_soon": dueSoon, "no_due_date": undated, "not_opened_overdue": unopenedOverdue, "not_opened_upcoming": unopenedSoon},
			"prepared":       map[string]int{"total": total, "done": done, "failed": failed, "waiting_person": waitingPerson, "running": running, "waiting_client": waitingClient, "cancelled": cancelled, "no_draft": noDraft},
			"books":          map[string]int{"captured": captured, "of": len(cohort), "exceptions": exceptions, "unchecked": unchecked, "exported": exported},
			"due":            due,
			"owed":           owed,
			"reopened_later": later,
			"uncaptured":     uncaptured,
			"not_opened":     unopened,
		})
	})
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// reopenedCloses lists the reader's closes of this and last month whose recorded sign-off no longer stands (changed
// since, or not recheckable), read in tx's snapshot, so a read that leaves out signed-off closes can keep them in.
func reopenedCloses(ctx context.Context, tx *sql.Tx, r *http.Request, now time.Time) ([]string, error) {
	states, err := reopenedBetween(ctx, tx, r, endedMonth(now), now.Format("2006-01"))
	ids := make([]string, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	return ids, err
}

// reopenedBetween is reopenedCloses over the periods from through to (YYYY-MM, inclusive), with each close's recheck
// result (reopen_required, or unknown when its readiness could not be read).
func reopenedBetween(ctx context.Context, tx *sql.Tx, r *http.Request, from, to string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT c.id::text FROM client_closes c WHERE c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 AND c.period BETWEEN $4 AND $5 AND COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')='signed_off'`,
		organization(r), firmAccess(r), subject(r), from, to)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	reopened := map[string]string{}
	for _, id := range ids {
		state, err := recheckSignOff(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if state != "signed_off" {
			reopened[id] = state
		}
	}
	return reopened, nil
}

// qboBooksUsable says whether a QuickBooks month-end capture can carry the close: an account inventory that is not
// empty, coverage that finds every account the Trial Balance reports in it, and balances the tie-out can read.
func qboBooksUsable(accounts, trialBalance json.RawMessage) bool {
	var listed []json.RawMessage
	if json.Unmarshal(accounts, &listed) != nil || len(listed) == 0 {
		return false
	}
	items, err := quickbooks.Coverage(accounts, map[string]json.RawMessage{"TrialBalance": trialBalance})
	if err != nil {
		return false
	}
	missing := quickbooks.MissingAccount("", "", "").Reason
	for _, item := range items {
		if item.Reason == missing {
			return false
		}
	}
	_, _, err = quickbooks.TrialBalance(accounts, trialBalance)
	return err == nil
}

// xeroBooksUsable is qboBooksUsable for a Xero capture: coverage that finds every account the Trial Balance reports
// in the account list, and balances the tie-out can read.
func xeroBooksUsable(payload json.RawMessage) bool {
	items, err := xero.Coverage(payload)
	if err != nil {
		return false
	}
	missing := quickbooks.MissingAccount("", "", "").Reason
	for _, item := range items {
		if item.Reason == missing {
			return false
		}
	}
	_, _, err = xero.AccountBalances(payload)
	return err == nil
}
