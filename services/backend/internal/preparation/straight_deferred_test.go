package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestStraightDeferredRequiresAnApprovedExampleAndFullAdvance(t *testing.T) {
	invoice := IntakeSource{Source: Source{ID: "1", Digest: "invoice", Name: "customer-invoice.txt", Role: "invoice"}, Content: "Example\nInvoice REV-22\nBill to: Northwind\nTotal USD 1,200.00\nService period: 2026-09-01 to 2027-08-31"}
	instructions := "Revenue account: Subscription Revenue\nDeferred revenue account: Deferred Revenue\nQuickBooks company: 123\nCustomer ID for Northwind: 7"
	rule := Source{ID: "3", Digest: "rule", Name: "Approved method", Role: "support"}
	procedure := Procedure{ID: "7", ClientEntity: "Example", Kind: "prepaid", Method: prepaids.Method, Enabled: true, DeferredExample: true, Instructions: instructions}
	p := Proposal{Kind: "deferred_revenue", Engine: "context_machine", Sources: []Source{invoice.Source, rule}}
	for _, f := range []struct{ field, value, quote string }{{"amount", "1200.00", "Total USD 1,200.00"}, {"currency", "USD", "Total USD 1,200.00"}, {"vendor", "Example", "Example"}, {"billed_to", "Northwind", "Bill to: Northwind"}, {"invoice_number", "REV-22", "Invoice REV-22"}, {"service_start", "2026-09-01", "Service period: 2026-09-01 to 2027-08-31"}, {"service_end", "2027-08-31", "Service period: 2026-09-01 to 2027-08-31"}} {
		p.Facts = append(p.Facts, Fact{Field: f.field, Value: f.value, Candidates: []Candidate{{Value: f.value, SourceID: "1", Quote: f.quote}}})
	}
	l := &Ledger{Source: Source{ID: "9", Digest: "capture"}, Chart: "31", ChartSource: Source{ID: "8", Digest: "chart"}, Realm: "123", CaptureRealm: "123", ChartRealm: "123", ReportPeriod: "2026-09", Accounts: map[string]string{"12": "Subscription Revenue", "13": "Deferred Revenue"}, Types: map[string]string{"12": "Income", "13": "Other Current Liability"}, IDs: map[string][]string{"subscription revenue": {"12"}, "deferred revenue": {"13"}}, AccountCatalog: json.RawMessage(`[{"Id":"12","Name":"Subscription Revenue","AccountType":"Income"},{"Id":"13","Name":"Deferred Revenue","AccountType":"Other Current Liability"}]`), TrialBalance: json.RawMessage(`{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"13","value":"Deferred Revenue"},{"value":"0.00"},{"value":"1200.00"}]}]}}`)}
	l.CustomerRows = []LedgerRow{{Kind: "Invoice", ProviderID: "41", Date: "2026-09-03", Currency: "USD", Record: json.RawMessage(`{"DocNumber":"REV-22","TotalAmt":1200,"CustomerRef":{"value":"7","name":"Northwind"},"Line":[{"Amount":1200,"SalesItemLineDetail":{"ItemRef":{"value":"1","name":"Annual subscription"}}}]}`)}}
	l.CustomerRows[0].Record = json.RawMessage(strings.TrimSuffix(string(l.CustomerRows[0].Record), "]}") + `,{"Amount":1200,"DetailType":"SubTotalLineDetail","SubTotalLineDetail":{}}]}`)
	l.GeneralLedger = json.RawMessage(`{"Header":{"ReportName":"GeneralLedger","Currency":"USD","ReportBasis":"Accrual","StartPeriod":"2026-09-01","EndPeriod":"2026-09-30"},"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Num"},{"ColTitle":"Name"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[{"type":"Section","Header":{"ColData":[{"id":"13","value":"Deferred Revenue"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":""},{"value":"Beginning Balance"},{"value":""},{"value":""},{"value":""},{"value":"0.00"}]},{"type":"Data","ColData":[{"value":"2026-09-03"},{"id":"41","value":"Invoice"},{"value":"REV-22"},{"id":"7","value":"Northwind"},{"value":"1200.00"},{"value":"1200.00"}]}]}}]}}`)

	plan, reason := StraightDeferred(p, []IntakeSource{invoice, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, l)
	if reason != "" {
		t.Fatal("the complete advance still needs confirmation:", reason)
	}
	var in prepaids.Input
	if json.Unmarshal(plan.Input, &in) != nil || in.Schedule != prepaids.DeferredRevenue || in.BookedBalance != "1200.00" {
		t.Fatalf("wrong deferred input: %+v", in)
	}
	raw, err := Calculate(plan, "Example", "2026-09")
	var paper struct {
		PeriodExpense  string               `json:"period_expense"`
		PrepaidBalance string               `json:"prepaid_balance"`
		Proposal       *prepaids.Adjustment `json:"proposal"`
	}
	if err != nil || json.Unmarshal(raw, &paper) != nil || paper.PeriodExpense != "100.00" || paper.PrepaidBalance != "1100.00" || paper.Proposal == nil || paper.Proposal.DebitAccount != "Deferred Revenue" || paper.Proposal.CreditAccount != "Subscription Revenue" {
		t.Fatalf("wrong recognition: %s %v", raw, err)
	}
	for _, tc := range []struct{ name, old, new string }{
		{"multiple charges", "Total USD 1,200.00", "Implementation USD 400.00\nSubscription USD 800.00\nTotal USD 1,200.00"},
		{"truncated customer", "Bill to: Northwind", "Bill to: Northwind Holdings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := invoice
			changed.Content = strings.ReplaceAll(invoice.Content, tc.old, tc.new)
			if _, why := StraightDeferred(p, []IntakeSource{changed, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, l); why == "" {
				t.Fatal("unsupported source was automatically recognized")
			}
		})
	}
	fullGL := l.GeneralLedger
	l.GeneralLedger = json.RawMessage(strings.Replace(string(fullGL), `"value":"0.00"`, `"value":"500.00"`, 1))
	if _, why := StraightDeferred(p, []IntakeSource{invoice, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, l); why == "" {
		t.Fatal("an earlier deferred balance was allocated to the new invoice")
	}
	l.GeneralLedger = fullGL
	l.CustomerRows = append(l.CustomerRows, LedgerRow{Kind: "CreditMemo", ProviderID: "55", Date: "2026-09-05", Currency: "USD", Record: json.RawMessage(`{"DocNumber":"CM-55","TotalAmt":5}`)})
	if _, why := StraightDeferred(p, []IntakeSource{invoice, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, l); why == "" {
		t.Fatal("a credit without customer identity was silently ignored")
	}
	l.CustomerRows = l.CustomerRows[:1]
	procedure.DeferredExample = false
	if _, why := StraightDeferred(p, []IntakeSource{invoice, {Source: rule, Content: instructions}}, procedure, rule, "USD", "Example", "2026-09", nil, false, l); why == "" {
		t.Fatal("a prepaid-only example authorized revenue recognition")
	}
}
