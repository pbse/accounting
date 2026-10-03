package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestBudgetParsesMapsAndComparesByHand(t *testing.T) {
	if _, err := parseBudget("Name,2026-09\nSales,10"); err == nil {
		t.Error("the first column must be Account")
	}
	if _, err := parseBudget("Account,2026-09\nSales,10.005"); err == nil || !strings.Contains(err.Error(), "cents") {
		t.Errorf("an amount beyond cents is refused: %v", err)
	}
	if _, err := parseBudget("Account,2026-09\nSales,1\nsales,2"); err == nil {
		t.Error("a repeated account is refused")
	}
	if _, err := parseBudget("\ufeffAccount,2026-09\nSales,10"); err != nil {
		t.Errorf("a UTF-8 BOM on Account is accepted: %v", err)
	}
	// July has no books below, so its budget must not count in year to date either.
	lines, err := parseBudget("Account,2026-07,2026-08,2026-09\nSales,,\"9,000\",\"$10,000.00\"\nRent,1500,1500,1500\nMarketing,,0,0\nRefunds,,-200,-100\nTravel,,300,")
	if err != nil || len(lines) != 5 || lines[0].Months["2026-08"] != "9000.00" || lines[0].Months["2026-09"] != "10000.00" || lines[4].Months["2026-09"] != "0.00" {
		t.Fatalf("thousands separators and a dollar sign are read; a blank month is zero: %+v %v", lines, err)
	}
	accounts := map[string]packAccount{
		"10": {"Sales", "Revenue", "Income", ""}, "20": {"Rent", "Expense", "Expense", ""}, "21": {"Marketing", "Expense", "Expense", ""},
		"22": {"Refunds", "Revenue", "Income", ""}, "23": {"Travel", "Expense", "Expense", ""}, "24": {"Travel", "Expense", "Expense", ""},
		"30": {"Software", "Expense", "Expense", ""}, "1": {"Checking", "Asset", "Bank", ""},
	}
	mapBudget(lines, accounts)
	if lines[0].AccountID != "10" || lines[1].AccountID != "20" || lines[4].AccountID != "" {
		t.Fatalf("lines map by exact name; two Travel accounts leave Travel unmapped: %+v", lines)
	}
	if p := budgetProblems(lines, accounts); len(p) != 1 || !strings.Contains(p[0], "Travel") {
		t.Fatalf("an unmapped line blocks approval: %v", p)
	}
	lines[4].AccountID = "1"
	if p := budgetProblems(lines, accounts); len(p) != 1 || !strings.Contains(p[0], "no longer an income or expense") {
		t.Fatalf("a mapping to an account that is not income or expense in the chart blocks approval: %v", p)
	}
	lines[4].AccountID = "23"
	rat := func(s string) *big.Rat { v, _ := new(big.Rat).SetString(s); return v }
	// June has books but no budget column, and an account active only in August still shows.
	accounts["31"] = packAccount{"Legal", "Expense", "Expense", ""}
	actuals := map[string]map[string]*big.Rat{
		"2026-06": {"10": rat("5000")},
		"2026-08": {"10": rat("8500"), "20": rat("1500"), "22": rat("-150"), "23": rat("350"), "31": rat("90")},
		"2026-09": {"10": rat("11000"), "20": rat("1600"), "21": rat("50"), "22": rat("-100"), "30": rat("400")},
	}
	rows, totals, missing, unbudgeted := compareBudget(lines, actuals, accounts, "2026-09", "2026-08", []string{"2026-06", "2026-07", "2026-08", "2026-09"}, rat("1000"))
	byName := map[string]budgetRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	sales, rent, marketing, refunds, software := byName["Sales"], byName["Rent"], byName["Marketing"], byName["Refunds"], byName["Software"]
	if sales.Month.Variance != "1000.00" || *sales.Month.Percent != "10.0" || !sales.Month.Favorable || !sales.Month.Material || *sales.Prior != "8500.00" {
		t.Errorf("sales 1,000 over a 10,000 budget is a favorable, material 10%% variance: %+v", sales)
	}
	if rent.Month.Variance != "100.00" || rent.Month.Favorable || rent.Month.Material || rent.YTD.Actual != "3100.00" || rent.YTD.Budget != "3000.00" {
		t.Errorf("rent 100 over budget is unfavorable and immaterial; year to date sums only the months with books: %+v", rent)
	}
	if marketing.Month.Percent != nil || refunds.Month.Percent != nil || refunds.Month.Variance != "0.00" {
		t.Errorf("no percentage against a zero or negative budget: %+v %+v", marketing.Month, refunds.Month)
	}
	if !software.Unbudgeted || software.Month.Budget != "0.00" || software.Month.Favorable {
		t.Errorf("activity on an unbudgeted account is shown, not dropped: %+v", software)
	}
	if len(missing) != 1 || missing[0] != "2026-07" || len(unbudgeted) != 1 || unbudgeted[0] != "2026-06" || sales.YTD.Actual != "19500.00" || sales.YTD.Budget != "19000.00" {
		t.Errorf("a fiscal month without books or without a budget column is named and left out of both sides: %v %v %+v", missing, unbudgeted, sales.YTD)
	}
	if legal := byName["Legal"]; !legal.Unbudgeted || *legal.Prior != "90.00" || legal.Month.Actual != "0.00" {
		t.Errorf("an account active only last month is shown with its prior month: %+v", legal)
	}
	// Income 11,000 - 100 = 10,900 against 9,900; expenses 1,600 + 50 + 400 = 2,050 against 1,500: net 8,850 against 8,400.
	if n := totals["net_month"]; n.Actual != "8850.00" || n.Budget != "8400.00" || n.Variance != "450.00" || !n.Favorable || n.Material {
		t.Errorf("net income month: %+v", n)
	}
	if e := totals["expense_ytd"]; e.Actual != "3990.00" || e.Budget != "3300.00" {
		t.Errorf("expenses year to date include travel's and legal's August actuals and travel's budget: %+v", e)
	}
}

