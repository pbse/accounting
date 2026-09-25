// Package loans checks a month of loan activity against the lender's statement.
// The statement's principal/interest split is authoritative; recomputed interest only raises questions.
package loans

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/prepaids"
)

const Method = "lender_statement_v1"

// VariableMethod also checks dated rate changes and lender fees added to the loan balance.
// The product requires a separately approved procedure for these treatments.
const VariableMethod = "lender_statement_variable_v1"

// CostsMethod covers everything VariableMethod does plus debt issuance costs, under its own approved procedure.
const CostsMethod = "lender_statement_costs_v1"

var DayCounts = map[string]string{
	"actual_360":  "Actual/360",
	"actual_365":  "Actual/365 (fixed)",
	"30_360_bond": "30/360 bond basis (US, no February rule)",
	"30e_360":     "30E/360 (Eurobond)",
}

type RateChange struct {
	Effective  string `json:"effective"`
	AnnualRate string `json:"annual_rate"`
	Reference  string `json:"reference"`
}

type Fee struct {
	Date        string `json:"date"`
	Amount      string `json:"amount"`
	Description string `json:"description"`
	Reference   string `json:"reference"`
}

type Payment struct {
	Date      string `json:"date"`
	Principal string `json:"principal"`
	Interest  string `json:"interest"`
	// A fee the lender charged and the client paid in cash with this payment; it is not added to the balance.
	Fee          string `json:"fee,omitempty"`
	FeeReference string `json:"fee_reference,omitempty"`
}

// IssuanceCosts are costs of obtaining the loan, kept in their own account as a deduction from the debt and amortized
// to interest straight-line over the term, where the accountant concluded that is not materially different from the
// interest method.
type IssuanceCosts struct {
	Amount               string `json:"amount"`
	Start                string `json:"start"`
	End                  string `json:"end"`
	Account              string `json:"account"`
	Reference            string `json:"reference"`
	Method               string `json:"method"`
	MaterialityReference string `json:"materiality_reference"`
	BookedUnamortized    string `json:"booked_unamortized"`
	BookingReference     string `json:"booking_reference"`
	MethodConfirmed      bool   `json:"method_confirmed"`
	BookingConfirmed     bool   `json:"booking_confirmed"`
}

type Input struct {
	Entity                   string         `json:"entity"`
	ClosePeriod              string         `json:"close_period"`
	Lender                   string         `json:"lender"`
	SourceReference          string         `json:"source_reference"`
	AgreementReference       string         `json:"agreement_reference"`
	Currency                 string         `json:"currency"`
	AnnualRate               string         `json:"annual_rate"`
	DayCount                 string         `json:"day_count"`
	OpeningPrincipal         string         `json:"opening_principal"`
	InterestPaidThrough      string         `json:"interest_paid_through"`
	Payments                 []Payment      `json:"payments"`
	RateChanges              []RateChange   `json:"rate_changes,omitempty"`
	Fees                     []Fee          `json:"fees,omitempty"`
	FeeAccount               string         `json:"fee_account,omitempty"`
	BookedFees               string         `json:"booked_fees,omitempty"`
	ClosingPrincipal         string         `json:"closing_principal"`
	BookBalance              string         `json:"book_balance"`
	LoanAccount              string         `json:"loan_account"`
	InterestAccount          string         `json:"interest_account"`
	AccrueInterest           bool           `json:"accrue_interest"`
	AccruedInterestAccount   string         `json:"accrued_interest_account,omitempty"`
	BookedAccruedInterest    string         `json:"booked_accrued_interest,omitempty"`
	ReclassCurrentPortion    bool           `json:"reclass_current_portion"`
	PrincipalDueNext12Months string         `json:"principal_due_next_12_months,omitempty"`
	ScheduleReference        string         `json:"schedule_reference,omitempty"`
	CurrentPortionAccount    string         `json:"current_portion_account,omitempty"`
	BookedCurrentPortion     string         `json:"booked_current_portion,omitempty"`
	IssuanceCosts            *IssuanceCosts `json:"issuance_costs,omitempty"`
	Method                   string         `json:"method"`
	MethodConfirmed          bool           `json:"method_confirmed"`
	RateConfirmed            bool           `json:"rate_confirmed"`
	BookingConfirmed         bool           `json:"booking_confirmed"`
}

