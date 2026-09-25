package xero

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/quickbooks"
)

// books is a September 2026 snapshot with one of each transaction shape the checks read.
func books() map[string]any {
	contact := func(id, name string) map[string]string { return map[string]string{"ContactID": id, "Name": name} }
	return map[string]any{
		"date":         "2026-09-30",
		"organisation": map[string]any{"Organisations": []any{map[string]any{"BaseCurrency": "USD"}}},
		"accounts": map[string]any{"Accounts": []any{
			map[string]string{"AccountID": "a-620", "Code": "620", "Name": "Prepayments", "Type": "PREPAYMENT"},
			map[string]string{"AccountID": "a-710", "Code": "710", "Name": "Office Equipment", "Type": "FIXED"},
			map[string]string{"AccountID": "a-711", "Code": "711", "Name": "Accumulated Depreciation", "Type": "DEPRECIATN"},
			map[string]string{"AccountID": "a-400", "Code": "400", "Name": "Advertising", "Type": "EXPENSE"},
			map[string]string{"AccountID": "a-200", "Code": "200", "Name": "Sales", "Type": "REVENUE"},
			map[string]string{"AccountID": "a-999", "Code": "999", "Name": "Suspense", "Type": "CURRLIAB"},
			map[string]string{"AccountID": "a-090", "Code": "090", "Name": "Bank", "Type": "BANK"},
		}},
		"transactions": map[string]any{
			"records": map[string]any{
				"Invoices": []any{
					map[string]any{"Type": "ACCPAY", "InvoiceID": "bill-1", "InvoiceNumber": "INS-88", "Status": "AUTHORISED", "Date": "/Date(1788998400000+0000)/", "Contact": contact("c-1", "Harbor Insurance"), "CurrencyCode": "USD", "LineAmountTypes": "Inclusive", "Total": 115,
						"LineItems": []any{map[string]any{"AccountCode": "620", "LineAmount": 115, "TaxAmount": 15}, map[string]any{"LineAmount": 5, "TaxAmount": 0}}},
					map[string]any{"Type": "ACCREC", "InvoiceID": "inv-1", "InvoiceNumber": "INV-1", "Status": "PAID", "Date": "2026-09-12T00:00:00", "Contact": contact("c-2", "Acme"), "CurrencyCode": "USD", "LineAmountTypes": "Exclusive", "Total": 230,
						"LineItems": []any{map[string]any{"AccountID": "a-200", "LineAmount": 200, "TaxAmount": 30}}},
					map[string]any{"Type": "ACCPAY", "InvoiceID": "draft-1", "Status": "DRAFT", "Date": "2026-09-12T00:00:00", "Contact": contact("c-1", "Harbor Insurance"), "Total": 9},
				},
				"CreditNotes": []any{map[string]any{"Type": "ACCPAYCREDIT", "CreditNoteID": "cn-1", "CreditNoteNumber": "CN-1", "Status": "AUTHORISED", "Date": "2026-09-14T00:00:00", "Contact": contact("c-1", "Harbor Insurance"), "CurrencyCode": "USD", "Total": 20,
					"LineItems": []any{map[string]any{"AccountCode": "400", "LineAmount": 20}}}},
				"BankTransactions": []any{
					map[string]any{"Type": "SPEND", "BankTransactionID": "bt-1", "Reference": "card", "Status": "AUTHORISED", "Date": "2026-09-15T00:00:00", "Contact": contact("c-3", "Coffee Co"), "CurrencyCode": "USD", "Total": 12,
						"LineItems": []any{map[string]any{"AccountCode": "999", "LineAmount": 12}}},
					map[string]any{"Type": "SPEND-TRANSFER", "BankTransactionID": "bt-2", "Status": "AUTHORISED", "Date": "2026-09-16T00:00:00", "CurrencyCode": "USD", "Total": 1000},
					map[string]any{"Type": "RECEIVE", "BankTransactionID": "bt-3", "Status": "AUTHORISED", "Date": "2026-09-17T00:00:00", "Contact": contact("c-2", "Acme"), "CurrencyCode": "USD", "Total": 50,
						"LineItems": []any{map[string]any{"AccountCode": "200", "LineAmount": 50}}},
					map[string]any{"Type": "SPEND", "BankTransactionID": "bt-4", "Status": "DELETED", "Date": "2026-09-17T00:00:00", "Total": 7},
					map[string]any{"Type": "SPEND-OVERPAYMENT", "BankTransactionID": "bt-5", "Status": "AUTHORISED", "Date": "2026-09-18T00:00:00", "Contact": contact("c-1", "Harbor Insurance"), "CurrencyCode": "USD", "Total": 30,
						"LineItems": []any{map[string]any{"AccountCode": "400", "LineAmount": 30}}},
				},
				"ManualJournals": []any{map[string]any{"ManualJournalID": "mj-1", "Status": "POSTED", "Date": "2026-09-20T00:00:00", "Narration": "Laptop",
					"JournalLines": []any{map[string]any{"AccountID": "a-710", "LineAmount": 500}, map[string]any{"AccountCode": "090", "LineAmount": -500}}}},
				"Payments": []any{
					map[string]any{"PaymentID": "p-1", "PaymentType": "ACCRECPAYMENT", "Status": "AUTHORISED", "Date": "/Date(1788998400000+0000)/", "Amount": 230, "Invoice": map[string]any{"Contact": contact("c-2", "Acme"), "CurrencyCode": "EUR"}},
					map[string]any{"PaymentID": "p-2", "PaymentType": "APCREDITPAYMENT", "Status": "AUTHORISED", "Date": "2026-09-21T00:00:00", "Amount": 20, "Invoice": map[string]any{"Contact": contact("c-1", "Harbor Insurance")}},
				},
			},
			"open": map[string]any{
				"Invoices": []any{
					map[string]any{"Type": "ACCREC", "InvoiceID": "inv-9", "InvoiceNumber": "INV-9", "Status": "AUTHORISED", "Date": "/Date(1785542400000+0000)/", "DueDate": "2026-08-01T00:00:00", "Contact": contact("c-2", "Acme"), "CurrencyCode": "USD", "AmountDue": 300},
					map[string]any{"Type": "ACCPAY", "InvoiceID": "bill-9", "InvoiceNumber": "B-9", "Status": "AUTHORISED", "Date": "2026-07-01T00:00:00", "DueDate": "2026-07-31T00:00:00", "Contact": contact("c-1", "Harbor Insurance"), "CurrencyCode": "USD", "AmountDue": 80},
					map[string]any{"Type": "ACCPAY", "InvoiceID": "bill-0", "Status": "AUTHORISED", "Date": "2026-09-01T00:00:00", "Contact": contact("c-1", "Harbor Insurance"), "AmountDue": 0},
					map[string]any{"Type": "ACCREC", "InvoiceID": "inv-late", "Status": "AUTHORISED", "Date": "/Date(1792022400000+0000)/", "Contact": contact("c-2", "Acme"), "AmountDue": 10},
				},
				"CreditNotes":  []any{map[string]any{"Type": "ACCRECCREDIT", "CreditNoteID": "cn-9", "Status": "AUTHORISED", "Date": "2026-09-02T00:00:00", "Contact": contact("c-2", "Acme"), "CurrencyCode": "USD", "RemainingCredit": 40}},
				"Overpayments": []any{map[string]any{"Type": "RECEIVE-OVERPAYMENT", "OverpaymentID": "op-1", "Status": "AUTHORISED", "Date": "2026-09-03T00:00:00", "Contact": contact("c-2", "Acme"), "CurrencyCode": "USD", "RemainingCredit": 25}},
				"Prepayments":  []any{map[string]any{"Type": "SPEND-PREPAYMENT", "PrepaymentID": "pp-1", "Status": "AUTHORISED", "Date": "2026-09-04T00:00:00", "Contact": contact("c-1", "Harbor Insurance"), "CurrencyCode": "USD", "RemainingCredit": 60}},
			},
		},
	}
}

