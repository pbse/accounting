package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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
	for _, kind := range []string{"Invoice", "Bill"} {
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
	return rows, nil
}
