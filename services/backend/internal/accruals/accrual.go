package accruals

import (
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/prepaids"
	"fmt"
	"strings"
	"time"
)

const Method = "fixed_incurred_amount_v1"
const ActualMethod = "actual_invoice_true_up_v1"
const RecurringMethod = "fixed_recurring_incurred_v1"

// ProRataMethod accrues the share of a contract amount for the service days elapsed by month-end, less what the vendor
// has already invoiced for it.
const ProRataMethod = "pro_rata_service_days_v1"

type Input struct {
	Entity                  string `json:"entity"`
	SourceReference         string `json:"source_reference"`
	ClosePeriod             string `json:"close_period"`
	ServiceStart            string `json:"service_start"`
	ServiceEnd              string `json:"service_end"`
	Amount                  string `json:"amount"`
	BookedBalance           string `json:"booked_balance"`
	BookingReference        string `json:"booking_reference"`
	EstimationBasis         string `json:"estimation_basis"`
	EstimateAmount          string `json:"estimate_amount,omitempty"`
	EstimateReference       string `json:"estimate_reference,omitempty"`
	ActualReference         string `json:"actual_reference,omitempty"`
	AccountantRuleReference string `json:"accountant_rule_reference,omitempty"`
	ContractAmount          string `json:"contract_amount,omitempty"`
	InvoicedToDate          string `json:"invoiced_to_date,omitempty"`
	ExpenseAccount          string `json:"expense_account"`
	LiabilityAccount        string `json:"liability_account"`
	Currency                string `json:"currency"`
	Basis                   string `json:"basis"`
	Method                  string `json:"method"`
	MethodConfirmed         bool   `json:"method_confirmed"`
	BookingConfirmed        bool   `json:"booking_confirmed"`
	ExamplesConfirmed       bool   `json:"examples_confirmed"`
	ReverseNextMonth        bool   `json:"reverse_next_month"`
}
type Workpaper struct {
	Status           string               `json:"status"`
	Input            Input                `json:"input"`
	Proposal         *prepaids.Adjustment `json:"proposal"`
	Reversal         *prepaids.Adjustment `json:"reversal"`
	EstimateVariance string               `json:"estimate_variance,omitempty"`
}