func TestLedgerReshapesXeroTransactionsForTheChecks(t *testing.T) {
	raw, _ := json.Marshal(books())
	rows, open, accounts, home, err := Ledger(raw)
	if err != nil || home != "USD" {
		t.Fatal(err, home)
	}
	kinds := map[string]string{}
	for _, r := range rows {
		kinds[r.ProviderID] = r.Kind + " " + r.Date + " " + r.Currency + " " + r.Amount
	}
	want := map[string]string{"bill-1": "Bill 2026-09-10 USD 115.00", "inv-1": "Invoice 2026-09-12 USD 230.00", "cn-1": "VendorCredit 2026-09-14 USD 20.00", "bt-1": "Purchase 2026-09-15 USD 12.00",
		"bt-2": "Transfer 2026-09-16 USD 1000.00", "bt-3": "Deposit 2026-09-17 USD 50.00", "mj-1": "JournalEntry 2026-09-20 USD 500.00", "p-1": "Payment 2026-09-10 EUR 230.00"}
	if len(kinds) != len(want) {
		t.Fatalf("drafts, deleted records, refunds and bank overpayments are not reshaped: %v", kinds)
	}
	for id, w := range want {
		if kinds[id] != w {
			t.Fatalf("%s: got %q, want %q", id, kinds[id], w)
		}
	}
	var bill struct {
		VendorRef struct{ Value, Name string }
		Line      []map[string]json.RawMessage
	}
	json.Unmarshal(rows[0].Record, &bill)
	if bill.VendorRef.Name != "Harbor Insurance" || len(bill.Line) != 2 || string(bill.Line[0]["Amount"]) != "100.00" || !strings.Contains(string(bill.Line[0]["AccountBasedExpenseLineDetail"]), `"name":"Prepayments","value":"a-620"`) || bill.Line[1]["ItemBasedExpenseLineDetail"] == nil {
		t.Fatalf("a tax-inclusive line is net of tax, a code finds its account, and a line without one is an item line: %s", rows[0].Record)
	}
	if !strings.Contains(string(rows[1].Record), `"Amount":200.00`) {
		t.Fatalf("a tax-exclusive line amount is already net of tax: %s", rows[1].Record)
	}
	candidates, unknown, items, err := quickbooks.ScheduleCandidates(rows, accounts)
	if err != nil || len(unknown) != 0 || items != 1 || len(candidates) != 2 || candidates[0].Type != "unscheduled_prepaid" || candidates[0].Amount != "100.00" || candidates[1].Type != "unscheduled_fixed_asset" || candidates[1].Amount != "500.00" {
		t.Fatalf("a prepayment bill and a journal to a fixed asset need schedules: %v %v %d %+v", err, unknown, items, candidates)
	}
	found := quickbooks.FindExceptions(rows)
	if len(found) != 1 || found[0].Type != "uncategorized" || found[0].Counterparty != "Coffee Co" {
		t.Fatalf("a purchase to a suspense account is a question: %+v", found)
	}
	receivables, payables, unreadable := quickbooks.Aging(open, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), home)
	if unreadable != 0 || len(receivables) != 1 || receivables[0].Buckets[2] != "300.00" || receivables[0].Open != "300.00" || receivables[0].Credits != "65.00" || len(payables) != 1 || payables[0].Buckets[3] != "80.00" || payables[0].Credits != "60.00" {
		t.Fatalf("open invoices age by due date, credits and overpayments stay apart; a zero balance and a later document are left out: %+v %+v", receivables, payables)
	}
	past := quickbooks.OpenItemExceptions(open, time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC))
	if len(past) != 2 || past[0].Type != "past_due_payable" || past[1].Type != "unapplied_payment" {
		t.Fatalf("the overdue bill and the unapplied overpayment raise questions; a 26-day-old supplier prepayment does not yet: %+v", past)
	}
	if _, _, _, _, err = Ledger(json.RawMessage(`{"date":"2026-09-30"}`)); err == nil || !strings.Contains(err.Error(), "has no transactions") {
		t.Fatalf("a snapshot from before transactions were read says so: %v", err)
	}
	unread, _ := json.Marshal(map[string]any{"date": "2026-09-30", "transactions_error": "reconnect"})
	if _, _, _, _, err = Ledger(unread); err == nil || err.Error() != "reconnect" {
		t.Fatalf("a snapshot without transactions says why: %v", err)
	}
}

