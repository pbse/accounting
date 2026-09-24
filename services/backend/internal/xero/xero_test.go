package xero

import (
	"encoding/json"
	"testing"
)

func TestBalancesJoinReportRowsToAccountsByID(t *testing.T) {
	cell := func(v string) map[string]any { return map[string]any{"Value": v} }
	account := func(label, id string, values ...string) map[string]any {
		cells := []any{map[string]any{"Value": label, "Attributes": []any{map[string]any{"Value": id, "Id": "account"}}}}
		for _, v := range values {
			cells = append(cells, cell(v))
		}
		return map[string]any{"RowType": "Row", "Cells": cells}
	}
	report := map[string]any{"Reports": []any{map[string]any{"ReportType": "TrialBalance", "Rows": []any{
		map[string]any{"RowType": "Header", "Cells": []any{cell("Account"), cell("Debit"), cell("Credit"), cell("YTD Debit"), cell("YTD Credit")}},
		map[string]any{"RowType": "Section", "Title": "Revenue", "Rows": []any{account("Sales (200)", "a-200", "", "900.00", "", "8100.00")}},
		map[string]any{"RowType": "Section", "Title": "Assets", "Rows": []any{
			account("Business Bank Account (090)", "a-090", "50.00", "", "12500.00", ""),
			account("Renamed in report", "a-610", "", "", "", "40.00"),
			account("Deleted since", "a-999", "", "", "5.00", ""),
			map[string]any{"RowType": "SummaryRow", "Cells": []any{cell("Total"), cell("50.00"), cell("900.00"), cell("12505.00"), cell("8140.00")}},
		}},
	}}}}
	raw, _ := json.Marshal(report)
	payload, _ := json.Marshal(map[string]any{"date": "2026-09-30", "organisation": map[string]any{"Organisations": []any{map[string]any{"BaseCurrency": "NZD"}}},
		"accounts": map[string]any{"Accounts": []any{
			map[string]any{"AccountID": "a-090", "Code": "090", "Name": "Business Bank Account", "Class": "ASSET"},
			map[string]any{"AccountID": "a-200", "Code": "200", "Name": "Sales", "Class": "REVENUE"},
			map[string]any{"AccountID": "a-610", "Code": "610", "Name": "Accounts Receivable", "Class": "ASSET"},
		}}, "trial_balance": json.RawMessage(raw)})
	currency, date, rows, err := Balances(payload)
	if err != nil || currency != "NZD" || date != "2026-09-30" || len(rows) != 4 {
		t.Fatalf("four account rows with the report currency and date: %v %s %s %+v", err, currency, date, rows)
	}
	if rows[0].Code != "200" || rows[0].Class != "REVENUE" || rows[0].YTDCredit != "8100.00" || rows[0].Credit != "900.00" {
		t.Fatalf("columns follow the header titles: %+v", rows[0])
	}
	if rows[2].Name != "Accounts Receivable" || rows[2].Code != "610" {
		t.Fatalf("identity comes from the account ID, not the report label: %+v", rows[2])
	}
	if rows[3].Code != "" || rows[3].Class != "" || rows[3].Name != "Deleted since" {
		t.Fatalf("an account missing from Accounts keeps its report label and no class: %+v", rows[3])
	}
}
