package loans

import (
	"strings"
	"testing"
	"time"
)

func base() Input {
	return Input{Entity: "Harbor Dental", ClosePeriod: "2026-09", Lender: "First Bank", SourceReference: "Statement Sep 2026", AgreementReference: "Note §2", Currency: "USD",
		AnnualRate: "6.000", DayCount: "actual_360", OpeningPrincipal: "120000.00", InterestPaidThrough: "2026-08-15",
		// 12,000,000¢ × 6% × 31/360 = 62,000¢; then 11,850,000¢ × 6% × 14/360 = 27,650¢, but the lender charged 280.00.
		Payments:         []Payment{{Date: "2026-09-15", Principal: "1500.00", Interest: "620.00"}, {Date: "2026-09-29", Principal: "500.00", Interest: "280.00"}},
		ClosingPrincipal: "118000.00", BookBalance: "100000.00", LoanAccount: "Loan payable", InterestAccount: "Interest expense",
		AccrueInterest: true, AccruedInterestAccount: "Accrued interest", BookedAccruedInterest: "0.00",
		ReclassCurrentPortion: true, PrincipalDueNext12Months: "20000.00", ScheduleReference: "Amortization schedule p2", CurrentPortionAccount: "Current portion of loan", BookedCurrentPortion: "18000.00",
		Method: Method, MethodConfirmed: true, RateConfirmed: true, BookingConfirmed: true}
}

func TestLenderSplitIsCheckedAndOnlySupportedEntriesAreProposed(t *testing.T) {
	paper, err := Prepare(base())
	if err != nil {
		t.Fatal(err)
	}
	if len(paper.Schedule) != 2 || paper.Schedule[0].ExpectedInterest != "620.00" || paper.Schedule[1].ExpectedInterest != "276.50" || paper.Schedule[1].Difference != "3.50" || paper.Schedule[1].PrincipalBefore != "118500.00" {
		t.Fatalf("interest must be recomputed per payment on the principal outstanding: %+v", paper.Schedule)
	}
	if len(paper.Questions) != 1 || !strings.Contains(paper.Questions[0], "280.00") || len(paper.Blockers) != 0 {
		t.Fatalf("only the differing lender charge is a question: %v %v", paper.Questions, paper.Blockers)
	}
	// 11,800,000¢ × 6% × 1/360 = 1,966.67¢ → 19.67 accrued from the last payment to month-end.
	if paper.AccruedInterest == nil || paper.AccruedInterest.Days != 1 || paper.AccruedInterest.Amount != "19.67" {
		t.Fatalf("accrued interest: %+v", paper.AccruedInterest)
	}
	if len(paper.Proposals) != 2 || paper.Proposals[0].DebitAccount != "Interest expense" || paper.Proposals[0].Amount != "19.67" || paper.Proposals[1].DebitAccount != "Loan payable" || paper.Proposals[1].CreditAccount != "Current portion of loan" || paper.Proposals[1].Amount != "2000.00" || paper.Proposals[1].Date != "2026-09-30" {
		t.Fatalf("expected an accrual and a 2,000.00 current portion reclass: %+v", paper.Proposals)
	}

	in := base()
	in.BookBalance, in.BookedAccruedInterest = "99000.00", "25.00"
	paper, err = Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	if paper.BookDifference != "-1000.00" || len(paper.Proposals) != 1 || paper.Proposals[0].DebitAccount != "Accrued interest" || paper.Proposals[0].Amount != "5.33" || !strings.Contains(strings.Join(paper.Questions, " "), "1000.00 less loan principal") {
		t.Fatalf("a book difference is a question, blocks the reclass, and an over-accrual reverses: %+v", paper)
	}

	in = base()
	in.ClosingPrincipal = "118100.00"
	if paper, err = Prepare(in); err != nil || len(paper.Blockers) != 1 || !strings.Contains(paper.Blockers[0], "does not tie") || len(paper.Proposals) != 0 {
		t.Fatalf("an untied statement must block: %v %+v", err, paper.Blockers)
	}
	in = base()
	in.RateConfirmed = false
	if _, err = Prepare(in); err == nil {
		t.Fatal("the rate and day-count must be confirmed from the note")
	}
	in = base()
	in.DayCount = "30_360"
	if _, err = Prepare(in); err == nil {
		t.Fatal("an ambiguous 30/360 must be refused")
	}
}

