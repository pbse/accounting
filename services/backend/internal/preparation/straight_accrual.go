package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/documents"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// StraightAccrual admits an unbilled, wholly incurred service against an explicitly zero liability. Nonzero balances
// need allocation by a person; a model cannot allocate an account's other accruals to this service.
func StraightAccrual(p Proposal, selected []IntakeSource, procedure Procedure, rule Source, currency, entity, period string, aliases []string, unread bool, other map[string]bool, ledger *Ledger) (Plan, string) {
	if !llm.IsModelEngine(p.Engine) || p.Kind != "accrual" || len(p.Questions) > 0 || len(p.Lines) > 0 {
		return Plan{}, "only an agent-read incurred service with no open questions can be prepared without confirmation"
	}
	if unread {
		return Plan{}, "evidence is not readable yet or belongs to another job's answer"
	}
	if currency == "" || !procedure.Enabled || procedure.Kind != "accrual" || procedure.Method != accruals.Method || !strings.EqualFold(procedure.ClientEntity, entity) || rule.ID == "" {
		return Plan{}, "apply a client-specific, example-approved fixed incurred accrual procedure in the client's home currency"
	}
	if ledger == nil || ledger.ChartSource.ID == "" || ledger.ReportPeriod != period {
		return Plan{}, "a current transaction capture, chart and close-month Trial Balance are required"
	}
	machine := map[string]bool{rule.ID: true, ledger.Source.ID: true, ledger.ChartSource.ID: true}
	for id := range ledger.Machine {
		machine[id] = true
	}
	invoice := IntakeSource{}
	for _, source := range p.Sources {
		var s IntakeSource
		for _, candidate := range selected {
			if candidate.ID == source.ID {
				s = candidate
				s.Source = source
				break
			}
		}
		if s.ID == "" {
			return Plan{}, "selected evidence is no longer available"
		}
		if s.Role == "invoice" {
			if invoice.ID != "" {
				return Plan{}, "more than one service document needs a person"
			}
			invoice = s
		} else if !machine[s.ID] {
			return Plan{}, "supporting evidence needs a person to weigh it"
		}
	}
	if invoice.ID == "" || len(documents.ImageDisputed(invoice.Content)) > 0 {
		return Plan{}, "one undisputed service document is required"
	}
	unrelated := []string{}
	for _, s := range p.Unselected {
		if !machine[s.ID] {
			if !other[s.ID] {
				return Plan{}, "evidence (" + s.Name + ") was not used; a person confirms it does not bear on this service"
			}
			unrelated = append(unrelated, s.ID)
		}
	}
	facts, quotes := map[string]string{}, map[string]string{}
	for _, f := range p.Facts {
		if len(f.Candidates) == 0 {
			continue
		}
		if len(f.Candidates) != 1 || f.Value == "" {
			return Plan{}, "the " + f.Label + " is not one source-verified candidate"
		}
		if f.Field == "booked_balance" || f.Field == "booking_reference" {
			if f.Candidates[0].SourceID != ledger.ChartSource.ID {
				return Plan{}, "the booked liability must be cited from the current Trial Balance"
			}
			if f.Field == "booked_balance" {
				value, err := prepaids.Amount(f.Value)
				if err != nil || value != 0 {
					return Plan{}, "the cited booked liability conflicts with the required zero balance"
				}
			}
			continue
		}
		source := invoice.ID
		if slices.Contains([]string{"expense_account", "liability_account", "reverse_next_month"}, f.Field) {
			source = rule.ID
		}
		if f.Candidates[0].SourceID != source {
			return Plan{}, "the " + f.Label + " must come from its own service document or approved procedure"
		}
		if slices.Contains([]string{"estimate_amount", "estimate_reference", "actual_reference"}, f.Field) {
			return Plan{}, "estimate true-ups need a separate reviewed method"
		}
		facts[f.Field], quotes[f.Field] = f.Value, f.Candidates[0].Quote
	}
	for _, field := range []string{"amount", "currency", "service_start", "service_end", "vendor", "invoice_number", "billed_to", "expense_account", "liability_account", "reverse_next_month"} {
		if facts[field] == "" {
			return Plan{}, "confirm " + strings.ReplaceAll(field, "_", " ") + " from the service document or approved procedure"
		}
	}
	if err := BilledToClient(facts["billed_to"], entity, aliases); err != nil {
		return Plan{}, err.Error()
	}
	if len(billToNames(invoice.Content)) != 1 || !labelledLine(quotes["billed_to"], facts["billed_to"], billToLabels, notBillTo) || !issuerLine(quotes["vendor"], facts["vendor"], invoice.Content) || !endsLine(quotes["invoice_number"], facts["invoice_number"]) {
		return Plan{}, "the document's issuer, number and billed client must be explicit"
	}
	if facts["currency"] != currency || !strings.HasPrefix(facts["service_start"], period+"-") || !strings.HasPrefix(facts["service_end"], period+"-") {
		return Plan{}, "the service must be wholly incurred in this close month and home currency"
	}
	for _, field := range []string{"service_start", "service_end"} {
		other := "service_end"
		if field == other {
			other = "service_start"
		}
		if !dateLineSays(quotes[field], facts[field], facts[other], []string{"service", "period", "incurred"}, []string{"invoice date", "due", "payment"}, nil) {
			return Plan{}, "the cited date must describe incurred service"
		}
	}
	company, companyOK := accrualPolicyValue(procedure.Instructions, "QuickBooks company")
	vendorID, vendorOK := accrualPolicyValue(procedure.Instructions, "Vendor ID for "+facts["vendor"])
	vendorNumber, vendorErr := strconv.ParseUint(vendorID, 10, 64)
	if !companyOK || company != ledger.Realm || company != ledger.CaptureRealm || company != ledger.ChartRealm || !vendorOK || vendorErr != nil || vendorNumber == 0 || strconv.FormatUint(vendorNumber, 10) != vendorID {
		return Plan{}, "the client's approved procedure must map this document's vendor to its ID in the current QuickBooks company"
	}
	amount, err := prepaids.Amount(facts["amount"])
	if err != nil || amount <= 0 || !printsAmount(quotes["amount"], amount) || !currencyBefore(quotes["amount"], currency, amount) || !currencyBefore(quotes["currency"], currency, amount) || !mentionsAny(quotes["amount"], []string{"unbilled"}) || negated(documents.NormalizeEntity(quotes["amount"])) || negativeFigure(quotes["amount"], amount) || moneyFigures(quotes["amount"]) != 1 {
		return Plan{}, "the source must explicitly state one positive unbilled service amount in home currency"
	}
	if largestFigure(invoice.Content) > amount || totalDisagrees(invoice.Content, amount) != "" || mentionsAny(invoice.Content, []string{"credit", "cancel", "refund", "adjust", "revers", "amend", "estimate", "pro rata", "prorat"}) {
		return Plan{}, "a conflicting amount, estimate or service change needs a person"
	}
	input := accruals.Input{Entity: entity, ClosePeriod: period, SourceReference: "Evidence #" + invoice.ID, ServiceStart: facts["service_start"], ServiceEnd: facts["service_end"], Amount: prepaids.Money(amount), BookedBalance: "0.00", EstimationBasis: "Vendor-stated unbilled amount, evidence #" + invoice.ID, ExpenseAccount: facts["expense_account"], LiabilityAccount: facts["liability_account"], Currency: currency, Basis: "accrual", Method: accruals.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	input.ReverseNextMonth, err = strconv.ParseBool(facts["reverse_next_month"])
	if err != nil {
		return Plan{}, "the approved procedure must explicitly choose true or false for reverse next month"
	}
	raw, _ := json.Marshal(input)
	plan := Plan{Kind: "accrual", Procedure: procedure.ID, Chart: ledger.Chart, BilledTo: facts["billed_to"], Input: raw, Unrelated: unrelated}
	if reason := AccrualProcedurePinned(plan, procedure); reason != "" {
		return Plan{}, reason
	}
	expenseIDs, liabilityIDs := ledger.IDs[strings.ToLower(input.ExpenseAccount)], ledger.IDs[strings.ToLower(input.LiabilityAccount)]
	if len(expenseIDs) != 1 || len(liabilityIDs) != 1 || ledger.Types[expenseIDs[0]] != "Expense" {
		return Plan{}, "the procedure's accounts must each name one current chart account, with an expense account"
	}
	_, location, cells, err := quickbooks.TrialBalanceLiability(ledger.AccountCatalog, ledger.TrialBalance, liabilityIDs[0], currency)
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
		return Plan{}, "the liability's debit and credit cells are required"
	}
	stated := false
	for _, cell := range values[1:] {
		if strings.TrimSpace(cell.Value) != "" {
			stated = true
			value, e := prepaids.Amount(strings.ReplaceAll(cell.Value, ",", ""))
			if e != nil || value != 0 {
				return Plan{}, "a nonzero booked liability needs a person to allocate it to this service"
			}
		}
	}
	if !stated {
		return Plan{}, "the liability row must explicitly state a zero balance"
	}
	// Unknown posting accounts cannot establish that this liability was untouched.
	clearAccount := func(ref accountRef) bool {
		ids := ledger.IDs[strings.ToLower(ref.Name)]
		if ref.Value != "" {
			if ledger.Accounts[ref.Value] == "" || ref.Name != "" && !slices.Contains(ids, ref.Value) {
				return false
			}
			return ref.Value != liabilityIDs[0]
		}
		return len(ids) == 1 && ids[0] != liabilityIDs[0]
	}
	for _, row := range ledger.Rows {
		if _, err := time.Parse("2006-01-02", row.Date); err != nil {
			return Plan{}, "a captured transaction date is unreadable"
		}
		var record struct {
			DocNumber                                 string
			VendorRef, EntityRef, DepositToAccountRef accountRef
			CashBack                                  *struct{ AccountRef accountRef }
			Line                                      []struct {
				AccountBasedExpenseLineDetail *struct{ AccountRef accountRef }
				JournalEntryLineDetail        *struct {
					AccountRef accountRef
					Entity     *struct{ EntityRef accountRef }
				}
				DepositLineDetail *struct {
					AccountRef accountRef
					Entity     accountRef
				}
			}
		}
		if json.Unmarshal(row.Record, &record) != nil {
			return Plan{}, "a captured transaction cannot be read"
		}
		if strings.EqualFold(strings.TrimSpace(record.DocNumber), facts["invoice_number"]) {
			return Plan{}, "the capture already contains this service's number; check its booking"
		}
		if !strings.HasPrefix(row.Date, period+"-") {
			continue
		}
		party := record.VendorRef.Name
		if party == "" {
			party = record.EntityRef.Name
		}
		if party == "" && (record.VendorRef.Value != "" || record.EntityRef.Value != "") {
			return Plan{}, "a captured counterparty has only an ID; check whether this service is already booked"
		}
		if record.VendorRef.Value == vendorID || record.EntityRef.Value == vendorID || sameName(party, facts["vendor"]) {
			return Plan{}, "the capture already contains this service's number or vendor; check its booking"
		}
		if row.Kind == "Deposit" && (!clearAccount(record.DepositToAccountRef) || record.CashBack != nil && !clearAccount(record.CashBack.AccountRef)) {
			return Plan{}, "a deposit's destination or cash-back account may affect the liability"
		}
		if len(record.Line) == 0 {
			return Plan{}, "a captured transaction has no readable posting lines"
		}
		for _, line := range record.Line {
			var account accountRef
			switch {
			case line.AccountBasedExpenseLineDetail != nil:
				account = line.AccountBasedExpenseLineDetail.AccountRef
			case line.JournalEntryLineDetail != nil:
				detail := line.JournalEntryLineDetail
				if detail.Entity == nil || detail.Entity.EntityRef.Name == "" || detail.Entity.EntityRef.Value == vendorID || sameName(detail.Entity.EntityRef.Name, facts["vendor"]) {
					return Plan{}, "a journal line's counterparty may relate to this service"
				}
				account = detail.AccountRef
			case line.DepositLineDetail != nil:
				account = line.DepositLineDetail.AccountRef
				party := line.DepositLineDetail.Entity
				if party.Name == "" || party.Value == vendorID || sameName(party.Name, facts["vendor"]) {
					return Plan{}, "a deposit's counterparty may relate to this service"
				}
			default:
				return Plan{}, "a captured transaction's posting account is unavailable (including item-based lines); check its booking"
			}
			if !clearAccount(account) {
				return Plan{}, "liability activity or an unnamed account needs a person to check its booking"
			}
		}
	}

	input.BookingReference = "Evidence #" + ledger.ChartSource.ID + " · " + location
	plan.Input, _ = json.Marshal(input)
	capture, chart := ledger.Source, ledger.ChartSource
	capture.Role, capture.Location = "period_ledger", "Current transaction capture; no matching service booking"
	chart.Role, chart.Location = "chart", location+" · explicit zero liability; expense account "+expenseIDs[0]
	rule.Role, rule.Location = "procedure", "Version "+procedure.ID
	plan.Sources = []Source{invoice.Source, capture, chart, rule}
	if _, err := Calculate(plan, entity, period); err != nil {
		return Plan{}, err.Error()
	}
	return plan, ""
}

