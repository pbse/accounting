package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"slices"
	"strings"
	"time"
)

var periodEntityTypes = []string{"Bill", "BillPayment", "CreditMemo", "Deposit", "Invoice", "JournalEntry", "Payment", "Purchase", "RefundReceipt", "SalesReceipt", "Transfer", "VendorCredit"}

type EntityCapture struct {
	Realm        string                       `json:"realm"`
	Period       string                       `json:"period"`
	Types        []string                     `json:"types"`
	Counts       map[string]int               `json:"counts"`
	Limits       map[string]int               `json:"limits"`
	Records      map[string][]json.RawMessage `json:"records"`
	Settings     json.RawMessage              `json:"settings"`
	StartedAt    time.Time                    `json:"started_at"`
	FinishedAt   time.Time                    `json:"finished_at"`
	Completeness string                       `json:"completeness"`
	// Invoices and bills still open at capture time, dated on or before month-end. Their balances
	// change with later payments, so they are kept out of the transaction evidence digest.
	OpenItems map[string][]json.RawMessage `json:"open_items,omitempty"`
}

func periodCount(ctx context.Context, env, realm, token, kind, where string) (int, error) {
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s %s", kind, where)
	raw, err := read(ctx, env, realm, token, "/query?query="+url.QueryEscape(query), 1024*1024)
	if err != nil {
		return 0, err
	}
	var response struct {
		QueryResponse struct {
			TotalCount *int `json:"totalCount"`
		} `json:"QueryResponse"`
	}
	if json.Unmarshal(raw, &response) != nil || response.QueryResponse.TotalCount == nil || *response.QueryResponse.TotalCount < 0 {
		return 0, fmt.Errorf("%s transaction count is unavailable", kind)
	}
	return *response.QueryResponse.TotalCount, nil
}

