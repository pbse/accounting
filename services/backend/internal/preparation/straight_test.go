package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
)

func TestStraightThroughNeedsOneVerifiedCandidatePerFactAndTheInvoiceExpensed(t *testing.T) {
	fact := func(field, value string) Fact {
		quote := "Annual premium USD " + value
		if field == "currency" {
			quote = "Annual premium USD 1200.00"
		}
		if strings.HasPrefix(field, "service_") {
			quote = "Coverage period " + value
		}
		return Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value, Candidates: []Candidate{{Value: value, SourceID: "1", Location: "line 1", Quote: quote}}}
	}
	optionalFact := func(field, value string) Fact {
		f := fact(field, value)
		f.Optional = true
		return f
	}
	vendorFact := optionalFact("vendor", "Acme Insurance Co")
	vendorFact.Candidates[0].Quote = "Acme Insurance Co"
	billedTo := optionalFact("billed_to", "Example")
	billedTo.Candidates[0].Quote = "Bill to: Example"
	invoiceNumber := optionalFact("invoice_number", "INV-778")
	invoiceNumber.Candidates[0].Quote = "Invoice INV-778"
	proposal := Proposal{Kind: "prepaid", Engine: "openai", Sources: []Source{{ID: "1", Digest: "d", Name: "insurance.pdf", Role: "invoice"}},
		Facts: []Fact{fact("amount", "1200.00"), fact("service_start", "2026-09-01"), fact("service_end", "2027-08-31"), fact("currency", "USD"), {Field: "expense_account", Label: "expense account"}, {Field: "prepaid_account", Label: "prepaid account"}, {Field: "booked_balance", Label: "booked balance", Optional: true}, vendorFact, billedTo, invoiceNumber}}
	invoice := "Acme Insurance Co\nInvoice INV-778\nPO-123\nBill to: Example\nAnnual premium USD 1,200.00\nCoverage September 1, 2026 to August 31, 2027\n\nPay online with Stripe"
	rule := Source{ID: "3", Digest: "rule", Name: "Approved prepaid procedure v7.txt"}
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
		record, _ := json.Marshal(map[string]any{"DocNumber": number, "TotalAmt": json.Number(amount), who: map[string]string{"Name": vendor}, "Line": []map[string]any{{"Amount": json.Number(amount), detail: map[string]any{"AccountRef": map[string]string{"Name": account}}}}})
		return LedgerRow{Kind: kind, ProviderID: id, Date: date, Currency: "USD", Record: record}
	}
	bill := row("Bill", "41", "2026-09-03", "Acme Insurance Co", "INV-778", "Insurance", "1200")
	split := bill
	split.Record, _ = json.Marshal(map[string]any{"DocNumber": "INV-778", "TotalAmt": json.Number("1500"), "VendorRef": map[string]string{"Name": "Acme Insurance Co"}, "Line": []map[string]any{{"Amount": json.Number("1200"), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"Name": "Insurance"}}}, {"Amount": json.Number("300"), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"Name": "Prepaid Expenses"}}}}})
	refund := row("Purchase", "74", "2026-09-12", "Acme Insurance Co", "", "Insurance", "100")
	refund.Record = json.RawMessage(strings.Replace(string(refund.Record), `{"DocNumber"`, `{"Credit":true,"DocNumber"`, 1))
	line := func(account, amount string) map[string]any {
		return map[string]any{"Amount": json.Number(amount), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"Name": account}}}
	}
	discounted, twoAccounts, totalOff := bill, bill, bill
	discounted.Record, _ = json.Marshal(map[string]any{"DocNumber": "INV-778", "TotalAmt": json.Number("1100"), "VendorRef": map[string]string{"Name": "Acme Insurance Co"}, "Line": []map[string]any{line("Insurance", "1200"), line("Insurance", "-100")}})
	twoAccounts.Record, _ = json.Marshal(map[string]any{"DocNumber": "INV-778", "TotalAmt": json.Number("1240"), "VendorRef": map[string]string{"Name": "Acme Insurance Co"}, "Line": []map[string]any{line("Insurance", "1200"), line("Travel", "40")}})
	totalOff.Record, _ = json.Marshal(map[string]any{"DocNumber": "INV-778", "TotalAmt": json.Number("1100"), "VendorRef": map[string]string{"Name": "Acme Insurance Co"}, "Line": []map[string]any{line("Insurance", "1200")}})
	noTotal := bill
	noTotal.Record = json.RawMessage(`{"DocNumber":"INV-778","VendorRef":{"Name":"Acme Insurance Co"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"name":"Insurance"}}}]}`)
	foreign := row("JournalEntry", "65", "2026-09-30", "", "", "Insurance", "900")
	foreign.Currency = "CAD"
	ledger := func(rows ...LedgerRow) *Ledger {
		return &Ledger{Source: Source{ID: "9", Digest: "cap", Name: "QuickBooks transactions 2026-09"}, Chart: "31", ChartSource: Source{ID: "8", Digest: "chart", Name: "QuickBooks accounts 2026-09"}, Rows: rows, Accounts: map[string]string{"12": "Insurance", "13": "Prepaid Expenses"}, IDs: map[string][]string{"insurance": {"12"}, "prepaid expenses": {"13"}}}
	}
	plan, reason := StraightThrough(proposal, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill, row("Bill", "42", "2026-09-04", "Other Vendor", "", "Travel", "1200")))
	if reason != "" || plan.Kind != "prepaid" || len(plan.Sources) != 4 || plan.Sources[1].Role != "period_ledger" || plan.Sources[1].Digest != "cap" || !strings.Contains(plan.Sources[1].Location, "Bill 41") || plan.Sources[2].Role != "chart" || plan.Sources[2].ID != "8" || plan.Sources[3].Role != "procedure" || plan.Sources[3].ID != "3" {
		t.Fatal("expensed invoice not prepared with the capture cited", reason, plan.Sources)
	}
	var input prepaids.Input
	json.Unmarshal(plan.Input, &input)
	if input.BookedBalance != "" || !input.BookingConfirmed || input.Method != prepaids.Method || input.ExpenseAccount != "Insurance" || input.SourceReference != "Evidence #1 · work #5" || plan.Procedure != "7" || plan.Chart != "31" || plan.BilledTo != "Example" {
		t.Fatal("unexpected input", input, plan.Procedure)
	}
	if BilledToClient(plan.BilledTo, "Example", nil) != nil || BilledToClient("Example Services LLC", "Example", []string{"Example Services, LLC"}) != nil {
		t.Fatal("the billed-to name refused at preparation")
	}
	if BilledToClient("Example Services LLC", "Example", nil) == nil || BilledToClient("", "Example", nil) == nil {
		t.Fatal("a retired alias or empty name accepted at preparation")
	}
	if ProcedurePinned(plan, procedure) != nil {
		t.Fatal("the procedure it was prepared under refused")
	}
	for name, changed := range map[string]Procedure{"newer version": {ID: "8", Enabled: true, ExpenseAccount: "Insurance", PrepaidAccount: "Prepaid Expenses"}, "disabled": {ID: "7", Enabled: false, ExpenseAccount: "Insurance", PrepaidAccount: "Prepaid Expenses"}, "other account": {ID: "7", Enabled: true, ExpenseAccount: "Rent", PrepaidAccount: "Prepaid Expenses"}, "none": {}} {
		if ProcedurePinned(plan, changed) == nil {
			t.Fatal(name, "accepted at preparation")
		}
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill, row("Bill", "52", "2026-09-09", "Other Vendor", "", "Prepaid Expenses", "1200"))); reason != "" {
		t.Fatal("another vendor's prepaid bill blocked this invoice", reason)
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(row("Purchase", "51", "2026-09-03", "Acme Insurance Co", "INV-778", "Insurance", "1200"))); reason != "" {
		t.Fatal("purchase from the vendor not accepted", reason)
	}
	other := proposal
	other.Facts = append([]Fact(nil), proposal.Facts...)
	other.Facts[8] = optionalFact("billed_to", "Other Co")
	other.Facts[8].Candidates[0].Quote = "Bill to: Other Co"
	if _, reason = StraightThrough(other, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared an invoice billed to another company")
	}
	other.Facts[8] = optionalFact("billed_to", "Example Services LLC")
	other.Facts[8].Candidates[0].Quote = "Billed to Example Services LLC"
	if _, reason = StraightThrough(other, invoice, []string{"Example Services, LLC"}, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason != "" {
		t.Fatal("confirmed alias not accepted", reason)
	}
	issued := proposal
	issued.Facts = append([]Fact(nil), proposal.Facts...)
	issued.Facts[7] = optionalFact("vendor", "Acme Insurance Co")
	issued.Facts[7].Candidates[0].Quote = "From: Acme Insurance Co"
	if _, reason = StraightThrough(issued, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason != "" {
		t.Fatal("issuer-labelled vendor line not accepted", reason)
	}
	ranged := proposal
	ranged.Facts = append([]Fact(nil), proposal.Facts...)
	ranged.Facts[1] = fact("service_start", "2026-09-01")
	ranged.Facts[2] = fact("service_end", "2027-08-31")
	ranged.Facts[1].Candidates[0].Quote = "Coverage from 2026-09-01 through 2027-08-31"
	ranged.Facts[2].Candidates[0].Quote = "Coverage from 2026-09-01 through 2027-08-31"
	if _, reason = StraightThrough(ranged, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason != "" {
		t.Fatal("range line not accepted for both dates", reason)
	}
	if _, reason = StraightThrough(proposal, invoice+"\nDiscount USD 100.00\nTotal USD 1,100.00", nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared a line item when the invoice prints a discount and a lower total")
	}
	if _, reason = StraightThrough(proposal, invoice+"\nTotal 1,300", nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared against a bare-integer total that differs")
	}
	if _, reason = StraightThrough(proposal, invoice+"\nTotal USD 1,200.00", nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason != "" {
		t.Fatal("a total line printing the amount refused", reason)
	}
	if _, reason = StraightThrough(proposal, invoice+"\nTotal including tax USD 1,300.00", nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared a subtotal when the invoice prints a larger total")
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, Source{}, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared without the procedure applied to the close as evidence")
	}
	bare := ledger(bill)
	bare.ChartSource = Source{}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, bare); reason == "" {
		t.Fatal("prepared without a chart evidence entry")
	}
	withRule := proposal
	withRule.Sources = append([]Source{}, proposal.Sources...)
	withRule.Sources = append(withRule.Sources, Source{ID: "3", Digest: "rule", Name: rule.Name, Role: "support"}, Source{ID: "9", Digest: "cap", Name: "QuickBooks transactions 2026-09", Role: "support"})
	withRule.Unselected = []Source{{ID: "8", Digest: "chart", Name: "QuickBooks accounts 2026-09"}}
	if _, reason = StraightThrough(withRule, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(bill)); reason != "" {
		t.Fatal("the procedure copy, the capture and the chart refused as selected or unselected evidence", reason)
	}
	idOnly := bill
	idOnly.Record = json.RawMessage(`{"DocNumber":"INV-778","TotalAmt":1200,"VendorRef":{"Name":"Acme Insurance Co"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"12"}}}]}`)
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, rule, "USD", "Example", "2026-09", "5", false, ledger(idOnly)); reason != "" {
		t.Fatal("bill line naming its account by ID only not accepted", reason)
	}
	if _, reason = StraightThrough(proposal, invoice, nil, procedure, rule, "", "Example", "2026-09", "5", false, ledger(bill)); reason == "" {
		t.Fatal("prepared without a detected home currency")
	}
	for name, c := range map[string]struct {
		change func(*Proposal, *Procedure)
		ledger *Ledger
		unread bool
	}{
		"processor heads the invoice":   {func(*Proposal, *Procedure) {}, ledger(row("Bill", "79", "2026-09-03", "Stripe", "", "Insurance", "1200")), false},
		"vendor not read":               {func(p *Proposal, _ *Procedure) { p.Facts[7].Candidates = nil; p.Facts[7].Value = "" }, ledger(bill), false},
		"billed-to not read":            {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates = nil; p.Facts[8].Value = "" }, ledger(bill), false},
		"processor named in the body":   {func(*Proposal, *Procedure) {}, ledger(row("Bill", "70", "2026-09-03", "Stripe", "", "Insurance", "1200")), false},
		"line-less vendor credit":       {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "VendorCredit", ProviderID: "71", Date: "2026-09-10", Record: json.RawMessage(`{"VendorRef":{"Name":"Acme Insurance Co"}}`)}), false},
		"line-less journal":             {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "JournalEntry", ProviderID: "72", Date: "2026-09-10", Record: json.RawMessage(`{}`)}), false},
		"card refund":                   {func(*Proposal, *Procedure) {}, ledger(bill, refund), false},
		"unnamed account on a credit":   {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Purchase", ProviderID: "78", Date: "2026-09-10", Record: json.RawMessage(`{"Credit":true,"EntityRef":{"Name":"Someone"},"Line":[{"Amount":50,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"99"}}}]}`)}), false},
		"amount cited from support":     {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].SourceID = "2" }, ledger(bill), false},
		"line-less card credit":         {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Purchase", ProviderID: "77", Date: "2026-09-10", Record: json.RawMessage(`{"Credit":true,"EntityRef":{"Name":"Stripe"}}`)}), false},
		"line-less credit, other party": {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "VendorCredit", ProviderID: "92", Date: "2026-09-20", Record: json.RawMessage(`{"VendorRef":{"name":"Someone Else"}}`)}), false},
		"journal on an unlisted ID":     {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "JournalEntry", ProviderID: "93", Date: "2026-09-30", Record: json.RawMessage(`{"Line":[{"Amount":100,"JournalEntryLineDetail":{"AccountRef":{"value":"99","name":"Insurance"}}}]}`)}), false},
		"account inside a longer name": {func(p *Proposal, _ *Procedure) {
			p.Facts[4].Value = "Insurance"
			p.Facts[4].Candidates = []Candidate{{Value: "Insurance", SourceID: "1", Quote: "Expense account: Prepaid Insurance"}}
		}, ledger(bill), false},
		"support document selected": {func(p *Proposal, _ *Procedure) {
			p.Sources = append(p.Sources, Source{ID: "2", Digest: "x", Name: "credit-note.pdf", Role: "support"})
		}, ledger(bill), false},
		"undecodable capture row":      {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "JournalEntry", ProviderID: "95", Date: "2026-09-20", Record: json.RawMessage(`{"Line":{"Amount":1}}`)}), false},
		"deposit with no counterparty": {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Deposit", ProviderID: "94", Date: "2026-09-20", Record: json.RawMessage(`{"Line":[{"Amount":100,"DepositLineDetail":{"AccountRef":{"name":"Bank"}}}]}`)}), false},
		"dollar sign beside another code": {func(p *Proposal, _ *Procedure) {
			p.Facts[0].Candidates[0].Quote = "Total CAD $1,200.00 (USD accepted)"
			p.Facts[3].Candidates[0].Quote = "Total CAD $1,200.00 (USD accepted)"
		}, ledger(bill), false},
		"currency not before the figure": {func(p *Proposal, _ *Procedure) {
			p.Facts[0].Candidates[0].Quote = "Total CAD 1,200.00 (USD accepted)"
			p.Facts[3].Candidates[0].Quote = "Total CAD 1,200.00 (USD accepted)"
		}, ledger(bill), false},
		"credit with no counterparty":  {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Purchase", ProviderID: "91", Date: "2026-09-20", Record: json.RawMessage(`{"Credit":true,"Line":[{"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"name":"Travel"}}}]}`)}), false},
		"currency on another line":     {func(p *Proposal, _ *Procedure) { p.Facts[3].Candidates[0].Quote = "Payments accepted in USD" }, ledger(bill), false},
		"subtotal cited":               {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].Quote = "Subtotal USD 1,200.00" }, ledger(bill), false},
		"not billed to":                {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates[0].Quote = "Not billed to: Example" }, ledger(bill), false},
		"unicode minus":                {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].Quote = "Credit note total \u22121,200.00" }, ledger(bill), false},
		"two accounts share the name":  {func(*Proposal, *Procedure) {}, &Ledger{Source: Source{ID: "9", Digest: "cap"}, Rows: []LedgerRow{bill}, Accounts: map[string]string{"12": "Insurance", "14": "Insurance", "13": "Prepaid Expenses"}, IDs: map[string][]string{"insurance": {"12", "14"}, "prepaid expenses": {"13"}}}, false},
		"line ID is another account":   {func(*Proposal, *Procedure) {}, ledger(LedgerRow{Kind: "Bill", ProviderID: "90", Date: "2026-09-03", Record: json.RawMessage(`{"DocNumber":"INV-778","TotalAmt":1200,"VendorRef":{"name":"Acme Insurance Co"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"14"}}}]}`)}), false},
		"bill-from cited as billed-to": {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates[0].Quote = "Bill from: Example" }, ledger(bill), false},
		"end line cited as start":      {func(p *Proposal, _ *Procedure) { p.Facts[1].Candidates[0].Quote = "Coverage ends 2026-09-01" }, ledger(bill), false},
		"renamed account":              {func(*Proposal, *Procedure) {}, ledger(LedgerRow{Kind: "Bill", ProviderID: "89", Date: "2026-09-03", Record: json.RawMessage(`{"DocNumber":"INV-778","TotalAmt":1200,"VendorRef":{"name":"Acme Insurance Co"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"13","name":"Insurance"}}}]}`)}), false},
		"bill with ID-only vendor":     {func(*Proposal, *Procedure) {}, ledger(LedgerRow{Kind: "Bill", ProviderID: "87", Date: "2026-09-03", Record: json.RawMessage(`{"DocNumber":"INV-778","TotalAmt":1200,"VendorRef":{"value":"7"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"name":"Insurance"}}}]}`)}), false},
		"order date beside a service word": {func(p *Proposal, _ *Procedure) {
			p.Facts[1].Candidates[0].Quote = "Service: hosting\nOrder placed: 2026-09-01"
		}, ledger(bill), false},
		"processor cited as vendor": {func(p *Proposal, _ *Procedure) {
			p.Facts[7].Value = "Stripe"
			p.Facts[7].Candidates[0].Value = "Stripe"
			p.Facts[7].Candidates[0].Quote = "Payment processor: Stripe"
		}, ledger(row("Bill", "88", "2026-09-03", "Stripe", "INV-778", "Insurance", "1200")), false},
		"credit with ID-only vendor":     {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "VendorCredit", ProviderID: "85", Date: "2026-09-20", Record: json.RawMessage(`{"VendorRef":{"value":"7"},"Line":[{"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"name":"Travel"}}}]}`)}), false},
		"deposit with ID-only party":     {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Deposit", ProviderID: "86", Date: "2026-09-20", Record: json.RawMessage(`{"Line":[{"Amount":100,"DepositLineDetail":{"AccountRef":{"name":"Bank"},"Entity":{"value":"7"}}}]}`)}), false},
		"invoice date as service start":  {func(p *Proposal, _ *Procedure) { p.Facts[1].Candidates[0].Quote = "Invoice date: 2026-09-01" }, ledger(bill), false},
		"due date as service end":        {func(p *Proposal, _ *Procedure) { p.Facts[2].Candidates[0].Quote = "Due date 2027-08-31" }, ledger(bill), false},
		"previous invoice number":        {func(p *Proposal, _ *Procedure) { p.Facts[9].Candidates[0].Quote = "Previous invoice: INV-778" }, ledger(bill), false},
		"two figures in the quote":       {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].Quote = "Total $1,200; amount due $500" }, ledger(bill), false},
		"customer service footer":        {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates[0].Quote = "Customer Service: Example" }, ledger(bill), false},
		"negative printed amount":        {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].Quote = "Credit note total -1,200.00" }, ledger(bill), false},
		"bracketed printed amount":       {func(p *Proposal, _ *Procedure) { p.Facts[0].Candidates[0].Quote = "Adjustment (USD 1,200.00)" }, ledger(bill), false},
		"bill without a total":           {func(*Proposal, *Procedure) {}, ledger(noTotal), false},
		"billed-to inside another name":  {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates[0].Quote = "Bill to: OtherExample" }, ledger(bill), false},
		"invoice date cited as number":   {func(p *Proposal, _ *Procedure) { p.Facts[9].Candidates[0].Quote = "Invoice date: INV-778" }, ledger(bill), false},
		"vendor on a later deposit line": {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Deposit", ProviderID: "84", Date: "2026-09-15", Record: json.RawMessage(`{"Line":[{"Amount":5,"DepositLineDetail":{"AccountRef":{"name":"Bank"},"Entity":{"Name":"Someone"}}},{"Amount":100,"DepositLineDetail":{"AccountRef":{"name":"Bank"},"Entity":{"Name":"Acme Insurance Co"}}}]}`)}), false},
		"ship-to cited as billed-to":     {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates[0].Quote = "Ship to: Example" }, ledger(bill), false},
		"refund deposit":                 {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Deposit", ProviderID: "82", Date: "2026-09-15", Record: json.RawMessage(`{"Line":[{"Amount":100,"DepositLineDetail":{"AccountRef":{"name":"Insurance"},"Entity":{"Name":"Someone"}}}]}`)}), false},
		"deposit from the vendor":        {func(*Proposal, *Procedure) {}, ledger(bill, LedgerRow{Kind: "Deposit", ProviderID: "83", Date: "2026-09-15", Record: json.RawMessage(`{"Line":[{"Amount":100,"DepositLineDetail":{"AccountRef":{"name":"Bank"},"Entity":{"Name":"Acme Insurance Co"}}}]}`)}), false},
		"order number cited as invoice":  {func(p *Proposal, _ *Procedure) { p.Facts[9].Candidates[0].Quote = "Purchase order INV-778" }, ledger(bill), false},
		"discount line on the bill":      {func(*Proposal, *Procedure) {}, ledger(discounted), false},
		"other account on the bill":      {func(*Proposal, *Procedure) {}, ledger(twoAccounts), false},
		"total differs":                  {func(*Proposal, *Procedure) {}, ledger(totalOff), false},
		"same vendor, other invoice":     {func(*Proposal, *Procedure) {}, ledger(row("Bill", "81", "2026-09-03", "Acme Insurance Co", "INV-900", "Insurance", "1200")), false},
		"invoice number not read":        {func(p *Proposal, _ *Procedure) { p.Facts[9].Candidates = nil; p.Facts[9].Value = "" }, ledger(bill), false},
		"billed-to is a prefix":          {func(p *Proposal, _ *Procedure) { p.Facts[8].Candidates[0].Quote = "Bill to: Example Holdings" }, ledger(bill), false},
		"purchase order number":          {func(*Proposal, *Procedure) {}, ledger(row("Bill", "80", "2026-09-03", "Other Vendor", "PO-123", "Insurance", "1200")), false},
		"number is a prefix":             {func(p *Proposal, _ *Procedure) {}, ledger(row("Bill", "76", "2026-09-03", "Other Vendor", "INV-77", "Insurance", "1200")), false},
		"date as document number":        {func(*Proposal, *Procedure) {}, ledger(row("Bill", "75", "2026-09-03", "Other Vendor", "2026-09", "Insurance", "1200")), false},
		"year as document number":        {func(*Proposal, *Procedure) {}, ledger(row("Bill", "73", "2026-09-03", "Other Vendor", "2026", "Insurance", "1200")), false},
		"unread evidence":                {func(*Proposal, *Procedure) {}, ledger(bill), true},
		"partial prepaid line":           {func(*Proposal, *Procedure) {}, ledger(split), false},
		"foreign journal":                {func(*Proposal, *Procedure) {}, ledger(bill, foreign), false},
		"unnamed account":                {func(*Proposal, *Procedure) {}, ledger(bill, row("JournalEntry", "66", "2026-09-30", "", "", "", "10")), false},
		"no capture":                     {func(*Proposal, *Procedure) {}, nil, false},
		"no bill":                        {func(*Proposal, *Procedure) {}, ledger(), false},
		"other vendor, same amount":      {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-09-03", "Other Vendor", "77", "Insurance", "1200")), false},
		"bill outside month":             {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-08-31", "Acme Insurance Co", "INV-778", "Insurance", "1200")), false},
		"two bills":                      {func(*Proposal, *Procedure) {}, ledger(bill, row("Bill", "43", "2026-09-05", "Acme Insurance Co", "INV-778", "Insurance", "1200")), false},
		"booked to prepaid":              {func(*Proposal, *Procedure) {}, ledger(row("Bill", "41", "2026-09-03", "Acme Insurance Co", "INV-778", "Prepaid Expenses", "1200")), false},
		"journal moves it":               {func(*Proposal, *Procedure) {}, ledger(bill, row("JournalEntry", "60", "2026-09-30", "", "", "Prepaid Expenses", "1200")), false},
		"open question":                  {func(p *Proposal, _ *Procedure) { p.Questions = []string{"Which policy?"} }, ledger(bill), false},
		"unselected evidence":            {func(p *Proposal, _ *Procedure) { p.Unselected = []Source{{ID: "2", Name: "credit-note.pdf"}} }, ledger(bill), false},
		"currency not quoted":            {func(p *Proposal, _ *Procedure) { p.Facts[3].Candidates[0].Quote = "Annual premium 1,200.00" }, ledger(bill), false},
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
		if _, reason := StraightThrough(p, invoice, nil, q, rule, "USD", "Example", "2026-09", "5", c.unread, c.ledger); reason == "" {
			t.Fatal(name, "prepared without a person")
		}
	}
}
