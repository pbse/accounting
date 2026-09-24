package quickbooks

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type vendorCreditRoundTrip func(*http.Request) (*http.Response, error)

func (f vendorCreditRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestVendorCreditChecksAccountsAndCanonicalReadback(t *testing.T) {
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/v3/company/123/companyinfo/123":
			body = `{"CompanyInfo":{"CompanyName":"Example LLC"}}`
		case "/v3/company/123/preferences":
			body = `{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":"2026-08-31"}}}`
		case "/v3/company/123/vendor/21":
			body = `{"Vendor":{"Id":"21","Active":true,"DisplayName":"Supplier"}}`
		case "/v3/company/123/account/5":
			body = `{"Account":{"Id":"5","Name":"Accounts Payable","AccountType":"Accounts Payable","AccountSubType":"AccountsPayable","Active":true,"CurrencyRef":{"value":"USD"},"CurrentBalance":0,"SyncToken":"1"}}`
		case "/v3/company/123/account/7":
			body = `{"Account":{"Id":"7","Name":"Prepaid service","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses","Active":true,"CurrencyRef":{"value":"USD"},"CurrentBalance":0,"SyncToken":"1"}}`
		case "/v3/company/123/query":
			if !strings.Contains(r.URL.Query().Get("query"), "DocNumber = 'Credit CN-7'") {
				t.Fatal("provider duplicate check did not use the exact credit reference")
			}
			body = `{"QueryResponse":{"VendorCredit":[]}}`
		case "/v3/company/123/vendorcredit/33":
			body = `{"VendorCredit":{"Id":"33","DocNumber":"Credit CN-7","TxnDate":"2026-09-15","VendorRef":{"value":"21"},"APAccountRef":{"value":"5"},"CurrencyRef":{"value":"USD"},"PrivateNote":"Reviewed supplier credit / Credit CN-7","Line":[{"Amount":25,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"7"}}}]}}`
		default:
			t.Fatalf("unexpected provider request %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	credit, err := sandbox.PrepareVendorCredit(context.Background(), "21", "5", "7", "Prepaid service", "2026-09-15", "25.00", "Credit CN-7")
	if err != nil {
		t.Fatal(err)
	}
	if credit.VendorRef.Value != "21" || credit.APAccountRef.Value != "5" || credit.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value != "7" {
		t.Fatal("wrong native credit accounts")
	}
	if _, err = sandbox.VerifyVendorCredit(context.Background(), "33", credit); err != nil {
		t.Fatal(err)
	}
	changed := credit
	changed.Line = []CreditLine{credit.Line[0]}
	changed.Line[0].Amount = json.Number("26.00")
	if _, err = sandbox.VerifyVendorCredit(context.Background(), "33", changed); err == nil {
		t.Fatal("amount mismatch was accepted")
	}
	// Production credits the accounts the client's grant allows, not the sandbox's configured prepaid account.
	production := sandbox
	production.Production, production.DebitAccount, production.Accounts = true, "", []string{"5", "9"}
	if _, err = production.PrepareVendorCredit(context.Background(), "21", "5", "7", "Prepaid service", "2026-09-15", "25.00", "Credit CN-7"); err == nil || !strings.Contains(err.Error(), "allowed") {
		t.Fatalf("a production credit to an account outside the grant was prepared: %v", err)
	}
	production.Accounts = []string{"5", "7"}
	if id, err := production.AccountNamed(context.Background(), "Prepaid service"); err != nil || id != "7" {
		t.Fatalf("the granted prepaid account is found by name: %q %v", id, err)
	}
	if _, err = production.PrepareVendorCredit(context.Background(), "21", "5", "7", "Prepaid service", "2026-09-15", "25.00", "Credit CN-7"); err != nil {
		t.Fatalf("a production credit between granted accounts is prepared: %v", err)
	}
}

func TestVendorCreditBlocksAnExistingProviderReference(t *testing.T) {
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/v3/company/123/companyinfo/123":
			body = `{"CompanyInfo":{"CompanyName":"Example LLC"}}`
		case "/v3/company/123/preferences":
			body = `{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":"2026-08-31"}}}`
		case "/v3/company/123/vendor/21":
			body = `{"Vendor":{"Id":"21","Active":true,"DisplayName":"Supplier"}}`
		case "/v3/company/123/account/5":
			body = `{"Account":{"Id":"5","Name":"Accounts Payable","AccountType":"Accounts Payable","AccountSubType":"AccountsPayable","Active":true,"CurrencyRef":{"value":"USD"},"CurrentBalance":0,"SyncToken":"1"}}`
		case "/v3/company/123/account/7":
			body = `{"Account":{"Id":"7","Name":"Prepaid service","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses","Active":true,"CurrencyRef":{"value":"USD"},"CurrentBalance":0,"SyncToken":"1"}}`
		case "/v3/company/123/query":
			body = `{"QueryResponse":{"VendorCredit":[{"Id":"33","DocNumber":"CN-7"}]}}`
		default:
			t.Fatalf("unexpected provider request %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	if _, err := sandbox.PrepareVendorCredit(context.Background(), "21", "5", "7", "Prepaid service", "2026-09-15", "25.00", "CN-7"); err == nil {
		t.Fatal("an already booked supplier credit was accepted")
	}
}

func TestVendorCreditRefusesAClosedPeriodBeforeAnyWrite(t *testing.T) {
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := `{"CompanyInfo":{"CompanyName":"Example LLC"}}`
		if r.URL.Path == "/v3/company/123/preferences" {
			body = `{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":"2026-09-30"}}}`
		} else if r.URL.Path != "/v3/company/123/companyinfo/123" {
			t.Fatalf("closed period must stop before %s", r.URL.String())
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	if _, err := sandbox.PrepareVendorCredit(context.Background(), "21", "5", "7", "Prepaid service", "2026-09-15", "25.00", "Credit CN-7"); err == nil || !strings.Contains(err.Error(), "closed through 2026-09-30") {
		t.Fatalf("a credit dated inside the closed period must be refused: %v", err)
	}
}
