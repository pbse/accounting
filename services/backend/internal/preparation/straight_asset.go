package preparation

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// StraightAsset prepares a newly capitalized purchase under an explicitly approved full-first-month asset policy.
// A nonzero shared accumulated-depreciation balance needs allocation by a person, never an inferred asset balance.
func StraightAsset(p Proposal, selected []IntakeSource, procedure Procedure, rule Source, currency, entity, period string, aliases []string, unread bool, ledger *Ledger) (Plan, string) {
	if !llm.IsModelEngine(p.Engine) || p.Kind != "depreciation" || len(p.Questions) > 0 || len(p.Lines) > 0 || unread {
		return Plan{}, "one agent-read asset purchase with no open questions or unread evidence is required"
	}
	if currency == "" || !strings.EqualFold(procedure.ClientEntity, entity) || rule.ID == "" || ledger == nil || ledger.ChartSource.ID == "" || ledger.ReportPeriod != period {
		return Plan{}, "apply the client's approved asset policy and obtain current home-currency books and chart"
	}
	machine := map[string]bool{rule.ID: true, ledger.Source.ID: true, ledger.ChartSource.ID: true}
	for id := range ledger.Machine {
		machine[id] = true
	}
	answers := map[string]IntakeSource{}
	for _, source := range selected {
		if source.PreparerAnswer {
			answers[source.ID] = source
		}
	}
	invoice := IntakeSource{}
	for _, source := range p.Sources {
		if source.Role == "invoice" {
			if invoice.ID != "" {
				return Plan{}, "more than one purchase invoice needs a person"
			}
			for _, s := range selected {
				if s.ID == source.ID {
					invoice = s
					invoice.Source = source
				}
			}
		} else if !machine[source.ID] && !answers[source.ID].PreparerAnswer {
			return Plan{}, "additional asset support needs a person to weigh it"
		}
	}
	if invoice.ID == "" || len(documents.ImageDisputed(invoice.Content)) > 0 {
		return Plan{}, "one undisputed purchase invoice is required"
	}
	for _, s := range p.Unselected {
		if !machine[s.ID] {
			return Plan{}, "evidence (" + s.Name + ") was not used; confirm whether it changes this asset"
		}
	}
	facts, quotes := map[string]string{}, map[string]string{}
	dateSource := IntakeSource{}
	for _, f := range p.Facts {
		if len(f.Candidates) == 0 {
			continue
		}
		if len(f.Candidates) != 1 || f.Value == "" || f.Candidates[0].Value != f.Value {
			return Plan{}, "the " + f.Field + " is not one verified candidate"
		}
		source := invoice
		for _, s := range selected {
			if s.ID == f.Candidates[0].SourceID {
				source = s
			}
		}
		if source.ID != f.Candidates[0].SourceID {
			return Plan{}, "a fact's cited source is unavailable"
		}
		if _, ok := documents.QuoteLocation(source.Name, source.Content, f.Candidates[0].Quote); !ok || !valueShown(f.Field, f.Value, f.Candidates[0].Quote) {
			return Plan{}, "a fact's value or quote is not in its source"
		}
		fromAnswer := f.Field == "service_start" && source.PreparerAnswer
		if slices.Contains([]string{"amount", "currency", "service_start", "vendor", "billed_to", "invoice_number"}, f.Field) && source.ID != invoice.ID && !fromAnswer {
			return Plan{}, "purchase identity, cost and in-service date must come from the invoice"
		}
		if source.ID != invoice.ID && source.ID != rule.ID && source.ID != ledger.ChartSource.ID && !fromAnswer {
			return Plan{}, "a fact comes from additional support that needs a person"
		}
		facts[f.Field], quotes[f.Field] = f.Value, f.Candidates[0].Quote
		if f.Field == "service_start" {
			dateSource = source
		}
	}
	for _, field := range []string{"amount", "currency", "service_start", "vendor", "billed_to", "invoice_number"} {
		if facts[field] == "" {
			return Plan{}, "confirm " + strings.ReplaceAll(field, "_", " ") + " from the purchase invoice"
		}
	}
	if err := BilledToClient(facts["billed_to"], entity, aliases); err != nil {
		return Plan{}, err.Error()
	}
	if len(billToNames(invoice.Content)) != 1 || !labelledLine(quotes["billed_to"], facts["billed_to"], billToLabels, notBillTo) || !issuerLine(quotes["vendor"], facts["vendor"], invoice.Content) || !endsLine(quotes["invoice_number"], facts["invoice_number"]) || !lineSays(quotes["invoice_number"], facts["invoice_number"], []string{"invoice"}, []string{"order", "reference", "date", "due", "prior", "related", "replaces", "credit"}) {
		return Plan{}, "the invoice must explicitly identify its issuer, invoice number and billed client"
	}
	start, err := time.Parse("2006-01-02", facts["service_start"])
	explicit := dateLineSays(quotes["service_start"], facts["service_start"], "", []string{"in service"}, []string{"invoice date", "due", "delivery", "order"}, nil)
	if dateSource.PreparerAnswer {
		explicit = assetAnswerDate(dateSource.Content, facts["service_start"])
	}
	if err != nil || !strings.HasPrefix(facts["service_start"], period+"-") || !explicit {
		return Plan{}, "the invoice or this job's preparer answer must explicitly state when the asset was placed in service in this close month"
	}
	for _, line := range strings.Split(invoice.Content, "\n") {
		normal := documents.NormalizeEntity(line)
		if strings.Contains(normal, "in service") && (negated(normal) || mentionsAny(line, []string{"expected", "planned", "anticipat", "proposed"}) || !documents.DateIn(line, facts["service_start"], false)) {
			return Plan{}, "the invoice gives a conflicting or unconfirmed in-service date"
		}
	}
	for _, answer := range answers {
		if !assetAnswerDate(answer.Content, facts["service_start"]) {
			return Plan{}, "a preparer answer is ambiguous or changes more than this asset's in-service date; a person weighs it"
		}
	}
	amount, err := prepaids.Amount(facts["amount"])
	if err != nil || amount <= 0 || facts["currency"] != currency || negativeFigure(quotes["amount"], amount) || moneyFigures(quotes["amount"]) != 1 || !currencyBefore(quotes["amount"], currency, amount) || !currencyBefore(quotes["currency"], currency, amount) || !totalLineShows(invoice.Content, amount) || largestFigure(invoice.Content) > amount || totalDisagrees(invoice.Content, amount) != "" {
		return Plan{}, "one positive invoice total in home currency is required"
	}
	if mentionsAny(strings.ReplaceAll(strings.ToLower(invoice.Content), "credit card", ""), []string{"credit", "cancel", "refund", "adjust", "dispos", "impair", "trade in", "trade-in", "rebate", "revers", "amend", "estimate", "pro rata", "prorat"}) {
		return Plan{}, "an asset change, credit, estimate or disposal needs a person"
	}
	policy := func(label string) string {
		if v, ok := procedureValue(procedure.Instructions, label); ok {
			return v
		}
		return ""
	}
	company, vendorID := policy("QuickBooks company"), policy("Vendor ID for "+facts["vendor"])
	id, idErr := strconv.ParseUint(vendorID, 10, 64)
	if company == "" || company != ledger.Realm || company != ledger.CaptureRealm || company != ledger.ChartRealm || idErr != nil || id == 0 || strconv.FormatUint(id, 10) != vendorID {
		return Plan{}, "the approved client policy must map this vendor to its ID in the current QuickBooks company"
	}
	months, err := strconv.Atoi(policy("Useful life months"))
	if err != nil || months < 1 || months > 120 {
		return Plan{}, "the asset policy must state a useful life from 1 to 120 months"
	}
	first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	input := prepaids.Input{Schedule: prepaids.Depreciation, Entity: entity, ClosePeriod: period, Currency: currency, Amount: prepaids.Money(amount), ServiceStart: first.Format("2006-01-02"), ServiceEnd: first.AddDate(0, months, -1).Format("2006-01-02"), SalvageValue: policy("Salvage value"), ExpenseAccount: policy("Depreciation expense account"), PrepaidAccount: policy("Accumulated depreciation account"), BookedBalance: "0.00", BookingReference: "Current Trial Balance #" + ledger.ChartSource.ID, SourceReference: "Evidence #" + invoice.ID + "; in service " + facts["service_start"] + "; approved full first month", Basis: "accrual", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	if dateSource.PreparerAnswer {
		input.SourceReference += "; date from answer #" + dateSource.ID
	}
	raw, _ := json.Marshal(input)
	plan := Plan{Kind: "prepaid", Procedure: procedure.ID, Chart: ledger.Chart, BilledTo: facts["billed_to"], Input: raw}
	if reason := AssetProcedurePinned(plan, procedure); reason != "" {
		return Plan{}, reason
	}
	for field, want := range map[string]string{"service_end": input.ServiceEnd, "salvage_value": input.SalvageValue, "expense_account": input.ExpenseAccount, "prepaid_account": input.PrepaidAccount, "booked_balance": "0.00"} {
		if got := facts[field]; got != "" {
			agrees := strings.EqualFold(got, want)
			if AmountField(field) {
				a, e1 := prepaids.Amount(got)
				b, e2 := prepaids.Amount(want)
				agrees = e1 == nil && e2 == nil && a == b
			}
			if !agrees {
				return Plan{}, "the cited " + field + " conflicts with the approved policy or current zero balance"
			}
		}
	}
	cost := policy("Asset cost account")
	costIDs, depIDs, accumIDs := ledger.IDs[strings.ToLower(cost)], ledger.IDs[strings.ToLower(input.ExpenseAccount)], ledger.IDs[strings.ToLower(input.PrepaidAccount)]
	if len(costIDs) != 1 || len(depIDs) != 1 || len(accumIDs) != 1 || costIDs[0] == accumIDs[0] || ledger.Types[costIDs[0]] != "Fixed Asset" || ledger.Types[accumIDs[0]] != "Fixed Asset" || !slices.Contains([]string{"Expense", "Other Expense"}, ledger.Types[depIDs[0]]) {
		return Plan{}, "the policy must name distinct current asset-cost, accumulated-depreciation and depreciation-expense accounts"
	}
	_, location, cells, err := quickbooks.TrialBalanceAccount(ledger.AccountCatalog, ledger.TrialBalance, accumIDs[0], currency, "Fixed Asset")
	if err != nil {
		return Plan{}, err.Error()
	}
	if _, _, err := quickbooks.TrialBalance(ledger.AccountCatalog, ledger.TrialBalance); err != nil {
		return Plan{}, err.Error()
	}
	var values []struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(cells, &values) != nil || len(values) != 3 {
		return Plan{}, "the accumulated-depreciation debit and credit cells are required"
	}
	stated := false
	for _, cell := range values[1:] {
		if strings.TrimSpace(cell.Value) != "" {
			stated = true
			v, e := prepaids.Amount(strings.ReplaceAll(cell.Value, ",", ""))
			if e != nil || v != 0 {
				return Plan{}, "a nonzero accumulated-depreciation account needs allocation to this asset by a person"
			}
		}
	}
	if !stated {
		return Plan{}, "the accumulated-depreciation row must explicitly state zero"
	}
	// Positive purchase matching uses both the invoice's vendor name and its approved provider ID. Identity conflicts
	// anywhere in the captured vendor activity are unresolved, even when a provider alias would hide a credit by name.
	for _, row := range ledger.Rows {
		var record struct {
			VendorRef, EntityRef accountRef
			Line                 []struct {
				DepositLineDetail      *struct{ Entity accountRef }
				JournalEntryLineDetail *struct {
					Entity *struct{ EntityRef accountRef }
				}
			}
		}
		if json.Unmarshal(row.Record, &record) != nil {
			return Plan{}, "a captured transaction cannot be read"
		}
		party := record.VendorRef
		if party.Value == "" && party.Name == "" {
			party = record.EntityRef
		}
		if (row.Kind == "Bill" || row.Kind == "Purchase" || row.Kind == "VendorCredit") && (party.Value == "" || party.Name == "") {
			return Plan{}, "captured purchases and credits must identify their counterparties by name and ID"
		}
		refs := []accountRef{record.VendorRef, record.EntityRef}
		for _, line := range record.Line {
			if line.DepositLineDetail != nil {
				if line.DepositLineDetail.Entity.Value == "" {
					return Plan{}, "a captured deposit has no counterparty ID"
				}
				refs = append(refs, line.DepositLineDetail.Entity)
			}
			if line.JournalEntryLineDetail != nil && line.JournalEntryLineDetail.Entity != nil {
				ref := line.JournalEntryLineDetail.Entity.EntityRef
				if ref.Value == vendorID || sameName(ref.Name, facts["vendor"]) {
					return Plan{}, "a journal for this asset vendor needs a person to weigh it"
				}
			}
		}
		for _, ref := range refs {
			if ref.Value == vendorID && !sameName(ref.Name, facts["vendor"]) || sameName(ref.Name, facts["vendor"]) && ref.Value != vendorID {
				return Plan{}, "captured vendor names and IDs disagree with the approved asset vendor mapping"
			}
		}
	}
	// The shared booking check holds activity on either account: both accumulated depreciation and depreciation
	// expense must be untouched, as well as the cost account. Reuse its provider-line validation for each pair.
	booking := ""
	for _, account := range []string{input.PrepaidAccount, input.ExpenseAccount} {
		var reason string
		booking, reason = expensedLine(ledger, invoice.Content, facts["vendor"], facts["invoice_number"], input.Amount, currency, period, cost, account)
		if reason != "" {
			return Plan{}, "asset capitalization: " + reason
		}
	}
	capture, chart := ledger.Source, ledger.ChartSource
	capture.Role, capture.Location = "period_ledger", booking
	chart.Role, chart.Location = "chart", location+"; zero accumulated depreciation; accounts "+strings.Join([]string{costIDs[0], depIDs[0], accumIDs[0]}, ", ")
	rule.Role, rule.Location = "procedure", "Version "+procedure.ID+"; approved full-first-month asset policy"
	plan.Sources = []Source{invoice.Source, capture, chart, rule}
	for _, source := range p.Sources {
		if answers[source.ID].PreparerAnswer {
			plan.Sources = append(plan.Sources, source)
		}
	}
	if _, err := Calculate(plan, entity, period); err != nil {
		return Plan{}, err.Error()
	}
	return plan, ""
}

func AssetProcedurePinned(p Plan, current Procedure) string {
	var in prepaids.Input
	if Strict(p.Input, &in) != nil || current.ID != p.Procedure || !current.Enabled || !current.AssetExample || current.Kind != "prepaid" || current.Method != prepaids.Method || in.Schedule != prepaids.Depreciation || in.Method != current.Method {
		return "activate a current equal-month asset policy with an approved depreciation example"
	}
	for label, value := range map[string]string{"Depreciation expense account": in.ExpenseAccount, "Accumulated depreciation account": in.PrepaidAccount, "Salvage value": in.SalvageValue, "Depreciation convention": "full_first_month"} {
		if approved, ok := procedureValue(current.Instructions, label); !ok || !strings.EqualFold(approved, value) {
			return "the approved asset policy must unambiguously state " + label + ": " + value
		}
	}
	start, e1 := time.Parse("2006-01-02", in.ServiceStart)
	end, e2 := time.Parse("2006-01-02", in.ServiceEnd)
	months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
	life, ok := procedureValue(current.Instructions, "Useful life months")
	if e1 != nil || e2 != nil || !ok || life != strconv.Itoa(months) {
		return "the asset useful life no longer matches the approved policy"
	}
	return ""
}

// A job's own factual answer can establish an in-service date, never a treatment or account choice.
func assetAnswerDate(body, date string) bool {
	dates := regexp.MustCompile(`(?i)(?:[0-9]{4}[-/][0-9]{1,2}[-/][0-9]{1,2}|[0-9]{1,2}[-/.][0-9]{1,2}[-/.][0-9]{2,4}|[a-z]{3,9}\.? [0-9]{1,2}(?:st|nd|rd|th)?,? [0-9]{4}|[0-9]{1,2}(?:st|nd|rd|th)? [a-z]{3,9} [0-9]{4})`).FindAllString(body, -1)
	if len(dates) != 1 || strings.Contains(" "+documents.NormalizeEntity(body)+" ", " or ") {
		return false
	}
	if moneyFigures(body) > 0 || negated(documents.NormalizeEntity(body)) || mentionsAny(body, []string{"maybe", "expect", "plan", "anticipat", "propos", "estimate", "uncertain", "unknown", "credit", "cancel", "refund", "adjust", "dispos", "impair", "salvage", "life", "convention", "account", "amount", "cost", "instead"}) {
		return false
	}
	for _, layout := range []string{"2006-01-02", "1/2/2006", "January 2, 2006", "Jan 2, 2006"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(body)); err == nil {
			return parsed.Format("2006-01-02") == date
		}
	}
	return dateLineSays(body, date, "", []string{"in service"}, []string{"invoice date", "due", "delivery", "order", "received"}, nil)
}