func PeriodEntities(ctx context.Context, env, realm, token, period string) (EntityCapture, error) {
	result := EntityCapture{Realm: realm, Period: period, Types: periodEntityTypes, Counts: map[string]int{}, Limits: map[string]int{"records_per_type": 2500, "page_size": 500, "capture_bytes": 12 * 1024 * 1024}, Records: map[string][]json.RawMessage{}}
	start, err := time.Parse("2006-01", period)
	if err != nil || start.Format("2006-01") != period {
		return result, fmt.Errorf("Invalid close month")
	}
	first, last := start.Format("2006-01-02"), start.AddDate(0, 1, -1).Format("2006-01-02")
	result.StartedAt = time.Now().UTC()
	raw, err := read(ctx, env, realm, token, "/preferences", 2*1024*1024)
	if err != nil {
		return result, fmt.Errorf("Company settings: %w", err)
	}
	var settings struct {
		Preferences struct {
			ID        string `json:"Id"`
			SyncToken string
		}
	}
	if json.Unmarshal(raw, &settings) != nil || settings.Preferences.ID == "" || settings.Preferences.SyncToken == "" {
		return result, fmt.Errorf("Company settings identity or revision is unavailable")
	}
	result.Settings = raw
	for _, kind := range periodEntityTypes {
		before, err := periodCount(ctx, env, realm, token, kind, fmt.Sprintf("WHERE TxnDate >= '%s' AND TxnDate <= '%s'", first, last))
		if err != nil {
			return result, fmt.Errorf("%s transaction count: %w", kind, err)
		}
		if before > 2500 {
			return result, fmt.Errorf("%s exceeds the 2500-record period limit", kind)
		}
		seen := map[string]bool{}
		rows := []json.RawMessage{}
		for position := 1; position <= 2501; position += 500 {
			pageSize := 500
			if position == 2501 {
				pageSize = 1
			}
			query := fmt.Sprintf("SELECT * FROM %s WHERE TxnDate >= '%s' AND TxnDate <= '%s' ORDERBY Id STARTPOSITION %d MAXRESULTS %d", kind, first, last, position, pageSize)
			raw, err := read(ctx, env, realm, token, "/query?query="+url.QueryEscape(query), 8*1024*1024)
			if err != nil {
				return result, fmt.Errorf("%s transaction page %d: %w", kind, position, err)
			}
			var response struct{ QueryResponse map[string]json.RawMessage }
			if json.Unmarshal(raw, &response) != nil || response.QueryResponse == nil {
				return result, fmt.Errorf("%s transaction page is missing", kind)
			}
			var page []json.RawMessage
			if entity := response.QueryResponse[kind]; len(entity) > 0 && json.Unmarshal(entity, &page) != nil {
				return result, fmt.Errorf("%s transaction rows are invalid", kind)
			}
			var actualPosition, reported int
			json.Unmarshal(response.QueryResponse["startPosition"], &actualPosition)
			json.Unmarshal(response.QueryResponse["maxResults"], &reported)
			if len(page) > pageSize || (len(page) > 0 && (actualPosition != position || reported != len(page))) {
				return result, fmt.Errorf("%s transaction pagination is incomplete", kind)
			}
			if position == 2501 && len(page) > 0 {
				return result, fmt.Errorf("%s exceeds the 2500-record period limit", kind)
			}
			for _, record := range page {
				var item struct {
					ID                 string `json:"Id"`
					SyncToken, TxnDate string
					MetaData           struct{ LastUpdatedTime string }
				}
				if json.Unmarshal(record, &item) != nil || item.ID == "" || item.SyncToken == "" || item.TxnDate < first || item.TxnDate > last || item.MetaData.LastUpdatedTime == "" || seen[item.ID] {
					return result, fmt.Errorf("%s transaction identity, revision or period is inconsistent", kind)
				}
				if date, dateErr := time.Parse("2006-01-02", item.TxnDate); dateErr != nil || date.Format("2006-01-02") != item.TxnDate {
					return result, fmt.Errorf("%s transaction date is invalid", kind)
				}
				if _, updatedErr := time.Parse(time.RFC3339Nano, item.MetaData.LastUpdatedTime); updatedErr != nil {
					return result, fmt.Errorf("%s transaction update time is invalid", kind)
				}
				seen[item.ID] = true
				rows = append(rows, record)
			}
			if len(page) < pageSize {
				break
			}
		}
		result.Records[kind], result.Counts[kind] = rows, len(rows)
		after, err := periodCount(ctx, env, realm, token, kind, fmt.Sprintf("WHERE TxnDate >= '%s' AND TxnDate <= '%s'", first, last))
		if err != nil || before != len(rows) || after != before {
			return result, fmt.Errorf("%s transaction count changed or pagination was incomplete; retry the capture", kind)
		}
	}
	// Open items only add questions; if a read fails, the transaction capture still completes and says so.
	result.OpenItems = map[string][]json.RawMessage{}
	unread := []string{}
	for _, kind := range []string{"Invoice", "Bill", "Payment", "CreditMemo", "VendorCredit"} {
		items, err := openItems(ctx, env, realm, token, kind, last)
		if err != nil {
			unread = append(unread, err.Error())
			continue
		}
		result.OpenItems[kind] = items
	}
	check, err := read(ctx, env, realm, token, "/preferences", 2*1024*1024)
	if err != nil {
		return result, fmt.Errorf("Company settings recheck: %w", err)
	}
	var latest struct {
		Preferences struct {
			ID        string `json:"Id"`
			SyncToken string
		}
	}
	if json.Unmarshal(check, &latest) != nil || latest.Preferences.ID != settings.Preferences.ID || latest.Preferences.SyncToken != settings.Preferences.SyncToken {
		return result, fmt.Errorf("Company settings changed during transaction capture; retry")
	}
	result.FinishedAt = time.Now().UTC()
	result.Completeness = "All supported types were paged and provider counts agreed before and after each type. Reads are sequential, not an atomic ledger snapshot; later edits require refresh."
	if len(unread) > 0 {
		result.Completeness += " Open items were not fully read, so their overdue questions are missing: " + strings.Join(unread, "; ") + "."
	}
	return result, nil
}

