package reconciliation

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/prepaids"
)

const SettlementMethod = "stripe_automatic_payouts_v1"

// PayoutsMethod traces every payout in Stripe's itemized Payouts report to its deposit: the way to trace manual and
// instant payouts, whose transactions Stripe cannot identify. Only the payout itself is traced, whatever the schedule.
const PayoutsMethod = "stripe_payouts_v1"

type SettlementInput struct {
	Method            string            `json:"method,omitempty"`
	Entity            string            `json:"entity"`
	ClosePeriod       string            `json:"close_period"`
	Currency          string            `json:"currency"`
	BankAccount       string            `json:"bank_account"`
	ReportReference   string            `json:"report_reference"`
	BankReference     string            `json:"bank_reference"`
	ReportCSV         string            `json:"report_csv"`
	BankCSV           string            `json:"bank_csv"`
	ActivityReference string            `json:"activity_reference,omitempty"`
	ActivityCSV       string            `json:"activity_csv,omitempty"`
	MethodConfirmed   bool              `json:"method_confirmed"`
	CoverageConfirmed bool              `json:"coverage_confirmed"`
	ReviewedDeposits  []ReviewedDeposit `json:"reviewed_deposits,omitempty"`
	InTransit         []InTransit       `json:"in_transit,omitempty"`
	FeeEntry          *FeeEntry         `json:"fee_entry,omitempty"`
	RefundEntry       *RefundEntry      `json:"refund_entry,omitempty"`
	RevenueEntry      *RevenueEntry     `json:"revenue_entry,omitempty"`
}

// RefundEntry moves the refunds netted into deposits booked this month from the fee entry's deposit account to a
// refunds (contra-revenue) account, under the client accountant's rule.
type RefundEntry struct {
	RefundAccount string `json:"refund_account"`
	BookedRefunds string `json:"booked_refunds"`
	RuleReference string `json:"rule_reference"`
}

// RevenueEntry records the gross charges of deposits booked this month to a revenue account, when the accountant's rule
// says the fee entry's deposit account is a clearing account.
type RevenueEntry struct {
	RevenueAccount    string `json:"revenue_account"`
	BookedRevenue     string `json:"booked_revenue"`
	RuleReference     string `json:"rule_reference"`
	ClearingConfirmed bool   `json:"clearing_confirmed"`
}

// FeeEntry applies a client accountant's rule for deposits booked net of Stripe fees: the fees of
// payouts deposited this month are moved from the deposit account to a fee expense account.
type FeeEntry struct {
	FeeAccount     string `json:"fee_account"`
	DepositAccount string `json:"deposit_account"`
	BookedFees     string `json:"booked_fees"`
	RuleReference  string `json:"rule_reference"`
}
type ReviewedDeposit struct {
	PayoutID string `json:"payout_id"`
	BankID   string `json:"bank_id"`
	Reason   string `json:"reason"`
}
type InTransit struct {
	PayoutID        string `json:"payout_id"`
	PolicyReference string `json:"policy_reference"`
	Reason          string `json:"reason"`
}
type CategoryTotal struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
	Gross    string `json:"gross"`
	Fee      string `json:"fee"`
	Net      string `json:"net"`
}
type Payout struct {
	ID           string          `json:"id"`
	ExpectedDate string          `json:"expected_date"`
	Count        int             `json:"count"`
	Gross        string          `json:"gross"`
	Fee          string          `json:"fee"`
	Net          string          `json:"net"`
	Categories   []CategoryTotal `json:"categories"`
	Status       string          `json:"status"`
	Deposit      *Line           `json:"deposit,omitempty"`
	CandidateIDs []string        `json:"candidate_ids"`
	Reason       string          `json:"reason,omitempty"`
	Policy       string          `json:"policy_reference,omitempty"`
}
type SettlementWorkpaper struct {
	Status     string               `json:"status"`
	Method     string               `json:"method"`
	Input      SettlementInput      `json:"input"`
	Payouts    []Payout             `json:"payouts"`
	Categories []CategoryTotal      `json:"categories"`
	Total      CategoryTotal        `json:"total"`
	Unassigned []string             `json:"unassigned"`
	Questions  []string             `json:"questions"`
	Blockers   []string             `json:"blockers"`
	Fees       string               `json:"fees,omitempty"`
	Proposal   *prepaids.Adjustment `json:"proposal,omitempty"`
	Refunds    string               `json:"refunds,omitempty"`
	Revenue    string               `json:"revenue,omitempty"`
	// Refund and revenue entries are drafts to book by hand; only the fee entry can post.
	RefundProposal  *prepaids.Adjustment `json:"refund_proposal,omitempty"`
	RevenueProposal *prepaids.Adjustment `json:"revenue_proposal,omitempty"`
	RollForward     *RollForward         `json:"roll_forward,omitempty"`
}

