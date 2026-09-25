package quickbooks

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFindExceptionsRaisesQuestionsWithoutConcluding(t *testing.T) {
	row := func(kind, id, date, amount, record string) TransactionRow {
		return TransactionRow{Kind: kind, ProviderID: id, Date: date, Amount: amount, Record: []byte(record)}
	}
	software := `{"EntityRef":{"value":"7","name":"CloudSuite"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"60","name":"Software"}}}]}`
	rows := []TransactionRow{
		row("Purchase", "101", "2026-09-02", "49.00", software),
		row("Purchase", "102", "2026-09-04", "49.00", software),
		row("Purchase", "103", "2026-09-30", "49.00", software),
		row("Purchase", "104", "2026-09-03", "49.00", `{"EntityRef":{"value":"8","name":"Other vendor"},"Line":[]}`),
		{Kind: "Purchase", ProviderID: "105", Date: "2026-09-03", Currency: "EUR", Amount: "49.00", Record: []byte(software)},
		{Kind: "Bill", ProviderID: "201", Date: "2026-09-10", Currency: "EUR", Amount: "310.00", Record: []byte(`{"VendorRef":{"value":"9","name":"Printer Co"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"99","name":"Uncategorized Expense"}}}]}`)},
		row("Purchase", "501", "2026-09-20", "75.00", `{"EntityRef":{"value":"11","name":"Courier"},"Line":[]}`),
		row("Purchase", "502", "2026-09-23", "75.00", `{"EntityRef":{"value":"11","name":"Courier"},"Line":[]}`),
		row("Purchase", "503", "2026-09-26", "75.00", `{"EntityRef":{"value":"11","name":"Courier"},"Line":[]}`),
		row("Purchase", "301", "2026-09-12", "18.50", `{"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}}]}`),
		row("JournalEntry", "401", "2026-09-30", "1000.00", `{"Line":[{"JournalEntryLineDetail":{"AccountRef":{"value":"12","name":"Prepaid expense"}}}]}`),
		row("Purchase", "601", "2026-09-05", "30.00", `{"EntityRef":{"value":"12","name":"Staples","type":"Vendor"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}}]}`),
		row("Purchase", "602", "2026-09-20", "45.00", `{"EntityRef":{"value":"12","name":"Staples","type":"Vendor"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}}]}`),
		row("Purchase", "603", "2026-09-27", "18.00", `{"EntityRef":{"value":"12","name":"Staples","type":"Vendor"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}}]}`),
		row("Bill", "604", "2026-09-28", "22.00", `{"VendorRef":{"value":"12","name":"Staples"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}}]}`),
		row("Purchase", "605", "2026-09-29", "15.00", `{"EntityRef":{"value":"12","name":"Staples customer","type":"Customer"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}}]}`),
		row("Purchase", "801", "2026-09-07", "11.00", `{"EntityRef":{"value":"15","name":"Twin","type":"Vendor"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"80","name":"Supplies"}}}]}`),
		row("Purchase", "802", "2026-09-08", "12.00", `{"EntityRef":{"value":"15","name":"Twin","type":"Vendor"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"81","name":"Supplies"}}}]}`),
		row("Purchase", "701", "2026-09-06", "80.00", `{"EntityRef":{"value":"13","name":"Split shop"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}},{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}}]}`),
		row("Purchase", "702", "2026-09-26", "25.00", `{"EntityRef":{"value":"13","name":"Split shop"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}},{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}}]}`),
		row("Bill", "202", "2026-09-11", "99.00", `{"VendorRef":{"value":"9","name":"Printer Co"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"63","name":"Printing"}}}]}`),
	}
	found := map[string][]Exception{}
	for _, item := range FindExceptions(rows) {
		found[item.Type] = append(found[item.Type], item)
	}
	if len(found["possible_duplicate"]) != 2 || len(found["possible_duplicate"][0].ProviderIDs) != 2 || found["possible_duplicate"][0].ProviderIDs[1] != "102" {
		t.Fatalf("only the nearby same-vendor pair is a possible duplicate: %+v", found["possible_duplicate"])
	}
	if courier := found["possible_duplicate"][1]; len(courier.ProviderIDs) != 2 || courier.ProviderIDs[1] != "502" {
		t.Fatalf("a cluster must fit within three days of its first payment: %+v", courier)
	}
	if len(found["uncategorized"]) != 1 || found["uncategorized"][0].Account != "Uncategorized Expense" || !strings.Contains(found["uncategorized"][0].Question, "310.00 EUR") {
		t.Fatalf("holding-account posting must become a question: %+v", found["uncategorized"])
	}
	if coded := found["inconsistent_coding"]; len(coded) != 2 || coded[0].Counterparty != "Staples" || coded[0].Account != "Meals, Office supplies" || len(coded[0].ProviderIDs) != 4 || coded[1].Counterparty != "Twin" {
		t.Fatalf("Staples (purchases and a bill, not the customer with the same ID) and Twin (two accounts named alike) are coding questions; a split purchase is not: %+v", coded)
	}
	if len(found["no_payee"]) != 1 || found["no_payee"][0].ProviderIDs[0] != "301" {
		t.Fatalf("only the payee-less purchase needs a payee: %+v", found["no_payee"])
	}
}

