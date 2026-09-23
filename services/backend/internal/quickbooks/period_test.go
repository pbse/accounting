package quickbooks

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSelectLedgerKeepsExactAccountAndPages(t *testing.T) {
	accounts := json.RawMessage(`[{
"Id":"7"},{"Id":"8"}]`)
	rows := []string{`{"type":"Data","ColData":[{"id":"8","value":"Other"},{"value":"999.00"}]}`}
	for n := 0; n < 25; n++ {
		rows = append(rows, fmt.Sprintf(`{"type":"Data","ColData":[{"id":"7","value":"Prepaid"},{"value":"9007199254740993.%02d"}]}`, n))
	}
	report := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Account"}]},"Rows":{"Row":[` + strings.Join(rows, ",") + `]}}`)
	first, err := SelectLedger(accounts, report, "7", 0)
	if err != nil || first.Total != 25 || len(first.Rows) != 20 || strings.Contains(string(first.Rows[0].Row), "999.00") || !strings.Contains(string(first.Rows[0].Row), "9007199254740993.00") {
		t.Fatalf("first page: %+v %v", first, err)
	}
	last, err := SelectLedger(accounts, report, "7", 20)
	if err != nil || len(last.Rows) != 5 || last.Rows[0].Location != "reports.GeneralLedger.Rows.Row[21]" {
		t.Fatalf("last page: %+v %v", last, err)
	}
	if _, err := SelectLedger(accounts, report, "9", 0); err == nil {
		t.Fatal("unknown account was accepted")
	}
	unsafe := json.RawMessage(strings.Replace(string(report), `"ColTitle":"Account"`, `"ColTitle":"Date"`, 1))
	page, err := SelectLedger(accounts, unsafe, "7", 0)
	if err != nil || page.Total != 0 {
		t.Fatalf("transaction ID was mistaken for an account ID: %+v %v", page, err)
	}
	grouped := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Date"}]},"Rows":{"Row":[{"Header":{"ColData":[{"id":"7","value":"Prepaid"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-01"}]},{"Header":{"ColData":[{"id":"8","value":"Other"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":"2026-09-02"}]}]}}]}}]}}`)
	page, err = SelectLedger(accounts, grouped, "7", 0)
	if err != nil || page.Total != 2 || strings.Contains(string(page.Rows[1].Row), "2026-09-02") {
		t.Fatalf("nested foreign account was selected: %+v %v", page, err)
	}
}

func TestSearchLedgerKeepsAccountAndOriginalLocations(t *testing.T) {
	accounts := json.RawMessage(`[{"Id":"7"},{"Id":"8"}]`)
	report := json.RawMessage(`{"Columns":{"Column":[{"ColTitle":"Account"}]},"Rows":{"Row":[{"ColData":[{"id":"8","value":"INV-42"}]},{"ColData":[{"id":"7","value":"other"}]},{"ColData":[{"id":"7","value":"INV-42"}]}]}}`)
	p, err := SearchLedger(accounts, report, "7", "inv-42", 0)
	if err != nil || p.Total != 1 || len(p.Rows) != 1 || p.Rows[0].Location != "reports.GeneralLedger.Rows.Row[2]" {
		t.Fatalf("unsafe search result: %+v %v", p, err)
	}
	if _, err = SearchLedger(accounts, report, "7", "x", 0); err == nil {
		t.Fatal("unbounded query accepted")
	}
	p, err = SearchLedger(accounts, report, "7", "value", 0)
	if err != nil || p.Total != 0 {
		t.Fatalf("JSON field names must not become search matches: %+v %v", p, err)
	}
}
