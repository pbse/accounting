package accruals

import (
	"strings"
	"testing"
)

func TestFixedAccrualDifferenceAndReversal(t *testing.T) {
	in := Input{Entity: "Synthetic", SourceReference: "Service completion", ClosePeriod: "2026-09", ServiceStart: "2026-09-01", ServiceEnd: "2026-09-30", Amount: "1200.00", BookedBalance: "200.00", BookingReference: "September ledger", EstimationBasis: "Approved 12 hours at $100", ExpenseAccount: "Consulting expense", LiabilityAccount: "Accrued liabilities", Currency: "USD", Basis: "accrual", Method: Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, ReverseNextMonth: true}
	p, err := Prepare(in)
	if err != nil || p.Proposal == nil || p.Proposal.Amount != "1000.00" || p.Proposal.DebitAccount != in.ExpenseAccount || p.Reversal == nil || p.Reversal.Date != "2026-10-01" || p.Reversal.CreditAccount != in.ExpenseAccount {
		t.Fatalf("accrual difference/reversal: %+v %v", p, err)
	}
	in.BookedBalance = "1400.00"
	p, err = Prepare(in)
	if err != nil || p.Proposal.Amount != "200.00" || p.Proposal.DebitAccount != in.LiabilityAccount || p.Reversal.DebitAccount != in.ExpenseAccount {
		t.Fatal("overaccrual correction", err)
	}
	in.BookedBalance = "1200.00"
	p, err = Prepare(in)
	if err != nil || p.Proposal != nil || p.Reversal != nil {
		t.Fatal("duplicate accrual", err)
	}
	in.ServiceEnd = "2026-10-01"
	if _, err = Prepare(in); err == nil {
		t.Fatal("future service accepted")
	}
	in.ServiceEnd = "2026-09-30"
	in.ExamplesConfirmed = false
	if _, err = Prepare(in); err == nil {
		t.Fatal("unconfirmed method accepted")
	}
}

func TestActualInvoiceLinksEstimateAndOnlyReversesNewDifference(t *testing.T) {
	in := Input{Entity: "Synthetic", SourceReference: "Actual invoice", ClosePeriod: "2026-09", ServiceStart: "2026-09-01", ServiceEnd: "2026-09-30", Amount: "1250.00", BookedBalance: "1200.00", BookingReference: "Liability ledger", EstimationBasis: "Actual invoice confirmed incurred", ExpenseAccount: "Consulting expense", LiabilityAccount: "Accrued liabilities", Currency: "USD", Basis: "accrual", Method: ActualMethod, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, ReverseNextMonth: true, EstimateAmount: "1200.00", EstimateReference: "Approved estimate #12", ActualReference: "Invoice #18", AccountantRuleReference: "Client accountant rule v2"}
	p, err := Prepare(in)
	if err != nil || p.EstimateVariance != "50.00" || p.Proposal == nil || p.Proposal.Amount != "50.00" || p.Reversal == nil || p.Reversal.Amount != "50.00" {
		t.Fatalf("actual true-up: %+v %v", p, err)
	}
	in.BookedBalance = "1250.00"
	p, err = Prepare(in)
	if err != nil || p.Proposal != nil || p.Reversal != nil {
		t.Fatalf("already booked actual proposed twice: %+v %v", p, err)
	}
	in.AccountantRuleReference = ""
	if _, err = Prepare(in); err == nil {
		t.Fatal("unapproved method accepted")
	}
}