func TestOpenItemExceptionsAskAboutOldInvoicesAndBills(t *testing.T) {
	item := func(id, due, balance, name string) json.RawMessage {
		return json.RawMessage(`{"Id":"` + id + `","DocNumber":"N` + id + `","DueDate":"` + due + `","Balance":` + balance + `,"CustomerRef":{"value":"1","name":"` + name + `"},"VendorRef":{"value":"2","name":"` + name + `"}}`)
	}
	monthEnd := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	found := OpenItemExceptions(map[string][]json.RawMessage{
		// 75 and 120 days past due, 30 days (not yet), no due date, and a zero balance.
		"Invoice": {item("1", "2026-07-17", "1200", "Acme"), item("2", "2026-06-02", "300.5", "Birch"), item("3", "2026-08-31", "90", "Cedar"), json.RawMessage(`{"Id":"4","Balance":10}`), item("5", "2026-01-01", "0", "Dune")},
		// 45 days past due, and 20 days.
		"Bill": {item("6", "2026-08-16", "450", "Paper Co"), item("7", "2026-09-10", "80", "Ink Co")},
	}, monthEnd)
	got := []string{}
	for _, e := range found {
		got = append(got, e.Type+":"+e.ProviderIDs[0]+":"+e.Amount)
	}
	if strings.Join(got, ",") != "past_due_receivable:1:1200.00,past_due_receivable:2:300.50,past_due_payable:6:450.00" {
		t.Fatalf("only invoices over 60 days and bills over 30 days past due at month-end: %v", got)
	}
	if strings.Contains(found[0].NextStep, "write-off") || !strings.Contains(found[1].NextStep, "write-off") || !strings.Contains(found[0].Question, "75 days past due") {
		t.Fatalf("the policy note starts past 90 days: %+v", found[:2])
	}
}

func TestUnappliedPaymentsAndOldCreditsAreAskedAbout(t *testing.T) {
	monthEnd := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	found := OpenItemExceptions(map[string][]json.RawMessage{
		"Payment":      {json.RawMessage(`{"Id":"11","TxnDate":"2026-09-28","UnappliedAmt":500,"CustomerRef":{"value":"1","name":"Acme"}}`)},
		"CreditMemo":   {json.RawMessage(`{"Id":"12","DocNumber":"CM-4","TxnDate":"2026-08-16","RemainingCredit":120,"CustomerRef":{"value":"1","name":"Acme"}}`)},
		"VendorCredit": {json.RawMessage(`{"Id":"13","TxnDate":"2026-09-20","Balance":80,"VendorRef":{"value":"2","name":"Ink Co"}}`)},
	}, monthEnd)
	got := []string{}
	for _, e := range found {
		got = append(got, e.Type+":"+e.ProviderIDs[0]+":"+e.Amount)
	}
	if strings.Join(got, ",") != "unapplied_payment:11:500.00,open_customer_credit:12:120.00" {
		t.Fatalf("an unapplied payment of any age and a credit unused for over 30 days are asked about: %v", got)
	}
	if !strings.Contains(found[0].Question, "advance or deposit") || !strings.Contains(found[1].Question, "applied to an invoice or refunded") {
		t.Fatalf("questions ask what the money settles: %+v", found)
	}
}

