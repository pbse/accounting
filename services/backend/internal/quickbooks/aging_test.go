package quickbooks

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestAgingBucketsByDueDateAndKeepsCreditsApart(t *testing.T) {
	doc := func(v map[string]any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
	customer := func(id, name string) map[string]string { return map[string]string{"value": id, "name": name} }
	usd, eur := map[string]string{"value": "USD"}, map[string]string{"value": "EUR"}
	invoice := func(id, due, balance string, currency map[string]string) json.RawMessage {
		return doc(map[string]any{"Id": id, "DocNumber": "INV-" + id, "TxnDate": "2026-05-01", "DueDate": due, "Balance": json.Number(balance), "CustomerRef": customer("1", "Acme"), "CurrencyRef": currency})
	}
	open := map[string][]json.RawMessage{
		"Invoice": {
			invoice("1", "2026-10-15", "100.00", usd), invoice("2", "2026-09-15", "200.00", usd), invoice("3", "2026-08-15", "300.00", usd),
			invoice("4", "2026-07-15", "400.00", usd), invoice("5", "2026-05-15", "500.00", usd), invoice("6", "", "60.00", usd),
			invoice("7", "2026-09-01", "0", usd), invoice("8", "2026-09-01", "70.00", eur),
			doc(map[string]any{"Id": "9", "TxnDate": "2026-09-01", "DueDate": "2026-09-10", "Balance": "unreadable", "CustomerRef": customer("2", "Bistro"), "CurrencyRef": usd}),
		},
		"Payment":      {doc(map[string]any{"Id": "10", "TxnDate": "2026-09-20", "UnappliedAmt": json.Number("25.00"), "CustomerRef": customer("1", "Acme"), "CurrencyRef": usd})},
		"CreditMemo":   {doc(map[string]any{"Id": "11", "TxnDate": "2026-09-21", "RemainingCredit": json.Number("15.00"), "CustomerRef": customer("1", "Acme"), "CurrencyRef": usd})},
		"Bill":         {doc(map[string]any{"Id": "12", "TxnDate": "2026-08-01", "DueDate": "2026-08-20", "Balance": json.Number("80.00"), "VendorRef": customer("7", "Supplies"), "CurrencyRef": usd})},
		"VendorCredit": {doc(map[string]any{"Id": "13", "TxnDate": "2026-09-02", "Balance": json.Number("5.00"), "VendorRef": customer("7", "Supplies"), "CurrencyRef": usd})},
	}
	open["Invoice"] = append(open["Invoice"],
		doc(map[string]any{"Id": "14", "TxnDate": "2026-09-01", "DueDate": "2026-09-20", "Balance": json.Number("1.005"), "CustomerRef": customer("3", "Cafe")}),
		doc(map[string]any{"Id": "15", "TxnDate": "2026-09-01", "DueDate": "2026-09-20", "Balance": json.Number("1.005"), "CustomerRef": customer("3", "Cafe")}),
		doc(map[string]any{"Id": "16", "TxnDate": "2026-09-01", "DueDate": "2026-09-20", "Balance": json.Number("9.00"), "CustomerRef": customer("", "")}),
		doc(map[string]any{"Id": "17", "TxnDate": "2026-09-01", "DueDate": "2026-09-20", "Balance": json.Number("8.00"), "CustomerRef": customer("", "")}),
		json.RawMessage(`{"Id":"18","CustomerRef":"not an object"}`))
	receivables, payables, unreadable := Aging(open, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), "USD")
	if unreadable != 1 {
		t.Fatalf("an undecodable record is counted: %d", unreadable)
	}
	for _, p := range receivables {
		switch {
		case p.ID == "" && p.Open != "9.00" && p.Open != "8.00":
			t.Fatalf("documents without a customer stay apart: %+v", p)
		case p.ID == "3" && (p.Currency != "USD" || p.Buckets[1] != "2.02"):
			t.Fatalf("a missing currency takes home, and totals add the rounded lines: %+v", p)
		}
	}
	receivables = slices.DeleteFunc(receivables, func(p AgingParty) bool { return p.ID == "" || p.ID == "3" })
	if len(receivables) != 3 || len(payables) != 1 {
		t.Fatalf("customers and currencies group apart, vendors on their own side: %+v %+v", receivables, payables)
	}
	acme := receivables[1]
	if acme.ID != "1" || acme.Currency != "USD" || acme.Buckets != [5]string{"100.00", "200.00", "300.00", "400.00", "500.00"} || acme.Unaged != "60.00" || acme.Open != "1560.00" || acme.Credits != "40.00" || len(acme.Lines) != 8 {
		t.Fatalf("invoices by days past due, an undated one unaged, credits apart and a zero balance dropped: %+v", acme)
	}
	if euro := receivables[0]; euro.Currency != "EUR" || euro.Open != "70.00" {
		t.Fatalf("another currency is its own group: %+v", euro)
	}
	if bistro := receivables[2]; bistro.Name != "Bistro" || bistro.Unresolved != 1 || bistro.Open != "0.00" || bistro.Lines[0].Problem == "" {
		t.Fatalf("an unreadable balance is listed but left out of the totals: %+v", bistro)
	}
	if supplies := payables[0]; supplies.Buckets[2] != "80.00" || supplies.Credits != "5.00" {
		t.Fatalf("bills age by due date and supplier credits stay apart: %+v", supplies)
	}
}
