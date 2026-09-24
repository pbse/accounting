package quickbooks

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestOpenPeriodFollowsTheCompanyClosingDate(t *testing.T) {
	for _, c := range []struct {
		preferences, date string
		open              bool
	}{
		{`{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":"2026-09-30"}}}`, "2026-09-30", false},
		{`{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":"2026-08-31"}}}`, "2026-09-01", true},
		{`{"Preferences":{"AccountingInfoPrefs":{}}}`, "2026-09-01", true},
		{`{"CompanyInfo":{}}`, "2026-09-01", false},
		{`{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":"30/09/2026"}}}`, "2026-10-01", false},
	} {
		sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
		sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(c.preferences)), Header: make(http.Header)}, nil
		})}
		if _, err := sandbox.OpenPeriod(context.Background(), c.date); (err == nil) != c.open {
			t.Errorf("%s on %s: open=%v, err=%v", c.preferences, c.date, c.open, err)
		}
	}
}

func TestDeferredRevenueAndDepreciationStayDraftOnly(t *testing.T) {
	paper, err := prepaids.Prepare(prepaids.Input{Entity: "Example LLC", SourceReference: "Annual plan", Amount: "1200.00", ServiceStart: "2026-09-01", ServiceEnd: "2027-08-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Revenue", PrepaidAccount: "Deferred revenue", Schedule: prepaids.DeferredRevenue, Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	sandbox := Sandbox{Realm: "123", Token: "synthetic", Entity: "Example LLC", DebitAccount: "7", CreditAccount: "8"}
	sandbox.Client = &http.Client{Transport: vendorCreditRoundTrip(func(*http.Request) (*http.Response, error) {
		t.Fatal("a deferred revenue draft reached QuickBooks")
		return nil, nil
	})}
	if _, _, err := sandbox.PrepareObserved(context.Background(), paper); err == nil || !strings.Contains(err.Error(), "draft review only") {
		t.Fatalf("deferred revenue must not post through the prepaid journal: %v", err)
	}
}
