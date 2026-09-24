package reconciliation

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/prepaids"
)

const SettlementMethod = "stripe_automatic_payouts_v1"

type SettlementInput struct {
	Entity            string            `json:"entity"`
	ClosePeriod       string            `json:"close_period"`
	Currency          string            `json:"currency"`
	BankAccount       string            `json:"bank_account"`
	ReportReference   string            `json:"report_reference"`
	BankReference     string            `json:"bank_reference"`
	ReportCSV         string            `json:"report_csv"`
	BankCSV           string            `json:"bank_csv"`
	MethodConfirmed   bool              `json:"method_confirmed"`
	CoverageConfirmed bool              `json:"coverage_confirmed"`
	ReviewedDeposits  []ReviewedDeposit `json:"reviewed_deposits,omitempty"`
	InTransit         []InTransit       `json:"in_transit,omitempty"`
	FeeEntry          *FeeEntry         `json:"fee_entry,omitempty"`
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
}

var stripeColumns = []string{"balance_transaction_id", "currency", "gross", "fee", "net", "reporting_category", "automatic_payout_id", "automatic_payout_effective_at"}

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

func stripeHeader(header []string) (map[string]int, bool) {
	index := map[string]int{}
	for i, name := range header {
		if i == 0 {
			name = strings.TrimPrefix(name, "\ufeff")
		}
		index[strings.TrimSpace(name)] = i
	}
	for _, name := range stripeColumns {
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

// StripeReportSummary recognizes an itemized payout export without a model. ok is false for any other file.
func StripeReportSummary(source, period string) (string, bool, error) {
	header, err := csv.NewReader(strings.NewReader(source)).Read()
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
	result := SettlementWorkpaper{Status: "draft", Method: SettlementMethod, Input: input, Payouts: []Payout{}, Categories: []CategoryTotal{}, Unassigned: []string{}, Questions: []string{}, Blockers: []string{}}
	fail := func(format string, args ...any) (SettlementWorkpaper, error) {
		return SettlementWorkpaper{}, fmt.Errorf(format, args...)
	}
	for _, value := range []string{input.Entity, input.BankAccount, input.ReportReference, input.BankReference} {
		if strings.TrimSpace(value) == "" || len(value) > 200 {
			return fail("Entity, bank account and both source references require 1–200 characters.")
		}
	}
	if !ledger.ValidCurrency(input.Currency) || !input.MethodConfirmed || !input.CoverageConfirmed {
		return fail("Confirm the automatic-payout method in the client's home currency and that both files cover the full close month.")
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
	if len(input.ReviewedDeposits)+len(input.InTransit) > 100 {
		return fail("Review at most 100 payouts per workpaper.")
	}
	groups, categories, unassigned, err := stripePayouts(input.ReportCSV, input.ClosePeriod, input.Currency)
	if err != nil {
		return fail("Stripe export: %s", err.Error())
	}
	// Deposits can land a few days after the expected date, so the bank file may run past month-end.
	bank, _, err := datedLines(input.BankCSV, month.AddDate(0, 0, -1), month.AddDate(0, 1, 3), "bank rows must be dated from the last day of the prior month through the fourth day after month-end")
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
		if group == nil || reviewed[item.PayoutID].PayoutID != "" || row.ID == "" || used[row.ID] || !reason(item.Reason) {
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
		if byPayout[item.PayoutID] == nil || reviewed[item.PayoutID].PayoutID != "" || transit[item.PayoutID].PayoutID != "" || !reason(item.Reason) || len(strings.TrimSpace(item.PolicyReference)) < 5 || len(item.PolicyReference) > 200 {
			return fail("Each in-transit payout needs a listed, otherwise unreviewed payout, the accountant-approved rule reference and a 5–500 character reason citing the evidence.")
		}
		transit[item.PayoutID] = item
	}
	candidates := map[string][]string{}
	claims := map[string]int{}
	for _, group := range groups {
		if reviewed[group.id].PayoutID != "" {
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
		// per-transaction fees plus Stripe fees charged as their own rows (category "fee", negative gross).
		monthEnd := month.AddDate(0, 1, -1).Format("2006-01-02")
		var fees, deferred, untraced int64
		for _, payout := range result.Payouts {
			fee, _ := cents(payout.Fee)
			for _, category := range payout.Categories {
				if category.Category == "fee" {
					gross, _ := cents(category.Gross)
					fee -= gross
				}
			}
			switch {
			case payout.Status == "unmatched" || payout.Status == "ambiguous":
				untraced += fee
			case payout.Deposit != nil && strings.HasPrefix(payout.Deposit.Date, input.ClosePeriod+"-"):
				fees += fee
			default:
				deferred += fee
			}
		}
		result.Fees = money(fees)
		if deferred != 0 {
			result.Questions = append(result.Questions, fmt.Sprintf("Fees of %s belong to payouts in transit or deposited outside this month; they are left for the month their deposit is booked.", ledger.Show(money(deferred), input.Currency)))
		}
		if untraced != 0 {
			result.Questions = append(result.Questions, fmt.Sprintf("Fees of %s belong to payouts not yet traced to a deposit; trace them before the fee entry is proposed.", ledger.Show(money(untraced), input.Currency)))
		}
		if delta := fees - bookedFees; len(result.Blockers) == 0 && delta != 0 {
			result.Proposal = &prepaids.Adjustment{Date: monthEnd, DebitAccount: input.FeeEntry.FeeAccount, CreditAccount: input.FeeEntry.DepositAccount, Amount: money(delta)}
			if delta < 0 {
				result.Proposal.DebitAccount, result.Proposal.CreditAccount, result.Proposal.Amount = input.FeeEntry.DepositAccount, input.FeeEntry.FeeAccount, money(-delta)
			}
		}
	}
	return result, nil
}
