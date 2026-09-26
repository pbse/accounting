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
	openBillsFail := false
	changingCount := false
	countReads := 0
	unreadable := false
	hosts := map[string]bool{}
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		hosts[req.URL.Host] = true
		body := `{ "Preferences": {"Id":"1","SyncToken":"3"} }`
		if req.URL.Path != "/v3/company/123/preferences" {
			query, _ := url.QueryUnescape(req.URL.Query().Get("query"))
			if openBillsFail && strings.Contains(query, "FROM Bill WHERE Balance") {
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"Fault":{}}`)), Header: make(http.Header)}, nil
			}
			var kind string
			if strings.HasPrefix(query, "SELECT COUNT(*)") {
				fmt.Sscanf(strings.TrimPrefix(query, "SELECT COUNT(*) FROM "), "%s", &kind)
				count := 0
				if kind == "Payment" && strings.Contains(query, "TxnDate >= '2025-10-01'") {
					count = 2
				}
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
			if strings.Contains(query, "UnappliedAmt") {
				t.Fatal("unapplied amounts are not a documented filter")
			}
			if kind == "Payment" && strings.Contains(query, "TxnDate >= '2025-10-01'") {
				page = []map[string]any{{"Id": "p1", "TxnDate": "2026-08-01", "UnappliedAmt": 0}, {"Id": "p2", "TxnDate": "2026-09-15", "UnappliedAmt": 500}}
				if unreadable {
					delete(page[1], "UnappliedAmt")
				}
			}
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
	capture, err := PeriodEntities(context.Background(), "sandbox", "123", "synthetic", "2026-09")
	if err != nil || capture.Counts["Bill"] != 502 || len(capture.Records["Bill"]) != 502 || capture.Limits["records_per_type"] != 2500 || len(capture.OpenItems["Bill"]) != 502 || len(capture.OpenItems["Invoice"]) != 0 || len(capture.OpenItems["Payment"]) != 1 {
		t.Fatalf("complete paged capture: count=%d err=%v", capture.Counts["Bill"], err)
	}
	hosts = map[string]bool{}
	if _, err = PeriodEntities(context.Background(), "production", "123", "synthetic", "2026-09"); err != nil || len(hosts) != 1 || !hosts["quickbooks.api.intuit.com"] {
		t.Fatalf("a production company is read only from the production host: %v %v", hosts, err)
	}
	unreadable = true
	capture, err = PeriodEntities(context.Background(), "sandbox", "123", "synthetic", "2026-09")
	unreadable = false
	if err != nil || capture.OpenItems["Payment"] != nil || !strings.Contains(capture.Completeness, "Open Payment UnappliedAmt is unreadable") {
		t.Fatalf("a payment without a readable unapplied amount is named, not treated as applied: %v %q", err, capture.Completeness)
	}
	openBillsFail = true
	capture, err = PeriodEntities(context.Background(), "sandbox", "123", "synthetic", "2026-09")
	openBillsFail = false
	if err != nil || len(capture.Records["Bill"]) != 502 || capture.OpenItems["Bill"] != nil || !strings.Contains(capture.Completeness, "Open Bill") {
		t.Fatalf("an unreadable open-item query must not fail the transaction capture: %v %q", err, capture.Completeness)
	}
	duplicate = true
	if _, err := PeriodEntities(context.Background(), "sandbox", "123", "synthetic", "2026-09"); err == nil || !strings.Contains(err.Error(), "revision or period") {
		t.Fatalf("duplicate provider ID must block complete capture: %v", err)
	}
	duplicate = false
	changingCount = true
	countReads = 0
	if _, err := PeriodEntities(context.Background(), "sandbox", "123", "synthetic", "2026-09"); err == nil || !strings.Contains(err.Error(), "count changed") {
		t.Fatalf("changed provider count must block capture: %v", err)
	}
}

func TestCertifyComparesTheReportWithTheCaptureTransactionByTransaction(t *testing.T) {
	row := func(kind, id, noPost string) string {
		return `{"type":"Data","ColData":[{"value":"2026-09-02"},{"value":"` + kind + `","id":"` + id + `"},{"value":"` + noPost + `"}]}`
	}
	report := func(rows ...string) json.RawMessage {
		return json.RawMessage(`{"Columns":{"Column":[{"ColType":"Date","MetaData":[{"Name":"ColKey","Value":"tx_date"}]},{"ColType":"String","MetaData":[{"Name":"ColKey","Value":"txn_type"}]},{"ColType":"String","MetaData":[{"Name":"ColKey","Value":"is_no_post"}]}]},"Rows":{"Row":[` + strings.Join(rows, ",") + `]}}`)
	}
	counts := map[string]int{}
	for _, kind := range periodEntityTypes {
		counts[kind] = 0
	}
	counts["Bill"], counts["Purchase"], counts["JournalEntry"] = 1, 2, 1
	captured := map[string][]string{"Bill": {"41"}, "Purchase": {"52", "53"}, "JournalEntry": {"7"}}
	// A section of rows nests like any report, and an estimate is not posting.
	agreed := report(row("Bill", "41", "No"), `{"type":"Section","Rows":{"Row":[`+row("Expense", "52", "No")+`,`+row("Credit Card Credit", "53", "No")+`]}}`, row("General Journal", "7", "No"), row("Estimate", "9", "Yes"))
	if c, err := Certify(agreed, counts, captured); err != nil || !c.Agrees || c.Reported != 4 || c.Captured != 4 || c.NonPosting != 1 {
		t.Fatalf("the report and the capture agree: %+v %v", c, err)
	}
	if c, _ := Certify(report(row("Bill", "41", "No"), row("Expense", "52", "No"), row("Expense", "53", "No"), row("Journal Entry", "7", "No"), row("Invoice", "130", "No")), counts, captured); c.Agrees || strings.Join(c.Missing, ",") != "Invoice 130" {
		t.Fatalf("a reported transaction the capture lacks is missing: %+v", c)
	}
	if c, _ := Certify(report(row("Bill", "41", "No"), row("Expense", "52", "No"), row("Journal Entry", "7", "No")), counts, captured); c.Agrees || strings.Join(c.Extra, ",") != "Purchase 53" {
		t.Fatalf("a captured transaction the report lacks is extra: %+v", c)
	}
	if c, _ := Certify(report(row("Bill", "41", "No"), row("Expense", "52", "No"), row("Expense", "53", "No"), row("Journal Entry", "7", "No"), row("Inventory Qty Adjust", "3", "No")), counts, captured); c.Agrees || strings.Join(c.Unsupported, ",") != "Inventory Qty Adjust 3" {
		t.Fatalf("a posting type the capture does not read keeps the ledger uncertified: %+v", c)
	}
	for name, bad := range map[string]json.RawMessage{
		"no ID":    report(`{"type":"Data","ColData":[{"value":"2026-09-02"},{"value":"Bill"},{"value":"No"}]}`),
		"repeated": report(row("Bill", "41", "No"), row("Bill", "41", "No")),
		"no type":  json.RawMessage(`{"Columns":{"Column":[{"ColType":"Date"}]},"Rows":{"Row":[]}}`),
	} {
		if _, err := Certify(bad, counts, captured); err == nil {
			t.Fatalf("%s: an unreadable report is refused, not certified", name)
		}
	}
	delete(counts, "Transfer")
	if _, err := Certify(agreed, counts, captured); err == nil {
		t.Fatal("a capture without every type's count is refused")
	}
	counts["Transfer"], counts["Bill"] = 0, 2
	if _, err := Certify(agreed, counts, captured); err == nil {
		t.Fatal("a capture whose rows do not match its counts is refused")
	}
}