// Account names and the reversal choice must be unambiguous labelled lines in the current approved policy, not
// guesses from a prior draft or instructions supplied by the service document.
func AccrualProcedurePinned(p Plan, current Procedure) string {
	var in accruals.Input
	if Strict(p.Input, &in) != nil || current.ID != p.Procedure || !current.Enabled || current.Kind != "accrual" || current.Method != accruals.Method || in.Method != current.Method {
		return "the fixed incurred accrual procedure changed; confirm the current policy"
	}
	for label, value := range map[string]string{"Expense account": in.ExpenseAccount, "Liability account": in.LiabilityAccount, "Reverse next month": strconv.FormatBool(in.ReverseNextMonth)} {
		if approved, ok := accrualPolicyValue(current.Instructions, label); !ok || !strings.EqualFold(approved, value) {
			return "the approved procedure must unambiguously state " + label + ": " + value
		}
	}

	return ""
}

// A zero books balance does not include unposted drafts. Another current accrual on this account must be resolved
// before a new zero-balance adjustment; a replacement may revise its own predecessor only.
func AccrualDrafted(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID string, p Plan) (string, error) {
	var in accruals.Input
	if err := Strict(p.Input, &in); err != nil {
		return "", err
	}
	var exists bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id::text=$1 AND w.kind='accrual' AND lower(btrim(w.paper->'input'->>'liability_account'))=lower(btrim($2)) AND w.id::text<>$3 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text))`, closeID, in.LiabilityAccount, p.Revises).Scan(&exists)
	if err != nil || !exists {
		return "", err
	}
	return "another current accrual draft already uses this liability account; resolve its allocation before preparing a new zero-balance adjustment", nil
}

func accrualPolicyValue(instructions, label string) (string, bool) {
	value, count := "", 0
	for _, line := range strings.Split(instructions, "\n") {
		key, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok && documents.NormalizeEntity(key) == documents.NormalizeEntity(label) {
			value = strings.TrimSpace(v)
			count++
		}
	}
	return value, count == 1 && value != ""
}
