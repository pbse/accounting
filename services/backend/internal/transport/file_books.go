package transport

import (
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/quickbooks"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Months on files (docs/books-sources.md, One authoritative books version per month; delivery step 9, first part): a
// month whose books source is the client's files reads the books version of its file's current acceptance. The
// version is written once per acceptance (source files, source_id the acceptance): its accounts with the map's classes
// and types, their balances, and the month's activity, or why there is none. The worker writes any missing; accepting a
// file and choosing a source write them at once.

// pendingFileVersions lists the files months' books still to write: for each client month whose source is files, has
// a close, and has a current acceptance (on its file's latest reading and scope), the newest such acceptance, when no
// version of it is written yet.
const pendingFileVersions = `SELECT a.organization_id,a.client_entity,a.id::text,a.import_id::text,c.id
 FROM books_import_acceptances a JOIN client_closes c ON c.organization_id=a.organization_id AND client_identity(c.organization_id,c.entity)=client_identity(a.organization_id,a.client_entity) AND c.period=a.period
 WHERE ($1='' OR a.organization_id=$1) AND ($2='' OR client_identity(a.organization_id,a.client_entity)=client_identity(a.organization_id,$2)) AND ($3='' OR a.period=$3)
  AND books_source(a.organization_id,a.client_entity,a.period)='files'
  AND c.id=(SELECT max(d.id) FROM client_closes d WHERE d.organization_id=c.organization_id AND client_identity(d.organization_id,d.entity)=client_identity(c.organization_id,c.entity) AND d.period=c.period)
  AND a.id=(SELECT max(b.id) FROM books_import_acceptances b WHERE b.organization_id=a.organization_id AND client_identity(b.organization_id,b.client_entity)=client_identity(a.organization_id,a.client_entity) AND b.period=a.period
   AND b.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=b.import_id) AND b.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=b.import_id))
  AND NOT EXISTS(SELECT 1 FROM books_versions v WHERE v.source='files' AND v.source_id=a.id::text)
 ORDER BY a.id LIMIT 50`

// writeFileVersions writes the books versions pending for firm org (every firm, for ""), client name and month period
// ("" for any), and says how many it wrote.
func writeFileVersions(ctx context.Context, db *sql.DB, org, name, period string) (int, error) {
	rows, err := db.QueryContext(ctx, pendingFileVersions, org, name, period)
	if err != nil {
		return 0, err
	}
	type pending struct {
		org, client, acceptance, file string
		close                         int64
	}
	var due []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.org, &p.client, &p.acceptance, &p.file, &p.close); err != nil {
			break
		}
		due = append(due, p)
	}
	rows.Close()
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		return 0, err
	}
	written := 0
	for _, p := range due {
		if err = writeFileVersion(ctx, db, p.org, p.client, p.acceptance, p.file, p.close); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// writeFileVersion writes the books version of acceptance id of client name's file, for its month's close, under the
// client's map lock, so the reading, scope and map it reads are the ones the acceptance stands on.
func writeFileVersion(ctx context.Context, db *sql.DB, org, name, acceptance, file string, closeID int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('books_account_maps:'||$1||':'||client_identity($1,$2)))`, org, name); err != nil {
		return err
	}
	state, found, err := readFileState(ctx, tx, org, name, file)
	if err != nil {
		return err
	}
	var layoutID, scopeID, period, stamp string
	var acceptedAt sql.NullTime
	if err = tx.QueryRowContext(ctx, `SELECT layout_id::text,scope_id::text,period,created_at,map_stamp FROM books_import_acceptances WHERE id::text=$1`, acceptance).Scan(&layoutID, &scopeID, &period, &acceptedAt, &stamp); err != nil {
		return err
	}
	// The classes and types are the map the acceptance was given on, never one confirmed since.
	accepted := map[string]mappedAccount{}
	if mapID, _, ok := strings.Cut(stamp, ":"); ok {
		var raw []byte
		if err = tx.QueryRowContext(ctx, `SELECT accounts FROM books_account_maps WHERE id::text=$1`, mapID).Scan(&raw); err != nil && err != sql.ErrNoRows {
			return err
		}
		json.Unmarshal(raw, &accepted)
	}
	// An acceptance no longer on the file's reading or scope is not its month's books: nothing is written for it.
	if !found || state.scope == nil || state.reading.ID != layoutID || state.scope.ID != scopeID {
		return nil
	}
	// The reading's accounts as the accepted map maps them, for the accounts and the activity alike.
	for i, a := range state.reading.Accounts {
		state.reading.Accounts[i].Mapped = nil
		if m, ok := accepted[a.Key]; ok {
			state.reading.Accounts[i].Mapped = &m
		}
	}
	accounts, balances := map[string]booksAccount{}, map[string]string{}
	for _, a := range state.reading.Accounts {
		account := booksAccount{Name: strings.TrimSpace(a.Number + " " + a.Name)}
		if m, ok := accepted[a.Key]; ok {
			account.Class, account.Type = m.Class, m.Type
		}
		accounts[a.Key], balances[a.Key] = account, a.Balance
	}
	activity := map[string]string{}
	month, _, missing := monthActivity(state)
	for key, v := range month {
		activity[key] = exactDecimal(v)
	}
	content, _ := json.Marshal(map[string]any{"accounts": accounts, "balances": balances, "activity": activity, "activity_unavailable": missing,
		"currency": state.layout.Layout.Currency, "as_of": state.layout.Layout.AsOf, "basis": state.scope.Basis})
	sum := sha256.Sum256(content)
	rawAccounts, _ := json.Marshal(accounts)
	rawBalances, _ := json.Marshal(balances)
	rawActivity, _ := json.Marshal(activity)
	if _, err = tx.ExecContext(ctx, `INSERT INTO books_versions(organization_id,close_id,period,source,source_id,company,as_of,captured_at,currency,basis,typed,accounts,balances,activity,digest,activity_unavailable)
 VALUES($1,$2,$3,'files',$4,$5,$6::date,$7,$8,$9,true,$10::jsonb,$11::jsonb,$12::jsonb,$13,$14) ON CONFLICT (source,source_id) DO NOTHING`,
		org, closeID, period, acceptance, name, state.layout.Layout.AsOf, acceptedAt.Time, strings.ToUpper(state.layout.Layout.Currency), state.scope.Basis,
		string(rawAccounts), string(rawBalances), string(rawActivity), hex.EncodeToString(sum[:]), missing); err != nil {
		return err
	}
	return tx.Commit()
}

// writeFileVersionsNow writes a client's pending files months' books at once, after an acceptance or a choice of
// source; the worker writes any this misses.
func writeFileVersionsNow(ctx context.Context, db *sql.DB, org, name, period string) {
	if _, err := writeFileVersions(systemContext(ctx), db, org, name, period); err != nil {
		log.Print("A month's books from files could not be written: ", err)
	}
}

// fileMonth is where a month on files took its books from: the version, the acceptance it is of, the file's name, its
// as-of date and basis, and the acceptance's checks that hold the file's statements.
type fileMonth struct {
	Version, Acceptance, File, AsOf, Basis string
	Holds                                  []string
	// ExportedAt is when the file's books were taken from the client's ledger: the day it was exported, from midnight UTC.
	ExportedAt time.Time
	// ReadAt is when the books version was written from the accepted file.
	ReadAt time.Time
}

// fileMonthBooks is close id's books from its files: the version of the month's newest current acceptance (on its
// file's latest reading and scope), or nil while none is, or while that acceptance's version is still to be written.
func fileMonthBooks(ctx context.Context, q rowReader, closeID string) (*books, *fileMonth, error) {
	var m fileMonth
	var v booksVersion
	var accounts, balances, activity, cashFlow, uncounted []byte
	var unavailable, org, entity, importID string
	var checks []byte
	err := q.QueryRowContext(ctx, `SELECT v.id::text,v.source_id,i.name,v.as_of::text,v.basis,a.exported_on::timestamp AT TIME ZONE 'UTC',v.currency,v.typed,v.accounts,v.balances,v.activity,v.cash_flow,v.uncounted,v.activity_unavailable,a.checks,v.created_at,c.organization_id,c.entity,a.import_id::text
 FROM client_closes c JOIN books_import_acceptances a ON a.organization_id=c.organization_id AND client_identity(a.organization_id,a.client_entity)=client_identity(c.organization_id,c.entity) AND a.period=c.period
 JOIN books_imports i ON i.id=a.import_id JOIN books_versions v ON v.source='files' AND v.source_id=a.id::text AND v.close_id=c.id
 WHERE c.id::text=$1 AND a.id=(SELECT max(b.id) FROM books_import_acceptances b WHERE b.organization_id=c.organization_id AND client_identity(b.organization_id,b.client_entity)=client_identity(c.organization_id,c.entity) AND b.period=c.period
   AND b.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=b.import_id) AND b.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=b.import_id))`, closeID).
		Scan(&m.Version, &m.Acceptance, &m.File, &m.AsOf, &m.Basis, &m.ExportedAt, &v.Currency, &v.Typed, &accounts, &balances, &activity, &cashFlow, &uncounted, &unavailable, &checks, &m.ReadAt, &org, &entity, &importID)
	if err == sql.ErrNoRows {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	// The export day is a calendar date: said in UTC, whatever the server's zone, so a page reading its date reads that day.
	m.ExportedAt = m.ExportedAt.UTC()
	json.Unmarshal(accounts, &v.Accounts)
	json.Unmarshal(balances, &v.Balances)
	json.Unmarshal(activity, &v.Activity)
	json.Unmarshal(uncounted, &v.Uncounted)
	if len(cashFlow) > 0 {
		v.CashFlow = cashFlow
	}
	b, err := booksOfVersion(v)
	if err != nil {
		return nil, nil, err
	}
	b.ActivityUnavailable = unavailable
	// Every check that holds but the month's activity holds the file's statements; the activity holds the profit and
	// loss alone, which the books say themselves.
	var kept []importCheck
	json.Unmarshal(checks, &kept)
	for _, c := range kept {
		if c.State == "holds" && c.Key != "activity" {
			m.Holds = append(m.Holds, c.Title)
		}
	}
	// A newer account map classifying one of the file's accounts differently holds its statements too, until the file is
	// accepted again on it; the books stay as accepted meanwhile.
	if state, found, err := readFileState(ctx, q, org, entity, importID); err != nil {
		return nil, nil, err
	} else if found {
		changed, err := acceptedMapChanged(ctx, q, m.Acceptance, state.reading.Accounts)
		if err != nil {
			return nil, nil, err
		}
		if changed {
			m.Holds = append(m.Holds, "the account map now classifies one of its accounts differently; accept the file again")
		}
	}
	return b, &m, nil
}

// closeFilesPack is the management pack of a month whose books are its files, compared with last month's when its
// books are its files too, in the same currency. It is signed off while the sign-off of every month shown stands over its books version.
func closeFilesPack(ctx context.Context, tx *sql.Tx, closeID, org, entity, period string) (map[string]any, error) {
	thisBooks, this, err := fileMonthBooks(ctx, tx, closeID)
	if err != nil {
		return nil, tieRefusal{503, "The month's books are unavailable."}
	}
	if thisBooks == nil {
		// An acceptance whose books are still to be written is not one to give again.
		accepted, err := fileBooksPending(ctx, tx, closeID)
		if err != nil {
			return nil, tieRefusal{503, "The month's books are unavailable."}
		}
		if accepted {
			return map[string]any{"state": "waiting", "reason": "The month's accepted file is being written as its books: look again in a minute."}, nil
		}
		return map[string]any{"state": "waiting", "reason": "This month's books are its files: accept a reading of the month's file first."}, nil
	}
	if len(this.Holds) > 0 {
		return map[string]any{"state": "waiting", "reason": "The accepted file " + this.File + " has checks that hold its statements: " + strings.Join(this.Holds, "; ") + "."}, nil
	}
	// The firm's imported statement of cash flows completes the pack, as for a month on Xero.
	var i cashFlowImport
	var currency string
	switch err = tx.QueryRowContext(ctx, `SELECT csv,source,attestation,actor,created_at,currency FROM cash_flow_imports WHERE close_id::text=$1 AND company='files' ORDER BY id DESC LIMIT 1`, closeID).Scan(&i.CSV, &i.Source, &i.Attestation, &i.Actor, &i.At, &currency); {
	case err == nil && currency == thisBooks.Currency:
		thisBooks.Imported = &i
	case err != nil && err != sql.ErrNoRows:
		return nil, tieRefusal{503, "The imported cash-flow statement is unavailable."}
	}
	var lastBooks *books
	var last *fileMonth
	comparison := ""
	var priorClose string
	switch err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, org, entity, shiftMonth(period, -1)).Scan(&priorClose); {
	case err == sql.ErrNoRows:
		comparison = "Last month has no close in this workspace."
	case err != nil:
		return nil, tieRefusal{503, "Last month's close is unavailable."}
	default:
		source, err := closeBooksSource(ctx, tx, priorClose)
		if err != nil {
			return nil, tieRefusal{503, "Last month's books source is unavailable."}
		}
		if source != "files" {
			comparison = "Last month's books come from another source."
			break
		}
		if lastBooks, last, err = fileMonthBooks(ctx, tx, priorClose); err != nil {
			return nil, tieRefusal{503, "Last month's books are unavailable."}
		}
		switch {
		case lastBooks == nil:
			comparison = "Last month has no accepted file."
		case len(last.Holds) > 0:
			comparison, lastBooks = "Last month's file has checks that hold its statements.", nil
		case last.Basis != this.Basis:
			comparison, lastBooks = "Last month's books are on another basis.", nil
		case lastBooks.Currency != thisBooks.Currency:
			comparison, lastBooks = "Last month's books are in a different currency.", nil
		case lastBooks.ActivityUnavailable != "" && thisBooks.ActivityUnavailable == "":
			comparison, lastBooks = "Last month's activity is unavailable: "+lastBooks.ActivityUnavailable+".", nil
		}
	}
	pack := managementPack(thisBooks, lastBooks)
	if thisBooks.Imported == nil && len(thisBooks.CashFlow) == 0 {
		pack["cash_flow"] = map[string]any{"unavailable": "The client's files give no Statement of Cash Flows: import the firm's statement for the month (CSV) to complete the pack. Balance changes alone cannot separate depreciation, assets bought on credit or borrowing from cash. Book cash and its change are on the balance sheet."}
	}
	signed, err := signedOff(ctx, tx, closeID)
	if err != nil {
		return nil, tieRefusal{503, "Close readiness unavailable."}
	}
	status := boundStatus(ctx, tx, closeID, fileCapture+this.Version)
	pack["open"] = !signed
	pack["state"], pack["entity"], pack["period"], pack["currency"], pack["basis"] = "ready", entity, period, thisBooks.Currency, basisTitle(this.Basis)
	pack["source"], pack["snapshot_id"], pack["as_of"], pack["stale"], pack["status"], pack["file"] = "files", this.Version, this.AsOf, false, status, this.File
	note := "Figures are the client's books from its accepted file " + this.File + ", as of " + this.AsOf + "; approved drafts not yet posted are excluded."
	if lastBooks != nil {
		prior := boundStatus(ctx, tx, priorClose, fileCapture+last.Version)
		pack["prior"] = map[string]any{"close_id": priorClose, "period": shiftMonth(period, -1), "snapshot_id": last.Version, "as_of": last.AsOf, "stale": false, "status": prior}
		if prior != "signed_off" {
			status = prior
		}
	} else {
		pack["comparison_unavailable"] = comparison
	}
	if status != "signed_off" {
		note += " Provisional until the sign-off of every month shown stands."
	}
	if disclosure, ok := pack["cash_flow"].(map[string]any)["disclosure"].(string); ok {
		note += " " + disclosure
	}
	return finishPack(ctx, tx, org, pack, note, status)
}

// basisTitle is a basis as the pack names it: Accrual, Cash or Modified cash.
func basisTitle(basis string) string {
	return map[string]string{"accrual": "Accrual", "cash": "Cash", "modified_cash": "Modified cash"}[basis]
}

// fileCoverage is a month on files' coverage, as ledgerCoverage gives a capture's: each account of its accepted file's
// books with the evidence it needs, by the type the account map gives it (a bank or card account is reconciled; a
// prepaid or accrued account gets its proposal; any other, an accountant's review). Its capture is "file:" and the books
// version; null while the month has none.
func fileCoverage(ctx context.Context, q rowReader, closeID string) (json.RawMessage, error) {
	b, month, err := fileMonthBooks(ctx, q, closeID)
	if err != nil {
		return nil, err
	}
	if b == nil {
		return json.RawMessage("null"), nil
	}
	var digest, period string
	var at time.Time
	if err = q.QueryRowContext(ctx, `SELECT digest,period,captured_at FROM books_versions WHERE id::text=$1`, month.Version).Scan(&digest, &period, &at); err != nil {
		return nil, err
	}
	items := []quickbooks.CoverageItem{}
	for _, key := range slices.Sorted(maps.Keys(b.Accounts)) {
		a := b.Accounts[key]
		name := strings.ToLower(a.Name)
		kind := "accountant_review"
		switch {
		case a.Type == "Bank" || a.Type == "Credit Card":
			kind = "reconciliation"
		case a.Type == "Other Current Asset" && strings.Contains(name, "prepaid"):
			kind = "prepaid"
		case a.Type == "Other Current Liability" && strings.Contains(name, "accru"):
			kind = "accrual"
		}
		items = append(items, quickbooks.NewCoverageItem(key, a.Name, kind, month.File+": "+key))
	}
	return json.Marshal(map[string]any{"source": "files", "snapshot_id": fileCapture + month.Version, "digest": digest, "source_id": month.Acceptance, "period": period,
		"captured_at": at.UTC(), "items": items, "stale": false})
}

// contraName is an account whose balance normally sits opposite its class's side, which QuickBooks marks by subtype and a
// file only by name: accumulated depreciation, amortisation or depletion, and retained earnings.
var contraName = regexp.MustCompile(`(?i)\baccumulated\s+(depreciation|amorti[sz]ation|depletion)\b|\bretained\s+earnings\b`)

// fileBalanceExceptions are a month on files' balances to review, as quickbooks.AbnormalBalances finds a capture's: opening
// balance equity still held, holding or clearing accounts with a balance, and balances opposite their normal side,
// skipping contra accounts and owner draws. An account is recognised by its name without its number.
func fileBalanceExceptions(b *books) []ledger.BalanceException {
	out := []ledger.BalanceException{}
	for _, key := range slices.Sorted(maps.Keys(b.Balances)) {
		a, net := b.Accounts[key], b.Balances[key]
		name := fileAccountName(key, a.Name)
		lower := strings.ToLower(name)
		if item := ledger.OpeningEquity(key, name, "", net, b.Currency, "balance"); item != nil {
			out = append(out, *item)
			continue
		}
		if a.Class == "" || contraName.MatchString(name) || a.Class == "Equity" && (strings.Contains(lower, "draw") || strings.Contains(lower, "distribution")) {
			continue
		}
		if item := ledger.Holding(key, a.Name, a.Class, net, b.Currency, "balance"); item != nil {
			out = append(out, *item)
		} else if item := ledger.WrongSide(key, a.Name, a.Class, net, b.Currency, "balance"); item != nil {
			out = append(out, *item)
		}
	}
	slices.SortFunc(out, func(a, b ledger.BalanceException) int { return strings.Compare(a.Account, b.Account) })
	return out
}

// filesVersion is SQL for close expression c's books from its files, when they are its books: the version of the month's
// newest current acceptance (as fileMonthBooks reads it, without the holds it reads besides), as id, and when its file
// was exported, as exported_at; no row otherwise.
func filesVersion(c string) string {
	return `(SELECT v.id,a.exported_on::timestamp AT TIME ZONE 'UTC' AS exported_at FROM client_closes fc JOIN books_import_acceptances a ON a.organization_id=fc.organization_id AND client_identity(a.organization_id,a.client_entity)=client_identity(fc.organization_id,fc.entity) AND a.period=fc.period
 JOIN books_versions v ON v.source='files' AND v.source_id=a.id::text AND v.close_id=fc.id
 WHERE fc.id=` + c + ` AND books_source(fc.organization_id,fc.entity,fc.period)='files' AND a.id=(SELECT max(b.id) FROM books_import_acceptances b WHERE b.organization_id=fc.organization_id AND client_identity(b.organization_id,b.client_entity)=client_identity(fc.organization_id,fc.entity) AND b.period=fc.period
   AND b.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=b.import_id) AND b.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=b.import_id)))`
}

// fileBooksPending says whether close id's month has a current acceptance (on its file's latest reading and scope) whose
// books are not read: while fileMonthBooks finds none, that acceptance's version is still to be written, and the
// acceptance is not one to give again.
func fileBooksPending(ctx context.Context, q rowReader, closeID string) (bool, error) {
	var accepted bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM client_closes c JOIN books_import_acceptances a ON a.organization_id=c.organization_id AND client_identity(a.organization_id,a.client_entity)=client_identity(c.organization_id,c.entity) AND a.period=c.period
 WHERE c.id::text=$1 AND a.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=a.import_id) AND a.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=a.import_id))`, closeID).Scan(&accepted)
	return accepted, err
}