func TestDayCountConventions(t *testing.T) {
	day := func(s string) time.Time { v, _ := time.Parse("2006-01-02", s); return v }
	for _, c := range []struct {
		convention, from, to string
		want                 int64
	}{
		{"actual_360", "2026-08-15", "2026-09-15", 31},
		{"actual_365", "2026-09-30", "2026-10-31", 31},
		{"30_360_bond", "2026-09-30", "2026-10-31", 30},
		{"30_360_bond", "2026-09-15", "2026-10-31", 46},
		{"30e_360", "2026-09-15", "2026-10-31", 45},
		{"30e_360", "2026-01-31", "2026-02-28", 28},
	} {
		if got := Days(c.convention, day(c.from), day(c.to)); got != c.want {
			t.Fatalf("%s %s→%s: %d, want %d", c.convention, c.from, c.to, got, c.want)
		}
	}
	// 10,000,000¢ × 7.5% × 30/360 = 62,500¢.
	if got := Interest(10_000_000, 7500, 30, "30_360_bond"); got != 62500 {
		t.Fatalf("interest %d", got)
	}
}

func variable() Input {
	in := base()
	in.Method = VariableMethod
	in.Payments = []Payment{{Date: "2026-09-15", Principal: "1500.00", Interest: "645.00"}}
	in.ClosingPrincipal, in.BookBalance = "118500.00", "118500.00"
	in.AccrueInterest, in.AccruedInterestAccount, in.BookedAccruedInterest = false, "", ""
	in.ReclassCurrentPortion, in.PrincipalDueNext12Months, in.ScheduleReference, in.CurrentPortionAccount, in.BookedCurrentPortion = false, "", "", "", ""
	return in
}

func TestRateChangesSplitTheInterestWindow(t *testing.T) {
	// 12,000,000¢ × (6.000% × 16 days + 6.500% × 15 days) / 360 = 64,500¢.
	in := variable()
	in.RateChanges = []RateChange{{Effective: "2026-09-01", AnnualRate: "6.500", Reference: "Rate notice Aug 2026"}}
	paper, err := Prepare(in)
	if err != nil || len(paper.Questions) != 0 || paper.Schedule[0].ExpectedInterest != "645.00" || strings.Join(paper.Schedule[0].Rates, ",") != "6.000%,6.500%" || paper.Method != VariableMethod {
		t.Fatalf("a rate change from 1 September explains the lender's interest: %v %+v", err, paper)
	}
	if paper, _ = Prepare(variable()); len(paper.Questions) != 1 || !strings.Contains(paper.Questions[0], "6.000%") {
		t.Fatalf("without the rate change the lender's interest is a question: %v", paper.Questions)
	}
	// A rate effective on the payment date covers that one day: 30 days at 6.000% plus 1 day at 6.500% = 621.67.
	in = variable()
	in.Payments[0].Interest = "621.67"
	in.RateChanges = []RateChange{{Effective: "2026-09-15", AnnualRate: "6.500", Reference: "Rate notice Sep 2026"}}
	if paper, err = Prepare(in); err != nil || len(paper.Questions) != 0 {
		t.Fatalf("a rate applies from its effective date: %v %v %+v", err, paper.Questions, paper.Schedule)
	}
	// 30/360 bond basis counts 29 January → 15 February as 16 days: 2 at 6.000% and 14 at 6.500% = 343.33.
	in = variable()
	in.DayCount, in.ClosePeriod, in.InterestPaidThrough = "30_360_bond", "2027-02", "2027-01-29"
	in.Payments = []Payment{{Date: "2027-02-15", Principal: "1500.00", Interest: "343.33"}}
	in.RateChanges = []RateChange{{Effective: "2027-02-01", AnnualRate: "6.500", Reference: "Rate notice Jan 2027"}}
	if paper, err = Prepare(in); err != nil || len(paper.Questions) != 0 || paper.Schedule[0].Days != 16 {
		t.Fatalf("a split window keeps the convention's day count: %v %v %+v", err, paper.Questions, paper.Schedule)
	}
	// Actual/365: 26,301.37¢ + 26,712.33¢ rounds once to 530.14, not 530.13 segment by segment.
	in = variable()
	in.DayCount, in.OpeningPrincipal, in.ClosingPrincipal, in.BookBalance = "actual_365", "100000.00", "98500.00", "98500.00"
	in.Payments[0].Interest = "530.14"
	in.RateChanges = []RateChange{{Effective: "2026-09-01", AnnualRate: "6.500", Reference: "Rate notice Aug 2026"}}
	if paper, err = Prepare(in); err != nil || len(paper.Questions) != 0 {
		t.Fatalf("interest is rounded once per window: %v %v", err, paper.Questions)
	}
}

