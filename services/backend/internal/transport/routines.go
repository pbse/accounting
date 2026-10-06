package transport

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// A client's monthly hand-over: when a month ends, the worker opens that month's close if needed and hands it to the
// agent as the member who set it up approved. It stops when a person stops it, when its creator leaves the firm or
// loses an explicit assignment to the client, or when a close of the client is deleted for contract termination or at
// the client's request.

type closeRoutine struct {
	Author  string `json:"author"`
	Finish  string `json:"finish"`
	DueDays *int   `json:"due_days"`
	Next    string `json:"next_period"`
	Waiting string `json:"waiting"`
}

// endedMonth is the month that most recently ended at now, counted from the first of now's UTC month.
func endedMonth(now time.Time) string {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0).Format("2006-01")
}

// monthEnd is the last day of a YYYY-MM month.
func monthEnd(period string) time.Time {
	first, _ := time.Parse("2006-01", period)
	return first.AddDate(0, 1, -1)
}

// nextMonth is the YYYY-MM month after period.
func nextMonth(period string) string {
	return monthEnd(period).AddDate(0, 0, 1).Format("2006-01")
}

// readRoutine is the active monthly hand-over of the close's client, or nil.
func readRoutine(ctx context.Context, tx *sql.Tx, closeID string) (*closeRoutine, error) {
	var rt closeRoutine
	var due sql.NullInt64
	var last string
	err := tx.QueryRowContext(ctx, `SELECT r.author,r.finish,r.due_days,r.last_period,r.waiting FROM close_routines r JOIN client_closes c ON c.organization_id=r.organization_id AND lower(c.entity)=lower(r.client_entity) WHERE c.id=$1 AND r.stopped_at IS NULL`, closeID).Scan(&rt.Author, &rt.Finish, &due, &last, &rt.Waiting)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if due.Valid {
		days := int(due.Int64)
		rt.DueDays = &days
	}
	rt.Next = nextMonth(last)
	return &rt, nil
}

func registerRoutines(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("POST /v1/closes/{id}/routine/stop", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := reviewActor(w, r); !ok {
			return
		}
		var in struct {
			Reason string `json:"reason"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Reason = strings.TrimSpace(in.Reason); len(in.Reason) < 1 || len(in.Reason) > 500 {
			problem(w, 422, "Give a reason of 1–500 characters for stopping the monthly hand-over.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "The monthly hand-over is unavailable.")
			return
		}
		defer tx.Rollback()
		var closeID string
		if err = tx.QueryRowContext(r.Context(), `SELECT id::text FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		result, err := tx.ExecContext(r.Context(), `UPDATE close_routines r SET stopped_at=now(),stopped_by=$2,stop_reason=$3 FROM client_closes c WHERE c.id=$1 AND r.organization_id=c.organization_id AND lower(r.client_entity)=lower(c.entity) AND r.stopped_at IS NULL`, closeID, subject(r), in.Reason)
		if n, _ := result.RowsAffected(); err == nil && n == 0 {
			problem(w, 409, "No monthly hand-over is set up for this client.")
			return
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The monthly hand-over could not be stopped. Refresh and try again.")
			return
		}
		jsonReply(w, 200, map[string]bool{"stopped": true})
	})
}

