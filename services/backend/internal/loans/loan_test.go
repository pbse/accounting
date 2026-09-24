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
	if paper, err = Prepare(in); err != nil || len(paper.Blockers) != 1 || !strings.Contains(paper.Blockers[0], "does not tie") {
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
