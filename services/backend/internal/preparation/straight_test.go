package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestStraightThroughNeedsOneVerifiedCandidatePerFactAndTheInvoiceExpensed(t *testing.T) {
	fact := func(field, value string) Fact {
		return Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value, Candidates: []Candidate{{Value: value, SourceID: "1", Location: "line 1", Quote: "Annual premium " + value}}}
	}
	optionalFact := func(field, value string) Fact {
		f := fact(field, value)
		f.Optional = true
		return f
	}
	proposal := Proposal{Kind: "prepaid", Engine: "openai", Sources: []Source{{ID: "1", Digest: "d", Name: "insurance.pdf", Role: "invoice"}},
		Facts: []Fact{fact("amount", "1200.00"), fact("service_start", "2026-09-01"), fact("service_end", "2027-08-31"), fact("currency", "USD"), {Field: "expense_account", Label: "expense account"}, {Field: "prepaid_account", Label: "prepaid account"}, {Field: "booked_balance", Label: "booked balance", Optional: true}, optionalFact("vendor", "Acme Insurance Co"), optionalFact("billed_to", "Example")}}
	invoice := "Acme Insurance Co\nInvoice INV-778\nPO-123\nBill to: Example\nAnnual premium USD 1,200.00\nCoverage September 1, 2026 to August 31, 2027\n\nPay online with Stripe"
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
	split := bill
	split.Record, _ = json.Marshal(map[string]any{"VendorRef": map[string]string{"Name": "Acme Insurance Co"}, "Line": []map[string]any{{"Amount": json.Number("1200"), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"Name": "Insurance"}}}, {"Amount": json.Number("300"), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"Name": "Prepaid Expenses"}}}}})
	refund := row("Purchase", "74", "2026-09-12", "Acme Insurance Co", "", "Insurance", "100")
	refund.Record = json.RawMessage(strings.Replace(string(refund.Record), `{"DocNumber"`, `{"Credit":true,"DocNumber"`, 1))
	foreign := row("JournalEntry", "65", "2026-09-30", "", "", "Insurance", "900")
	foreign.Currency = "CAD"
	ledger := func(rows ...LedgerRow) *Ledger {
		return &Ledger{Source: Source{ID: "9", Digest: "cap", Name: "QuickBooks transactions 2026-09"}, Rows: rows, Accounts: map[string]string{"12": "Insurance", "13": "Prepaid Expenses"}}
	}
	plan, reason := StraightThrough(proposal, invoice, nil, procedure, "USD", "Example", "2026-09", "5", false, ledger(bill, row("Bill", "42", "2026-09-04", "Other Vendor", "", "Travel", "1200")))
	if reason != "" || plan.Kind != "prepaid" || len(plan.Sources) != 2 || plan.Sources[1].Role != "period_ledger" || plan.Sources[1].Digest != "cap" || !strings.Contains(plan.Sources[1].Location, "Bill 41") {
		t.Fatal("expensed invoice not prepared with the capture cited", reason, plan.Sources)
	}
	var input prepaids.Input
	json.Unmarshal(plan.Input, &input)
	if input.BookedBalance != "" || !input.BookingConfirmed || input.Method != prepaids.Method || input.ExpenseAccount != "Insurance" || input.SourceReference != "Evidence #1 · work #5" {
		t.Fatal("unexpected input", input)
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, "USD", "Example", "2026-09", "5", false, ledger(bill, row("Bill", "52", "2026-09-09", "Other Vendor", "", "Prepaid Expenses", "1200"))); reason != "" {
		t.Fatal("another vendor's prepaid bill blocked this invoice", reason)
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, "USD", "Example", "2026-09", "5", false, ledger(row("Purchase", "51", "2026-09-03", "Acme Insurance Co", "", "Insurance", "1200"))); reason != "" {
		t.Fatal("purchase from the vendor not accepted", reason)
	}
	other := proposal
	other.Facts = append([]Fact(nil), proposal.Facts...)
	other.Facts[8] = optionalFact("billed_to", "Other Co")
	if _, reason = StraightThrough(other, invoice, nil, procedure, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared an invoice billed to another company")
	}
	other.Facts[8] = optionalFact("billed_to", "Example Services LLC")
	if _, reason = StraightThrough(other, invoice, []string{"Example Services, LLC"}, procedure, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason != "" {
		t.Fatal("confirmed alias not accepted", reason)
	}
	idOnly := bill
	idOnly.Record = json.RawMessage(`{"VendorRef":{"Name":"Acme Insurance Co"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"12"}}}]}`)
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, "USD", "Example", "2026-09", "5", false, ledger(idOnly)); reason != "" {
		t.Fatal("bill line naming its account by ID only not accepted", reason)
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, "", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared without a detected home currency")
	}
	for name, c := range map[string]struct {
		change func(*Proposal, *Procedure)
		ledger *Ledger
		unread bool
	}{
		"processor heads the invoice": {func(*Proposal, *Procedure) {}, ledger(row("Bill", "79", "2026-09-03", "Stripe", "", "Insurance", "1200")), false},
		"vendor not read":             {func(p *Proposal, _ *Procedure) { p.Facts[7].Candidates = nil; p.Facts[7].Value = "" }, ledger(bill), false},
		"billed-to not read":          {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates = nil; p.Facts[8].Value = "" }, ledger(bill), false},
		"processor named in the body": {func(*Proposal, *Procedure) {}, ledger(row("Bill", "70", "2026-09-03", "Stripe", "", "Insurance", "1200")), false},
		"line-less vendor credit":     {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "VendorCredit", ProviderID: "71", Date: "2026-09-10", Record: json.RawMessage(`{"VendorRef":{"Name":"Acme Insurance Co"}}`)}), false},
		"line-less journal":           {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "JournalEntry", ProviderID: "72", Date: "2026-09-10", Record: json.RawMessage(`{}`)}), false},
		"card refund":                 {func(*Proposal, *Procedure) {}, ledger(bill, refund), false},
		"unnamed account on a credit": {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Purchase", ProviderID: "78", Date: "2026-09-10", Record: json.RawMessage(`{"Credit":true,"EntityRef":{"Name":"Someone"},"Line":[{"Amount":50,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"99"}}}]}`)}), false},
		"amount cited from support":   {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].SourceID = "2" }, ledger(bill), false},
		"line-less card credit":       {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Purchase", ProviderID: "77", Date: "2026-09-10", Record: json.RawMessage(`{"Credit":true,"EntityRef":{"Name":"Stripe"}}`)}), false},
		"purchase order number":       {func(*Proposal, *Procedure) {}, ledger(row("Bill", "80", "2026-09-03", "Other Vendor", "PO-123", "Insurance", "1200")), false},
		"number is a prefix":          {func(p *Proposal, _ *Procedure) {}, ledger(row("Bill", "76", "2026-09-03", "Other Vendor", "INV-77", "Insurance", "1200")), false},
		"date as document number":     {func(*Proposal, *Procedure) {}, ledger(row("Bill", "75", "2026-09-03", "Other Vendor", "2026-09", "Insurance", "1200")), false},
		"year as document number":     {func(*Proposal, *Procedure) {}, ledger(row("Bill", "73", "2026-09-03", "Other Vendor", "2026", "Insurance", "1200")), false},
		"unread evidence":             {func(*Proposal, *Procedure) {}, ledger(bill), true},
		"partial prepaid line":        {func(*Proposal, *Procedure) {}, ledger(split), false},
		"foreign journal":             {func(*Proposal, *Procedure) {}, ledger(bill, foreign), false},
		"unnamed account":             {func(*Proposal, *Procedure) {}, ledger(bill, row("JournalEntry", "66", "2026-09-30", "", "", "", "10")), false},
		"no capture":                  {func(*Proposal, *Procedure) {}, nil, false},
		"no bill":                     {func(*Proposal, *Procedure) {}, ledger(), false},
		"other vendor, same amount":   {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-09-03", "Other Vendor", "77", "Insurance", "1200")), false},
		"bill outside month":          {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-08-31", "Acme Insurance Co", "", "Insurance", "1200")), false},
		"two bills":                   {func(*Proposal, *Procedure) {}, ledger(bill, row("Bill", "43", "2026-09-05", "Acme Insurance Co", "", "Insurance", "1200")), false},
		"booked to prepaid":           {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-09-03", "Acme Insurance Co", "", "Prepaid Expenses", "1200")), false},
		"journal moves it":            {func(*Proposal, *Procedure) {}, ledger(bill, row("JournalEntry", "60", "2026-09-30", "", "", "Prepaid Expenses", "1200")), false},
		"open question":               {func(p *Proposal, _ *Procedure) { p.Questions = []string{"Which policy?"} }, ledger(bill), false},
		"unselected evidence":         {func(p *Proposal, _ *Procedure) { p.Unselected = []Source{{ID: "2", Name: "credit-note.pdf"}} }, ledger(bill), false},
		"currency not quoted":         {func(p *Proposal, _ *Procedure) { p.Facts[3].Candidates[0].Quote = "Annual premium 1,200.00" }, ledger(bill), false},
		"account not quoted": {func(p *Proposal, _ *Procedure) {
			p.Facts[4].Value = "Insurance"
			p.Facts[4].Candidates = []Candidate{{Value: "Insurance", Quote: "Coverage"}}
		}, ledger(bill), false},
		"journal on expense": {func(*Proposal, *Procedure) {}, ledger(bill, row("JournalEntry", "64", "2026-09-30", "", "", "Insurance", "1200")), false},
		"conflicting amount": {func(p *Proposal, _ *Procedure) {
			p.Facts[0].Value = ""
			p.Facts[0].Candidates = append(p.Facts[0].Candidates, Candidate{Value: "1300.00"})
		}, ledger(bill), false},
		"cited credit": {func(p *Proposal, _ *Procedure) {
			p.Facts[6].Candidates = []Candidate{{Value: "100.00"}}
			p.Facts[6].Value = "100.00"
		}, ledger(bill), false},
		"disabled procedure": {func(_ *Proposal, q *Procedure) { q.Enabled = false }, ledger(bill), false},
		"actual days":        {func(_ *Proposal, q *Procedure) { q.Method = prepaids.DailyMethod }, ledger(bill), false},
		"other account cited": {func(p *Proposal, _ *Procedure) {
			p.Facts[4].Value = "Rent"
			p.Facts[4].Candidates = []Candidate{{Value: "Rent"}}
		}, ledger(bill), false},
		"foreign currency":      {func(p *Proposal, _ *Procedure) { p.Facts[3].Value = "CAD" }, ledger(bill), false},
		"local proposal":        {func(p *Proposal, _ *Procedure) { p.Engine = "local" }, ledger(bill), false},
		"close outside service": {func(p *Proposal, _ *Procedure) { p.Facts[1].Value = "2026-10-01" }, ledger(bill), false},
	} {
		p, q := proposal, procedure
		p.Facts = append([]Fact(nil), proposal.Facts...)
		for i := range p.Facts {
			p.Facts[i].Candidates = append([]Candidate(nil), p.Facts[i].Candidates...)
		}
		c.change(&p, &q)
		if _, reason := StraightThrough(p, invoice, nil, q, "USD", "Example", "2026-09", "5", c.unread, c.ledger); reason == "" {
			t.Fatal(name, "prepared without a person")
		}
	}
}
