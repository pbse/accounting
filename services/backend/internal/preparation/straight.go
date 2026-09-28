package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
)

// A captured QuickBooks transaction, as the straight-through check reads it.
type LedgerRow struct {
	Kind, ProviderID, Date, Currency string
	Record                           json.RawMessage
}

// StraightThrough turns an agent proposal into a prepaid plan without a person confirming each field, when every
// fact the calculation needs is one verified candidate, the firm's enabled procedure fixes the method and accounts,
// and exactly one captured bill or purchase in the close month books the invoice amount to one of those accounts.
// The reason says why a proposal still needs a person. The draft it queues is reviewed like any other; nothing is
// approved here.
func StraightThrough(p Proposal, procedure Procedure, currency, entity, period, jobID string, rows []LedgerRow) (Plan, string) {
	if p.Engine != "openai" || p.Kind != "prepaid" {
		return Plan{}, "only an agent-read prepaid invoice can be prepared without confirmation"
	}
	if len(p.Questions) > 0 {
		return Plan{}, "the agent has open questions"
	}
	if !procedure.Enabled || procedure.Kind != "prepaid" || procedure.Method != prepaids.Method || procedure.ExpenseAccount == "" || procedure.PrepaidAccount == "" {
		return Plan{}, "no enabled prepaid procedure names equal full months and both accounts for this client"
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
	booked, reference, reason := bookedLine(rows, prepaids.Money(amount), currency, period, procedure.ExpenseAccount, procedure.PrepaidAccount)
	if reason != "" {
		return Plan{}, reason
	}
	invoice := ""
	for _, s := range p.Sources {
		if s.Role == "invoice" {
			invoice = s.ID
		}
	}
	input := prepaids.Input{Entity: entity, ClosePeriod: period, SourceReference: "Evidence #" + invoice + " · work #" + jobID, Currency: currency, Amount: prepaids.Money(amount), ServiceStart: facts["service_start"], ServiceEnd: facts["service_end"],
		ExpenseAccount: procedure.ExpenseAccount, PrepaidAccount: procedure.PrepaidAccount, Basis: "accrual", Method: prepaids.Method,
		MethodConfirmed: true, BookingConfirmed: true, BookedBalance: booked, BookingReference: reference, ExamplesConfirmed: booked != ""}
	raw, _ := json.Marshal(input)
	plan := Plan{Kind: "prepaid", Input: raw, Sources: p.Sources}
	if _, err := Calculate(plan, entity, period); err != nil {
		return Plan{}, err.Error()
	}
	return plan, ""
}

// bookedLine finds the one captured bill or purchase line in the close month carrying the invoice amount on the
// expense account (the invoice was expensed: no booked balance) or the prepaid account (its booked balance is the
// line). Any other count is a question for a person.
func bookedLine(rows []LedgerRow, amount, currency, period, expense, prepaid string) (booked, reference, reason string) {
	matches := 0
	for _, row := range rows {
		if (row.Kind != "Bill" && row.Kind != "Purchase") || !strings.HasPrefix(row.Date, period+"-") || (row.Currency != "" && row.Currency != currency) {
			continue
		}
		var record struct {
			Credit bool
			Line   []struct {
				Amount                        json.Number
				AccountBasedExpenseLineDetail *struct{ AccountRef struct{ Name string } }
			}
		}
		if json.Unmarshal(row.Record, &record) != nil || record.Credit {
			continue
		}
		for _, line := range record.Line {
			if line.AccountBasedExpenseLineDetail == nil {
				continue
			}
			value, err := prepaids.Amount(line.Amount.String())
			if err != nil || prepaids.Money(value) != amount {
				continue
			}
			name := line.AccountBasedExpenseLineDetail.AccountRef.Name
			switch {
			case strings.EqualFold(name, expense):
				matches++
				booked, reference = "", ""
			case strings.EqualFold(name, prepaid):
				matches++
				booked, reference = amount, fmt.Sprintf("QuickBooks %s %s dated %s, %s %s to %s", row.Kind, row.ProviderID, row.Date, currency, amount, name)
			}
		}
	}
	switch matches {
	case 0:
		return "", "", "no captured bill or purchase in the close month books this amount to the procedure's accounts"
	case 1:
		return booked, reference, ""
	}
	return "", "", "more than one captured bill or purchase in the close month books this amount"
}

// CloseLedgerRows are the bills and purchases of the close's newest completed QuickBooks capture, none if it is stale.
func CloseLedgerRows(ctx context.Context, tx *sql.Tx, closeID string) ([]LedgerRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT t.kind,t.provider_id,t.txn_date::text,COALESCE(t.currency,''),t.record FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL JOIN quickbooks_transaction_rows t ON t.sync_id=s.id WHERE s.close_id::text=$1 AND s.state='completed' AND s.id=(SELECT max(z.id) FROM quickbooks_entity_syncs z WHERE z.close_id=s.close_id AND z.state='completed') AND t.kind IN ('Bill','Purchase')`, closeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LedgerRow
	for rows.Next() {
		var r LedgerRow
		if err := rows.Scan(&r.Kind, &r.ProviderID, &r.Date, &r.Currency, &r.Record); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// queueStraightThrough queues the plan on the job as the agent's confirmation, recording why in the checkpoint.
func queueStraightThrough(ctx context.Context, tx *sql.Tx, jobID string, plan Plan) error {
	plans, _ := json.Marshal([]Plan{plan})
	if _, err := tx.ExecContext(ctx, `UPDATE preparation_jobs SET plans=$2,state='queued',error='',finished_at=NULL WHERE id=$1`, jobID, string(plans)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE preparation_intakes SET confirmed_by='agent',confirmed_at=$2 WHERE job_id=$1`, jobID, time.Now())
	return err
}
