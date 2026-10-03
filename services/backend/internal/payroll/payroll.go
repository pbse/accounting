// Package payroll records a month's payroll from the provider's approved summary; it never runs payroll.
package payroll

import (
	"fmt"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/prepaids"
)

const Method = "payroll_summary_v1"

type Input struct {
	Entity                string `json:"entity"`
	ClosePeriod           string `json:"close_period"`
	Currency              string `json:"currency"`
	Provider              string `json:"provider"`
	SourceReference       string `json:"source_reference"`
	FirstPayDate          string `json:"first_pay_date"`
	LastPayDate           string `json:"last_pay_date"`
	GrossWages            string `json:"gross_wages"`
	EmployeeTaxes         string `json:"employee_taxes"`
	EmployeeDeductions    string `json:"employee_deductions"`
	NetPay                string `json:"net_pay"`
	EmployerTaxes         string `json:"employer_taxes"`
	EmployerContributions string `json:"employer_contributions"`
	ProviderDebits        string `json:"provider_debits"`
	LedgerDebits          string `json:"ledger_debits"`
	LedgerReference       string `json:"ledger_reference"`
	WageAccount           string `json:"wage_account"`
	TaxExpenseAccount     string `json:"tax_expense_account"`
	BenefitsAccount       string `json:"benefits_account,omitempty"`
	ClearingAccount       string `json:"clearing_account"`
	LiabilityAccount      string `json:"liability_account"`
	Method                string `json:"method"`
	ApprovedConfirmed     bool   `json:"approved_confirmed,omitempty"`
	NotBookedConfirmed    bool   `json:"not_booked_confirmed,omitempty"`
}

type Workpaper struct {
	Status    string                `json:"status"`
	Method    string                `json:"method"`
	Input     Input                 `json:"input"`
	TotalCost string                `json:"total_cost"`
	Unpaid    string                `json:"unpaid"`
	Proposals []prepaids.Adjustment `json:"proposals"`
	Blockers  []string              `json:"blockers"`
}

