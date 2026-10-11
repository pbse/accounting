package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"strings"

	"close.local/backend/internal/wf"
)

// Readers move onto the month-books record (docs/books-sources.md, Migration step 3; delivery step 5): a reader reads a
// month's books from its stored version, rather than recomputing them from the capture's payload, only where the record
// is known to agree. Two things must hold. Staff have switched the reader on (BOOKS_RECORD_READERS names it), once the
// switch puts no launch work at risk. And the version was checked by the latest parity run of the reader build now
// running, which found every version agreeing (books_record_gate): a version written since, or any version while that
// run found a disagreement, is read from its capture as before. So a reader switched on reads exactly the books it read before, from the record.

// recordReaders are the readers staff have switched onto the record: month_books is the month-end books the management
// pack, the cash-flow import and budget against actual read.
func recordReader(name string) bool {
	for _, on := range strings.Split(os.Getenv("BOOKS_RECORD_READERS"), ",") {
		if strings.TrimSpace(on) == name {
			return true
		}
	}
	return false
}

// recordBooks is a month's books from capture m: from its stored version when month_books reads the record and the
// version is verified, else as readLedgerMonth makes them from the capture.
func recordBooks(ctx context.Context, q rowReader, m *fluxMonth) (*books, error) {
	if recordReader("month_books") {
		b, kept, err := storedMonthBooks(ctx, q, m.Source, m.SnapshotID)
		if err != nil || kept {
			return b, err
		}
	}
	return readLedgerMonth(m)
}

// storedMonthBooks is the books stored for capture id of source, when its version is verified: no later than the last
// version the latest parity run of this build checked, while that run found every version agreeing (books_record_gate,
// read in the same query). They are rebuilt as readLedgerMonth made them, or the reason the readers refused the capture.
func storedMonthBooks(ctx context.Context, q rowReader, source, id string) (*books, bool, error) {
	var v booksVersion
	var accounts, balances, activity, cashFlow, uncounted []byte
	var unreadable string
	err := q.QueryRowContext(ctx, `SELECT currency,typed,accounts,balances,activity,cash_flow,uncounted,unreadable FROM books_versions
 WHERE source=$1 AND source_id=$2 AND id<=COALESCE((SELECT g.through FROM books_record_gate g WHERE g.build=$3 AND g.clean),0) ORDER BY id DESC LIMIT 1`,
		source, id, wf.CodeVersion).Scan(&v.Currency, &v.Typed, &accounts, &balances, &activity, &cashFlow, &uncounted, &unreadable)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if unreadable != "" {
		if unreadable == errCurrency.Error() {
			return nil, true, errCurrency
		}
		return nil, true, errors.New(unreadable)
	}
	json.Unmarshal(accounts, &v.Accounts)
	json.Unmarshal(balances, &v.Balances)
	json.Unmarshal(activity, &v.Activity)
	json.Unmarshal(uncounted, &v.Uncounted)
	if len(cashFlow) > 0 {
		v.CashFlow = cashFlow
	}
	b, err := booksOfVersion(v)
	return b, true, err
}

// booksOfVersion is the books a stored version holds, as readLedgerMonth made them.
func booksOfVersion(v booksVersion) (*books, error) {
	b := &books{Currency: v.Currency, Typed: v.Typed, CashFlow: v.CashFlow, Accounts: map[string]packAccount{}, Balances: map[string]*big.Rat{}, Activity: map[string]*big.Rat{}}
	for id, a := range v.Accounts {
		b.Accounts[id] = packAccount(a)
	}
	for _, side := range []struct {
		stored map[string]string
		into   map[string]*big.Rat
	}{{v.Balances, b.Balances}, {v.Activity, b.Activity}} {
		for id, amount := range side.stored {
			r, ok := new(big.Rat).SetString(amount)
			if !ok {
				return nil, errors.New("a stored amount cannot be read")
			}
			side.into[id] = r
		}
	}
	if len(v.Uncounted) > 0 {
		b.Uncounted = v.Uncounted
	}
	return b, nil
}
