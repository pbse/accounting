package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/prepaids"
)

// bookedBalance reads a carried schedule's month-end balance from its Trial Balance row; one on the other side than its account type normally sits is refused.
func bookedBalance(item recurringItem) (int64, bool) {
	var cells []struct{ Value string }
	if json.Unmarshal(item.Cells, &cells) != nil || len(cells) != len(item.Columns) {
		return 0, false
	}
	debit, credit := -1, -1
	for i, title := range item.Columns {
		switch strings.TrimSpace(title) {
		case "Debit":
			if debit >= 0 {
				return 0, false
			}
			debit = i
		case "Credit":
			if credit >= 0 {
				return 0, false
			}
			credit = i
		}
	}
	if debit < 0 || credit < 0 {
		return 0, false
	}
	net, err := ledger.Net(cells[debit].Value, cells[credit].Value)
	if err != nil {
		return 0, false
	}
	if scheduleSubtypes[item.Schedule] != "PrepaidExpenses" {
		net.Neg(net)
	}
	if !new(big.Rat).Mul(net, big.NewRat(100, 1)).IsInt() {
		return 0, false
	}
	balance, err := prepaids.Amount(net.FloatString(2))
	return balance, err == nil
}

// sweepComparisons prepares, for up to 20 open closes after the cursor, each carried schedule's month-end comparison from the captured Trial Balance.
func sweepComparisons(ctx context.Context, db *sql.DB, cursor string) (string, int, error) {
	rows, err := db.QueryContext(ctx, `SELECT c.id::text FROM client_closes c WHERE c.id>$1::bigint
 AND COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),'')<>'signed_off'
 AND EXISTS(SELECT 1 FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload ? 'period')
 AND EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id=c.id AND w.kind='prepaid' AND w.paper ? 'carry_forward' AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text))
 ORDER BY c.id LIMIT 20`, cursor)
	if err != nil {
		return cursor, 0, err
	}
	closes := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		closes = append(closes, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return cursor, 0, err
	}
	next := "0"
	if len(closes) == 20 {
		next = closes[19]
	}
	queued := 0
	for _, id := range closes {
		n, err := queueComparisons(ctx, db, id)
		if err != nil {
			return next, queued, err
		}
		queued += n
	}
	return next, queued, nil
}

func queueComparisons(ctx context.Context, db *sql.DB, closeID string) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var entity, period, decision string
	var enabled bool
	if err = tx.QueryRowContext(ctx, `SELECT c.entity,c.period,COALESCE((SELECT decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1),''),o.enabled FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1 FOR UPDATE OF c,o`, closeID).Scan(&entity, &period, &decision, &enabled); err != nil || decision == "signed_off" || !enabled {
		return 0, err
	}
	snapshot, items, err := recurringPreview(ctx, tx, closeID)
	if err != nil {
		return 0, err
	}
	queued := 0
	var failure error
	for i := range items {
		item := &items[i]
		cited := false
		for _, s := range item.Paper.Sources {
			cited = cited || s.ID == snapshot.SourceID
		}
		balance, ok := bookedBalance(*item)
		if cited || !ok {
			continue
		}
		// Any earlier comparison of this schedule against this capture, even a person's failed one, is left to a person.
		var tried bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM preparation_jobs WHERE close_id::text=$1 AND plans->0->>'revises'=$2 AND plans->0->'sources' @> jsonb_build_array(jsonb_build_object('id',$3::text)))`, closeID, item.ID, snapshot.SourceID).Scan(&tried); err != nil {
			return 0, err
		}
		if tried {
			continue
		}
		reference := fmt.Sprintf("Agent-read from QuickBooks snapshot #%s, Trial Balance account #%s, %s; confirm before approving", snapshot.ID, item.AccountID, item.Location)
		// Each schedule is its own savepoint, so a refused or failed one cannot undo the others.
		if _, err = tx.ExecContext(ctx, `SAVEPOINT comparison`); err != nil {
			return 0, err
		}
		_, status, _, message := queueComparison(ctx, tx, closeID, entity, period, snapshot, item, balance, "scheduled-recurring-comparison", reference)
		if status != 0 {
			if _, err = tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT comparison`); err != nil {
				return 0, err
			}
			if status == 503 {
				failure = fmt.Errorf("close %s: %s", closeID, message)
			}
			if status == 429 || status == 503 {
				break
			}
			continue
		}
		if _, err = tx.ExecContext(ctx, `RELEASE SAVEPOINT comparison`); err != nil {
			return 0, err
		}
		queued++
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return queued, failure
}

// RunComparisons prepares month-end schedule comparisons once a minute; a person approves each draft and posting keeps its own approval.
func RunComparisons(ctx context.Context, db *sql.DB) {
	cursor := "0"
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		next, _, err := sweepComparisons(ctx, db, cursor)
		if err != nil && ctx.Err() == nil {
			log.Print("Scheduled comparisons could not run: ", err)
		}
		cursor = next
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
