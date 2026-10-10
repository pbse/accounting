package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"close.local/backend/internal/quickbooks"
)

// The month-books record (docs/books-sources.md, One month-books record, and Migration; delivery step 3): one stored
// form of a month's books that every source writes, whatever it is. A QuickBooks period capture and a Xero capture keep
// their raw payloads and also get a books version: the books the month-end readers make of the capture
// (readLedgerMonth), with the capture it came from, the company, the month-end date and a digest. The worker writes a
// version for every capture that has none, the existing ones first and then each new one, so writing them is idempotent
// and the captures' own writers are unchanged. No reader reads the record yet: each switches only once the parity check
// agrees with what it computes from the payload, for every close.

// booksAccount is an account as a books version keeps it.
type booksAccount struct {
	Name    string `json:"name"`
	Class   string `json:"class"`
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

// booksVersion is one month's books, as stored.
type booksVersion struct {
	Currency  string                  `json:"currency"`
	Typed     bool                    `json:"typed"`
	Accounts  map[string]booksAccount `json:"accounts"`
	Balances  map[string]string       `json:"balances"`
	Activity  map[string]string       `json:"activity"`
	CashFlow  json.RawMessage         `json:"cash_flow"`
	Uncounted []string                `json:"uncounted"`
}

// exactDecimal writes an amount exactly: as a decimal when it has one (up to twelve places, as ledgers' amounts do),
// else as a fraction, which big.Rat reads back either way.
func exactDecimal(r *big.Rat) string {
	for places := 0; places <= 12; places++ {
		scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)))
		if scaled.IsInt() {
			return r.FloatString(places)
		}
	}
	return r.RatString()
}

// versionOf is the books a month-end reader makes of capture m, in their stored form, or the reason it cannot read them.
func versionOf(m *fluxMonth) (booksVersion, error) {
	b, err := readLedgerMonth(m)
	if err != nil {
		return booksVersion{}, err
	}
	v := booksVersion{Currency: b.Currency, Typed: b.Typed, Accounts: map[string]booksAccount{}, Balances: map[string]string{}, Activity: map[string]string{}, CashFlow: b.CashFlow, Uncounted: b.Uncounted}
	for id, a := range b.Accounts {
		v.Accounts[id] = booksAccount(a)
	}
	for id, amount := range b.Balances {
		v.Balances[id] = exactDecimal(amount)
	}
	for id, amount := range b.Activity {
		v.Activity[id] = exactDecimal(amount)
	}
	if v.Uncounted == nil {
		v.Uncounted = []string{}
	}
	if len(v.CashFlow) == 0 || string(v.CashFlow) == "null" {
		v.CashFlow = nil
	}
	return v, nil
}

// digest is the version's figures' digest: of its JSON, whose maps encode in key order, so the same books always give
// the same digest.
func (v booksVersion) digest() string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// captureMonth reads one month-end capture as periodBooks gives it to the readers, with its close, firm and month.
func captureMonth(ctx context.Context, q rowReader, source, id string) (m *fluxMonth, closeID, org string, err error) {
	m = &fluxMonth{Source: source}
	switch source {
	case "quickbooks":
		err = q.QueryRowContext(ctx, `SELECT s.id::text,s.realm,s.captured_at,c.id::text,c.organization_id,c.period,s.payload->'accounts',s.payload->'reports'->'ProfitAndLoss',s.payload->'reports'->'GeneralLedger',COALESCE(s.payload->'reports'->'TrialBalance','null'),COALESCE(s.payload->'reports'->'CashFlow','null')
 FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE s.id::text=$1 AND s.payload ? 'period'`, id).Scan(&m.SnapshotID, &m.realm, &m.CapturedAt, &closeID, &org, &m.Period, &m.accounts, &m.pl, &m.gl, &m.tb, &m.cashFlow)
	case "xero":
		err = q.QueryRowContext(ctx, `SELECT s.id::text,s.tenant_id,s.captured_at,c.id::text,c.organization_id,c.period,s.payload FROM xero_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE s.id::text=$1`, id).Scan(&m.SnapshotID, &m.realm, &m.CapturedAt, &closeID, &org, &m.Period, &m.xero)
	default:
		err = fmt.Errorf("unknown books source %q", source)
	}
	return m, closeID, org, err
}