// openItems pages through a type's records with a balance, dated on or before month-end.
func openItems(ctx context.Context, env, realm, token, kind, last string) ([]json.RawMessage, error) {
	where := fmt.Sprintf("WHERE Balance > '0' AND TxnDate <= '%s'", last)
	// Unused payment and credit amounts are not documented as filterable: read a year and keep positive ones.
	unused := map[string]string{"Payment": "UnappliedAmt", "CreditMemo": "RemainingCredit", "VendorCredit": "Balance"}[kind]
	if unused != "" {
		end, _ := time.Parse("2006-01-02", last)
		where = fmt.Sprintf("WHERE TxnDate >= '%s' AND TxnDate <= '%s'", end.AddDate(-1, 0, 1).Format("2006-01-02"), last)
	}
	before, err := periodCount(ctx, env, realm, token, kind, where)
	if err != nil {
		return nil, fmt.Errorf("Open %s count: %w", kind, err)
	}
	if before > 2500 {
		return nil, fmt.Errorf("Open %s records exceed the 2500-record limit", kind)
	}
	rows, seen := []json.RawMessage{}, map[string]bool{}
	for position := 1; position <= before; position += 500 {
		raw, err := read(ctx, env, realm, token, "/query?query="+url.QueryEscape(fmt.Sprintf("SELECT * FROM %s %s ORDERBY Id STARTPOSITION %d MAXRESULTS 500", kind, where, position)), 8*1024*1024)
		if err != nil {
			return nil, fmt.Errorf("Open %s page %d: %w", kind, position, err)
		}
		var response struct{ QueryResponse map[string]json.RawMessage }
		var page []json.RawMessage
		if json.Unmarshal(raw, &response) != nil || (len(response.QueryResponse[kind]) > 0 && json.Unmarshal(response.QueryResponse[kind], &page) != nil) {
			return nil, fmt.Errorf("Open %s page is invalid", kind)
		}
		for _, record := range page {
			var item struct {
				ID string `json:"Id"`
			}
			if json.Unmarshal(record, &item) != nil || item.ID == "" || seen[item.ID] {
				return nil, fmt.Errorf("Open %s identity is inconsistent", kind)
			}
			seen[item.ID] = true
			rows = append(rows, record)
		}
	}
	if after, err := periodCount(ctx, env, realm, token, kind, where); err != nil || after != before || len(rows) != before {
		return nil, fmt.Errorf("Open %s records changed during capture; retry", kind)
	}
	if unused == "" {
		return rows, nil
	}
	open := []json.RawMessage{}
	for _, record := range rows {
		var fields map[string]json.RawMessage
		json.Unmarshal(record, &fields)
		amount, ok := new(big.Rat).SetString(strings.Trim(string(fields[unused]), `"`))
		if !ok {
			return nil, fmt.Errorf("Open %s %s is unreadable", kind, unused)
		}
		if amount.Sign() > 0 {
			open = append(open, record)
		}
	}
	return open, nil
}

// reportedKinds maps a TransactionList transaction type to the entity the capture reads it as.
var reportedKinds = map[string]string{"Bill": "Bill", "Bill Payment (Check)": "BillPayment", "Bill Payment (Credit Card)": "BillPayment", "Check": "Purchase", "Expense": "Purchase", "Cash Expense": "Purchase", "Credit Card Expense": "Purchase", "Credit Card Credit": "Purchase", "Deposit": "Deposit", "Journal Entry": "JournalEntry", "General Journal": "JournalEntry", "Invoice": "Invoice", "Payment": "Payment", "Sales Receipt": "SalesReceipt", "Refund": "RefundReceipt", "Transfer": "Transfer", "Vendor Credit": "VendorCredit", "Credit Memo": "CreditMemo"}

// Certification is how a period's TransactionList report and the transaction capture compare, transaction by transaction.
type Certification struct {
	Agrees      bool     `json:"agrees"`
	Reported    int      `json:"reported"`
	Captured    int      `json:"captured"`
	NonPosting  int      `json:"non_posting"`
	Missing     []string `json:"missing"`
	Extra       []string `json:"extra"`
	Unsupported []string `json:"unsupported"`
}