// RollForward is the Stripe balance's computed change for the month on the Payouts report method: the month's
// activity, less payouts that left the balance, plus payout funds returned. It is not tied to Stripe's balances.
type RollForward struct {
	Activity string `json:"activity"`
	PaidOut  string `json:"paid_out"`
	Returned string `json:"returned"`
	Change   string `json:"change"`
}

var stripeColumns = []string{"balance_transaction_id", "currency", "gross", "fee", "net", "reporting_category", "automatic_payout_id", "automatic_payout_effective_at"}

var activityColumns = []string{"balance_transaction_id", "currency", "gross", "fee", "net", "reporting_category", "created"}

var payoutColumns = []string{"balance_transaction_id", "currency", "net", "reporting_category", "payout_id", "effective_at", "payout_expected_arrival_date", "payout_status"}

var ordinaryCategories = map[string]bool{"charge": true, "refund": true, "partial_capture_reversal": true, "refund_failure": true, "dispute": true, "dispute_reversal": true, "fee": true}

type totals struct {
	count           int
	gross, fee, net int64
}

func (t *totals) add(gross, fee, net int64) {
	t.count++
	t.gross += gross
	t.fee += fee
	t.net += net
}
func (t totals) summary(category string) CategoryTotal {
	return CategoryTotal{category, t.count, money(t.gross), money(t.fee), money(t.net)}
}

type payoutGroup struct {
	id, date   string
	total      totals
	categories map[string]*totals
}

func sortedCategories(groups map[string]*totals) []CategoryTotal {
	result := []CategoryTotal{}
	for name, t := range groups {
		result = append(result, t.summary(name))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Category < result[j].Category })
	return result
}

func stripeHeader(header []string, columns ...string) (map[string]int, bool) {
	if columns == nil {
		columns = stripeColumns
	}
	index := map[string]int{}
	for i, name := range header {
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		index[strings.TrimSpace(name)] = i
	}
	for _, name := range columns {
		if _, ok := index[name]; !ok {
			return nil, false
		}
	}
	return index, true
}

func exportDate(value string) (string, bool) {
	for _, layout := range []string{"2006-01-02", "2006-01-02 15:04:05", time.RFC3339} {
		if _, err := time.Parse(layout, value); err == nil {
			return value[:10], true
		}
	}
	return "", false
}

// Payouts are the report's own automatic_payout_id batches; rows without one are never assigned.
// Every row must be in one currency: the given one, or the first row's when none is given.
func stripePayouts(source, period, currency string) ([]*payoutGroup, map[string]*totals, []string, error) {
	reader := csv.NewReader(strings.NewReader(source))
	header, err := reader.Read()
	index, ok := stripeHeader(header)
	if err != nil || !ok {
		return nil, nil, nil, fmt.Errorf("Use Stripe's itemized payout reconciliation CSV with columns %s.", strings.Join(stripeColumns, ", "))
	}
	reader.FieldsPerRecord = len(header)
	byID := map[string]*payoutGroup{}
	groups := []*payoutGroup{}
	categories := map[string]*totals{}
	unassigned := []string{}
	seen := map[string]bool{}
	for row := 1; ; row++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("Stripe row %d is not valid CSV.", row)
		}
		if row > 5000 {
			return nil, nil, nil, fmt.Errorf("At most 5,000 Stripe rows are supported per workpaper.")
		}
		field := func(name string) string { return strings.TrimSpace(record[index[name]]) }
		id := field("balance_transaction_id")
		if id == "" || len(id) > 100 || seen[id] {
			return nil, nil, nil, fmt.Errorf("Stripe row %d needs a unique balance transaction ID.", row)
		}
		seen[id] = true
		if currency == "" {
			currency = strings.ToUpper(field("currency"))
		}
		if !strings.EqualFold(field("currency"), currency) || !ledger.ValidCurrency(currency) {
			return nil, nil, nil, fmt.Errorf("Stripe row %d (%s) is in %.8q; this workpaper reconciles one two-decimal currency (%s) per export.", row, id, field("currency"), currency)
		}
		gross, grossErr := cents(field("gross"))
		fee, feeErr := cents(field("fee"))
		net, netErr := cents(field("net"))
		if grossErr != nil || feeErr != nil || netErr != nil {
			return nil, nil, nil, fmt.Errorf("Stripe row %d (%s) needs gross, fee and net as decimals.", row, id)
		}
		if gross-fee != net {
			return nil, nil, nil, fmt.Errorf("Stripe row %d (%s): net does not equal gross minus fee. Download a fresh, unedited export.", row, id)
		}
		category := field("reporting_category")
		if category == "" || len(category) > 100 {
			return nil, nil, nil, fmt.Errorf("Stripe row %d (%s) needs its reporting category.", row, id)
		}
		payoutID := field("automatic_payout_id")
		if payoutID == "" {
			unassigned = append(unassigned, id)
			continue
		}
		if len(payoutID) > 100 {
			return nil, nil, nil, fmt.Errorf("Stripe row %d (%s) has an invalid payout ID.", row, id)
		}
		date, ok := exportDate(field("automatic_payout_effective_at"))
		if !ok {
			return nil, nil, nil, fmt.Errorf("Stripe row %d (%s) needs the payout's expected arrival date.", row, id)
		}
		if date[:7] != period {
			return nil, nil, nil, fmt.Errorf("Payout %s is expected %s, outside %s. Download the payout reconciliation report for %s only.", payoutID, date, period, period)
		}
		group := byID[payoutID]
		if group == nil {
			group = &payoutGroup{id: payoutID, date: date, categories: map[string]*totals{}}
			byID[payoutID] = group
			groups = append(groups, group)
		} else if group.date != date {
			return nil, nil, nil, fmt.Errorf("Payout %s has more than one expected arrival date.", payoutID)
		}
		if group.categories[category] == nil {
			group.categories[category] = &totals{}
		}
		if categories[category] == nil {
			categories[category] = &totals{}
		}
		group.total.add(gross, fee, net)
		group.categories[category].add(gross, fee, net)
		categories[category].add(gross, fee, net)
	}
	if len(groups) == 0 && len(unassigned) == 0 {
		return nil, nil, nil, fmt.Errorf("The Stripe export has no transactions.")
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].date != groups[j].date {
			return groups[i].date < groups[j].date
		}
		return groups[i].id < groups[j].id
	})
	return groups, categories, unassigned, nil
}

