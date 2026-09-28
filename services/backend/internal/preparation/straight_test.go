package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestStraightThroughNeedsOneVerifiedCandidatePerFactAndOneBookedLine(t *testing.T) {
	fact := func(field, value string) Fact {
		return Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value, Candidates: []Candidate{{Value: value, SourceID: "1", Location: "line 1", Quote: value}}}
	}
	proposal := Proposal{Kind: "prepaid", Engine: "openai", Sources: []Source{{ID: "1", Digest: "d", Name: "insurance.pdf", Role: "invoice"}},
		Facts: []Fact{fact("amount", "1200.00"), fact("service_start", "2026-09-01"), fact("service_end", "2027-08-31"), fact("currency", "USD"), {Field: "expense_account", Label: "expense account"}, {Field: "prepaid_account", Label: "prepaid account"}, {Field: "booked_balance", Label: "booked balance", Optional: true}}}
	procedure := Procedure{ID: "7", Kind: "prepaid", Method: prepaids.Method, ExpenseAccount: "Insurance", PrepaidAccount: "Prepaid Expenses", Enabled: true}
	bill := func(id, date, account, amount string) LedgerRow {
		record, _ := json.Marshal(map[string]any{"Line": []map[string]any{{"Amount": json.Number(amount), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"Name": account}}}}})
		return LedgerRow{Kind: "Bill", ProviderID: id, Date: date, Currency: "USD", Record: record}
	}
	plan, reason := StraightThrough(proposal, procedure, "USD", "Example", "2026-09", "9", []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200"), bill("42", "2026-09-04", "Travel", "1200")})
	if reason != "" || plan.Kind != "prepaid" || len(plan.Sources) != 1 {
		t.Fatal("expensed invoice not prepared", reason)
	}
	var input prepaids.Input
	json.Unmarshal(plan.Input, &input)
	if input.BookedBalance != "" || !input.BookingConfirmed || input.Method != prepaids.Method || input.ExpenseAccount != "Insurance" {
		t.Fatal("unexpected input", input)
	}
	_, reason = StraightThrough(proposal, procedure, "USD", "Example", "2026-09", "9", []LedgerRow{bill("41", "2026-09-03", "prepaid expenses", "1200.00")})
	if reason != "" {
		t.Fatal("invoice booked to prepaid not prepared", reason)
	}
	for name, c := range map[string]struct {
		change func(*Proposal, *Procedure)
		rows   []LedgerRow
	}{
		"no bill":            {func(*Proposal, *Procedure) {}, nil},
		"bill outside month": {func(*Proposal, *Procedure) {}, []LedgerRow{bill("41", "2026-08-31", "Insurance", "1200")}},
		"two bills":          {func(*Proposal, *Procedure) {}, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200"), bill("43", "2026-09-05", "Prepaid Expenses", "1200")}},
		"open question":      {func(p *Proposal, _ *Procedure) { p.Questions = []string{"Which policy?"} }, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"conflicting amount": {func(p *Proposal, _ *Procedure) {
			p.Facts[0].Value = ""
			p.Facts[0].Candidates = append(p.Facts[0].Candidates, Candidate{Value: "1300.00"})
		}, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"cited credit": {func(p *Proposal, _ *Procedure) {
			p.Facts[6].Candidates = []Candidate{{Value: "100.00"}}
			p.Facts[6].Value = "100.00"
		}, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"disabled procedure": {func(_ *Proposal, q *Procedure) { q.Enabled = false }, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"actual days":        {func(_ *Proposal, q *Procedure) { q.Method = prepaids.DailyMethod }, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"other account cited": {func(p *Proposal, _ *Procedure) {
			p.Facts[4].Value = "Rent"
			p.Facts[4].Candidates = []Candidate{{Value: "Rent"}}
		}, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"foreign currency":      {func(p *Proposal, _ *Procedure) { p.Facts[3].Value = "CAD" }, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"local proposal":        {func(p *Proposal, _ *Procedure) { p.Engine = "local" }, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
		"close outside service": {func(p *Proposal, _ *Procedure) { p.Facts[1].Value = "2026-10-01" }, []LedgerRow{bill("41", "2026-09-03", "Insurance", "1200")}},
	} {
		p, q := proposal, procedure
		p.Facts = append([]Fact(nil), proposal.Facts...)
		for i := range p.Facts {
			p.Facts[i].Candidates = append([]Candidate(nil), p.Facts[i].Candidates...)
		}
		c.change(&p, &q)
		if _, reason := StraightThrough(p, q, "USD", "Example", "2026-09", "9", c.rows); reason == "" {
			t.Fatal(name, "prepared without a person")
		}
	}
}
