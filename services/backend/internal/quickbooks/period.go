package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// PeriodReports preserves report cells and provider decimals verbatim. These are
// sequential report reads, not an atomic ledger export or transaction entities.
func PeriodReports(ctx context.Context, realm, token, period string) (map[string]json.RawMessage, error) {
	start, err := time.Parse("2006-01", period)
	if err != nil {
		return nil, fmt.Errorf("Invalid close month")
	}
	end := start.AddDate(0, 1, -1).Format("2006-01-02")
	reports := map[string]json.RawMessage{}
	currency := ""
	for _, name := range []string{"TransactionList", "GeneralLedger", "TrialBalance", "ProfitAndLoss"} {
		query := url.Values{"start_date": {start.Format("2006-01-02")}, "end_date": {end}, "accounting_method": {"Accrual"}}
		raw, err := readSandbox(ctx, realm, token, "/reports/"+name+"?"+query.Encode())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		var report struct {
			Header struct {
				ReportName, StartPeriod, EndPeriod, ReportBasis, Currency, Time string
				Option                                                          []struct{ Name, Value string }
			}
			Columns *struct{ Column []json.RawMessage }
			Rows    *struct{ Row []json.RawMessage }
		}
		if json.Unmarshal(raw, &report) != nil || report.Header.ReportName != name || report.Header.StartPeriod != start.Format("2006-01-02") || report.Header.EndPeriod != end || report.Header.ReportBasis != "Accrual" || report.Header.Currency == "" || report.Header.Time == "" || report.Columns == nil || len(report.Columns.Column) == 0 || report.Rows == nil {
			return nil, fmt.Errorf("%s: report identity, period, basis or structure could not be verified", name)
		}
		if currency != "" && currency != report.Header.Currency {
			return nil, fmt.Errorf("%s: inconsistent report currency", name)
		}
		currency = report.Header.Currency
		empty := false
		for _, option := range report.Header.Option {
			if option.Name == "NoReportData" {
				empty = option.Value == "true"
			}
			if strings.Contains(strings.ToLower(option.Name), "truncat") && option.Value != "false" {
				return nil, fmt.Errorf("%s: provider reported truncated data", name)
			}
		}
		if (len(report.Rows.Row) == 0) != empty {
			return nil, fmt.Errorf("%s: empty-report status is inconsistent", name)
		}
		reports[name] = raw
	}
	return reports, nil
}

type CoverageItem struct {
	AccountID       string   `json:"account_id"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Reason          string   `json:"reason"`
	Question        string   `json:"question"`
	Location        string   `json:"location"`
	ReportLocations []string `json:"report_locations"`
}

// Account classification proposes evidence needs; it never proves that work is
// required, satisfied or immaterial. Inactive accounts remain in scope.
func Coverage(accounts json.RawMessage, reports map[string]json.RawMessage) ([]CoverageItem, error) {
	var rows []struct {
		ID                                string `json:"Id"`
		Name, AccountType, AccountSubType string
	}
	if err := json.Unmarshal(accounts, &rows); err != nil {
		return nil, err
	}
	items := []CoverageItem{}
	for index, a := range rows {
		item := CoverageItem{AccountID: a.ID, Name: a.Name, Kind: "accountant_review", Reason: "No supported preparation selected for this account type.", Question: "Review period activity and balances; define any required work or explicitly document exclusion from the close scope.", Location: fmt.Sprintf("accounts[%d]", index)}
		switch {
		case a.AccountType == "Bank" || a.AccountType == "Credit Card":
			item.Kind = "reconciliation"
			item.Reason = "Bank or credit-card account requires independent evidence before reconciliation can be proposed."
			item.Question = "Provide this month's complete independent statement and an account-specific ledger CSV (id,date,amount), verified opening/closing balances and page count. Report rows are not automatically converted to reconciliation transactions."
		case a.AccountSubType == "PrepaidExpenses":
			item.Kind = "prepaid"
			item.Reason = "Prepaid account is a candidate for schedule review; the ledger does not prove invoice service terms."
			item.Question = "Provide invoices and existing schedules with service dates, currency, original booking and prior adjustments. Only fully expensed USD invoices covering full calendar months can use the current calculator."
		}
		items = append(items, item)
	}
	// TrialBalance data-row account IDs link coverage to reported balances.
	// Preserve cell values in evidence; do not infer signs or calculate totals.
	var report struct{ Rows reportRows }
	if err := json.Unmarshal(reports["TrialBalance"], &report); err != nil {
		return nil, err
	}
	var visit func(reportRows, string)
	visit = func(group reportRows, path string) {
		for index, row := range group.Row {
			location := fmt.Sprintf("%s.Row[%d]", path, index)
			if row.Type == "Data" && len(row.ColData) > 0 && row.ColData[0].ID != "" {
				id := row.ColData[0].ID
				found := false
				for i := range items {
					if items[i].AccountID == id {
						items[i].ReportLocations = append(items[i].ReportLocations, location)
						found = true
						break
					}
				}
				if !found {
					items = append(items, CoverageItem{AccountID: id, Name: row.ColData[0].Value, Kind: "accountant_review", Reason: "Trial balance references an account missing from the account capture.", Question: "Resolve the inconsistent account inventory and recapture before preparing this account.", Location: location, ReportLocations: []string{location}})
				}
			}
			visit(row.Rows, location+".Rows")
		}
	}
	visit(report.Rows, "reports.TrialBalance.Rows")
	return items, nil
}

type reportRows struct {
	Row []struct {
		Type    string `json:"type"`
		ColData []struct {
			ID    string `json:"id"`
			Value string `json:"value"`
		}
		Rows reportRows
	}
}
