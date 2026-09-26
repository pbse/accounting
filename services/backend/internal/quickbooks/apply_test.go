package quickbooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreditApplicationsLinkBothDocumentsAndReadBack(t *testing.T) {
	payment := `{"Id":"70","SyncToken":"1","TxnDate":"2026-09-05","CustomerRef":{"value":"4","name":"Acme"},"CurrencyRef":{"value":"USD"},"TotalAmt":500,"UnappliedAmt":200,"Line":[{"Amount":300,"LinkedTxn":[{"TxnId":"90","TxnType":"Invoice"}]}]}`
	invoice := `{"Id":"91","SyncToken":"3","TxnDate":"2026-08-20","CustomerRef":{"value":"4","name":"Acme"},"CurrencyRef":{"value":"USD"},"TotalAmt":250,"Balance":250}`
	credit := `{"Id":"80","SyncToken":"2","TxnDate":"2026-09-01","VendorRef":{"value":"21","name":"Staples"},"CurrencyRef":{"value":"USD"},"Balance":60}`
	bill := `{"Id":"41","SyncToken":"5","TxnDate":"2026-08-25","VendorRef":{"value":"21","name":"Staples"},"CurrencyRef":{"value":"USD"},"Balance":100}`
	docs := map[string]string{"/payment/70": `{"Payment":` + payment + `}`, "/invoice/91": `{"Invoice":` + invoice + `}`, "/vendorcredit/80": `{"VendorCredit":` + credit + `}`, "/bill/41": `{"Bill":` + bill + `}`}
	var sent map[string]any
	var query string
	billPayment := `{"BillPayment":{"Id":"95","TxnDate":"2026-09-30","PayType":"Check","CheckPayment":{"BankAccountRef":{"value":"35"}},"CurrencyRef":{"value":"USD"},"VendorRef":{"value":"21"},"TotalAmt":0,"Line":[{"Amount":60,"LinkedTxn":[{"TxnId":"41","TxnType":"Bill"}]},{"Amount":60,"LinkedTxn":[{"TxnId":"80","TxnType":"VendorCredit"}]}]}}`
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
		path := strings.TrimPrefix(r.URL.Path, "/v3/company/123")
		body := docs[path]
		switch {
		case r.Method == "POST" && path == "/payment":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &sent)
			body = `{"Payment":{"Id":"70"}}`
		case r.Method == "POST" && path == "/billpayment":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &sent)
			query, body = r.URL.RawQuery, `{"BillPayment":{"Id":"95"}}`
		case path == "/billpayment/95":
			body = billPayment
		}
		if body == "" {
			t.Fatalf("unexpected QuickBooks call %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	ctx := context.Background()
	p, err := sandbox.ReadOpenDocument(ctx, "Payment", "70")
	i, err2 := sandbox.ReadOpenDocument(ctx, "Invoice", "91")
	if err != nil || err2 != nil || p.Open.FloatString(2) != "200.00" || i.Open.FloatString(2) != "250.00" || p.Party != i.Party {
		t.Fatalf("a payment's unapplied amount and an invoice's balance are what is open: %v %v %+v %+v", err, err2, p, i)
	}
	if err = sandbox.ApplyPayment(ctx, p, "91", "150.00"); err != nil {
		t.Fatal(err)
	}
	lines := sent["Line"].([]any)
	if sent["Id"] != "70" || sent["SyncToken"] != "1" || sent["sparse"] != true || len(lines) != 2 || lines[0].(map[string]any)["Amount"].(float64) != 300 || lines[1].(map[string]any)["LinkedTxn"].([]any)[0].(map[string]any)["TxnId"] != "91" {
		t.Fatalf("the payment keeps its applied line and gains one for the invoice: %v", sent)
	}
	if _, err = sandbox.VerifyApplication(ctx, "payment", p, i, "150.00", "", "", ""); err == nil || !strings.Contains(err.Error(), "did not fall") {
		t.Fatalf("unchanged documents fail readback: %v", err)
	}
	docs["/payment/70"] = `{"Payment":` + strings.Replace(payment, `"UnappliedAmt":200`, `"UnappliedAmt":50`, 1) + `}`
	docs["/invoice/91"] = `{"Invoice":` + strings.Replace(invoice, `"Balance":250`, `"Balance":100.00`, 1) + `}`
	if _, err = sandbox.VerifyApplication(ctx, "payment", p, i, "150.00", "", "", ""); err == nil || !strings.Contains(err.Error(), "links") {
		t.Fatalf("open amounts that fell without the invoice link fail readback: %v", err)
	}
	docs["/payment/70"] = strings.Replace(docs["/payment/70"], `"TxnType":"Invoice"}]}]`, `"TxnType":"Invoice"}]},{"Amount":150.00,"LinkedTxn":[{"TxnId":"91","TxnType":"Invoice"}]}]`, 1)
	if _, err = sandbox.VerifyApplication(ctx, "payment", p, i, "150.00", "", "", ""); err != nil {
		t.Fatalf("both open amounts fell and the payment gained the invoice link: %v", err)
	}

	c, _ := sandbox.ReadOpenDocument(ctx, "VendorCredit", "80")
	b, _ := sandbox.ReadOpenDocument(ctx, "Bill", "41")
	id, err := sandbox.ApplyVendorCredit(ctx, c, b, "60.00", "35", "2026-09-30", "req-1")
	billLines := sent["Line"].([]any)
	if err != nil || id != "95" || query != "requestid=req-1" || sent["TotalAmt"].(float64) != 0 || sent["TxnDate"] != "2026-09-30" || len(billLines) != 2 || sent["CheckPayment"].(map[string]any)["BankAccountRef"].(map[string]any)["value"] != "35" {
		t.Fatalf("a zero-amount bill payment links the bill and the credit, under a request ID: %v %s %v", err, query, sent)
	}
	docs["/vendorcredit/80"] = `{"VendorCredit":` + strings.Replace(credit, `"Balance":60`, `"Balance":0`, 1) + `}`
	docs["/bill/41"] = `{"Bill":` + strings.Replace(bill, `"Balance":100`, `"Balance":40`, 1) + `}`
	if _, err = sandbox.VerifyApplication(ctx, "vendor_credit", c, b, "60.00", "95", "2026-09-30", "35"); err != nil {
		t.Fatalf("the credit is used up and the bill falls by it: %v", err)
	}
	if _, err = sandbox.VerifyApplication(ctx, "vendor_credit", c, b, "60.00", "95", "2026-09-30", "36"); err == nil {
		t.Fatal("a bill payment from another bank account fails readback")
	}
	if _, err = sandbox.VerifyApplication(ctx, "vendor_credit", c, b, "60.00", "95", "2026-09-29", "35"); err == nil {
		t.Fatal("a bill payment on another date fails readback")
	}
	combined := billPayment
	billPayment = strings.Replace(combined, `[{"TxnId":"41","TxnType":"Bill"}]},{"Amount":60,"LinkedTxn":[{"TxnId":"80","TxnType":"VendorCredit"}]}`, `[{"TxnId":"41","TxnType":"Bill"},{"TxnId":"80","TxnType":"VendorCredit"}]},{"Amount":0}`, 1)
	if _, err = sandbox.VerifyApplication(ctx, "vendor_credit", c, b, "60.00", "95", "2026-09-30", "35"); err == nil {
		t.Fatal("one line linking both documents fails readback")
	}
	billPayment = strings.Replace(combined, `"TotalAmt":0`, `"TotalAmt":60`, 1)
	if _, err = sandbox.VerifyApplication(ctx, "vendor_credit", c, b, "60.00", "95", "2026-09-30", "35"); err == nil || !strings.Contains(err.Error(), "pays money") {
		t.Fatalf("a bill payment that pays money fails readback: %v", err)
	}
	production := sandbox
	production.Production, production.Accounts = true, []string{"7", "8"}
	if _, err = production.ApplyVendorCredit(ctx, c, b, "60.00", "35", "2026-09-30", "req-3"); err == nil || !strings.Contains(err.Error(), "grant") {
		t.Fatalf("a production bank account outside the grant is refused: %v", err)
	}
	other := b
	other.Party = Reference{"22"}
	if _, err = sandbox.ApplyVendorCredit(ctx, c, other, "60.00", "35", "2026-09-30", "req-2"); err == nil {
		t.Fatal("a credit is never applied to another vendor's bill")
	}
}
