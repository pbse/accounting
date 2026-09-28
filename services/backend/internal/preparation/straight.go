package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
)

// A captured QuickBooks transaction, as the straight-through check reads it.
type LedgerRow struct {
	Kind, ProviderID, Date, Currency string
	Record                           json.RawMessage
}

// Ledger is the close's current QuickBooks capture: its evidence entry and the rows the check reads.
type Ledger struct {
	Source Source
	Rows   []LedgerRow
}

// StraightThrough turns an agent proposal into a prepaid plan without a person confirming each field, when every
// fact the calculation needs is one verified candidate, the firm's enabled procedure fixes the method and accounts,
// and the current capture shows the invoice expensed: exactly one bill or purchase in the close month, from the
// invoice's vendor or bearing its number, books the amount to the procedure's expense account, and no journal in the
// month moves that amount on either account. The reason says why a proposal still needs a person. The draft it
// queues cites the capture and is reviewed like any other; nothing is approved here. Evidence in the close the agent
// did not use, or could not read, is left for a person, as the confirmation form asks.
func StraightThrough(p Proposal, invoice string, procedure Procedure, currency, entity, period, jobID string, unread bool, ledger *Ledger) (Plan, string) {
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
	facts := map[string]string{}
	for _, f := range p.Facts {
		if len(f.Candidates) == 0 {
			if !f.Optional && f.Field != "expense_account" && f.Field != "prepaid_account" {
				return Plan{}, "the " + f.Label + " was not read from the source"
			}
			continue
		}
		if f.Optional {
			return Plan{}, "the source cites a " + f.Label + ", which needs a reviewed method"
		}
		if f.Value == "" || len(f.Candidates) != 1 {
			return Plan{}, "the " + f.Label + " has more than one candidate"
		}
		// Amounts and dates were checked against their quotes when the proposal was validated; names are checked here.
		if (f.Field == "currency" || strings.HasSuffix(f.Field, "_account")) && !strings.Contains(strings.ToLower(f.Candidates[0].Quote), strings.ToLower(f.Value)) {
			return Plan{}, "the cited text does not show the " + f.Label
		}
		facts[f.Field] = f.Value
	}
	for field, account := range map[string]string{"expense_account": procedure.ExpenseAccount, "prepaid_account": procedure.PrepaidAccount} {
		if v, ok := facts[field]; ok && !strings.EqualFold(v, account) {
			return Plan{}, "the source names a different " + strings.ReplaceAll(field, "_", " ") + " than the procedure"
		}
	}
	if facts["currency"] != currency {
		return Plan{}, "the invoice is not in the client's home currency"
	}
	amount, err := prepaids.Amount(facts["amount"])
	if err != nil || amount <= 0 {
		return Plan{}, "the amount is not a positive money value"
	}
	booking, reason := expensedLine(ledger.Rows, invoice, prepaids.Money(amount), currency, period, procedure.ExpenseAccount, procedure.PrepaidAccount)
	if reason != "" {
		return Plan{}, reason
	}
	primary := ""
	for _, s := range p.Sources {
		if s.Role == "invoice" {
			primary = s.ID
		}
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
// account and belongs to the invoice: its vendor's name heads the invoice or its document number is printed in it. Any prepaid
// line on that bill, a second match, any journal on either account in the month, a vendor credit from the vendor or on
// either account, or a line whose account is not named is a question for a person, whatever its currency.
func expensedLine(rows []LedgerRow, invoice, amount, currency, period, expense, prepaid string) (booking, reason string) {
	text := strings.ToLower(invoice)
	// ponytail: the invoice's own vendor is taken to be a name in its first three lines (the letterhead); a name printed
	// anywhere else, such as a payment processor's, only counts against it. A parsed vendor fact would replace this.
	head := []string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			head = append(head, line)
		}
		if len(head) == 3 {
			break
		}
	}
	letterhead := strings.Join(head, "\n")
	matches := 0
	for _, row := range rows {
		if !strings.HasPrefix(row.Date, period+"-") {
			continue
		}
		var record struct {
			Credit               bool
			DocNumber            string
			VendorRef, EntityRef struct{ Name string }
			Line                 []struct {
				Amount                        json.Number
				AccountBasedExpenseLineDetail *struct{ AccountRef struct{ Name string } }
				JournalEntryLineDetail        *struct{ AccountRef struct{ Name string } }
			}
		}
		if json.Unmarshal(row.Record, &record) != nil {
			continue
		}
		mentioned := printed(text, record.VendorRef.Name) || printed(text, record.EntityRef.Name) || printed(text, record.DocNumber)
		mine := printed(letterhead, record.VendorRef.Name) || printed(letterhead, record.EntityRef.Name) || (numberLike(record.DocNumber) && printed(text, record.DocNumber))
		if len(record.Line) == 0 && (row.Kind == "JournalEntry" || (row.Kind == "VendorCredit" && mentioned) || mine) {
			return "", "a captured " + row.Kind + " in the close month has no readable lines"
		}
		for _, line := range record.Line {
			name := ""
			if line.AccountBasedExpenseLineDetail != nil {
				name = line.AccountBasedExpenseLineDetail.AccountRef.Name
			} else if line.JournalEntryLineDetail != nil {
				name = line.JournalEntryLineDetail.AccountRef.Name
			}
			onExpense, onPrepaid := strings.EqualFold(name, expense), strings.EqualFold(name, prepaid)
			if name == "" && (row.Kind == "JournalEntry" || row.Kind == "VendorCredit" || mentioned) {
				return "", "a captured " + row.Kind + " in the close month has a line whose account is not named"
			}
			if row.Kind == "JournalEntry" && (onExpense || onPrepaid) {
				return "", "a journal in the close month posts to the expense or prepaid account"
			}
			if row.Kind == "VendorCredit" && (mentioned || onExpense || onPrepaid) {
				return "", "a vendor credit in the close month may change what this invoice expensed"
			}
			if row.Kind == "Purchase" && record.Credit && (mentioned || onExpense || onPrepaid) {
				return "", "a card credit in the close month may change what this invoice expensed"
			}
			if (row.Kind != "Bill" && row.Kind != "Purchase") || !mine {
				continue
			}
			if onPrepaid {
				return "", "a captured " + row.Kind + " from this vendor books to the prepaid account; its balance needs a person"
			}
			value, err := prepaids.Amount(line.Amount.String())
			if record.Credit || err != nil || prepaids.Money(value) != amount || !onExpense || (row.Currency != "" && row.Currency != currency) {
				continue
			}
			matches++
			booking = fmt.Sprintf("QuickBooks %s %s dated %s, %s %s to %s", row.Kind, row.ProviderID, row.Date, currency, amount, name)
		}
	}
	switch matches {
	case 0:
		return "", "no captured bill or purchase in the close month from this vendor or with this invoice number books the amount to the expense account"
	case 1:
		return booking, ""
	}
	return "", "more than one captured bill or purchase in the close month books this amount"
}