// stripeManualPayouts reads Stripe's itemized Payouts report: one group per payout whose funds left the balance in the
// month, dated by its expected arrival and carrying the payout's own amount. failed lists payouts Stripe reports as
// failed or canceled, and reversed those whose returned funds are in the file; no deposit is expected for them. paid and
// returned are the funds that left and came back in the month; earlier lists returns of payouts from earlier months.
func stripeManualPayouts(source, period, currency string) (groups []*payoutGroup, failed, reversed, earlier []string, paid, returned int64, err error) {
	reader := csv.NewReader(strings.NewReader(source))
	header, err := reader.Read()
	index, ok := stripeHeader(header, payoutColumns...)
	if err != nil || !ok {
		return nil, nil, nil, nil, 0, 0, fmt.Errorf("Use Stripe's itemized Payouts report with columns %s.", strings.Join(payoutColumns, ", "))
	}
	reader.FieldsPerRecord = len(header)
	reversals, seen := []string{}, map[string]bool{}
	fail := func(format string, args ...any) ([]*payoutGroup, []string, []string, []string, int64, int64, error) {
		return nil, nil, nil, nil, 0, 0, fmt.Errorf(format, args...)
	}
	for n := 1; ; n++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || n > 5000 {
			return fail("Payouts row %d is not valid CSV, or the file has more than 5,000 rows.", n)
		}
		field := func(name string) string { return strings.TrimSpace(record[index[name]]) }
		id, payout, category := field("balance_transaction_id"), field("payout_id"), field("reporting_category")
		if id == "" || len(id) > 100 || seen[id] || payout == "" || len(payout) > 100 {
			return fail("Payouts row %d needs a unique balance transaction ID and its payout ID.", n)
		}
		seen[id] = true
		if !strings.EqualFold(field("currency"), currency) {
			return fail("Payouts row %d (%s) is in %.8q; this workpaper reconciles %s.", n, id, field("currency"), currency)
		}
		net, err := cents(field("net"))
		if err != nil || (category == "payout" && net >= 0) || (category == "payout_reversal" && net <= 0) || (category != "payout" && category != "payout_reversal") {
			return fail("Payouts row %d (%s) must be a payout (negative, as Stripe exports it) or a payout reversal (positive). Download an unedited file.", n, id)
		}
		effective, okEffective := exportDate(field("effective_at"))
		arrival, okArrival := exportDate(field("payout_expected_arrival_date"))
		if !okEffective || !okArrival {
			return fail("Payouts row %d (%s) needs the date the funds left the balance and the expected arrival date.", n, id)
		}
		if effective[:7] != period {
			return fail("Payouts row %d (%s) moved funds on %s, outside %s. Download the Payouts report for %s only.", n, id, effective, period, period)
		}
		if category == "payout_reversal" {
			returned += net
			reversals = append(reversals, payout)
		} else {
			paid -= net
			if slices.ContainsFunc(groups, func(g *payoutGroup) bool { return g.id == payout }) {
				return fail("Payout %s has more than one payout row.", payout)
			}
			groups = append(groups, &payoutGroup{id: payout, date: arrival})
			groups[len(groups)-1].total.add(-net, 0, -net)
			if status := field("payout_status"); status == "failed" || status == "canceled" {
				failed = append(failed, payout)
			}
		}
	}
	// A reversal returns the funds to the balance; the payout keeps its own amount. A reversal of an earlier month's
	// payout counts in the month's movements only.
	for _, payout := range reversals {
		if slices.ContainsFunc(groups, func(g *payoutGroup) bool { return g.id == payout }) {
			reversed = append(reversed, payout)
		} else {
			earlier = append(earlier, payout)
		}
	}
	if len(groups) == 0 && len(earlier) == 0 {
		return fail("The Payouts report has no payouts.")
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].date != groups[j].date {
			return groups[i].date < groups[j].date
		}
		return groups[i].id < groups[j].id
	})
	return groups, failed, reversed, earlier, paid, returned, nil
}