type fakeXero func(*http.Request) (*http.Response, error)

func (f fakeXero) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTransactionsNeedTheReadScopes(t *testing.T) {
	token := func(scope string) string {
		claims, _ := json.Marshal(map[string]string{"scope": scope})
		return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	}
	if !CanReadTransactions(token("accounting.invoices.read accounting.payments.read accounting.banktransactions.read accounting.manualjournals")) || !CanReadTransactions(token("accounting.transactions")) {
		t.Fatal("the granular read scopes, or the older broad one, read transactions")
	}
	if CanReadTransactions(token("accounting.invoices.read accounting.banktransactions.read accounting.manualjournals")) || CanReadTransactions(token("accounting.manualjournals")) {
		t.Fatal("a connection missing a read scope must reconnect")
	}
}

func TestTransactionsPageToTheEndAndCheckXerosCount(t *testing.T) {
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	invoices, reported, updated, counted := 1001, 1001, "/Date(1500000000000+0000)/", true
	var queries []string
	http.DefaultTransport = fakeXero(func(r *http.Request) (*http.Response, error) {
		endpoint := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		page := 0
		fmt.Sscan(r.URL.Query().Get("page"), &page)
		records, count := []any{}, 0
		if endpoint == "Invoices" && r.URL.Query().Get("Statuses") == "AUTHORISED" && r.URL.Query().Get("where") != "Date<=DateTime(2026,09,30)" {
			t.Errorf("open invoices are every one dated by month-end: %s", r.URL.Query().Get("where"))
		}
		if endpoint == "Invoices" && r.URL.Query().Get("Statuses") == "AUTHORISED,PAID" {
			queries = append(queries, r.URL.RawQuery)
			count = reported
			for i := (page - 1) * 1000; i < min(page*1000, invoices); i++ {
				records = append(records, map[string]any{"InvoiceID": fmt.Sprint(i), "UpdatedDateUTC": updated})
			}
		}
		body := map[string]any{endpoint: records, "pagination": map[string]int{"page": page, "pageSize": 1000, "itemCount": count}}
		if !counted && page == 1 && r.URL.Query().Get("Statuses") == "AUTHORISED,PAID" {
			body["pagination"] = map[string]int{"page": 1, "pageSize": 1000}
		}
		raw, _ := json.Marshal(body)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: http.Header{}}, nil
	})
	monthEnd := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	out, err := Transactions(context.Background(), "token", "tenant", monthEnd)
	if err != nil || len(out["records"].(map[string][]json.RawMessage)["Invoices"]) != 1001 || len(queries) != 2 {
		t.Fatalf("both pages are read: %v %d", err, len(queries))
	}
	if !strings.Contains(queries[0], "pageSize=1000") || !strings.Contains(queries[1], "page=2") || !strings.Contains(queries[0], "Date%3E%3DDateTime%282026%2C09%2C01%29%26%26Date%3C%3DDateTime%282026%2C09%2C30%29") {
		t.Fatalf("the month's range, 1000 a page: %v", queries)
	}
	reported = 1002
	if _, err = Transactions(context.Background(), "token", "tenant", monthEnd); err == nil || !strings.Contains(err.Error(), "Xero counted 1002") {
		t.Fatalf("a count that disagrees with the records read asks for a new capture: %v", err)
	}
	reported, counted = 1001, false
	if _, err = Transactions(context.Background(), "token", "tenant", monthEnd); err == nil || !strings.Contains(err.Error(), "did not count") {
		t.Fatalf("a page without Xero's count cannot show the read is complete: %v", err)
	}
	counted, updated = true, fmt.Sprintf("/Date(%d+0000)/", time.Now().Add(time.Minute).UnixMilli())
	if _, err = Transactions(context.Background(), "token", "tenant", monthEnd); err == nil || !strings.Contains(err.Error(), "changed in Xero") {
		t.Fatalf("a record updated during the capture asks for a new one: %v", err)
	}
	invoices, reported, updated = 5001, 5001, "/Date(1500000000000+0000)/"
	if _, err = Transactions(context.Background(), "token", "tenant", monthEnd); err == nil || !strings.Contains(err.Error(), "5000-record") {
		t.Fatalf("more than 5000 records of a type refuse: %v", err)
	}
}
