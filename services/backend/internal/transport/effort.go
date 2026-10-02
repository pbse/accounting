package transport

import (
	"database/sql"
	"net/http"
	"slices"
	"strings"
	"time"
)

var effortActivities = []string{"prepare", "review", "evidence", "requests", "signoff", "configuration"}

// Recorded entries may also be our own team's setup or support help, which the workspace never tracks.
var effortEntryActivities = append(slices.Clone(effortActivities), "assistance")

// Tracked activity is capped at 60 seconds per person per close per minute; it
// measures visible, interactive workspace time, not every minute worked.
// closeReviews counts a close's recorded review decisions; reviewsAfterApproval counts those on a draft some earlier
// version of which was approved, following each revision's link to the draft it replaces within the close.
const closeReviews = `(SELECT count(*) FROM workpaper_reviews v JOIN workpapers d ON d.id=v.workpaper_id WHERE d.close_id=c.id)`
const reviewsAfterApproval = `(SELECT count(*) FROM workpaper_reviews v JOIN workpapers d ON d.id=v.workpaper_id WHERE d.close_id=c.id AND EXISTS(
 WITH RECURSIVE earlier(id) AS (SELECT d.paper->>'revises' UNION SELECT p.paper->>'revises' FROM workpapers p JOIN earlier ON p.id::text=earlier.id WHERE p.close_id=c.id)
 SELECT 1 FROM earlier JOIN workpapers a ON a.id::text=earlier.id AND a.close_id=c.id JOIN workpaper_reviews x ON x.workpaper_id=a.id AND x.decision='approved'))`

// closeSignedOff is SQL: close c's latest recorded decision is a sign-off; readiness decides whether it still stands.
const closeSignedOff = `COALESCE((SELECT s.decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')='signed_off'`

// closeMinutes is a close's human minutes (alias c): tracked seconds as minutes plus recorded minutes.
const closeMinutes = `COALESCE((SELECT sum(seconds) FROM close_effort e WHERE e.close_id=c.id),0)/60.0+COALESCE((SELECT sum(minutes) FROM close_effort_entries x WHERE x.close_id=c.id),0)`

