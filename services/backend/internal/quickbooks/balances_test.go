package quickbooks

import (
	"encoding/json"
	"fmt"
	"strings"
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
	single := func(value string) json.RawMessage {
		return json.RawMessage(`{"Header":{"Currency":"EUR"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Accounts Receivable"},{"value":""},{"value":"` + value + `"}]}]}}`)
	}
	if found, err := AbnormalBalances(accounts, single("150.00")); err != nil || len(found) != 1 || !strings.Contains(found[0].Question, "150.00 EUR") {
		t.Fatalf("the question uses the report currency: %+v %v", found, err)
	}
	if _, err := AbnormalBalances(accounts, single("n/a")); err == nil {
		t.Fatal("an unreadable balance must fail the check, not pass as normal")
	}
}

func TestHoldingAccountsWithABalanceNeedReconciliation(t *testing.T) {
	accounts := json.RawMessage(`[
		{"Id":"1","Name":"Undeposited Funds","AccountType":"Other Current Asset","AccountSubType":"UndepositedFunds"},
		{"Id":"2","Name":"Payroll clearing","AccountType":"Other Current Liability"},
		{"Id":"3","Name":"Ask My Accountant","AccountType":"Expense"},
		{"Id":"4","Name":"Checking","AccountType":"Bank"},
		{"Id":"5","Name":"Clearing Revenue","AccountType":"Income"},
		{"Id":"6","Name":"Stripe clearing","AccountType":"Other Current Asset"},
		{"Id":"7","Name":"Opening Balance Equity","AccountType":"Equity","AccountSubType":"OpeningBalanceEquity"},
		{"Id":"8","Name":"Owner's equity","AccountType":"Equity"},
		{"Id":"9","Name":"Opening balances - distributions","AccountType":"Equity","AccountSubType":"OpeningBalanceEquity"},
		{"Id":"10","Name":"Former Opening Balance Equity","AccountType":"Equity"}]`)
	tb := json.RawMessage(`{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[
		{"type":"Data","ColData":[{"id":"1","value":"Undeposited Funds"},{"value":"1200.00"},{"value":""}]},
		{"type":"Data","ColData":[{"id":"2","value":"Payroll clearing"},{"value":"0.00"},{"value":""}]},
		{"type":"Data","ColData":[{"id":"3","value":"Ask My Accountant"},{"value":"35.00"},{"value":""}]},
		{"type":"Data","ColData":[{"id":"4","value":"Checking"},{"value":"500.00"},{"value":""}]},
		{"type":"Data","ColData":[{"id":"5","value":"Clearing Revenue"},{"value":""},{"value":"100.00"}]},
		{"type":"Data","ColData":[{"id":"6","value":"Stripe clearing"},{"value":""},{"value":"15.00"}]},
		{"type":"Data","ColData":[{"id":"7","value":"Opening Balance Equity"},{"value":""},{"value":"4200.00"}]},
		{"type":"Data","ColData":[{"id":"8","value":"Owner's equity"},{"value":""},{"value":"9000.00"}]},
		{"type":"Data","ColData":[{"id":"9","value":"Opening balances - distributions"},{"value":"300.00"},{"value":""}]},
		{"type":"Data","ColData":[{"id":"10","value":"Former Opening Balance Equity"},{"value":""},{"value":"50.00"}]}]}}`)
	found, err := AbnormalBalances(accounts, tb)
	if err != nil || len(found) != 5 {
		t.Fatalf("Undeposited Funds, Ask My Accountant, the Stripe clearing credit and both opening-balance accounts are the only candidates: %+v %v", found, err)
	}
	got := map[string]string{}
	for _, item := range found {
		got[item.Account] = item.Type + " " + item.Side + " " + item.Amount
	}
	if got["Undeposited Funds"] != "holding_balance debit 1200.00" || got["Ask My Accountant"] != "holding_balance debit 35.00" || got["Stripe clearing"] != "holding_balance credit 15.00" || got["Opening Balance Equity"] != "opening_balance_equity credit 4200.00" || got["Opening balances - distributions"] != "opening_balance_equity debit 300.00" {
		t.Fatalf("each holding account is one reconciliation item: %v", got)
	}
	for _, item := range found {
		if item.Account == "Stripe clearing" && !strings.Contains(item.Finding, "opposite the account's normal side") {
			t.Fatalf("a clearing balance on the wrong side says so: %s", item.Finding)
		}
		if item.Account == "Undeposited Funds" && !strings.Contains(item.NextStep, "deposit in transit") {
			t.Fatalf("undeposited funds are grouped into deposits: %s", item.NextStep)
		}
	}
}
