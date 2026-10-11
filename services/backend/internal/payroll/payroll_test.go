package payroll

import (
	"fmt"
	"strings"
	"testing"
)

func summary() Input {
	return Input{Entity: "Acme", ClosePeriod: "2026-09", Currency: "USD", Provider: "Gusto", SourceReference: "Payroll summary Sep 2026", FirstPayDate: "2026-09-15", LastPayDate: "2026-09-30",
		GrossWages: "10000.00", EmployeeTaxes: "1500.00", EmployeeDeductions: "500.00", NetPay: "8000.00", EmployerTaxes: "765.00", EmployerContributions: "300.00",
		ProviderDebits: "10265.00", LedgerDebits: "10265.00", LedgerReference: "Payroll clearing ledger Sep 2026",
		WageAccount: "Wages", TaxExpenseAccount: "Payroll taxes", BenefitsAccount: "Employee benefits", ClearingAccount: "Payroll clearing", LiabilityAccount: "Payroll liabilities", Method: Method,
		ApprovedConfirmed: true, NotBookedConfirmed: true}
}

func TestPayrollRecordsTheSummaryTiedToCashAndLiabilities(t *testing.T) {
	paper, err := Prepare(summary())
	if err != nil || len(paper.Blockers) != 0 || paper.TotalCost != "11065.00" || paper.Unpaid != "800.00" {
		t.Fatalf("a tied summary records its cost and leaves the unpaid deductions and contributions owed: %v %+v", err, paper)
	}
	var lines []string
	for _, p := range paper.Proposals {
		lines = append(lines, fmt.Sprintf("%s Dr %s / Cr %s %s", p.Date, p.DebitAccount, p.CreditAccount, p.Amount))
	}
	want := "2026-09-30 Dr Wages / Cr Payroll clearing 10000.00|2026-09-30 Dr Payroll taxes / Cr Payroll clearing 765.00|2026-09-30 Dr Employee benefits / Cr Payroll clearing 300.00|2026-09-30 Dr Payroll clearing / Cr Payroll liabilities 800.00"
	if strings.Join(lines, "|") != want {
		t.Fatalf("the clearing account nets to the provider's debits:\n%s", strings.Join(lines, "\n"))
	}

	for name, change := range map[string]func(*Input){
		"does not tie":     func(in *Input) { in.NetPay = "8100.00" },
		"provider debited": func(in *Input) { in.LedgerDebits = "10000.00" },
		"must cover":       func(in *Input) { in.ProviderDebits, in.LedgerDebits = "12000.00", "12000.00" },
	} {
		in := summary()
		change(&in)
		paper, err := Prepare(in)
		if err != nil || len(paper.Proposals) != 0 || len(paper.Blockers) != 1 || !strings.Contains(paper.Blockers[0], name) {
			t.Fatalf("%s: a summary that does not tie proposes nothing: %v %+v", name, err, paper)
		}
	}
	for name, change := range map[string]func(*Input){
		"pay date in another month": func(in *Input) { in.FirstPayDate = "2026-08-31" },
		"contributions, no account": func(in *Input) { in.BenefitsAccount = "" },
		"clearing is the liability": func(in *Input) { in.LiabilityAccount = "payroll CLEARING" },
		"expense is the clearing":   func(in *Input) { in.WageAccount = "Payroll clearing" },
		"no pay":                    func(in *Input) { in.NetPay = "0.00" },
		"other method":              func(in *Input) { in.Method = "payroll_run_v1" },
	} {
		in := summary()
		change(&in)
		if _, err := Prepare(in); err == nil {
			t.Fatalf("%s is refused", name)
		}
	}
}
