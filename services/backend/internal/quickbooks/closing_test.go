package quickbooks

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
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