type Row struct {
	Date             string   `json:"date"`
	PrincipalBefore  string   `json:"principal_before"`
	Days             int64    `json:"days"`
	Principal        string   `json:"principal"`
	Interest         string   `json:"interest"`
	ExpectedInterest string   `json:"expected_interest"`
	Difference       string   `json:"difference"`
	Rates            []string `json:"rates,omitempty"`
	Fee              string   `json:"fee,omitempty"`
}

type Accrual struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Days   int64  `json:"days"`
	Amount string `json:"amount"`
}

type Workpaper struct {
	Status          string   `json:"status"`
	Method          string   `json:"method"`
	Input           Input    `json:"input"`
	Schedule        []Row    `json:"schedule"`
	AccruedInterest *Accrual `json:"accrued_interest,omitempty"`
	BookDifference  string   `json:"book_difference"`
	Fees            string   `json:"fees,omitempty"`
	FeesPaid        string   `json:"fees_paid,omitempty"`
	// Unamortized issuance costs at month-end are shown as a deduction from the debt, not an asset.
	IssuanceCosts *Amortization         `json:"issuance_costs,omitempty"`
	Proposals     []prepaids.Adjustment `json:"proposals"`
	Questions     []string              `json:"questions"`
	Blockers      []string              `json:"blockers"`
}

type Amortization struct {
	Month       string `json:"month"`
	ToDate      string `json:"to_date"`
	Unamortized string `json:"unamortized"`
}

var ratePattern = regexp.MustCompile(`^\d{1,2}(\.\d{1,3})?$`)

func signed(cents int64) string {
	if cents < 0 {
		return "-" + prepaids.Money(-cents)
	}
	return prepaids.Money(cents)
}

// Days counts (from, to] under the note's convention.
func Days(convention string, from, to time.Time) int64 {
	if convention == "actual_360" || convention == "actual_365" {
		return int64(to.Sub(from).Hours() / 24)
	}
	d1, d2 := from.Day(), to.Day()
	if d1 == 31 {
		d1 = 30
	}
	if d2 == 31 && (convention == "30e_360" || d1 == 30) {
		d2 = 30
	}
	return int64(360*(to.Year()-from.Year()) + 30*(int(to.Month())-int(from.Month())) + d2 - d1)
}

// Interest is principal × rate × days / basis, rounded half-up to the cent.
func Interest(principal, rateMilli, days int64, convention string) int64 {
	num := new(big.Int).Mul(big.NewInt(principal), big.NewInt(rateMilli))
	return rounded(num.Mul(num, big.NewInt(days)), convention)
}

// rounded divides a sum of principal × rate × days by the basis once, half-up to the cent.
func rounded(num *big.Int, convention string) int64 {
	basis := int64(360)
	if convention == "actual_365" {
		basis = 365
	}
	den := big.NewInt(100 * 1000 * basis)
	num = new(big.Int).Mul(num, big.NewInt(2))
	num.Add(num, den)
	return num.Quo(num, den.Mul(den, big.NewInt(2))).Int64()
}

func parseRate(text string) int64 {
	whole, fraction, _ := strings.Cut(text, ".")
	rate, _ := strconv.ParseInt(whole+(fraction + "000")[:3], 10, 64)
	return rate
}

