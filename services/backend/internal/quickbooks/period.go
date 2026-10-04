package quickbooks

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
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
	// The Statement of Cash Flows is kept only when the ledger gives it for exactly this month in the same currency; a
	// capture never fails for it, and the management pack says when it is missing.
	query := url.Values{"start_date": {start.Format("2006-01-02")}, "end_date": {end}, "accounting_method": {"Accrual"}}
	optional, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 40*time.Second {
		return reports, nil
	}
	if raw, err := read(optional, env, realm, token, "/reports/CashFlow?"+query.Encode(), 1024*1024); err == nil {
		var report struct {
			Header struct {
				ReportName, StartPeriod, EndPeriod, Currency string
				Option                                       []struct{ Name, Value string }
			}
		}
		kept := json.Unmarshal(raw, &report) == nil && report.Header.ReportName == "CashFlow" && report.Header.StartPeriod == start.Format("2006-01-02") && report.Header.EndPeriod == end && report.Header.Currency == currency
		for _, option := range report.Header.Option {
			kept = kept && !(strings.Contains(strings.ToLower(option.Name), "truncat") && option.Value != "false")
		}
		if kept {
			reports["CashFlow"] = raw
		}
	}
	return reports, nil
}

// CashSection is one activity section of the ledger's Statement of Cash Flows: its lines as the report names them and
// the total it states.
type CashSection struct {
	Name  string
	Lines []CashLine
	Total *big.Rat
}

type CashLine struct {
	Name   string
	Amount *big.Rat
}

