package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestStraightThroughNeedsOneVerifiedCandidatePerFactAndTheInvoiceExpensed(t *testing.T) {
	fact := func(field, value string) Fact {
		return Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value, Candidates: []Candidate{{Value: value, SourceID: "1", Location: "line 1", Quote: value}}}
	}
	proposal := Proposal{Kind: "prepaid", Engine: "openai", Sources: []Source{{ID: "1", Digest: "d", Name: "insurance.pdf", Role: "invoice"}},
		Facts: []Fact{fact("amount", "1200.00"), fact("service_start", "2026-09-01"), fact("service_end", "2027-08-31"), fact("currency", "USD"), {Field: "expense_account", Label: "expense account"}, {Field: "prepaid_account", Label: "prepaid account"}, {Field: "booked_balance", Label: "booked balance", Optional: true}}}
	invoice := "Acme Insurance Co\nInvoice INV-778\nAnnual premium USD 1,200.00\nCoverage September 1, 2026 to August 31, 2027"
	procedure := Procedure{ID: "7", Kind: "prepaid", Method: prepaids.Method, ExpenseAccount: "Insurance", PrepaidAccount: "Prepaid Expenses", Enabled: true}
	row := func(kind, id, date, vendor, number, account, amount string) LedgerRow {
		detail := "AccountBasedExpenseLineDetail"
		if kind == "JournalEntry" {
			detail = "JournalEntryLineDetail"
		}
		who := "VendorRef"
		if kind == "Purchase" {
			who = "EntityRef"
		}
		record, _ := json.Marshal(map[string]any{"DocNumber": number, who: map[string]string{"Name": vendor}, "Line": []map[string]any{{"Amount": json.Number(amount), detail: map[string]any{"AccountRef": map[string]string{"Name": account}}}}})
		return LedgerRow{Kind: kind, ProviderID: id, Date: date, Currency: "USD", Record: record}
	}
	bill := row("Bill", "41", "2026-09-03", "Acme Insurance Co", "", "Insurance", "1200")
	ledger := func(rows ...LedgerRow) *Ledger {
		return &Ledger{Source: Source{ID: "9", Digest: "cap", Name: "QuickBooks transactions 2026-09"}, Rows: rows}
	}
	plan, reason := StraightThrough(proposal, invoice, procedure, "USD", "Example", "2026-09", "5", ledger(bill, row("Bill", "42", "2026-09-04", "Other Vendor", "", "Travel", "1200")))
	if reason != "" || plan.Kind != "prepaid" || len(plan.Sources) != 2 || plan.Sources[1].Role != "period_ledger" || plan.Sources[1].Digest != "cap" || !strings.Contains(plan.Sources[1].Location, "Bill 41") {
		t.Fatal("expensed invoice not prepared with the capture cited", reason, plan.Sources)
	}
	var input prepaids.Input
	json.Unmarshal(plan.Input, &input)
	if input.BookedBalance != "" || !input.BookingConfirmed || input.Method != prepaids.Method || input.ExpenseAccount != "Insurance" || input.SourceReference != "Evidence #1 · work #5" {
		t.Fatal("unexpected input", input)
	}
	if _, reason = StraightThrough(proposal, invoice, procedure, "USD", "Example", "2026-09", "5", ledger(row("Purchase", "50", "2026-09-03", "", "INV-778", "insurance", "1200.00"))); reason != "" {
		t.Fatal("purchase bearing the invoice number not accepted", reason)
	}
	if _, reason = StraightThrough(proposal, invoice, procedure, "USD", "Example", "2026-09", "5", ledger(row("Purchase", "51", "2026-09-03", "Acme Insurance Co", "", "Insurance", "1200"))); reason != "" {
		t.Fatal("purchase from the vendor not accepted", reason)
	}
	for name, c := range map[string]struct {
		change func(*Proposal, *Procedure)
		ledger *Ledger
	}{
		"no capture":                {func(*Proposal, *Procedure) {}, nil},
		"no bill":                   {func(*Proposal, *Procedure) {}, ledger()},
		"other vendor, same amount": {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-09-03", "Other Vendor", "77", "Insurance", "1200"))},
		"bill outside month":        {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-08-31", "Acme Insurance Co", "", "Insurance", "1200"))},
		"two bills":                 {func(*Proposal, *Procedure) {}, ledger(bill, row("Bill", "43", "2026-09-05", "Acme Insurance Co", "", "Insurance", "1200"))},
		"booked to prepaid":         {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-09-03", "Acme Insurance Co", "", "Prepaid Expenses", "1200"))},
		"journal moves it":          {func(*Proposal, *Procedure) {}, ledger(bill, row("JournalEntry", "60", "2026-09-30", "", "", "Prepaid Expenses", "1200"))},
		"open question":             {func(p *Proposal, _ *Procedure) { p.Questions = []string{"Which policy?"} }, ledger(bill)},
		"conflicting amount": {func(p *Proposal, _ *Procedure) {
			p.Facts[0].Value = ""
			p.Facts[0].Candidates = append(p.Facts[0].Candidates, Candidate{Value: "1300.00"})
		}, ledger(bill)},
		"cited credit": {func(p *Proposal, _ *Procedure) {
			p.Facts[6].Candidates = []Candidate{{Value: "100.00"}}
			p.Facts[6].Value = "100.00"
		}, ledger(bill)},
		"disabled procedure": {func(_ *Proposal, q *Procedure) { q.Enabled = false }, ledger(bill)},
		"actual days":        {func(_ *Proposal, q *Procedure) { q.Method = prepaids.DailyMethod }, ledger(bill)},
		"other account cited": {func(p *Proposal, _ *Procedure) {
			p.Facts[4].Value = "Rent"
			p.Facts[4].Candidates = []Candidate{{Value: "Rent"}}
		}, ledger(bill)},
		"foreign currency":      {func(p *Proposal, _ *Procedure) { p.Facts[3].Value = "CAD" }, ledger(bill)},
		"local proposal":        {func(p *Proposal, _ *Procedure) { p.Engine = "local" }, ledger(bill)},
		"close outside service": {func(p *Proposal, _ *Procedure) { p.Facts[1].Value = "2026-10-01" }, ledger(bill)},
	} {
		p, q := proposal, procedure
		p.Facts = append([]Fact(nil), proposal.Facts...)
		for i := range p.Facts {
			p.Facts[i].Candidates = append([]Candidate(nil), p.Facts[i].Candidates...)
		}
		c.change(&p, &q)
		if _, reason := StraightThrough(p, invoice, q, "USD", "Example", "2026-09", "5", c.ledger); reason == "" {
			t.Fatal(name, "prepared without a person")
		}
	}
}
