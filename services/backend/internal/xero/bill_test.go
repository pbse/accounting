package xero

import (
	"encoding/json"
	"testing"
)

func TestSameBillComparesEveryLineAcrossXerosFormats(t *testing.T) {
	read := func(raw string) Bill {
		t.Helper()
		b, err := ParseBill(json.RawMessage(raw))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	captured := read(`{"InvoiceID":"i1","Type":"ACCPAY","Status":"AUTHORISED","Contact":{"ContactID":"c1","Name":"Supplier"},"Date":"/Date(1790208000000+0000)/","DueDate":"/Date(1790812800000+0000)/","CurrencyCode":"USD","CurrencyRate":1,"LineAmountTypes":"NoTax","SubTotal":150,"TotalTax":0,"Total":150,"AmountDue":150,"AmountPaid":0,"AmountCredited":0,"UpdatedDateUTC":"/Date(1790208000000+0000)/",
 "LineItems":[{"LineItemID":"l1","Description":"Laptop","Quantity":1,"UnitAmount":100,"AccountCode":"429","AccountID":"a-general","TaxType":"NONE","TaxAmount":0,"LineAmount":100,"Tracking":[{"Name":"Region","Option":"East","TrackingCategoryID":"t1"}]},
 {"LineItemID":"l2","Description":"Taxi","Quantity":1,"UnitAmount":50,"AccountCode":"493","AccountID":"a-travel","TaxType":"NONE","TaxAmount":0,"LineAmount":50}]}`)
	live := read(`{"InvoiceID":"i1","Type":"ACCPAY","Status":"AUTHORISED","Contact":{"ContactID":"c1"},"Date":"2026-09-24T00:00:00","DueDate":"2026-10-01T00:00:00","CurrencyCode":"USD","CurrencyRate":1.0,"LineAmountTypes":"NoTax","SubTotal":150.00,"TotalTax":0.00,"Total":150.00,"AmountDue":150.00,"AmountPaid":0.00,"AmountCredited":0.00,"UpdatedDateUTC":"/Date(1790300000000+0000)/",
 "LineItems":[{"LineItemID":"l2","Description":"Taxi","Quantity":1.0,"UnitAmount":50.00,"AccountCode":"493","AccountID":"a-travel","TaxType":"NONE","TaxAmount":0.00,"LineAmount":50.00},
 {"LineItemID":"l1","Description":"Laptop","Quantity":1.0,"UnitAmount":100.00,"AccountCode":"429","AccountID":"a-general","TaxType":"NONE","TaxAmount":0.00,"LineAmount":100.00,"Tracking":[{"TrackingCategoryID":"t1","Option":"East","Name":"Region"}]}]}`)
	if !SameBill(captured, live) {
		t.Fatal("the same bill in Xero's date, number, order and key formats is the same")
	}
	recoded := Recoded(live, "l1", "a-equipment", "720")
	if SameBill(live, recoded) || !SameBill(Recoded(captured, "l1", "a-equipment", "720"), recoded) || live.LineItems[1].AccountCode != "429" {
		t.Fatal("a recode changes only the chosen line's account, on a copy")
	}
	for name, change := range map[string]func(*Bill){
		"other line's account": func(b *Bill) { b.LineItems[0].AccountID = "a-general" },
		"line removed":         func(b *Bill) { b.LineItems = b.LineItems[:1] },
		"line added":           func(b *Bill) { b.LineItems = append(b.LineItems, BillLine{LineItemID: "l3", LineAmount: "1"}) },
		"tracking":             func(b *Bill) { b.LineItems[1].Tracking = json.RawMessage(`[{"Name":"Region","Option":"West"}]`) },
		"due date":             func(b *Bill) { b.DueDate = "2026-10-02T00:00:00" },
		"currency rate":        func(b *Bill) { b.CurrencyRate = "1.1" },
		"amount paid":          func(b *Bill) { b.AmountPaid = "10" },
		"status":               func(b *Bill) { b.Status = "PAID" },
	} {
		changed := Recoded(live, "l1", "a-equipment", "720")
		changed.LineItems = append([]BillLine(nil), changed.LineItems...)
		change(&changed)
		if SameBill(recoded, changed) {
			t.Fatalf("a changed %s is not the approved readback", name)
		}
	}
}
