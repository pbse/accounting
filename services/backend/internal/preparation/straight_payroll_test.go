package preparation

import (
	"strings"
	"testing"

	"close.local/backend/internal/payroll"
)

// A payroll summary is prepared straight through only in the client's currency, every figure printed where it is quoted.
func TestStraightPayrollReadsTheClientsCurrencyAndItsOwnFigures(t *testing.T) {
	lines := []string{"Gusto payroll summary, September 2026", "Pay dates 2026-09-15 to 2026-09-30", "Gross wages 10,000", "Employee taxes 1,500.00", "Employee deductions 500.00",
		"Net pay 8,000.00", "Employer taxes 765.00", "Employer contributions 0.00", "Debited from your bank C$10,265.00"}
	run := func(extra string) string {
		summary := IntakeSource{Source: Source{ID: "1", Digest: "s", Name: "summary.txt"}, Content: strings.Join(lines, "\n") + extra}
		ledger := IntakeSource{Source: Source{ID: "2", Digest: "l", Name: "ledger.txt"}, Content: "Payroll Clearing ledger\nGusto debits 10,265.00"}
		fact := func(field, value, source, quote string) Fact {
			return Fact{Field: field, Label: field, Value: value, Candidates: []Candidate{{Value: value, SourceID: source, Quote: quote}}}
		}
		p := Proposal{Kind: "payroll", Engine: "openai", Sources: []Source{{ID: "1", Role: "support", Name: "summary.txt"}, {ID: "2", Role: "ledger", Name: "ledger.txt"}}, Facts: []Fact{
			fact("provider", "Gusto", "1", lines[0]), fact("source_reference", "Gusto payroll summary, September 2026", "1", lines[0]), fact("first_pay_date", "2026-09-15", "1", lines[1]),
			fact("last_pay_date", "2026-09-30", "1", lines[1]), fact("gross_wages", "10000.00", "1", lines[2]), fact("employee_taxes", "1500.00", "1", lines[3]),
			fact("employee_deductions", "500.00", "1", lines[4]), fact("net_pay", "8000.00", "1", lines[5]), fact("employer_taxes", "765.00", "1", lines[6]),
			fact("employer_contributions", "0.00", "1", lines[7]), fact("provider_debits", "10265.00", "1", lines[8]),
			fact("ledger_debits", "10265.00", "2", "Gusto debits 10,265.00"), fact("ledger_reference", "Payroll Clearing ledger", "2", "Payroll Clearing ledger"),
		}}
		procedure := Procedure{ID: "4", Kind: "payroll", Method: payroll.Method, Enabled: true}
		prior := &PriorPayroll{ID: "9", Period: "2026-08", Input: payroll.Input{WageAccount: "Wages", TaxExpenseAccount: "Payroll Tax Expense", ClearingAccount: "Payroll Clearing", LiabilityAccount: "Payroll Liabilities"}}
		_, reason := StraightPayroll(p, []IntakeSource{summary, ledger}, procedure, Source{ID: "3", Digest: "r", Name: "Payroll procedure"}, "CAD", "Example", "2026-09", false, map[string]bool{}, prior)
		return reason
	}
	// The client's own dollar (C$) and a figure printed without cents are read as written.
	if reason := run(""); reason != "" {
		t.Fatal("a Canadian summary in its own dollars, a figure without cents among them, is prepared:", reason)
	}
	// A run of digits inside a date or with more decimals is no figure; a whole figure is one, grouped or not.
	for _, c := range []struct {
		quote string
		cents int64
		want  bool
	}{{"Pay dates 2026-09-30", 202600, false}, {"Gross wages 10,000.000", 1000000, false}, {"Gross wages 10,000", 1000000, true}, {"Net pay 10000.00.", 1000000, true}} {
		if printsAmount(c.quote, c.cents) != c.want {
			t.Fatalf("%q prints %d cents: %v", c.quote, c.cents, !c.want)
		}
	}
	// Another currency, as a country's dollar or a code in any case, holds it.
	for _, extra := range []string{"\nTotal US$10,265.00", "\nAll amounts in eur", "\nConverted at 1.36 USD", "\nTotal €10,265.00"} {
		if reason := run(extra); !strings.Contains(reason, "another currency") {
			t.Fatalf("a summary printing %q waits for a person: %q", extra, reason)
		}
	}
}
