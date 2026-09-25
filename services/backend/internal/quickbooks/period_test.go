package quickbooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSelectLedgerKeepsExactAccountAndPages(t *testing.T) {
	accounts := json.RawMessage(`[{
"Id":"7"},{"Id":"8"}]`)
	rows := []string{`{"type":"Data","ColData":[{"id":"8","value":"Other"},{"value":"999.00"}]}`}
	for n := 0; n < 25; n++ {
		rows = append(rows, fmt.Sprintf(`{"type":"Data","ColData":[{"id":"7","value":"Prepaid"},{"value":"9007199254740993.%02d"}]}`, n))
	}
	report := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Account"}]},"Rows":{"Row":[` + strings.Join(rows, ",") + `]}}`)
	first, err := SelectLedger(accounts, report, "7", 0)
	if err != nil || first.Total != 25 || len(first.Rows) != 20 || strings.Contains(string(first.Rows[0].Row), "999.00") || !strings.Contains(string(first.Rows[0].Row), "9007199254740993.00") {
		t.Fatalf("first page: %+v %v", first, err)
	}
	last, err := SelectLedger(accounts, report, "7", 20)
	if err != nil || len(last.Rows) != 5 || last.Rows[0].Location != "reports.GeneralLedger.Rows.Row[21]" {
		t.Fatalf("last page: %+v %v", last, err)
	}
	if _, err := SelectLedger(accounts, report, "9", 0); err == nil {
		t.Fatal("unknown account was accepted")
	}
	unsafe := json.RawMessage(strings.Replace(string(report), `"ColTitle":"Account"`, `"ColTitle":"Date"`, 1))
	page, err := SelectLedger(accounts, unsafe, "7", 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("transaction ID was mistaken for an account ID: %+v %v", page, err)
	}
	grouped := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Date"}]},"Rows":{"Row":[{"Header":{"ColData":[{"id":"7","value":"Prepaid"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-01"}]},{"Header":{"ColData":[{"id":"8","value":"Other"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-02"}]}]}}]}}]}}`)
	page, err = SelectLedger(accounts, grouped, "7", 0)
	if err != nil || page.Total != 2 || strings.Contains(string(page.Rows[1].Row), "2026-09-02") {
		t.Fatalf("nested foreign account was selected: %+v %v", page, err)
	}
}

func TestSearchLedgerKeepsAccountAndOriginalLocations(t *testing.T) {
	accounts := json.RawMessage(`[{"Id":"7"},{"Id":"8"}]`)
	report := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Account"}]},"Rows":{"Row":[{"ColData":[{"id":"8","value":"INV-42"}]},{"ColData":[{"id":"7","value":"other"}]},{"ColData":[{"id":"7","value":"INV-42"}]}]}}`)
	p, err := SearchLedger(accounts, report, "7", "inv-42", 0)
	if err != nil || p.Total != 1 || len(p.Rows) != 1 || p.Rows[0].Location != "reports.GeneralLedger.Rows.Row[2]" {
		t.Fatalf("unsafe search result: %+v %v", p, err)
	}
	if _, err = SearchLedger(accounts, report, "7", "x", 0); err == nil {
		t.Fatal("unbounded query accepted")
	}
	p, err = SearchLedger(accounts, report, "7", "value", 0)
	if err != nil || p.Total != 0 {
		t.Fatalf("JSON field names must not become search matches: %+v %v", p, err)
	}
}

func TestTrialBalanceAccountRequiresUniquePrepaidRow(t *testing.T) {
	accounts := json.RawMessage(`[{"Id":"7","Name":"Prepaid insurance","AccountSubType":"PrepaidExpenses"},{"Id":"8","Name":"Cash","AccountSubType":"Checking"}]`)
	report := json.RawMessage(`{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"8","value":"Cash"},{"value":"100.00"}]},{"Rows":{"Row":[{"type":"Data","ColData":[{"id":"7","value":"Prepaid insurance"},{"value":"40.00"}]}]}}]}}`)
	name, location, cells, err := TrialBalanceAccount(accounts, report, "7", "USD", "PrepaidExpenses")
	if err != nil || name != "Prepaid insurance" || location != "reports.TrialBalance.Rows.Row[1].Rows.Row[0]" || !strings.Contains(string(cells), "40.00") {
		t.Fatalf("wrong account selection: %s %s %s %v", name, location, cells, err)
	}
	if _, _, _, err := TrialBalanceAccount(accounts, report, "7", "CAD", "PrepaidExpenses"); err == nil {
		t.Fatal("trial balance in another currency accepted")
	}
	if _, _, _, err := TrialBalanceAccount(accounts, report, "8", "USD", "PrepaidExpenses"); err == nil {
		t.Fatal("non-prepaid account accepted")
	}
	if _, _, _, err := TrialBalanceAccount(accounts, report, "9", "USD", "PrepaidExpenses"); err == nil {
		t.Fatal("missing account accepted as zero")
	}
	duplicate := json.RawMessage(strings.Replace(string(report), `{"type":"Data","ColData":[{"id":"8","value":"Cash"},{"value":"100.00"}]`, `{"type":"Data","ColData":[{"id":"7","value":"Prepaid insurance"},{"value":"40.00"}]`, 1))
	if _, _, _, err := TrialBalanceAccount(accounts, duplicate, "7", "USD", "PrepaidExpenses"); err == nil {
		t.Fatal("duplicate row accepted")
	}
}

func TestMonthlyActivityAndPayeesReadReportSections(t *testing.T) {
	pl := json.RawMessage(`{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[
		{"type":"Section","Header":{"ColData":[{"value":"Income"},{"value":""}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"10","value":"Sales"},{"value":"5,000.00"}]}]},"Summary":{"ColData":[{"value":"Total Income"},{"value":"5000.00"}]}},
		{"type":"Section","Header":{"ColData":[{"value":"Expenses"},{"value":""}]},"Rows":{"Row":[
			{"type":"Section","Header":{"ColData":[{"id":"19","value":"Office"},{"value":""}]},"ColData":[{"id":"19","value":"Office"},{"value":"2000.00"}],"Rows":{"Row":[{"type":"Data","ColData":[{"id":"20","value":"Utilities"},{"value":"2000.00"}]}]},"Summary":{"ColData":[{"value":"Total Office"},{"value":"2000.00"}]}}
		]}}]}}`)
	currency, activity, err := MonthlyActivity(pl)
	if err != nil || currency != "USD" || len(activity) != 2 || activity["10"].Amount.FloatString(2) != "5000.00" || activity["20"].Name != "Utilities" {
		t.Fatalf("account rows inside nested sections are read and summaries are not: %s %v %v", currency, activity, err)
	}
	if _, _, err := MonthlyActivity(json.RawMessage(strings.Replace(string(pl), `"id":"20"`, `"id":"10"`, 1))); err == nil {
		t.Fatal("an account listed twice is refused")
	}
	if _, _, err := MonthlyActivity(json.RawMessage(strings.Replace(string(pl), `{"value":"2000.00"}]}]}`, `{"value":"2000.00"},{"value":"1"}]}]}`, 1))); err == nil {
		t.Fatal("a row that does not match the columns is refused")
	}
	if _, _, err := MonthlyActivity(json.RawMessage(`{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"},{"ColTitle":"% of Income"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"10","value":"Sales"},{"value":"5000.00"},{"value":"100.00"}]}]}}`)); err == nil {
		t.Fatal("a Profit and Loss with more than one amount column is refused")
	}
	if _, _, err := MonthlyActivity(json.RawMessage(strings.Replace(string(pl), `{"ColTitle":"Total"}`, `{"ColTitle":"% of Income"}`, 1))); err == nil {
		t.Fatal("a Profit and Loss whose second column is not the total is refused")
	}
	if _, _, err := MonthlyActivity(json.RawMessage(strings.Replace(string(pl), `{"ColTitle":"Total"}`, `{"ColTitle":"Sep 2026","ColType":"Money"}`, 1))); err != nil {
		t.Fatal("a money column titled by its month is the total")
	}
	gl := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Name"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[
		{"type":"Section","Header":{"ColData":[{"id":"20","value":"Utilities"}]},"Rows":{"Row":[
			{"type":"Data","ColData":[{"value":"2026-09-03"},{"value":"Bill"},{"value":"Con Edison"},{"value":"1,200.00"},{"value":"1200.00"}]},
			{"type":"Data","ColData":[{"value":"2026-09-10"},{"value":"Bill"},{"value":"Con Edison"},{"value":"300.00"},{"value":"1500.00"}]},
			{"type":"Data","ColData":[{"value":"2026-09-12"},{"value":"Journal Entry"},{"value":""},{"value":"500.00"},{"value":"2000.00"}]},
			{"type":"Data","ColData":[{"value":"2026-09-13"},{"value":"Memo"},{"value":"Note only"},{"value":""},{"value":"2000.00"}]},
			{"type":"Section","ColData":[{"value":""},{"value":""},{"value":"Subtotal line"},{"value":"9.00"},{"value":""}]}
		]},"Summary":{"ColData":[{"value":"Total for Utilities"},{"value":""},{"value":""},{"value":"2000.00"}]}},
		{"type":"Section","Header":{"ColData":[{"id":"22","value":"Rent"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-01"},{"value":"Bill"},{"value":"Landlord"},{"value":"1500.00"},{"value":"1500.00"}]}]}}
	]}}`)
	payees, err := PayeeActivity(gl, []string{"20"})
	if err != nil || len(payees) != 1 || payees["20"].ByName["Con Edison"].FloatString(2) != "1500.00" || payees["20"].Unattributed.FloatString(2) != "500.00" || len(payees["20"].ByName) != 1 {
		t.Fatalf("a wanted account's transaction rows are summed by payee, blank names apart, other accounts ignored: %+v %v", payees, err)
	}
	if _, err := PayeeActivity(json.RawMessage(strings.Replace(string(gl), `"ColTitle":"Name"`, `"ColTitle":"Customer"`, 1)), []string{"20"}); err == nil {
		t.Fatal("a ledger without a Name column cannot attribute payees")
	}
}
