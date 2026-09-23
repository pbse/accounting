package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

var periodEntityTypes = []string{"Bill", "BillPayment", "CreditMemo", "Deposit", "Invoice", "JournalEntry", "Payment", "Purchase", "RefundReceipt", "SalesReceipt", "Transfer", "VendorCredit"}

type EntityCapture struct {
	Types    []string                     `json:"types"`
	Counts   map[string]int               `json:"counts"`
	Records  map[string][]json.RawMessage `json:"records"`
	Settings json.RawMessage              `json:"settings"`
}

func PeriodEntities(ctx context.Context, realm, token, period string) (EntityCapture, error) {
	result := EntityCapture{Types: periodEntityTypes, Counts: map[string]int{}, Records: map[string][]json.RawMessage{}}
	start, err := time.Parse("2006-01", period)
	if err != nil || start.Format("2006-01") != period {
		return result, fmt.Errorf("Invalid close month")
	}
	first, last := start.Format("2006-01-02"), start.AddDate(0, 1, -1).Format("2006-01-02")
	raw, err := readSandboxLimit(ctx, realm, token, "/preferences", 2*1024*1024)
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
		seen := map[string]bool{}
		rows := []json.RawMessage{}
		for position := 1; position <= 2501; position += 500 {
			pageSize := 500
			if position == 2501 {
				pageSize = 1
			}
			query := fmt.Sprintf("SELECT * FROM %s WHERE TxnDate >= '%s' AND TxnDate <= '%s' ORDERBY Id STARTPOSITION %d MAXRESULTS %d", kind, first, last, position, pageSize)
			raw, err := readSandboxLimit(ctx, realm, token, "/query?query="+url.QueryEscape(query), 8*1024*1024)
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
				seen[item.ID] = true
				rows = append(rows, record)
			}
			if len(page) < pageSize {
				break
			}
		}
		result.Records[kind], result.Counts[kind] = rows, len(rows)
	}
	return result, nil
}
