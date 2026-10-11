package transport

import (
	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/quickbooks"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
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
  AND (NOT EXISTS(SELECT 1 FROM books_versions v WHERE v.source='files' AND v.source_id=a.id::text)
   OR EXISTS(SELECT 1 FROM books_versions v JOIN books_layouts l ON l.id=a.layout_id WHERE v.source='files' AND v.source_id=a.id::text
    AND EXISTS(SELECT 1 FROM jsonb_each_text(l.layout->'columns') m WHERE m.value='date') AND NOT EXISTS(SELECT 1 FROM books_lines n WHERE n.version_id=v.id)
    AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(l.reading->'lines','[]'::jsonb)) e WHERE e->>'kind'='posting')))
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
	// An acceptance no longer on the file's reading or scope is not its month's books: nothing is written for it.
	f, ok, err := acceptedFileBooks(ctx, tx, org, name, acceptance, file)
	if err != nil || !ok {
		return err
	}
	content, _ := json.Marshal(map[string]any{"accounts": f.accounts, "balances": f.balances, "activity": f.activity, "activity_unavailable": f.missing,
		"currency": f.currency, "as_of": f.asOf, "basis": f.basis})
	sum := sha256.Sum256(content)
	rawAccounts, _ := json.Marshal(f.accounts)
	rawBalances, _ := json.Marshal(f.balances)
	rawActivity, _ := json.Marshal(f.activity)
	var versionID string
	switch err = tx.QueryRowContext(ctx, `INSERT INTO books_versions(organization_id,close_id,period,source,source_id,company,as_of,captured_at,currency,basis,typed,accounts,balances,activity,digest,activity_unavailable)
 VALUES($1,$2,$3,'files',$4,$5,$6::date,$7,$8,$9,true,$10::jsonb,$11::jsonb,$12::jsonb,$13,$14) ON CONFLICT (source,source_id) DO NOTHING RETURNING id::text`,
		org, closeID, f.period, acceptance, name, f.asOf, f.acceptedAt, strings.ToUpper(f.currency), f.basis,
		string(rawAccounts), string(rawBalances), string(rawActivity), hex.EncodeToString(sum[:]), f.missing).Scan(&versionID); {
	case err == sql.ErrNoRows:
		// Written before its lines were kept: they are added to it, the version itself left as it is.
		if err = tx.QueryRowContext(ctx, `SELECT v.id::text FROM books_versions v WHERE v.source='files' AND v.source_id=$1 AND NOT EXISTS(SELECT 1 FROM books_lines n WHERE n.version_id=v.id)`, acceptance).Scan(&versionID); err == sql.ErrNoRows {
			return tx.Commit()
		} else if err != nil {
			return err
		}
	case err != nil:
		return err
	}
	// A ledger's lines are kept with its version, each under its account's key, at the cell it came from: the
	// transactions behind each figure.
	if f.ledger {
		if _, err = tx.ExecContext(ctx, `INSERT INTO books_lines(version_id,position,account_id,location,cells)
 SELECT $1::bigint,l.ordinality::integer,$3::jsonb->>(l.ordinality::integer-1),l.value->>'ref',jsonb_strip_nulls(jsonb_build_object('date',l.value->'date','reference',l.value->'reference',
   'party',l.value->'party','memo',l.value->'memo','amount',l.value->'balance','account',trim(COALESCE(l.value->>'number','')||' '||COALESCE(l.value->>'name',''))))
 FROM jsonb_array_elements((SELECT COALESCE(jsonb_agg(e ORDER BY n),'[]') FROM jsonb_array_elements((SELECT reading->'lines' FROM books_layouts WHERE id::text=$2)) WITH ORDINALITY x(e,n) WHERE e->>'kind'='posting')) WITH ORDINALITY l`,
			versionID, f.layoutID, f.lineKeys); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// acceptedFile is the books an acceptance gives its month: its file's reading as the map it was accepted on classifies
// it, balances and the month's activity as exact decimals, with what the month's activity is missing, if anything.
type acceptedFile struct {
	// ledger says the accepted reading is a General Ledger's (layoutID, its confirmed reading), and lineKeys its posting
	// lines' account keys, in order, as a JSON array.
	ledger             bool
	layoutID, lineKeys string
	// underived says why a ledger's month-end balances could not be derived; its balances are then only its movements.
	underived                              string
	accounts                               map[string]booksAccount
	balances, activity                     map[string]string
	missing, period, currency, asOf, basis string
	acceptedAt                             time.Time
}

// acceptedFileBooks reads the books acceptance gives its month; ok is false when the acceptance is no longer on its
// file's reading or scope.
func acceptedFileBooks(ctx context.Context, q rowReader, org, name, acceptance, file string) (acceptedFile, bool, error) {
	state, found, err := readFileState(ctx, q, org, name, file)
	if err != nil {
		return acceptedFile{}, false, err
	}
	var layoutID, scopeID, period, stamp string
	var acceptedAt sql.NullTime
	if err = q.QueryRowContext(ctx, `SELECT layout_id::text,scope_id::text,period,created_at,map_stamp FROM books_import_acceptances WHERE id::text=$1`, acceptance).Scan(&layoutID, &scopeID, &period, &acceptedAt, &stamp); err != nil {
		return acceptedFile{}, false, err
	}
	// The classes and types are the map the acceptance was given on, never one confirmed since.
	accepted := map[string]mappedAccount{}
	if mapID, _, ok := strings.Cut(stamp, ":"); ok {
		var raw []byte
		if err = q.QueryRowContext(ctx, `SELECT accounts FROM books_account_maps WHERE id::text=$1`, mapID).Scan(&raw); err != nil && err != sql.ErrNoRows {
			return acceptedFile{}, false, err
		}
		json.Unmarshal(raw, &accepted)
	}
	if !found || state.scope == nil || state.reading.ID != layoutID || state.scope.ID != scopeID {
		return acceptedFile{}, false, nil
	}
	// The reading's accounts as the accepted map maps them, for the accounts and the activity alike.
	for i, a := range state.reading.Accounts {
		state.reading.Accounts[i].Mapped = nil
		if m, ok := accepted[a.Key]; ok {
			state.reading.Accounts[i].Mapped = &m
		}
	}
	f := acceptedFile{accounts: map[string]booksAccount{}, balances: map[string]string{}, activity: map[string]string{}, period: period,
		currency: state.layout.Layout.Currency, asOf: state.layout.Layout.AsOf, basis: state.scope.Basis, acceptedAt: acceptedAt.Time,
		ledger: state.layout.Layout.Ledger(), layoutID: layoutID}
	if f.ledger {
		var raw []byte
		if err = q.QueryRowContext(ctx, `SELECT COALESCE(reading->'lines','[]'::jsonb) FROM books_layouts WHERE id::text=$1`, layoutID).Scan(&raw); err != nil {
			return acceptedFile{}, false, err
		}
		var lines []bookfiles.Line
		if err = json.Unmarshal(raw, &lines); err != nil {
			return acceptedFile{}, false, err
		}
		keys := []string{}
		for _, l := range lines {
			if l.Kind == bookfiles.Posting {
				keys = append(keys, bookfiles.AccountKey(l.Number, l.Name))
			}
		}
		rawKeys, _ := json.Marshal(keys)
		f.lineKeys = string(rawKeys)
	}
	for _, a := range state.reading.Accounts {
		account := booksAccount{Name: strings.TrimSpace(a.Number + " " + a.Name)}
		if m, ok := accepted[a.Key]; ok {
			account.Class, account.Type = m.Class, m.Type
		}
		f.accounts[a.Key], f.balances[a.Key] = account, a.Balance
	}
	month, _, missing := monthActivity(state)
	for key, v := range month {
		f.activity[key] = exactDecimal(v)
	}
	f.missing, f.underived = missing, state.underived
	return f, true, nil
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
	live, err := liveHolds(ctx, q, org, entity, importID, m.Acceptance, kept, b.Balances)
	if err != nil {
		return nil, nil, err
	}
	m.Holds = append(m.Holds, live...)
	return b, &m, nil
}

// liveHolds are what holds an accepted file's statements since it was accepted, beyond the checks it was accepted with
// (kept): a newer account map classifying one of its accounts differently, until the file is accepted again on it (the
// books stay as accepted meanwhile); the prior month's accepted books changed so that it no longer rolls from them; and,
// for a ledger whose stored balances (stored, when given) were derived from them, so that they derive others now.
func liveHolds(ctx context.Context, q rowReader, org, entity, importID, acceptance string, kept []importCheck, stored map[string]*big.Rat) ([]string, error) {
	state, found, err := readFileState(ctx, q, org, entity, importID)
	if err != nil || !found {
		return nil, err
	}
	holds := []string{}
	changed, err := acceptedMapChanged(ctx, q, acceptance, state.reading.Accounts)
	if err != nil {
		return nil, err
	}
	if changed {
		holds = append(holds, "the account map now classifies one of its accounts differently; accept the file again")
	}
	if c := rollForward(state); c.State == "holds" && !slices.ContainsFunc(kept, func(k importCheck) bool { return k.Key == "roll_forward" && k.State == "holds" }) {
		holds = append(holds, "it no longer rolls forward: "+c.Detail)
	}
	if state.layout.Layout.Ledger() && stored != nil {
		// The accounts derived now, and their balances, must be the ones stored.
		changed := !state.derived || len(state.reading.Accounts) != len(stored)
		for _, a := range state.reading.Accounts {
			live, _ := new(big.Rat).SetString(a.Balance)
			if was := stored[a.Key]; live == nil || was == nil || live.Cmp(was) != 0 {
				changed = true
			}
		}
		if changed {
			holds = append(holds, "the prior month's accepted books changed since this ledger was accepted, so its balances no longer roll from them; accept it again")
		}
	}
	return holds, nil
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
	// Last month's books are compared only when they hold nothing, on the same basis and currency, with activity.
	usable := func() {
		switch {
		case lastBooks == nil:
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
	comparative := false
	var ledgerPrior *fluxMonth
	switch err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, org, entity, shiftMonth(period, -1)).Scan(&priorClose); {
	case err == sql.ErrNoRows:
		// A month that is no close can still be a reviewed comparative: its file accepted as the prior period's figures.
		comparison = "Last month has no close in this workspace."
		if lastBooks, last, err = reviewedComparative(ctx, tx, org, entity, shiftMonth(period, -1)); err != nil {
			return nil, tieRefusal{503, "Last month's books are unavailable."}
		}
		comparative = lastBooks != nil
		usable()
	case err != nil:
		return nil, tieRefusal{503, "Last month's close is unavailable."}
	default:
		source, err := closeBooksSource(ctx, tx, priorClose)
		if err != nil {
			return nil, tieRefusal{503, "Last month's books source is unavailable."}
		}
		if source != "files" {
			// Books from a ledger company are compared once a reviewer confirms it is the files' company, through the
			// accounts mapped across.
			m, err := periodBooks(ctx, tx, priorClose, shiftMonth(period, -1))
			if err != nil {
				return nil, tieRefusal{503, "Last month's books are unavailable."}
			}
			if m == nil {
				comparison = "Last month's close has no month-end capture."
				break
			}
			link, err := currentLink(ctx, tx, org, entity, m.Source, m.realm)
			if err != nil {
				return nil, tieRefusal{503, "Last month's books are unavailable."}
			}
			captured, readErr := recordBooks(ctx, tx, m)
			switch {
			case link == nil:
				comparison = "Last month's books come from a ledger company not confirmed as the files' company: confirm it on the client's page."
			case this.Basis != "accrual":
				comparison = "Last month's ledger books are on the accrual basis, and this month's file is not."
			case readErr != nil:
				comparison = "Last month's capture could not be read: " + readErr.Error() + "."
			default:
				back := map[string]string{}
				for key, id := range link.Accounts {
					back[id] = key
				}
				// Each account as the client's files describe it: this month's file, else the account map.
				described := maps.Clone(thisBooks.Accounts)
				if current, err := latestAccountMap(ctx, tx, org, entity); err != nil {
					return nil, tieRefusal{503, "Last month's books are unavailable."}
				} else if current != nil {
					for key, m := range current.Accounts {
						if _, ok := described[key]; !ok {
							described[key] = packAccount{Name: strings.TrimSpace(m.Number + " " + m.Name), Class: m.Class, Type: m.Type}
						}
					}
				}
				if b, missing := relinked(captured, back, described, thisBooks.Typed); missing != "" {
					comparison = "Last month's account " + missing + " is not mapped to the files' accounts."
				} else if b.Currency != thisBooks.Currency {
					comparison = "Last month's books are in a different currency."
				} else {
					lastBooks, ledgerPrior = b, m
				}
			}
			break
		}
		if lastBooks, last, err = fileMonthBooks(ctx, tx, priorClose); err != nil {
			return nil, tieRefusal{503, "Last month's books are unavailable."}
		}
		if lastBooks == nil {
			comparison = "Last month has no accepted file."
		}
		usable()
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
	switch {
	case lastBooks != nil && ledgerPrior != nil:
		prior := captureStatus(ctx, tx, priorClose, ledgerPrior)
		pack["prior"] = map[string]any{"close_id": priorClose, "period": ledgerPrior.Period, "snapshot_id": ledgerPrior.SnapshotID, "as_of": ledgerPrior.CapturedAt, "stale": ledgerPrior.Stale, "status": prior, "source": ledgerPrior.Source}
		note += " " + ledgerPrior.Period + " is compared from its " + map[string]string{"quickbooks": "QuickBooks", "xero": "Xero"}[ledgerPrior.Source] + " books, confirmed as the files' company, through the accounts mapped across."
		if prior != "signed_off" {
			status = prior
		}
	case lastBooks != nil && comparative:
		// A reviewed comparative is no close and carries no sign-off: it is labelled, and leaves the pack's status alone.
		pack["prior"] = map[string]any{"close_id": "", "period": shiftMonth(period, -1), "snapshot_id": "", "as_of": last.AsOf, "stale": false, "status": "comparative", "file": last.File}
		note += " " + shiftMonth(period, -1) + " is a reviewed comparative, its accepted file " + last.File + ": not a close, and not signed off."
	case lastBooks != nil:
		prior := boundStatus(ctx, tx, priorClose, fileCapture+last.Version)
		pack["prior"] = map[string]any{"close_id": priorClose, "period": shiftMonth(period, -1), "snapshot_id": last.Version, "as_of": last.AsOf, "stale": false, "status": prior}
		if prior != "signed_off" {
			status = prior
		}
	default:
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

// fileSupplement reconciles a month's accepted file to the month's books when they come from a live ledger: a supplement
// never replaces the month's books, and one that does not reconcile is shown with its differences and not used
// (docs/books-sources.md, one authoritative books version per month). Each of the file's accounts is matched to one of
// the ledger's by its name, without its number, then by its whole name; a name two ledger accounts share matches
// neither. Nil when the month has no close, or when its books are its files.
func fileSupplement(ctx context.Context, q rowReader, org, name, acceptance, file string) (map[string]any, error) {
	f, ok, err := acceptedFileBooks(ctx, q, org, name, acceptance, file)
	if err != nil || !ok {
		return nil, err
	}
	var closeID string
	switch err = q.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND client_identity(organization_id,entity)=client_identity($1,$2) AND period=$3 ORDER BY id DESC LIMIT 1`, org, name, f.period).Scan(&closeID); {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, err
	}
	if source, err := closeBooksSource(ctx, q, closeID); err != nil || source == "files" {
		return nil, err
	}
	out := map[string]any{"state": "unavailable", "period": f.period, "close_id": closeID}
	// A file whose accepted checks hold its statements is not compared; the month's activity alone holds none of them.
	var raw []byte
	if err = q.QueryRowContext(ctx, `SELECT checks FROM books_import_acceptances WHERE id::text=$1`, acceptance).Scan(&raw); err != nil {
		return nil, err
	}
	var kept []importCheck
	json.Unmarshal(raw, &kept)
	holds := []string{}
	for _, c := range kept {
		if c.State == "holds" && c.Key != "activity" {
			holds = append(holds, c.Title)
		}
	}
	if len(holds) > 0 {
		out["reason"] = "Checks it was accepted with hold its statements, so it is not compared: " + strings.Join(holds, "; ") + "."
		return out, nil
	}
	m, err := periodBooks(ctx, q, closeID, f.period)
	if err != nil {
		return nil, err
	}
	if m == nil {
		out["reason"] = "The month has no captured books to reconcile the file to."
		return out, nil
	}
	out["ledger"], out["captured_at"] = m.Source, m.CapturedAt.UTC()
	captured, err := recordBooks(ctx, q, m)
	if err != nil {
		out["reason"] = "The month's capture could not be read: " + err.Error() + "."
		return out, nil
	}
	if !strings.EqualFold(f.currency, captured.Currency) {
		out["state"], out["differences"] = "differs", []string{"The file is in " + strings.ToUpper(f.currency) + ", the books in " + captured.Currency + "."}
		return out, nil
	}
	matched, differences := reconcileFile(f, captured)
	out["state"], out["accounts"], out["differences"] = "reconciled", matched, differences
	if len(differences) > 0 {
		out["state"] = "differs"
	}
	return out, nil
}

// reconcileFile compares an accepted file's balances with a month's captured books: how many of the file's accounts it
// matched to one of the books' accounts, and every difference, of an amount or of an account found on one side only.
func reconcileFile(f acceptedFile, captured *books) (int, []string) {
	names := map[string]string{}
	for key := range f.balances {
		names[key] = f.accounts[key].Name
	}
	matches := accountsByName(names, captured.Accounts)
	zero := new(big.Rat)
	differences, used := []string{}, map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(f.balances)) {
		stated, _ := new(big.Rat).SetString(f.balances[key])
		if stated == nil {
			stated = new(big.Rat)
		}
		account := f.accounts[key].Name
		found, ok := matches[key]
		if !ok {
			if stated.Sign() != 0 {
				differences = append(differences, account+": "+money(stated)+" in the file, with no account of that name in the books.")
			}
			continue
		}
		used[found] = true
		books := cmp.Or(captured.Balances[found], zero)
		if stated.Cmp(books) != 0 {
			differences = append(differences, fmt.Sprintf("%s: the file says %s, the books %s, a difference of %s.", account, money(stated), money(books), money(new(big.Rat).Sub(stated, books))))
		}
	}
	for _, id := range slices.Sorted(maps.Keys(captured.Balances)) {
		if balance := captured.Balances[id]; !used[id] && balance.Sign() != 0 {
			differences = append(differences, captured.Accounts[id].Name+": "+money(balance)+" in the books, not in the file.")
		}
	}
	return len(used), differences
}

// accountsByName matches each file account (its key to its name) to one of a ledger's accounts by name: without its
// number, then whole. A ledger account is known by its name, the last part of a parent's name, and either without a
// leading number; a name two ledger accounts share matches neither, and each ledger account is matched once, in the
// file accounts' key order.
func accountsByName(file map[string]string, ledger map[string]packAccount) map[string]string {
	normal := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	byName := map[string][]string{}
	for _, id := range slices.Sorted(maps.Keys(ledger)) {
		whole := ledger[id].Name
		last := whole[strings.LastIndex(whole, ":")+1:]
		for _, n := range []string{whole, last, withoutNumber(whole), withoutNumber(last)} {
			if n = normal(n); n != "" && !slices.Contains(byName[n], id) {
				byName[n] = append(byName[n], id)
			}
		}
	}
	matches, used := map[string]string{}, map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(file)) {
		for _, n := range []string{fileAccountName(key, file[key]), file[key]} {
			if ids := byName[normal(n)]; len(ids) == 1 && !used[ids[0]] {
				matches[key], used[ids[0]] = ids[0], true
				break
			}
		}
	}
	return matches
}

// withoutNumber is an account name without a leading account number ("1000 Checking" is "Checking").
func withoutNumber(name string) string {
	fields := strings.Fields(name)
	if len(fields) > 1 && strings.ContainsAny(fields[0], "0123456789") {
		return strings.Join(fields[1:], " ")
	}
	return name
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

// monthChanged is how a file's reading differs from the books its month already accepted from another file: a
// re-export of an accepted month is the evidence that the month changed after it was accepted (docs/books-sources.md,
// Roll-forward and changed prior months). Each account is compared by its key; what was built on the accepted books
// (a sign-off standing, a pack published from them) is listed. Nil unless another file supplies the month's books: its
// newest current acceptance is another file's, and the month's books are not a live ledger's (whose files are
// supplements).
func monthChanged(ctx context.Context, q rowReader, org, name, importID string, s fileState) (map[string]any, error) {
	var acceptance, other, file string
	var at time.Time
	switch err := q.QueryRowContext(ctx, `SELECT a.id::text,a.import_id::text,i.name,a.created_at FROM books_import_acceptances a JOIN books_imports i ON i.id=a.import_id
 WHERE a.organization_id=$1 AND client_identity(a.organization_id,a.client_entity)=client_identity($1,$2) AND a.period=$3
  AND a.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=a.import_id) AND a.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=a.import_id)
 ORDER BY a.id DESC LIMIT 1`, org, name, s.reading.Period).Scan(&acceptance, &other, &file, &at); {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, err
	}
	if other == importID {
		return nil, nil
	}
	var closeID string
	switch err := q.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND client_identity(organization_id,entity)=client_identity($1,$2) AND period=$3 ORDER BY id DESC LIMIT 1`, org, name, s.reading.Period).Scan(&closeID); {
	case err == sql.ErrNoRows:
	case err != nil:
		return nil, err
	default:
		if source, err := closeBooksSource(ctx, q, closeID); err != nil || source != "files" {
			return nil, err
		}
	}
	accepted, ok, err := acceptedFileBooks(ctx, q, org, name, acceptance, other)
	if err != nil || !ok {
		return nil, err
	}
	amount := func(s string) *big.Rat {
		v, ok := new(big.Rat).SetString(cmp.Or(s, "0"))
		if !ok {
			return new(big.Rat)
		}
		return v
	}
	differences, here := []string{}, map[string]bool{}
	for _, a := range s.reading.Accounts {
		here[a.Key] = true
		now, was := amount(a.Balance), amount(accepted.balances[a.Key])
		label := strings.TrimSpace(a.Number + " " + a.Name)
		switch _, known := accepted.balances[a.Key]; {
		case !known:
			differences = append(differences, label+": "+money(now)+", not in the accepted books.")
		case now.Cmp(was) != 0:
			differences = append(differences, fmt.Sprintf("%s: was %s, is now %s.", label, money(was), money(now)))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(accepted.balances)) {
		if was := amount(accepted.balances[key]); !here[key] {
			differences = append(differences, accepted.accounts[key].Name+": was "+money(was)+", not in this file.")
		}
	}
	// What was built on the accepted books: a sign-off standing over them, and packs published from them.
	effects := []string{}
	var signed bool
	var published int
	if err = q.QueryRowContext(ctx, `SELECT COALESCE(bool_or((SELECT s.decision FROM close_signoffs s WHERE s.close_id=c.id ORDER BY s.id DESC LIMIT 1)='signed_off'),false),
 (SELECT count(*) FROM management_publications p JOIN books_versions v ON v.id::text=p.pack->>'snapshot_id' AND v.close_id=p.close_id WHERE v.source='files' AND v.source_id=$4)
 FROM client_closes c WHERE c.organization_id=$1 AND client_identity(c.organization_id,c.entity)=client_identity($1,$2) AND c.period=$3`, org, name, s.reading.Period, acceptance).Scan(&signed, &published); err != nil {
		return nil, err
	}
	if signed {
		effects = append(effects, "The month is signed off on the accepted books: reopen it before accepting this file.")
	}
	if published > 0 {
		effects = append(effects, fmt.Sprintf("%d management pack publication%s for the month %s built on the accepted books.", published, suffix(published), map[bool]string{true: "was", false: "were"}[published == 1]))
	}
	return map[string]any{"file": file, "accepted_at": at.UTC(), "same": len(differences) == 0, "differences": differences, "effects": effects}, nil
}

// reviewedComparative is a month that is no close, as its accepted file gives it (docs/books-sources.md, History and
// takeover): prior-period figures for comparisons, with what holds their statements, as for a month on files. Nil when
// the month's books are not its files, or it has no current acceptance.
func reviewedComparative(ctx context.Context, q rowReader, org, entity, period string) (*books, *fileMonth, error) {
	var acceptance, importID, file string
	var checks []byte
	switch err := q.QueryRowContext(ctx, `SELECT a.id::text,a.import_id::text,i.name,a.checks FROM books_import_acceptances a JOIN books_imports i ON i.id=a.import_id
 WHERE a.organization_id=$1 AND client_identity(a.organization_id,a.client_entity)=client_identity($1,$2) AND a.period=$3 AND books_source($1,$2,$3)='files'
  AND a.layout_id=(SELECT max(x.id) FROM books_layouts x WHERE x.import_id=a.import_id) AND a.scope_id=(SELECT max(y.id) FROM books_import_scopes y WHERE y.import_id=a.import_id)
 ORDER BY a.id DESC LIMIT 1`, org, entity, period).Scan(&acceptance, &importID, &file, &checks); {
	case err == sql.ErrNoRows:
		return nil, nil, nil
	case err != nil:
		return nil, nil, err
	}
	f, ok, err := acceptedFileBooks(ctx, q, org, entity, acceptance, importID)
	if err != nil || !ok {
		return nil, nil, err
	}
	b, err := booksOfVersion(booksVersion{Currency: strings.ToUpper(f.currency), Typed: true, Accounts: f.accounts, Balances: f.balances, Activity: f.activity})
	if err != nil {
		return nil, nil, err
	}
	b.ActivityUnavailable = f.missing
	m := &fileMonth{Acceptance: acceptance, File: file, AsOf: f.asOf, Basis: f.basis}
	var kept []importCheck
	json.Unmarshal(checks, &kept)
	for _, c := range kept {
		if c.State == "holds" && c.Key != "activity" {
			m.Holds = append(m.Holds, c.Title)
		}
	}
	live, err := liveHolds(ctx, q, org, entity, importID, acceptance, kept, nil)
	if err != nil {
		return nil, nil, err
	}
	m.Holds = append(m.Holds, live...)
	return b, m, nil
}

// registerFileLedger serves a month on files' ledger lines for one account, twenty at a time from offset, those naming
// query anywhere when it is given: the transactions behind a figure of its pack (docs/books-sources.md, delivery step 8).
func registerFileLedger(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/{id}/files/ledger", func(w http.ResponseWriter, r *http.Request) {
		accountID, query := r.URL.Query().Get("account_id"), strings.TrimSpace(r.URL.Query().Get("query"))
		offset, err := strconv.Atoi(cmp.Or(r.URL.Query().Get("offset"), "0"))
		if err != nil || offset < 0 || accountID == "" || len(accountID) > 300 || len(query) > 100 {
			problem(w, 422, "Choose an account, a valid page offset and a search of at most 100 characters.")
			return
		}
		_, month, err := fileMonthBooks(r.Context(), db, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "The month's books are unavailable.")
			return
		}
		if month == nil {
			problem(w, 404, "This month has no books from its files.")
			return
		}
		var rows json.RawMessage
		var total int
		if err = db.QueryRowContext(r.Context(), `SELECT (SELECT count(*) FROM books_lines WHERE version_id=$1::bigint AND account_id=$2 AND ($3='' OR strpos(lower(cells::text),lower($3))>0)),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('location',location)||cells ORDER BY position) FROM (SELECT position,location,cells FROM books_lines
  WHERE version_id=$1::bigint AND account_id=$2 AND ($3='' OR strpos(lower(cells::text),lower($3))>0) ORDER BY position OFFSET $4 LIMIT 20) x),'[]')`, month.Version, accountID, query, offset).Scan(&total, &rows); err != nil {
			problem(w, 503, "The month's ledger lines are unavailable.")
			return
		}
		jsonReply(w, 200, map[string]any{"account_id": accountID, "query": query, "offset": offset, "total": total, "rows": rows, "file": month.File})
	})
}
