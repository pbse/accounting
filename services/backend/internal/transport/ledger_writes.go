package transport

import (
	"context"
	"net/http"
)

// ledgerWritesOff is why a write to a client's live books is refused while the firm has not turned writing on.
const ledgerWritesOff = "Writing to clients' books is off in Firm settings. A firm administrator can turn it on there; until then, book the approved entry in the ledger yourself."

// refuseLedgerWrites refuses, and says why, a write to a client's live books (a QuickBooks production company or any
// Xero organisation) while the firm has not turned writing to clients' books on in Firm settings, which is off until an
// administrator turns it on (migration 131). In a decision's transaction it takes a share lock on the firm's row, which
// a change of the setting updates, so each write is decided wholly before or after the setting changes. Reading back a
// write already made is never refused, so turning writing off strands no attempt.
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
