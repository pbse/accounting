package preparation

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/loans"
)

func TestALoansScheduleOpensItsFirstMonthAndItsCurrentPortion(t *testing.T) {
	fact := func(field, value string) Fact {
		return Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value}
	}
	// A 6% loan of 10,000 paying 500 of principal on the 15th: July's payment opens August, August's closes it.
	schedule := func(rate string, through string) Proposal {
		p := Proposal{Kind: "loan", Facts: []Fact{fact("lender", "First Bank"), fact("agreement_reference", "Note 2024-1"), fact("annual_rate", rate), fact("currency", "USD"), fact("loan_account", "Term loan"), fact("interest_account", "Interest expense")}}
		balance := 10000
		for at, i := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC), 1; !at.After(time.Date(2027, 12, 15, 0, 0, 0, 0, time.UTC)) && at.Format("2006-01") <= through; at, i = at.AddDate(0, 1, 0), i+1 {
			principal := 500
			if i == 1 {
				principal = 0
			}
			balance -= principal
			p.Lines = append(p.Lines, ScheduleLine{Line: i, Facts: []Fact{fact("date", at.Format("2006-01-02")), fact("interest", "50.00"), fact("principal", fmt.Sprintf("%d.00", principal)), fact("balance", fmt.Sprintf("%d.00", balance))}})
		}
		return p
	}
	prepare := func(in loans.Input) (map[string]any, error) {
		raw, _ := json.Marshal(in)
		paper, err := Calculate(Plan{Kind: "loan", Input: raw, Imported: &Imported{RootSourceID: "1", Label: "First Bank", Remaining: in.ClosingPrincipal}}, "Example", "2026-08")
		var out map[string]any
		json.Unmarshal(paper, &out)
		return out, err
	}
	in, err := ScheduledLoan(schedule("6.000", "2027-08"), "Example", "2026-08", "Prior schedule #1 · First Bank", "actual_365", false, "")
	if err != nil || in.OpeningPrincipal != "10000.00" || in.InterestPaidThrough != "2026-07-15" || len(in.Payments) != 1 || in.ClosingPrincipal != "9500.00" || in.ReclassCurrentPortion {
		t.Fatalf("the payment before the month opens it and the month's payment closes it: %+v %v", in, err)
	}
	paper, err := prepare(in)
	proposals, _ := paper["proposals"].([]any)
	if err != nil || len(proposals) != 0 || paper["input"].(map[string]any)["book_balance"] != "" {
		t.Fatalf("the loan is a schedule-only draft that keeps no booked balance: %v %v", paper, err)
	}
	// With a current portion, the principal of the 12 calendar months after month-end (September to August) is due.
	if in, err = ScheduledLoan(schedule("6.000", "2027-08"), "Example", "2026-08", "Prior schedule #1 · First Bank", "actual_365", true, "Loan, current portion"); err != nil || in.PrincipalDueNext12Months != "6000.00" || in.ScheduleReference == "" {
		t.Fatalf("the current portion is the next 12 months' principal: %+v %v", in, err)
	}
	if paper, err = prepare(in); err != nil || strings.Contains(fmt.Sprint(paper["questions"]), "ledger") {
		t.Fatalf("a schedule-only loan with a current portion prepares without a ledger difference: %v %v", paper["questions"], err)
	}
	if _, err = ScheduledLoan(schedule("6.000", "2027-05"), "Example", "2026-08", "Prior schedule #1 · First Bank", "actual_365", true, "Loan, current portion"); err == nil || !strings.Contains(err.Error(), "not the 12 months after") {
		t.Fatalf("a schedule that stops short of the year cannot state the current portion: %v", err)
	}
	// Rows whose mistakes cancel out are still refused at the first row that does not run.
	broken := schedule("6.000", "2027-08")
	broken.Lines[1].Facts[3].Value, broken.Lines[2].Facts[3].Value = "9550.00", "9000.00"
	if _, err = ScheduledLoan(broken, "Example", "2026-08", "r", "actual_365", false, ""); err == nil || !strings.Contains(err.Error(), "Row 2: 10000.00 less principal 500.00 is 9500.00, but the schedule shows 9550.00") {
		t.Fatalf("a row that does not run is refused: %v", err)
	}
	gap := schedule("6.000", "2027-08")
	gap.Lines = append(gap.Lines[:1], gap.Lines[4:]...)
	if _, err = ScheduledLoan(gap, "Example", "2026-11", "r", "actual_365", false, ""); err == nil || !strings.Contains(err.Error(), "more than 62 days") {
		t.Fatalf("a month opened by a payment over 62 days earlier is left to the lender's statement: %v", err)
	}
	if in, err = ScheduledLoan(schedule("0", "2027-08"), "Example", "2026-08", "r", "actual_365", false, ""); err == nil {
		if _, err = prepare(in); err == nil {
			t.Fatal("a zero-rate loan was prepared")
		}
	}
}

func TestALoansScheduleIsReadWithTheLoanAndItsRows(t *testing.T) {
	content := "First Bank amortization schedule, note 2024-1, 6.000% (USD), Term loan / Interest expense\nDate,Interest,Principal,Balance\n2026-07-15,$50.00,$0.00,\"$10,000.00\"\n2026-08-15,$50.00,$500.00,\"$9,500.00\""
	sources := []IntakeSource{{Source: Source{ID: "1", Name: "loan.txt", Digest: "h", Role: "evidence"}, Content: content}}
	rows := []string{"2026-07-15,$50.00,$0.00,\"$10,000.00\"", "2026-08-15,$50.00,$500.00,\"$9,500.00\""}
	result := agentResult{Kind: "loan", Summary: "Loan", Questions: []string{}, Sources: []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}{{"1", "schedule"}}, Lines: []struct {
		Line int    `json:"line"`
		Row  string `json:"row"`
	}{{1, rows[0]}, {2, rows[1]}}, Facts: []agentFact{{"lender", "First Bank", "1", "First Bank amortization", 0}, {"agreement_reference", "note 2024-1", "1", "note 2024-1", 0}, {"annual_rate", "6.000", "1", "6.000%", 0},
		{"currency", "USD", "1", "(USD)", 0}, {"loan_account", "Term loan", "1", "Term loan /", 0}, {"interest_account", "Interest expense", "1", "/ Interest expense", 0},
		{"date", "2026-07-15", "1", "2026-07-15,$50.00,$0.00", 1}, {"interest", "50.00", "1", "2026-07-15,$50.00", 1}, {"principal", "0.00", "1", "$50.00,$0.00,\"$10", 1}, {"balance", "10000.00", "1", "\"$10,000.00\"", 1},
		{"date", "2026-08-15", "1", "2026-08-15,$50.00", 2}, {"interest", "50.00", "1", "2026-08-15,$50.00", 2}, {"principal", "500.00", "1", "$500.00", 2}, {"balance", "9500.00", "1", "\"$9,500.00\"", 2}}}
	p, err := validateSchedule(result, "1", sources)
	if err != nil || len(p.Facts) != 6 || p.Facts[0].Value != "First Bank" || len(p.Lines) != 2 || p.Lines[1].Facts[3].Value != "9500.00" {
		t.Fatal("a loan schedule reads the loan once and each payment row, with formatted amounts", p, err)
	}
	for _, f := range append(p.Facts, p.Lines[1].Facts...) {
		if f.Question != "" {
			t.Fatal("every cited fact is read", f)
		}
	}
}
