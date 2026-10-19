package preparation

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// StraightDeferred admits one new full advance, separately proved in the captured customer activity, GL and TB.
func StraightDeferred(p Proposal, selected []IntakeSource, procedure Procedure, rule Source, currency, entity, period string, aliases []string, unread bool, ledger *Ledger) (Plan, string) {
	if !llm.IsModelEngine(p.Engine) || p.Kind != "deferred_revenue" || len(p.Questions) > 0 || len(p.Lines) > 0 || unread {
		return Plan{}, "one agent-read customer invoice with no open questions or unread evidence is required"
	}
	if currency == "" || !strings.EqualFold(procedure.ClientEntity, entity) || rule.ID == "" || ledger == nil || ledger.ChartSource.ID == "" || ledger.ReportPeriod != period {
		return Plan{}, "apply the client's approved revenue policy and obtain current home-currency books"
	}
	machine := map[string]bool{rule.ID: true, ledger.Source.ID: true, ledger.ChartSource.ID: true}
	for id := range ledger.Machine {
		machine[id] = true
	}
	invoice := IntakeSource{}
	for _, source := range p.Sources {
		if source.Role == "invoice" {
			if invoice.ID != "" {
				return Plan{}, "more than one customer invoice needs a person"
			}
			for _, s := range selected {
				if s.ID == source.ID {
					invoice = s
					invoice.Source = source
				}
			}
		} else if !machine[source.ID] {
			return Plan{}, "additional customer support needs a person to weigh it"
		}
	}
	if invoice.ID == "" || len(documents.ImageDisputed(invoice.Content)) > 0 {
		return Plan{}, "one undisputed customer invoice is required"
	}
	for _, s := range p.Unselected {
		if !machine[s.ID] {
			return Plan{}, "evidence in the close (" + s.Name + ") was not used; a person confirms it is unrelated"
		}
	}
	facts, quotes := map[string]string{}, map[string]string{}
	for _, f := range p.Facts {
		if len(f.Candidates) == 0 {
			continue
		}
		if len(f.Candidates) != 1 || f.Value == "" || f.Candidates[0].Value != f.Value {
			return Plan{}, "each revenue fact must have one verified candidate"
		}
		source := invoice
		for _, s := range selected {
			if s.ID == f.Candidates[0].SourceID {
				source = s
			}
		}
		if source.ID != f.Candidates[0].SourceID {
			return Plan{}, "a cited source is unavailable"
		}
		if _, ok := documents.QuoteLocation(source.Name, source.Content, f.Candidates[0].Quote); !ok || !valueShown(f.Field, f.Value, f.Candidates[0].Quote) {
			return Plan{}, "a revenue fact's quote or value is not in its source"
		}
		want := invoice.ID
		if slices.Contains([]string{"expense_account", "prepaid_account"}, f.Field) {
			want = rule.ID
		}
		if slices.Contains([]string{"booked_balance", "booking_reference"}, f.Field) {
			want = ledger.ChartSource.ID
		}
		if source.ID != want {
			return Plan{}, "invoice facts, policy accounts and booked balances must keep their own source authority"
		}
		facts[f.Field], quotes[f.Field] = f.Value, f.Candidates[0].Quote
	}
	for _, field := range []string{"amount", "currency", "vendor", "billed_to", "invoice_number", "service_start", "service_end"} {
		if facts[field] == "" {
			return Plan{}, "confirm " + strings.ReplaceAll(field, "_", " ") + " from the customer invoice"
		}
	}
	if err := BilledToClient(facts["vendor"], entity, aliases); err != nil {
		return Plan{}, "the invoice issuer must be this client or its confirmed alias"
	}
	if CitedBilledTo(p, selected) == "" || !issuerLine(quotes["vendor"], facts["vendor"], invoice.Content) || !endsLine(quotes["invoice_number"], facts["invoice_number"]) || !lineSays(quotes["invoice_number"], facts["invoice_number"], []string{"invoice"}, []string{"order", "reference", "date", "due", "previous", "prior", "original", "related", "earlier", "last", "replaces", "credit"}) {
		return Plan{}, "the invoice must explicitly identify its issuer, customer and invoice number"
	}
	if facts["service_start"] != period+"-01" {
		return Plan{}, "this automatic path starts service on the first day of this close month"
	}
	for _, field := range []string{"service_start", "service_end"} {
		other, against := "service_end", []string{"end", "expir", "through", "until"}
		if field == "service_end" {
			other, against = "service_start", []string{"start", "begin", "effective", "commenc", "from"}
		}
		if !dateLineSays(quotes[field], facts[field], facts[other], []string{"service", "coverage", "period", "term", "subscription", "from"}, []string{"invoice date", "date of invoice", "issued", "issue date", "due", "payment", "paid", "order"}, against) {
			return Plan{}, "the invoice must explicitly state its full service period"
		}
	}
	amount, err := prepaids.Amount(facts["amount"])
	if err != nil || amount <= 0 || facts["currency"] != currency || negativeFigure(quotes["amount"], amount) || moneyFigures(quotes["amount"]) != 1 || !currencyBefore(quotes["amount"], currency, amount) || !currencyBefore(quotes["currency"], currency, amount) || !totalLineShows(invoice.Content, amount) || largestFigure(invoice.Content) > amount || totalDisagrees(invoice.Content, amount) != "" || chargeLine(invoice.Content) || len(distinctFigures(invoice.Content)) != 1 {
		return Plan{}, "one positive untaxed invoice total in home currency is required"
	}
	if mentionsAny(strings.ReplaceAll(strings.ToLower(invoice.Content), "credit card", ""), []string{"credit", "cancel", "refund", "adjust", "pro rata", "prorat", "terminat", "rebate", "write off", "revers", "amend", "estimate"}) {
		return Plan{}, "a revenue change, credit, cancellation or estimate needs a person"
	}
	value := func(label string) string {
		v, ok := procedureValue(procedure.Instructions, label)
		if !ok {
			return ""
		}
		return v
	}
	company, customer := value("QuickBooks company"), value("Customer ID for "+facts["billed_to"])
	id, idErr := strconv.ParseUint(customer, 10, 64)
	if company == "" || company != ledger.Realm || company != ledger.CaptureRealm || company != ledger.ChartRealm || idErr != nil || id == 0 || strconv.FormatUint(id, 10) != customer {
		return Plan{}, "the approved policy must map this customer to its ID in the current QuickBooks company"
	}
	input := prepaids.Input{Schedule: prepaids.DeferredRevenue, Entity: entity, ClosePeriod: period, Currency: currency, Amount: prepaids.Money(amount), ServiceStart: facts["service_start"], ServiceEnd: facts["service_end"], ExpenseAccount: value("Revenue account"), PrepaidAccount: value("Deferred revenue account"), BookedBalance: prepaids.Money(amount), BookingReference: "Current Trial Balance #" + ledger.ChartSource.ID, SourceReference: "Evidence #" + invoice.ID, Basis: "accrual", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	raw, _ := json.Marshal(input)
	plan := Plan{Kind: "prepaid", Procedure: procedure.ID, Chart: ledger.Chart, BilledTo: facts["billed_to"], Input: raw}
	if why := DeferredProcedurePinned(plan, procedure); why != "" {
		return Plan{}, why
	}
	for field, want := range map[string]string{"expense_account": input.ExpenseAccount, "prepaid_account": input.PrepaidAccount, "booked_balance": input.BookedBalance} {
		if got := facts[field]; got != "" && !strings.EqualFold(got, want) {
			return Plan{}, "the cited " + field + " conflicts with the approved policy or full advance"
		}
	}
	revenue, liability := ledger.IDs[strings.ToLower(input.ExpenseAccount)], ledger.IDs[strings.ToLower(input.PrepaidAccount)]
	if len(revenue) != 1 || len(liability) != 1 || !slices.Contains([]string{"Income", "Other Income"}, ledger.Types[revenue[0]]) || ledger.Types[liability[0]] != "Other Current Liability" {
		return Plan{}, "the policy must name unique current revenue and deferred-liability accounts"
	}
	booking, why := deferredAdvance(ledger, customer, facts["billed_to"], facts["invoice_number"], liability[0], currency, period, amount)
	if why != "" {
		return Plan{}, why
	}
	capture, chart := ledger.Source, ledger.ChartSource
	capture.Role, capture.Location = "period_ledger", booking
	chart.Role, chart.Location = "chart", "Full advance in account "+liability[0]+"; revenue account "+revenue[0]
	rule.Role, rule.Location = "procedure", "Version "+procedure.ID+"; approved equal-month deferred revenue"
	plan.Sources = []Source{invoice.Source, capture, chart, rule}
	if _, err := Calculate(plan, entity, period); err != nil {
		return Plan{}, err.Error()
	}
	return plan, ""
}

func DeferredProcedurePinned(p Plan, current Procedure) string {
	var in prepaids.Input
	if Strict(p.Input, &in) != nil || current.ID != p.Procedure || !current.Enabled || !current.DeferredExample || current.Kind != "prepaid" || current.Method != prepaids.Method || in.Schedule != prepaids.DeferredRevenue || in.Method != current.Method {
		return "activate a current equal-month revenue policy with an approved deferred-revenue example"
	}
	for label, want := range map[string]string{"Revenue account": in.ExpenseAccount, "Deferred revenue account": in.PrepaidAccount} {
		if got, ok := procedureValue(current.Instructions, label); !ok || want == "" || !strings.EqualFold(got, want) {
			return "the approved revenue policy must unambiguously name " + label
		}
	}
	return ""
}

// A zero opening GL, exactly one full customer invoice and an exact TB credit prove allocation without Item defaults.
func deferredAdvance(l *Ledger, customer, name, number, account, currency, period string, amount int64) (string, string) {
	fail := "the current customer capture, General Ledger and Trial Balance must prove this invoice's full advance with no earlier or other balance"
	invoiceID, invoiceDate := "", ""
	for _, r := range l.CustomerRows {
		var row struct {
			DocNumber    string
			TotalAmt     json.Number
			CustomerRef  accountRef
			TxnTaxDetail struct{ TotalTax json.Number }
			Line         []struct {
				Amount              json.Number
				DetailType          string
				SalesItemLineDetail *struct{ ItemRef accountRef }
				SubTotalLineDetail  *struct{}
			}
		}
		if json.Unmarshal(r.Record, &row) != nil || row.CustomerRef.Value == "" || row.CustomerRef.Name == "" {
			return "", fail
		}
		sameID, sameLabel := row.CustomerRef.Value == customer, sameName(row.CustomerRef.Name, name)
		if !sameID && !sameLabel && !strings.EqualFold(row.DocNumber, number) {
			continue
		}
		total, err := prepaids.Amount(row.TotalAmt.String())
		if len(row.Line) < 1 || len(row.Line) > 2 {
			return "", fail
		}
		sales := 0
		for i, line := range row.Line {
			lineAmount, lineErr := prepaids.Amount(line.Amount.String())
			if lineErr != nil || lineAmount != amount {
				return "", fail
			}
			switch {
			case line.SalesItemLineDetail != nil && line.SubTotalLineDetail == nil && (line.DetailType == "" || line.DetailType == "SalesItemLineDetail"):
				if line.SalesItemLineDetail.ItemRef.Value == "" {
					return "", fail
				}
				sales++
			case line.SubTotalLineDetail != nil && line.SalesItemLineDetail == nil && (line.DetailType == "" || line.DetailType == "SubTotalLineDetail"):
				if sales != 1 || i != len(row.Line)-1 {
					return "", fail
				}
			default:
				return "", fail
			}
		}
		if sales != 1 {
			return "", fail
		}
		tax := row.TxnTaxDetail.TotalTax.String()
		if !sameID || !sameLabel || r.Kind != "Invoice" || invoiceID != "" || !strings.EqualFold(row.DocNumber, number) || r.Currency != currency || !strings.HasPrefix(r.Date, period+"-") || err != nil || total != amount || (tax != "" && tax != "0" && tax != "0.00") {
			return "", fail
		}
		invoiceID, invoiceDate = r.ProviderID, r.Date
	}
	if invoiceID == "" {
		return "", fail
	}
	_, tbLocation, cells, err := quickbooks.TrialBalanceLiability(l.AccountCatalog, l.TrialBalance, account, currency)
	if err != nil {
		return "", err.Error()
	}
	var tb struct {
		Columns struct{ Column []struct{ ColTitle string } }
	}
	var values []struct{ Value string }
	if json.Unmarshal(l.TrialBalance, &tb) != nil || json.Unmarshal(cells, &values) != nil {
		return "", fail
	}
	debit, credit := -1, -1
	for i, c := range tb.Columns.Column {
		if c.ColTitle == "Debit" {
			debit = i
		}
		if c.ColTitle == "Credit" {
			credit = i
		}
	}
	if debit < 1 || credit < 1 || len(values) <= max(debit, credit) {
		return "", fail
	}
	debitValue := strings.TrimSpace(values[debit].Value)
	if debitValue == "" {
		debitValue = "0.00"
	}
	d, de := prepaids.Amount(strings.ReplaceAll(debitValue, ",", ""))
	c, ce := prepaids.Amount(strings.ReplaceAll(values[credit].Value, ",", ""))
	if de != nil || ce != nil || d != 0 || c != amount {
		return "", fail
	}
	var gl struct {
		Header  struct{ ReportName, Currency, ReportBasis, StartPeriod, EndPeriod string }
		Columns struct{ Column []struct{ ColTitle string } }
	}
	month, e := time.Parse("2006-01", period)
	if e != nil || json.Unmarshal(l.GeneralLedger, &gl) != nil || gl.Header.ReportName != "GeneralLedger" || gl.Header.Currency != currency || gl.Header.ReportBasis != "Accrual" || gl.Header.StartPeriod != period+"-01" || gl.Header.EndPeriod != month.AddDate(0, 1, -1).Format("2006-01-02") {
		return "", fail
	}
	at := map[string]int{}
	for i, c := range gl.Columns.Column {
		key := strings.ToLower(strings.TrimSpace(c.ColTitle))
		if _, exists := at[key]; exists {
			return "", fail
		}
		at[key] = i
	}
	for _, key := range []string{"date", "transaction type", "num", "name", "amount", "balance"} {
		if _, ok := at[key]; !ok {
			return "", fail
		}
	}
	lines, err := quickbooks.LedgerLinesOf(l.GeneralLedger)
	if err != nil {
		return "", err.Error()
	}
	opening, found := false, false
	location := ""
	for _, line := range lines {
		if line.AccountID != account {
			continue
		}
		var row struct {
			Type    string
			ColData []struct{ ID, Value string }
		}
		if json.Unmarshal(line.Cells, &row) != nil {
			return "", fail
		}
		if row.Type == "Section" && len(row.ColData) == 0 {
			continue
		}
		if row.Type != "Data" || len(row.ColData) != len(gl.Columns.Column) {
			return "", fail
		}
		cell := func(key string) string { return strings.TrimSpace(row.ColData[at[key]].Value) }
		value := func(key string) (int64, error) {
			return prepaids.Amount(strings.ReplaceAll(strings.TrimPrefix(cell(key), "-"), ",", ""))
		}
		if slices.ContainsFunc(row.ColData, func(c struct{ ID, Value string }) bool { return c.Value == "Beginning Balance" }) {
			balance, err := value("balance")
			if opening || found || err != nil || balance != 0 {
				return "", fail
			}
			opening = true
			continue
		}
		if cell("amount") == "" {
			return "", fail
		}
		a, e1 := value("amount")
		b, e2 := value("balance")
		if !opening || found || e1 != nil || e2 != nil || a != amount || b != amount || cell("transaction type") != "Invoice" || row.ColData[at["transaction type"]].ID != invoiceID || cell("num") != number || row.ColData[at["name"]].ID != customer || !sameName(cell("name"), name) || cell("date") != invoiceDate || strings.HasPrefix(cell("amount"), "-") != strings.HasPrefix(cell("balance"), "-") {
			return "", fail
		}
		found, location = true, line.Location
	}
	if !opening || !found {
		return "", fail
	}
	return location + "; " + tbLocation, ""
}
