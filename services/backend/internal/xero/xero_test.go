package xero

import (
	"encoding/json"
	"strings"
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

func TestWrongSideReadsYTDColumnsWithXeroRules(t *testing.T) {
	accounts := []any{}
	rows := []any{}
	add := func(id, name, class, system, ytdDebit, ytdCredit string) {
		if class != "" {
			accounts = append(accounts, map[string]any{"AccountID": id, "Code": id, "Name": name, "Class": class, "SystemAccount": system})
		}
		rows = append(rows, map[string]any{"RowType": "Row", "Cells": []any{
			map[string]any{"Value": name, "Attributes": []any{map[string]any{"Value": id, "Id": "account"}}},
			map[string]any{"Value": ""}, map[string]any{"Value": ""}, map[string]any{"Value": ytdDebit}, map[string]any{"Value": ytdCredit}}})
	}
	add("090", "Business Bank Account", "ASSET", "", "", "1,200.00")
	add("610", "Accounts Receivable", "ASSET", "DEBTORS", "800.00", "")
	add("711", "Less Accumulated Depreciation on Office Equipment", "ASSET", "", "", "300.00")
	add("960", "Retained Earnings", "EQUITY", "RETAINEDEARNINGS", "500.00", "")
	add("880", "Owner A Drawings", "EQUITY", "", "250.00", "")
	add("429", "General Expenses", "EXPENSE", "", "", "40.00")
	add("091", "Petty Cash", "ASSET", "", "70.00", "70.00")
	add("850", "Suspense", "LIABILITY", "", "", "80.00")
	add("999", "Not in chart", "", "", "10.00", "")
	payload := func(header ...string) json.RawMessage {
		cells := []any{}
		for _, h := range header {
			cells = append(cells, map[string]any{"Value": h})
		}
		report, _ := json.Marshal(map[string]any{"Reports": []any{map[string]any{"Rows": append([]any{map[string]any{"RowType": "Header", "Cells": cells}}, map[string]any{"RowType": "Section", "Rows": rows})}}})
		raw, _ := json.Marshal(map[string]any{"date": "2026-09-30", "organisation": map[string]any{"Organisations": []any{map[string]any{"BaseCurrency": "USD"}}}, "accounts": map[string]any{"Accounts": accounts}, "trial_balance": json.RawMessage(report)})
		return raw
	}
	_, found, err := WrongSide(payload("Account", "Debit", "Credit", "YTD Debit", "YTD Credit"))
	if err != nil || len(found) != 3 {
		t.Fatalf("only the overdrawn bank, the expense credit and the suspense balance are candidates: %v %+v", err, found)
	}
	if found[2].Account != "Suspense" || found[2].Type != "holding_balance" || !strings.Contains(found[2].Finding, "$80.00 credit balance at month-end") {
		t.Fatalf("a suspense account with a balance needs reconciliation: %+v", found[2])
	}
	if found[0].Account != "Business Bank Account" || found[0].Side != "credit" || found[0].Amount != "1200.00" || !strings.Contains(found[0].Finding, "$1200.00 credit balance,") {
		t.Fatalf("a balance-sheet account shows its balance: %+v", found[0])
	}
	if found[1].Account != "General Expenses" || !strings.Contains(found[1].Finding, "credit year-to-date balance") {
		t.Fatalf("a profit-and-loss account shows its year-to-date movement: %+v", found[1])
	}
	if _, _, err = WrongSide(payload("Account", "Debit", "Credit", "YTD Debit")); err == nil {
		t.Fatal("a Trial Balance without YTD Credit must not pass as normal")
	}
	rows[0] = map[string]any{"RowType": "Row", "Cells": []any{map[string]any{"Value": "Business Bank Account", "Attributes": []any{map[string]any{"Value": "090", "Id": "account"}}}, map[string]any{"Value": ""}, map[string]any{"Value": ""}, map[string]any{"Value": "n/a"}, map[string]any{"Value": ""}}}
	if _, _, err = WrongSide(payload("Account", "Debit", "Credit", "YTD Debit", "YTD Credit")); err == nil {
		t.Fatal("an unreadable amount must fail the check")
	}
}

func TestNetRefusesAnAccountListedTwice(t *testing.T) {
	row := func(id string) map[string]any {
		return map[string]any{"RowType": "Row", "Cells": []any{map[string]any{"Value": "Sales", "Attributes": []any{map[string]any{"Id": "account", "Value": id}}}, map[string]any{"Value": ""}, map[string]any{"Value": ""}, map[string]any{"Value": "5.00"}, map[string]any{"Value": ""}}}
	}
	header := map[string]any{"RowType": "Header", "Cells": []any{map[string]any{"Value": "Account"}, map[string]any{"Value": "Debit"}, map[string]any{"Value": "Credit"}, map[string]any{"Value": "YTD Debit"}, map[string]any{"Value": "YTD Credit"}}}
	report := func(rows ...any) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"Reports": []any{map[string]any{"ReportType": "TrialBalance", "Rows": append([]any{header}, rows...)}}})
		return raw
	}
	if got, err := Net(report(row("a")), []string{"a", "b"}); err != nil || got["a"] != "5.00" || got["b"] != "0.00" {
		t.Fatalf("a listed account's net, and zero for one with no row: %v %v", got, err)
	}
	if _, err := Net(report(row("a"), row("a")), []string{"a"}); err == nil {
		t.Fatal("an account listed twice cannot prove an unchanged balance")
	}
}

func TestNativeReconciliationCountsXerosFlagsPerBankAccount(t *testing.T) {
	payload := json.RawMessage(`{"accounts":{"Accounts":[{"AccountID":"b-1","Name":"Operating","Type":"BANK"},{"AccountID":"b-2","Name":"Savings","Type":"BANK"},{"AccountID":"s-1","Name":"Sales","Type":"REVENUE"}]},
		"transactions":{"records":{"BankTransactions":[
			{"Status":"AUTHORISED","IsReconciled":true,"BankAccount":{"AccountID":"b-1"}},
			{"Status":"AUTHORISED","IsReconciled":false,"BankAccount":{"AccountID":"b-1"},"Type":"SPEND-TRANSFER"},
			{"Status":"AUTHORISED","BankAccount":{"AccountID":"b-1"}},
			{"Status":"VOIDED","IsReconciled":false,"BankAccount":{"AccountID":"b-1"}}],
		"Payments":[{"Status":"AUTHORISED","IsReconciled":true,"Account":{"AccountID":"b-1"}},{"Status":"AUTHORISED","IsReconciled":true,"Account":{"AccountID":"s-1"}}]}}}`)
	got, err := NativeReconciliation(payload)
	if err != nil || len(got) != 2 || got[0] != (NativeStatus{"b-1", "Operating", 2, 1, 1}) || got[1] != (NativeStatus{AccountID: "b-2", Account: "Savings"}) {
		t.Fatalf("each bank account counts its authorised records by Xero's flag, a missing flag as unknown: %+v %v", got, err)
	}
	if _, err := NativeReconciliation(json.RawMessage(`{"accounts":{"Accounts":[]},"transactions_error":"Reconnect to read transactions"}`)); err == nil || !strings.Contains(err.Error(), "Reconnect") {
		t.Fatalf("a snapshot without transactions says why: %v", err)
	}
}
