package quickbooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPurchaseAndDepositLinesAreRecodedWithEveryOtherFieldKept(t *testing.T) {
	purchase := `{"Id":"51","SyncToken":"2","TxnDate":"2026-09-10","PaymentType":"CreditCard","AccountRef":{"value":"35","name":"Visa"},"EntityRef":{"value":"12","name":"Staples","type":"Vendor"},"CurrencyRef":{"value":"USD"},"TotalAmt":90,"DocNumber":"R-9","Line":[{"Id":"1","Amount":50.00,"DetailType":"AccountBasedExpenseLineDetail","Description":"Paper","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"7","name":"Travel"}}},{"Id":"2","Amount":40,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"8","name":"Meals"}}}]}`
	deposit := `{"Id":"61","SyncToken":"5","TxnDate":"2026-09-12","DepositToAccountRef":{"value":"35","name":"Checking"},"CurrencyRef":{"value":"USD"},"TotalAmt":300,"Line":[{"Id":"1","Amount":300,"DetailType":"DepositLineDetail","DepositLineDetail":{"AccountRef":{"value":"81","name":"Uncategorized Income"},"Entity":{"value":"4","name":"Acme","type":"CUSTOMER"},"PaymentMethodRef":{"value":"2","name":"Check"},"CheckNum":"1004"}}]}`
	readback := map[string]string{}
	var sent map[string]any
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == "POST":
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &sent)
			kind := map[string]string{"/v3/company/123/purchase": "Purchase", "/v3/company/123/deposit": "Deposit"}[r.URL.Path]
			body = `{"` + kind + `":{"Id":"` + sent["Id"].(string) + `"}}`
		case r.URL.Path == "/v3/company/123/purchase/51":
			body = `{"Purchase":` + readback["Purchase"] + `}`
		case r.URL.Path == "/v3/company/123/deposit/61":
			body = `{"Deposit":` + readback["Deposit"] + `}`
		default:
			t.Fatalf("unexpected QuickBooks call %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	ctx := context.Background()

	if err := sandbox.Recode(ctx, "Purchase", json.RawMessage(purchase), "2", "9"); err != nil {
		t.Fatal(err)
	}
	lines := sent["Line"].([]any)
	first, second := lines[0].(map[string]any), lines[1].(map[string]any)
	if sent["SyncToken"] != "2" || sent["sparse"] != true || first["Description"] != "Paper" || first["AccountBasedExpenseLineDetail"].(map[string]any)["AccountRef"].(map[string]any)["value"] != "7" || second["AccountBasedExpenseLineDetail"].(map[string]any)["AccountRef"].(map[string]any)["value"] != "9" {
		t.Fatalf("the purchase's lines are sent as read, with only line 2's account changed: %v", sent)
	}
	readback["Purchase"] = strings.Replace(strings.Replace(purchase, `"SyncToken":"2"`, `"SyncToken":"3"`, 1), `{"value":"8","name":"Meals"}`, `{"value":"9","name":"Supplies"}`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Purchase", json.RawMessage(purchase), "2", "9"); err != nil {
		t.Fatalf("a readback with only the line's account changed passes: %v", err)
	}
	changed := readback["Purchase"]
	readback["Purchase"] = strings.Replace(strings.Replace(changed, `"TotalAmt":90`, `"TotalAmt":91`, 1), `"Amount":50.00`, `"Amount":51`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Purchase", json.RawMessage(purchase), "2", "9"); err == nil {
		t.Fatal("a changed amount fails readback")
	}
	readback["Purchase"] = strings.Replace(changed, `"DocNumber":"R-9"`, `"DocNumber":"R-9","PrivateNote":"added"`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Purchase", json.RawMessage(purchase), "2", "9"); err == nil || !strings.Contains(err.Error(), "adds a field") {
		t.Fatalf("a field that appears on readback fails it: %v", err)
	}
	if !SameRecodable("Purchase", json.RawMessage(purchase), json.RawMessage(strings.Replace(purchase, `"Amount":40`, `"Amount":40.00`, 1)), "USD") || SameRecodable("Purchase", json.RawMessage(purchase), json.RawMessage(strings.Replace(purchase, `{"value":"8","name":"Meals"}`, `{"value":"6","name":"Meals"}`, 1)), "USD") || SameRecodable("Purchase", json.RawMessage(purchase), json.RawMessage(strings.Replace(purchase, `"name":"Staples"`, `"name":"Office Depot"`, 1)), "USD") {
		t.Fatal("copies agree whatever the number formatting, and not when a line's account or the payee differs at the same version")
	}
	readback["Purchase"] = strings.Replace(changed, `"name":"Staples"`, `"name":"Office Depot"`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Purchase", json.RawMessage(purchase), "2", "9"); err == nil || !strings.Contains(err.Error(), "EntityRef") {
		t.Fatalf("a changed payee fails readback: %v", err)
	}

	if err := sandbox.Recode(ctx, "Deposit", json.RawMessage(deposit), "1", "82"); err != nil {
		t.Fatal(err)
	}
	detail := sent["Line"].([]any)[0].(map[string]any)["DepositLineDetail"].(map[string]any)
	if detail["AccountRef"].(map[string]any)["value"] != "82" || detail["Entity"].(map[string]any)["type"] != "CUSTOMER" || detail["CheckNum"] != "1004" || detail["PaymentMethodRef"].(map[string]any)["name"] != "Check" {
		t.Fatalf("the deposit line keeps its payer, payment method and check number: %v", detail)
	}
	readback["Deposit"] = strings.Replace(strings.Replace(deposit, `"SyncToken":"5"`, `"SyncToken":"6"`, 1), `{"value":"81","name":"Uncategorized Income"}`, `{"value":"82","name":"Consulting Income"}`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Deposit", json.RawMessage(deposit), "1", "82"); err != nil {
		t.Fatalf("a deposit readback with only the account changed passes: %v", err)
	}
	readback["Deposit"] = strings.Replace(readback["Deposit"], `"CheckNum":"1004"`, `"CheckNum":"1005"`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Deposit", json.RawMessage(deposit), "1", "82"); err == nil {
		t.Fatal("a changed check number on the recoded line fails readback")
	}
	readback["Deposit"] = strings.Replace(strings.Replace(deposit, `"SyncToken":"5"`, `"SyncToken":"6"`, 1), `"TotalAmt":300`, `"TotalAmt":300.00`, 1)
	if _, err := sandbox.VerifyRecode(ctx, "Deposit", json.RawMessage(deposit), "1", "82"); err == nil || !strings.Contains(err.Error(), "different account") {
		t.Fatalf("a readback still on the old account fails, whatever the number formatting: %v", err)
	}

	for name, bad := range map[string][2]string{
		"a purchase linked to another transaction": {"Purchase", strings.Replace(purchase, `"DocNumber":"R-9"`, `"LinkedTxn":[{"TxnId":"3","TxnType":"Bill"}]`, 1)},
		"a purchase with tax":                      {"Purchase", strings.Replace(purchase, `"DocNumber":"R-9"`, `"TxnTaxDetail":{"TotalTax":5}`, 1)},
		"a purchase item line":                     {"Purchase", strings.Replace(purchase, `"DetailType":"AccountBasedExpenseLineDetail","Description"`, `"DetailType":"ItemBasedExpenseLineDetail","Description"`, 1)},
		"a deposited payment":                      {"Deposit", strings.Replace(deposit, `"DetailType":"DepositLineDetail"`, `"LinkedTxn":[{"TxnId":"9","TxnType":"Payment"}],"DetailType":"DepositLineDetail"`, 1)},
		"cash back":                                {"Deposit", strings.Replace(deposit, `"TotalAmt":300`, `"TotalAmt":300,"CashBack":{"Amount":0}`, 1)},
		"an unsupported deposit detail":            {"Deposit", strings.Replace(deposit, `"CheckNum":"1004"`, `"TxnType":"Payment"`, 1)},
	} {
		if _, _, _, _, err := ParseRecodable(bad[0], json.RawMessage(bad[1]), "USD"); err == nil {
			t.Fatalf("%s is refused", name)
		}
	}
}
