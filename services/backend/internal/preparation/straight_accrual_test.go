package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/accruals"
)

func TestStraightFixedAccrual(t *testing.T) {
	invoice := IntakeSource{Source: Source{ID: "1", Digest: "invoice", Name: "service.txt", Role: "invoice"}, Content: "Vendor: Acme Services\nInvoice: A-17\nBill to: Example\nService period: 2026-09-01 through 2026-09-30\nUnbilled service total: USD 1200.00\nEstimation basis: Fixed incurred September service"}
	rule := Source{ID: "2", Digest: "rule", Name: "Approved accrual rule", Role: "procedure"}
	procedure := Procedure{ID: "3", ClientEntity: "Example", Kind: "accrual", Method: accruals.Method, Enabled: true, Instructions: "Expense account: Professional Fees\nLiability account: Accrued Expenses\nReverse next month: true\nQuickBooks company: 123\nVendor ID for Acme Services: 8"}
	p := Proposal{Kind: "accrual", Engine: "openai", Sources: []Source{invoice.Source, rule}}
	for _, f := range []struct{ key, value, quote, source string }{
		{"amount", "1200.00", "Unbilled service total: USD 1200.00", "1"}, {"currency", "USD", "Unbilled service total: USD 1200.00", "1"},
		{"service_start", "2026-09-01", "Service period: 2026-09-01 through 2026-09-30", "1"}, {"service_end", "2026-09-30", "Service period: 2026-09-01 through 2026-09-30", "1"},
		{"vendor", "Acme Services", "Vendor: Acme Services", "1"}, {"invoice_number", "A-17", "Invoice: A-17", "1"}, {"billed_to", "Example", "Bill to: Example", "1"},
		{"estimation_basis", "Fixed incurred September service", "Estimation basis: Fixed incurred September service", "1"},
		{"expense_account", "Professional Fees", "Expense account: Professional Fees", "2"}, {"liability_account", "Accrued Expenses", "Liability account: Accrued Expenses", "2"},
		{"reverse_next_month", "true", "Reverse next month: true", "2"},
	} {
		p.Facts = append(p.Facts, Fact{Field: f.key, Label: f.key, Value: f.value, Candidates: []Candidate{{Value: f.value, SourceID: f.source, Quote: f.quote}}})
	}
	ledger := &Ledger{Source: Source{ID: "4", Digest: "capture"}, Chart: "5", ChartSource: Source{ID: "6", Digest: "chart"}, Accounts: map[string]string{"10": "Professional Fees", "20": "Accrued Expenses"}, Types: map[string]string{"10": "Expense", "20": "Other Current Liability"}, IDs: map[string][]string{"professional fees": {"10"}, "accrued expenses": {"20"}}}
	ledger.AccountCatalog = json.RawMessage(`[{"Id":"10","Name":"Professional Fees","AccountType":"Expense"},{"Id":"20","Name":"Accrued Expenses","AccountType":"Other Current Liability"}]`)
	ledger.TrialBalance = json.RawMessage(`{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"20","value":"Accrued Expenses"},{"value":"0.00"},{"value":"0.00"}]}]}}`)
	ledger.ReportPeriod = "2026-09"
	ledger.Realm = "123"
	ledger.CaptureRealm, ledger.ChartRealm = "123", "123"
	aliases := []string{}
	run := func() (Plan, string) {
		return StraightAccrual(p, []IntakeSource{invoice, {Source: rule, Content: procedure.Instructions}, {Source: ledger.ChartSource, Content: string(ledger.TrialBalance)}}, procedure, rule, "USD", "Example", "2026-09", aliases, false, nil, ledger)
	}
	plan, reason := run()
	if reason != "" {
		t.Fatal("verified incurred service should queue an ordinary draft:", reason)
	}
	var in accruals.Input
	if json.Unmarshal(plan.Input, &in) != nil || in.Amount != "1200.00" || in.BookedBalance != "0.00" || !in.ReverseNextMonth || plan.Procedure != "3" || len(plan.Sources) != 4 {
		t.Fatalf("unbound accrual: %+v %+v", plan, in)
	}
	// A negated unbilled amount is not evidence of an incurred unpaid service.
	originalContent := invoice.Content
	for i := range p.Facts {
		if p.Facts[i].Field == "amount" || p.Facts[i].Field == "currency" {
			p.Facts[i].Candidates[0].Quote = "No unbilled service total: USD 1200.00"
		}
	}
	invoice.Content = strings.Replace(invoice.Content, "Unbilled service total: USD 1200.00", "No unbilled service total: USD 1200.00", 1)
	if _, reason := run(); reason == "" {
		t.Fatal("a negated unbilled figure was accepted")
	}
	invoice.Content = originalContent
	for i := range p.Facts {
		if p.Facts[i].Field == "amount" || p.Facts[i].Field == "currency" {
			p.Facts[i].Candidates[0].Quote = "Unbilled service total: USD 1200.00"
		}
	}
	// A confirmed client alias remains usable for the invoice's billed-to identity.
	aliases = []string{"Example Trading"}
	for i := range p.Facts {
		if p.Facts[i].Field == "billed_to" {
			p.Facts[i].Value = "Example Trading"
			p.Facts[i].Candidates[0].Value = "Example Trading"
			p.Facts[i].Candidates[0].Quote = "Bill to: Example Trading"
		}
	}
	invoice.Content = strings.Replace(invoice.Content, "Bill to: Example", "Bill to: Example Trading", 1)
	if _, reason := run(); reason != "" {
		t.Fatal("confirmed alias refused:", reason)
	}
	// Company-scoped IDs cannot be used against a capture from a different company.
	ledger.CaptureRealm = "old-company"
	if _, reason := run(); reason == "" {
		t.Fatal("a capture from another company was accepted")
	}
	ledger.CaptureRealm = "123"
	originalRule := procedure.Instructions
	procedure.Instructions = strings.Replace(procedure.Instructions, "Vendor ID for Acme Services: 8", "Vendor ID for Acme Services: #8", 1)
	if _, reason := run(); reason == "" {
		t.Fatal("a malformed supplier ID was accepted")
	}
	procedure.Instructions = originalRule
	// A model may cite the authoritative current books, rather than omit a balance the server can verify.
	p.Sources = append(p.Sources, ledger.ChartSource)
	p.Facts = append(p.Facts, Fact{Field: "booked_balance", Label: "booked balance", Value: "0.00", Candidates: []Candidate{{Value: "0.00", SourceID: ledger.ChartSource.ID, Quote: "Accrued Expenses 0.00"}}})
	if _, reason := run(); reason != "" {
		t.Fatal("an explicitly cited current zero balance should pass:", reason)
	}
	p.Facts = p.Facts[:len(p.Facts)-1]
	p.Sources = p.Sources[:len(p.Sources)-1]
	// An ordinary named-customer deposit is unrelated; a liability journal or purchase from this vendor is not.
	ledger.Accounts["30"], ledger.Accounts["40"] = "Checking", "Sales"
	ledger.IDs["checking"], ledger.IDs["sales"] = []string{"30"}, []string{"40"}
	for _, c := range []struct {
		kind, raw string
		pass      bool
	}{
		{"Deposit", `{"DepositToAccountRef":{"value":"30","name":"Checking"},"Line":[{"DepositLineDetail":{"AccountRef":{"value":"40","name":"Sales"},"Entity":{"value":"7","name":"Other Customer"}}}]}`, true},
		{"JournalEntry", `{"Line":[{"JournalEntryLineDetail":{"AccountRef":{"value":"20","name":"Accrued Expenses"},"Entity":{"EntityRef":{"name":"Other Vendor"}}}}]}`, false},
		{"Bill", `{"VendorRef":{"value":"8","name":"Acme Services"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"10","name":"Professional Fees"}}}]}`, false},
		{"Purchase", `{"EntityRef":{"value":"8","name":"Acme Services","type":"Vendor"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"10","name":"Professional Fees"}}}]}`, false},
		{"JournalEntry", `{"Line":[{"JournalEntryLineDetail":{"AccountRef":{"value":"10","name":"Professional Fees"},"Entity":{"EntityRef":{"value":"8","name":"Acme Services"}}}}]}`, false},
		{"Bill", `{"VendorRef":{"value":"8","name":"Acme Display Name"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"10","name":"Professional Fees"}}}]}`, false},
		{"Purchase", `{"EntityRef":{"value":"8"},"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"10","name":"Professional Fees"}}}]}`, false},
	} {
		ledger.Rows = []LedgerRow{{Kind: c.kind, Date: "2026-09-15", Record: json.RawMessage(c.raw)}}
		t.Run(c.kind, func(t *testing.T) {
			if _, reason := run(); (reason == "") != c.pass {
				t.Fatalf("%s booking: %s", c.kind, reason)
			}
		})
	}
	ledger.Rows = nil
	// A current liability already carrying a balance cannot be allocated to this invoice automatically.
	ledger.TrialBalance = json.RawMessage(`{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"20","value":"Accrued Expenses"},{"value":"0.00"},{"value":"500.00"}]}]}}`)
	if _, reason = run(); reason == "" {
		t.Fatal("nonzero liability was silently assigned to one service")
	}
}
