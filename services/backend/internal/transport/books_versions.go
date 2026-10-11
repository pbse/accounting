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
	"os"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/wf"
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

// unreadVersion is what an unreadable capture's version holds: no figures, only why the readers refuse it.
func unreadVersion() booksVersion {
	return booksVersion{Accounts: map[string]booksAccount{}, Balances: map[string]string{}, Activity: map[string]string{}, Uncounted: []string{}}
}

// digest is the version's figures' digest: of its JSON, whose maps encode in key order, so the same books always give
// the same digest.
func (v booksVersion) digest() string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// captureMonth reads one month-end capture as periodBooks gives it to the readers, with its close, firm and month. A
// report the capture lacks reads as null, so the readers refuse it and its version is kept as unreadable.
func captureMonth(ctx context.Context, q rowReader, source, id string) (m *fluxMonth, closeID, org string, err error) {
	m = &fluxMonth{Source: source}
	// A capture's id is its snapshot's bigint key; any other is no capture.
	if _, e := strconv.ParseInt(id, 10, 64); e != nil {
		return m, "", "", sql.ErrNoRows
	}
	switch source {
	case "quickbooks":
		err = q.QueryRowContext(ctx, `SELECT s.id::text,s.realm,s.captured_at,c.id::text,c.organization_id,c.period,COALESCE(s.payload->'accounts','null'),COALESCE(s.payload->'reports'->'ProfitAndLoss','null'),COALESCE(s.payload->'reports'->'GeneralLedger','null'),COALESCE(s.payload->'reports'->'TrialBalance','null'),COALESCE(s.payload->'reports'->'CashFlow','null')
 FROM quickbooks_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE s.id=$1::bigint AND s.payload ? 'period'`, id).Scan(&m.SnapshotID, &m.realm, &m.CapturedAt, &closeID, &org, &m.Period, &m.accounts, &m.pl, &m.gl, &m.tb, &m.cashFlow)
	case "xero":
		err = q.QueryRowContext(ctx, `SELECT s.id::text,s.tenant_id,s.captured_at,c.id::text,c.organization_id,c.period,s.payload FROM xero_snapshots s JOIN client_closes c ON c.id=s.close_id WHERE s.id=$1::bigint`, id).Scan(&m.SnapshotID, &m.realm, &m.CapturedAt, &closeID, &org, &m.Period, &m.xero)
	default:
		err = fmt.Errorf("unknown books source %q", source)
	}
	return m, closeID, org, err
}

