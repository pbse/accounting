package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"close.local/backend/internal/wf"
)

// Switched on, the month's books are read from the record where the parity runs verified it, and they are the books the
// capture gives; switched off, or past what was verified, they are read from the capture as before.
func TestTheMonthBooksAreReadFromTheRecordOnceVerified(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	// A versioned build: an unversioned local one never reads the record.
	build := wf.CodeVersion
	wf.CodeVersion = "record-test-" + rand.Text()
	defer func() { wf.CodeVersion = build }()
	org := "org_Record" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Record fixture')`, org)
	must(err)
	var closeID, connection, evidence string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Record client','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM books_record_gate WHERE build=$1`, wf.CodeVersion)
		db.Exec(`DELETE FROM books_lines WHERE version_id IN (SELECT id FROM books_versions WHERE organization_id=$1)`, org)
		db.Exec(`DELETE FROM books_versions WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,$2,'Record client','fixture','active',$3,now(),'123') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	row := func(id, name, a, b string) string {
		return `{"type":"Data","ColData":[{"id":"` + id + `","value":"` + name + `"},{"value":"` + a + `"}` + map[bool]string{true: `,{"value":"` + b + `"}`, false: ``}[b != "-"] + `]}`
	}
	accounts := `[{"Id":"1","Name":"Checking","Classification":"Asset","AccountType":"Bank"},{"Id":"3","Name":"Owner","Classification":"Equity","AccountType":"Equity"},{"Id":"4","Name":"Sales","Classification":"Revenue","AccountType":"Income"},{"Id":"5","Name":"Rent","Classification":"Expense","AccountType":"Expense"}]`
	tb := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[` + strings.Join([]string{row("1", "Checking", "1500.25", ""), row("3", "Owner", "", "1000.00"), row("4", "Sales", "", "800.25"), row("5", "Rent", "300.00", "")}, ",") + `]}}`
	pl := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[` + row("4", "Sales", "800.25", "-") + `,` + row("5", "Rent", "300.00", "-") + `]}}`
	capture := func(tb string) string {
		t.Helper()
		var id string
		payload := `{"period":"2026-09","source_id":"` + evidence + `","accounts":` + accounts + `,"reports":{"TrialBalance":` + tb + `,"ProfitAndLoss":` + pl + `,"GeneralLedger":{"Rows":{"Row":[]}}}}`
		must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture') RETURNING id::text`, closeID, connection, rand.Text(), payload).Scan(&id))
		return id
	}
	readable, unreadable := capture(tb), capture(`{"Header":{"Currency":"USD"},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Checking"},{"value":"not a number"}]}]}}`)
	for _, id := range []string{readable, unreadable} {
		must(writeBooksVersion(t.Context(), db, "quickbooks", id))
	}
	// A parity run of this build read both versions and found every version agreeing.
	_, err = db.Exec(`INSERT INTO books_record_gate(build,run,through,clean,finished_at) VALUES($1,1,(SELECT max(id) FROM books_versions),true,now())
 ON CONFLICT (build) DO UPDATE SET run=1,through=EXCLUDED.through,clean=true`, wf.CodeVersion)
	must(err)
	_, err = db.Exec(`UPDATE books_versions SET reading_run=1,verified_run=1 WHERE organization_id=$1`, org)
	must(err)
	month := func(id string) *fluxMonth {
		t.Helper()
		m, _, _, err := captureMonth(t.Context(), db, "quickbooks", id)
		must(err)
		return m
	}
	same := func(a, b *books) bool {
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		var x, y any
		json.Unmarshal(ja, &x)
		json.Unmarshal(jb, &y)
		return reflect.DeepEqual(x, y)
	}

	t.Setenv("BOOKS_RECORD_READERS", "month_books")
	fromCapture, err := readLedgerMonth(month(readable))
	must(err)
	fromRecord, err := recordBooks(t.Context(), db, month(readable))
	must(err)
	if !same(fromCapture, fromRecord) {
		t.Fatalf("the record gives the books the capture gives:\n%+v\n%+v", fromCapture, fromRecord)
	}
	// A capture the readers refuse is refused from the record for the same reason.
	_, captureErr := readLedgerMonth(month(unreadable))
	if _, recordErr := recordBooks(t.Context(), db, month(unreadable)); captureErr == nil || recordErr == nil || recordErr.Error() != captureErr.Error() {
		t.Fatalf("refused alike: %v %v", captureErr, recordErr)
	}
	// It is the record that is read: a stored figure shows.
	_, err = db.Exec(`UPDATE books_versions SET balances=jsonb_set(balances,'{1}','"1.00"') WHERE source='quickbooks' AND source_id=$1`, readable)
	must(err)
	if b, err := recordBooks(t.Context(), db, month(readable)); err != nil || b.Balances["1"].FloatString(2) != "1.00" {
		t.Fatalf("read from the record: %v %v", b, err)
	}
	// A version the gate's run did not verify, whether no run has or another run did (an earlier one, or one that
	// overlapped it), is read from its capture.
	for _, other := range []any{nil, 0, 2} {
		_, err = db.Exec(`UPDATE books_versions SET verified_run=$2 WHERE source='quickbooks' AND source_id=$1`, readable, other)
		must(err)
		if b, err := recordBooks(t.Context(), db, month(readable)); err != nil || b.Balances["1"].FloatString(2) != "1500.25" {
			t.Fatalf("a version verified by run %v, not the gate's, the capture: %v %v", other, b, err)
		}
	}
	_, err = db.Exec(`UPDATE books_versions SET verified_run=1 WHERE source='quickbooks' AND source_id=$1`, readable)
	must(err)
	// Switched off, the capture is read.
	t.Setenv("BOOKS_RECORD_READERS", "")
	if b, err := recordBooks(t.Context(), db, month(readable)); err != nil || b.Balances["1"].FloatString(2) != "1500.25" {
		t.Fatalf("switched off, the capture: %v %v", b, err)
	}
	// An unversioned local build never reads the record, whatever its gate says.
	versioned := wf.CodeVersion
	wf.CodeVersion = "dev"
	if recordReader("month_books") {
		t.Fatal("an unversioned build reads the captures")
	}
	wf.CodeVersion = versioned
	// Switched on, a parity run that found a disagreement verifies nothing: the capture is read.
	t.Setenv("BOOKS_RECORD_READERS", "month_books")
	_, err = db.Exec(`UPDATE books_record_gate SET clean=false WHERE build=$1`, wf.CodeVersion)
	must(err)
	if b, err := recordBooks(t.Context(), db, month(readable)); err != nil || b.Balances["1"].FloatString(2) != "1500.25" {
		t.Fatalf("an unclean run verifies nothing: %v %v", b, err)
	}
}
