package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Every month-end capture gets a books version, once: the books the month-end readers make of it, with each General
// Ledger row kept where the report had it, or the reason the readers cannot read it. The parity check agrees with the
// readers while the capture is as it was stored, and names the capture once they would read it differently.
func TestEveryMonthEndCaptureGetsABooksVersion(t *testing.T) {
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
	org := "org_Books" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Books fixture')`, org)
	must(err)
	var closeID, connection, evidence string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Books client','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM books_versions WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM xero_connections WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,$2,'Books client','fixture','active',$3,now(),'123') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	row := func(id, name, a, b string) string {
		return `{"type":"Data","ColData":[{"id":"` + id + `","value":"` + name + `"},{"value":"` + a + `"}` + map[bool]string{true: `,{"value":"` + b + `"}`, false: ``}[b != "-"] + `]}`
	}
	accounts := `[{"Id":"1","Name":"Checking","Classification":"Asset","AccountType":"Bank"},{"Id":"3","Name":"Owner","Classification":"Equity","AccountType":"Equity"},{"Id":"4","Name":"Sales","Classification":"Revenue","AccountType":"Income"},{"Id":"5","Name":"Rent","Classification":"Expense","AccountType":"Expense"}]`
	tb := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[` + strings.Join([]string{row("1", "Checking", "1500.25", ""), row("3", "Owner", "", "1000.00"), row("4", "Sales", "", "800.25"), row("5", "Rent", "300.00", "")}, ",") + `]}}`
	pl := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[` + row("4", "Sales", "800.25", "-") + `,` + row("5", "Rent", "300.00", "-") + `]}}`
	gl := `{"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Name"},{"ColTitle":"Amount"}]},"Rows":{"Row":[{"type":"Section","Header":{"ColData":[{"id":"5","value":"Rent"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-01"},{"value":"Landlord"},{"value":"300.00"}]}]}},{"type":"Section","Header":{"ColData":[{"id":"4","value":"Sales"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-15"},{"value":"Acme"},{"value":"-500.25"}]},{"type":"Data","ColData":[{"value":"2026-09-20"},{"value":"Birch"},{"value":"-300.00"}]}]}}]}}`
	capture := func(tb string) string {
		t.Helper()
		var id string
		payload := `{"period":"2026-09","source_id":"` + evidence + `","accounts":` + accounts + `,"reports":{"TrialBalance":` + tb + `,"ProfitAndLoss":` + pl + `,"GeneralLedger":` + gl + `}}`
		must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture') RETURNING id::text`, closeID, connection, rand.Text(), payload).Scan(&id))
		return id
	}
	readable, unreadable := capture(tb), capture(`{"Header":{"Currency":"USD"},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Checking"},{"value":"not a number"}]}]}}`)
	// A capture whose General Ledger cannot be read keeps the reason with its version.
	gl = `{"Rows":{"Row":["not a row"]}}`
	brokenLedger := capture(tb)
	// A Xero capture the readers cannot read is kept as unreadable too.
	var xeroConnection, xeroCapture string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,'Books client','fixture','active',$3,now(),'t-1') RETURNING id::text`, closeID, org, rand.Text()).Scan(&xeroConnection))
	must(db.QueryRow(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor) VALUES($1,$2,'t-1','2026-09','d','{}','fixture') RETURNING id::text`, closeID, xeroConnection).Scan(&xeroCapture))

	for more := true; more; {
		more, err = writeBooksVersions(t.Context(), db, 500)
		must(err)
	}
	var currency, balances, activity, accountsKept, problem, company, asOf string
	var typed bool
	var lines int
	must(db.QueryRow(`SELECT currency,typed,balances::text,activity::text,accounts::text,unreadable,company,as_of::text,(SELECT count(*) FROM books_lines l WHERE l.version_id=v.id) FROM books_versions v WHERE source='quickbooks' AND source_id=$1 AND organization_id=$2`, readable, org).Scan(&currency, &typed, &balances, &activity, &accountsKept, &problem, &company, &asOf, &lines))
	var b, a map[string]string
	var kept map[string]booksAccount
	json.Unmarshal([]byte(balances), &b)
	json.Unmarshal([]byte(activity), &a)
	json.Unmarshal([]byte(accountsKept), &kept)
	if currency != "USD" || !typed || problem != "" || company != "123" || asOf != "2026-09-30" || b["1"] != "1500.25" || b["3"] != "-1000" || b["4"] != "-800.25" || a["4"] != "800.25" || a["5"] != "300" || kept["1"] != (booksAccount{Name: "Checking", Class: "Asset", Type: "Bank"}) {
		t.Fatalf("the readers' books of the capture, exactly: %s %v %s %s %s %s %s", currency, typed, balances, activity, accountsKept, problem, asOf)
	}
	if lines != 3 {
		t.Fatalf("each General Ledger row is kept with its account and location: %d", lines)
	}
	var location string
	must(db.QueryRow(`SELECT location FROM books_lines l JOIN books_versions v ON v.id=l.version_id WHERE v.source_id=$1 AND v.source='quickbooks' AND l.account_id='4' ORDER BY l.position LIMIT 1`, readable).Scan(&location))
	if location != "reports.GeneralLedger.Rows.Row[1].Rows.Row[0]" {
		t.Fatalf("a line's report location: %s", location)
	}
	// The unreadable capture's General Ledger is kept all the same.
	must(db.QueryRow(`SELECT count(*) FROM books_lines l JOIN books_versions v ON v.id=l.version_id WHERE v.source='quickbooks' AND v.source_id=$1`, unreadable).Scan(&lines))
	if lines != 3 {
		t.Fatalf("a capture whose reports cannot be read keeps its ledger lines: %d", lines)
	}
	for _, c := range [][2]string{{"quickbooks", unreadable}, {"xero", xeroCapture}} {
		must(db.QueryRow(`SELECT unreadable FROM books_versions WHERE source=$1 AND source_id=$2`, c[0], c[1]).Scan(&problem))
		if problem == "" {
			t.Fatalf("%s capture %s is kept as unreadable, with the reason", c[0], c[1])
		}
	}
	var refused string
	must(db.QueryRow(`SELECT lines_unreadable FROM books_versions WHERE source='quickbooks' AND source_id=$1`, brokenLedger).Scan(&refused))
	if refused == "" {
		t.Fatal("a General Ledger that cannot be read is recorded as such")
	}
	// Written once: another pass writes nothing for these captures.
	var before int
	must(db.QueryRow(`SELECT count(*) FROM books_versions WHERE organization_id=$1`, org).Scan(&before))
	_, err = writeBooksVersions(t.Context(), db, 500)
	must(err)
	var after int
	must(db.QueryRow(`SELECT count(*) FROM books_versions WHERE organization_id=$1`, org).Scan(&after))
	if before != 4 || after != 4 {
		t.Fatalf("one version per capture: %d then %d", before, after)
	}
	checked, mismatched, err := booksParity(t.Context(), db, org, 0)
	must(err)
	if checked != 4 || len(mismatched) != 0 {
		t.Fatalf("the readers agree with every version, a General Ledger refused as it was recorded included: %d %v", checked, mismatched)
	}
	// The worker's run checks every version and records what it found.
	must(runBooksParity(t.Context(), db))
	var finished bool
	var recorded, pending int
	must(db.QueryRow(`SELECT finished_at IS NOT NULL,checked,pending FROM books_parity ORDER BY id DESC LIMIT 1`).Scan(&finished, &recorded, &pending))
	if !finished || recorded < 4 || pending != 0 {
		t.Fatalf("a parity run is recorded, with every capture's version written: %v %d %d", finished, recorded, pending)
	}
	// Once the readers would read a capture differently, the parity check names it.
	_, err = db.Exec(`UPDATE quickbooks_snapshots SET payload=jsonb_set(payload,'{reports,TrialBalance,Rows,Row,0,ColData,1,value}','"1500.26"') WHERE id=$1`, readable)
	must(err)
	_, mismatched, err = booksParity(t.Context(), db, org, 0)
	must(err)
	if len(mismatched) != 1 || mismatched[0] != "quickbooks capture "+readable+": the figures differ" {
		t.Fatalf("a disagreement is named by its capture and what differs: %v", mismatched)
	}
	_, err = db.Exec(`UPDATE quickbooks_snapshots SET payload=jsonb_set(payload,'{reports,TrialBalance,Rows,Row,0,ColData,1,value}','"1500.25"') WHERE id=$1`, readable)
	must(err)
	// So is a stored version whose content no longer matches its capture: its figures, or its ledger lines.
	_, err = db.Exec(`UPDATE books_lines SET cells=jsonb_set(cells,'{ColData,2,value}','"999.00"') WHERE position=1 AND version_id=(SELECT id FROM books_versions WHERE source='quickbooks' AND source_id=$1)`, readable)
	must(err)
	_, mismatched, err = booksParity(t.Context(), db, org, 0)
	must(err)
	if len(mismatched) != 1 || mismatched[0] != "quickbooks capture "+readable+": the ledger lines differ" {
		t.Fatalf("stored lines are compared: %v", mismatched)
	}
	_, err = db.Exec(`UPDATE books_versions SET balances=jsonb_set(balances,'{1}','"1"') WHERE source='quickbooks' AND source_id=$1`, readable)
	must(err)
	_, mismatched, err = booksParity(t.Context(), db, org, 0)
	must(err)
	if len(mismatched) != 1 || mismatched[0] != "quickbooks capture "+readable+": the figures differ" {
		t.Fatalf("stored figures are compared: %v", mismatched)
	}
}
