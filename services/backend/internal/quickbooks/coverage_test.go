package quickbooks

import (
	"encoding/json"
	"testing"
)

// A capture's coverage is stored with it and signed into close sign-offs, so its wording and shape stay exactly as they
// were: each kind's reason and question, and an account only the Trial Balance reports.
func TestCoverageKeepsEachKindsWording(t *testing.T) {
	accounts := `[{"Id":"1","Name":"Checking","AccountType":"Bank"},{"Id":"2","Name":"Card","AccountType":"Credit Card"},{"Id":"3","Name":"Prepaid Insurance","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses"},{"Id":"4","Name":"Accrued Wages","AccountType":"Other Current Liability"},{"Id":"5","Name":"Rent","AccountType":"Expense"}]`
	tb := `{"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Checking"}]},{"type":"Section","Rows":{"Row":[{"type":"Data","ColData":[{"id":"9","value":"Suspense"}]}]}}]}}`
	items, err := Coverage(json.RawMessage(accounts), map[string]json.RawMessage{"TrialBalance": json.RawMessage(tb)})
	raw, _ := json.Marshal(items)
	if err != nil || string(raw) != golden {
		t.Fatalf("coverage changed:\n%s\n%v", raw, err)
	}
}

const golden = `[{"account_id":"1","name":"Checking","kind":"reconciliation","reason":"Bank or credit-card account requires independent evidence before reconciliation can be proposed.","question":"Provide this month's complete independent statement and an account-specific ledger CSV (id,date,amount), verified opening/closing balances and page count. Report rows are not automatically converted to reconciliation transactions.","location":"accounts[0]","report_locations":["reports.TrialBalance.Rows.Row[0]"]},{"account_id":"2","name":"Card","kind":"reconciliation","reason":"Bank or credit-card account requires independent evidence before reconciliation can be proposed.","question":"Provide this month's complete independent statement and an account-specific ledger CSV (id,date,amount), verified opening/closing balances and page count. Report rows are not automatically converted to reconciliation transactions.","location":"accounts[1]","report_locations":null},{"account_id":"3","name":"Prepaid Insurance","kind":"prepaid","reason":"Prepaid account is a candidate for schedule review; the ledger does not prove invoice service terms.","question":"Provide invoices and existing schedules with service dates, currency, original booking, credits and prior adjustments. A client accountant must approve any treatment beyond the established methods.","location":"accounts[2]","report_locations":null},{"account_id":"4","name":"Accrued Wages","kind":"accrual","reason":"The account name suggests an accrued liability; its activity and invoices still need review.","question":"Provide incurred-service evidence, supported closing liability, current booked balance and any prior estimate or actual invoice. Confirm the client's approved method before preparing.","location":"accounts[3]","report_locations":null},{"account_id":"5","name":"Rent","kind":"accountant_review","reason":"No supported preparation selected for this account type.","question":"Review period activity and balances; define any required work or explicitly document exclusion from the close scope.","location":"accounts[4]","report_locations":null},{"account_id":"9","name":"Suspense","kind":"accountant_review","reason":"Trial balance references an account missing from the account capture.","question":"Resolve the inconsistent account inventory and recapture before preparing this account.","location":"reports.TrialBalance.Rows.Row[1].Rows.Row[0]","report_locations":["reports.TrialBalance.Rows.Row[1].Rows.Row[0]"]}]`