// stripeActivity reads Stripe's itemized balance change from activity report for the month: every balance transaction
// except payouts, by category, in the report's timezone like the Payouts report.
func stripeActivity(source, period, currency string) (map[string]*totals, totals, error) {
	reader := csv.NewReader(strings.NewReader(source))
	header, err := reader.Read()
	index, ok := stripeHeader(header, activityColumns...)
	if err != nil || !ok {
		return nil, totals{}, fmt.Errorf("Use Stripe's itemized balance change from activity report with columns %s.", strings.Join(activityColumns, ", "))
	}
	reader.FieldsPerRecord = len(header)
	categories, seen, all := map[string]*totals{}, map[string]bool{}, totals{}
	for n := 1; ; n++ {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || n > 5000 {
			return nil, totals{}, fmt.Errorf("Activity row %d is not valid CSV, or the file has more than 5,000 rows.", n)
		}
		field := func(name string) string { return strings.TrimSpace(record[index[name]]) }
		id, category := field("balance_transaction_id"), field("reporting_category")
		gross, grossErr := cents(field("gross"))
		fee, feeErr := cents(field("fee"))
		net, netErr := cents(field("net"))
		created, dated := exportDate(field("created"))
		switch {
		case id == "" || len(id) > 100 || seen[id]:
			return nil, totals{}, fmt.Errorf("Activity row %d needs a unique balance transaction ID.", n)
		case !strings.EqualFold(field("currency"), currency):
			return nil, totals{}, fmt.Errorf("Activity row %d (%s) is in %.8q; this workpaper reconciles %s.", n, id, field("currency"), currency)
		case grossErr != nil || feeErr != nil || netErr != nil || gross-fee != net:
			return nil, totals{}, fmt.Errorf("Activity row %d (%s) needs gross, fee and net as decimals, with net equal to gross minus fee.", n, id)
		case category == "" || len(category) > 100 || category == "payout" || category == "payout_reversal":
			return nil, totals{}, fmt.Errorf("Activity row %d (%s) needs a reporting category other than a payout; payouts come from the Payouts report.", n, id)
		case !dated || created[:7] != period:
			return nil, totals{}, fmt.Errorf("Activity row %d (%s) was created outside %s. Download the activity report for %s only.", n, id, period, period)
		}
		seen[id] = true
		if categories[category] == nil {
			categories[category] = &totals{}
		}
		categories[category].add(gross, fee, net)
		all.add(gross, fee, net)
	}
	return categories, all, nil
}