func TestBudgetImportRemapApproveAndCompare(t *testing.T) {
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
	entity := "Budget " + rand.Text()
	closes := map[string]string{}
	defer func() {
		db.Exec(`DELETE FROM budget_versions WHERE client_entity=$1`, entity)
		for _, id := range closes {
			db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
		}
		db.Exec(`DELETE FROM quickbooks_connections WHERE client_entity=$1`, entity)
		for _, id := range closes {
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
	}()
	var connection string
	row := func(id, name, a string) string {
		return `{"type":"Data","ColData":[{"id":"` + id + `","value":"` + name + `"},{"value":"` + a + `"}]}`
	}
	accounts := `[{"Id":"1","Name":"Checking","Classification":"Asset","AccountType":"Bank"},{"Id":"4","Name":"Sales","Classification":"Revenue","AccountType":"Income"},{"Id":"5","Name":"Rent","Classification":"Expense","AccountType":"Expense"},{"Id":"6","Name":"Office supplies","Classification":"Expense","AccountType":"Expense"}]`
	for _, month := range [][3]string{{"2026-08", "8500.00", "1500.00"}, {"2026-09", "11000.00", "1600.00"}} {
		var id, evidence string
		must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,$2) RETURNING id::text`, entity, month[0]).Scan(&id))
		closes[month[0]] = id
		if connection == "" {
			must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123','USD',NULL) RETURNING id::text`, id, entity, rand.Text()).Scan(&connection))
		}
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, id, rand.Text()).Scan(&evidence))
		tb := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[]}}`
		pl := `{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[` + row("4", "Sales", month[1]) + `,` + row("5", "Rent", month[2]) + `]}}`
		_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, id, connection, rand.Text(), `{"period":"`+month[0]+`","source_id":"`+evidence+`","accounts":`+accounts+`,"reports":{"TrialBalance":`+tb+`,"ProfitAndLoss":`+pl+`,"GeneralLedger":{}}}`)
		must(err)
	}
	token := strings.Repeat("b", 32)
	handler := apiWithStore(db, token)
	call := func(method string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/closes/"+closes["2026-09"]+"/budget", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type report struct {
		State    string
		Versions []budgetVersion
		Rows     []budgetRow
		Totals   map[string]budgetFigure
		Missing  []string `json:"ytd_missing"`
	}
	var got report
	must(json.Unmarshal(call("GET", nil, 200), &got))
	if got.State != "no_budget" || len(got.Versions) != 0 {
		t.Fatalf("without an approved budget the comparison says so: %+v", got)
	}
	var saved struct {
		ID       string
		Problems []string
	}
	call("POST", map[string]any{"action": "import", "name": "FY2026", "csv": "Account,2026-08\nSales,9000"}, 200)
	must(json.Unmarshal(call("POST", map[string]any{"action": "import", "name": "FY2026", "csv": "Account,2026-08,2026-09\nSales,9000,10000\nRent,1500,1500\nOffice,100,100"}, 200), &saved))
	if len(saved.Problems) != 1 || !strings.Contains(saved.Problems[0], "Office") {
		t.Fatalf("Office matches no account name: %+v", saved)
	}
	call("POST", map[string]any{"action": "approve", "id": saved.ID}, 409)
	call("POST", map[string]any{"action": "remap", "id": saved.ID, "mapping": map[string]string{"2": "1"}}, 422)
	var remapped struct{ ID string }
	must(json.Unmarshal(call("POST", map[string]any{"action": "remap", "id": saved.ID, "mapping": map[string]string{"2": "6"}}, 200), &remapped))
	// The September close's comparison ignores an approved budget without September, and uses the remapped baseline.
	var august struct{ ID string }
	must(json.Unmarshal(call("POST", map[string]any{"action": "import", "name": "August only", "csv": "Account,2026-08\nSales,1"}, 200), &august))
	call("POST", map[string]any{"action": "approve", "id": august.ID}, 200)
	call("POST", map[string]any{"action": "approve", "id": remapped.ID}, 200)
	call("POST", map[string]any{"action": "approve", "id": remapped.ID}, 409)
	got = report{}
	body := call("GET", nil, 200)
	must(json.Unmarshal(body, &got))
	if !bytes.Contains(body, []byte(`"ytd_unbudgeted":[]`)) {
		t.Fatalf("an empty list is sent as a list, never null: %s", body)
	}
	rows := map[string]budgetRow{}
	for _, r := range got.Rows {
		rows[r.Name] = r
	}
	if got.State != "ready" || rows["Sales"].Month.Variance != "1000.00" || *rows["Sales"].Prior != "8500.00" || rows["Rent"].YTD.Actual != "3100.00" || rows["Office supplies"].Month.Budget != "100.00" || rows["Office supplies"].Month.Actual != "0.00" {
		t.Fatalf("the remapped, approved budget against each month's books: %+v", got.Rows)
	}
	if n := got.Totals["net_month"]; n.Actual != "9400.00" || n.Budget != "8400.00" || len(got.Missing) != 7 {
		t.Fatalf("net income and the fiscal months without books (January to July): %+v %v", n, got.Missing)
	}
}
