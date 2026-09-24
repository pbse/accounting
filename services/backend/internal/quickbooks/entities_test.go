package quickbooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPeriodEntitiesRequiresCompletePagedRevisions(t *testing.T) {
	prior := http.DefaultTransport
	defer func() { http.DefaultTransport = prior }()
	duplicate := false
	changingCount := false
	countReads := 0
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{ "Preferences": {"Id":"1","SyncToken":"3"} }`
		if req.URL.Path != "/v3/company/123/preferences" {
			query, _ := url.QueryUnescape(req.URL.Query().Get("query"))
			var kind string
			if strings.HasPrefix(query, "SELECT COUNT(*)") {
				fmt.Sscanf(strings.TrimPrefix(query, "SELECT COUNT(*) FROM "), "%s", &kind)
				count := 0
				if kind == "Bill" {
					count = 502
					countReads++
					if changingCount && countReads == 2 {
						count++
					}
				}
				body = fmt.Sprintf(`{"QueryResponse":{"totalCount":%d}}`, count)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			}
			fmt.Sscanf(strings.TrimPrefix(query, "SELECT * FROM "), "%s", &kind)
			kind = strings.TrimSpace(kind)
			page := []map[string]any{}
			start := 1
			if strings.Contains(query, "STARTPOSITION 501") {
				start = 501
			}
			if kind == "Bill" {
				count := 500
				if start == 501 {
					count = 2
				}
				for i := 0; i < count; i++ {
					id := start + i
					if duplicate && start == 501 && i == 0 {
						id = 1
					}
					page = append(page, map[string]any{"Id": fmt.Sprint(id), "SyncToken": "2", "TxnDate": "2026-09-05", "MetaData": map[string]string{"LastUpdatedTime": "2026-09-06T00:00:00Z"}})
				}
			}
			encoded, _ := json.Marshal(map[string]any{"QueryResponse": map[string]any{kind: page, "startPosition": start, "maxResults": len(page)}})
			body = string(encoded)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	capture, err := PeriodEntities(context.Background(), "123", "synthetic", "2026-09")
	if err != nil || capture.Counts["Bill"] != 502 || len(capture.Records["Bill"]) != 502 || capture.Limits["records_per_type"] != 2500 || len(capture.OpenItems["Bill"]) != 502 || len(capture.OpenItems["Invoice"]) != 0 {
		t.Fatalf("complete paged capture: count=%d err=%v", capture.Counts["Bill"], err)
	}
	duplicate = true
	if _, err := PeriodEntities(context.Background(), "123", "synthetic", "2026-09"); err == nil || !strings.Contains(err.Error(), "revision or period") {
		t.Fatalf("duplicate provider ID must block complete capture: %v", err)
	}
	duplicate = false
	changingCount = true
	countReads = 0
	if _, err := PeriodEntities(context.Background(), "123", "synthetic", "2026-09"); err == nil || !strings.Contains(err.Error(), "count changed") {
		t.Fatalf("changed provider count must block capture: %v", err)
	}
}
