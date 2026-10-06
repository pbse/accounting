package preparation

import (
	"close.local/backend/internal/llm"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// A captured QuickBooks transaction, as the straight-through check reads it.
type LedgerRow struct {
	Kind, ProviderID, Date, Currency string
	Record                           json.RawMessage
}

// Ledger is the close's current QuickBooks capture: its evidence entry, the rows the check reads, and the account
// names by ID and IDs by name from the same connection's newest chart of accounts.
type Ledger struct {
	Source      Source
	Chart       string // the quickbooks_snapshots row the accounts come from
	ChartSource Source // that snapshot's evidence entry, cited by the draft so a new chart reopens its review
	Rows        []LedgerRow
	Accounts    map[string]string   // account ID → name
	IDs         map[string][]string // lower-cased name or fully qualified name → account IDs
	Machine     map[string]bool     // every capture, period-snapshot and procedure-copy evidence entry of the close, current or older
}

// StraightThrough turns an agent proposal into a prepaid plan without a person confirming each field, when every
// fact the calculation needs is one verified candidate, the firm's enabled procedure fixes the method and accounts,
// and the current capture shows the invoice expensed: exactly one bill or purchase in the close month, from the
// invoice's vendor or bearing its number, books the amount to the procedure's expense account, and no journal in the
// month moves that amount on either account. The reason says why a proposal still needs a person. The draft it
// queues cites the capture and is reviewed like any other; nothing is approved here. Evidence in the close the agent
// did not use, or could not read, is left for a person, as the confirmation form asks, except the capture, the chart
// and the procedure copy, which this check reads itself.
func StraightThrough(p Proposal, invoice string, aliases []string, procedure Procedure, procedureSource Source, currency, entity, period, jobID string, unread bool, ledger *Ledger) (Plan, string) {
	if !llm.IsModelEngine(p.Engine) || p.Kind != "prepaid" {
		return Plan{}, "only an agent-read prepaid invoice can be prepared without confirmation"
	}
	if len(p.Questions) > 0 {
		return Plan{}, "the agent has open questions"
	}
	if unread {
		return Plan{}, "evidence in the close is not readable yet, or belongs to another job's request or answer"
	}
	if currency == "" {
		return Plan{}, "the client's home currency is not detected from a connected ledger"
	}
	if !procedure.Enabled || procedure.Kind != "prepaid" || procedure.Method != prepaids.Method || procedure.ExpenseAccount == "" || procedure.PrepaidAccount == "" {
		return Plan{}, "no enabled prepaid procedure names equal full months and both accounts for this client"
	}
	if procedureSource.ID == "" {
		return Plan{}, "the approved prepaid procedure has not been applied to this close as evidence"
	}
	if ledger == nil {
		return Plan{}, "the close has no current QuickBooks capture"
	}
	if ledger.ChartSource.ID == "" {
		return Plan{}, "the close's chart of accounts capture has no current evidence entry"
	}
	// Every fact comes from the invoice, so any other document in the close (a credit note, a reply), selected or not, is
	// something only a person can weigh. The capture, the chart and the procedure's evidence copy are checked here instead.
	machine := map[string]bool{procedureSource.ID: true, ledger.Source.ID: true, ledger.ChartSource.ID: true}
	for id := range ledger.Machine {
		machine[id] = true
	}
	for _, s := range p.Sources {
		if s.Role != "invoice" && !machine[s.ID] {
			return Plan{}, "supporting evidence (" + s.Name + ") was selected; a person reviews it"
		}
	}
	for _, s := range p.Unselected {
		if !machine[s.ID] {
			return Plan{}, "evidence in the close (" + s.Name + ") was not used; a person confirms it is unrelated"
		}
	}
	primary := ""
	for _, s := range p.Sources {
		if s.Role == "invoice" {
			primary = s.ID
		}
	}
	facts := map[string]string{}
	for _, f := range p.Facts {
		identity := slices.Contains(IdentityFacts, f.Field)
		if len(f.Candidates) == 0 {
			if (!f.Optional || identity) && f.Field != "expense_account" && f.Field != "prepaid_account" {
				return Plan{}, "the " + f.Label + " was not read from the source"
			}
			continue
		}
		if f.Optional && !identity {
			return Plan{}, "the source cites a " + f.Label + ", which needs a reviewed method"
		}
		if f.Value == "" || len(f.Candidates) != 1 {
			return Plan{}, "the " + f.Label + " has more than one candidate"
		}
		// A value only a supporting document gives may contradict what the invoice prints; the invoice itself must cite it.
		if f.Candidates[0].SourceID != primary {
			return Plan{}, "the " + f.Label + " is cited from a supporting document, not the invoice"
		}
		// Amounts and dates were checked against their quotes when the proposal was validated; names are checked here.
		if f.Field == "currency" && !strings.Contains(strings.ToLower(f.Candidates[0].Quote), strings.ToLower(f.Value)) {
			return Plan{}, "the cited text does not show the " + f.Label
		}
		if strings.HasSuffix(f.Field, "_account") && !accountLine(f.Candidates[0].Quote, f.Value) {
			return Plan{}, "the cited text does not show the " + f.Label + " as a whole account name"
		}
		// A name or number must be the whole of what its quoted line ends with: "Example" is not what "Bill to: Example Holdings" names.
		if identity && !endsLine(f.Candidates[0].Quote, f.Value) {
			return Plan{}, "the cited text does not show the " + f.Label + " as a whole"
		}
		// An order or reference number is not the invoice number, and a ship-to name is not who is billed; the line must say which.
		if f.Field == "invoice_number" && !lineSays(f.Candidates[0].Quote, f.Value, []string{"invoice"}, []string{"order", "reference", "date", "due", "previous", "prior", "original", "related", "earlier", "last", "replaces", "credit"}) {
			return Plan{}, "the cited line does not name the number as the invoice number"
		}
		// A date is the service period's only when its quote speaks of the service, coverage or term, not the invoice, issue or due date.
		if strings.HasPrefix(f.Field, "service_") {
			// A line printing both dates is a range and serves either; otherwise a start line may not speak of an end, nor an end line of a start.
			other, against := "", []string{"end", "expir", "until", "through"}
			if f.Field == "service_end" {
				against = []string{"start", "begin", "effective", "commenc", "from"}
			}
			for _, g := range p.Facts {
				if strings.HasPrefix(g.Field, "service_") && g.Field != f.Field && len(g.Candidates) == 1 {
					other = g.Candidates[0].Value
				}
			}
			if !dateLineSays(f.Candidates[0].Quote, f.Value, other, []string{"service", "coverage", "cover", "period", "term", "effective", "start", "begin", "end", "expir", "through", "policy", "subscription", "from"}, []string{"invoice date", "date of invoice", "issued", "issue date", "due", "payment", "paid", "order"}, against) {
				return Plan{}, "the cited line does not give the " + f.Label + " as a service-period date"
			}
		}
		// The vendor is the issuer: a bare letterhead line, or one labelled as the issuer, never a processor, payee or recipient line.
		if f.Field == "vendor" && !issuerLine(f.Candidates[0].Quote, f.Value, invoice) {
			return Plan{}, "the cited line does not name the vendor as the invoice's issuer"
		}
		if f.Field == "billed_to" && !labelledLine(f.Candidates[0].Quote, f.Value, billToLabels, notBillTo) {
			return Plan{}, "the cited line does not say the invoice is billed to that name"
		}
		facts[f.Field] = f.Value
	}
	for field, account := range map[string]string{"expense_account": procedure.ExpenseAccount, "prepaid_account": procedure.PrepaidAccount} {
		if v, ok := facts[field]; ok && !strings.EqualFold(v, account) {
			return Plan{}, "the source names a different " + strings.ReplaceAll(field, "_", " ") + " than the procedure"
		}
	}
	// A booking in this client's ledger does not make the invoice this client's; the invoice must bill the client.
	if err := BilledToClient(facts["billed_to"], entity, aliases); err != nil {
		return Plan{}, err.Error()
	}
	// An invoice that bills two names bills neither for certain.
	if len(billToNames(invoice)) > 1 {
		return Plan{}, "the invoice prints more than one bill-to name; a person weighs who it bills"
	}
	if facts["currency"] != currency {
		return Plan{}, "the invoice is not in the client's home currency"
	}
	// The capture proves this month's booking only; a service that began earlier may already carry prepaid entries.
	if !strings.HasPrefix(facts["service_start"], period+"-") {
		return Plan{}, "the service began before the close month; earlier entries need a person"
	}
	amount, err := prepaids.Amount(facts["amount"])
	if err != nil || amount <= 0 {
		return Plan{}, "the amount is not a positive money value"
	}
	quotes := map[string]string{}
	for _, f := range p.Facts {
		if len(f.Candidates) == 1 {
			quotes[f.Field] = f.Candidates[0].Quote
		}
	}
	if negativeFigure(quotes["amount"], amount) {
		return Plan{}, "the invoice prints the amount as a negative or bracketed figure"
	}
	if moneyFigures(quotes["amount"]) != 1 {
		return Plan{}, "the cited text prints more than one money figure; which is the invoice amount needs a person"
	}
	// The amount is the invoice's total: not a subtotal, pre-tax, unit or partial figure, and no larger figure is printed anywhere.
	if mentionsAny(quotes["amount"], []string{"subtotal", "sub total", "before tax", "excl", "net of", "deposit", "instal", "partial", "unit", "per month", "monthly", "line", "limit", "balance forward"}) {
		return Plan{}, "the cited line gives a subtotal, partial or unit figure, not the invoice amount"
	}
	if larger := largestFigure(invoice); larger > amount {
		return Plan{}, "the invoice prints a larger money figure (" + prepaids.Money(larger) + ") than the cited amount"
	}
	if reason := totalDisagrees(invoice, amount); reason != "" {
		return Plan{}, reason
	}
	// Terms the schedule cannot carry, cited or not: a credit, cancellation, refund, adjustment or proration in the text.
	if mentionsAny(strings.ReplaceAll(strings.ToLower(invoice), "credit card", ""), []string{"credit", "cancel", "refund", "adjust", "pro rata", "prorat", "terminat", "rebate", "write off", "reversal"}) {
		return Plan{}, "the invoice speaks of a credit, cancellation, refund, adjustment or proration; a person weighs it"
	}
	// An invoice printing other money figures (a tax, a fee) must say which is payable: a total line printing the amount.
	if (moneyFigures(invoice) > 1 || chargeLine(invoice)) && !totalLineShows(invoice, amount) {
		return Plan{}, "the invoice prints several money figures, or a tax or fee, and no total line printing the cited amount"
	}
	// The currency is the amount's: both quotes print the currency's code or sign immediately before the figure.
	if !currencyBefore(quotes["amount"], currency, amount) || !currencyBefore(quotes["currency"], currency, amount) {
		return Plan{}, "the cited currency is not printed with the cited amount"
	}
	booking, reason := expensedLine(ledger, invoice, facts["vendor"], facts["invoice_number"], prepaids.Money(amount), currency, period, procedure.ExpenseAccount, procedure.PrepaidAccount)
	if reason != "" {
		return Plan{}, reason
	}
	input := prepaids.Input{Entity: entity, ClosePeriod: period, SourceReference: "Evidence #" + primary + " · work #" + jobID, Currency: currency, Amount: prepaids.Money(amount), ServiceStart: facts["service_start"], ServiceEnd: facts["service_end"],
		ExpenseAccount: procedure.ExpenseAccount, PrepaidAccount: procedure.PrepaidAccount, Basis: "accrual", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	raw, _ := json.Marshal(input)
	// The draft cites the capture, the chart and the procedure evidence, so a change to any of them reopens its review.
	capture, chart, rule := ledger.Source, ledger.ChartSource, procedureSource
	capture.Role, capture.Location = "period_ledger", booking
	chart.Role, chart.Location = "chart", "Accounts "+expenseIDsLabel(ledger, procedure)
	rule.Role, rule.Location = "procedure", "Version "+procedure.ID
	sources := []Source{}
	for _, s := range p.Sources {
		if s.ID != capture.ID && s.ID != chart.ID && s.ID != rule.ID {
			sources = append(sources, s)
		}
	}
	plan := Plan{Kind: "prepaid", Procedure: procedure.ID, Chart: ledger.Chart, BilledTo: facts["billed_to"], Input: raw, Sources: append(sources, capture, chart, rule)}
	if _, err := Calculate(plan, entity, period); err != nil {
		return Plan{}, err.Error()
	}
	return plan, ""
}

// expensedLine finds the one bill or purchase line in the close month that books the invoice amount to the expense
// account, nothing anywhere else, and belongs to the invoice: its vendor and document number are the invoice's cited vendor and number. Any prepaid line on that bill, a second match, any journal on either account in the month, a vendor or
// card credit from the vendor or on either account, or a line of a journal, credit or that vendor's record whose
// account cannot be named is a question for a person, whatever its currency.
func expensedLine(ledger *Ledger, invoice, vendor, number, amount, currency, period, expense, prepaid string) (booking, reason string) {
	text := strings.ToLower(invoice)
	// The procedure's accounts must each be one account in the current chart; a line carrying an ID is compared by ID.
	expenseIDs, prepaidIDs := ledger.IDs[strings.ToLower(expense)], ledger.IDs[strings.ToLower(prepaid)]
	if len(expenseIDs) != 1 || len(prepaidIDs) != 1 {
		return "", "the current chart of accounts does not name the procedure's expense and prepaid accounts exactly once each"
	}
	matches := 0
	for _, row := range ledger.Rows {
		if !strings.HasPrefix(row.Date, period+"-") {
			continue
		}
		var record struct {
			Credit               bool
			DocNumber            string
			TotalAmt             json.Number
			VendorRef, EntityRef accountRef
			DepositToAccountRef  accountRef
			CashBack             *struct{ AccountRef accountRef }
			Line                 []struct {
				Amount                        json.Number
				AccountBasedExpenseLineDetail *struct{ AccountRef accountRef }
				JournalEntryLineDetail        *struct{ AccountRef accountRef }
				DepositLineDetail             *struct {
					AccountRef accountRef
					Entity     accountRef
				}
			}
		}
		if json.Unmarshal(row.Record, &record) != nil {
			return "", "a captured " + row.Kind + " in the close month could not be read"
		}
		party := record.VendorRef.Name
		if party == "" {
			party = record.EntityRef.Name
		}
		// A bill bearing the invoice's number whose vendor carries only an ID cannot be tied to the vendor here.
		if party == "" && (record.VendorRef.Value != "" || record.EntityRef.Value != "") && (row.Kind == "Bill" || row.Kind == "Purchase") && !record.Credit && strings.EqualFold(strings.TrimSpace(record.DocNumber), strings.TrimSpace(number)) {
			return "", "the captured " + row.Kind + " bearing this invoice number does not name its vendor"
		}
		// A credit whose counterparty is missing or carries only an ID may be this vendor's; nothing here can tell.
		if party == "" && (row.Kind == "VendorCredit" || record.Credit) {
			return "", "a captured " + row.Kind + " in the close month does not name its counterparty"
		}
		mentioned := printed(text, party)
		// A Purchase's counterparty may be a customer or an employee; only a vendor's purchase is the invoice's booking.
		fromVendor := party != "" && sameName(party, vendor) && (row.Kind != "Purchase" || record.VendorRef.Name != "" || strings.EqualFold(record.EntityRef.Type, "Vendor"))
		mine := fromVendor && strings.EqualFold(strings.TrimSpace(record.DocNumber), strings.TrimSpace(number))
		if len(record.Line) == 0 && (row.Kind == "JournalEntry" || row.Kind == "Deposit" || row.Kind == "VendorCredit" || record.Credit || mine) {
			return "", "a captured " + row.Kind + " in the close month has no readable lines"
		}
		// A deposit also moves money through the account it is deposited to and any cash back; both count as its accounts.
		if row.Kind == "Deposit" {
			heads := []accountRef{record.DepositToAccountRef}
			if record.CashBack != nil {
				heads = append(heads, record.CashBack.AccountRef)
			}
			for i, head := range heads {
				name := head.Name
				if current, known := ledger.Accounts[head.Value]; known && head.Value != "" {
					name = current
				} else if head.Value != "" {
					return "", "a captured Deposit in the close month is deposited to an account the current chart of accounts does not list"
				}
				if name == "" && head.Value == "" && i == 0 {
					return "", "a captured Deposit in the close month does not name the account it is deposited to"
				}
				if strings.EqualFold(name, expense) || strings.EqualFold(name, prepaid) {
					return "", "a deposit in the close month is deposited to the expense or prepaid account"
				}
			}
		}
		// The invoice's own bill must book the whole amount to the expense account and nothing else anywhere.
		var expensed int64
		other := false
		for _, line := range record.Line {
			var account accountRef
			if line.AccountBasedExpenseLineDetail != nil {
				account = line.AccountBasedExpenseLineDetail.AccountRef
			} else if line.JournalEntryLineDetail != nil {
				account = line.JournalEntryLineDetail.AccountRef
			} else if line.DepositLineDetail != nil {
				account = line.DepositLineDetail.AccountRef
			}
			// The current chart names an account by its ID; a printed name that disagrees means a rename since capture, and an
			// ID the chart does not know cannot be placed on a journal, credit, deposit or the invoice's own bill.
			name := account.Name
			relevant := row.Kind == "JournalEntry" || row.Kind == "Deposit" || row.Kind == "VendorCredit" || record.Credit || mine
			if current, known := ledger.Accounts[account.Value]; known && account.Value != "" {
				if name != "" && !strings.EqualFold(name, current) {
					return "", "a captured " + row.Kind + " in the close month names an account differently from the current chart of accounts"
				}
				name = current
			} else if account.Value != "" && relevant {
				return "", "a captured " + row.Kind + " in the close month posts to an account the current chart of accounts does not list"
			}
			onExpense, onPrepaid := strings.EqualFold(name, expense), strings.EqualFold(name, prepaid)
			if account.Value != "" {
				onExpense, onPrepaid = account.Value == expenseIDs[0], account.Value == prepaidIDs[0]
			}
			if name == "" && (row.Kind == "JournalEntry" || row.Kind == "Deposit" || row.Kind == "VendorCredit" || record.Credit || mentioned || fromVendor) {
				return "", "a captured " + row.Kind + " in the close month has a line whose account cannot be named"
			}
			if row.Kind == "JournalEntry" && (onExpense || onPrepaid) {
				return "", "a journal in the close month posts to the expense or prepaid account"
			}
			if line.DepositLineDetail != nil {
				if line.DepositLineDetail.Entity.Name == "" {
					return "", "a captured Deposit in the close month does not name a counterparty"
				}
				from := line.DepositLineDetail.Entity.Name
				mentioned, fromVendor = mentioned || printed(text, from), fromVendor || (from != "" && sameName(from, vendor))
			}
			if row.Kind == "Deposit" && (mentioned || fromVendor || onExpense || onPrepaid) {
				return "", "a deposit in the close month may refund what this invoice expensed"
			}
			if row.Kind == "VendorCredit" && (mentioned || fromVendor || onExpense || onPrepaid) {
				return "", "a vendor credit in the close month may change what this invoice expensed"
			}
			if row.Kind == "Purchase" && record.Credit && (mentioned || fromVendor || onExpense || onPrepaid) {
				return "", "a card credit in the close month may change what this invoice expensed"
			}
			if (row.Kind != "Bill" && row.Kind != "Purchase") || !mine {
				continue
			}
			if onPrepaid {
				return "", "a captured " + row.Kind + " from this vendor books to the prepaid account; its balance needs a person"
			}
			value, err := prepaids.Amount(line.Amount.String())
			if err != nil || record.Credit || (row.Currency != "" && row.Currency != currency) {
				return "", "a captured " + row.Kind + " for this invoice has a line a person must read"
			}
			if onExpense {
				expensed += value
			} else {
				other = true
			}
		}
		if !mine {
			continue
		}
		total, err := prepaids.Amount(record.TotalAmt.String())
		if other || prepaids.Money(expensed) != amount || err != nil || prepaids.Money(total) != amount {
			return "", "a captured " + row.Kind + " for this invoice does not book exactly its amount to the expense account"
		}
		matches++
		booking = fmt.Sprintf("QuickBooks %s %s dated %s, %s %s to %s", row.Kind, row.ProviderID, row.Date, currency, amount, expense)
	}
	switch matches {
	case 0:
		return "", "no captured bill or purchase in the close month from this vendor bears this invoice number"
	case 1:
		return booking, ""
	}
	return "", "more than one captured bill or purchase in the close month bears this invoice number"
}

type accountRef struct {
	Value string `json:"value"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

// moneyFigures counts the money figures a quote prints: a number with cents, or one a currency sign or code precedes.
// Bare integers such as a year or an invoice number do not count.
func moneyFigures(quote string) int {
	plain := regexp.MustCompile(`(?i)(usd|eur|gbp|cad|aud|[$€£])\s*`).ReplaceAllString(quote, "¤")
	count := 0
	for _, figure := range regexp.MustCompile(`¤?\d[\d,]*(\.\d{2})?`).FindAllString(plain, -1) {
		if strings.HasPrefix(figure, "¤") || strings.Contains(figure, ".") {
			count++
		}
	}
	return count
}

// currencyBefore reports whether the quote prints the currency's code or sign immediately before the amount.
func currencyBefore(quote, currency string, cents int64) bool {
	signs := map[string]string{"USD": `\$`, "CAD": `\$|c\$|ca\$`, "AUD": `\$|a\$`, "EUR": `€`, "GBP": `£`}
	code := strings.ToLower(currency)
	sign, ok := signs[strings.ToUpper(currency)]
	if !ok {
		sign = code
	}
	plain := strings.ReplaceAll(strings.ToLower(quote), ",", "")
	// A quote naming another currency anywhere cannot vouch for this one by a bare sign: "CAD $1,200.00 (USD accepted)".
	for other := range signs {
		if strings.ToLower(other) != code && regexp.MustCompile(`(^|[^a-z])`+strings.ToLower(other)+`([^a-z]|$)`).MatchString(plain) {
			return false
		}
	}
	digits := fmt.Sprintf(`%d(\.%02d)?([^0-9]|$)`, cents/100, cents%100)
	// The code or sign must precede the figure on its own line. A dollar sign is shared by several currencies, so it
	// vouches for one only with the code on the same line; the euro and pound signs stand alone.
	for _, line := range strings.Split(plain, "\n") {
		byCode := regexp.MustCompile(code + `\s*` + digits).MatchString(line)
		bySign := regexp.MustCompile(`(` + sign + `)\s*` + digits).MatchString(line)
		codeOnLine := regexp.MustCompile(`(^|[^a-z])` + code + `([^a-z]|$)`).MatchString(line)
		if byCode || (bySign && (codeOnLine || !strings.Contains(sign, `\$`))) {
			return true
		}
	}
	return false
}

// expenseIDsLabel names the chart accounts the draft was checked against.
func expenseIDsLabel(ledger *Ledger, procedure Procedure) string {
	return strings.Join(append(ledger.IDs[strings.ToLower(procedure.ExpenseAccount)], ledger.IDs[strings.ToLower(procedure.PrepaidAccount)]...), ", ")
}

// BilledToClient refuses a billed-to name that is not the client's name or one of its confirmed, unretired aliases,
// compared ignoring case, punctuation and spacing.
func BilledToClient(billed, entity string, aliases []string) error {
	want := documents.NormalizeEntity(billed)
	if want == "" || (want != documents.NormalizeEntity(entity) && !slices.ContainsFunc(aliases, func(alias string) bool { return documents.NormalizeEntity(alias) == want })) {
		return fmt.Errorf("The invoice is billed to %q, not the client or a confirmed alias; a person confirms the facts", billed)
	}
	return nil
}

// ProcedurePinned refuses a plan prepared under a procedure version that is no longer the client's current, enabled
// prepaid procedure with the accounts the plan uses.
func ProcedurePinned(p Plan, current Procedure) error {
	var input struct {
		ExpenseAccount string `json:"expense_account"`
		PrepaidAccount string `json:"prepaid_account"`
	}
	json.Unmarshal(p.Input, &input)
	if current.ID != p.Procedure || !current.Enabled || !strings.EqualFold(current.ExpenseAccount, input.ExpenseAccount) || !strings.EqualFold(current.PrepaidAccount, input.PrepaidAccount) {
		return fmt.Errorf("The procedure this draft was prepared under (v%s) is no longer the client's current enabled procedure with these accounts; a person confirms the facts", p.Procedure)
	}
	return nil
}

// printsDate reports whether a line prints a date in numeric form.
func printsDate(line string) bool {
	return regexp.MustCompile(`\d{4}-\d{2}-\d{2}|\d{1,2}/\d{1,2}/\d{2,4}|\d{1,2}\.\d{1,2}\.\d{4}`).MatchString(line)
}

// totalDisagrees reports why the invoice's own total, due or payable lines do not support the cited amount: a discount
// anywhere, or such a line printing a different or negative figure.
func totalDisagrees(invoice string, cents int64) string {
	for _, line := range strings.Split(invoice, "\n") {
		normal := documents.NormalizeEntity(line)
		if strings.Contains(normal, "discount") {
			return "the invoice prints a discount; the payable total needs a person"
		}
		// A total, due or payable line states what is owed; a subtotal does not.
		// A due date is a date, not an amount due: a due line counts only when it prints no date and either prints a money
		// figure or calls itself an amount or balance ("Amount due 1300").
		dueAmount := (strings.Contains(normal, " due") || strings.HasPrefix(normal, "due")) && !strings.Contains(normal, "date") && (moneyFigures(line) > 0 || ((strings.Contains(normal, "amount") || strings.Contains(normal, "balance")) && !printsDate(line)))
		// "Total tax" or "Total fees" is that charge's total, not what is payable.
		charge := mentionsAny(line, chargeWords)
		payable := !charge && ((strings.Contains(normal, "total") && !strings.Contains(normal, "subtotal") && !strings.Contains(normal, "sub total")) || dueAmount || strings.Contains(normal, "payable"))
		if !payable {
			continue
		}
		if negativeFigure(line, cents) {
			return "a total line of the invoice prints the amount as a credit"
		}
		// A payable line printing money figures is judged by those figures alone ("Amount due 2026-10-01 USD 1,100.00" says
		// 1,100.00); one printing none is judged by every number, cents or not ("Total 1,300" is a total); one printing no
		// figure at all ("Total: to be determined") leaves the payable amount unresolved.
		if moneyFigures(line) > 0 {
			for value := range distinctFigures(line) {
				if value != cents {
					return "a total line of the invoice prints a figure other than the cited amount"
				}
			}
			continue
		}
		plain := strings.NewReplacer(",", "", "$", "", "€", "", "£", "").Replace(line)
		figures := regexp.MustCompile(`\d+(\.\d{1,2})?`).FindAllString(plain, -1)
		if len(figures) == 0 {
			return "a total line of the invoice prints no figure; the payable amount is unresolved"
		}
		for _, figure := range figures {
			if value, err := prepaids.Amount(figure); err != nil || value != cents {
				return "a total line of the invoice prints a figure other than the cited amount"
			}
		}
	}
	return ""
}

// distinctFigures is the set of money figures the invoice prints, in cents, read as largestFigure reads them.
func distinctFigures(invoice string) map[int64]bool {
	plain := regexp.MustCompile(`(?i)(usd|eur|gbp|cad|aud|[$€£])\s*`).ReplaceAllString(invoice, "¤")
	out := map[int64]bool{}
	for _, figure := range regexp.MustCompile(`¤?\d[\d,]*(\.\d{2})?`).FindAllString(plain, -1) {
		if !strings.HasPrefix(figure, "¤") && !strings.Contains(figure, ".") {
			continue
		}
		if cents, err := prepaids.Amount(strings.ReplaceAll(strings.TrimPrefix(figure, "¤"), ",", "")); err == nil {
			out[cents] = true
		}
	}
	return out
}

// totalLineShows reports whether a total line of the invoice (not a subtotal) prints the amount.
func totalLineShows(invoice string, cents int64) bool {
	for _, line := range strings.Split(invoice, "\n") {
		normal := documents.NormalizeEntity(line)
		if !mentionsAny(line, chargeWords) && (strings.Contains(normal, "total") && !strings.Contains(normal, "subtotal") && !strings.Contains(normal, "sub total") || (strings.Contains(normal, " due") && !strings.Contains(normal, "date")) || strings.Contains(normal, "payable")) && distinctFigures(line)[cents] {
			return true
		}
	}
	return false
}

// billToNames is the set of distinct names the invoice bills, read from lines whose label is a bill-to label.
// billToLabels name the line that says who an invoice bills; notBillTo mark a line that names someone else (an issuer,
// a ship-to or a contact).
var billToLabels, notBillTo = []string{"bill to", "billed to", "invoice to", "invoiced to", "sold to"}, []string{"from", "ship", "deliver", "service", "support", "contact"}

// CitedBilledTo is the billed-to name as the straight-through check accepts it: one candidate, cited from the invoice
// itself, on a line labelled as the bill-to that ends with the name, and that name the whole of a bill-to line the
// invoice's text prints (sources holds the read evidence), so a quote cut short ("Bill to: Harbor" from "Bill to:
// Harbor Fixture LLC") names no one. It is "" for anything less, so a weaker reading never has a held invoice called
// another company's.
func CitedBilledTo(p Proposal, sources []IntakeSource) string {
	primary, invoice := "", ""
	for _, s := range p.Sources {
		if s.Role == "invoice" {
			primary = s.ID
		}
	}
	for _, s := range sources {
		if s.ID == primary {
			invoice = s.Content
		}
	}
	for _, f := range p.Facts {
		if f.Field != "billed_to" || f.Value == "" || len(f.Candidates) != 1 || invoice == "" || f.Candidates[0].SourceID != primary {
			continue
		}
		quote := f.Candidates[0].Quote
		if endsLine(quote, f.Value) && labelledLine(quote, f.Value, billToLabels, notBillTo) && billToNames(invoice)[documents.NormalizeEntity(f.Value)] {
			return f.Value
		}
	}
	return ""
}

func billToNames(invoice string) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(invoice, "\n") {
		normal := documents.NormalizeEntity(line)
		for _, label := range billToLabels {
			if name := strings.TrimSpace(strings.TrimPrefix(normal, label)); strings.HasPrefix(normal, label+" ") && name != "" {
				names[name] = true
			}
		}
	}
	return names
}

// chargeWords name a tax, fee or similar charge on an invoice line.
var chargeWords = []string{"tax", "vat", "gst", "hst", "fee", "surcharge", "levy", "shipping", "freight", "handling", "duty"}

// chargeLine reports whether the invoice prints a tax, fee or similar charge with a number on its line, cents or not.
func chargeLine(invoice string) bool {
	for _, line := range strings.Split(invoice, "\n") {
		normal := documents.NormalizeEntity(line)
		if regexp.MustCompile(`\d`).MatchString(normal) && mentionsAny(line, chargeWords) {
			return true
		}
	}
	return false
}

// sameName compares two party names ignoring case, punctuation and spacing: "Acme Insurance Co." is "Acme Insurance Co".
func sameName(a, b string) bool {
	return documents.NormalizeEntity(a) != "" && documents.NormalizeEntity(a) == documents.NormalizeEntity(b)
}

// mentionsAny reports whether the quote, compared as text, contains any of the words.
func mentionsAny(quote string, words []string) bool {
	normal := documents.NormalizeEntity(quote)
	for _, word := range words {
		if strings.Contains(normal, word) {
			return true
		}
	}
	return false
}

// largestFigure is the largest money figure the invoice prints, in cents; zero when it prints none it can read.
func largestFigure(invoice string) int64 {
	plain := regexp.MustCompile(`(?i)(usd|eur|gbp|cad|aud|[$€£])\s*`).ReplaceAllString(invoice, "¤")
	var largest int64
	for _, figure := range regexp.MustCompile(`¤?\d[\d,]*(\.\d{2})?`).FindAllString(plain, -1) {
		if !strings.HasPrefix(figure, "¤") && !strings.Contains(figure, ".") {
			continue
		}
		if cents, err := prepaids.Amount(strings.ReplaceAll(strings.TrimPrefix(figure, "¤"), ",", "")); err == nil && cents > largest {
			largest = cents
		}
	}
	return largest
}

// negativeFigure reports whether the quote prints the amount preceded by a minus sign or an opening bracket, which
// the quote check ignores: a credit is not an invoice.
func negativeFigure(quote string, cents int64) bool {
	plain := strings.NewReplacer(",", "", "$", "", "€", "", "£", "", "−", "-", "–", "-", "—", "-", "‐", "-").Replace(strings.ToLower(quote))
	digits := fmt.Sprintf(`%d(\.%02d)?`, cents/100, cents%100)
	// A leading minus or bracket, or a trailing minus or "CR" in the accounting style, both mark a credit.
	return regexp.MustCompile(`[-(]\s*(usd|eur|gbp|cad|aud)?\s*`+digits+`([^0-9]|$)`).MatchString(plain) ||
		regexp.MustCompile(`(^|[^0-9.])`+digits+`\s*(-|cr)([^a-z0-9]|$)`).MatchString(plain)
}

// dateLineSays reports whether the line of the quote that prints the date contains one of the words that make it
// a service-period date, none of the words that make it another date, and, unless the line also prints the other
// end of the period, none of the words that point the other way.
func dateLineSays(quote, value, other string, any, none, against []string) bool {
	for _, line := range strings.Split(quote, "\n") {
		if !documents.DateIn(line, value, false) {
			continue
		}
		normal := documents.NormalizeEntity(line)
		ok := false
		for _, word := range any {
			ok = ok || strings.Contains(normal, word)
		}
		for _, word := range none {
			ok = ok && !strings.Contains(normal, word)
		}
		if other == "" || !documents.DateIn(line, other, false) {
			for _, word := range against {
				ok = ok && !strings.Contains(normal, word)
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// issuerLine reports whether the quoted line ending with the vendor's name is the invoice's letterhead line or one whose
// whole label names the issuer, and not one naming a payment, processor, payee, recipient or contact.
func issuerLine(quote, value, invoice string) bool {
	want := documents.NormalizeEntity(value)
	letterhead := ""
	for _, line := range strings.Split(invoice, "\n") {
		if strings.TrimSpace(line) != "" {
			letterhead = documents.NormalizeEntity(line)
			break
		}
	}
	for _, line := range strings.Split(quote, "\n") {
		normal := documents.NormalizeEntity(line)
		if !endsWithName(normal, want) {
			continue
		}
		label := strings.TrimSpace(strings.TrimSuffix(normal, want))
		if negated(label) {
			continue
		}
		// The label is the whole of what precedes the name: an issuer label, or nothing at all when the line is the
		// invoice's first, its letterhead. A bare name anywhere else (a footer, a payment line) proves nothing.
		// "Payment from" is not "invoice from".
		ok := label == "" && normal == letterhead
		for _, l := range []string{"invoice from", "bill from", "issued by", "billed by", "invoiced by", "vendor", "vendor name", "supplier", "supplier name", "seller", "provider", "issuer"} {
			ok = ok || label == l
		}
		for _, word := range []string{"payment", "processor", "pay to", "payable", "remit", "bill to", "billed to", "sold to", "ship", "customer", "client", "contact", "agent", "broker", "via", "online", "care of", "attn"} {
			ok = ok && !strings.Contains(label, word)
		}
		if ok {
			return true
		}
	}
	return false
}

// labelledLine reports whether the quoted line is one of the labels followed directly by the whole name, with no word
// between them and no word that denies or redirects the label: "Bill to: Example" names Example; "Bill to: Other
// Example" and "Bill from: Example" do not.
func labelledLine(quote, value string, labels, none []string) bool {
	want := documents.NormalizeEntity(value)
	for _, line := range strings.Split(quote, "\n") {
		normal := documents.NormalizeEntity(line)
		if !endsWithName(normal, want) {
			continue
		}
		label := strings.TrimSpace(strings.TrimSuffix(normal, want))
		if negated(label) {
			continue
		}
		// The label is the whole of what precedes the name: "Pay bill to" is not "Bill to".
		ok := false
		for _, l := range labels {
			ok = ok || label == l
		}
		for _, word := range none {
			ok = ok && !strings.Contains(label, word)
		}
		if ok {
			return true
		}
	}
	return false
}

// lineSays reports whether the quoted line ending with the value contains one of the words that give it its meaning
// and none of the words that give it another.
func lineSays(quote, value string, any, none []string) bool {
	want := documents.NormalizeEntity(value)
	for _, line := range strings.Split(quote, "\n") {
		normal := documents.NormalizeEntity(line)
		if !endsWithName(normal, want) {
			continue
		}
		// The words are looked for in the label before the name, so a client called "Example Services" is not a service line.
		label := strings.TrimSuffix(normal, want)
		ok := !negated(label)
		found := false
		for _, word := range any {
			found = found || strings.Contains(label, word)
		}
		ok = ok && found
		for _, word := range none {
			ok = ok && !strings.Contains(label, word)
		}
		if ok {
			return true
		}
	}
	return false
}

// negated reports whether a label denies what it says: "not billed to" is not a bill-to label.
func negated(label string) bool {
	for _, word := range strings.Fields(label) {
		if word == "not" || word == "no" || word == "never" || word == "except" || word == "formerly" || word == "previously" {
			return true
		}
	}
	return false
}

// accountLine reports whether a quoted line names the account as the whole of what follows an account label:
// "Expense account: Insurance" names Insurance; "Expense account: Prepaid Insurance" does not.
func accountLine(quote, value string) bool {
	want := documents.NormalizeEntity(value)
	for _, line := range strings.Split(quote, "\n") {
		normal := documents.NormalizeEntity(line)
		if !endsWithName(normal, want) {
			continue
		}
		words := strings.Fields(strings.TrimSuffix(normal, want))
		if len(words) == 0 {
			return true
		}
		switch words[len(words)-1] {
		case "account", "acct", "to", "code", "gl", "ledger":
			return true
		}
	}
	return false
}

// endsLine reports whether some line of the quote, compared as an entity name, ends with the whole value.
func endsLine(quote, value string) bool {
	want := documents.NormalizeEntity(value)
	if want == "" {
		return false
	}
	for _, line := range strings.Split(quote, "\n") {
		if endsWithName(documents.NormalizeEntity(line), want) {
			return true
		}
	}
	return false
}

// endsWithName reports whether a normalized line is the name or ends with it as whole words: "otherexample" does not end with "example".
func endsWithName(normal, want string) bool {
	return normal == want || strings.HasSuffix(normal, " "+want)
}

// printed reports whether the invoice prints the term as a whole word: "77" is not printed in "INV-778".
func printed(text, term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	if len(term) < 3 {
		return false
	}
	return regexp.MustCompile(`(^|[^0-9a-z])` + regexp.QuoteMeta(term) + `([^0-9a-z]|$)`).MatchString(text)
}

// OtherEvidence reports whether the close holds work evidence a person must weigh that this job's inventory cannot
// show: anything not yet readable, and a reply or answer belonging to another job of the same close.
func OtherEvidence(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID, jobID string) (bool, error) {
	var other bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM evidence e WHERE e.close_id::text=$1 AND e.purpose='work' AND e.superseded_by IS NULL AND (e.extraction_state<>'ready'
 OR EXISTS(SELECT 1 FROM work_replies r WHERE r.source_id=e.id AND NOT EXISTS(SELECT 1 FROM request_jobs q WHERE q.request_id=r.request_id AND q.job_id::text=$2))
 OR EXISTS(SELECT 1 FROM preparation_answers a WHERE a.source_id=e.id AND a.job_id::text<>$2)))`, closeID, jobID).Scan(&other)
	return other, err
}

// ProcedureEvidence is the close's current evidence copy of a procedure version, or an empty Source when it was never
// applied to the close.
func ProcedureEvidence(ctx context.Context, tx *sql.Tx, closeID, procedureID string) (Source, error) {
	var s Source
	err := tx.QueryRowContext(ctx, `SELECT e.id::text,e.digest,e.name FROM evidence e WHERE e.close_id::text=$1 AND e.procedure_id::text=$2 AND e.superseded_by IS NULL ORDER BY e.id DESC LIMIT 1`, closeID, procedureID).Scan(&s.ID, &s.Digest, &s.Name)
	if err == sql.ErrNoRows {
		return Source{}, nil
	}
	return s, err
}

// CloseLedger is the newest completed capture of the close month on the close's active reading connection, with its
// current evidence entry and its bill, purchase, journal, vendor credit and deposit rows (a newer sync that found the books
// unchanged reuses them); nil when a newer capture is still pending or failed,
// or there is none.
func CloseLedger(ctx context.Context, tx *sql.Tx, closeID string) (*Ledger, error) {
	var l Ledger
	var connection string
	var captured time.Time
	err := tx.QueryRowContext(ctx, `SELECT e.id::text,e.digest,e.name,s.connection_id::text,COALESCE(s.started_at,s.finished_at,now()) FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL JOIN quickbooks_connections x ON x.id=s.connection_id AND x.state='active'
 WHERE s.close_id::text=$1 AND s.state='completed' AND s.period=c.period AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.connection_id=s.connection_id AND z.state<>'unchanged')`, closeID).Scan(&l.Source.ID, &l.Source.Digest, &l.Source.Name, &connection, &captured)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.kind,t.provider_id,t.txn_date::text,COALESCE(t.currency,''),t.record FROM quickbooks_transaction_rows t JOIN quickbooks_entity_syncs s ON s.id=t.sync_id WHERE s.evidence_id::text=$1 AND t.kind IN ('Bill','Purchase','JournalEntry','VendorCredit','Deposit')`, l.Source.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.Kind, &r.ProviderID, &r.Date, &r.Currency, &r.Record); err != nil {
			return nil, err
		}
		l.Rows = append(l.Rows, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var catalog []byte
	// The chart must be at least as current as the transaction capture it is read beside; a chart refresh that failed
	// after a successful capture leaves no chart to trust.
	err = tx.QueryRowContext(ctx, `SELECT s.id::text,s.payload->'accounts',COALESCE(e.id::text,''),COALESCE(e.digest,''),COALESCE(e.name,'') FROM quickbooks_snapshots s LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id AND e.superseded_by IS NULL WHERE s.close_id::text=$1 AND s.connection_id::text=$2 AND s.payload ? 'period' AND COALESCE(s.captured_at,s.started_at)>=$3 ORDER BY s.id DESC LIMIT 1`, closeID, connection, captured).Scan(&l.Chart, &catalog, &l.ChartSource.ID, &l.ChartSource.Digest, &l.ChartSource.Name)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	// Captures, charts and every approved procedure copy attached to the close are context the check reads itself.
	machine, err := tx.QueryContext(ctx, `SELECT payload->>'source_id' FROM quickbooks_snapshots WHERE close_id::text=$1 AND payload ? 'source_id' UNION SELECT evidence_id::text FROM quickbooks_entity_syncs WHERE close_id::text=$1 AND evidence_id IS NOT NULL UNION SELECT id::text FROM evidence WHERE close_id::text=$1 AND procedure_id IS NOT NULL`, closeID)
	if err != nil {
		return nil, err
	}
	defer machine.Close()
	l.Machine = map[string]bool{}
	for machine.Next() {
		var id string
		if err := machine.Scan(&id); err != nil {
			return nil, err
		}
		l.Machine[id] = true
	}
	if err := machine.Err(); err != nil {
		return nil, err
	}
	var accounts []struct{ ID, Name, FullyQualifiedName string }
	json.Unmarshal(catalog, &accounts)
	l.Accounts, l.IDs = map[string]string{}, map[string][]string{}
	for _, a := range accounts {
		if a.ID == "" {
			continue
		}
		l.Accounts[a.ID] = a.Name
		for _, name := range []string{a.Name, a.FullyQualifiedName} {
			if name != "" && !slices.Contains(l.IDs[strings.ToLower(name)], a.ID) {
				l.IDs[strings.ToLower(name)] = append(l.IDs[strings.ToLower(name)], a.ID)
			}
		}
	}
	return &l, nil
}

// queueStraightThrough queues the plan on the job as the agent's confirmation. When the invoice replaced exactly one
// source that exactly one current prepaid draft cites, the plan revises that draft: the old draft is already marked as
// changed at source, the new one carries the lineage, and the change review shows only what moved. Any other lineage
// (two replaced sources, two drafts citing one) is a person's to resolve, and the reason says so.
func queueStraightThrough(ctx context.Context, tx *sql.Tx, closeID, jobID, invoiceID string, plan Plan) (string, error) {
	// A draft is revised automatically only when it cites a replaced source as its invoice and was itself prepared
	// straight through (its paper says so, or its job's intake was confirmed by the agent; a carried-forward draft never is); a draft a person confirmed keeps its
	// revision in a person's hands. The chain of replacements is followed, so a source replaced twice before its
	// replacement was read still leads to the current draft; two sources replaced by the same file anywhere along the chain,
	// or a chain longer than twenty replacements, are a person's to resolve.
	var branching, truncated bool
	var citing, eligible int
	var revises string
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE chain(id, parent, depth) AS (
  SELECT id, superseded_by, 1 FROM evidence WHERE close_id::text=$1 AND superseded_by::text=$2
  UNION ALL SELECT e.id, e.superseded_by, c.depth+1 FROM evidence e JOIN chain c ON e.superseded_by=c.id WHERE c.depth < 20),
 drafts AS (SELECT DISTINCT w.id, (COALESCE((w.paper->>'straight_through')::boolean,false) OR EXISTS(SELECT 1 FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.close_id=w.close_id AND j.workpaper_ids @> jsonb_build_array(w.id::text) AND i.confirmed_by='agent')) AND w.paper->'carry_forward' IS NULL AS straight FROM workpapers w, chain
  WHERE w.close_id::text=$1 AND w.kind='prepaid' AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',chain.id::text,'role','invoice'))
  AND NOT EXISTS(SELECT 1 FROM workpapers newer WHERE newer.close_id=w.close_id AND newer.paper->>'revises'=w.id::text))
 SELECT EXISTS(SELECT 1 FROM chain GROUP BY parent HAVING count(*)>1),EXISTS(SELECT 1 FROM chain WHERE depth>=20),(SELECT count(*) FROM drafts),(SELECT count(*) FROM drafts WHERE straight),COALESCE((SELECT max(id)::text FROM drafts WHERE straight),'')`, closeID, invoiceID).Scan(&branching, &truncated, &citing, &eligible, &revises)
	if err != nil {
		return "", err
	}
	switch {
	case branching || citing > 1:
		return "the replacement stands for more than one earlier source or draft; a person chooses which draft it revises", nil
	case truncated:
		return "the replacement chain is too long to follow; a person chooses which draft it revises", nil
	case citing == 1 && eligible == 0:
		return "the draft citing the replaced invoice was confirmed by a person; a person chooses its revision", nil
	case eligible == 0:
		revises = ""
	}
	// A predecessor whose entry was attempted or posted needs a correction against that entry, which a person prepares.
	if revises != "" {
		var posted bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM production_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM vendor_credit_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified')) OR EXISTS(SELECT 1 FROM xero_postings p WHERE p.workpaper_id::text=$1 AND p.state IN ('executing','unknown','verified'))`, revises).Scan(&posted); err != nil {
			return "", err
		}
		if posted {
			return "the draft citing the replaced invoice was posted; a person prepares the correction against that entry", nil
		}
	}
	plan.Revises = revises
	plans, _ := json.Marshal([]Plan{plan})
	if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET plans=$2,state='queued',error='',finished_at=NULL WHERE id=$1`, jobID, string(plans)); err != nil {
		return "", err
	}
	_, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET confirmed_by='agent',confirmed_at=now() WHERE job_id=$1`, jobID)
	return "", err
}