// handOverMonths hands every due month over, a sweep at a time, and returns how many months it handed over. It goes on
// while a sweep hands a month over (a routine behind by several months moves one a sweep) and until every routine due
// at the start has been tried: one that cannot move, such as at its firm's limit, goes to the back, so the next sweep
// reaches the rest, and it is tried again by the next hour's pass.
func handOverMonths(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	var due int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM close_routines r JOIN organizations o ON o.id=r.organization_id WHERE r.stopped_at IS NULL AND o.enabled AND r.last_period<$1`, endedMonth(now)).Scan(&due); err != nil {
		return 0, err
	}
	total, triedAll := 0, 0
	for range 1000 {
		handed, tried, err := sweepCloseRoutines(ctx, db, now)
		total, triedAll = total+handed, triedAll+tried
		if err != nil || tried == 0 || (handed == 0 && triedAll >= due) {
			return total, err
		}
	}
	return total, nil
}

// routineSweep is how many due routines one sweep tries.
const routineSweep = 50

// sweepCloseRoutines hands the next ended month to the agent for up to 50 routines of enabled firms, the least
// recently tried first, one month per routine per sweep so no month is skipped, and returns how many it handed over and
// how many it tried. A routine whose step fails, or whose firm's roles cannot be read, is logged and marked waiting, so
// it cannot hold up the others.
func sweepCloseRoutines(ctx context.Context, db *sql.DB, now time.Time) (handed, tried int, err error) {
	rows, err := db.QueryContext(ctx, `SELECT r.id::text,r.last_period,r.organization_id FROM close_routines r JOIN organizations o ON o.id=r.organization_id WHERE r.stopped_at IS NULL AND o.enabled AND r.last_period<$1 ORDER BY r.checked_at NULLS FIRST,r.id LIMIT $2`, endedMonth(now), routineSweep)
	if err != nil {
		return 0, 0, err
	}
	due := [][3]string{}
	for rows.Next() {
		var id, last, org string
		if rows.Scan(&id, &last, &org) == nil {
			due = append(due, [3]string{id, nextMonth(last), org})
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, 0, err
	}
	roles := sweepRoles{}
	for _, step := range due {
		id, period := step[0], step[1]
		members, err := roles.of(ctx, step[2])
		var done bool
		if err == nil {
			done, err = stepRoutine(ctx, db, id, period, members)
		}
		if done {
			handed++
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("Monthly hand-over #%s could not hand over %s: %v", id, period, err)
			db.ExecContext(ctx, `UPDATE close_routines SET waiting=$2,checked_at=now() WHERE id=$1 AND stopped_at IS NULL AND last_period<$3`, id, "I couldn't hand over "+period+" yet. I'll try again.", period)
		}
	}
	return handed, len(due), ctx.Err()
}

// stepRoutine opens the client's close for period, the month after the routine's last, if needed and hands it to
// the agent, once. done says it moved on to period.
func stepRoutine(ctx context.Context, db *sql.DB, id, period string, roles map[string]reviewer) (done bool, err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var org, entity, author, finish, last string
	var viaGrant bool
	var dueDays sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT organization_id,client_entity,author,via_grant,finish,due_days FROM close_routines WHERE id=$1 AND stopped_at IS NULL`, id).Scan(&org, &entity, &author, &viaGrant, &finish, &dueDays)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Locks follow the goal paths: the close, then the firm, then the routine. Opening a close counts against the
	// firm's limit, so the firm row is then held for update, as when a person opens one.
	var closeID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3 FOR UPDATE`, org, entity, period).Scan(&closeID); err != nil && err != sql.ErrNoRows {
		return false, err
	}
	lock := "FOR SHARE"
	if closeID == "" {
		lock = "FOR UPDATE"
	}
	var enabled bool
	var limit int
	if err = tx.QueryRowContext(ctx, `SELECT enabled,max_closes FROM organizations WHERE id=$1 `+lock, org).Scan(&enabled, &limit); err != nil || !enabled {
		return false, err
	}
	var member, granted bool
	if err = tx.QueryRowContext(ctx, `SELECT $1='local-development' OR EXISTS(SELECT 1 FROM current_organization_members WHERE organization_id=$1 AND subject=$2 AND granted),
 EXISTS(SELECT 1 FROM current_client_access WHERE organization_id=$1 AND subject=$2 AND lower(client_entity)=lower($3) AND granted)`, org, author, entity).Scan(&member, &granted); err != nil {
		return false, err
	}
	allowed := mayAct(roles, author, member, viaGrant, granted)
	if err = tx.QueryRowContext(ctx, `SELECT last_period FROM close_routines WHERE id=$1 AND stopped_at IS NULL FOR UPDATE`, id).Scan(&last); err == sql.ErrNoRows || err == nil && last >= period {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if !allowed {
		if _, err = tx.ExecContext(ctx, `UPDATE close_routines SET stopped_at=now(),stopped_by='agent',stop_reason=$2 WHERE id=$1`, id, "Stopped because "+author+" can no longer act for "+entity+"."); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	// A client the firm's roster says has left owes no new month: its hand-over stops, whether or not the month's close
	// is already there.
	var left bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM current_client_roster r WHERE `+rosterMatch("$1", "$2")+` AND NOT r.active)`, org, entity).Scan(&left); err != nil {
		return false, err
	}
	if left {
		if _, err = tx.ExecContext(ctx, `UPDATE close_routines SET stopped_at=now(),stopped_by='agent',stop_reason=$2 WHERE id=$1`, id, "Stopped because "+entity+" is inactive on the firm's roster."); err != nil {
			return false, err
		}
		return false, tx.Commit()
	}
	if closeID == "" {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM client_closes WHERE organization_id=$1`, org).Scan(&count); err != nil {
			return false, err
		}
		if count >= limit {
			if _, err = tx.ExecContext(ctx, `UPDATE close_routines SET waiting=$2,checked_at=now() WHERE id=$1`, id, "The firm is at its limit of "+strconv.Itoa(limit)+" closes, so "+entity+" "+period+" is not open yet. I'll open it once there is room."); err != nil {
				return false, err
			}
			return false, tx.Commit()
		}
		// A close this opens (not one already there) takes the firm's monthly close template.
		var opened bool
		if err = tx.QueryRowContext(ctx, `INSERT INTO client_closes(entity,period,organization_id) VALUES($1,$2,$3) ON CONFLICT(organization_id,lower(entity),period) DO UPDATE SET entity=client_closes.entity RETURNING id::text,xmax=0`, entity, period, org).Scan(&closeID, &opened); err != nil {
			return false, err
		}
		if opened {
			if err = applyCloseTemplate(ctx, tx, closeID, roles); err != nil {
				return false, err
			}
		}
	}
	signed, err := signedOff(ctx, tx, closeID)
	if err != nil {
		return false, err
	}
	if !signed {
		g := goalRow{close: closeID, org: org, author: author, state: "active", finish: finish}
		due, text := "", "Handed over by the monthly hand-over "+author+" set up, to get it "+goalFinishes[finish]
		if dueDays.Valid {
			day := monthEnd(period).AddDate(0, 0, int(dueDays.Int64))
			due, text = day.Format("2006-01-02"), text+" by "+day.Format("Mon 2 Jan 2006")
		}
		// An open goal, or one this routine started for the month, means it is already handed over.
		err = tx.QueryRowContext(ctx, `INSERT INTO close_goals(close_id,author,due_on,finish,routine_id,events) VALUES($1,$2,NULLIF($3,'')::date,$4,$5,jsonb_build_array(jsonb_build_object('actor','agent','text',$6::text,'at',now()))) ON CONFLICT DO NOTHING RETURNING id::text`, closeID, author, due, finish, id, text).Scan(&g.id)
		switch err {
		case nil:
			if err = advanceGoal(ctx, tx, g, roles); err == nil {
				err = startGoalRun(ctx, tx, g, "routine:"+id)
			}
		case sql.ErrNoRows:
			err = nil
		}
		if err != nil {
			return false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE close_routines SET last_period=$2,waiting='',checked_at=now() WHERE id=$1`, id, period); err != nil {
		return false, err
	}
	err = tx.Commit()
	return err == nil, err
}
