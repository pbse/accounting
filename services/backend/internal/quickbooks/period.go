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
func PeriodReports(ctx context.Context, env, realm, token, period string) (map[string]json.RawMessage, error) {
	start, err := time.Parse("2006-01", period)
	if err != nil {
		return nil, fmt.Errorf("Invalid close month")
	}
	end := start.AddDate(0, 1, -1).Format("2006-01-02")
	reports := map[string]json.RawMessage{}
	currency := ""
	for _, name := range []string{"TransactionList", "GeneralLedger", "TrialBalance", "ProfitAndLoss"} {
		query := url.Values{"start_date": {start.Format("2006-01-02")}, "end_date": {end}, "accounting_method": {"Accrual"}}
		raw, err := read(ctx, env, realm, token, "/reports/"+name+"?"+query.Encode(), 8*1024*1024)
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
	byID := map[string]int{}
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
			item.Question = "Provide invoices and existing schedules with service dates, currency, original booking, credits and prior adjustments. A client accountant must approve any treatment beyond the established methods."
		case a.AccountType == "Other Current Liability" && strings.Contains(strings.ToLower(a.Name), "accru"):
			item.Kind = "accrual"
			item.Reason = "The account name suggests an accrued liability; its activity and invoices still need review."
			item.Question = "Provide incurred-service evidence, supported closing liability, current booked balance and any prior estimate or actual invoice. Confirm the client's approved method before preparing."
		}
		items = append(items, item)
		byID[a.ID] = index
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
				if i, found := byID[id]; found {
					items[i].ReportLocations = append(items[i].ReportLocations, location)
				} else {
					byID[id] = len(items)
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

// TrialBalanceAccount returns the exact reported cells for one prepaid account.
// The reviewer, not the report parser, interprets debit/credit signs and cutoff.
func TrialBalanceAccount(accounts, report json.RawMessage, accountID, currency string) (string, string, json.RawMessage, error) {
	return trialBalanceAccount(accounts, report, accountID, currency, "PrepaidExpenses")
}

func TrialBalanceLiability(accounts, report json.RawMessage, accountID, currency string) (string, string, json.RawMessage, error) {
	return trialBalanceAccount(accounts, report, accountID, currency, "Other Current Liability")
}

func trialBalanceAccount(accounts, report json.RawMessage, accountID, currency, requiredType string) (string, string, json.RawMessage, error) {
	var directory []struct {
		ID             string `json:"Id"`
		Name           string
		AccountType    string
		AccountSubType string
	}
	if json.Unmarshal(accounts, &directory) != nil {
		return "", "", nil, fmt.Errorf("Account directory is invalid")
	}
	name := ""
	for _, a := range directory {
		if a.ID == accountID && (a.AccountSubType == requiredType || a.AccountType == requiredType) {
			name = a.Name
		}
	}
	if accountID == "" || name == "" {
		return "", "", nil, fmt.Errorf("Choose an exact %s account in this snapshot", requiredType)
	}
	var root struct {
		Header struct{ Currency, ReportBasis string }
		Rows   struct{ Row []json.RawMessage }
	}
	if json.Unmarshal(report, &root) != nil {
		return "", "", nil, fmt.Errorf("Trial Balance is invalid")
	}
	if root.Header.Currency != currency || root.Header.ReportBasis != "Accrual" {
		return "", "", nil, fmt.Errorf("This comparison requires an accrual Trial Balance in the schedule's currency, %s", currency)
	}
	var location string
	var cells json.RawMessage
	var visit func([]json.RawMessage, string) error
	visit = func(rows []json.RawMessage, path string) error {
		for i, raw := range rows {
			var row struct {
				Type    string
				ColData json.RawMessage
				Rows    struct{ Row []json.RawMessage }
			}
			if json.Unmarshal(raw, &row) != nil {
				return fmt.Errorf("Trial Balance row is invalid")
			}
			at := fmt.Sprintf("%s.Row[%d]", path, i)
			var values []struct {
				ID string `json:"id"`
			}
			if row.Type == "Data" && json.Unmarshal(row.ColData, &values) == nil && len(values) > 0 && values[0].ID == accountID {
				if location != "" {
					return fmt.Errorf("Account appears more than once in Trial Balance")
				}
				location, cells = at, row.ColData
			}
			if err := visit(row.Rows.Row, at+".Rows"); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root.Rows.Row, "reports.TrialBalance.Rows"); err != nil {
		return "", "", nil, err
	}
	if location == "" {
		return "", "", nil, fmt.Errorf("Account has no Trial Balance row; do not assume zero")
	}
	return name, location, cells, nil
}

// LedgerPage selects only rows under an exact account ID. It preserves the
// provider's cells and JSON locations; a missing match is not a zero balance.
type LedgerRow struct {
	Location string          `json:"location"`
	Row      json.RawMessage `json:"row"`
}
type LedgerPage struct {
	AccountID string      `json:"account_id"`
	Query     string      `json:"query,omitempty"`
	Offset    int         `json:"offset"`
	Total     int         `json:"total"`
	Columns   []string    `json:"columns"`
	Rows      []LedgerRow `json:"rows"`
}

func SelectLedger(accounts, report json.RawMessage, accountID string, offset int) (LedgerPage, error) {
	return selectLedger(accounts, report, accountID, offset, "")
}

// SearchLedger scans the captured report once and returns only exact-account
// rows containing a reviewer-supplied reference. Search is a discovery aid;
// the original report location remains the citation.
func SearchLedger(accounts, report json.RawMessage, accountID, query string, offset int) (LedgerPage, error) {
	query = strings.TrimSpace(query)
	if len(query) < 3 || len(query) > 100 {
		return LedgerPage{}, fmt.Errorf("Search using a 3–100 character invoice, memo or transaction reference")
	}
	return selectLedger(accounts, report, accountID, offset, query)
}

func selectLedger(accounts, report json.RawMessage, accountID string, offset int, query string) (LedgerPage, error) {
	page := LedgerPage{AccountID: accountID, Query: query, Offset: offset, Rows: []LedgerRow{}}
	if accountID == "" || offset < 0 || offset > 100000 {
		return page, fmt.Errorf("Choose an account and a valid ledger offset")
	}
	var directory []struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(accounts, &directory) != nil {
		return page, fmt.Errorf("Account inventory is invalid")
	}
	found := false
	for _, account := range directory {
		found = found || account.ID == accountID
	}
	if !found {
		return page, fmt.Errorf("Account is not in this snapshot")
	}
	var root struct {
		Columns struct{ Column []struct{ ColTitle string } }
		Rows    struct{ Row []json.RawMessage }
	}
	if json.Unmarshal(report, &root) != nil {
		return page, fmt.Errorf("General Ledger report is invalid")
	}
	for _, column := range root.Columns.Column {
		page.Columns = append(page.Columns, column.ColTitle)
	}
	accountColumn := len(root.Columns.Column) > 0 && strings.Contains(strings.ToLower(root.Columns.Column[0].ColTitle), "account")
	needle := strings.ToLower(query)
	var visit func([]json.RawMessage, string, bool) error
	visit = func(rows []json.RawMessage, path string, inherited bool) error {
		for n, raw := range rows {
			location := fmt.Sprintf("%s.Row[%d]", path, n)
			var value struct {
				ColData []struct {
					ID, Value string
				}
				Header struct {
					ColData []struct {
						ID, Value string
					}
				}
				Rows struct{ Row []json.RawMessage }
			}
			if json.Unmarshal(raw, &value) != nil {
				return fmt.Errorf("General Ledger row is invalid")
			}
			matched := inherited
			if len(value.Header.ColData) > 0 && value.Header.ColData[0].ID != "" {
				matched = value.Header.ColData[0].ID == accountID
			}
			if accountColumn && len(value.ColData) > 0 && value.ColData[0].ID != "" {
				matched = value.ColData[0].ID == accountID
			}
			if matched {
				include := query == ""
				for _, cell := range value.ColData {
					include = include || strings.Contains(strings.ToLower(cell.Value), needle)
				}
				for _, cell := range value.Header.ColData {
					include = include || strings.Contains(strings.ToLower(cell.Value), needle)
				}
				if include {
					var cells map[string]json.RawMessage
					if json.Unmarshal(raw, &cells) != nil {
						return fmt.Errorf("General Ledger row is invalid")
					}
					delete(cells, "Rows")
					body, _ := json.Marshal(cells)
					if page.Total >= offset && len(page.Rows) < 20 {
						if len(body) > 32*1024 {
							return fmt.Errorf("A selected ledger row exceeds 32 KiB; inspect the original report")
						}
						page.Rows = append(page.Rows, LedgerRow{Location: location, Row: body})
					}
					page.Total++
				}
			}
			if err := visit(value.Rows.Row, location+".Rows", matched); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root.Rows.Row, "reports.GeneralLedger.Rows", false); err != nil {
		return page, err
	}
	return page, nil
}