func TestFeesAddedToTheBalanceBearInterestAndAreProposed(t *testing.T) {
	// A 250.00 fee on 5 September bears interest for 10 days: 42,000¢ + 20,041.67¢ = 620.42.
	in := variable()
	in.Payments[0].Interest = "620.42"
	in.Fees = []Fee{{Date: "2026-09-05", Amount: "250", Description: "Late fee", Reference: "Statement line 4"}}
	in.ClosingPrincipal, in.FeeAccount, in.BookedFees = "118750.00", "Loan fees", "0"
	paper, err := Prepare(in)
	if err != nil || len(paper.Questions) != 0 || len(paper.Blockers) != 0 || paper.Fees != "250.00" || paper.BookDifference != "0.00" {
		t.Fatalf("the fee ties the statement and bears interest from the next day: %v %+v", err, paper)
	}
	if len(paper.Proposals) != 1 || paper.Proposals[0].DebitAccount != "Loan fees" || paper.Proposals[0].CreditAccount != "Loan payable" || paper.Proposals[0].Amount != "250.00" {
		t.Fatalf("the unbooked fee is proposed against the loan: %+v", paper.Proposals)
	}
	// A fee on the payment date comes after the payment, so this window's interest is unchanged.
	in.Fees[0].Date, in.Payments[0].Interest = "2026-09-15", "620.00"
	if paper, err = Prepare(in); err != nil || len(paper.Questions) != 0 || len(paper.Blockers) != 0 {
		t.Fatalf("a same-day fee follows the payment: %v %v %v", err, paper.Questions, paper.Blockers)
	}
	// A fee after the last payment is still part of the statement's closing principal.
	in.Fees[0].Date = "2026-09-25"
	if paper, err = Prepare(in); err != nil || len(paper.Blockers) != 0 || len(paper.Questions) != 0 {
		t.Fatalf("a fee after the last payment ties the statement: %v %v %v", err, paper.Blockers, paper.Questions)
	}
	in.Fees[0].Date = "2026-09-15"
	// More booked than charged reverses the entry for the excess.
	in.BookedFees, in.BookBalance = "300.00", "118800.00"
	if paper, err = Prepare(in); err != nil || len(paper.Proposals) != 1 || paper.Proposals[0].DebitAccount != "Loan payable" || paper.Proposals[0].Amount != "50.00" || paper.BookDifference != "0.00" {
		t.Fatalf("over-booked fees are proposed back: %v %+v", err, paper)
	}
	for name, change := range map[string]func(*Input){
		"fixed method":        func(in *Input) { in.Method = Method },
		"no fee account":      func(in *Input) { in.FeeAccount = "" },
		"fee account is loan": func(in *Input) { in.FeeAccount = "Loan payable" },
		"no booked fees":      func(in *Input) { in.BookedFees = "" },
		"fee next month":      func(in *Input) { in.Fees[0].Date = "2026-10-01" },
		"rate before window": func(in *Input) {
			in.RateChanges = []RateChange{{Effective: "2026-08-10", AnnualRate: "6.500", Reference: "Notice"}}
		},
		"rate without source": func(in *Input) { in.RateChanges = []RateChange{{Effective: "2026-09-01", AnnualRate: "6.500"}} },
		"rates out of order": func(in *Input) {
			in.RateChanges = []RateChange{{Effective: "2026-09-10", AnnualRate: "6.500", Reference: "N1"}, {Effective: "2026-09-02", AnnualRate: "7.000", Reference: "N2"}}
		},
	} {
		bad := in
		bad.Fees = append([]Fee(nil), in.Fees...)
		change(&bad)
		if _, err := Prepare(bad); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

func TestFeesPaidInCashAreShownWithoutAnEntry(t *testing.T) {
	in := base()
	in.Method, in.BookBalance = VariableMethod, "99975.00"
	in.Payments[0].Fee, in.Payments[0].FeeReference = "25", " Statement line 4 "
	paper, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	if paper.Schedule[0].Fee != "25.00" || paper.FeesPaid != "25.00" || paper.Schedule[1].ExpectedInterest != "276.50" || paper.Input.Payments[0].FeeReference != "Statement line 4" {
		t.Fatalf("a fee paid in cash is shown on its payment and leaves principal and interest alone: %+v", paper)
	}
	if len(paper.Proposals) != 1 || paper.Proposals[0].Amount != "19.67" || !strings.Contains(strings.Join(paper.Questions, " "), "equals the fees paid in cash") {
		t.Fatalf("no entry is proposed for it; a book difference of exactly the fees names them as a possible cause: %+v %v", paper.Proposals, paper.Questions)
	}
	in.BookBalance = "99000.00"
	if paper, err = Prepare(in); err != nil || strings.Contains(strings.Join(paper.Questions, " "), "fees paid in cash") {
		t.Fatalf("another difference does not name them: %v %v", err, paper.Questions)
	}
	in.Payments[0].FeeReference = ""
	if _, err = Prepare(in); err == nil || !strings.Contains(err.Error(), "statement line") {
		t.Fatalf("a fee paid in cash needs its statement line: %v", err)
	}
	in.Payments[0].FeeReference, in.Method = "Statement line 4", Method
	if _, err = Prepare(in); err == nil || !strings.Contains(err.Error(), "approved loan procedure") {
		t.Fatalf("a fee paid in cash needs the procedure for fees: %v", err)
	}
}

func TestIssuanceCostsAmortizeToInterest(t *testing.T) {
	in := base()
	in.Method = CostsMethod
	in.IssuanceCosts = &IssuanceCosts{Amount: "1200", Start: "2026-01-01", End: "2026-12-31", Account: "Debt issuance costs", Reference: "Closing statement", Method: "equal_full_months_v1", MaterialityReference: "Memo 2026-01", BookedUnamortized: "400", BookingReference: "Trial balance Sep", MethodConfirmed: true, BookingConfirmed: true}
	paper, err := Prepare(in)
	if err != nil {
		t.Fatal(err)
	}
	// 1,200.00 over twelve months is 100.00 a month; nine months to September leave 300.00, against 400.00 booked.
	if c := paper.IssuanceCosts; c == nil || c.Month != "100.00" || c.ToDate != "900.00" || c.Unamortized != "300.00" {
		t.Fatalf("the month's amortization and the unamortized costs: %+v", paper.IssuanceCosts)
	}
	last := paper.Proposals[len(paper.Proposals)-1]
	if len(paper.Proposals) != 3 || last.DebitAccount != "Interest expense" || last.CreditAccount != "Debt issuance costs" || last.Amount != "100.00" || last.Date != "2026-09-30" {
		t.Fatalf("the amortization not yet booked is proposed to interest: %+v", paper.Proposals)
	}
	rated := in
	rated.RateChanges = []RateChange{{Effective: "2026-09-01", AnnualRate: "6.500", Reference: "Rate notice Aug 2026"}}
	if _, err = Prepare(rated); err != nil {
		t.Fatalf("the procedure that includes issuance costs also covers rate changes: %v", err)
	}
	in.IssuanceCosts.BookedUnamortized = "250"
	if paper, err = Prepare(in); err != nil || paper.Proposals[2].DebitAccount != "Debt issuance costs" || paper.Proposals[2].Amount != "50.00" {
		t.Fatalf("amortization booked beyond the schedule reverses: %v %+v", err, paper.Proposals)
	}
	for name, change := range map[string]func(c *IssuanceCosts, in *Input){
		"procedure that includes them":       func(c *IssuanceCosts, in *Input) { in.Method = VariableMethod },
		"their own account":                  func(c *IssuanceCosts, in *Input) { c.Account = "loan payable" },
		"not materially different":           func(c *IssuanceCosts, in *Input) { c.MaterialityReference = "" },
		"booked to their account":            func(c *IssuanceCosts, in *Input) { c.BookingConfirmed = false },
		"month-end balance (including zero)": func(c *IssuanceCosts, in *Input) { c.BookingReference = "" },
		"Debt issuance costs: ":              func(c *IssuanceCosts, in *Input) { c.End = "2025-12-31" },
	} {
		changed := base()
		changed.Method, changed.IssuanceCosts = CostsMethod, &IssuanceCosts{}
		*changed.IssuanceCosts = *in.IssuanceCosts
		change(changed.IssuanceCosts, &changed)
		if _, err = Prepare(changed); err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestRatesFromAnIndexAreTheirSum(t *testing.T) {
	in := base()
	in.Method = VariableMethod
	in.RateChanges = []RateChange{{Effective: "2026-09-01", AnnualRate: "6.160", Reference: "Note §3: SOFR + 2.500%", Index: "SOFR", IndexDate: "2026-08-31", IndexRate: "3.66", Margin: "2.500"}}
	if _, err := Prepare(in); err != nil {
		t.Fatalf("SOFR 3.66%% plus a 2.500%% margin is 6.160%%: %v", err)
	}
	for name, change := range map[string]func(c *RateChange){
		"a rate that is not the sum":          func(c *RateChange) { c.AnnualRate = "6.170" },
		"an index published after the change": func(c *RateChange) { c.IndexDate = "2026-09-02" },
		"an unsupported index":                func(c *RateChange) { c.Index = "PRIME" },
		"a missing margin":                    func(c *RateChange) { c.Margin = "" },
	} {
		changed := base()
		changed.Method, changed.RateChanges = VariableMethod, []RateChange{in.RateChanges[0]}
		change(&changed.RateChanges[0])
		if _, err := Prepare(changed); err == nil || !strings.Contains(err.Error(), "from an index") {
			t.Fatalf("%s is refused: %v", name, err)
		}
	}
}
