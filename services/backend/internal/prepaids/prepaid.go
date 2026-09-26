package prepaids

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
)

const Method = "equal_full_months_v1"
const DailyMethod = "actual_days_v1"
const FullRefundCancellation = "full_refund_month_boundary_v1"
const NoRefundCancellation = "no_refund_month_boundary_v1"

// Deferred revenue and depreciation reuse the straight-line schedule; only their accounts and entry direction differ.
const DeferredRevenue = "deferred_revenue"
const Depreciation = "depreciation"

// DecliningMethod depreciates a fixed share of the net book value each month (factor ÷ useful life in months), switching
// to straight-line over the remaining months once that is larger, never below salvage value.
const DecliningMethod = "declining_balance_v1"

// MidMonth treats an asset as placed in service mid-month: half a month's depreciation in the first month and the other
// half in the month after the useful life ends.
const MidMonth = "mid_month"

type Credit struct {
	Amount          string `json:"amount"`
	EffectivePeriod string `json:"effective_period"`
	RecordedPeriod  string `json:"recorded_period"`
	Reference       string `json:"reference"`
}

type Input struct {
	Schedule                string   `json:"schedule,omitempty"`
	SalvageValue            string   `json:"salvage_value,omitempty"`
	Factor                  string   `json:"factor,omitempty"`
	Convention              string   `json:"convention,omitempty"`
	DisposalPeriod          string   `json:"disposal_period,omitempty"`
	DisposalProceeds        string   `json:"disposal_proceeds,omitempty"`
	DisposalReference       string   `json:"disposal_reference,omitempty"`
	AssetAccount            string   `json:"asset_account,omitempty"`
	ProceedsAccount         string   `json:"proceeds_account,omitempty"`
	GainLossAccount         string   `json:"gain_loss_account,omitempty"`
	DisposalConfirmed       bool     `json:"disposal_confirmed,omitempty"`
	ExamplesConfirmed       bool     `json:"examples_confirmed,omitempty"`
	CreditAmount            string   `json:"credit_amount,omitempty"`
	CreditPeriod            string   `json:"credit_period,omitempty"`
	CreditReference         string   `json:"credit_reference,omitempty"`
	Credits                 []Credit `json:"credits,omitempty"`
	AccountantRuleReference string   `json:"accountant_rule_reference,omitempty"`
	CancellationMethod      string   `json:"cancellation_method,omitempty"`
	CancellationPeriod      string   `json:"cancellation_period,omitempty"`
	CancellationReference   string   `json:"cancellation_reference,omitempty"`
	BookedBalance           string   `json:"booked_balance,omitempty"`
	BookingReference        string   `json:"booking_reference,omitempty"`
	Entity                  string   `json:"entity"`
	SourceReference         string   `json:"source_reference"`
	Amount                  string   `json:"amount"`
	ServiceStart            string   `json:"service_start"`
	ServiceEnd              string   `json:"service_end"`
	ClosePeriod             string   `json:"close_period"`
	Currency                string   `json:"currency"`
	Basis                   string   `json:"basis"`
	ExpenseAccount          string   `json:"expense_account"`
	PrepaidAccount          string   `json:"prepaid_account"`
	Method                  string   `json:"method"`
	MethodConfirmed         bool     `json:"method_confirmed"`
	BookingConfirmed        bool     `json:"booking_confirmed"`
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
	// Disposal removes a disposed asset's cost and accumulated depreciation and records proceeds and any gain or loss.
	// It is booked by hand.
	Disposal []Adjustment `json:"disposal,omitempty"`
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
	if !ledger.ValidCurrency(input.Currency) {
		return empty, invalid("currency", "Use the client's home currency as a three-letter code with two decimals, such as USD, CAD or GBP.")
	}
	if input.Basis != "accrual" {
		return empty, invalid("basis", "This method requires accrual-basis accounting.")
	}
	if (input.Method != Method && input.Method != DailyMethod && (input.Method != DecliningMethod || input.Schedule != Depreciation)) || !input.MethodConfirmed {
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
	monthly := input.Method == Method || input.Method == DecliningMethod
	if monthly && start.Day() != 1 {
		return empty, invalid("service_start", "Partial months need a separately approved method. Start on the first day of a month.")
	}
	if end.Before(start) || (monthly && end.AddDate(0, 0, 1).Day() != 1) {
		return empty, invalid("service_end", "End on or after the start; equal full months must end on a month-end.")
	}
	months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
	if months > 120 || start.Year() < 1000 || end.Year() > 9998 {
		return empty, invalid("service_end", "This method supports up to 120 months.")
	}
	// Declining balance and the mid-month convention are depreciation methods under the accountant's cited policy.
	var factorTenths int64
	life := int64(months)
	if input.Method == DecliningMethod {
		factorTenths = map[string]int64{"2": 20, "1.5": 15}[input.Factor]
		if factorTenths == 0 {
			return empty, invalid("factor", "Choose a declining-balance factor of 2 (double) or 1.5.")
		}
	} else if input.Factor != "" {
		return empty, invalid("factor", "A factor applies to declining-balance depreciation only.")
	}
	if input.Convention != "" && (input.Convention != MidMonth || input.Schedule != Depreciation || !monthly) {
		return empty, invalid("convention", "The mid-month convention applies to depreciation in equal months or declining balance.")
	}
	input.AccountantRuleReference = strings.TrimSpace(input.AccountantRuleReference)
	if (input.Method == DecliningMethod || input.Convention == MidMonth || input.DisposalPeriod != "") && (len(input.AccountantRuleReference) < 5 || len(input.AccountantRuleReference) > 200) {
		return empty, &InputError{"POLICY_REQUIRED", "accountant_rule_reference", "Cite the client accountant's approved depreciation policy for this method, convention or disposal."}
	}
	if input.Convention == MidMonth {
		months++
		end = time.Date(end.Year(), end.Month()+2, 0, 0, 0, 0, 0, time.UTC)
	}
	period, err := time.Parse("2006-01", input.ClosePeriod)
	if err != nil || period.Before(time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)) || period.After(end) {
		return empty, invalid("close_period", "Choose a close month within the service period.")
	}
	var credit, booked, salvage int64
	var effective time.Time
	extended := input.Method == DailyMethod || input.CreditAmount != "" || len(input.Credits) > 0 || input.BookedBalance != "" || input.CancellationMethod != ""
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
	if input.Schedule != "" && input.Schedule != DeferredRevenue && input.Schedule != Depreciation {
		return empty, invalid("schedule", "Choose a prepaid, deferred revenue or depreciation schedule.")
	}
	if input.Schedule != Depreciation && input.SalvageValue != "" {
		return empty, invalid("salvage_value", "Salvage value applies to depreciation only.")
	}
	if input.Schedule == Depreciation {
		if input.CreditAmount != "" || len(input.Credits) > 0 || input.CancellationMethod != "" {
			return empty, invalid("credits", "Disposals, impairments and credits on a fixed asset need a separate procedure.")
		}
		if input.BookedBalance == "" {
			return empty, invalid("booked_balance", "Enter the booked accumulated depreciation and its ledger reference, including an explicit 0.00.")
		}
		salvage = 0
		if input.SalvageValue != "" {
			if salvage, err = Amount(input.SalvageValue); err != nil || salvage >= total {
				return empty, invalid("salvage_value", "Salvage value must be less than the asset's cost.")
			}
			input.SalvageValue = Money(salvage)
		}
		// The engine compares scheduled and booked amounts left to depreciate.
		total -= salvage
		if booked > total {
			return empty, invalid("booked_balance", "Booked accumulated depreciation exceeds cost less salvage. Resolve the ledger difference separately.")
		}
		booked = total - booked
	}
	// A disposal ends the schedule in its close month: no depreciation that month under the full-month convention, half
	// a month under mid-month.
	var disposal time.Time
	var proceeds int64
	if input.DisposalPeriod != "" || input.DisposalProceeds != "" || input.DisposalReference != "" || input.AssetAccount != "" || input.ProceedsAccount != "" || input.GainLossAccount != "" {
		if input.Schedule != Depreciation || !monthly {
			return empty, invalid("disposal_period", "Disposals apply to depreciation in equal months or declining balance.")
		}
		if disposal, err = time.Parse("2006-01", input.DisposalPeriod); err != nil || !disposal.Equal(period) {
			return empty, invalid("disposal_period", "Prepare a disposal in its close month.")
		}
		if proceeds, err = Amount(input.DisposalProceeds); err != nil {
			return empty, invalid("disposal_proceeds", "Enter the disposal proceeds, including an explicit 0.00 for a scrapped asset.")
		}
		input.DisposalProceeds = Money(proceeds)
		for _, field := range []struct {
			name  string
			value *string
		}{{"disposal_reference", &input.DisposalReference}, {"asset_account", &input.AssetAccount}, {"proceeds_account", &input.ProceedsAccount}, {"gain_loss_account", &input.GainLossAccount}} {
			if *field.value = strings.TrimSpace(*field.value); *field.value == "" || len(*field.value) > 200 {
				return empty, invalid(field.name, "Cite the disposal evidence and name the asset cost, proceeds and gain or loss accounts.")
			}
		}
		accounts := []string{strings.ToLower(input.PrepaidAccount), strings.ToLower(input.AssetAccount), strings.ToLower(input.ProceedsAccount), strings.ToLower(input.GainLossAccount)}
		slices.Sort(accounts)
		if len(slices.Compact(accounts)) != 4 || strings.EqualFold(input.AssetAccount, input.ExpenseAccount) {
			return empty, invalid("asset_account", "Use different accumulated depreciation, asset cost, proceeds and gain or loss accounts, and an asset cost account other than the expense account.")
		}
		if !input.DisposalConfirmed {
			return empty, invalid("disposal_confirmed", "Confirm the asset's cost is still booked, the booked accumulated depreciation is before the disposal, and nothing for this disposal has been recorded.")
		}
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
	if len(input.Credits) > 20 || (len(input.Credits) > 0 && (input.CreditAmount != "" || input.CancellationMethod != "" || input.BookedBalance == "")) {
		return empty, invalid("credits", "Use up to 20 separately documented credits with a confirmed booked balance; do not combine with a legacy credit or cancellation.")
	}
	if len(input.Credits) > 0 && (input.AccountantRuleReference == "" || len(input.AccountantRuleReference) > 200) {
		return empty, &InputError{"POLICY_REQUIRED", "accountant_rule_reference", "Cite the client accountant's approved rule for multiple or retrospective credits."}
	}
	creditsByMonth := map[string]int64{}
	for i := range input.Credits {
		c := &input.Credits[i]
		amount, amountErr := Amount(c.Amount)
		effectiveMonth, effectiveErr := time.Parse("2006-01", c.EffectivePeriod)
		recordedMonth, recordedErr := time.Parse("2006-01", c.RecordedPeriod)
		c.Reference = strings.TrimSpace(c.Reference)
		if amountErr != nil || amount == 0 || effectiveErr != nil || recordedErr != nil || effectiveMonth.Before(time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)) || effectiveMonth.After(period) || recordedMonth.Before(effectiveMonth) || recordedMonth.After(period) || c.Reference == "" || len(c.Reference) > 200 {
			return empty, invalid("credits", "Each credit needs a positive amount, service-month effective date, booked month no earlier than effective and no later than this close, and a source reference.")
		}
		c.Amount = Money(amount)
		creditsByMonth[c.EffectivePeriod] += amount
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
	rows := months
	if !disposal.IsZero() {
		rows = (disposal.Year()-first.Year())*12 + int(disposal.Month()-first.Month()) + 1
	}
	for i := 0; i < rows; i++ {
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
		if extra := creditsByMonth[month.Format("2006-01")]; extra > 0 {
			if extra > remaining {
				return empty, invalid("credits", "Credits exceed the unamortized balance at their effective month. Resolve the excess separately.")
			}
			remaining -= extra
			base = remaining
			denominator = int64(months - i)
			if input.Method == DailyMethod {
				denominator = int64(end.Sub(from).Hours()/24) + 1
			}
			rowCredit = Money(extra)
		}
		weight := int64(1)
		if input.Method == DailyMethod {
			weight = int64(to.Sub(from).Hours()/24) + 1
		}
		amount := base * weight / denominator
		if input.Convention == MidMonth || input.Method == DecliningMethod {
			// Half months weigh 1 and full months 2; the life's total weight is twice its months.
			weight = 2
			if input.Convention == MidMonth && (i == 0 || month.Equal(disposal)) {
				weight = 1
			}
			left := 2*int64(months-i) - 1
			if input.Convention != MidMonth {
				left = 2 * int64(months-i)
			} else if i == 0 {
				left = 2*int64(months) - 2
			}
			amount = remaining * weight / left
			if input.Method == DecliningMethod {
				// Net book value is what is left to depreciate plus salvage.
				declining := (remaining + salvage) * factorTenths * weight / (10 * life * 2)
				amount = min(max(declining, amount), remaining)
			}
		}
		if i == months-1 {
			amount = remaining
		}
		if month.Equal(disposal) && input.Convention != MidMonth {
			amount = 0
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
	if !disposal.IsZero() {
		// Book value is cost less the schedule's depreciation; the asset's cost is credited in full.
		accumulated, book := total-remaining, remaining+salvage
		date := period.AddDate(0, 1, -1).Format("2006-01-02")
		for _, line := range []Adjustment{
			{date, input.PrepaidAccount, input.AssetAccount, Money(accumulated)},
			{date, input.ProceedsAccount, input.AssetAccount, Money(min(proceeds, book))},
			{date, input.ProceedsAccount, input.GainLossAccount, Money(max(proceeds-book, 0))},
			{date, input.GainLossAccount, input.AssetAccount, Money(max(book-proceeds, 0))},
		} {
			if line.Amount != "0.00" {
				result.Disposal = append(result.Disposal, line)
			}
		}
	}
	// Deferred revenue is a liability: recognizing it debits the deferred account and credits revenue.
	if input.Schedule == DeferredRevenue && result.Proposal != nil {
		result.Proposal.DebitAccount, result.Proposal.CreditAccount = result.Proposal.CreditAccount, result.Proposal.DebitAccount
	}
	return result, nil
}

// ScheduleOnly calculates a carried schedule without asserting a new month's
// ledger balance or proposing an entry. A historical credit still affects the
// schedule; its prior booked-balance confirmation is deliberately not reused.
func ScheduleOnly(input Input) (Workpaper, error) {
	input.BookedBalance, input.BookingReference = "", ""
	if input.CreditAmount != "" || len(input.Credits) > 0 || input.Schedule == Depreciation {
		input.BookedBalance, input.BookingReference = "0.00", "Schedule only"
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
		return 0, fmt.Errorf("Enter a nonnegative amount with at most two decimals, up to 9999999999.99.")
	}
	dollars, fraction, _ := strings.Cut(value, ".")
	whole, _ := strconv.ParseInt(dollars, 10, 64)
	cents, _ := strconv.ParseInt((fraction + "00")[:2], 10, 64)
	return whole*100 + cents, nil
}
