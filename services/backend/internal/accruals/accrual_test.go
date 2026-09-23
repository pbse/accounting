package accruals

import "testing"

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
