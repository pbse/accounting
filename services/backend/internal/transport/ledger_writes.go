package transport

import (
	"context"
	"database/sql"
	"net/http"
)

// ledgerWritesOff is why a write to a client's live books is refused while the firm has not turned writing on.
const ledgerWritesOff = "Writing to clients' books is off in Firm settings. A firm administrator can turn it on there; until then, book the approved entry in the ledger yourself."

// refuseLedgerWrites refuses, and says why, a write to a client's live books (a QuickBooks production company or any
// Xero organisation) while the firm has not turned writing to clients' books on in Firm settings, which is off until an
// administrator turns it on (migration 131). In a decision's transaction it takes a share lock on the firm's row, which
// a change of the setting updates, so each write is decided wholly before or after the setting changes. Reading back a
// write already made is never refused, so turning writing off strands no attempt.
// liveWriteTables hold the attempts at writing to clients' live books; an attempt there with a grant is a live one.
var liveWriteTables = []string{"production_postings", "bill_corrections", "credit_applications", "vendor_credit_postings", "xero_postings", "xero_corrections"}

// stopLedgerWrites is what turning writing off does, in the transaction that turns it off: every live write approved
// and not yet started needs a fresh approval, and it returns how many live writes had already started, which finish and
// are read back as usual. A write starts only under a share lock on the firm's row, which turning writing off updates,
// so each write either started before (and is counted) or is refused.
func stopLedgerWrites(ctx context.Context, tx *sql.Tx, org string) (int, error) {
	inFlight := 0
	for _, table := range liveWriteTables {
		var n int
		if _, err := tx.ExecContext(ctx, `UPDATE `+table+` SET approval_expires_at=now() WHERE state='approved' AND grant_id IS NOT NULL AND approval_expires_at>now() AND close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, org); err != nil {
			return 0, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE state='executing' AND grant_id IS NOT NULL AND close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, org).Scan(&n); err != nil {
			return 0, err
		}
		inFlight += n
	}
	return inFlight, nil
}

func refuseLedgerWrites(w http.ResponseWriter, ctx context.Context, q querier, org string) bool {
	var on bool
	if err := q.QueryRowContext(ctx, `SELECT allow_ledger_writes FROM organizations WHERE id=$1 FOR SHARE`, org).Scan(&on); err != nil {
		problem(w, 503, "Firm settings could not be checked. No write was made.")
		return true
	}
	if !on {
		problemCode(w, 409, "LEDGER_WRITES_OFF", ledgerWritesOff)
		return true
	}
	return false
}