// StripeReportSummary recognizes an itemized payout reconciliation export or Payouts report without a model. ok is
// false for any other file.
func StripeReportSummary(source, period string) (string, bool, error) {
	reader := csv.NewReader(strings.NewReader(source))
	header, err := reader.Read()
	if index, ok := stripeHeader(header, payoutColumns...); err == nil && ok {
		currency := ""
		if record, e := reader.Read(); e == nil && len(record) == len(header) {
			currency = strings.ToUpper(strings.TrimSpace(record[index["currency"]]))
		}
		groups, _, _, earlier, _, _, err := stripeManualPayouts(source, period, currency)
		if err != nil {
			return "", true, err
		}
		summary := fmt.Sprintf("Stripe itemized Payouts report with %d returns of earlier payouts.", len(earlier))
		if len(groups) > 0 {
			summary = fmt.Sprintf("Stripe itemized Payouts report: %d payouts expected %s to %s.", len(groups), groups[0].date, groups[len(groups)-1].date)
		}
		return summary + " Trace it with the Payouts report method, with the bank account's CSV for the same month and, to roll the Stripe balance forward, the month's balance activity report.", true, nil
	}
	if _, ok := stripeHeader(header); err != nil || !ok {
		return "", false, nil
	}
	groups, _, unassigned, err := stripePayouts(source, period, "")
	if err != nil {
		return "", true, err
	}
	rows := len(unassigned)
	for _, group := range groups {
		rows += group.total.count
	}
	summary := fmt.Sprintf("Stripe itemized payout reconciliation export with %d balance transactions.", rows)
	if len(groups) > 0 {
		summary = fmt.Sprintf("Stripe itemized payout reconciliation export: %d automatic payouts expected %s to %s from %d balance transactions.", len(groups), groups[0].date, groups[len(groups)-1].date, rows)
	}
	if len(unassigned) > 0 {
		summary += fmt.Sprintf(" %d rows have no automatic payout ID and will stay unassigned.", len(unassigned))
	}
	return summary + " Choose the bank account's CSV for the same month to trace each payout to its deposit.", true, nil
}