// writeBooksVersion stores the books version of one capture, once: a capture the readers cannot read is stored with the
// reason, so it is not tried again and a reader that would also refuse it agrees. Its ledger lines come with it, for a
// QuickBooks capture's General Ledger, each with the report location it came from.
func writeBooksVersion(ctx context.Context, db *sql.DB, source, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The capture leaves the queue with its version, or alone when it is gone (its close was deleted).
	if _, err = tx.ExecContext(ctx, `DELETE FROM books_pending WHERE source=$1 AND source_id=$2::bigint`, source, id); err != nil {
		return err
	}
	m, closeID, org, err := captureMonth(ctx, tx, source, id)
	if err == sql.ErrNoRows {
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	v, readErr := versionOf(m)
	unreadable := ""
	if readErr != nil {
		unreadable, v = readErr.Error(), unreadVersion()
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
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	// The General Ledger's rows are kept whether or not the readers can read the month's financial reports.
	if lines, kept, e := captureLines(m); e != nil {
		_, err = tx.ExecContext(ctx, `UPDATE books_versions SET lines_unreadable=$2 WHERE id=$1::bigint`, versionID, e.Error())
	} else if kept {
		raw, _ := json.Marshal(lines)
		if _, err = tx.ExecContext(ctx, `INSERT INTO books_lines(version_id,position,account_id,location,cells)
 SELECT $1::bigint,l.ordinality::integer,l.value->>'account_id',l.value->>'location',l.value->'cells' FROM jsonb_array_elements($2::jsonb) WITH ORDINALITY l`, versionID, string(raw)); err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE books_versions SET lines_digest=$2 WHERE id=$1::bigint`, versionID, linesDigest(lines))
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// captureLines is a capture's ledger lines, and whether it has a General Ledger to keep them from (a QuickBooks
// capture's; the General Ledger's rows are kept whether or not the readers can read the month's financial reports).
func captureLines(m *fluxMonth) ([]quickbooks.LedgerLine, bool, error) {
	if m.Source != "quickbooks" || len(m.gl) == 0 || string(m.gl) == "null" {
		return nil, false, nil
	}
	lines, err := quickbooks.LedgerLinesOf(m.gl)
	return lines, err == nil, err
}

// linesDigest is the digest of a version's ledger lines, in order: of each line's account, location and cells as the
// database keeps JSON (its jsonb text), so lines read back from books_lines give the same digest.
func linesDigest(lines []quickbooks.LedgerLine) string {
	h := sha256.New()
	for _, l := range lines {
		var cells any
		json.Unmarshal(l.Cells, &cells)
		canonical, _ := json.Marshal(cells)
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", l.AccountID, l.Location, canonical)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// writeBooksVersions writes the versions of up to limit captures waiting in books_pending, oldest first, and says
// whether more may be waiting. A capture joins books_pending in the transaction that saves it, so none is missed.
func writeBooksVersions(ctx context.Context, db *sql.DB, limit int) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT source,source_id::text FROM books_pending ORDER BY queued_at,source,source_id LIMIT $1`, limit)
	if err != nil {
		return false, err
	}
	type capture struct{ source, id string }
	var pending []capture
	for rows.Next() {
		var c capture
		if err = rows.Scan(&c.source, &c.id); err != nil {
			rows.Close()
			return false, err
		}
		pending = append(pending, c)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return false, err
	}
	// A capture whose version cannot be written goes to the back of the queue, so it never holds the rest up; the first
	// failure is reported, and the capture is tried again after them.
	var failed error
	for _, c := range pending {
		if err = writeBooksVersion(ctx, db, c.source, c.id); err != nil {
			if ctx.Err() != nil {
				return false, err
			}
			if failed == nil {
				failed = fmt.Errorf("%s capture %s: %w", c.source, c.id, err)
			}
			if _, e := db.ExecContext(ctx, `UPDATE books_pending SET queued_at=now() WHERE source=$1 AND source_id=$2::bigint`, c.source, c.id); e != nil {
				return false, e
			}
		}
	}
	return len(pending) == limit, failed
}

// RunBooksVersions keeps every month-end capture's books version written: the existing captures first, a batch at a
// time, then each new one within a minute of its capture.
func RunBooksVersions(ctx context.Context, db *sql.DB) {
	for {
		more, err := writeBooksVersions(ctx, db, 500)
		if err != nil && ctx.Err() == nil {
			log.Print("Books versions could not be written: ", err)
		}
		wait := time.Minute
		if err == nil && more {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// RunBooksParity checks the readers against every books version each hour, apart from the loop that writes them, so a
// long check never holds a new capture's version back.
func RunBooksParity(ctx context.Context, db *sql.DB) {
	for {
		if err := runBooksParity(ctx, db); err != nil && ctx.Err() == nil {
			log.Print("The books parity check could not run: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

// booksParity compares, for the captures with a books version, the books the readers compute from the capture's payload
// with the stored version: the same figures, or the same refusal. It says how many it checked and the captures where
// the two disagree.
func booksParity(ctx context.Context, q interface {
	rowReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org string, through int64) (checked int, mismatched []string, err error) {
	// A page of versions at a time, by id, so every version up to through (0: every one) is checked however many
	// there are.
	after := int64(0)
	for {
		var page []storedVersion
		if page, err = storedVersions(ctx, q, org, after, through, 200); err != nil {
			return checked, mismatched, err
		}
		if len(page) == 0 {
			break
		}
		after = page[len(page)-1].row
		n, wrong, e := compareVersions(ctx, q, page)
		checked, mismatched = checked+n, append(mismatched, wrong...)
		if e != nil {
			return checked, mismatched, e
		}
	}
	// Every capture has its version, or waits in the queue for it: one with neither is missing from the record.
	rows, err := q.QueryContext(ctx, `SELECT x.source,x.id FROM (`+unversionedCaptures+`) x JOIN client_closes c ON c.id=x.close_id
 WHERE ($1='' OR c.organization_id=$1) AND NOT EXISTS(SELECT 1 FROM books_pending p WHERE p.source=x.source AND p.source_id=x.id) ORDER BY 1,2`, org)
	if err != nil {
		return checked, mismatched, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, id string
		if err = rows.Scan(&source, &id); err != nil {
			return checked, mismatched, err
		}
		mismatched = append(mismatched, source+" capture "+id+": it has no version")
	}
	return checked, mismatched, rows.Err()
}

// unversionedCaptures is SQL for each month-end capture with no books version yet: its source, id and close. Captures
// are looked up by their own bigint ids, so their primary keys serve every lookup.
const unversionedCaptures = `SELECT 'quickbooks' AS source,s.id,s.close_id FROM quickbooks_snapshots s WHERE s.payload ? 'period'
  AND NOT EXISTS(SELECT 1 FROM books_versions v WHERE v.source='quickbooks' AND v.source_id=s.id::text)
 UNION ALL SELECT 'xero',s.id,s.close_id FROM xero_snapshots s WHERE NOT EXISTS(SELECT 1 FROM books_versions v WHERE v.source='xero' AND v.source_id=s.id::text)`

type storedVersion struct {
	row                            int64
	source, id, digest, unreadable string
}

// storedVersions is up to limit versions after row id after (and up to through, unless it is 0), of firm org or of every
// firm ("").
func storedVersions(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, org string, after, through int64, limit int) ([]storedVersion, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,source,source_id,digest,unreadable FROM books_versions WHERE ($1='' OR organization_id=$1) AND id>$2 AND ($3=0 OR id<=$3) ORDER BY id LIMIT $4`, org, after, through, limit)
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

// compareVersions recomputes each version from its capture and compares it with what is stored: the figures, read
// back from the stored version, and its ledger lines, read back from books_lines. It names each that disagrees by its
// capture and what disagrees, never by what the books hold, since staff read the result.
func compareVersions(ctx context.Context, q interface {
	rowReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, versions []storedVersion) (checked int, mismatched []string, err error) {
	for _, v := range versions {
		m, closeID, org, e := captureMonth(ctx, q, v.source, v.id)
		name := v.source + " capture " + v.id
		if e == sql.ErrNoRows {
			// A version goes with its capture; one left without it has nothing to agree with.
			checked++
			mismatched = append(mismatched, name+": the capture is gone")
			continue
		}
		if e != nil {
			return checked, mismatched, e
		}
		checked++
		computed, readErr := versionOf(m)
		stored, storedLines, recordedLines, linesRefused, e := storedBooks(ctx, q, v.row)
		if e != nil {
			return checked, mismatched, e
		}
		// What identifies the books: the firm and close, the month, its end and the company they are of, and when they
		// were read.
		if stored.org != org || stored.close != closeID || stored.period != m.Period || stored.asOf != monthEnd(m.Period).Format("2006-01-02") || stored.company != m.realm || !stored.captured.Equal(m.CapturedAt) {
			mismatched = append(mismatched, name+": what identifies the books differs")
			continue
		}
		switch {
		case readErr != nil && v.unreadable == "":
			mismatched = append(mismatched, name+": the readers now refuse it")
			continue
		case readErr == nil && v.unreadable != "":
			mismatched = append(mismatched, name+": stored as unreadable, but the readers read it")
			continue
		case readErr == nil && (computed.digest() != v.digest || stored.digest() != v.digest):
			mismatched = append(mismatched, name+": the figures differ")
			continue
		case readErr != nil && readErr.Error() != v.unreadable:
			mismatched = append(mismatched, name+": the readers refuse it for another reason")
			continue
		case readErr != nil && (stored.digest() != unreadVersion().digest() || v.digest != unreadVersion().digest()):
			mismatched = append(mismatched, name+": stored as unreadable, but it holds figures")
			continue
		}
		// The lines must match the capture's General Ledger, both as recorded with the version and as stored row by row.
		lines, kept, lineErr := captureLines(m)
		switch {
		case lineErr != nil && lineErr.Error() != linesRefused:
			mismatched = append(mismatched, name+": its ledger lines cannot be read")
		case lineErr != nil && (recordedLines != "" || storedLines != linesDigest(nil)):
			mismatched = append(mismatched, name+": its ledger lines are refused, but lines are stored")
		case lineErr != nil:
			// The General Ledger is refused now as it was when the version was written, and no lines are kept: the
			// record agrees.
		case linesRefused != "":
			mismatched = append(mismatched, name+": its ledger lines are stored as unreadable, but the readers read them")
		case kept && (recordedLines != linesDigest(lines) || storedLines != linesDigest(lines)):
			mismatched = append(mismatched, name+": the ledger lines differ")
		case !kept && (recordedLines != "" || storedLines != linesDigest(nil)):
			mismatched = append(mismatched, name+": the ledger lines differ")
		}
	}
	return checked, mismatched, nil
}

// storedVersionOf is a stored version: its figures, and what identifies them.
type storedVersionOf struct {
	booksVersion
	org, close, period, asOf, company string
	captured                          time.Time
}

// storedBooks reads a stored version's figures back with what identifies them, the digest of its stored ledger lines,
// the lines' digest recorded with the version ("" when it kept none), and why its General Ledger could not be read, if
// it could not.
func storedBooks(ctx context.Context, q interface {
	rowReader
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, row int64) (storedVersionOf, string, string, string, error) {
	var stored storedVersionOf
	v := &stored.booksVersion
	var accounts, balances, activity, uncounted []byte
	var cashFlow []byte
	var recorded, refused string
	if err := q.QueryRowContext(ctx, `SELECT currency,typed,accounts,balances,activity,cash_flow,uncounted,lines_digest,lines_unreadable,organization_id,close_id::text,period,as_of::text,company,captured_at FROM books_versions WHERE id=$1`, row).Scan(&v.Currency, &v.Typed, &accounts, &balances, &activity, &cashFlow, &uncounted, &recorded, &refused, &stored.org, &stored.close, &stored.period, &stored.asOf, &stored.company, &stored.captured); err != nil {
		return stored, "", "", "", err
	}
	json.Unmarshal(accounts, &v.Accounts)
	json.Unmarshal(balances, &v.Balances)
	json.Unmarshal(activity, &v.Activity)
	json.Unmarshal(uncounted, &v.Uncounted)
	if len(cashFlow) > 0 {
		v.CashFlow = cashFlow
	}
	rows, err := q.QueryContext(ctx, `SELECT account_id,location,cells FROM books_lines WHERE version_id=$1 ORDER BY position`, row)
	if err != nil {
		return stored, "", "", "", err
	}
	defer rows.Close()
	var lines []quickbooks.LedgerLine
	for rows.Next() {
		var l quickbooks.LedgerLine
		var cells []byte
		if err = rows.Scan(&l.AccountID, &l.Location, &cells); err != nil {
			return stored, "", "", "", err
		}
		l.Cells = cells
		lines = append(lines, l)
	}
	return stored, linesDigest(lines), recorded, refused, rows.Err()
}

// adminBooks is the month-books record's state across firms, for staff deciding when a reader may switch to it
// (delivery step 5): how many month-end captures there are, how many have a version and how many are unreadable, and
// the worker's latest finished parity run, with the captures where it disagrees (identifiers only), how many captures
// were still waiting when it began, how many versions were written since and how many captures have no version now,
// whether queued or not, the reader build it checked, and a later run still going or stalled (one that failed never
// finishes): it is the gate only when all three counts are none, it checked the build now running, and no later run
// is unfinished.
func adminBooks(w http.ResponseWriter, r *http.Request, db *sql.DB) {
	var captures, pending, unreadable int
	var parity json.RawMessage
	err := adminRead(r.Context(), db, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM quickbooks_snapshots WHERE payload ? 'period')+(SELECT count(*) FROM xero_snapshots),
 (SELECT count(*) FROM (`+unversionedCaptures+`) x),(SELECT count(*) FROM books_versions WHERE unreadable<>''),
 COALESCE((SELECT jsonb_build_object('checked',p.checked,'pending',p.pending,'mismatched',p.mismatched,'finished_at',p.finished_at,'build',p.build,
  'later',(SELECT jsonb_build_object('started_at',l.started_at,'stalled',l.started_at<now()-interval '2 hours') FROM books_parity l WHERE l.id>p.id ORDER BY l.id DESC LIMIT 1),'since',(SELECT count(*) FROM books_versions v WHERE v.id>p.through),'waiting',(SELECT count(*) FROM (`+unversionedCaptures+`) w)) FROM books_parity p WHERE p.finished_at IS NOT NULL ORDER BY p.id DESC LIMIT 1),'null'::jsonb)`).Scan(&captures, &pending, &unreadable, &parity)
	})
	if err != nil {
		adminFirmProblem(w, err, "books read")
		return
	}
	readers := []string{}
	for _, name := range strings.Split(os.Getenv("BOOKS_RECORD_READERS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			readers = append(readers, name)
		}
	}
	jsonReply(w, 200, map[string]any{"captures": captures, "versions": captures - pending, "pending": pending, "unreadable": unreadable, "parity": parity, "build": wf.CodeVersion, "readers": readers})
}

// runBooksParity checks every version against its capture and records the run, so staff read its outcome.
func runBooksParity(ctx context.Context, db *sql.DB) error {
	// The captures still waiting when the run begins, and the last version it covers: a clean run is the gate only while
	// none was waiting and no version has been written since.
	var run, through int64
	if err := db.QueryRowContext(ctx, `INSERT INTO books_parity(pending,through,build) SELECT (SELECT count(*) FROM books_pending),COALESCE((SELECT max(id) FROM books_versions),0),$1 RETURNING id,through`, wf.CodeVersion).Scan(&run, &through); err != nil {
		return err
	}
	checked, mismatched, err := booksParity(ctx, db, "", through)
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
