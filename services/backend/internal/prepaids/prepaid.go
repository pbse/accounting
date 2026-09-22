package prepaids

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const Method = "equal_full_months_v1"
const DailyMethod = "actual_days_v1"
const FullRefundCancellation = "full_refund_month_boundary_v1"
const NoRefundCancellation = "no_refund_month_boundary_v1"

type Input struct {
	ExamplesConfirmed     bool   `json:"examples_confirmed,omitempty"`
	CreditAmount          string `json:"credit_amount,omitempty"`
	CreditPeriod          string `json:"credit_period,omitempty"`
	CreditReference       string `json:"credit_reference,omitempty"`
	CancellationMethod    string `json:"cancellation_method,omitempty"`
	CancellationPeriod    string `json:"cancellation_period,omitempty"`
	CancellationReference string `json:"cancellation_reference,omitempty"`
	BookedBalance         string `json:"booked_balance,omitempty"`
	BookingReference      string `json:"booking_reference,omitempty"`
	Entity                string `json:"entity"`
	SourceReference       string `json:"source_reference"`
	Amount                string `json:"amount"`
	ServiceStart          string `json:"service_start"`
	ServiceEnd            string `json:"service_end"`
	ClosePeriod           string `json:"close_period"`
	Currency              string `json:"currency"`
	Basis                 string `json:"basis"`
	ExpenseAccount        string `json:"expense_account"`
	PrepaidAccount        string `json:"prepaid_account"`
	Method                string `json:"method"`
	MethodConfirmed       bool   `json:"method_confirmed"`
	BookingConfirmed      bool   `json:"booking_confirmed"`
}

type Row struct {
	Credit    string `json:"credit,omitempty"`
	Period    string `json:"period"`
	Expense   string `json:"expense"`
	Remaining string `json:"remaining"`
}

type Adjustment struct {
	Date          string `json:"date"`
	DebitAccount  string `json:"debit_account"`
	CreditAccount string `json:"credit_account"`
	Amount        string `json:"amount"`
}

type Workpaper struct {
	Status            string      `json:"status"`
	Input             Input       `json:"input"`
	Schedule          []Row       `json:"schedule"`
	PeriodExpense     string      `json:"period_expense"`
	RecognizedExpense string      `json:"recognized_expense"`
	PrepaidBalance    string      `json:"prepaid_balance"`
	Proposal          *Adjustment `json:"proposal"`
}

type InputError struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e *InputError) Error() string       { return e.Message }
func invalid(field, message string) error { return &InputError{"INPUT_UNSUPPORTED", field, message} }

var moneyPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,9})(\.[0-9]{1,2})?$`)

func Money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

func Prepare(input Input) (Workpaper, error) {
	empty := Workpaper{}
	for _, field := range []struct {
		name  string
		value *string
	}{
		{"entity", &input.Entity}, {"source_reference", &input.SourceReference},
		{"expense_account", &input.ExpenseAccount}, {"prepaid_account", &input.PrepaidAccount},
	} {
		*field.value = strings.TrimSpace(*field.value)
		if len(*field.value) == 0 || len(*field.value) > 200 {
			return empty, invalid(field.name, "Enter between 1 and 200 characters.")
		}
	}
	if strings.EqualFold(input.ExpenseAccount, input.PrepaidAccount) {
		return empty, invalid("prepaid_account", "Choose a prepaid account different from the expense account.")
	}
	if input.Currency != "USD" {
		return empty, invalid("currency", "This method supports USD only.")
	}
	if input.Basis != "accrual" {
		return empty, invalid("basis", "This method requires accrual-basis accounting.")
	}
	if (input.Method != Method && input.Method != DailyMethod) || !input.MethodConfirmed {
		return empty, &InputError{"POLICY_REQUIRED", "method_confirmed", "Select and confirm equal full months or inclusive actual days, with cents rounded down and the residual in the final month."}
	}
	if !input.BookingConfirmed {
		return empty, &InputError{"POLICY_REQUIRED", "booking_confirmed", "Confirm the invoice and any credit are booked, and the supplied prepaid balance reflects all prior entries through the close. Without a balance, confirm the invoice was fully expensed with no credits or prior adjustments."}
	}
	total, err := Amount(input.Amount)
	if err != nil {
		return empty, invalid("amount", err.Error())
	}
	if total == 0 {
		return empty, invalid("amount", "The invoice amount must be greater than zero.")
	}
	input.Amount = Money(total)
	start, err := time.Parse("2006-01-02", input.ServiceStart)
	if err != nil {
		return empty, invalid("service_start", "Enter a valid service start date.")
	}
	end, err := time.Parse("2006-01-02", input.ServiceEnd)
	if err != nil {
		return empty, invalid("service_end", "Enter a valid service end date.")
	}
	if input.Method == Method && start.Day() != 1 {
		return empty, invalid("service_start", "Partial months need a separately approved method. Start on the first day of a month.")
	}
	if end.Before(start) || (input.Method == Method && end.AddDate(0, 0, 1).Day() != 1) {
		return empty, invalid("service_end", "End on or after the start; equal full months must end on a month-end.")
	}
	months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
	if months > 120 || start.Year() < 1000 || end.Year() > 9998 {
		return empty, invalid("service_end", "This method supports up to 120 months.")
	}
	period, err := time.Parse("2006-01", input.ClosePeriod)
	if err != nil || period.Before(time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)) || period.After(end) {
		return empty, invalid("close_period", "Choose a close month within the service period.")
	}
	var credit, booked int64
	var effective time.Time
	extended := input.Method == DailyMethod || input.CreditAmount != "" || input.BookedBalance != "" || input.CancellationMethod != ""
	if extended && !input.ExamplesConfirmed {
		return empty, &InputError{"POLICY_REQUIRED", "examples_confirmed", "Review the synthetic examples and confirm the chosen method before preparing."}
	}
	if input.BookedBalance != "" {
		booked, err = Amount(input.BookedBalance)
		if err != nil {
			return empty, invalid("booked_balance", err.Error())
		}
		input.BookingReference = strings.TrimSpace(input.BookingReference)
		if input.BookingReference == "" || len(input.BookingReference) > 200 {
			return empty, invalid("booking_reference", "Cite the ledger balance and cutoff supporting all prior entries.")
		}
		input.BookedBalance = Money(booked)
	} else if input.BookingReference != "" {
		return empty, invalid("booked_balance", "Supply the referenced booked prepaid balance, including an explicit zero.")
	}
	if input.CreditAmount != "" {
		credit, err = Amount(input.CreditAmount)
		if err != nil || credit == 0 {
			return empty, invalid("credit_amount", "Enter a positive credit; omit all credit fields if there is none.")
		}
		effective, err = time.Parse("2006-01", input.CreditPeriod)
		first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
		if err != nil || effective.Before(first) || effective.After(period) {
			return empty, invalid("credit_period", "Credit effective month must be within service and no later than this close.")
		}
		input.CreditReference = strings.TrimSpace(input.CreditReference)
		if input.BookedBalance == "" || input.CreditReference == "" || len(input.CreditReference) > 200 {
			return empty, invalid("credit_reference", "Cite the credit and supply the booked prepaid balance after its ledger entry.")
		}
		input.CreditAmount = Money(credit)
	} else if input.CreditPeriod != "" || input.CreditReference != "" {
		return empty, invalid("credit_amount", "Supply the documented credit amount or clear all credit fields.")
	}
	var cancellation time.Time
	if input.CancellationMethod != "" {
		if input.CancellationMethod != FullRefundCancellation && input.CancellationMethod != NoRefundCancellation {
			return empty, invalid("cancellation_method", "Choose full refund or no refund at a service-month boundary.")
		}
		if input.CreditAmount != "" {
			return empty, invalid("cancellation_method", "Combined credits and cancellation require separate accountant review.")
		}
		cancellation, err = time.Parse("2006-01", input.CancellationPeriod)
		if err != nil || !cancellation.Equal(period) {
			return empty, invalid("cancellation_period", "Prepare cancellation in its effective close month; it starts on that service month's first day (or service start). Prior-period correction needs separate review.")
		}
		input.CancellationReference = strings.TrimSpace(input.CancellationReference)
		if input.CancellationReference == "" || len(input.CancellationReference) > 200 || input.BookedBalance == "" {
			return empty, invalid("cancellation_reference", "Cite the cancellation and provide the current booked prepaid balance: after a full refund, or before any proposed no-refund write-off (explicit zero is allowed).")
		}
	} else if input.CancellationPeriod != "" || input.CancellationReference != "" {
		return empty, invalid("cancellation_method", "Choose a cancellation treatment or clear its period and reference.")
	}
	result := Workpaper{Status: "draft", Input: input, Schedule: make([]Row, 0, months)}
	remaining, recognized, base := total, int64(0), total
	first := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	denominator := int64(months)
	if input.Method == DailyMethod {
		denominator = int64(end.Sub(start).Hours()/24) + 1
	}
	for i := 0; i < months; i++ {
		month := first.AddDate(0, i, 0)
		from, to := month, month.AddDate(0, 1, -1)
		if from.Before(start) {
			from = start
		}
		if to.After(end) {
			to = end
		}
		rowCredit := ""
		if !cancellation.IsZero() && month.Equal(cancellation) {
			if booked > remaining {
				return empty, invalid("booked_balance", "Booked prepaid exceeds the unamortized amount at cancellation. Resolve the ledger difference separately.")
			}
			if input.CancellationMethod == FullRefundCancellation {
				rowCredit = Money(remaining)
				if booked != 0 {
					return empty, invalid("booked_balance", "Record the full refund against prepaid and confirm a zero booked balance; a refund journal is outside this calculator.")
				}
			} else {
				if booked != 0 && booked != remaining {
					return empty, invalid("booked_balance", "No-refund cancellation supports the full remaining prepaid balance or an already-booked zero balance. Investigate partial prior write-offs separately.")
				}
				result.Schedule = append(result.Schedule, Row{Period: month.Format("2006-01"), Expense: Money(remaining), Remaining: "0.00"})
				recognized += remaining
				result.PeriodExpense, result.RecognizedExpense, result.PrepaidBalance = Money(remaining), Money(recognized), "0.00"
				if booked > 0 {
					result.Proposal = &Adjustment{period.AddDate(0, 1, -1).Format("2006-01-02"), input.ExpenseAccount, input.PrepaidAccount, Money(booked)}
				}
				remaining = 0
				continue
			}
			remaining = 0
			base = 0
		}
		if credit > 0 && month.Equal(effective) {
			if credit > remaining {
				return empty, invalid("credit_amount", "Credit exceeds the unamortized balance at its effective month. Retrospective corrections require separate review.")
			}
			remaining -= credit
			base = remaining
			denominator = int64(months - i)
			if input.Method == DailyMethod {
				denominator = int64(end.Sub(from).Hours()/24) + 1
			}
			rowCredit = Money(credit)
		}
		weight := int64(1)
		if input.Method == DailyMethod {
			weight = int64(to.Sub(from).Hours()/24) + 1
		}
		amount := base * weight / denominator
		if i == months-1 {
			amount = remaining
		}
		if !cancellation.IsZero() && !month.Before(cancellation) {
			amount = 0
		}
		remaining -= amount
		recognized += amount
		result.Schedule = append(result.Schedule, Row{Period: month.Format("2006-01"), Expense: Money(amount), Remaining: Money(remaining), Credit: rowCredit})
		if month.Equal(period) {
			result.PeriodExpense, result.RecognizedExpense, result.PrepaidBalance = Money(amount), Money(recognized), Money(remaining)
			delta := remaining - booked
			if delta != 0 {
				debit, creditAccount := input.PrepaidAccount, input.ExpenseAccount
				if delta < 0 {
					delta = -delta
					debit, creditAccount = creditAccount, debit
				}
				result.Proposal = &Adjustment{period.AddDate(0, 1, -1).Format("2006-01-02"), debit, creditAccount, Money(delta)}
			}
		}
	}
	return result, nil
}

// ScheduleOnly calculates a carried schedule without asserting a new month's
// ledger balance or proposing an entry. A historical credit still affects the
// schedule; its prior booked-balance confirmation is deliberately not reused.
func ScheduleOnly(input Input) (Workpaper, error) {
	input.BookedBalance, input.BookingReference = "", ""
	if input.CreditAmount != "" {
		input.BookedBalance, input.BookingReference = "0.00", "Schedule-only historical credit"
	}
	input.MethodConfirmed, input.BookingConfirmed, input.ExamplesConfirmed = true, true, true
	paper, err := Prepare(input)
	if err != nil {
		return Workpaper{}, err
	}
	paper.Input.BookedBalance, paper.Input.BookingReference = "", ""
	paper.Input.MethodConfirmed, paper.Input.BookingConfirmed, paper.Input.ExamplesConfirmed = false, false, false
	paper.Proposal = nil
	return paper, nil
}

// Amount accepts bounded nonnegative exact cents, shared by supported schedules.
func Amount(value string) (int64, error) {
	if !moneyPattern.MatchString(value) {
		return 0, fmt.Errorf("Enter a nonnegative USD amount with at most two decimals, up to 9999999999.99.")
	}
	dollars, fraction, _ := strings.Cut(value, ".")
	whole, _ := strconv.ParseInt(dollars, 10, 64)
	cents, _ := strconv.ParseInt((fraction + "00")[:2], 10, 64)
	return whole*100 + cents, nil
}
