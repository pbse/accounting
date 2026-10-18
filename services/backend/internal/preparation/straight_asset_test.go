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

}
