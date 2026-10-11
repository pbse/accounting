package preparation

import "testing"

func TestClearingActivityFindsBookedPayrollAndTheClearingDebits(t *testing.T) {
	ledger := &Ledger{Rows: []LedgerRow{
		{Kind: "Purchase", Date: "2026-09-15", Record: []byte(`{"Line":[{"Amount":5000.00,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40"}}}]}`)},
		{Kind: "JournalEntry", Date: "2026-09-30", Record: []byte(`{"Line":[{"Amount":5265.00,"JournalEntryLineDetail":{"PostingType":"Debit","AccountRef":{"value":"40"}}},{"Amount":5265.00,"JournalEntryLineDetail":{"PostingType":"Credit","AccountRef":{"value":"10"}}}]}`)},
		{Kind: "Purchase", Date: "2026-09-20", Record: []byte(`{"Credit":true,"Line":[{"Amount":100.00,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40"}}}]}`)},
		{Kind: "Purchase", Date: "2026-08-31", Record: []byte(`{"Line":[{"Amount":999.00,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40"}}}]}`)},
	}}
	ledger.Rows = append(ledger.Rows, LedgerRow{Kind: "Bill", Date: "2026-09-25", Record: []byte(`{"Line":[{"Amount":700.00,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40"}}}]}`)})
	booked, unnamed, debits, unreadable := clearingActivity(ledger, "2026-09", "40", map[string]bool{"60": true}, "Gusto")
	if booked != "" || unnamed != "" || debits != 1016500 || unreadable != "" {
		t.Fatalf("the month's cash debited to clearing, net of a credit purchase and without an unpaid bill, and nothing booked yet: %q %q %d %q", booked, unnamed, debits, unreadable)
	}
	ledger.Rows = append(ledger.Rows, LedgerRow{Kind: "JournalEntry", Date: "2026-09-30", Record: []byte(`{"DocNumber":"JE-4","Line":[{"Amount":8000.00,"JournalEntryLineDetail":{"PostingType":"Debit","AccountRef":{"value":"60"}}},{"Amount":8000.00,"JournalEntryLineDetail":{"PostingType":"Credit","AccountRef":{"value":"40"}}}]}`)})
	if booked, unnamed, _, _ = clearingActivity(ledger, "2026-09", "40", map[string]bool{"60": true}, "Gusto"); booked != "" || unnamed != "JE-4" {
		t.Fatalf("a journal moving wages out of clearing without naming the provider may be another payroll: %q %q", booked, unnamed)
	}
	ledger.Rows = append(ledger.Rows, LedgerRow{Kind: "JournalEntry", Date: "2026-09-30", Record: []byte(`{"DocNumber":"PR-9","PrivateNote":"Close adjustment 2026-09: payroll, Gusto summary","Line":[{"Amount":10000.00,"JournalEntryLineDetail":{"PostingType":"Debit","AccountRef":{"value":"60"}}},{"Amount":10000.00,"JournalEntryLineDetail":{"PostingType":"Credit","AccountRef":{"value":"40"}}}]}`)})
	if booked, _, _, _ = clearingActivity(ledger, "2026-09", "40", map[string]bool{"60": true}, "Gusto"); booked != "PR-9" {
		t.Fatalf("a journal naming the provider that moves wages out of clearing is payroll already booked: %q", booked)
	}
	ledger.Rows = append(ledger.Rows, LedgerRow{Kind: "JournalEntry", Date: "2026-09-30", Record: []byte(`{"Line":[{"Amount":"lots"}]}`)})
	if _, _, _, unreadable = clearingActivity(ledger, "2026-09", "40", nil, "Gusto"); unreadable != "JournalEntry" {
		t.Fatalf("an unreadable row makes the checks unavailable: %q", unreadable)
	}
}
