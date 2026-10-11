package transport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log"
	"strings"
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
