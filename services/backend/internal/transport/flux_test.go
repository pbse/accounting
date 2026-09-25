package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFluxExplainsMovementsByPayee(t *testing.T) {
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
	client := "Flux " + rand.Text()
	var september, august, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, client).Scan(&september))
	defer func() {
		for _, id := range []string{september, august} {
			db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
	}()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	type payee struct {
		Name      string
		ThisMonth string `json:"this_month"`
		LastMonth string `json:"last_month"`
	}
	type result struct {
		State   string
		Reason  string
		This    struct{ Stale bool }
		Changes []struct {
			Account    string
			Change     string
			New        []payee
			Missing    []payee
			Changed    []payee
			Reconciled bool
			Omitted    int
			Questions  []string
			Causes     string `json:"causes_unavailable"`
		}
	}
	read := func() (out result) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+september+"/flux", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("flux: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	pl := func(rows ...string) string {
		return `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[{"type":"Section","Header":{"ColData":[{"value":"Expenses"},{"value":""}]},"Rows":{"Row":[` + strings.Join(rows, ",") + `]}}]}}`
	}
	account := func(id, name, amount string) string {
		return `{"type":"Data","ColData":[{"id":"` + id + `","value":"` + name + `"},{"value":"` + amount + `"}]}`
	}
	gl := func(sections ...string) string {
		return `{"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Name"},{"ColTitle":"Amount"}]},"Rows":{"Row":[` + strings.Join(sections, ",") + `]}}`
	}
	section := func(id string, rows ...[2]string) string {
		data := []string{}
		for _, r := range rows {
			data = append(data, `{"type":"Data","ColData":[{"value":"2026-09-03"},{"value":"`+r[0]+`"},{"value":"`+r[1]+`"}]}`)
		}
		return `{"type":"Section","Header":{"ColData":[{"id":"` + id + `","value":"account"}]},"Rows":{"Row":[` + strings.Join(data, ",") + `]}}`
	}
	snapshot := func(closeID, period, realm, profit, ledger string) {
		t.Helper()
		var evidence string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
		payload := `{"period":"` + period + `","source_id":"` + evidence + `","accounts":[{"Id":"25","Name":"Utilities","Classification":"Expense"}],"reports":{"ProfitAndLoss":` + profit + `,"GeneralLedger":` + ledger + `}}`
		_, e := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,$3,$4,$5,now(),'fixture')`, closeID, connection, realm, rand.Text(), payload)
		must(e)
	}
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, september, rand.Text()).Scan(&connection))
	current := func(software string, softwareLedger ...[2]string) {
		t.Helper()
		snapshot(september, "2026-09", "123", pl(account("10", "Sales", "5000.00"), account("25", "Utilities", "2000.00"), account("21", "Software", software), account("22", "Rent", "1500.00"), account("30", "Consulting", "900.00")),
			gl(section("25", [2]string{"Con Edison", "1200.00"}, [2]string{"PSEG", "800.00"}), section("21", softwareLedger...), section("22", [2]string{"Landlord", "1500.00"}), section("30", [2]string{"Acme", "-900.00"})))
	}
	current("0.00")
	if got := read(); got.State != "waiting" || !strings.Contains(got.Reason, "2026-08") {
		t.Fatalf("without last month's close there is nothing to compare: %+v", got)
	}
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-08') RETURNING id::text`, client).Scan(&august))
	snapshot(august, "2026-08", "123", pl(account("10", "Sales", "5000.00"), account("25", "Utilities", "800.00"), account("21", "Software", "49.00"), account("22", "Rent", "1500.00")),
		gl(section("25", [2]string{"PSEG", "800.00"}), section("21", [2]string{"CloudSuite", "49.00"}), section("22", [2]string{"Landlord", "1500.00"})))

	got := read()
	if got.State != "ready" || len(got.Changes) != 3 || got.Changes[0].Account != "Utilities" || got.Changes[0].Change != "1200.00" || got.Changes[1].Account != "Consulting" || got.Changes[2].Account != "Software" || got.Changes[2].Change != "-49.00" {
		t.Fatalf("only accounts that moved are listed, largest first: %+v", got)
	}
	utilities, consulting, software := got.Changes[0], got.Changes[1], got.Changes[2]
	if len(consulting.New) != 1 || consulting.New[0].Name != "Acme" || consulting.New[0].ThisMonth != "900.00" || !consulting.Reconciled {
		t.Errorf("a ledger that signs income opposite the Profit and Loss is turned to match: %+v", consulting)
	}
	if len(utilities.New) != 1 || utilities.New[0].Name != "Con Edison" || len(utilities.Changed) != 0 || len(utilities.Missing) != 0 || !utilities.Reconciled {
		t.Errorf("a new payee explains the rise: %+v", utilities)
	}
	if len(software.Missing) != 1 || software.Missing[0].Name != "CloudSuite" || len(software.Questions) != 1 || !strings.Contains(software.Questions[0], "Was there a bill for 2026-09") || !software.Reconciled {
		t.Errorf("a payee that did not recur is asked about: %+v", software)
	}

	current("0.00", [2]string{"", "10.00"}, [2]string{"", "-10.00"})
	if software = read().Changes[2]; len(software.Missing) != 0 || len(software.Questions) != 0 {
		t.Errorf("unnamed rows this month, even netting to zero, mean a missing payee cannot be claimed: %+v", software)
	}

	snapshot(september, "2026-09", "123", pl(account("10", "Sales", "5000.00"), account("25", "Utilities", "2000.00"), account("21", "Software", "49.00"), account("22", "Rent", "1500.00")),
		gl(section("25", [2]string{"Con Edison", "1200.00"}, [2]string{"PSEG", "700.00"}, [2]string{"Water Co", "10.00"}, [2]string{"Gas Co", "10.00"}, [2]string{"Trash Co", "10.00"}, [2]string{"Sewer Co", "10.00"}), section("21", [2]string{"CloudSuite", "49.00"}), section("22", [2]string{"Landlord", "1500.00"})))
	if utilities = read().Changes[0]; utilities.Reconciled || len(utilities.Changed) != 1 || utilities.Changed[0].Name != "PSEG" || len(utilities.New) != 3 || utilities.Omitted != 2 {
		t.Errorf("payees that do not add up to the movement are possible drivers only: %+v", utilities)
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id=$1`, connection)
	must(err)
	if got = read(); got.State != "ready" || !got.This.Stale {
		t.Fatalf("a stale snapshot is reported, not hidden: %+v", got)
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='active' WHERE id=$1`, connection)
	must(err)
	var xero string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, september, rand.Text()).Scan(&xero))
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-08','d','{}','fixture',now()+interval '1 minute')`, august, xero)
	must(err)
	if got = read(); got.State != "waiting" || !strings.Contains(got.Reason, "different ledgers") {
		t.Fatalf("a QuickBooks month is not compared with a Xero month: %+v", got)
	}
	_, err = db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, august)
	must(err)
	snapshot(august, "2026-08", "123", strings.Replace(pl(account("25", "Utilities", "800.00")), "USD", "CAD", 1), gl())
	if got = read(); got.State != "waiting" || !strings.Contains(got.Reason, "different currencies") {
		t.Fatalf("months in different currencies are not compared: %+v", got)
	}
	snapshot(august, "2026-08", "999", pl(account("25", "Utilities", "800.00")), gl())
	if got = read(); got.State != "waiting" || !strings.Contains(got.Reason, "different companies") {
		t.Fatalf("months from different companies are not compared: %+v", got)
	}
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d','{}','fixture',now()+interval '1 minute')`, september, xero)
	must(err)
	if got = read(); got.State != "waiting" || !strings.Contains(got.Reason, "Xero") {
		t.Fatalf("when the newest books come from Xero, QuickBooks changes are not presented as current: %+v", got)
	}

	// Two Xero months compare revenue and expense accounts from each Trial Balance's month columns.
	xeroMonth := func(closeID, period, sales, rent, bank string, invoices ...string) {
		t.Helper()
		row := func(id, label, debit, credit string) string {
			return `{"RowType":"Row","Cells":[{"Value":"` + label + `","Attributes":[{"Id":"account","Value":"` + id + `"}]},{"Value":"` + debit + `"},{"Value":"` + credit + `"},{"Value":"1.00"},{"Value":""}]}`
		}
		tb := `{"Reports":[{"ReportType":"TrialBalance","Rows":[{"RowType":"Header","Cells":[{"Value":"Account"},{"Value":"Debit"},{"Value":"Credit"},{"Value":"YTD Debit"},{"Value":"YTD Credit"}]},{"RowType":"Section","Rows":[` +
			row("x-200", "Sales (200)", "", sales) + "," + row("x-469", "Rent (469)", rent, "") + "," + row("x-090", "Bank (090)", bank, "") + `]}]}]}`
		transactions := ""
		if invoices != nil {
			transactions = `,"transactions":{"records":{"Invoices":[` + strings.Join(invoices, ",") + `]},"open":{}}`
		}
		payload := `{"date":"` + period + `-28","organisation":{"Organisations":[{"BaseCurrency":"USD"}]},"accounts":{"Accounts":[{"AccountID":"x-200","Name":"Sales","Class":"REVENUE"},{"AccountID":"x-469","Name":"Rent","Class":"EXPENSE"},{"AccountID":"x-090","Name":"Bank","Class":"ASSET"}]},"trial_balance":` + tb + transactions + `}`
		_, e := db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1',$3,'d',$4,'fixture',now()+interval '2 minutes'+$5::interval)`, closeID, xero, period, payload, fmt.Sprintf("%d seconds", len(invoices)*10))
		must(e)
	}
	xeroMonth(august, "2026-08", "4000.00", "1500.00", "100.00")
	xeroMonth(september, "2026-09", "5000.00", "1500.00", "999.00")
	if got = read(); got.State != "ready" || len(got.Changes) != 1 || got.Changes[0].Account != "Sales" || got.Changes[0].Change != "1000.00" || !strings.Contains(got.Changes[0].Causes, "Xero") {
		t.Fatalf("two Xero months show the month's revenue and expense movements; snapshots without transactions have no payees: %+v", got)
	}
	// With transactions, a Xero month explains the change by contact.
	invoice := func(id, contact, currency, amount string) string {
		return `{"Type":"ACCREC","InvoiceID":"` + id + `","Status":"AUTHORISED","Date":"2026-08-10T00:00:00","Contact":{"ContactID":"` + contact + `","Name":"` + contact + `"},"CurrencyCode":"` + currency + `","Total":` + amount + `,"LineItems":[{"AccountID":"x-200","LineAmount":` + amount + `}]}`
	}
	xeroMonth(august, "2026-08", "4000.00", "1500.00", "100.00", invoice("a-1", "Acme", "USD", "3000"), invoice("a-2", "Beta", "USD", "1000"))
	xeroMonth(september, "2026-09", "5000.00", "1500.00", "999.00", invoice("s-1", "Acme", "USD", "3000"), invoice("s-2", "Gamma", "USD", "2000"))
	if got = read(); got.State != "ready" || got.Changes[0].Causes != "" || len(got.Changes[0].New) != 1 || got.Changes[0].New[0].Name != "Gamma" || got.Changes[0].New[0].ThisMonth != "2000.00" || len(got.Changes[0].Missing) != 1 || got.Changes[0].Missing[0].Name != "Beta" || !got.Changes[0].Reconciled || len(got.Changes[0].Questions) != 1 {
		t.Fatalf("Xero payees explain the change, signed as the Profit and Loss shows revenue: %+v", got)
	}
	xeroMonth(september, "2026-09", "5000.00", "1500.00", "999.00", invoice("s-1", "Acme", "USD", "3000"), invoice("s-2", "Gamma", "USD", "2000"), invoice("s-3", "Euro Ltd", "EUR", "10"))
	if got = read(); got.Changes[0].Causes == "" || !strings.Contains(got.Changes[0].Causes, "another currency") {
		t.Fatalf("an account with foreign-currency lines is not explained from them: %+v", got)
	}
}
