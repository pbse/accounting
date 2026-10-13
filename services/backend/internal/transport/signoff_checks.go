package transport

import (
	"context"
	"database/sql"
	"log"
	"time"
)

// signOffCheckAge is how old a stored check may grow before the worker makes it again; signOffCheckEvery is how often
// the worker looks for one to make. A check older than signOffCheckStale is not trusted: the worker has stopped or
// fallen behind, so a read rechecks the close itself.
var signOffCheckAge, signOffCheckEvery, signOffCheckStale = 5 * time.Minute, 30 * time.Second, 15 * time.Minute

// signOffCheckBatch is how many closes the worker checks before it looks again.
const signOffCheckBatch = 100

// RunSignOffChecks keeps each recorded sign-off's standing current for the lists that show many closes at once: Home,
// Clients, the roster, the closes list, the morning brief and effort. A recheck reads the whole close in about 25
// round trips, too many to make for every signed-off month on each read. A close's own page still rechecks it live.
func RunSignOffChecks(ctx context.Context, db *sql.DB) {
	for {
		n, err := checkSignOffs(ctx, db, "")
		if err != nil && ctx.Err() == nil {
			log.Print("Sign-off checks could not run: ", err)
		}
		// A full batch checked may leave more due, so the next one starts at once; one with failures waits.
		wait := signOffCheckEvery
		if err == nil && n == signOffCheckBatch {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// checkSignOffs rechecks, oldest check first, the closes of org ("" for every firm) whose latest decision is a sign-off
// with no stored check, or one older than signOffCheckAge, and stores each result. It says how many it checked, at most
// signOffCheckBatch.
func checkSignOffs(ctx context.Context, db *sql.DB, org string) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id::text FROM client_closes c
 JOIN LATERAL (SELECT id,decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1) s ON s.decision='signed_off'
 LEFT JOIN signoff_checks k ON k.close_id=c.id AND k.signoff_id=s.id
 WHERE ($1='' OR c.organization_id=$1) AND (k.checked_at IS NULL OR k.checked_at<now()-make_interval(secs=>$2))
 ORDER BY k.checked_at NULLS FIRST,c.id LIMIT $3`, org, signOffCheckAge.Seconds(), signOffCheckBatch)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	checked := 0
	for _, id := range ids {
		if err = checkSignOff(ctx, db, id); err != nil {
			if ctx.Err() != nil {
				return checked, err
			}
			log.Printf("The sign-off of close %s could not be checked: %v", id, err)
			continue
		}
		checked++
	}
	return checked, nil
}

// checkSignOff rechecks one close's latest sign-off and stores the result. The decision and its recheck read one
// snapshot, so the state stored is that sign-off's; a newer decision has no check until it gets its own.
func checkSignOff(ctx context.Context, db *sql.DB, id string) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var signoff int64
	var decision string
	if err = tx.QueryRowContext(ctx, `SELECT id,decision FROM close_signoffs WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, id).Scan(&signoff, &decision); err != nil || decision != "signed_off" {
		return err
	}
	state, err := recheckSignOff(ctx, tx, id)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO signoff_checks(close_id,signoff_id,state,checked_at) VALUES($1::bigint,$2,$3,now())
 ON CONFLICT (close_id) DO UPDATE SET signoff_id=EXCLUDED.signoff_id,state=EXCLUDED.state,checked_at=EXCLUDED.checked_at`, id, signoff, state); err != nil {
		return err
	}
	return tx.Commit()
}

// signOffStates is the standing of the recorded sign-offs of the closes ids, by close: signed_off, reopen_required or
// unknown. It is the worker's stored check of each close's latest decision, or, for a sign-off not yet checked or whose
// check is older than signOffCheckStale, a recheck in the caller's snapshot.
func signOffStates(ctx context.Context, tx *sql.Tx, ids []string) (map[string]string, error) {
	states := map[string]string{}
	if len(ids) == 0 {
		return states, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT k.close_id::text,k.state FROM signoff_checks k WHERE k.close_id=ANY($1::text[]::bigint[]) AND k.signoff_id=(SELECT max(id) FROM close_signoffs WHERE close_id=k.close_id) AND k.checked_at>now()-make_interval(secs=>$2)`, ids, signOffCheckStale.Seconds())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, state string
		if err = rows.Scan(&id, &state); err != nil {
			rows.Close()
			return nil, err
		}
		states[id] = state
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, ok := states[id]; !ok {
			if states[id], err = recheckSignOff(ctx, tx, id); err != nil {
				return nil, err
			}
		}
	}
	return states, nil
}
