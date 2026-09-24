package quickbooks

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestAbnormalBalancesReadTheTrialBalanceColumns(t *testing.T) {
	accounts := json.RawMessage(`[
		{"Id":"1","Name":"Accounts Receivable","AccountType":"Accounts Receivable","Classification":"Asset"},
		{"Id":"2","Name":"Business card","AccountType":"Credit Card"},
		{"Id":"3","Name":"Checking","AccountType":"Bank"},
		{"Id":"4","Name":"Accumulated depreciation","AccountType":"Fixed Asset","AccountSubType":"AccumulatedDepreciation"},
		{"Id":"5","Name":"Retained Earnings","AccountType":"Equity","AccountSubType":"RetainedEarnings"},
		{"Id":"6","Name":"Owner's Draw","AccountType":"Equity"},
		{"Id":"7","Name":"Office supplies","AccountType":"Expense"},
		{"Id":"8","Name":"Mystery","AccountType":"Unknown kind"}]`)
	row := func(id, name, debit, credit string, order [2]int) string {
		cells := [3]string{fmt.Sprintf(`{"id":%q,"value":%q}`, id, name), "", ""}
		cells[order[0]], cells[order[1]] = fmt.Sprintf(`{"value":%q}`, debit), fmt.Sprintf(`{"value":%q}`, credit)
		return fmt.Sprintf(`{"type":"Data","ColData":[%s,%s,%s]}`, cells[0], cells[1], cells[2])
	}
	for _, order := range [][2]int{{1, 2}, {2, 1}} {
		titles := [3]string{"", "", ""}
		titles[order[0]], titles[order[1]] = "Debit", "Credit"
		rows := []string{row("1", "Accounts Receivable", "", "150.00", order), row("2", "Business card", "20.00", "", order), row("3", "Checking", "", "12.00", order),
			row("4", "Accumulated depreciation", "", "500.00", order), row("5", "Retained Earnings", "800.00", "", order), row("6", "Owner's Draw", "300.00", "", order),
			row("7", "Office supplies", "40.00", "", order), row("8", "Mystery", "9.00", "", order)}
		tb := fmt.Sprintf(`{"Columns":{"Column":[{"ColTitle":%q},{"ColTitle":%q},{"ColTitle":%q}]},"Rows":{"Row":[{"type":"Section","Rows":{"Row":[%s,%s,%s,%s]}},%s,%s,%s,%s]}}`, titles[0], titles[1], titles[2], rows[0], rows[1], rows[2], rows[3], rows[4], rows[5], rows[6], rows[7])
		found, err := AbnormalBalances(accounts, json.RawMessage(tb))
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, item := range found {
			got[item.Account] = item.Side + " " + item.Amount
		}
		if len(found) != 3 || got["Accounts Receivable"] != "credit 150.00" || got["Business card"] != "debit 20.00" || got["Checking"] != "credit 12.00" {
			t.Fatalf("columns %v: only the receivable credit, card debit and overdrawn bank are candidates: %v", order, got)
		}
	}
}