func registerEffort(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/{id}/effort", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Activity string `json:"activity"`
			Seconds  int    `json:"seconds"`
		}
		if !decode(w, r, &input) {
			return
		}
		if !slices.Contains(effortActivities, input.Activity) || input.Seconds < 1 || input.Seconds > 30 {
			problem(w, 422, "Send a known activity and 1–30 seconds.")
			return
		}
		actor := subject(r)
		if actor == "" {
			problem(w, 403, "Sign in before recording activity.")
			return
		}
		_, err := db.ExecContext(r.Context(), `INSERT INTO close_effort(close_id,actor,minute,activity,seconds) SELECT id,$2,date_trunc('minute',now()),$3,$4 FROM client_closes WHERE id::text=$1 ON CONFLICT (close_id,actor,minute) DO UPDATE SET seconds=LEAST(60,close_effort.seconds+EXCLUDED.seconds),activity=EXCLUDED.activity`, r.PathValue("id"), actor, input.Activity, input.Seconds)
		if err != nil {
			problem(w, 503, "Activity could not be recorded.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/closes/{id}/effort/entries", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Activity string `json:"activity"`
			Minutes  int    `json:"minutes"`
			Note     string `json:"note"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Note = strings.TrimSpace(input.Note)
		if !slices.Contains(effortEntryActivities, input.Activity) || input.Minutes < 1 || input.Minutes > 480 || len(input.Note) < 5 || len(input.Note) > 500 {
			problem(w, 422, "Record 1–480 minutes for a known activity with a 5–500 character note.")
			return
		}
		actor := subject(r)
		if actor == "" {
			problem(w, 403, "Sign in before recording time.")
			return
		}
		var id string
		if err := db.QueryRowContext(r.Context(), `INSERT INTO close_effort_entries(close_id,actor,activity,minutes,note) SELECT id,$2,$3,$4,$5 FROM client_closes WHERE id::text=$1 RETURNING id::text`, r.PathValue("id"), actor, input.Activity, input.Minutes, input.Note).Scan(&id); err != nil {
			problem(w, 503, "Time could not be recorded.")
			return
		}
		jsonReply(w, 200, map[string]string{"id": id})
	})
	mux.HandleFunc("GET /v1/closes/{id}/effort", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		out := map[string]any{}
		var tracked, recorded, answered int64
		var open int
		var since, oldest sql.NullTime
		err := db.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT sum(seconds) FROM close_effort WHERE close_id::text=$1),0),(SELECT min(minute) FROM close_effort WHERE close_id::text=$1),COALESCE((SELECT sum(minutes) FROM close_effort_entries WHERE close_id::text=$1),0)`, id).Scan(&tracked, &since, &recorded)
		if err == nil {
			err = db.QueryRowContext(r.Context(), `SELECT COALESCE(sum(EXTRACT(EPOCH FROM first_reply-q.delivered_at)) FILTER (WHERE first_reply IS NOT NULL),0)::bigint,count(*) FILTER (WHERE first_reply IS NULL),min(q.delivered_at) FILTER (WHERE first_reply IS NULL) FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id LEFT JOIN LATERAL (SELECT min(p.recorded_at) AS first_reply FROM work_replies p WHERE p.request_id=q.id AND p.recorded_at>=q.delivered_at) reply ON true WHERE d.close_id::text=$1 AND q.delivered_at IS NOT NULL`, id).Scan(&answered, &open, &oldest)
		}
		if err != nil {
			problem(w, 503, "Effort is unavailable.")
			return
		}
		byActivity := map[string]int64{}
		rows, err := db.QueryContext(r.Context(), `SELECT activity,sum(seconds) FROM close_effort WHERE close_id::text=$1 GROUP BY activity`, id)
		if err != nil {
			problem(w, 503, "Effort is unavailable.")
			return
		}
		for rows.Next() {
			var activity string
			var seconds int64
			if rows.Scan(&activity, &seconds) == nil {
				byActivity[activity] = seconds
			}
		}
		rows.Close()
		entries := []map[string]any{}
		rows, err = db.QueryContext(r.Context(), `SELECT activity,minutes,note,created_at,actor=$2 FROM close_effort_entries WHERE close_id::text=$1 ORDER BY id DESC LIMIT 20`, id, subject(r))
		if err != nil {
			problem(w, 503, "Effort is unavailable.")
			return
		}
		defer rows.Close()
		for rows.Next() {
			var activity, note string
			var minutes int
			var created time.Time
			var mine bool
			if rows.Scan(&activity, &minutes, &note, &created, &mine) == nil {
				entries = append(entries, map[string]any{"activity": activity, "minutes": minutes, "note": note, "created_at": created, "mine": mine})
			}
		}
		out["tracked_seconds"], out["tracked_by_activity"], out["recorded_minutes"], out["entries"] = tracked, byActivity, recorded, entries
		out["answered_wait_seconds"], out["open_requests"] = answered, open
		if since.Valid {
			out["tracking_since"] = since.Time
		}
		if oldest.Valid {
			out["oldest_open_since"] = oldest.Time
		}
		jsonReply(w, 200, out)
	})
	mux.HandleFunc("GET /v1/closes/track-record", trackRecord(db))
	mux.HandleFunc("GET /v1/closes/morning", morning(db))
	mux.HandleFunc("GET /v1/closes/effort", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		period := r.URL.Query().Get("period")
		if period != "" {
			if _, err := time.Parse("2006-01", period); err != nil {
				problem(w, 422, "Choose a month as YYYY-MM.")
				return
			}
		}
		fail := func() { problem(w, 503, "Effort is unavailable.") }
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			fail()
			return
		}
		defer tx.Rollback()
		args := []any{organization(r), firmAccess(r), subject(r)}
		scope := `c.organization_id=$1 AND ($2 OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=$3 AND lower(g.client_entity)=lower(c.entity) AND g.granted))`
		if period == "" {
			if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(c.period),'') FROM client_closes c WHERE `+scope, args...).Scan(&period); err != nil {
				fail()
				return
			}
		}
		month, _ := time.Parse("2006-01", period)
		from := month.AddDate(0, -11, 0).Format("2006-01")
		// Every accessible close of the twelve months up to this one: a recorded sign-off counts only while readiness
		// still says it stands, read in this snapshot.
		// ponytail: rechecks each signed-off close of the year per read; keep each sign-off's last recheck if this slows.
		type closeEffort struct {
			id, period               string
			total, tracked, recorded float64
			assistance               int
			signed                   bool
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.id::text,c.period,`+closeMinutes+`,COALESCE((SELECT sum(seconds) FROM close_effort e WHERE e.close_id=c.id),0)/60.0,
 COALESCE((SELECT sum(minutes) FROM close_effort_entries x WHERE x.close_id=c.id AND x.activity<>'assistance'),0),COALESCE((SELECT sum(minutes) FROM close_effort_entries x WHERE x.close_id=c.id AND x.activity='assistance'),0),`+closeSignedOff+`
 FROM client_closes c WHERE `+scope+` AND c.period BETWEEN $4 AND $5`, append(args, from, period)...)
		if err != nil {
			fail()
			return
		}
		closes := []closeEffort{}
		for rows.Next() {
			var c closeEffort
			if err := rows.Scan(&c.id, &c.period, &c.total, &c.tracked, &c.recorded, &c.assistance, &c.signed); err != nil {
				rows.Close()
				fail()
				return
			}
			closes = append(closes, c)
		}
		rows.Close()
		if rows.Err() != nil {
			fail()
			return
		}
		signed, unchecked := map[string]bool{}, 0
		for i, c := range closes {
			if !c.signed {
				continue
			}
			state, err := recheckSignOff(ctx, tx, c.id)
			if err != nil {
				fail()
				return
			}
			closes[i].signed = state == "signed_off"
			signed[c.id] = closes[i].signed
			if state == "unknown" && c.period == period {
				unchecked++
			}
		}
		median := func(values []float64) any {
			if len(values) == 0 {
				return nil
			}
			slices.Sort(values)
			n := len(values)
			return (values[(n-1)/2] + values[n/2]) / 2
		}
		// Signed-off closes give final effort; open closes give effort so far, which is not a time to finish. Each
		// cohort counts the month's accessible closes (eligible) and those with any time (measured).
		cohort := func(completed bool) map[string]any {
			eligible, totals, tracked, recorded, assistance := 0, []float64{}, 0.0, 0.0, 0
			for _, c := range closes {
				if c.period != period || c.signed != completed {
					continue
				}
				eligible++
				tracked, recorded, assistance = tracked+c.tracked, recorded+c.recorded, assistance+c.assistance
				if c.total > 0 {
					totals = append(totals, c.total)
				}
			}
			out := map[string]any{"eligible": eligible, "measured": len(totals), "tracked_minutes": tracked, "recorded_minutes": recorded, "assistance_minutes": assistance}
			if m := median(totals); m != nil {
				out["median_minutes"] = m
			}
			return out
		}
		trend := []map[string]any{}
		for i := -11; i <= 0; i++ {
			p := month.AddDate(0, i, 0).Format("2006-01")
			totals := []float64{}
			for _, c := range closes {
				if c.period == p && c.signed && c.total > 0 {
					totals = append(totals, c.total)
				}
			}
			point := map[string]any{"period": p, "measured": len(totals)}
			if m := median(totals); m != nil {
				point["median_minutes"] = m
			}
			trend = append(trend, point)
		}

		items := []map[string]any{}
		more := false
		list, err := tx.QueryContext(ctx, `SELECT c.id::text,c.entity,COALESCE((SELECT sum(seconds) FROM close_effort e WHERE e.close_id=c.id),0),COALESCE((SELECT sum(minutes) FROM close_effort_entries x WHERE x.close_id=c.id AND x.activity<>'assistance'),0),COALESCE((SELECT sum(minutes) FROM close_effort_entries x WHERE x.close_id=c.id AND x.activity='assistance'),0),(SELECT count(*) FROM workpaper_review_state w WHERE w.close_id=c.id AND w.review_state='approved'),`+closeReviews+`,`+reviewsAfterApproval+`,
 COALESCE((SELECT sum(seconds) FROM close_effort e WHERE e.close_id=c.id AND e.activity='review'),0)/60.0+COALESCE((SELECT sum(minutes) FROM close_effort_entries x WHERE x.close_id=c.id AND x.activity='review'),0) FROM client_closes c WHERE `+scope+` AND c.period=$4 ORDER BY c.entity LIMIT 201`, append(args, period)...)
		if err != nil {
			fail()
			return
		}
		defer list.Close()
		for list.Next() {
			if len(items) == 200 {
				more = true
				break
			}
			var id, entity string
			var tracked, recorded, assistance int64
			var approved, reviews, afterApproval int
			var reviewMinutes float64
			if err := list.Scan(&id, &entity, &tracked, &recorded, &assistance, &approved, &reviews, &afterApproval, &reviewMinutes); err != nil {
				fail()
				return
			}
			total := float64(tracked)/60 + float64(recorded+assistance)
			item := map[string]any{"close_id": id, "entity": entity, "tracked_seconds": tracked, "recorded_minutes": recorded, "assistance_minutes": assistance, "approved_drafts": approved, "measured": total > 0,
				"reviews": reviews, "reviews_after_approval": afterApproval, "review_minutes": reviewMinutes, "signed_off": signed[id]}
			if total > 0 {
				item["total_minutes"] = total
			}
			items = append(items, item)
		}
		if list.Close() != nil || list.Err() != nil {
			fail()
			return
		}
		// Review counts cover every accessible close in the month, measured or not.
		var reviews, afterApproval int
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(sum(r),0),COALESCE(sum(a),0) FROM (SELECT `+closeReviews+` AS r,`+reviewsAfterApproval+` AS a FROM client_closes c WHERE `+scope+` AND c.period=$4) counts`, append(args, period)...).Scan(&reviews, &afterApproval); err != nil {
			fail()
			return
		}
		jsonReply(w, 200, map[string]any{"period": period, "items": items, "completed": cohort(true), "in_progress": cohort(false), "unchecked": unchecked, "has_more": more, "reviews": reviews, "reviews_after_approval": afterApproval, "trend": trend})
	})
}
