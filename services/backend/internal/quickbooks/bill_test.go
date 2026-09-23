package quickbooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBillCorrectionPreservesSimpleBillAndChecksReadback(t *testing.T) {
	beforeJSON := `{"Id":"41","SyncToken":"3","TxnDate":"2026-09-15","VendorRef":{"value":"21"},"CurrencyRef":{"value":"USD"},"TotalAmt":100,"Balance":100,"Line":[{"Id":"1","Amount":100,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"7"}}}]}`
	var before Bill
	if err := json.Unmarshal([]byte(beforeJSON), &before); err != nil {
		t.Fatal(err)
	}
	if !simpleBill(before) {
		t.Fatal("supported Bill was rejected")
	}
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == "POST" && r.URL.Path == "/v3/company/123/bill":
			data, _ := io.ReadAll(r.Body)
			var update struct {
				ID        string `json:"Id"`
				SyncToken string
				Sparse    bool
				Line      []struct {
					ID                            string `json:"Id"`
					AccountBasedExpenseLineDetail struct{ AccountRef Reference }
				}
			}
			if json.Unmarshal(data, &update) != nil || update.ID != "41" || update.SyncToken != "3" || !update.Sparse || len(update.Line) != 1 || update.Line[0].ID != "1" || update.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value != "9" {
				t.Fatal("Bill update lost its exact version, line or target account")
			}
			body = `{"Bill":{"Id":"41"}}`
		case r.Method == "GET" && r.URL.Path == "/v3/company/123/bill/41":
			body = `{"Bill":{"Id":"41","SyncToken":"4","TxnDate":"2026-09-15","VendorRef":{"value":"21"},"CurrencyRef":{"value":"USD"},"TotalAmt":100,"Balance":100,"Line":[{"Id":"1","Amount":100,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"9"}}}]}}`
		default:
			t.Fatalf("unexpected QuickBooks call %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	if _, err := sandbox.CorrectBillAccount(context.Background(), before, "9"); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.VerifyBillCorrection(context.Background(), before, "9"); err != nil {
		t.Fatal(err)
	}
	before.TotalAmt = "101"
	if _, err := sandbox.VerifyBillCorrection(context.Background(), before, "9"); err == nil {
		t.Fatal("changed amount passed readback")
	}
	sandbox.Production = true
	if _, err := sandbox.CorrectBillAccount(context.Background(), before, "9"); err == nil {
		t.Fatal("production Bill update was allowed")
	}
}