func Prepare(in Input) (Workpaper, error) {
	fail := func(format string, args ...any) (Workpaper, error) { return Workpaper{}, fmt.Errorf(format, args...) }
	if in.BenefitsAccount = strings.TrimSpace(in.BenefitsAccount); len(in.BenefitsAccount) > 200 {
		return fail("Name the benefits expense account in at most 200 characters.")
	}
	for _, v := range []*string{&in.Entity, &in.Provider, &in.SourceReference, &in.LedgerReference, &in.WageAccount, &in.TaxExpenseAccount, &in.ClearingAccount, &in.LiabilityAccount} {
		if *v = strings.TrimSpace(*v); *v == "" || len(*v) > 200 {
			return fail("Provide the client, payroll provider, summary and ledger references, and the wage, tax expense, clearing and liability accounts (1–200 characters each).")
		}
	}
	if in.Method != Method {
		return fail("Choose the payroll summary method.")
	}
	if !in.ApprovedConfirmed || !in.NotBookedConfirmed {
		return fail("Confirm the summary is the provider's final, approved payroll for these pay dates, and that the ledger shows its bank debits in the clearing account with this payroll not yet recorded.")
	}
	if !ledger.ValidCurrency(in.Currency) {
		return fail("Use the client's home currency as a three-letter code with two decimals, such as USD or CAD.")
	}
	month, err := time.Parse("2006-01", in.ClosePeriod)
	if err != nil {
		return fail("Enter a supported close month.")
	}
	first, err1 := time.Parse("2006-01-02", in.FirstPayDate)
	last, err2 := time.Parse("2006-01-02", in.LastPayDate)
	if err1 != nil || err2 != nil || first.Format("2006-01") != in.ClosePeriod || last.Format("2006-01") != in.ClosePeriod || last.Before(first) {
		return fail("The summary's pay dates must fall in the close month, first to last; other months need their own summary.")
	}
	cents := map[string]int64{}
	for _, a := range []struct {
		name  string
		value *string
	}{{"gross wages", &in.GrossWages}, {"employee taxes", &in.EmployeeTaxes}, {"employee deductions", &in.EmployeeDeductions}, {"net pay", &in.NetPay},
		{"employer taxes", &in.EmployerTaxes}, {"employer contributions", &in.EmployerContributions}, {"provider debits", &in.ProviderDebits}, {"ledger debits", &in.LedgerDebits}} {
		c, err := prepaids.Amount(*a.value)
		if err != nil {
			return fail("Enter %s as a nonnegative amount with at most two decimals.", a.name)
		}
		cents[a.name], *a.value = c, prepaids.Money(c)
	}
	if cents["gross wages"] == 0 || cents["net pay"] == 0 {
		return fail("Gross wages and net pay must be more than zero; a summary without pay has nothing to record.")
	}
	if cents["employer contributions"] > 0 && in.BenefitsAccount == "" {
		return fail("Name the benefits expense account for the employer contributions.")
	}
	for _, expense := range []string{in.WageAccount, in.TaxExpenseAccount, in.BenefitsAccount} {
		if strings.EqualFold(expense, in.ClearingAccount) || strings.EqualFold(expense, in.LiabilityAccount) {
			return fail("Expense accounts must differ from the clearing and liability accounts.")
		}
	}
	if strings.EqualFold(in.ClearingAccount, in.LiabilityAccount) {
		return fail("The clearing and liability accounts must differ.")
	}
	show := func(c int64) string { return ledger.Show(prepaids.Money(c), in.Currency) }
	total := cents["gross wages"] + cents["employer taxes"] + cents["employer contributions"]
	unpaid := total - cents["provider debits"]
	result := Workpaper{Status: "draft", Method: Method, Input: in, TotalCost: prepaids.Money(total), Unpaid: prepaids.Money(max(unpaid, 0)), Proposals: []prepaids.Adjustment{}, Blockers: []string{}}
	if withheld := cents["gross wages"] - cents["employee taxes"] - cents["employee deductions"]; withheld != cents["net pay"] {
		result.Blockers = append(result.Blockers, fmt.Sprintf("The summary does not tie: gross wages %s less employee taxes %s and deductions %s is %s, not the net pay %s.",
			show(cents["gross wages"]), show(cents["employee taxes"]), show(cents["employee deductions"]), show(withheld), show(cents["net pay"])))
	}
	if cents["provider debits"] < cents["net pay"] || unpaid < 0 {
		result.Blockers = append(result.Blockers, fmt.Sprintf("The provider's debits (%s) must cover the net pay (%s) and cannot exceed the payroll's total cost (%s); check the summary's funding section.",
			show(cents["provider debits"]), show(cents["net pay"]), show(total)))
	}
	if cents["ledger debits"] != cents["provider debits"] {
		result.Blockers = append(result.Blockers, fmt.Sprintf("The provider debited %s but the ledger shows %s of payroll debits in %s; find the missing or extra bank debit before recording.",
			show(cents["provider debits"]), show(cents["ledger debits"]), in.ClearingAccount))
	}
	if len(result.Blockers) > 0 {
		return result, nil
	}
	end := month.AddDate(0, 1, -1).Format("2006-01-02")
	add := func(debit, credit string, c int64) {
		if c > 0 {
			result.Proposals = append(result.Proposals, prepaids.Adjustment{Date: end, DebitAccount: debit, CreditAccount: credit, Amount: prepaids.Money(c)})
		}
	}
	add(in.WageAccount, in.ClearingAccount, cents["gross wages"])
	add(in.TaxExpenseAccount, in.ClearingAccount, cents["employer taxes"])
	add(in.BenefitsAccount, in.ClearingAccount, cents["employer contributions"])
	add(in.ClearingAccount, in.LiabilityAccount, unpaid)
	return result, nil
}
