package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

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
// names by ID from the same connection's newest chart of accounts, for a line that carries only the ID.
type Ledger struct {
	Source   Source
	Rows     []LedgerRow
	Accounts map[string]string
}

// StraightThrough turns an agent proposal into a prepaid plan without a person confirming each field, when every
// fact the calculation needs is one verified candidate, the firm's enabled procedure fixes the method and accounts,
// and the current capture shows the invoice expensed: exactly one bill or purchase in the close month, from the
// invoice's vendor or bearing its number, books the amount to the procedure's expense account, and no journal in the
// month moves that amount on either account. The reason says why a proposal still needs a person. The draft it
// queues cites the capture and is reviewed like any other; nothing is approved here. Evidence in the close the agent
// did not use, or could not read, is left for a person, as the confirmation form asks.
func StraightThrough(p Proposal, invoice string, aliases []string, procedure Procedure, currency, entity, period, jobID string, unread bool, ledger *Ledger) (Plan, string) {
	if p.Engine != "openai" || p.Kind != "prepaid" {
		return Plan{}, "only an agent-read prepaid invoice can be prepared without confirmation"
	}
	if len(p.Questions) > 0 {
		return Plan{}, "the agent has open questions"
	}
	if len(p.Unselected) > 0 {
		return Plan{}, "evidence in the close was not used; a person confirms it is unrelated"
	}
	if unread {
		return Plan{}, "evidence in the close is not extracted yet, or failed extraction"
	}
	if currency == "" {
		return Plan{}, "the client's home currency is not detected from a connected ledger"
	}
	if !procedure.Enabled || procedure.Kind != "prepaid" || procedure.Method != prepaids.Method || procedure.ExpenseAccount == "" || procedure.PrepaidAccount == "" {
		return Plan{}, "no enabled prepaid procedure names equal full months and both accounts for this client"
	}
	if ledger == nil {
		return Plan{}, "the close has no current QuickBooks capture"
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
		if (f.Field == "currency" || strings.HasSuffix(f.Field, "_account")) && !strings.Contains(strings.ToLower(f.Candidates[0].Quote), strings.ToLower(f.Value)) {
			return Plan{}, "the cited text does not show the " + f.Label
		}
		// A name or number must be the whole of what its quoted line ends with: "Example" is not what "Bill to: Example Holdings" names.
		if identity && !endsLine(f.Candidates[0].Quote, f.Value) {
			return Plan{}, "the cited text does not show the " + f.Label + " as a whole"
		}
		// An order or reference number is not the invoice number, and a ship-to name is not who is billed; the line must say which.
		if f.Field == "invoice_number" && !lineSays(f.Candidates[0].Quote, f.Value, []string{"invoice"}, []string{"order", "reference", "date", "due"}) {
			return Plan{}, "the cited line does not name the number as the invoice number"
		}
		if f.Field == "billed_to" && !lineSays(f.Candidates[0].Quote, f.Value, []string{"bill", "invoice to", "sold to"}, []string{"ship", "deliver", "service", "support", "contact"}) {
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
	billed := documents.NormalizeEntity(facts["billed_to"])
	if billed != documents.NormalizeEntity(entity) && !slices.ContainsFunc(aliases, func(alias string) bool { return documents.NormalizeEntity(alias) == billed }) {
		return Plan{}, "the invoice is billed to " + facts["billed_to"] + ", not the client or a confirmed alias"
	}
	if facts["currency"] != currency {
		return Plan{}, "the invoice is not in the client's home currency"
	}
	amount, err := prepaids.Amount(facts["amount"])
	if err != nil || amount <= 0 {
		return Plan{}, "the amount is not a positive money value"
	}
	for _, f := range p.Facts {
		if f.Field == "amount" && negativeFigure(f.Candidates[0].Quote, amount) {
			return Plan{}, "the invoice prints the amount as a negative or bracketed figure"
		}
		if f.Field == "amount" && moneyFigures(f.Candidates[0].Quote) != 1 {
			return Plan{}, "the cited text prints more than one money figure; which is the invoice amount needs a person"
		}
	}
	booking, reason := expensedLine(ledger, invoice, facts["vendor"], facts["invoice_number"], prepaids.Money(amount), currency, period, procedure.ExpenseAccount, procedure.PrepaidAccount)
	if reason != "" {
		return Plan{}, reason
	}
	input := prepaids.Input{Entity: entity, ClosePeriod: period, SourceReference: "Evidence #" + primary + " · work #" + jobID, Currency: currency, Amount: prepaids.Money(amount), ServiceStart: facts["service_start"], ServiceEnd: facts["service_end"],
		ExpenseAccount: procedure.ExpenseAccount, PrepaidAccount: procedure.PrepaidAccount, Basis: "accrual", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	raw, _ := json.Marshal(input)
	capture := ledger.Source
	capture.Role, capture.Location = "period_ledger", booking
	plan := Plan{Kind: "prepaid", Input: raw, Sources: append(append([]Source{}, p.Sources...), capture)}
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
	matches := 0
	for _, row := range ledger.Rows {
		if !strings.HasPrefix(row.Date, period+"-") {
			continue
		}
		var record struct {
			Credit               bool
			DocNumber            string
			TotalAmt             json.Number
			VendorRef, EntityRef struct{ Name string }
			Line                 []struct {
				Amount                        json.Number
				AccountBasedExpenseLineDetail *struct{ AccountRef accountRef }
				JournalEntryLineDetail        *struct{ AccountRef accountRef }
				DepositLineDetail             *struct {
					AccountRef accountRef
					Entity     struct{ Name string }
				}
			}
		}
		if json.Unmarshal(row.Record, &record) != nil {
			continue
		}
		party := record.VendorRef.Name
		if party == "" {
			party = record.EntityRef.Name
		}
		mentioned := printed(text, party)
		fromVendor := party != "" && strings.EqualFold(party, vendor)
		mine := fromVendor && strings.EqualFold(strings.TrimSpace(record.DocNumber), strings.TrimSpace(number))
		if len(record.Line) == 0 && (row.Kind == "JournalEntry" || row.Kind == "Deposit" || ((row.Kind == "VendorCredit" || record.Credit) && (mentioned || fromVendor)) || mine) {
			return "", "a captured " + row.Kind + " in the close month has no readable lines"
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
			name := account.Name
			if name == "" {
				name = ledger.Accounts[account.Value]
			}
			onExpense, onPrepaid := strings.EqualFold(name, expense), strings.EqualFold(name, prepaid)
			if name == "" && (row.Kind == "JournalEntry" || row.Kind == "Deposit" || row.Kind == "VendorCredit" || record.Credit || mentioned || fromVendor) {
				return "", "a captured " + row.Kind + " in the close month has a line whose account cannot be named"
			}
			if row.Kind == "JournalEntry" && (onExpense || onPrepaid) {
				return "", "a journal in the close month posts to the expense or prepaid account"
			}
			if line.DepositLineDetail != nil {
				from := line.DepositLineDetail.Entity.Name
				mentioned, fromVendor = mentioned || printed(text, from), fromVendor || (from != "" && strings.EqualFold(from, vendor))
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

// negativeFigure reports whether the quote prints the amount preceded by a minus sign or an opening bracket, which
// the quote check ignores: a credit is not an invoice.
func negativeFigure(quote string, cents int64) bool {
	plain := strings.NewReplacer(",", "", "$", "", "€", "", "£", "").Replace(strings.ToLower(quote))
	digits := fmt.Sprintf(`%d(\.%02d)?`, cents/100, cents%100)
	return regexp.MustCompile(`[-(]\s*(usd|eur|gbp|cad|aud)?\s*` + digits + `([^0-9]|$)`).MatchString(plain)
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
		ok := false
		for _, word := range any {
			ok = ok || strings.Contains(label, word)
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

// CloseLedger is the newest completed capture of the close month on the close's reading connection, with its
// current evidence entry and its bill, purchase, journal, vendor credit and deposit rows (a newer sync that found the books
// unchanged reuses them); nil when a newer capture is still pending or failed,
// or there is none.
func CloseLedger(ctx context.Context, tx *sql.Tx, closeID string) (*Ledger, error) {
	var l Ledger
	var connection string
	err := tx.QueryRowContext(ctx, `SELECT e.id::text,e.digest,e.name,s.connection_id::text FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL
 WHERE s.close_id::text=$1 AND s.state='completed' AND s.period=c.period AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.connection_id=s.connection_id AND z.state<>'unchanged')`, closeID).Scan(&l.Source.ID, &l.Source.Digest, &l.Source.Name, &connection)
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
	err = tx.QueryRowContext(ctx, `SELECT payload->'accounts' FROM quickbooks_snapshots WHERE close_id::text=$1 AND connection_id::text=$2 AND payload ? 'period' ORDER BY id DESC LIMIT 1`, closeID, connection).Scan(&catalog)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	var accounts []struct{ ID, Name string }
	json.Unmarshal(catalog, &accounts)
	l.Accounts = map[string]string{}
	for _, a := range accounts {
		if a.ID != "" {
			l.Accounts[a.ID] = a.Name
		}
	}
	return &l, nil
}

// queueStraightThrough queues the plan on the job as the agent's confirmation.
func queueStraightThrough(ctx context.Context, tx *sql.Tx, jobID string, plan Plan) error {
	plans, _ := json.Marshal([]Plan{plan})
	if _, err := tx.ExecContext(ctx, `UPDATE preparation_jobs SET plans=$2,state='queued',error='',finished_at=NULL WHERE id=$1`, jobID, string(plans)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE preparation_intakes SET confirmed_by='agent',confirmed_at=now() WHERE job_id=$1`, jobID)
	return err
}
