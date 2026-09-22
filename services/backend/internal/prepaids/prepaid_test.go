package prepaids

import (
	"encoding/json"
	"errors"
	"testing"

	"close.local/backend/internal/reference"
)

func example() Input {
	return Input{Entity: "Acme Services", SourceReference: "Invoice 001, page 1", Amount: "12000.00", ServiceStart: "2026-09-01", ServiceEnd: "2027-08-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software expense", PrepaidAccount: "Prepaid expense", Method: Method, MethodConfirmed: true, BookingConfirmed: true}
}

func TestPrepareReferenceAndConserveCents(t *testing.T) {
	result, err := Prepare(example())
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Schedule []Row }
	if err := json.Unmarshal(reference.Prepaid, &fixture); err != nil {
		t.Fatal(err)
	}
	actual, _ := json.Marshal(result.Schedule)
	expected, _ := json.Marshal(fixture.Schedule)
	if string(actual) != string(expected) || result.PeriodExpense != "1000.00" || result.PrepaidBalance != "11000.00" || result.Proposal == nil || result.Proposal.Amount != "11000.00" || result.Proposal.Date != "2026-09-30" {
		t.Fatalf("reference calculation disagrees: %+v", result)
	}
	input := example()
	input.Amount = "100.00"
	input.ServiceEnd = "2026-11-30"
	input.ClosePeriod = "2026-11"
	result, err = Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Schedule) != 3 || result.Schedule[0].Expense != "33.33" || result.Schedule[1].Remaining != "33.34" || result.Schedule[2].Expense != "33.34" || result.RecognizedExpense != "100.00" || result.PrepaidBalance != "0.00" || result.Proposal != nil {
		t.Fatalf("rounding or final-period proposal incorrect: %+v", result)
	}
}

func TestUnsupportedTermsBlockPreparation(t *testing.T) {
	for _, test := range []struct {
		name, field string
		change      func(*Input)
	}{
		{"partial month", "service_start", func(i *Input) { i.ServiceStart = "2026-09-02" }},
		{"unconfirmed booking", "booking_confirmed", func(i *Input) { i.BookingConfirmed = false }},
		{"unconfirmed method", "method_confirmed", func(i *Input) { i.MethodConfirmed = false }},
		{"fractional cent", "amount", func(i *Input) { i.Amount = "100.001" }},
		{"amount overflow", "amount", func(i *Input) { i.Amount = "999999999999999999999" }},
		{"cash basis", "basis", func(i *Input) { i.Basis = "cash" }},
		{"outside period", "close_period", func(i *Input) { i.ClosePeriod = "2027-09" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := example()
			test.change(&input)
			_, err := Prepare(input)
			var invalid *InputError
			if !errors.As(err, &invalid) || invalid.Field != test.field {
				t.Fatalf("expected %s blocker, got %v", test.field, err)
			}
		})
	}
}

func TestActualDaysCreditsAndBookedBalance(t *testing.T) {
	in := example()
	in.Method = DailyMethod
	in.ExamplesConfirmed = true
	in.Amount = "100.00"
	in.ServiceStart = "2028-01-16"
	in.ServiceEnd = "2028-03-15"
	in.ClosePeriod = "2028-02"
	p, err := Prepare(in)
	if err != nil || len(p.Schedule) != 3 || p.Schedule[0].Expense != "26.66" || p.Schedule[1].Expense != "48.33" || p.Schedule[2].Expense != "25.01" || p.PrepaidBalance != "25.01" {
		t.Fatalf("inclusive leap-year schedule: %+v %v", p, err)
	}
	in.CreditAmount = "10.00"
	in.CreditPeriod = "2028-02"
	in.CreditReference = "Credit memo"
	in.BookedBalance = "30.00"
	in.BookingReference = "Ledger after credit"
	p, err = Prepare(in)
	if err != nil || p.Schedule[0].Expense != "26.66" || p.Schedule[1].Expense != "41.74" || p.Schedule[2].Expense != "21.60" || p.Schedule[1].Credit != "10.00" || p.Proposal == nil || p.Proposal.Amount != "8.40" || p.Proposal.DebitAccount != in.ExpenseAccount {
		t.Fatalf("prospective credit and prior balance: %+v %v", p, err)
	}
	in.BookedBalance = "21.60"
	p, err = Prepare(in)
	if err != nil || p.Proposal != nil {
		t.Fatal("already-booked balance proposed again", err)
	}
	in.CreditAmount = "74.00"
	if _, err = Prepare(in); err == nil {
		t.Fatal("credit exceeded unamortized balance")
	}
	in.CreditAmount = "10.00"
	in.ExamplesConfirmed = false
	if _, err = Prepare(in); err == nil {
		t.Fatal("method examples not confirmed")
	}
	in = example()
	in.Method = DailyMethod
	in.ExamplesConfirmed = true
	in.ServiceStart = "2026-09-15"
	in.ServiceEnd = "2026-09-15"
	p, err = Prepare(in)
	if err != nil || len(p.Schedule) != 1 || p.PeriodExpense != "12000.00" || p.Proposal != nil {
		t.Fatal("one-day allocation", err)
	}
}

