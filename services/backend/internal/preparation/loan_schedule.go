package preparation

import (
	"fmt"
	"strings"
	"time"

	"close.local/backend/internal/loans"
	"close.local/backend/internal/prepaids"
)

// ScheduledLoan builds a loan's opening month from a prior accountant's amortization schedule: the last payment before the
// month opens it (interest taken as paid through that payment's date), the month's payments close it and, with a current
// portion, the principal of the payments in the 12 calendar months after month-end is due within a year. Each row's
// balance must be the balance before it less its principal, so a mistyped or missing row is refused rather than summed.
func ScheduledLoan(p Proposal, entity, period, reference, dayCount string, reclass bool, currentAccount string) (loans.Input, error) {
	fail := func(format string, args ...any) (loans.Input, error) {
		return loans.Input{}, fmt.Errorf(format, args...)
	}
	facts := map[string]string{}
	for _, f := range p.Facts {
		if f.Question != "" || f.Value == "" {
			return fail("Confirm the %s of the loan.", f.Label)
		}
		facts[f.Field] = f.Value
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return fail("Enter a supported close month.")
	}
	end := month.AddDate(0, 1, -1)
	horizon := time.Date(month.Year(), month.Month()+13, 0, 0, 0, 0, 0, time.UTC)
	type row struct {
		line                         int
		at                           time.Time
		interest, principal, balance int64
	}
	rows := []row{}
	for _, l := range p.Lines {
		values := map[string]string{}
		for _, f := range l.Facts {
			if f.Question != "" || f.Value == "" {
				return fail("Row %d: confirm its %s.", l.Line, f.Label)
			}
			values[f.Field] = f.Value
		}
		at, err := time.Parse("2006-01-02", values["date"])
		r := row{line: l.Line, at: at}
		var e1, e2, e3 error
		r.interest, e1 = prepaids.Amount(values["interest"])
		r.principal, e2 = prepaids.Amount(values["principal"])
		r.balance, e3 = prepaids.Amount(values["balance"])
		if err != nil || e1 != nil || e2 != nil || e3 != nil {
			return fail("Row %d needs a date and nonnegative interest, principal and balance.", l.Line)
		}
		if len(rows) > 0 && !at.After(rows[len(rows)-1].at) {
			return fail("Row %d is not after the row before it.", l.Line)
		}
		rows = append(rows, r)
	}
	before := -1
	for i, r := range rows {
		if r.at.Before(month) {
			before = i
		}
	}
	if before < 0 {
		return fail("The schedule needs the last payment before %s, which opens the month.", period)
	}
	opening, paidThrough := rows[before].balance, rows[before].at
	if paidThrough.Before(month.AddDate(0, 0, -62)) {
		return fail("The last payment before %s is more than 62 days earlier; prepare this loan from the lender's statement.", period)
	}
	payments, closing, due, running, paidOff := []loans.Payment{}, opening, int64(0), opening, false
	for _, r := range rows[before+1:] {
		if r.principal > running || running-r.principal != r.balance {
			return fail("Row %d: %s less principal %s is %s, but the schedule shows %s.", r.line, prepaids.Money(running), prepaids.Money(r.principal), prepaids.Money(running-r.principal), prepaids.Money(r.balance))
		}
		running = r.balance
		switch {
		case !r.at.After(end):
			payments = append(payments, loans.Payment{Date: r.at.Format("2006-01-02"), Principal: prepaids.Money(r.principal), Interest: prepaids.Money(r.interest)})
			closing = r.balance
		case !r.at.After(horizon):
			due += r.principal
		}
		paidOff = paidOff || (r.balance == 0 && !r.at.After(horizon))
	}
	in := loans.Input{Entity: entity, ClosePeriod: period, Lender: facts["lender"], SourceReference: reference, AgreementReference: facts["agreement_reference"], Currency: facts["currency"],
		AnnualRate: facts["annual_rate"], DayCount: dayCount, OpeningPrincipal: prepaids.Money(opening), InterestPaidThrough: paidThrough.Format("2006-01-02"), Payments: payments,
		ClosingPrincipal: prepaids.Money(closing), LoanAccount: facts["loan_account"], InterestAccount: facts["interest_account"], Method: loans.Method}
	if reclass {
		last := rows[len(rows)-1].at
		if !paidOff && last.Format("2006-01") < horizon.Format("2006-01") {
			return fail("The schedule shows payments only to %s, not the 12 months after %s, so the principal due within a year cannot be stated.", last.Format("2006-01-02"), end.Format("2006-01-02"))
		}
		if strings.TrimSpace(currentAccount) == "" {
			return fail("Name the account the current portion of the loan is reclassified to.")
		}
		in.ReclassCurrentPortion, in.PrincipalDueNext12Months, in.CurrentPortionAccount, in.ScheduleReference = true, prepaids.Money(due), strings.TrimSpace(currentAccount), reference
	}
	return in, nil
}