func Prepare(in Input) (Workpaper, error) {
	fail := func(format string, args ...any) (Workpaper, error) { return Workpaper{}, fmt.Errorf(format, args...) }
	for _, field := range []*string{&in.Entity, &in.Lender, &in.SourceReference, &in.AgreementReference, &in.LoanAccount, &in.InterestAccount} {
		*field = strings.TrimSpace(*field)
		if *field == "" || len(*field) > 200 {
			return fail("Provide the client, lender, statement and note references, and loan and interest accounts (1–200 characters each).")
		}
	}
	if strings.EqualFold(in.LoanAccount, in.InterestAccount) {
		return fail("Loan and interest accounts must differ.")
	}
	if (in.Method != Method && in.Method != VariableMethod && in.Method != CostsMethod) || !ledger.ValidCurrency(in.Currency) || !in.MethodConfirmed || !in.RateConfirmed || !in.BookingConfirmed {
		return fail("Confirm the lender-statement method, the rate and day-count from the note, and the month-end book balances (USD only).")
	}
	if _, ok := DayCounts[in.DayCount]; !ok {
		return fail("Choose the exact day-count method stated in the note.")
	}
	month, err := time.Parse("2006-01", in.ClosePeriod)
	if err != nil || month.Year() < 1000 || month.Year() > 9998 {
		return fail("Enter a supported close month.")
	}
	monthEnd := month.AddDate(0, 1, -1)
	if !ratePattern.MatchString(in.AnnualRate) {
		return fail("Enter the annual rate as a percent with at most three decimals, for example 6.250.")
	}
	rateMilli := parseRate(in.AnnualRate)
	if rateMilli == 0 {
		return fail("A zero-rate loan has no interest to check; prepare it as a reconciliation instead.")
	}
	amount := func(value, label string, required bool) (int64, bool, error) {
		if value == "" && !required {
			return 0, false, nil
		}
		cents, err := prepaids.Amount(value)
		if err != nil {
			return 0, false, fmt.Errorf("Enter %s as a nonnegative amount with at most two decimals.", label)
		}
		return cents, true, nil
	}
	opening, _, err := amount(in.OpeningPrincipal, "opening principal", true)
	if err != nil {
		return Workpaper{}, err
	}
	closing, _, err := amount(in.ClosingPrincipal, "the lender's closing principal", true)
	if err != nil {
		return Workpaper{}, err
	}
	book, _, err := amount(in.BookBalance, "the book loan balance", true)
	if err != nil {
		return Workpaper{}, err
	}
	paidThrough, err := time.Parse("2006-01-02", in.InterestPaidThrough)
	if err != nil {
		return fail("Enter the date interest was paid through before this month's first payment.")
	}
	if len(in.Payments) > 31 {
		return fail("At most 31 payments per month are supported.")
	}
	if len(in.RateChanges) > 12 || len(in.Fees) > 12 {
		return fail("At most 12 rate changes and 12 fees per month are supported.")
	}
	if len(in.RateChanges)+len(in.Fees) > 0 && in.Method == Method {
		return fail("Rate changes and fees added to the loan balance need the firm's approved variable-rate loan procedure.")
	}
	// A rate applies from its effective date; days are counted (from, to], so the old rate covers the day before.
	type change struct {
		at   time.Time
		rate int64
	}
	changes := []change{}
	for i := range in.RateChanges {
		c := &in.RateChanges[i]
		c.Reference = strings.TrimSpace(c.Reference)
		at, err := time.Parse("2006-01-02", c.Effective)
		if err != nil || !at.After(paidThrough) || at.After(monthEnd) || (i > 0 && !at.After(changes[i-1].at)) || !ratePattern.MatchString(c.AnnualRate) || c.Reference == "" || len(c.Reference) > 200 {
			return fail("Rate change %d needs an effective date after interest was paid through and by month-end, in date order, a rate such as 6.500, and the notice it comes from.", i+1)
		}
		changes = append(changes, change{at, parseRate(c.AnnualRate)})
	}
	// A fee is added to the balance at the end of its day: it bears interest from the next day, and a
	// payment on the same day is applied before it.
	type charge struct {
		at     time.Time
		amount int64
	}
	charges, totalFees := []charge{}, int64(0)
	for i := range in.Fees {
		f := &in.Fees[i]
		f.Description, f.Reference = strings.TrimSpace(f.Description), strings.TrimSpace(f.Reference)
		at, err := time.Parse("2006-01-02", f.Date)
		cents, amountErr := prepaids.Amount(f.Amount)
		if err != nil || at.Format("2006-01") != in.ClosePeriod || !at.After(paidThrough) || amountErr != nil || cents == 0 || f.Description == "" || len(f.Description) > 200 || f.Reference == "" || len(f.Reference) > 200 {
			return fail("Fee %d needs a date in the close month after interest was paid through, a positive amount, a description and the statement line it comes from. A fee paid in cash with a payment is entered on that payment instead.", i+1)
		}
		f.Amount = prepaids.Money(cents)
		charges, totalFees = append(charges, charge{at, cents}), totalFees+cents
	}
	slices.SortStableFunc(charges, func(a, b charge) int { return a.at.Compare(b.at) })
	result := Workpaper{Status: "draft", Method: in.Method, Schedule: []Row{}, Proposals: []prepaids.Adjustment{}, Questions: []string{}, Blockers: []string{}}
	if paidThrough.After(monthEnd) || paidThrough.Before(month.AddDate(0, 0, -62)) {
		result.Blockers = append(result.Blockers, "Interest paid-through date must fall between two months before this month and month-end, so opening principal is known for the whole window.")
	}
	outstanding, last, charged, feesPaid := opening, paidThrough, 0, int64(0)
	rateOn := func(day time.Time) int64 {
		rate := rateMilli
		for _, c := range changes {
			if !c.at.After(day) {
				rate = c.rate
			}
		}
		return rate
	}
	addFees := func(through time.Time) {
		for ; charged < len(charges) && !charges[charged].at.After(through); charged++ {
			outstanding += charges[charged].amount
		}
	}
	// window is the expected interest for (from, to], split where a rate changes or a fee is added.
	window := func(from, to time.Time) (int64, []string) {
		points := []time.Time{to}
		for _, c := range changes {
			if b := c.at.AddDate(0, 0, -1); b.After(from) && b.Before(to) {
				points = append(points, b)
			}
		}
		for _, f := range charges {
			if f.at.After(from) && f.at.Before(to) {
				points = append(points, f.at)
			}
		}
		slices.SortFunc(points, func(a, b time.Time) int { return a.Compare(b) })
		sum, rates, cur := new(big.Int), []string{}, from
		for _, b := range points {
			if b.After(cur) {
				rate := rateOn(b)
				term := new(big.Int).Mul(big.NewInt(outstanding), big.NewInt(rate))
				// 30/360 is not additive across month ends, so segments are differences of counts from the window start.
				sum.Add(sum, term.Mul(term, big.NewInt(Days(in.DayCount, from, b)-Days(in.DayCount, from, cur))))
				if label := fmt.Sprintf("%d.%03d%%", rate/1000, rate%1000); !slices.Contains(rates, label) {
					rates = append(rates, label)
				}
				cur = b
			}
			if b.Before(to) {
				addFees(b)
			}
		}
		return rounded(sum, in.DayCount), rates
	}
	for i := range in.Payments {
		p := &in.Payments[i]
		date, err := time.Parse("2006-01-02", p.Date)
		if err != nil || date.Format("2006-01") != in.ClosePeriod || !date.After(last) {
			return fail("Payment %d must be dated in the close month, after the paid-through date and after the previous payment.", i+1)
		}
		principal, _, err := amount(p.Principal, fmt.Sprintf("payment %d principal", i+1), true)
		if err != nil {
			return Workpaper{}, err
		}
		interest, _, err := amount(p.Interest, fmt.Sprintf("payment %d interest", i+1), true)
		if err != nil {
			return Workpaper{}, err
		}
		p.FeeReference = strings.TrimSpace(p.FeeReference)
		if p.Fee != "" || p.FeeReference != "" {
			if in.Method == Method {
				return fail("Fees paid in cash need the firm's approved loan procedure for rate changes and fees.")
			}
			fee, err := prepaids.Amount(p.Fee)
			if err != nil || fee == 0 || p.FeeReference == "" || len(p.FeeReference) > 200 {
				return fail("Payment %d's fee paid in cash needs a positive amount and the statement line it comes from.", i+1)
			}
			p.Fee, feesPaid = prepaids.Money(fee), feesPaid+fee
		}
		days := Days(in.DayCount, last, date)
		expected, rates := window(last, date)
		if principal > outstanding {
			return fail("Payment %d principal exceeds the principal outstanding.", i+1)
		}
		p.Principal, p.Interest = prepaids.Money(principal), prepaids.Money(interest)
		row := Row{Date: p.Date, PrincipalBefore: prepaids.Money(outstanding), Days: days, Principal: p.Principal, Interest: p.Interest, ExpectedInterest: prepaids.Money(expected), Difference: signed(interest - expected), Fee: p.Fee}
		if len(changes)+len(charges) > 0 {
			row.Rates = rates
		}
		result.Schedule = append(result.Schedule, row)
		if interest != expected {
			result.Questions = append(result.Questions, fmt.Sprintf("The lender charged %s interest on %s; the rate (%s) gives %s for %d days. Check for a rate change, fees or a different day-count before relying on the split.", ledger.Show(prepaids.Money(interest), in.Currency), p.Date, strings.Join(rates, " then "), ledger.Show(prepaids.Money(expected), in.Currency), days))
		}
		outstanding -= principal
		addFees(date)
		last = date
	}
	var accrued int64
	if in.AccrueInterest {
		accrued, _ = window(last, monthEnd)
	}
	addFees(monthEnd)
	if outstanding != closing {
		result.Blockers = append(result.Blockers, fmt.Sprintf("The statement does not tie: opening %s less principal paid gives %s, but the lender shows %s.", ledger.Show(prepaids.Money(opening), in.Currency), ledger.Show(prepaids.Money(outstanding), in.Currency), ledger.Show(prepaids.Money(closing), in.Currency)))
	}
	in.OpeningPrincipal, in.ClosingPrincipal, in.BookBalance = prepaids.Money(opening), prepaids.Money(closing), prepaids.Money(book)
	entry := func(delta int64, increase, decrease string) {
		if delta > 0 {
			result.Proposals = append(result.Proposals, prepaids.Adjustment{Date: monthEnd.Format("2006-01-02"), DebitAccount: increase, CreditAccount: decrease, Amount: prepaids.Money(delta)})
		} else if delta < 0 {
			result.Proposals = append(result.Proposals, prepaids.Adjustment{Date: monthEnd.Format("2006-01-02"), DebitAccount: decrease, CreditAccount: increase, Amount: prepaids.Money(-delta)})
		}
	}
	if in.AccrueInterest {
		in.AccruedInterestAccount = strings.TrimSpace(in.AccruedInterestAccount)
		if in.AccruedInterestAccount == "" || len(in.AccruedInterestAccount) > 200 || strings.EqualFold(in.AccruedInterestAccount, in.InterestAccount) || strings.EqualFold(in.AccruedInterestAccount, in.LoanAccount) {
			return fail("Name a separate accrued interest account to accrue interest.")
		}
		booked, _, err := amount(in.BookedAccruedInterest, "the booked accrued interest balance, including zero", true)
		if err != nil {
			return Workpaper{}, err
		}
		in.BookedAccruedInterest = prepaids.Money(booked)
		days := Days(in.DayCount, last, monthEnd)
		result.AccruedInterest = &Accrual{From: last.Format("2006-01-02"), To: monthEnd.Format("2006-01-02"), Days: days, Amount: prepaids.Money(accrued)}
		entry(accrued-booked, in.InterestAccount, in.AccruedInterestAccount)
	} else if in.AccruedInterestAccount != "" || in.BookedAccruedInterest != "" {
		return fail("Accrued interest fields require choosing to accrue interest.")
	}
	// The statement's fees go to a fee expense account; the ones not yet booked are proposed against the loan.
	var unbookedFees int64
	in.FeeAccount = strings.TrimSpace(in.FeeAccount)
	if len(charges) > 0 {
		if in.FeeAccount == "" || len(in.FeeAccount) > 200 || strings.EqualFold(in.FeeAccount, in.LoanAccount) || strings.EqualFold(in.FeeAccount, in.InterestAccount) || strings.EqualFold(in.FeeAccount, in.AccruedInterestAccount) || strings.EqualFold(in.FeeAccount, strings.TrimSpace(in.CurrentPortionAccount)) {
			return fail("Name a separate fee expense account for fees added to the loan balance.")
		}
		booked, _, err := amount(in.BookedFees, "the fees already booked to the loan this month, including zero", true)
		if err != nil {
			return Workpaper{}, err
		}
		in.BookedFees, result.Fees, unbookedFees = prepaids.Money(booked), prepaids.Money(totalFees), totalFees-booked
		entry(unbookedFees, in.FeeAccount, in.LoanAccount)
	} else if in.FeeAccount != "" || in.BookedFees != "" {
		return fail("A fee account and booked fees require at least one fee added to the loan balance.")
	}
	// A separate current portion account counts toward the loan balance every month; it is reclassified only when confirmed.
	var bookedCurrent int64
	in.CurrentPortionAccount = strings.TrimSpace(in.CurrentPortionAccount)
	if in.CurrentPortionAccount != "" || in.BookedCurrentPortion != "" || in.ReclassCurrentPortion {
		if in.CurrentPortionAccount == "" || len(in.CurrentPortionAccount) > 200 || strings.EqualFold(in.CurrentPortionAccount, in.LoanAccount) || strings.EqualFold(in.CurrentPortionAccount, in.InterestAccount) {
			return fail("Name a separate current portion account.")
		}
		if bookedCurrent, _, err = amount(in.BookedCurrentPortion, "the booked current portion balance, including zero", true); err != nil {
			return Workpaper{}, err
		}
		in.BookedCurrentPortion = prepaids.Money(bookedCurrent)
	}
	if in.ReclassCurrentPortion {
		in.ScheduleReference = strings.TrimSpace(in.ScheduleReference)
		due, _, err := amount(in.PrincipalDueNext12Months, "principal due in the next 12 months", true)
		if err != nil || in.ScheduleReference == "" || len(in.ScheduleReference) > 200 {
			return fail("Cite the lender's amortization schedule and the principal due in the next 12 months to reclassify.")
		}
		in.PrincipalDueNext12Months = prepaids.Money(due)
		if book+unbookedFees+bookedCurrent == closing {
			entry(min(due, closing)-bookedCurrent, in.LoanAccount, in.CurrentPortionAccount)
		}
	} else if in.PrincipalDueNext12Months != "" || in.ScheduleReference != "" {
		return fail("Principal due in 12 months and the schedule reference require confirming this is a reclassification month.")
	}
	if c := in.IssuanceCosts; c != nil {
		if in.Method != CostsMethod {
			return fail("Debt issuance costs need the firm's approved loan procedure that includes them.")
		}
		c.Account, c.Reference, c.MaterialityReference, c.BookingReference = strings.TrimSpace(c.Account), strings.TrimSpace(c.Reference), strings.TrimSpace(c.MaterialityReference), strings.TrimSpace(c.BookingReference)
		for _, other := range []string{in.LoanAccount, in.InterestAccount, in.AccruedInterestAccount, in.FeeAccount, in.CurrentPortionAccount} {
			if c.Account == "" || len(c.Account) > 200 || strings.EqualFold(c.Account, other) {
				return fail("Keep debt issuance costs in their own account, deducted from the debt; the loan account keeps the principal.")
			}
		}
		if c.Reference == "" || len(c.Reference) > 200 || c.MaterialityReference == "" || len(c.MaterialityReference) > 200 || !c.MethodConfirmed {
			return fail("Cite the issuance costs and the accountant's conclusion that straight-line amortization is not materially different from the interest method, and confirm the method.")
		}
		if !c.BookingConfirmed || c.BookedUnamortized == "" || c.BookingReference == "" || len(c.BookingReference) > 200 {
			return fail("Confirm the issuance costs were booked to their account, and give its month-end balance (including zero) and ledger reference.")
		}
		schedule, err := prepaids.Prepare(prepaids.Input{Entity: in.Entity, SourceReference: c.Reference, Amount: c.Amount, ServiceStart: c.Start, ServiceEnd: c.End, ClosePeriod: in.ClosePeriod, Currency: in.Currency, Basis: "accrual",
			ExpenseAccount: in.InterestAccount, PrepaidAccount: c.Account, Method: c.Method, MethodConfirmed: true, ExamplesConfirmed: true, BookingConfirmed: true, BookedBalance: c.BookedUnamortized, BookingReference: c.BookingReference})
		if err != nil {
			return fail("Debt issuance costs: %s", err.Error())
		}
		c.Amount, c.BookedUnamortized = schedule.Input.Amount, schedule.Input.BookedBalance
		result.IssuanceCosts = &Amortization{Month: schedule.PeriodExpense, ToDate: schedule.RecognizedExpense, Unamortized: schedule.PrepaidBalance}
		if schedule.Proposal != nil {
			result.Proposals = append(result.Proposals, *schedule.Proposal)
		}
	}
	if feesPaid > 0 {
		result.FeesPaid = prepaids.Money(feesPaid)
	}
	difference := book + unbookedFees + bookedCurrent - closing
	result.BookDifference = signed(difference)
	if difference != 0 {
		more := "more"
		if difference < 0 {
			more = "less"
		}
		question := fmt.Sprintf("The ledger shows %s %s loan principal than the lender's statement. Find the cause (a payment booked to another account, a missing payment or fees) before any adjustment; nothing is proposed for it.", ledger.Show(prepaids.Money(max(difference, -difference)), in.Currency), more)
		if difference == -feesPaid {
			question += fmt.Sprintf(" It equals the fees paid in cash with this month's payments (%s), so they may have been booked against principal; check the statement before moving them.", ledger.Show(prepaids.Money(feesPaid), in.Currency))
		}
		result.Questions = append(result.Questions, question)
	}
	if len(result.Blockers) > 0 {
		result.Proposals = []prepaids.Adjustment{}
	}
	result.Input = in
	return result, nil
}