// pendingCaptures are month-end captures with no books version yet, oldest first.
const pendingCaptures = `SELECT source,id FROM (
 SELECT 'quickbooks' AS source,s.id::text AS id,s.captured_at FROM quickbooks_snapshots s WHERE s.payload ? 'period' AND NOT EXISTS(SELECT 1 FROM books_versions v WHERE v.source='quickbooks' AND v.source_id=s.id::text)
 UNION ALL SELECT 'xero',s.id::text,s.captured_at FROM xero_snapshots s WHERE NOT EXISTS(SELECT 1 FROM books_versions v WHERE v.source='xero' AND v.source_id=s.id::text)) p
 ORDER BY captured_at,source,id LIMIT $1`

// writeBooksVersion stores the books version of one capture, once: a capture the readers cannot read is stored with the
// reason, so it is not tried again and a reader that would also refuse it agrees. Its ledger lines come with it, for a
// QuickBooks capture's General Ledger, each with the report location it came from.
func writeBooksVersion(ctx context.Context, db *sql.DB, source, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, closeID, org, err := captureMonth(ctx, tx, source, id)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	v, readErr := versionOf(m)
	unreadable := ""
	if readErr != nil {
		unreadable = readErr.Error()
		v = booksVersion{Accounts: map[string]booksAccount{}, Balances: map[string]string{}, Activity: map[string]string{}, Uncounted: []string{}}
	}
	accounts, _ := json.Marshal(v.Accounts)
	balances, _ := json.Marshal(v.Balances)
	activity, _ := json.Marshal(v.Activity)
	uncounted, _ := json.Marshal(v.Uncounted)
	var cashFlow any
	if v.CashFlow != nil {
		cashFlow = string(v.CashFlow)
	}
	var versionID string
	err = tx.QueryRowContext(ctx, `INSERT INTO books_versions(organization_id,close_id,period,source,source_id,company,as_of,captured_at,currency,typed,accounts,balances,activity,cash_flow,uncounted,digest,unreadable)
 VALUES($1,$2,$3,$4,$5,$6,$7::date,$8,$9,$10,$11::jsonb,$12::jsonb,$13::jsonb,$14::jsonb,$15::jsonb,$16,$17) ON CONFLICT (source,source_id) DO NOTHING RETURNING id::text`,
		org, closeID, m.Period, source, id, m.realm, monthEnd(m.Period).Format("2006-01-02"), m.CapturedAt, v.Currency, v.Typed, string(accounts), string(balances), string(activity), cashFlow, string(uncounted), v.digest(), unreadable).Scan(&versionID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	// The General Ledger's rows are kept whether or not the readers can read the month's financial reports.
	if source == "quickbooks" && len(m.gl) > 0 && string(m.gl) != "null" {
		lines, e := quickbooks.LedgerLinesOf(m.gl)
		if e == nil {
			raw, _ := json.Marshal(lines)
			_, err = tx.ExecContext(ctx, `INSERT INTO books_lines(version_id,position,account_id,location,cells)
 SELECT $1::bigint,l.ordinality::integer,l.value->>'account_id',l.value->>'location',l.value->'cells' FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY l`, versionID, string(raw))
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE books_versions SET lines_unreadable=$2 WHERE id=$1::bigint`, versionID, e.Error())
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// writeBooksVersions writes the versions of up to limit captures that have none, and says how many it wrote.
func writeBooksVersions(ctx context.Context, db *sql.DB, limit int) (int, error) {
	rows, err := db.QueryContext(ctx, pendingCaptures, limit)
	if err != nil {
		return 0, err
	}
	type capture struct{ source, id string }
	var pending []capture
	for rows.Next() {
		var c capture
		if err = rows.Scan(&c.source, &c.id); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	for i, c := range pending {
		if err = writeBooksVersion(ctx, db, c.source, c.id); err != nil {
			return i, fmt.Errorf("%s capture %s: %w", c.source, c.id, err)
		}
	}
	return len(pending), nil
}

// RunBooksVersions keeps every month-end capture's books version written: the existing captures first, a batch at a
// time, then each new one within a minute of its capture; and once they are, checks the readers against every version
// each hour.
func RunBooksVersions(ctx context.Context, db *sql.DB) {
	var checked time.Time
	for {
		written, err := writeBooksVersions(ctx, db, 200)
		if err != nil && ctx.Err() == nil {
			log.Print("Books versions could not be written: ", err)
		}
		// Once the versions are written, the readers are checked against every one of them, hourly.
		if err == nil && written < 200 && time.Since(checked) > time.Hour {
			if e := runBooksParity(ctx, db); e != nil && ctx.Err() == nil {
				log.Print("The books parity check could not run: ", e)
			} else {
				checked = time.Now()
			}
		}
		wait := time.Minute
		if err == nil && written == 200 {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// booksParity compares, for the captures with a books version, the books the readers compute from the capture's payload
// with the stored version: the same figures, or the same refusal. It says how many it checked and the captures where
// the two disagree.
func booksParity(ctx context.Context, q interface {
	rowReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org string) (checked int, mismatched []string, err error) {
	// A page of versions at a time, by id, so every version is checked however many there are.
	after := int64(0)
	for {
		var page []storedVersion
		if page, err = storedVersions(ctx, q, org, after, 200); err != nil || len(page) == 0 {
			return checked, mismatched, err
		}
		after = page[len(page)-1].row
		n, wrong, e := compareVersions(ctx, q, page)
		checked, mismatched = checked+n, append(mismatched, wrong...)
		if e != nil {
			return checked, mismatched, e
		}
	}
}

type storedVersion struct {
	row                            int64
	source, id, digest, unreadable string
}

// storedVersions is up to limit versions after row id after, of firm org or of every firm ("").
func storedVersions(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org string, after int64, limit int) ([]storedVersion, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,source,source_id,digest,unreadable FROM books_versions WHERE ($1='' OR organization_id=$1) AND id>$2 ORDER BY id LIMIT $3`, org, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var versions []storedVersion
	for rows.Next() {
		var v storedVersion
		if err = rows.Scan(&v.row, &v.source, &v.id, &v.digest, &v.unreadable); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// compareVersions recomputes each version from its capture and names those that disagree.
func compareVersions(ctx context.Context, q rowReader, versions []storedVersion) (checked int, mismatched []string, err error) {
	for _, v := range versions {
		m, _, _, e := captureMonth(ctx, q, v.source, v.id)
		if e == sql.ErrNoRows {
			continue
		}
		if e != nil {
			return checked, mismatched, e
		}
		checked++
		computed, readErr := versionOf(m)
		switch {
		case readErr != nil && v.unreadable == "":
			mismatched = append(mismatched, v.source+" "+v.id+": the readers now refuse it: "+readErr.Error())
		case readErr == nil && v.unreadable != "":
			mismatched = append(mismatched, v.source+" "+v.id+": stored as unreadable, but the readers read it")
		case readErr == nil && computed.digest() != v.digest:
			mismatched = append(mismatched, v.source+" "+v.id+": the figures differ")
		}
	}
	return checked, mismatched, nil
}

// adminBooks is the month-books record's state across firms, for staff deciding when a reader may switch to it
// (delivery step 5): how many month-end captures there are, how many have a version and how many are unreadable, and
// the worker's latest finished parity run over every version, with the captures where it disagrees (identifiers
// only).
func adminBooks(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var captures, versions, unreadable int
	var parity json.RawMessage
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM quickbooks_snapshots WHERE payload ? 'period')+(SELECT count(*) FROM xero_snapshots),
 (SELECT count(*) FROM books_versions),(SELECT count(*) FROM books_versions WHERE unreadable<>''),
 COALESCE((SELECT jsonb_build_object('checked',checked,'mismatched',mismatched,'finished_at',finished_at) FROM books_parity WHERE finished_at IS NOT NULL ORDER BY id DESC LIMIT 1),'null'::jsonb)`).Scan(&captures, &versions, &unreadable, &parity)
	})
	if err != nil {
		adminFirmProblem(w, err, "books read")
		return
	}
	jsonReply(w, 200, map[string]any{"captures": captures, "versions": versions, "pending": max(captures-versions, 0), "unreadable": unreadable, "parity": parity})
}

// runBooksParity checks every version against its capture and records the run, so staff read its outcome.
func runBooksParity(ctx context.Context, db *sql.DB) error {
	var run int64
	if err := db.QueryRowContext(ctx, `INSERT INTO books_parity DEFAULT VALUES RETURNING id`).Scan(&run); err != nil {
		return err
	}
	checked, mismatched, err := booksParity(ctx, db, "")
	if err != nil {
		return err
	}
	if mismatched == nil {
		mismatched = []string{}
	}
	raw, _ := json.Marshal(mismatched)
	_, err = db.ExecContext(ctx, `UPDATE books_parity SET finished_at=now(),checked=$2,mismatched=$3::jsonb WHERE id=$1`, run, checked, string(raw))
	return err
}