// numberLike keeps a bill's document number from matching a year, a date fragment or a short figure printed in the
// invoice: it must carry a letter, or be at least six digits with nothing else.
func numberLike(number string) bool {
	number = strings.TrimSpace(number)
	letter := strings.ContainsFunc(number, func(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' })
	digits := !strings.ContainsFunc(number, func(r rune) bool { return r < '0' || r > '9' })
	return letter || (digits && len(number) >= 6)
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
// current evidence entry and its bill, purchase, journal and vendor credit rows (a newer sync that found the books
// unchanged reuses them); nil when a newer capture is still pending or failed,
// or there is none.
func CloseLedger(ctx context.Context, tx *sql.Tx, closeID string) (*Ledger, error) {
	var l Ledger
	err := tx.QueryRowContext(ctx, `SELECT e.id::text,e.digest,e.name FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL
 WHERE s.close_id::text=$1 AND s.state='completed' AND s.period=c.period AND s.connection_id=`+quickbooks.ReadingConnection("s.close_id")+` AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.connection_id=s.connection_id AND z.state<>'unchanged')`, closeID).Scan(&l.Source.ID, &l.Source.Digest, &l.Source.Name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT t.kind,t.provider_id,t.txn_date::text,COALESCE(t.currency,''),t.record FROM quickbooks_transaction_rows t JOIN quickbooks_entity_syncs s ON s.id=t.sync_id WHERE s.evidence_id::text=$1 AND t.kind IN ('Bill','Purchase','JournalEntry','VendorCredit')`, l.Source.ID)
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
	return &l, rows.Err()
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