func TestScheduleCandidatesAreNetAdditions(t *testing.T) {
	accounts := json.RawMessage(`[{"Id":"1","Name":"Prepaid insurance","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses"},{"Id":"2","Name":"Prepaid software","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses"},
		{"Id":"3","Name":"Office equipment","AccountType":"Fixed Asset","AccountSubType":"MachineryAndEquipment"},{"Id":"4","Name":"Accumulated depreciation","AccountType":"Fixed Asset","AccountSubType":"AccumulatedDepreciation"},
		{"Id":"5","Name":"Deferred revenue","AccountType":"Other Current Liability","AccountSubType":"DeferredRevenue"},{"Id":"6","Name":"Insurance expense","AccountType":"Expense"},{"Id":"7","Name":"Checking","AccountType":"Bank"},{"Id":"8","Name":"Sales","AccountType":"Income"}]`)
	expense := func(account, name, amount string) string {
		return `{"Amount":` + amount + `,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"` + account + `","name":"` + name + `"}}}`
	}
	journal := func(side, account, amount string) string {
		return `{"Amount":` + amount + `,"JournalEntryLineDetail":{"PostingType":"` + side + `","AccountRef":{"value":"` + account + `"}}}`
	}
	row := func(kind, id, head string, lines ...string) TransactionRow {
		return TransactionRow{Kind: kind, ProviderID: id, Date: "2026-09-03", Currency: "USD", Record: json.RawMessage(`{` + head + `"Line":[` + strings.Join(lines, ",") + `]}`)}
	}
	rows := []TransactionRow{
		row("Bill", "101", `"DocNumber":"INV-88","VendorRef":{"value":"9","name":"Allianz"},`, expense("1", "Prepaid insurance", "12000"), expense("6", "Insurance expense", "50")),
		row("Purchase", "102", `"EntityRef":{"value":"4","name":"Dell"},`, expense("3", "Office equipment", "2400")),
		row("JournalEntry", "103", ``, journal("Debit", "2", "300"), journal("Credit", "1", "300")),
		row("JournalEntry", "104", ``, journal("Debit", "6", "1000"), journal("Credit", "1", "1000")),
		row("JournalEntry", "105", ``, journal("Debit", "7", "6000"), journal("Credit", "5", "6000")),
		row("JournalEntry", "106", ``, journal("Debit", "6", "100"), journal("Credit", "4", "100")),
		row("Deposit", "107", ``, `{"Amount":500,"DepositLineDetail":{"AccountRef":{"value":"5"}}}`),
		row("JournalEntry", "108", ``, journal("Debit", "5", "400"), journal("Credit", "8", "400")),
		row("Bill", "109", ``, expense("99", "New prepaid", "75")),
		row("Purchase", "110", `"Credit":true,`, expense("1", "Prepaid insurance", "200")),
		row("JournalEntry", "111", ``, journal("Debit", "4", "200"), journal("Credit", "8", "200")),
		row("Bill", "112", `"DocNumber":"INV-92",`, expense("1", "Prepaid insurance", "100"), expense("2", "Prepaid software", "200")),
		row("Bill", "113", ``, `{"Amount":80,"ItemBasedExpenseLineDetail":{"ItemRef":{"value":"5","name":"Toner"}}}`),
		row("JournalEntry", "114", ``, journal("Debit", "2", "500"), journal("Credit", "1", "200"), journal("Credit", "7", "300")),
		row("Bill", "115", ``, `{"ItemBasedExpenseLineDetail":{"ItemRef":{"value":"6","name":"Chairs"}}}`),
	}
	found, unknown, items, err := ScheduleCandidates(rows, accounts)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, f := range found {
		got = append(got, f.Type+" "+f.ProviderIDs[0]+" "+f.Amount+" "+f.Account+" "+f.Reference)
	}
	want := "unscheduled_prepaid 101 12000.00 Prepaid insurance INV-88|unscheduled_fixed_asset 102 2400.00 Office equipment |unscheduled_deferred_revenue 105 6000.00 Deferred revenue |unscheduled_deferred_revenue 107 500.00 Deferred revenue |unscheduled_prepaid 112 100.00 Prepaid insurance INV-92|unscheduled_prepaid 112 200.00 Prepaid software INV-92|unscheduled_prepaid 114 500.00 Prepaid software "
	if strings.Join(got, "|") != want {
		t.Fatalf("got %q\nwant %q", strings.Join(got, "|"), want)
	}
	if len(unknown) != 1 || unknown[0] != "New prepaid" || items != 2 {
		t.Fatalf("accounts missing from the directory and item lines are reported: %v %d", unknown, items)
	}
	if !strings.Contains(found[0].Question, "What period does the $12000.00 paid to Allianz on 2026-09-03 cover?") {
		t.Fatalf("the question asks for the service period: %s", found[0].Question)
	}
}
