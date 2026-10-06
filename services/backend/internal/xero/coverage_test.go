package xero

import (
	"encoding/json"
	"strings"
	"testing"
)

// Each Xero account is proposed the evidence its type needs, as a QuickBooks account of the same kind is, and an account
// only the Trial Balance reports is flagged; a capture without its Trial Balance cannot be read.
func TestCoverageProposesEachXeroAccountsNeed(t *testing.T) {
	payload := `{"accounts":{"Accounts":[{"AccountID":"a-bank","Name":"Business Bank Account","Type":"BANK","BankAccountType":"BANK"},
	 {"AccountID":"a-card","Name":"Company Card","Type":"BANK","BankAccountType":"CREDITCARD"},{"AccountID":"a-pre","Name":"Prepayments","Type":"PREPAYMENT"},
	 {"AccountID":"a-acc","Name":"Accruals","Type":"CURRLIAB"},{"AccountID":"a-gst","Name":"GST","Type":"CURRLIAB"},{"AccountID":"a-rent","Name":"Rent","Type":"EXPENSE","Status":"ARCHIVED"}]},
	 "trial_balance":{"Reports":[{"Rows":[{"RowType":"Header","Cells":[{"Value":"Account"},{"Value":"Debit"}]},{"RowType":"Section","Rows":[
	  {"RowType":"Row","Cells":[{"Value":"Business Bank Account","Attributes":[{"Value":"a-bank","Id":"account"}]},{"Value":"100.00"}]},
	  {"RowType":"Row","Cells":[{"Value":"Suspense","Attributes":[{"Value":"a-sus","Id":"account"}]},{"Value":"5.00"}]},
	  {"RowType":"SummaryRow","Cells":[{"Value":"Total"}]}]}]}]}}`
	items, err := Coverage(json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, item := range items {
		got = append(got, item.AccountID+":"+item.Kind+":"+strings.Join(item.ReportLocations, ","))
	}
	if strings.Join(got, " ") != "a-bank:reconciliation:trial_balance.Reports[0].Rows[1].Rows[0] a-card:reconciliation: a-pre:prepaid: a-acc:accrual: a-gst:accountant_review: a-rent:accountant_review: a-sus:accountant_review:trial_balance.Reports[0].Rows[1].Rows[1]" {
		t.Fatalf("kinds and report rows: %s", strings.Join(got, " "))
	}
	if items[0].Location != "accounts.Accounts[0]" || !strings.HasPrefix(items[0].Question, "Provide this month's complete independent statement") || items[6].Name != "Suspense" || !strings.Contains(items[6].Reason, "missing from the account capture") {
		t.Fatalf("the QuickBooks wording, and the account only the Trial Balance reports: %+v %+v", items[0], items[6])
	}
	if _, err = Coverage(json.RawMessage(`{"accounts":{"Accounts":[]}}`)); err == nil {
		t.Fatal("a capture without its Trial Balance is unreadable")
	}
}