// CashFlow reads QuickBooks' Statement of Cash Flows: the operating, investing and financing sections, and the net
// change, beginning cash and ending cash it states (keyed CashIncrease, BeginningCash and EndingCash).
func CashFlow(raw json.RawMessage) ([]CashSection, map[string]*big.Rat, error) {
	type cells []struct {
		Value string `json:"value"`
	}
	type row struct {
		Group   string `json:"group"`
		ColData cells  `json:"ColData"`
		Rows    *struct {
			Row []json.RawMessage `json:"Row"`
		} `json:"Rows"`
		Summary *struct {
			ColData cells `json:"ColData"`
		} `json:"Summary"`
	}
	amount := func(c cells) (*big.Rat, error) {
		if len(c) < 2 {
			return nil, fmt.Errorf("a row has no amount")
		}
		return ledger.Net(c[len(c)-1].Value, "")
	}
	var lines func([]json.RawMessage) ([]CashLine, error)
	lines = func(rows []json.RawMessage) ([]CashLine, error) {
		out := []CashLine{}
		for _, raw := range rows {
			var r row
			if err := json.Unmarshal(raw, &r); err != nil {
				return nil, fmt.Errorf("a row is not readable")
			}
			if len(r.ColData) >= 2 {
				value, err := amount(r.ColData)
				if err != nil {
					return nil, err
				}
				out = append(out, CashLine{strings.TrimSpace(r.ColData[0].Value), value})
			}
			if r.Rows != nil {
				nested, err := lines(r.Rows.Row)
				if err != nil {
					return nil, err
				}
				out = append(out, nested...)
			}
		}
		return out, nil
	}
	var report struct {
		Rows struct {
			Row []json.RawMessage `json:"Row"`
		} `json:"Rows"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, nil, fmt.Errorf("the report is not readable")
	}
	names := map[string]string{"OperatingActivities": "Operating activities", "InvestingActivities": "Investing activities", "FinancingActivities": "Financing activities"}
	sections, totals := []CashSection{}, map[string]*big.Rat{}
	for _, raw := range report.Rows.Row {
		var r row
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, nil, fmt.Errorf("a section is not readable")
		}
		switch r.Group {
		case "OperatingActivities", "InvestingActivities", "FinancingActivities", "CashIncrease", "BeginningCash", "EndingCash":
			stated := r.ColData
			if r.Summary != nil {
				stated = r.Summary.ColData
			}
			total, err := amount(stated)
			if err != nil {
				return nil, nil, err
			}
			if names[r.Group] == "" {
				totals[r.Group] = total
				continue
			}
			section := CashSection{Name: names[r.Group], Lines: []CashLine{}, Total: total}
			if r.Rows != nil {
				if section.Lines, err = lines(r.Rows.Row); err != nil {
					return nil, nil, err
				}
			}
			sections = append(sections, section)
		}
	}
	for _, group := range []string{"CashIncrease", "BeginningCash", "EndingCash"} {
		if totals[group] == nil {
			return nil, nil, fmt.Errorf("the report does not state the net change, beginning cash and ending cash")
		}
	}
	return sections, totals, nil
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

func TrialBalanceLiability(accounts, report json.RawMessage, accountID, currency string) (string, string, json.RawMessage, error) {
	return TrialBalanceAccount(accounts, report, accountID, currency, "Other Current Liability")
}

// TrialBalanceAccount returns the exact reported cells for one account of the required type or subtype.
// The reviewer, not the report parser, interprets debit/credit signs and cutoff.
func TrialBalanceAccount(accounts, report json.RawMessage, accountID, currency, requiredType string) (string, string, json.RawMessage, error) {
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
	err := walkLedger(root.Rows.Row, "reports.GeneralLedger.Rows", "", accountColumn, func(location, account string, raw json.RawMessage, value ledgerRow) error {
		if account != accountID {
			return nil
		}
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
		return nil
	})
	return page, err
}

type ledgerCell struct{ ID, Value string }

type ledgerRow struct {
	Type    string `json:"type"`
	ColData []ledgerCell
	Header  struct{ ColData []ledgerCell }
	Rows    struct{ Row []json.RawMessage }
}

// walkLedger visits every General Ledger row with the account it sits under: a section header's or, with an account column, its own.
func walkLedger(rows []json.RawMessage, path, account string, accountColumn bool, fn func(location, account string, raw json.RawMessage, row ledgerRow) error) error {
	for n, raw := range rows {
		location := fmt.Sprintf("%s.Row[%d]", path, n)
		var row ledgerRow
		if json.Unmarshal(raw, &row) != nil {
			return fmt.Errorf("General Ledger row is invalid")
		}
		current := account
		if len(row.Header.ColData) > 0 && row.Header.ColData[0].ID != "" {
			current = row.Header.ColData[0].ID
		}
		if accountColumn && len(row.ColData) > 0 && row.ColData[0].ID != "" {
			current = row.ColData[0].ID
		}
		if err := fn(location, current, raw, row); err != nil {
			return err
		}
		if err := walkLedger(row.Rows.Row, location+".Rows", current, accountColumn, fn); err != nil {
			return err
		}
	}
	return nil
}

type Activity struct {
	Name   string
	Amount *big.Rat
}

// MonthlyActivity reads each account's amount from a single-period Profit and Loss report; section summaries are not accounts.
func MonthlyActivity(report json.RawMessage) (string, map[string]Activity, error) {
	var root struct {
		Header  struct{ Currency string }
		Columns struct {
			Column []struct{ ColTitle, ColType string }
		}
		Rows struct{ Row []json.RawMessage }
	}
	if json.Unmarshal(report, &root) != nil || len(root.Columns.Column) != 2 || !strings.EqualFold(strings.TrimSpace(root.Columns.Column[1].ColTitle), "Total") && root.Columns.Column[1].ColType != "Money" {
		return "", nil, fmt.Errorf("Profit and Loss report is not a single account and total column")
	}
	out := map[string]Activity{}
	var visit func([]json.RawMessage) error
	visit = func(rows []json.RawMessage) error {
		for _, raw := range rows {
			var row ledgerRow
			if json.Unmarshal(raw, &row) != nil {
				return fmt.Errorf("Profit and Loss row is invalid")
			}
			if row.Type == "Data" && len(row.ColData) > 0 && row.ColData[0].ID != "" {
				if len(row.ColData) != len(root.Columns.Column) {
					return fmt.Errorf("Profit and Loss row for %s does not match its columns", row.ColData[0].Value)
				}
				if _, seen := out[row.ColData[0].ID]; seen {
					return fmt.Errorf("Profit and Loss lists %s twice", row.ColData[0].Value)
				}
				amount, err := ledger.Net(row.ColData[len(row.ColData)-1].Value, "")
				if err != nil {
					return fmt.Errorf("Profit and Loss %v for %s", err, row.ColData[0].Value)
				}
				out[row.ColData[0].ID] = Activity{row.ColData[0].Value, amount}
			}
			if err := visit(row.Rows.Row); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root.Rows.Row); err != nil {
		return "", nil, err
	}
	return root.Header.Currency, out, nil
}

type Payees struct {
	ByName       map[string]*big.Rat
	Unattributed *big.Rat
	Unnamed      int
}

// PayeeActivity sums each wanted account's General Ledger transaction rows by the Name column; rows without a name are unattributed.
func PayeeActivity(report json.RawMessage, accounts []string) (map[string]Payees, error) {
	var root struct {
		Columns struct{ Column []struct{ ColTitle string } }
		Rows    struct{ Row []json.RawMessage }
	}
	if json.Unmarshal(report, &root) != nil {
		return nil, fmt.Errorf("General Ledger report is invalid")
	}
	name, amount := -1, -1
	for i, column := range root.Columns.Column {
		switch strings.ToLower(strings.TrimSpace(column.ColTitle)) {
		case "name":
			name = i
		case "amount":
			amount = i
		}
	}
	if name < 0 || amount < 0 {
		return nil, fmt.Errorf("General Ledger has no Name and Amount columns")
	}
	accountColumn := len(root.Columns.Column) > 0 && strings.Contains(strings.ToLower(root.Columns.Column[0].ColTitle), "account")
	out := map[string]Payees{}
	for _, id := range accounts {
		out[id] = Payees{map[string]*big.Rat{}, new(big.Rat), 0}
	}
	err := walkLedger(root.Rows.Row, "", "", accountColumn, func(_, account string, _ json.RawMessage, row ledgerRow) error {
		payees, wanted := out[account]
		if !wanted || row.Type != "Data" || len(row.ColData) <= max(name, amount) || strings.TrimSpace(row.ColData[amount].Value) == "" {
			return nil
		}
		value, err := ledger.Net(row.ColData[amount].Value, "")
		if err != nil {
			return fmt.Errorf("General Ledger %v", err)
		}
		who := strings.TrimSpace(row.ColData[name].Value)
		if who == "" {
			payees.Unattributed.Add(payees.Unattributed, value)
			payees.Unnamed++
			out[account] = payees
			return nil
		}
		if payees.ByName[who] == nil {
			payees.ByName[who] = new(big.Rat)
		}
		payees.ByName[who].Add(payees.ByName[who], value)
		return nil
	})
	return out, err
}

// ReadSnapshot reads accounts and, for a period, month-end reports plus the evidence to cite (an index above 96 KiB).
func ReadSnapshot(ctx context.Context, env, realm, token, company, period string, started time.Time) (map[string]any, []byte, error) {
	name, err := CompanyFor(ctx, env, realm, token)
	if err != nil {
		return nil, nil, err
	}
	if name != company {
		return nil, nil, fmt.Errorf("Company identity changed. Disconnect and confirm the company again.")
	}
	accounts, err := Accounts(ctx, env, realm, token)
	if err != nil {
		return nil, nil, err
	}
	snapshot := map[string]any{"environment": env, "realm": realm, "company_name": company, "accounts": accounts, "started_at": started, "completed_at": time.Now(), "scope": "Account records only; current balances are not a period-end ledger or atomic read."}
	if period == "" {
		return snapshot, nil, nil
	}
	reports, err := PeriodReports(ctx, env, realm, token, period)
	if err != nil {
		return nil, nil, err
	}
	coverage, err := Coverage(accounts, reports)
	if err != nil {
		return nil, nil, fmt.Errorf("Account coverage could not be read.")
	}
	snapshot["period"], snapshot["basis"], snapshot["reports"], snapshot["coverage"] = period, "Accrual", reports, coverage
	snapshot["completed_at"] = time.Now()
	snapshot["scope"] = "Sequential account and period report reads, not an atomic or independently verified complete ledger. TransactionList contains reported transactions, not full transaction entities. Account CurrentBalance is current, not month-end. Coverage is proposed evidence needs, never approval or completeness."
	full, err := json.Marshal(snapshot)
	// The optional Statement of Cash Flows is left out rather than refuse a capture the required reports fit.
	if err == nil && len(full) > 18*1024*1024 && reports["CashFlow"] != nil {
		delete(reports, "CashFlow")
		full, err = json.Marshal(snapshot)
	}
	if err != nil || len(full) > 18*1024*1024 {
		return nil, nil, fmt.Errorf("Period reports exceed the 18 MiB supported snapshot size. No partial ledger was saved.")
	}
	if len(full) <= 96*1024 {
		return snapshot, full, nil
	}
	// Report and account digests tie the index to this read, so two captures of equal sizes stay distinct evidence.
	directory, _ := json.Marshal(accounts)
	index := map[string]any{"accounts_digest": fmt.Sprintf("%x", sha256.Sum256(directory)), "environment": env, "realm": realm, "company_name": company, "period": period, "basis": "Accrual", "coverage_item_count": len(coverage), "report_sizes": map[string]int{}, "report_digests": map[string]string{}, "scope": "Full reports are stored in the immutable snapshot, not this index. Account-scoped ledger pages must be selected separately; no complete ledger or balance is certified."}
	for name, report := range reports {
		index["report_sizes"].(map[string]int)[name] = len(report)
		index["report_digests"].(map[string]string)[name] = fmt.Sprintf("%x", sha256.Sum256(report))
	}
	evidence, err := json.Marshal(index)
	if err != nil {
		return nil, nil, fmt.Errorf("Could not prepare the ledger evidence index.")
	}
	return snapshot, evidence, nil
}

// LedgerLines is one bank account's month from the captured General Ledger as a reconciliation's ledger side: its rows as
// id,date,amount, and its opening and closing balances. It is refused unless every row is dated in the month and the
// ledger proves itself against the Trial Balance: the beginning balance plus the rows equals the account's Trial
// Balance balance, and so does the last running balance. Otherwise the ledger is exported by hand.
func LedgerLines(accounts, generalLedger, trialBalance json.RawMessage, accountID, period string) (csv, opening, closing string, err error) {
	refuse := func(why string) (string, string, string, error) {
		return "", "", "", fmt.Errorf("The captured General Ledger cannot stand in for the ledger export: %s. Export the ledger by hand", why)
	}
	month, e := time.Parse("2006-01", period)
	if e != nil {
		return refuse("the close month is invalid")
	}
	currency, balances, e := TrialBalance(accounts, trialBalance)
	if e != nil {
		return refuse(e.Error())
	}
	var directory []struct {
		ID          string `json:"Id"`
		CurrencyRef struct{ Value string }
	}
	json.Unmarshal(accounts, &directory)
	for _, a := range directory {
		if a.ID == accountID && (currency == "" || a.CurrencyRef.Value != currency) {
			return refuse("the account's currency is not shown to be the books' currency")
		}
	}
	var tb *big.Rat
	for _, b := range balances {
		if b.ID == accountID && b.AccountType == "Bank" {
			tb = b.Net
		}
	}
	if tb == nil {
		return refuse("the account is not a bank account in this capture's Trial Balance")
	}
	var root struct {
		Columns struct{ Column []struct{ ColTitle string } }
		Rows    struct{ Row []json.RawMessage }
	}
	if json.Unmarshal(generalLedger, &root) != nil {
		return refuse("the report is invalid")
	}
	at := map[string]int{}
	for i, column := range root.Columns.Column {
		at[strings.ToLower(strings.TrimSpace(column.ColTitle))] = i + 1
	}
	date, amount, running := at["date"]-1, at["amount"]-1, at["balance"]-1
	if date < 0 || amount < 0 || running < 0 {
		return refuse("it has no Date, Amount and Balance columns")
	}
	parse := func(v string) (*big.Rat, bool) {
		return new(big.Rat).SetString(strings.ReplaceAll(strings.TrimSpace(v), ",", ""))
	}
	var begin, last *big.Rat
	sum, lines := new(big.Rat), []string{"id,date,amount"}
	accountColumn := len(root.Columns.Column) > 0 && strings.Contains(strings.ToLower(root.Columns.Column[0].ColTitle), "account")
	walkErr := walkLedger(root.Rows.Row, "reports.GeneralLedger.Rows", "", accountColumn, func(_, account string, _ json.RawMessage, row ledgerRow) error {
		if account != accountID || row.Type != "Data" {
			return nil
		}
		if len(row.ColData) <= max(date, amount, running) {
			return fmt.Errorf("a row of the account is incomplete")
		}
		if slices.ContainsFunc(row.ColData, func(c ledgerCell) bool { return strings.EqualFold(strings.TrimSpace(c.Value), "Beginning Balance") }) {
			value, ok := parse(row.ColData[running].Value)
			if !ok {
				return fmt.Errorf("the beginning balance is unreadable")
			}
			begin = value
			return nil
		}
		// A row with no amount (a memo line) moves nothing; any other row must be a readable transaction of the month.
		if strings.TrimSpace(row.ColData[amount].Value) == "" {
			return nil
		}
		value, ok := parse(row.ColData[amount].Value)
		balance, readable := parse(row.ColData[running].Value)
		if !ok || !readable {
			return fmt.Errorf("a row's amount or running balance is unreadable")
		}
		when := strings.TrimSpace(row.ColData[date].Value)
		on, e := time.Parse("2006-01-02", when)
		if e != nil || on.Before(month) || !on.Before(month.AddDate(0, 1, 0)) {
			return fmt.Errorf("a row is dated %q, outside %s", when, period)
		}
		last = balance
		sum.Add(sum, value)
		lines = append(lines, fmt.Sprintf("GL%d,%s,%s", len(lines), when, value.FloatString(2)))
		return nil
	})
	if walkErr != nil {
		return refuse(walkErr.Error())
	}
	if begin == nil {
		begin = new(big.Rat)
	}
	if new(big.Rat).Add(begin, sum).Cmp(tb) != 0 {
		return refuse(fmt.Sprintf("its beginning balance %s plus its rows %s is not the Trial Balance balance %s", begin.FloatString(2), sum.FloatString(2), tb.FloatString(2)))
	}
	if len(lines) > 1 && (last == nil || last.Cmp(tb) != 0) {
		return refuse("its last running balance is not the Trial Balance balance")
	}
	return strings.Join(lines, "\n") + "\n", begin.FloatString(2), tb.FloatString(2), nil
}