func PrepareSettlement(input SettlementInput) (SettlementWorkpaper, error) {
	// An input without a method is an automatic-payout export, as before the manual-payout method existed.
	method := cmp.Or(input.Method, SettlementMethod)
	result := SettlementWorkpaper{Status: "draft", Method: method, Input: input, Payouts: []Payout{}, Categories: []CategoryTotal{}, Unassigned: []string{}, Questions: []string{}, Blockers: []string{}}
	fail := func(format string, args ...any) (SettlementWorkpaper, error) {
		return SettlementWorkpaper{}, fmt.Errorf(format, args...)
	}
	for _, value := range []string{input.Entity, input.BankAccount, input.ReportReference, input.BankReference} {
		if strings.TrimSpace(value) == "" || len(value) > 200 {
			return fail("Entity, bank account and both source references require 1–200 characters.")
		}
	}
	if !ledger.ValidCurrency(input.Currency) || !input.MethodConfirmed || !input.CoverageConfirmed || (method != SettlementMethod && method != PayoutsMethod) {
		return fail("Confirm the automatic-payout or manual-payout method in the client's home currency and that both files cover the full close month.")
	}
	if method != PayoutsMethod && input.ActivityCSV != "" {
		return fail("An activity report goes with the Payouts report method; the automatic-payout export already has each payout's transactions.")
	}
	if input.ActivityCSV != "" && (strings.TrimSpace(input.ActivityReference) == "" || len(input.ActivityReference) > 200) {
		return fail("The activity report's source reference requires 1–200 characters.")
	}
	if method == PayoutsMethod && input.FeeEntry != nil {
		return fail("A fee entry needs each payout's fees, which only the automatic-payout export gives; take the month's fees from Stripe's Balance report instead.")
	}
	month, err := time.Parse("2006-01", input.ClosePeriod)
	if err != nil {
		return fail("Choose a valid close month.")
	}
	var bookedFees int64
	if f := input.FeeEntry; f != nil {
		f.FeeAccount, f.DepositAccount, f.RuleReference = strings.TrimSpace(f.FeeAccount), strings.TrimSpace(f.DepositAccount), strings.TrimSpace(f.RuleReference)
		booked, err := cents(strings.TrimSpace(f.BookedFees))
		if f.FeeAccount == "" || len(f.FeeAccount) > 200 || f.DepositAccount == "" || len(f.DepositAccount) > 200 || strings.EqualFold(f.FeeAccount, f.DepositAccount) || len(f.RuleReference) < 5 || len(f.RuleReference) > 200 || err != nil || booked < 0 {
			return fail("A Stripe fee entry needs a fee expense account, the separate account the net deposits were booked to, the fees already booked this month (enter zero explicitly) and the client accountant's rule reference.")
		}
		f.BookedFees, bookedFees = money(booked), booked
	}
	var bookedRefunds, bookedRevenue int64
	if r := input.RefundEntry; r != nil {
		r.RefundAccount, r.RuleReference = strings.TrimSpace(r.RefundAccount), strings.TrimSpace(r.RuleReference)
		booked, err := cents(strings.TrimSpace(r.BookedRefunds))
		if input.FeeEntry == nil || r.RefundAccount == "" || len(r.RefundAccount) > 200 || strings.EqualFold(r.RefundAccount, input.FeeEntry.DepositAccount) || strings.EqualFold(r.RefundAccount, input.FeeEntry.FeeAccount) || len(r.RuleReference) < 5 || len(r.RuleReference) > 200 || err != nil || booked < 0 {
			return fail("A refund entry needs the fee entry (which names the deposit account), a separate refunds account, the refunds already booked for these payouts this month (enter zero explicitly) and the client accountant's rule reference.")
		}
		r.BookedRefunds, bookedRefunds = money(booked), booked
	}
	if v := input.RevenueEntry; v != nil {
		v.RevenueAccount, v.RuleReference = strings.TrimSpace(v.RevenueAccount), strings.TrimSpace(v.RuleReference)
		booked, err := cents(strings.TrimSpace(v.BookedRevenue))
		clash := input.FeeEntry == nil || strings.EqualFold(v.RevenueAccount, input.FeeEntry.DepositAccount) || strings.EqualFold(v.RevenueAccount, input.FeeEntry.FeeAccount) || (input.RefundEntry != nil && strings.EqualFold(v.RevenueAccount, input.RefundEntry.RefundAccount))
		if clash || !v.ClearingConfirmed || v.RevenueAccount == "" || len(v.RevenueAccount) > 200 || len(v.RuleReference) < 5 || len(v.RuleReference) > 200 || err != nil || booked < 0 {
			return fail("A revenue entry needs the fee entry, the accountant's rule that its deposit account is a clearing account, a separate revenue account, the revenue already booked for these payouts this month (enter zero explicitly) and the rule reference.")
		}
		v.BookedRevenue, bookedRevenue = money(booked), booked
	}
	if len(input.ReviewedDeposits)+len(input.InTransit) > 100 {
		return fail("Review at most 100 payouts per workpaper.")
	}
	var groups []*payoutGroup
	var categories map[string]*totals
	var unassigned, failed, reversed, earlier []string
	var paid, returned int64
	if method == PayoutsMethod {
		groups, failed, reversed, earlier, paid, returned, err = stripeManualPayouts(input.ReportCSV, input.ClosePeriod, input.Currency)
	} else {
		groups, categories, unassigned, err = stripePayouts(input.ReportCSV, input.ClosePeriod, input.Currency)
	}
	if err != nil {
		return fail("Stripe export: %s", err.Error())
	}
	// Deposits can land a few days after the expected date, so the bank file may run past month-end; a payout that left
	// the balance late in the month may be expected early next month.
	through, window := month.AddDate(0, 1, 3), "the fourth day after month-end"
	for _, group := range groups {
		if expected, _ := time.Parse("2006-01-02", group.date); expected.AddDate(0, 0, 4).After(through) {
			through, window = expected.AddDate(0, 0, 4), "four days after the last payout's expected arrival"
		}
	}
	bank, _, err := datedLines(input.BankCSV, month.AddDate(0, 0, -1), through, "bank rows must be dated from the last day of the prior month through "+window)
	if err != nil {
		return fail("Bank CSV: %s", err.Error())
	}
	byPayout := map[string]*payoutGroup{}
	for _, group := range groups {
		byPayout[group.id] = group
	}
	byBank := map[string]Line{}
	for _, row := range bank {
		byBank[row.ID] = row
	}
	day := func(value string) time.Time {
		parsed, _ := time.Parse("2006-01-02", value)
		return parsed
	}
	reason := func(value string) bool { return len(strings.TrimSpace(value)) >= 5 && len(value) <= 500 }
	used := map[string]bool{}
	reviewed := map[string]ReviewedDeposit{}
	for _, item := range input.ReviewedDeposits {
		group, row := byPayout[item.PayoutID], byBank[item.BankID]
		if group == nil || slices.Contains(failed, item.PayoutID) || reviewed[item.PayoutID].PayoutID != "" || row.ID == "" || used[row.ID] || !reason(item.Reason) {
			return fail("Each reviewed deposit needs a listed payout, an unused bank row and a 5–500 character reason.")
		}
		amount, _ := cents(row.Amount)
		if amount != group.total.net || day(row.Date).Before(day(group.date).AddDate(0, 0, -1)) {
			return fail("Bank row %s must carry exactly %s and post no earlier than one day before payout %s is expected.", row.ID, ledger.Show(money(group.total.net), input.Currency), group.id)
		}
		used[row.ID] = true
		reviewed[item.PayoutID] = item
	}
	transit := map[string]InTransit{}
	for _, item := range input.InTransit {
		if byPayout[item.PayoutID] == nil || slices.Contains(failed, item.PayoutID) || reviewed[item.PayoutID].PayoutID != "" || transit[item.PayoutID].PayoutID != "" || !reason(item.Reason) || len(strings.TrimSpace(item.PolicyReference)) < 5 || len(item.PolicyReference) > 200 {
			return fail("Each in-transit payout needs a listed, otherwise unreviewed payout, the accountant-approved rule reference and a 5–500 character reason citing the evidence.")
		}
		transit[item.PayoutID] = item
	}
	candidates := map[string][]string{}
	claims := map[string]int{}
	for _, group := range groups {
		if reviewed[group.id].PayoutID != "" || slices.Contains(failed, group.id) {
			continue
		}
		expected := day(group.date)
		for _, row := range bank {
			amount, _ := cents(row.Amount)
			offset := day(row.Date).Sub(expected).Hours() / 24
			if !used[row.ID] && amount == group.total.net && offset >= -1 && offset <= 4 {
				candidates[group.id] = append(candidates[group.id], row.ID)
				claims[row.ID]++
			}
		}
	}
	for _, group := range groups {
		payout := Payout{ID: group.id, ExpectedDate: group.date, Count: group.total.count, Gross: money(group.total.gross), Fee: money(group.total.fee), Net: money(group.total.net), Categories: sortedCategories(group.categories), CandidateIDs: candidates[group.id]}
		if payout.CandidateIDs == nil {
			payout.CandidateIDs = []string{}
		}
		switch found := candidates[group.id]; {
		case slices.Contains(failed, group.id):
			payout.Status = "failed"
			returned := "its return is not in this file; confirm it in Stripe"
			if slices.Contains(reversed, group.id) {
				returned = "the file shows its funds returned to the Stripe balance"
			}
			result.Questions = append(result.Questions, fmt.Sprintf("Stripe reports payout %s (%s) as failed or canceled, so no deposit is expected; %s before recording it.", group.id, ledger.Show(payout.Net, input.Currency), returned))
		case reviewed[group.id].PayoutID != "":
			row := byBank[reviewed[group.id].BankID]
			payout.Status, payout.Deposit, payout.Reason = "reviewed", &row, reviewed[group.id].Reason
		case transit[group.id].PayoutID != "":
			if len(found) > 0 {
				return fail("Payout %s has a possible bank row (%s); review it instead of marking the payout in transit.", group.id, strings.Join(found, ", "))
			}
			payout.Status, payout.Reason, payout.Policy = "in_transit", transit[group.id].Reason, transit[group.id].PolicyReference
		case len(found) == 1 && claims[found[0]] == 1:
			row := byBank[found[0]]
			payout.Status, payout.Deposit = "matched", &row
		case len(found) == 0:
			payout.Status = "unmatched"
			result.Blockers = append(result.Blockers, fmt.Sprintf("Payout %s (%s, expected %s) has no bank row of exactly that amount from one day before to four days after. Find the deposit, or mark it in transit with evidence.", group.id, ledger.Show(payout.Net, input.Currency), group.date))
		default:
			payout.Status = "ambiguous"
			message := fmt.Sprintf("Payout %s (%s, expected %s) could be bank row %s. Choose its deposit with a reason.", group.id, ledger.Show(payout.Net, input.Currency), group.date, strings.Join(found, " or "))
			if len(found) == 1 {
				message = fmt.Sprintf("Payout %s (%s, expected %s) matches bank row %s, but another payout of the same amount does too. Choose each deposit with a reason.", group.id, ledger.Show(payout.Net, input.Currency), group.date, found[0])
			}
			result.Blockers = append(result.Blockers, message)
		}
		result.Payouts = append(result.Payouts, payout)
	}
	var total totals
	for _, group := range groups {
		total.count += group.total.count
		total.gross += group.total.gross
		total.fee += group.total.fee
		total.net += group.total.net
	}
	result.Total = total.summary("all")
	if len(earlier) > 0 {
		result.Questions = append(result.Questions, fmt.Sprintf("Payouts from earlier months returned funds to the Stripe balance this month (%s). Confirm how each return was recorded.", strings.Join(earlier, ", ")))
	}
	if method == PayoutsMethod && input.ActivityCSV == "" {
		result.Questions = append(result.Questions, "Payouts are traced as payouts only: Stripe cannot say which transactions a manual or instant payout settles. Add Stripe's balance change from activity report for the month to roll the balance forward.")
		return result, nil
	}
	if method == PayoutsMethod {
		var activity totals
		if categories, activity, err = stripeActivity(input.ActivityCSV, input.ClosePeriod, input.Currency); err != nil {
			return fail("Activity report: %s", err.Error())
		}
		change := activity.net - paid + returned
		result.RollForward = &RollForward{Activity: money(activity.net), PaidOut: money(paid), Returned: money(returned), Change: money(change)}
		result.Questions = append(result.Questions, fmt.Sprintf("Computed roll-forward: the month's activity of %s, less %s paid out, plus %s returned, changes the Stripe balance by %s. Stripe cannot say which of these transactions each payout settled, and this is not tied to Stripe's opening and closing balances; compare it with the Balance summary for the month.", ledger.Show(money(activity.net), input.Currency), ledger.Show(money(paid), input.Currency), ledger.Show(money(returned), input.Currency), ledger.Show(money(change), input.Currency)))
	}
	result.Categories = sortedCategories(categories)
	for _, category := range result.Categories {
		if !ordinaryCategories[category.Category] {
			result.Questions = append(result.Questions, fmt.Sprintf("Stripe category %s (%d rows, net %s) is outside ordinary charges, refunds, disputes and fees. Confirm its treatment before recording these deposits.", category.Category, category.Count, ledger.Show(category.Net, input.Currency)))
		}
	}
	if len(unassigned) > 0 {
		result.Unassigned = unassigned
		shown := unassigned
		if len(shown) > 5 {
			shown = shown[:5]
		}
		result.Blockers = append(result.Blockers, fmt.Sprintf("Stripe rows without an automatic payout ID: %d (%s). This method traces automatic payouts only; reconcile manual or instant payouts against transaction history.", len(unassigned), strings.Join(shown, ", ")))
	}
	if input.FeeEntry != nil {
		// Only payouts whose deposit landed this month were booked net this month. Their fees are the
		// per-transaction fees plus Stripe fees charged as their own rows (category "fee", negative gross);
		// their refunds and revenue are the signed gross of those categories.
		monthEnd := month.AddDate(0, 1, -1).Format("2006-01-02")
		type amounts struct{ fee, refund, revenue, other int64 }
		var current, deferred, untraced amounts
		for _, payout := range result.Payouts {
			var a amounts
			a.fee, _ = cents(payout.Fee)
			for _, category := range payout.Categories {
				gross, _ := cents(category.Gross)
				switch category.Category {
				case "fee":
					a.fee -= gross
				case "refund", "refund_failure":
					a.refund -= gross
				case "charge", "partial_capture_reversal":
					a.revenue += gross
				default:
					a.other += gross
				}
			}
			bucket := &deferred
			switch {
			case payout.Status == "unmatched" || payout.Status == "ambiguous":
				bucket = &untraced
			case payout.Deposit != nil && strings.HasPrefix(payout.Deposit.Date, input.ClosePeriod+"-"):
				bucket = &current
			}
			bucket.fee, bucket.refund, bucket.revenue, bucket.other = bucket.fee+a.fee, bucket.refund+a.refund, bucket.revenue+a.revenue, bucket.other+a.other
		}
		// entry proposes debit/credit for the difference from what is booked, reversed when more was booked.
		entry := func(delta int64, debit, credit string) *prepaids.Adjustment {
			if len(result.Blockers) > 0 || delta == 0 {
				return nil
			}
			if delta < 0 {
				debit, credit, delta = credit, debit, -delta
			}
			return &prepaids.Adjustment{Date: monthEnd, DebitAccount: debit, CreditAccount: credit, Amount: money(delta)}
		}
		left := func(label string, later, open int64) {
			if later != 0 {
				result.Questions = append(result.Questions, fmt.Sprintf("%s of %s belong to payouts in transit or deposited after this month. This workpaper leaves them out, and next month's report does not include these payouts, so record them with the deposit month's books by hand.", label, ledger.Show(money(later), input.Currency)))
			}
			if open != 0 {
				result.Questions = append(result.Questions, fmt.Sprintf("%s of %s belong to payouts not yet traced to a deposit; trace them before the entry is proposed.", label, ledger.Show(money(open), input.Currency)))
			}
		}
		deposit := input.FeeEntry.DepositAccount
		result.Fees = money(current.fee)
		left("Fees", deferred.fee, untraced.fee)
		result.Proposal = entry(current.fee-bookedFees, input.FeeEntry.FeeAccount, deposit)
		if r := input.RefundEntry; r != nil {
			result.Refunds = money(current.refund)
			left("Refunds", deferred.refund, untraced.refund)
			result.RefundProposal = entry(current.refund-bookedRefunds, r.RefundAccount, deposit)
		}
		if v := input.RevenueEntry; v != nil {
			result.Revenue = money(current.revenue)
			left("Gross charges", deferred.revenue, untraced.revenue)
			result.RevenueProposal = entry(current.revenue-bookedRevenue, deposit, v.RevenueAccount)
		}
		if (input.RefundEntry != nil || input.RevenueEntry != nil) && current.other != 0 {
			result.Questions = append(result.Questions, fmt.Sprintf("Disputes and other Stripe categories in payouts deposited this month total %s gross. No entry covers them, so they stay in %s; confirm their treatment.", ledger.Show(money(current.other), input.Currency), deposit))
		}
	}
	return result, nil
}
