package quickbooks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"testing"
)

func TestTheStatementOfCashFlowsIsKeptOnlyForTheMonth(t *testing.T) {
	prior := http.DefaultTransport
	defer func() { http.DefaultTransport = prior }()
	ending, status := "2026-09-30", 200
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		name, end, code := path.Base(r.URL.Path), "2026-09-30", 200
		if name == "CashFlow" {
			end, code = ending, status
		}
		body := fmt.Sprintf(`{"Header":{"ReportName":%q,"StartPeriod":"2026-09-01","EndPeriod":%q,"ReportBasis":"Accrual","Currency":"USD","Time":"2026-10-02T10:00:00Z","Option":[{"Name":"NoReportData","Value":"false"}]},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[{"ColData":[{"value":"Line"},{"value":"1.00"}]}]}}`, name, end)
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})
	reports, err := PeriodReports(context.Background(), "sandbox", "123", "access-synthetic", "2026-09")
	if err != nil || reports["CashFlow"] == nil || reports["TrialBalance"] == nil {
		t.Fatalf("the month's statement of cash flows is kept: %v %v", err, len(reports))
	}
	ending = "2026-08-31"
	if reports, err = PeriodReports(context.Background(), "sandbox", "123", "access-synthetic", "2026-09"); err != nil || reports["CashFlow"] != nil {
		t.Fatalf("a statement for another period is not kept: %v", err)
	}
	ending, status = "2026-09-30", 500
	if reports, err = PeriodReports(context.Background(), "sandbox", "123", "access-synthetic", "2026-09"); err != nil || reports["CashFlow"] != nil || reports["ProfitAndLoss"] == nil {
		t.Fatalf("a statement that cannot be read does not fail the capture: %v", err)
	}
}