// Certify compares the posting transactions a TransactionList report lists with those a capture read, by type and
// QuickBooks ID. counts are the capture's per-type counts and captured its IDs; every captured type must be present.
func Certify(report json.RawMessage, counts map[string]int, captured map[string][]string) (Certification, error) {
	result := Certification{Missing: []string{}, Extra: []string{}, Unsupported: []string{}}
	have := map[string]bool{}
	for kind := range captured {
		if !slices.Contains(periodEntityTypes, kind) {
			return result, fmt.Errorf("The transaction capture has rows of an unread type, %s", kind)
		}
	}
	for _, kind := range periodEntityTypes {
		count, ok := counts[kind]
		if !ok || count != len(captured[kind]) {
			return result, fmt.Errorf("The transaction capture has no complete %s count", kind)
		}
		for _, id := range captured[kind] {
			have[kind+" "+id] = true
		}
	}
	result.Captured = len(have)
	var root struct {
		Columns struct {
			Column []struct {
				ColType  string
				MetaData []struct{ Name, Value string }
			}
		}
		Rows struct{ Row []json.RawMessage }
	}
	if json.Unmarshal(report, &root) != nil {
		return result, fmt.Errorf("TransactionList is unreadable")
	}
	typeColumn, postColumn := -1, -1
	for i, column := range root.Columns.Column {
		key := column.ColType
		for _, m := range column.MetaData {
			if m.Name == "ColKey" {
				key = m.Value
			}
		}
		switch key {
		case "txn_type":
			typeColumn = i
		case "is_no_post":
			postColumn = i
		}
	}
	if typeColumn < 0 {
		return result, fmt.Errorf("TransactionList has no transaction type column")
	}
	reported, seen := map[string]bool{}, map[string]bool{}
	var walk func(rows []json.RawMessage) error
	walk = func(rows []json.RawMessage) error {
		for _, raw := range rows {
			var row struct {
				ColData []struct{ Value, ID string }
				Rows    struct{ Row []json.RawMessage }
			}
			if json.Unmarshal(raw, &row) != nil {
				return fmt.Errorf("TransactionList row is unreadable")
			}
			if err := walk(row.Rows.Row); err != nil {
				return err
			}
			if len(row.ColData) == 0 {
				continue
			}
			if len(row.ColData) <= typeColumn || row.ColData[typeColumn].ID == "" {
				return fmt.Errorf("A TransactionList row has no transaction type or ID")
			}
			cell := row.ColData[typeColumn]
			if seen[cell.Value+" "+cell.ID] {
				return fmt.Errorf("TransactionList lists %s %s more than once", cell.Value, cell.ID)
			}
			seen[cell.Value+" "+cell.ID] = true
			if postColumn >= 0 && postColumn < len(row.ColData) {
				if flag := strings.ToLower(row.ColData[postColumn].Value); flag == "yes" || flag == "true" {
					result.NonPosting++
					continue
				}
			}
			kind, known := reportedKinds[cell.Value]
			if !known {
				result.Unsupported = append(result.Unsupported, cell.Value+" "+cell.ID)
				continue
			}
			key := kind + " " + cell.ID
			if reported[key] {
				return fmt.Errorf("TransactionList lists %s more than once", key)
			}
			reported[key] = true
		}
		return nil
	}
	if err := walk(root.Rows.Row); err != nil {
		return result, err
	}
	result.Reported = len(reported)
	for key := range reported {
		if !have[key] {
			result.Missing = append(result.Missing, key)
		}
	}
	for key := range have {
		if !reported[key] {
			result.Extra = append(result.Extra, key)
		}
	}
	slices.Sort(result.Missing)
	slices.Sort(result.Extra)
	result.Agrees = len(result.Missing)+len(result.Extra)+len(result.Unsupported) == 0
	return result, nil
}