func Prepare(in Input) (Workpaper, error) {
	empty := Workpaper{}
	for _, field := range []*string{&in.Entity, &in.SourceReference, &in.BookingReference, &in.EstimationBasis, &in.ExpenseAccount, &in.LiabilityAccount} {
		*field = strings.TrimSpace(*field)
		if *field == "" || len(*field) > 200 {
			return empty, fmt.Errorf("Provide bounded client, source, estimate basis, ledger reference and account names (1–200 characters each)")
		}
	}
	if strings.EqualFold(in.ExpenseAccount, in.LiabilityAccount) {
		return empty, fmt.Errorf("Expense and liability accounts must differ")
	}
	if (in.Method != Method && in.Method != ActualMethod && in.Method != RecurringMethod && in.Method != ProRataMethod) || !ledger.ValidCurrency(in.Currency) || in.Basis != "accrual" || !in.MethodConfirmed || !in.BookingConfirmed || !in.ExamplesConfirmed {
		return empty, fmt.Errorf("Select and confirm a supported accrual method in the client's home currency, booked liability, synthetic examples and reversal choice")
	}
	period, err := time.Parse("2006-01", in.ClosePeriod)
	if err != nil || period.Year() < 1000 || period.Year() > 9998 {
		return empty, fmt.Errorf("Enter a supported close month")
	}
	start, e1 := time.Parse("2006-01-02", in.ServiceStart)
	end, e2 := time.Parse("2006-01-02", in.ServiceEnd)
	monthEnd := period.AddDate(0, 1, -1)
	// A pro-rata service may continue past month-end; only its elapsed share is accrued.
	if e1 != nil || e2 != nil || start.Year() < 1000 || end.Before(start) || start.After(monthEnd) || (end.After(monthEnd) && in.Method != ProRataMethod) {
		return empty, fmt.Errorf("Service must have been incurred by month-end; future services and inferred estimates are unsupported")
	}
	if in.Method == ProRataMethod {
		contract, contractErr := prepaids.Amount(in.ContractAmount)
		invoiced, invoicedErr := prepaids.Amount(in.InvoicedToDate)
		if contractErr != nil || contract == 0 || invoicedErr != nil {
			return empty, fmt.Errorf("Enter the positive contract amount for the whole service period and the amount invoiced to date, including an explicit 0.00")
		}
		through := end
		if through.After(monthEnd) {
			through = monthEnd
		}
		share := contract * (int64(through.Sub(start).Hours()/24) + 1) / (int64(end.Sub(start).Hours()/24) + 1)
		if invoiced > share {
			return empty, fmt.Errorf("Invoiced to date exceeds the share of the contract incurred by month-end; treat the excess as a prepaid")
		}
		in.ContractAmount, in.InvoicedToDate, in.Amount = prepaids.Money(contract), prepaids.Money(invoiced), prepaids.Money(share-invoiced)
	} else if in.ContractAmount != "" || in.InvoicedToDate != "" {
		return empty, fmt.Errorf("Contract and invoiced-to-date amounts apply to the pro-rata method only")
	}
	amount, err := prepaids.Amount(in.Amount)
	if err != nil || (amount == 0 && in.Method != ProRataMethod) {
		return empty, fmt.Errorf("Enter a positive supported closing accrual amount")
	}
	booked, err := prepaids.Amount(in.BookedBalance)
	if err != nil {
		return empty, fmt.Errorf("Enter the booked liability credit balance explicitly, including zero")
	}
	in.Amount = prepaids.Money(amount)
	in.BookedBalance = prepaids.Money(booked)
	result := Workpaper{Status: "draft", Input: in}
	if in.Method == ActualMethod {
		in.EstimateReference, in.ActualReference, in.AccountantRuleReference = strings.TrimSpace(in.EstimateReference), strings.TrimSpace(in.ActualReference), strings.TrimSpace(in.AccountantRuleReference)
		estimate, estimateErr := prepaids.Amount(in.EstimateAmount)
		if estimateErr != nil || estimate == 0 || in.EstimateReference == "" || len(in.EstimateReference) > 200 || in.ActualReference == "" || len(in.ActualReference) > 200 || in.EstimateReference == in.ActualReference || in.AccountantRuleReference == "" || len(in.AccountantRuleReference) > 200 {
			return empty, fmt.Errorf("Cite a distinct prior estimate, actual invoice, client accountant-approved rule, and positive estimate amount")
		}
		in.EstimateAmount = prepaids.Money(estimate)
		variance := amount - estimate
		if variance < 0 {
			result.EstimateVariance = "-" + prepaids.Money(-variance)
		} else {
			result.EstimateVariance = prepaids.Money(variance)
		}
	} else if in.Method == RecurringMethod {
		in.AccountantRuleReference = strings.TrimSpace(in.AccountantRuleReference)
		if in.AccountantRuleReference == "" || len(in.AccountantRuleReference) > 200 || in.EstimateAmount != "" || in.EstimateReference != "" || in.ActualReference != "" {
			return empty, fmt.Errorf("Cite the client accountant-approved recurring rule; prior estimates and actual invoices use the actual-invoice method")
		}
		if start.Format("2006-01") != in.ClosePeriod || end.Format("2006-01") != in.ClosePeriod {
			return empty, fmt.Errorf("Recurring accrual must cover incurred service in this close month only")
		}
	} else if in.Method == ProRataMethod {
		in.AccountantRuleReference = strings.TrimSpace(in.AccountantRuleReference)
		if in.AccountantRuleReference == "" || len(in.AccountantRuleReference) > 200 || in.EstimateAmount != "" || in.EstimateReference != "" || in.ActualReference != "" {
			return empty, fmt.Errorf("Cite the client accountant-approved pro-rata rule; prior estimates and actual invoices use the actual-invoice method")
		}
	} else if in.EstimateAmount != "" || in.EstimateReference != "" || in.ActualReference != "" || in.AccountantRuleReference != "" {
		return empty, fmt.Errorf("Estimate-to-actual references require the actual invoice true-up method")
	}
	result.Input = in
	delta := amount - booked
	if delta != 0 {
		debit, credit := in.ExpenseAccount, in.LiabilityAccount
		if delta < 0 {
			delta = -delta
			debit, credit = credit, debit
		}
		result.Proposal = &prepaids.Adjustment{Date: period.AddDate(0, 1, -1).Format("2006-01-02"), DebitAccount: debit, CreditAccount: credit, Amount: prepaids.Money(delta)}
		if in.ReverseNextMonth {
			result.Reversal = &prepaids.Adjustment{Date: period.AddDate(0, 1, 0).Format("2006-01-02"), DebitAccount: credit, CreditAccount: debit, Amount: prepaids.Money(delta)}
		}
	}
	return result, nil
}