func TestMonthBoundaryCancellationTreatments(t *testing.T) {
	in := example()
	in.Amount = "100.00"
	in.ServiceEnd = "2026-11-30"
	in.ClosePeriod = "2026-10"
	in.CancellationPeriod = "2026-10"
	in.CancellationReference = "Approved cancellation and refund evidence"
	in.BookingReference = "October ledger after cancellation"
	in.ExamplesConfirmed = true
	in.CancellationMethod = FullRefundCancellation
	in.BookedBalance = "0.00"
	p, err := Prepare(in)
	if err != nil || p.Schedule[0].Expense != "33.33" || p.Schedule[1].Credit != "66.67" || p.Schedule[1].Expense != "0.00" || p.Schedule[2].Expense != "0.00" || p.PrepaidBalance != "0.00" || p.Proposal != nil {
		t.Fatalf("full-refund cancellation: %+v %v", p, err)
	}
	in.CancellationMethod = NoRefundCancellation
	in.BookedBalance = "66.67"
	p, err = Prepare(in)
	if err != nil || p.Schedule[1].Expense != "66.67" || p.Schedule[1].Remaining != "0.00" || p.Schedule[2].Expense != "0.00" || p.Proposal == nil || p.Proposal.Amount != "66.67" || p.Proposal.DebitAccount != in.ExpenseAccount {
		t.Fatalf("no-refund write-off: %+v %v", p, err)
	}
	in.CancellationMethod = FullRefundCancellation
	if _, err = Prepare(in); err == nil {
		t.Fatal("refund with outstanding booked balance must block")
	}
	in.CancellationMethod = NoRefundCancellation
	in.BookedBalance = "50.00"
	if _, err = Prepare(in); err == nil {
		t.Fatal("partial prior write-off must block")
	}
	in.BookedBalance = "66.67"
	in.CreditAmount, in.CreditPeriod, in.CreditReference = "1.00", "2026-10", "credit"
	if _, err = Prepare(in); err == nil {
		t.Fatal("combined cancellation and credit must block")
	}
}

func TestCarryScheduleDropsHistoricalBookedBalance(t *testing.T) {
	in := example()
	in.Amount = "100.00"
	in.ServiceEnd = "2026-11-30"
	in.ClosePeriod = "2026-10"
	in.CreditAmount, in.CreditPeriod, in.CreditReference = "10.00", "2026-10", "October credit"
	in.BookedBalance, in.BookingReference = "25.00", "October ledger"
	p, err := ScheduleOnly(in)
	if err != nil || p.Input.BookedBalance != "" || p.Input.BookingReference != "" || p.Proposal != nil || p.Schedule[1].Credit != "10.00" {
		t.Fatalf("historical booked balance leaked into carried schedule: %+v %v", p, err)
	}
	in = p.Input
	in.ClosePeriod = "2026-11"
	p, err = ScheduleOnly(in)
	if err != nil || p.Input.BookedBalance != "" || p.Proposal != nil || p.PrepaidBalance != "0.00" {
		t.Fatalf("second-generation carry lost credit or repeated booking: %+v %v", p, err)
	}
}
