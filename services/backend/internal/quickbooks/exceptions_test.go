package quickbooks

import "testing"

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
		row("Bill", "201", "2026-09-10", "310.00", `{"VendorRef":{"value":"9","name":"Printer Co"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"99","name":"Uncategorized Expense"}}}]}`),
		row("Purchase", "301", "2026-09-12", "18.50", `{"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"61","name":"Meals"}}}]}`),
		row("JournalEntry", "401", "2026-09-30", "1000.00", `{"Line":[{"JournalEntryLineDetail":{"AccountRef":{"value":"12","name":"Prepaid expense"}}}]}`),
	}
	found := map[string][]Exception{}
	for _, item := range FindExceptions(rows) {
		found[item.Type] = append(found[item.Type], item)
	}
	if len(found["possible_duplicate"]) != 1 || len(found["possible_duplicate"][0].ProviderIDs) != 2 || found["possible_duplicate"][0].ProviderIDs[1] != "102" {
		t.Fatalf("only the nearby same-vendor pair is a possible duplicate: %+v", found["possible_duplicate"])
	}
	if len(found["uncategorized"]) != 1 || found["uncategorized"][0].Account != "Uncategorized Expense" || found["uncategorized"][0].Question == "" {
		t.Fatalf("holding-account posting must become a question: %+v", found["uncategorized"])
	}
	if len(found["no_payee"]) != 1 || found["no_payee"][0].ProviderIDs[0] != "301" {
		t.Fatalf("only the payee-less purchase needs a payee: %+v", found["no_payee"])
	}
}
