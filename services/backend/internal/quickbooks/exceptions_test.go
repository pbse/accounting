package quickbooks

import (
	"strings"
	"testing"
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
		row("Purchase", "601", "2026-09-05", "30.00", `{"EntityRef":{"value":"12","name":"Staples"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}}]}`),
		row("Purchase", "602", "2026-09-20", "45.00", `{"EntityRef":{"value":"12","name":"Staples"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}}]}`),
		row("Purchase", "603", "2026-09-27", "18.00", `{"EntityRef":{"value":"12","name":"Staples"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"62","name":"Office supplies"}}}]}`),
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
	if coded := found["inconsistent_coding"]; len(coded) != 1 || coded[0].Counterparty != "Staples" || coded[0].Account != "Meals, Office supplies" || len(coded[0].ProviderIDs) != 3 {
		t.Fatalf("only Staples, coded differently across two purchases, is a coding question (not one split purchase, not a holding account): %+v", coded)
	}
	if len(found["no_payee"]) != 1 || found["no_payee"][0].ProviderIDs[0] != "301" {
		t.Fatalf("only the payee-less purchase needs a payee: %+v", found["no_payee"])
	}
}