// ErrInventoryChanged is a proposal whose close no longer holds the evidence it was read from.
var ErrInventoryChanged = errors.New("Evidence inventory changed. Recheck proposals before confirming.")

// pendingEvidence is SQL for whether the close (a SQL expression for its id) holds current work evidence still being read.
func pendingEvidence(close string) string {
	return `EXISTS(SELECT 1 FROM evidence o WHERE o.close_id=` + close + ` AND o.purpose='work' AND o.superseded_by IS NULL AND o.extraction_state NOT IN ('ready','failed'))`
}

// UnrelatedEvidence is the close's documents that positively are another invoice: another job's own document, fully
// read, that the agent read as a prepaid invoice (its latest reading, even while that job is read again) whose vendor and
// invoice number its current text prints and differ from this proposal's, and whose text names neither this vendor nor this invoice number (whole words) and speaks of no credit,
// cancellation, refund, adjustment, reversal or void. Anything else stays something a person weighs.
func UnrelatedEvidence(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, closeID, jobID string, p Proposal) (map[string]bool, error) {
	vendor, number := identity(p)
	out := map[string]bool{}
	if vendor == "" || number == "" {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT e.id::text,COALESCE(e.content,''),i.proposal FROM evidence e JOIN preparation_intakes i ON i.source_id=e.id AND i.job_id::text<>$2

 WHERE e.close_id::text=$1 AND e.purpose='work' AND e.superseded_by IS NULL AND e.extraction_state='ready' AND i.proposal->>'kind'='prepaid' AND i.proposal->>'engine'='openai'`, closeID, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	thisVendor, thisNumber := wholeWord(vendor), wholeWord(number)
	for rows.Next() {
		var id, text string
		var raw []byte
		var other Proposal
		if err = rows.Scan(&id, &text, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &other) != nil || strings.TrimSpace(text) == "" || strings.Contains(text, documents.UnreadPage) {
			continue
		}
		lower, normal := strings.ToLower(text), documents.NormalizeEntity(text)
		v, n := identity(other)
		if v == "" || n == "" || v == vendor || n == number || !wholeWord(v).MatchString(normal) || !wholeWord(n).MatchString(lower) {
			continue
		}
		if thisVendor.MatchString(normal) || thisNumber.MatchString(lower) ||
			mentionsAny(strings.ReplaceAll(lower, "credit card", ""), []string{"credit", "cancel", "refund", "adjust", "revers", "void"}) {
			continue
		}
		out[id] = true
	}
	return out, rows.Err()
}

// identity is a proposal's normalized vendor and lower-cased invoice number.
func identity(p Proposal) (vendor, number string) {
	for _, f := range p.Facts {
		switch f.Field {
		case "vendor":
			vendor = documents.NormalizeEntity(f.Value)
		case "invoice_number":
			number = strings.ToLower(strings.TrimSpace(f.Value))
		}
	}
	return vendor, number
}

func wholeWord(term string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^0-9a-z])` + regexp.QuoteMeta(term) + `([^0-9a-z]|$)`)
}
