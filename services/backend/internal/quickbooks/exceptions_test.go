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
