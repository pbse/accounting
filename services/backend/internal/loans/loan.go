// Package loans checks a month of loan activity against the lender's statement.
// The statement's principal/interest split is authoritative; recomputed interest only raises questions.
package loans

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/prepaids"
)

const Method = "lender_statement_v1"

var DayCounts = map[string]string{
	"actual_360":  "Actual/360",
	"actual_365":  "Actual/365 (fixed)",
	"30_360_bond": "30/360 bond basis (US, no February rule)",
	"30e_360":     "30E/360 (Eurobond)",
}

type Payment struct {
	Date      string `json:"date"`
	Principal string `json:"principal"`
	Interest  string `json:"interest"`
}

type Input struct {
	Entity                   string    `json:"entity"`
	ClosePeriod              string    `json:"close_period"`
	Lender                   string    `json:"lender"`
	SourceReference          string    `json:"source_reference"`
	AgreementReference       string    `json:"agreement_reference"`
	Currency                 string    `json:"currency"`
	AnnualRate               string    `json:"annual_rate"`
	DayCount                 string    `json:"day_count"`
	OpeningPrincipal         string    `json:"opening_principal"`
	InterestPaidThrough      string    `json:"interest_paid_through"`
	Payments                 []Payment `json:"payments"`
	ClosingPrincipal         string    `json:"closing_principal"`
	BookBalance              string    `json:"book_balance"`
	LoanAccount              string    `json:"loan_account"`
	InterestAccount          string    `json:"interest_account"`
	AccrueInterest           bool      `json:"accrue_interest"`
	AccruedInterestAccount   string    `json:"accrued_interest_account,omitempty"`
	BookedAccruedInterest    string    `json:"booked_accrued_interest,omitempty"`
	ReclassCurrentPortion    bool      `json:"reclass_current_portion"`
	PrincipalDueNext12Months string    `json:"principal_due_next_12_months,omitempty"`
	ScheduleReference        string    `json:"schedule_reference,omitempty"`
	CurrentPortionAccount    string    `json:"current_portion_account,omitempty"`
	BookedCurrentPortion     string    `json:"booked_current_portion,omitempty"`
	Method                   string    `json:"method"`
	MethodConfirmed          bool      `json:"method_confirmed"`
	RateConfirmed            bool      `json:"rate_confirmed"`
	BookingConfirmed         bool      `json:"booking_confirmed"`
}

type Row struct {
	Date             string `json:"date"`
	PrincipalBefore  string `json:"principal_before"`
	Days             int64  `json:"days"`
	Principal        string `json:"principal"`
	Interest         string `json:"interest"`
	ExpectedInterest string `json:"expected_interest"`
	Difference       string `json:"difference"`
}

type Accrual struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Days   int64  `json:"days"`
	Amount string `json:"amount"`
}

type Workpaper struct {
	Status          string                `json:"status"`
	Method          string                `json:"method"`
	Input           Input                 `json:"input"`
	Schedule        []Row                 `json:"schedule"`
	AccruedInterest *Accrual              `json:"accrued_interest,omitempty"`
	BookDifference  string                `json:"book_difference"`
	Proposals       []prepaids.Adjustment `json:"proposals"`
	Questions       []string              `json:"questions"`
	Blockers        []string              `json:"blockers"`
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
	basis := int64(360)
	if convention == "actual_365" {
		basis = 365
	}
	num := new(big.Int).Mul(big.NewInt(principal), big.NewInt(rateMilli))
	num.Mul(num, big.NewInt(days))
	den := big.NewInt(100 * 1000 * basis)
	num.Mul(num, big.NewInt(2)).Add(num, den)
	return num.Quo(num, den.Mul(den, big.NewInt(2))).Int64()
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
	if in.Method != Method || in.Currency != "USD" || !in.MethodConfirmed || !in.RateConfirmed || !in.BookingConfirmed {
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
	whole, fraction, _ := strings.Cut(in.AnnualRate, ".")
	rateMilli, _ := strconv.ParseInt(whole+(fraction + "000")[:3], 10, 64)
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
	result := Workpaper{Status: "draft", Method: Method, Schedule: []Row{}, Proposals: []prepaids.Adjustment{}, Questions: []string{}, Blockers: []string{}}
	if paidThrough.After(monthEnd) || paidThrough.Before(month.AddDate(0, 0, -62)) {
		result.Blockers = append(result.Blockers, "Interest paid-through date must fall between two months before this month and month-end, so opening principal is known for the whole window.")
	}
	outstanding, last := opening, paidThrough
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
		if principal > outstanding {
			return fail("Payment %d principal exceeds the principal outstanding.", i+1)
		}
		days := Days(in.DayCount, last, date)
		expected := Interest(outstanding, rateMilli, days, in.DayCount)
		p.Principal, p.Interest = prepaids.Money(principal), prepaids.Money(interest)
		result.Schedule = append(result.Schedule, Row{Date: p.Date, PrincipalBefore: prepaids.Money(outstanding), Days: days, Principal: p.Principal, Interest: p.Interest, ExpectedInterest: prepaids.Money(expected), Difference: signed(interest - expected)})
		if interest != expected {
			result.Questions = append(result.Questions, fmt.Sprintf("The lender charged $%s interest on %s; the note's rate gives $%s for %d days. Check for a rate change, fees or a different day-count before relying on the split.", prepaids.Money(interest), p.Date, prepaids.Money(expected), days))
		}
		outstanding -= principal
		last = date
	}
	if outstanding != closing {
		result.Blockers = append(result.Blockers, fmt.Sprintf("The statement does not tie: opening $%s less principal paid gives $%s, but the lender shows $%s.", prepaids.Money(opening), prepaids.Money(outstanding), prepaids.Money(closing)))
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
		accrued := Interest(closing, rateMilli, days, in.DayCount)
		result.AccruedInterest = &Accrual{From: last.Format("2006-01-02"), To: monthEnd.Format("2006-01-02"), Days: days, Amount: prepaids.Money(accrued)}
		entry(accrued-booked, in.InterestAccount, in.AccruedInterestAccount)
	} else if in.AccruedInterestAccount != "" || in.BookedAccruedInterest != "" {
		return fail("Accrued interest fields require choosing to accrue interest.")
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
		if book+bookedCurrent == closing {
			entry(min(due, closing)-bookedCurrent, in.LoanAccount, in.CurrentPortionAccount)
		}
	} else if in.PrincipalDueNext12Months != "" || in.ScheduleReference != "" {
		return fail("Principal due in 12 months and the schedule reference require confirming this is a reclassification month.")
	}
	difference := book + bookedCurrent - closing
	result.BookDifference = signed(difference)
	if difference != 0 {
		more := "more"
		if difference < 0 {
			more = "less"
		}
		result.Questions = append(result.Questions, fmt.Sprintf("The ledger shows $%s %s loan principal than the lender's statement. Find the cause (a payment booked to another account, a missing payment or fees) before any adjustment; nothing is proposed for it.", prepaids.Money(max(difference, -difference)), more))
	}
	if len(result.Blockers) > 0 {
		result.Proposals = []prepaids.Adjustment{}
	}
	result.Input = in
	return result, nil
}
