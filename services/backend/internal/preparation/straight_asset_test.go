package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestStraightAssetRequiresApprovedPolicyAndCapitalizedPurchase(t *testing.T) {
	invoice := IntakeSource{Source: Source{ID: "1", Digest: "invoice", Role: "invoice", Name: "asset.txt"}, Content: "Acme Equipment\nInvoice EQ-778\nBill to: Example\nTotal USD 1,200.00\nPlaced in service: 2026-09-14"}
	instructions := "Asset cost account: Equipment\nDepreciation expense account: Depreciation\nAccumulated depreciation account: Accumulated Depreciation\nUseful life months: 12\nSalvage value: 0.00\nDepreciation convention: full_first_month\nQuickBooks company: 123\nVendor ID for Acme Equipment: 8"
	rule := Source{ID: "3", Digest: "rule", Role: "support", Name: "Approved policy"}
	procedure := Procedure{ID: "7", Kind: "prepaid", Method: prepaids.Method, Enabled: true, AssetExample: true, Instructions: instructions, ClientEntity: "Example"}
	proposal := Proposal{Kind: "depreciation", Engine: "context_machine", Sources: []Source{invoice.Source, rule}}
	for _, f := range []struct{ field, value, quote string }{{"amount", "1200.00", "Total USD 1,200.00"}, {"currency", "USD", "Total USD 1,200.00"}, {"service_start", "2026-09-14", "Placed in service: 2026-09-14"}, {"vendor", "Acme Equipment", "Acme Equipment"}, {"invoice_number", "EQ-778", "Invoice EQ-778"}, {"billed_to", "Example", "Bill to: Example"}} {
		proposal.Facts = append(proposal.Facts, Fact{Field: f.field, Value: f.value, Candidates: []Candidate{{Value: f.value, SourceID: "1", Quote: f.quote}}})
	}
	ledger := &Ledger{Source: Source{ID: "9", Digest: "capture"}, Chart: "31", ChartSource: Source{ID: "8", Digest: "chart"}, Realm: "123", CaptureRealm: "123", ChartRealm: "123", ReportPeriod: "2026-09", Accounts: map[string]string{"12": "Equipment", "13": "Accumulated Depreciation", "14": "Depreciation"}, Types: map[string]string{"12": "Fixed Asset", "13": "Fixed Asset", "14": "Other Expense"}, IDs: map[string][]string{"equipment": {"12"}, "accumulated depreciation": {"13"}, "depreciation": {"14"}}, AccountCatalog: json.RawMessage(`[{"Id":"12","Name":"Equipment","AccountType":"Fixed Asset"},{"Id":"13","Name":"Accumulated Depreciation","AccountType":"Fixed Asset"},{"Id":"14","Name":"Depreciation","AccountType":"Other Expense"}]`), TrialBalance: json.RawMessage(`{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"13","value":"Accumulated Depreciation"},{"value":"0.00"},{"value":"0.00"}]}]}}`), Rows: []LedgerRow{{Kind: "Bill", ProviderID: "41", Date: "2026-09-03", Currency: "USD", Record: json.RawMessage(`{"DocNumber":"EQ-778","TotalAmt":1200,"VendorRef":{"value":"8","name":"Acme Equipment"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"12","name":"Equipment"}}}]}`)}}}
	plan, reason := StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger)
	if reason != "" {
		t.Fatal(reason)
	}
	var input prepaids.Input
	if json.Unmarshal(plan.Input, &input) != nil || input.Schedule != "depreciation" || input.ServiceStart != "2026-09-01" || input.ServiceEnd != "2027-08-31" || input.BookedBalance != "0.00" || input.ExpenseAccount != "Depreciation" || input.PrepaidAccount != "Accumulated Depreciation" {
		t.Fatalf("wrong asset input: %+v", input)
	}
	var paper struct {
		PeriodExpense string `json:"period_expense"`
	}
	raw, err := Calculate(plan, "Example", "2026-09")
	if err != nil || json.Unmarshal(raw, &paper) != nil || paper.PeriodExpense != "100.00" {
		t.Fatalf("existing calculator: %s %v", raw, err)
	}
	if !strings.Contains(input.SourceReference, "2026-09-14") || len(plan.Sources) != 4 {
		t.Fatalf("missing source/convention proof: %+v", plan)
	}
	for _, label := range []string{"First used:", "Expected first used:"} {
		changed := invoice
		changed.Content = strings.ReplaceAll(invoice.Content, "Placed in service:", label)
		check := proposal
		check.Facts = append([]Fact{}, proposal.Facts...)
		for i, f := range check.Facts {
			if f.Field == "service_start" {
				f.Candidates = append([]Candidate{}, f.Candidates...)
				f.Candidates[0].Quote = label + " 2026-09-14"
				check.Facts[i] = f
			}
		}
		if _, why := StraightAsset(check, []IntakeSource{changed, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); why == "" {
			t.Fatal("an unsupported invoice date phrase bypassed the guard:", label)
		}
	}
	// A planned or negated in-service date is not an actual placed-in-service date.
	invoice.Content = strings.ReplaceAll(invoice.Content, "Placed in service:", "Not yet placed in service:")
	for i := range proposal.Facts {
		if proposal.Facts[i].Field == "service_start" {
			proposal.Facts[i].Candidates[0].Quote = "Not yet placed in service: 2026-09-14"
		}
	}
	if _, reason = StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); reason == "" {
		t.Fatal("a negated in-service date was accepted")
	}
	// Repeated policy labels are ambiguity, even when the last value matches the books.
	invoice.Content = strings.ReplaceAll(invoice.Content, "Not yet placed in service:", "Placed in service:")
	for i := range proposal.Facts {
		if proposal.Facts[i].Field == "service_start" {
			proposal.Facts[i].Candidates[0].Quote = "Placed in service: 2026-09-14"
		}
	}
	procedure.Instructions = strings.Replace(instructions, "Vendor ID for Acme Equipment: 8", "Vendor ID for Acme Equipment: 9", 1) + "\nVendor ID for Acme Equipment: 8"
	if _, reason = StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: procedure.Instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); reason == "" {
		t.Fatal("conflicting vendor policy lines were silently resolved")
	}

	procedure.Instructions = instructions
	invoice.Content = strings.ReplaceAll(invoice.Content, "\nPlaced in service: 2026-09-14", "")
	answer := IntakeSource{Source: Source{ID: "4", Digest: "answer", Name: "Preparer answer.txt", Role: "support"}, Content: "2026-09-14", PreparerAnswer: true}
	proposal.Sources = append(proposal.Sources, answer.Source)
	for i := range proposal.Facts {
		if proposal.Facts[i].Field == "service_start" {
			proposal.Facts[i].Candidates[0].SourceID = "4"
			proposal.Facts[i].Candidates[0].Quote = "2026-09-14"
		}
	}
	if _, reason = StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); reason != "" {
		t.Fatal("the job's own date answer did not complete preparation:", reason)
	}
	for _, body := range []string{"Placed in service 2026-09-14 or 2026-09-15", "Placed in service 2026-09-14T01:00:00Z and 2026-09-15T01:00:00Z", "Placed in service 2026-09-14; cost USD 1,250.00", "Placed in service 2026-09-14, use account 1510", "Expected in service 2026-09-14"} {
		answer.Content = body
		if _, reason = StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); reason == "" {
			t.Fatal("an ambiguous or policy-changing answer was accepted:", body)
		}
	}
	answer.Content = "2026-09-14"
	invoice.Content += "\nPlaced in service: 2026-09-20"
	if _, why := StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); why == "" {
		t.Fatal("an answer overrode a conflicting invoice date")
	}
	invoice.Content = strings.TrimSuffix(invoice.Content, "\nPlaced in service: 2026-09-20")
	answer.PreparerAnswer = false
	answer.Role = "client_reply"
	answer.Content = "Client reply for Example\n\n1. asset.txt: When was it placed in service?\nAnswer: 2026-09-14\n\n2. Other document: When was it placed in service?\nAnswer: 2026-09-20\n"
	answer.ClientAnswers = []ClientAnswer{{"When was it placed in service?", "2026-09-14", "\n1. asset.txt: When was it placed in service?\nAnswer: 2026-09-14\n"}}
	if _, reason = StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); reason != "" {
		t.Fatal("the explicitly bound client answer did not complete preparation:", reason)
	}
	for _, question := range []string{"When was it placed in service, or when is it going to be?", "When is it going to be placed in service?", "When should it be placed in service?", "When will it be placed in service?", "When is it scheduled to be placed in service?", "When is it expected to be placed in service?", "When was it ordered and placed in service?", "in service asset.pdf: When was it delivered?"} {
		answer.ClientAnswers[0].Question = question
		if _, why := StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); why == "" {
			t.Fatal("a planned or unrelated date question established actual in-service:", question)
		}
	}
	answer.ClientAnswers[0].Question = "Per the fixed asset schedule, when was it placed in service?"
	if _, why := StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); why != "" {
		t.Fatal("a past-tense question mentioning its schedule was held:", why)
	}
	for i := range proposal.Facts {
		if proposal.Facts[i].Field == "service_start" {
			proposal.Facts[i].Value = "2026-09-20"
			proposal.Facts[i].Candidates[0].Value = "2026-09-20"
			proposal.Facts[i].Candidates[0].Quote = "2026-09-20"
		}
	}
	if _, why := StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); why == "" {
		t.Fatal("another job's client answer supplied the asset date")
	}
	for i := range proposal.Facts {
		if proposal.Facts[i].Field == "service_start" {
			proposal.Facts[i].Value = "2026-09-14"
			proposal.Facts[i].Candidates[0].Value = "2026-09-14"
			proposal.Facts[i].Candidates[0].Quote = "2026-09-14"
		}
	}
	answer.Content = "2026-09-14"
	answer.ClientAnswers = nil
	if _, reason = StraightAsset(proposal, []IntakeSource{invoice, {Source: rule, Content: instructions}, answer}, procedure, rule, "USD", "Example", "2026-09", nil, false, ledger); reason == "" {
		t.Fatal("an unbound answer supplied the asset date")
	}

}
