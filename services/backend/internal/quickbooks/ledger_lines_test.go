package quickbooks

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestABankAccountsMonthComesFromTheCapturedLedgerOnlyWhenItTies(t *testing.T) {
	accounts := json.RawMessage(`[{"Id":"3","Name":"Checking","AccountType":"Bank","Classification":"Asset","CurrencyRef":{"value":"USD"}},{"Id":"9","Name":"Card","AccountType":"Credit Card","Classification":"Liability","CurrencyRef":{"value":"USD"}}]`)
	tb := func(checking string) json.RawMessage {
		return json.RawMessage(`{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"3","value":"Checking"},{"value":"` + checking + `"},{"value":""}]},{"type":"Data","ColData":[{"id":"9","value":"Card"},{"value":""},{"value":"50.00"}]}]}}`)
	}
	gl := func(rows string) json.RawMessage {
		return json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[
 {"type":"Section","Header":{"ColData":[{"id":"3","value":"Checking"}]},"Rows":{"Row":[` + rows + `]}}]}}`)
	}
	row := func(date, kind, amount, balance string) string {
		return `{"type":"Data","ColData":[{"value":"` + date + `"},{"value":"` + kind + `"},{"value":"` + amount + `"},{"value":"` + balance + `"}]}`
	}
	// The beginning balance is found whichever column says so; a memo line with no amount moves nothing.
	month := strings.Join([]string{row("", "Beginning Balance", "", "1,000.00"), row("2026-09-03", "Deposit", "500.00", "1,500.00"), row("2026-09-05", "Memo", "", "1,500.00"), row("2026-09-10", "Check", "-200.00", "1,300.00")}, ",")
	csv, opening, closing, err := LedgerLines(accounts, gl(month), tb("1,300.00"), "3", "2026-09")
	if err != nil || csv != "id,date,amount\nGL1,2026-09-03,500.00\nGL2,2026-09-10,-200.00\n" || opening != "1000.00" || closing != "1300.00" {
		t.Fatalf("the month's rows, opening and closing come from the ledger: %q %s %s %v", csv, opening, closing, err)
	}
	for _, refused := range []struct {
		why, rows, tb, account string
	}{
		{"the rows do not add up to the Trial Balance", month, "1400.00", "3"},
		{"a row is outside the month", strings.Replace(month, "2026-09-10", "2026-10-01", 1), "1300.00", "3"},
		{"there is no beginning balance for a balance carried in", row("2026-09-03", "Deposit", "500.00", "1,500.00") + "," + row("2026-09-10", "Check", "-200.00", "1,300.00"), "1300.00", "3"},
		{"the running balance does not end at the Trial Balance", strings.Replace(month, `"1,300.00"}]}`, `"1,250.00"}]}`, 1), "1300.00", "3"},
		{"an amount is unreadable", strings.Replace(month, `"-200.00"`, `"(200.00) see note"`, 1), "1300.00", "3"},
		{"a row of the account is incomplete", month + `,{"type":"Data","ColData":[{"value":"2026-09-11"}]}`, "1300.00", "3"},
	} {
		if _, _, _, err := LedgerLines(accounts, gl(refused.rows), tb(refused.tb), refused.account, "2026-09"); err == nil || !strings.Contains(err.Error(), "Export the ledger by hand") {
			t.Fatalf("refused when %s: %v", refused.why, err)
		}
	}
	// A credit card ties too, but its signs are not a bank's: it is never read this way.
	card := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[
 {"type":"Section","Header":{"ColData":[{"id":"9","value":"Card"}]},"Rows":{"Row":[` + row("Beginning Balance", "", "", "-50.00") + `]}}]}}`)
	if _, _, _, err := LedgerLines(accounts, card, tb("1300.00"), "9", "2026-09"); err == nil {
		t.Fatal("a card account is not read as a bank ledger")
	}
	// A euro bank account in dollar books is not read as the books' cash.
	euro := json.RawMessage(strings.Replace(string(accounts), `"Classification":"Asset","CurrencyRef":{"value":"USD"}`, `"Classification":"Asset","CurrencyRef":{"value":"EUR"}`, 1))
	if _, _, _, err := LedgerLines(euro, gl(month), tb("1,300.00"), "3", "2026-09"); err == nil {
		t.Fatal("a foreign-currency bank account is refused")
	}
}
