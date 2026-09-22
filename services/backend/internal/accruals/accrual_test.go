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