func TestRecurringAccrualRequiresCurrentMonthAndApprovedRule(t *testing.T) {
	in := Input{Entity: "Synthetic", SourceReference: "Current-month service confirmation", ClosePeriod: "2026-09", ServiceStart: "2026-09-01", ServiceEnd: "2026-09-30", Amount: "1200.00", BookedBalance: "200.00", BookingReference: "Current September liability ledger", EstimationBasis: "Approved $1,200 monthly service", AccountantRuleReference: "Client accountant rule R1", ExpenseAccount: "Consulting expense", LiabilityAccount: "Accrued consulting", Currency: "USD", Basis: "accrual", Method: RecurringMethod, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	p, err := Prepare(in)
	if err != nil || p.Proposal == nil || p.Proposal.Amount != "1000.00" {
		t.Fatalf("recurring accrual: %+v %v", p, err)
	}
	in.AccountantRuleReference = ""
	if _, err = Prepare(in); err == nil {
		t.Fatal("recurring rule missing")
	}
	in.AccountantRuleReference = "Client accountant rule R1"
	in.ServiceStart = "2026-08-31"
	if _, err = Prepare(in); err == nil {
		t.Fatal("prior-month service accepted")
	}
}

func TestProRataAccrualTakesTheElapsedShareLessInvoiced(t *testing.T) {
	in := Input{Entity: "Synthetic", SourceReference: "Quarterly maintenance contract", ClosePeriod: "2026-09", ServiceStart: "2026-08-15", ServiceEnd: "2026-11-14", ContractAmount: "9000", InvoicedToDate: "0.00", BookedBalance: "0.00", BookingReference: "September ledger", EstimationBasis: "Signed contract", ExpenseAccount: "Maintenance", LiabilityAccount: "Accrued maintenance", Currency: "USD", Basis: "accrual", Method: ProRataMethod, AccountantRuleReference: "Service contract rule 3", MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	p, err := Prepare(in)
	if err != nil || p.Input.Amount != "4597.82" || p.Input.ContractAmount != "9000.00" || p.Proposal.Amount != "4597.82" || p.Proposal.CreditAccount != "Accrued maintenance" {
		t.Fatalf("47 of 92 service days of 9000 accrue 4597.82: %+v %v", p, err)
	}
	in.InvoicedToDate, in.BookedBalance = "3000.00", "1000.00"
	if p, err = Prepare(in); err != nil || p.Input.Amount != "1597.82" || p.Proposal.Amount != "597.82" {
		t.Fatalf("what was invoiced is not accrued again: %+v %v", p, err)
	}
	in.Amount = "1.00"
	if p, err = Prepare(in); err != nil || p.Input.Amount != "1597.82" {
		t.Fatalf("a resent target is recomputed: %+v %v", p, err)
	}
	in.ServiceEnd = "2026-09-20"
	if p, err = Prepare(in); err != nil || p.Input.Amount != "6000.00" {
		t.Fatalf("a service ended by month-end accrues the whole contract less invoiced: %+v %v", p, err)
	}
	in.ServiceEnd, in.InvoicedToDate = "2026-11-14", "4597.82"
	if p, err = Prepare(in); err != nil || p.Input.Amount != "0.00" || p.Proposal.Amount != "1000.00" || p.Proposal.DebitAccount != "Accrued maintenance" {
		t.Fatalf("a fully invoiced share clears the booked liability: %+v %v", p, err)
	}
	for want, change := range map[string]func(*Input){
		"treat the excess as a prepaid":      func(i *Input) { i.InvoicedToDate = "4597.83" },
		"inferred estimates are unsupported": func(i *Input) { i.ServiceStart, i.ServiceEnd, i.InvoicedToDate = "2026-10-01", "2026-12-31", "0.00" },
		"including an explicit 0.00":         func(i *Input) { i.InvoicedToDate = "" },
		"ten years":                          func(i *Input) { i.ServiceEnd = "2036-08-16" },
		"pro-rata rule":                      func(i *Input) { i.AccountantRuleReference = "" },
		"pro-rata method only":               func(i *Input) { i.Method, i.ServiceEnd, i.AccountantRuleReference = Method, "2026-09-30", "" },
		"future services": func(i *Input) {
			i.Method, i.ContractAmount, i.InvoicedToDate, i.AccountantRuleReference = Method, "", "", ""
		},
	} {
		bad := in
		change(&bad)
		if _, err := Prepare(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want a refusal naming %q: %v", want, err)
		}
	}
}
