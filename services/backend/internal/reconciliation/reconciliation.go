package reconciliation

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/ledger"
)

type Input struct {
	Entity             string          `json:"entity"`
	Account            string          `json:"account"`
	AccountType        string          `json:"account_type,omitempty"`
	CardRuleReference  string          `json:"card_rule_reference,omitempty"`
	ClosePeriod        string          `json:"close_period"`
	Currency           string          `json:"currency"`
	StatementReference string          `json:"statement_reference"`
	LedgerReference    string          `json:"ledger_reference"`
	ExpectedPages      int             `json:"expected_pages"`
	Pages              []int           `json:"pages"`
	StatementOpening   string          `json:"statement_opening"`
	StatementClosing   string          `json:"statement_closing"`
	LedgerOpening      string          `json:"ledger_opening"`
	LedgerClosing      string          `json:"ledger_closing"`
	StatementCSV       string          `json:"statement_csv"`
	LedgerCSV          string          `json:"ledger_csv"`
	PolicyConfirmed    bool            `json:"policy_confirmed"`
	ReviewedMatches    []ReviewedMatch `json:"reviewed_matches,omitempty"`
	PriorOutstanding   []PriorItem     `json:"prior_outstanding,omitempty"`
	CurrentOutstanding []CurrentItem   `json:"current_outstanding,omitempty"`
	Transfers          []TransferPair  `json:"transfers,omitempty"`
	OtherAccount       string          `json:"other_account,omitempty"`
	OtherStatementCSV  string          `json:"other_statement_csv,omitempty"`
	OtherLedgerCSV     string          `json:"other_ledger_csv,omitempty"`
	OtherStatementRef  string          `json:"other_statement_reference,omitempty"`
	OtherLedgerRef     string          `json:"other_ledger_reference,omitempty"`
	NativeStatus       string          `json:"native_status,omitempty"`
	NativeStatusRef    string          `json:"native_status_reference,omitempty"`
}
type PriorItem struct {
	Side            string `json:"side"`
	ID              string `json:"id"`
	Amount          string `json:"amount"`
	Treatment       string `json:"treatment,omitempty"`
	ClearingRef     string `json:"clearing_reference,omitempty"`
	SourceReference string `json:"source_reference"`
	PolicyReference string `json:"policy_reference"`
	ClearedBy       string `json:"cleared_by,omitempty"`
	Reason          string `json:"reason"`
}
type CurrentItem struct {
	Side            string `json:"side"`
	ID              string `json:"id"`
	Treatment       string `json:"treatment,omitempty"`
	ClearingRef     string `json:"clearing_reference,omitempty"`
	PolicyReference string `json:"policy_reference"`
	Reason          string `json:"reason"`
}
type TransferPair struct {
	StatementID      string `json:"statement_id"`
	LedgerID         string `json:"ledger_id"`
	OtherStatementID string `json:"other_statement_id"`
	OtherLedgerID    string `json:"other_ledger_id"`
	PolicyReference  string `json:"policy_reference"`
	Reason           string `json:"reason"`
}
type ReviewedMatch struct {
	StatementIDs    []string `json:"statement_ids"`
	LedgerIDs       []string `json:"ledger_ids"`
	Method          string   `json:"method"`
	Reason          string   `json:"reason"`
	PolicyReference string   `json:"policy_reference,omitempty"`
	DateWindowDays  int      `json:"date_window_days,omitempty"`
	AmountTolerance string   `json:"amount_tolerance,omitempty"`
}
type ConfirmedMatch struct {
	ReviewedMatch
	AmountDifference string `json:"amount_difference"`
}
type Line struct {
	ID     string `json:"id"`
	Date   string `json:"date"`
	Amount string `json:"amount"`
}
type Match struct {
	StatementID string `json:"statement_id"`
	LedgerID    string `json:"ledger_id"`
	Reason      string `json:"reason"`
}
type Exception struct {
	Side            string     `json:"side"`
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	CandidateIDs    []string   `json:"candidate_ids"`
	CandidateGroups [][]string `json:"candidate_groups,omitempty"`
	SplitGroups     [][]string `json:"split_groups,omitempty"`
	CandidateCount  int        `json:"candidate_count"`
	GroupCount      int        `json:"group_count,omitempty"`
	SplitCount      int        `json:"split_count,omitempty"`
	Question        string     `json:"question"`
}
type Workpaper struct {
	Status                         string           `json:"status"`
	Method                         string           `json:"method"`
	Input                          Input            `json:"input"`
	Matches                        []Match          `json:"matches"`
	ReviewedMatches                []ConfirmedMatch `json:"reviewed_matches"`
	UnmatchedStatement             []Line           `json:"unmatched_statement"`
	UnmatchedLedger                []Line           `json:"unmatched_ledger"`
	Exceptions                     []Exception      `json:"exceptions"`
	Blockers                       []string         `json:"blockers"`
	StatementRollForwardDifference string           `json:"statement_roll_forward_difference"`
	LedgerRollForwardDifference    string           `json:"ledger_roll_forward_difference"`
	OpeningDifference              string           `json:"opening_difference"`
	ClosingDifference              string           `json:"closing_difference"`
	OpeningBridgeDifference        string           `json:"opening_bridge_difference"`
	ClosingBridgeDifference        string           `json:"closing_bridge_difference"`
}

var amountPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,9})(\.[0-9]{1,2})?$`)

func cents(value string) (int64, error) {
	if !amountPattern.MatchString(value) {
		return 0, fmt.Errorf("amounts must be decimals with at most two fractional digits and at most 10 whole digits")
	}
	negative := strings.HasPrefix(value, "-")
	value = strings.TrimPrefix(value, "-")
	whole, fraction, _ := strings.Cut(value, ".")
	n, _ := strconv.ParseInt(whole, 10, 64)
	f, _ := strconv.ParseInt((fraction + "00")[:2], 10, 64)
	n = n*100 + f
	if negative {
		n = -n
	}
	return n, nil
}
func money(n int64) string {
	if n < 0 {
		return "-" + money(-n)
	}
	return fmt.Sprintf("%d.%02d", n/100, n%100)
}
func lines(source, period string) ([]Line, int64, error) {
	month, _ := time.Parse("2006-01", period)
	return datedLines(source, month, month.AddDate(0, 1, -1), "transaction dates must be within the close month")
}

// CSVSummary recognizes the shared id,date,amount format; it cannot tell a bank
// statement from a ledger export, so the reviewer chooses the role.
func CSVSummary(source, period string) (string, bool, error) {
	header, err := csv.NewReader(strings.NewReader(source)).Read()
	if err != nil || strings.Join(header, ",") != "id,date,amount" {
		return "", false, nil
	}
	rows, total, err := lines(source, period)
	if err != nil {
		return "", true, err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("Reconciliation CSV with no transactions in %s. It could be the bank statement or the ledger export; choose which on the reconciliation page.", period), true, nil
	}
	first, last := rows[0].Date, rows[0].Date
	for _, row := range rows {
		first, last = min(first, row.Date), max(last, row.Date)
	}
	return fmt.Sprintf("Reconciliation CSV with %d transactions dated %s to %s, net %s. It could be the bank statement or the ledger export; choose which on the reconciliation page.", len(rows), first, last, money(total)), true, nil
}
func datedLines(source string, first, last time.Time, dateError string) ([]Line, int64, error) {
	reader := csv.NewReader(strings.NewReader(source))
	reader.FieldsPerRecord = 3
	header, err := reader.Read()
	if err != nil || strings.Join(header, ",") != "id,date,amount" {
		return nil, 0, fmt.Errorf("CSV must begin with id,date,amount")
	}
	result := []Line{}
	seen := map[string]bool{}
	var total int64
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("invalid CSV row: %w", err)
		}
		if len(result) >= 2000 {
			return nil, 0, fmt.Errorf("at most 2000 transactions per source are supported")
		}
		id := strings.TrimSpace(row[0])
		if id == "" || len(id) > 100 || seen[id] {
			return nil, 0, fmt.Errorf("each source needs unique nonempty IDs of at most 100 characters")
		}
		seen[id] = true
		date, err := time.Parse("2006-01-02", row[1])
		if err != nil || date.Before(first) || date.After(last) {
			return nil, 0, fmt.Errorf("%s", dateError)
		}
		n, err := cents(row[2])
		if err != nil {
			return nil, 0, err
		}
		total += n
		result = append(result, Line{id, row[1], money(n)})
	}
	return result, total, nil
}

func reviewMatches(input []ReviewedMatch, statement, ledger []Line) ([]ConfirmedMatch, map[string]bool, map[string]bool, error) {
	selected := []ConfirmedMatch{}
	usedStatement, usedLedger := map[string]bool{}, map[string]bool{}
	byStatement, byLedger := map[string]Line{}, map[string]Line{}
	for _, row := range statement {
		byStatement[row.ID] = row
	}
	for _, row := range ledger {
		byLedger[row.ID] = row
	}
	if len(input) > 100 {
		return nil, nil, nil, fmt.Errorf("Review at most 100 match groups per workpaper.")
	}
	for _, match := range input {
		if len(match.StatementIDs) < 1 || len(match.StatementIDs) > 5 || len(match.LedgerIDs) < 1 || len(match.LedgerIDs) > 5 || len(strings.TrimSpace(match.Reason)) < 5 || len(match.Reason) > 500 {
			return nil, nil, nil, fmt.Errorf("Each reviewed match needs 1–5 IDs per side and a 5–500 character reason.")
		}
		if match.Method != "exact" && match.Method != "tolerance" && match.Method != "split" {
			return nil, nil, nil, fmt.Errorf("Choose an exact group, net split, or accountant-approved tolerance method.")
		}
		grouped := len(match.StatementIDs) > 1 || len(match.LedgerIDs) > 1
		approvedRule := len(strings.TrimSpace(match.PolicyReference)) >= 5 && len(match.PolicyReference) <= 200
		var tolerance int64
		if match.Method == "tolerance" {
			var err error
			tolerance, err = cents(match.AmountTolerance)
			if err != nil || tolerance < 0 || tolerance > 99999 || match.DateWindowDays < 0 || match.DateWindowDays > 31 || (tolerance == 0 && match.DateWindowDays == 0) || !approvedRule {
				return nil, nil, nil, fmt.Errorf("Tolerance needs an approved rule reference, a date window of at most 31 days and an amount ceiling of at most $999.99.")
			}
		} else if match.Method == "split" {
			if !approvedRule || match.AmountTolerance != "" || match.DateWindowDays < 0 || match.DateWindowDays > 31 || !((len(match.StatementIDs) == 1 && len(match.LedgerIDs) > 1) || (len(match.LedgerIDs) == 1 && len(match.StatementIDs) > 1)) {
				return nil, nil, nil, fmt.Errorf("A net split needs one row against 2–5 components, an approved rule, exact net cents and a date window of at most 31 days.")
			}
		} else if match.DateWindowDays != 0 || match.AmountTolerance != "" || (grouped && !approvedRule) || (!grouped && match.PolicyReference != "") {
			return nil, nil, nil, fmt.Errorf("A grouped exact match needs an approved rule; exact matches cannot carry tolerance settings.")
		}
		var statementTotal, ledgerTotal int64
		var first, last time.Time
		var sign int64
		var componentPositive, componentNegative bool
		for _, side := range []struct {
			ids   []string
			rows  map[string]Line
			used  map[string]bool
			total *int64
		}{{match.StatementIDs, byStatement, usedStatement, &statementTotal}, {match.LedgerIDs, byLedger, usedLedger, &ledgerTotal}} {
			for _, id := range side.ids {
				row, ok := side.rows[id]
				if !ok || side.used[id] {
					return nil, nil, nil, fmt.Errorf("A reviewed ID is missing or selected more than once. Refresh both sources.")
				}
				side.used[id] = true
				amount, _ := cents(row.Amount)
				if amount == 0 || (match.Method != "split" && sign != 0 && (amount > 0) != (sign > 0)) {
					return nil, nil, nil, fmt.Errorf("Reviewed groups must use one nonzero cash direction. Inspect transfers separately.")
				}
				if match.Method == "split" && len(side.ids) > 1 {
					componentPositive = componentPositive || amount > 0
					componentNegative = componentNegative || amount < 0
				}
				sign = amount
				*side.total += amount
				date, _ := time.Parse("2006-01-02", row.Date)
				if first.IsZero() || date.Before(first) {
					first = date
				}
				if last.IsZero() || date.After(last) {
					last = date
				}
			}
		}
		difference := statementTotal - ledgerTotal
		if match.Method == "split" && (!componentPositive || !componentNegative || statementTotal == 0 || ledgerTotal == 0) {
			return nil, nil, nil, fmt.Errorf("A net split needs both gross and opposite-sign adjustment components with a nonzero net.")
		}
		if difference < 0 {
			difference = -difference
		}
		if difference > tolerance || int(last.Sub(first).Hours()/24) > match.DateWindowDays {
			return nil, nil, nil, fmt.Errorf("Reviewed group exceeds its exact or approved tolerance limits.")
		}
		selected = append(selected, ConfirmedMatch{match, money(statementTotal - ledgerTotal)})
	}
	return selected, usedStatement, usedLedger, nil
}
func reviewTiming(input Input, statement, ledger []Line, usedStatement, usedLedger map[string]bool) (int64, int64, error) {
	if len(input.PriorOutstanding)+len(input.CurrentOutstanding)+len(input.Transfers) > 100 {
		return 0, 0, fmt.Errorf("Review at most 100 outstanding items and transfers per workpaper.")
	}
	statementRows, ledgerRows := map[string]Line{}, map[string]Line{}
	for _, row := range statement {
		statementRows[row.ID] = row
	}
	for _, row := range ledger {
		ledgerRows[row.ID] = row
	}
	choose := func(side, id string) (Line, error) {
		rows, used := statementRows, usedStatement
		if side == "ledger" {
			rows, used = ledgerRows, usedLedger
		} else if side != "statement" {
			return Line{}, fmt.Errorf("Choose statement or ledger for each outstanding item.")
		}
		row, ok := rows[id]
		if !ok || used[id] {
			return Line{}, fmt.Errorf("A reviewed outstanding or transfer ID is missing or already selected. Refresh the sources.")
		}
		used[id] = true
		return row, nil
	}
	var opening, closing int64
	priorIDs := map[string]bool{}
	for _, item := range input.PriorOutstanding {
		amount, err := cents(item.Amount)
		if err != nil || amount == 0 || (item.Side != "statement" && item.Side != "ledger") || strings.TrimSpace(item.ID) == "" || priorIDs[item.Side+"/"+item.ID] || len(strings.TrimSpace(item.SourceReference)) < 5 || len(item.SourceReference) > 200 || len(strings.TrimSpace(item.PolicyReference)) < 5 || len(item.PolicyReference) > 200 || len(strings.TrimSpace(item.Reason)) < 5 || len(item.Reason) > 500 {
			return 0, 0, fmt.Errorf("Each prior item needs a unique ID, signed amount, cited source, approved rule and reviewer reason.")
		}
		if err := validTreatment(item.Treatment, item.ClearingRef, input.AccountType); err != nil {
			return 0, 0, err
		}
		priorIDs[item.Side+"/"+item.ID] = true
		if item.Side == "statement" {
			amount = -amount
		}
		opening += amount
		if item.ClearedBy == "" {
			closing += amount
			continue
		}
		clearingSide := "statement"
		if item.Side == "statement" {
			clearingSide = "ledger"
		}
		row, err := choose(clearingSide, item.ClearedBy)
		if err != nil {
			return 0, 0, err
		}
		clearingAmount, _ := cents(row.Amount)
		original, _ := cents(item.Amount)
		if clearingAmount != original {
			return 0, 0, fmt.Errorf("Prior item clearing row must have the same signed amount as the cited prior item.")
		}
	}
	for _, item := range input.CurrentOutstanding {
		if len(strings.TrimSpace(item.PolicyReference)) < 5 || len(item.PolicyReference) > 200 || len(strings.TrimSpace(item.Reason)) < 5 || len(item.Reason) > 500 {
			return 0, 0, fmt.Errorf("Each current outstanding item needs an approved rule reference and reviewer reason.")
		}
		if err := validTreatment(item.Treatment, item.ClearingRef, input.AccountType); err != nil {
			return 0, 0, err
		}
		row, err := choose(item.Side, item.ID)
		if err != nil {
			return 0, 0, err
		}
		amount, _ := cents(row.Amount)
		if amount == 0 {
			return 0, 0, fmt.Errorf("A zero-amount row cannot be classified as outstanding.")
		}
		if item.Side == "statement" {
			amount = -amount
		}
		closing += amount
	}
	if len(input.Transfers) > 0 {
		if strings.TrimSpace(input.OtherAccount) == "" || strings.EqualFold(strings.TrimSpace(input.OtherAccount), strings.TrimSpace(input.Account)) || len(input.OtherAccount) > 200 || len(strings.TrimSpace(input.OtherStatementRef)) < 5 || len(input.OtherStatementRef) > 200 || len(strings.TrimSpace(input.OtherLedgerRef)) < 5 || len(input.OtherLedgerRef) > 200 {
			return 0, 0, fmt.Errorf("Transfers need a different account and both of its source references.")
		}
		otherStatement, _, err := lines(input.OtherStatementCSV, input.ClosePeriod)
		if err != nil {
			return 0, 0, fmt.Errorf("Other account statement: %w", err)
		}
		otherLedger, _, err := lines(input.OtherLedgerCSV, input.ClosePeriod)
		if err != nil {
			return 0, 0, fmt.Errorf("Other account ledger: %w", err)
		}
		otherS, otherL := map[string]Line{}, map[string]Line{}
		for _, row := range otherStatement {
			otherS[row.ID] = row
		}
		for _, row := range otherLedger {
			otherL[row.ID] = row
		}
		usedOtherS, usedOtherL := map[string]bool{}, map[string]bool{}
		for _, pair := range input.Transfers {
			if len(strings.TrimSpace(pair.PolicyReference)) < 5 || len(pair.PolicyReference) > 200 || len(strings.TrimSpace(pair.Reason)) < 5 || len(pair.Reason) > 500 {
				return 0, 0, fmt.Errorf("Transfer pairs need an approved rule reference and reviewer reason.")
			}
			if usedOtherS[pair.OtherStatementID] || usedOtherL[pair.OtherLedgerID] {
				return 0, 0, fmt.Errorf("A counterpart transfer row was selected more than once.")
			}
			os, okS := otherS[pair.OtherStatementID]
			ol, okL := otherL[pair.OtherLedgerID]
			if !okS || !okL {
				return 0, 0, fmt.Errorf("Transfer pair needs both counterpart statement and ledger rows.")
			}
			currentS, err := choose("statement", pair.StatementID)
			if err != nil {
				return 0, 0, err
			}
			currentL, err := choose("ledger", pair.LedgerID)
			if err != nil {
				return 0, 0, err
			}
			a, _ := cents(currentS.Amount)
			b, _ := cents(currentL.Amount)
			c, _ := cents(os.Amount)
			d, _ := cents(ol.Amount)
			if a == 0 || a != b || a != -c || c != d {
				return 0, 0, fmt.Errorf("All four transfer rows must carry the same absolute amount with opposite account signs.")
			}
			usedOtherS[pair.OtherStatementID], usedOtherL[pair.OtherLedgerID] = true, true
		}
	}
	if input.NativeStatus != "" && input.NativeStatus != "unverified" && input.NativeStatus != "reconciled" && input.NativeStatus != "unreconciled" {
		return 0, 0, fmt.Errorf("Choose a valid native reconciliation status.")
	}
	if input.NativeStatus != "" && input.NativeStatus != "unverified" && (len(strings.TrimSpace(input.NativeStatusRef)) < 5 || len(input.NativeStatusRef) > 200) {
		return 0, 0, fmt.Errorf("Cite the source for the observed native reconciliation status.")
	}
	return opening, closing, nil
}

func validTreatment(treatment, clearingRef, accountType string) error {
	if treatment == "" && accountType != "credit_card" && clearingRef == "" {
		return nil
	}
	if treatment != "timing" && treatment != "clearing" {
		return fmt.Errorf("Choose the reviewed timing or clearing treatment for each outstanding item.")
	}
	if treatment == "clearing" && (len(strings.TrimSpace(clearingRef)) < 5 || len(clearingRef) > 200) {
		return fmt.Errorf("A clearing treatment needs its clearing-account source reference.")
	}
	if treatment == "timing" && clearingRef != "" {
		return fmt.Errorf("A timing item cannot cite a clearing-account source.")
	}
	return nil
}

func CarryForwardItems(prior Workpaper, source string) ([]PriorItem, error) {
	if (prior.Method != "exact_bank_month_v1" && prior.Method != "card_liability_month_v1") || len(prior.Blockers) != 0 || len(strings.TrimSpace(source)) < 5 || len(source) > 200 {
		return nil, fmt.Errorf("Choose an approved, complete prior reconciliation and cite its workpaper.")
	}
	statement, _, err := lines(prior.Input.StatementCSV, prior.Input.ClosePeriod)
	if err != nil {
		return nil, err
	}
	ledger, _, err := lines(prior.Input.LedgerCSV, prior.Input.ClosePeriod)
	if err != nil {
		return nil, err
	}
	rows := map[string]map[string]Line{"statement": {}, "ledger": {}}
	for _, row := range statement {
		rows["statement"][row.ID] = row
	}
	for _, row := range ledger {
		rows["ledger"][row.ID] = row
	}
	items := []PriorItem{}
	seen := map[string]bool{}
	for _, item := range prior.Input.PriorOutstanding {
		if item.ClearedBy != "" {
			continue
		}
		key := item.Side + "/" + item.ID
		if seen[key] {
			return nil, fmt.Errorf("Prior outstanding IDs overlap; review them before carry-forward.")
		}
		seen[key] = true
		items = append(items, item)
	}
	for _, item := range prior.Input.CurrentOutstanding {
		key := item.Side + "/" + item.ID
		row, ok := rows[item.Side][item.ID]
		if !ok || seen[key] {
			return nil, fmt.Errorf("Current outstanding row changed or overlaps a prior item.")
		}
		seen[key] = true
		items = append(items, PriorItem{Side: item.Side, ID: item.ID, Amount: row.Amount, Treatment: item.Treatment, ClearingRef: item.ClearingRef, SourceReference: source, PolicyReference: item.PolicyReference, Reason: "Carried from approved prior close; confirm whether it cleared this month."})
	}
	return items, nil
}
func Prepare(input Input) (Workpaper, error) {
	result := Workpaper{Status: "draft", Method: "exact_bank_month_v1", Input: input, Matches: []Match{}, ReviewedMatches: []ConfirmedMatch{}, UnmatchedStatement: []Line{}, UnmatchedLedger: []Line{}, Exceptions: []Exception{}, Blockers: []string{}}
	fail := func(message string) (Workpaper, error) { return Workpaper{}, fmt.Errorf("%s", message) }
	for _, value := range []string{input.Entity, input.Account, input.StatementReference, input.LedgerReference} {
		if strings.TrimSpace(value) == "" || len(value) > 200 {
			return fail("Entity, account and both source references require 1–200 characters.")
		}
	}
	if !ledger.ValidCurrency(input.Currency) || !input.PolicyConfirmed || (input.AccountType != "" && input.AccountType != "bank" && input.AccountType != "credit_card") {
		return fail("Confirm the account method in the client's home currency, complete month boundaries and signed movements.")
	}
	if input.AccountType == "credit_card" {
		if len(strings.TrimSpace(input.CardRuleReference)) < 5 || len(input.CardRuleReference) > 200 {
			return fail("Card reconciliation needs the client accountant's approved liability-sign rule reference.")
		}
		result.Method = "card_liability_month_v1"
	} else if input.CardRuleReference != "" {
		return fail("A bank reconciliation cannot use a card liability rule.")
	}
	if _, err := time.Parse("2006-01", input.ClosePeriod); err != nil {
		return fail("Choose a valid close month.")
	}
	if input.ExpectedPages < 1 || input.ExpectedPages > 1000 || len(input.Pages) > 1000 {
		return fail("Provide an expected statement page count between 1 and 1000.")
	}
	seen := map[int]bool{}
	for _, page := range input.Pages {
		if page < 1 || page > input.ExpectedPages || seen[page] {
			return fail("Statement page numbers must be unique and within the expected page count.")
		}
		seen[page] = true
	}
	for page := 1; page <= input.ExpectedPages; page++ {
		if !seen[page] {
			result.Blockers = append(result.Blockers, fmt.Sprintf("Statement page %d is missing.", page))
		}
	}
	balances := []int64{}
	for _, value := range []string{input.StatementOpening, input.StatementClosing, input.LedgerOpening, input.LedgerClosing} {
		n, err := cents(value)
		if err != nil {
			return fail(err.Error())
		}
		balances = append(balances, n)
	}
	statement, stotal, err := lines(input.StatementCSV, input.ClosePeriod)
	if err != nil {
		return fail("Statement: " + err.Error())
	}
	ledger, ltotal, err := lines(input.LedgerCSV, input.ClosePeriod)
	if err != nil {
		return fail("Ledger: " + err.Error())
	}
	confirmed, usedStatement, usedLedger, err := reviewMatches(input.ReviewedMatches, statement, ledger)
	if err != nil {
		return fail(err.Error())
	}
	result.ReviewedMatches = confirmed
	for _, match := range confirmed {
		if match.AmountDifference != "0.00" {
			result.Blockers = append(result.Blockers, "A reviewed tolerance group still has a signed amount difference; investigate or prepare an authorized correction.")
		}
	}
	openingItems, closingItems, err := reviewTiming(input, statement, ledger, usedStatement, usedLedger)
	if err != nil {
		return fail(err.Error())
	}
	sd := balances[0] + stotal - balances[1]
	ld := balances[2] + ltotal - balances[3]
	result.StatementRollForwardDifference = money(sd)
	result.LedgerRollForwardDifference = money(ld)
	result.OpeningDifference = money(balances[0] - balances[2])
	result.ClosingDifference = money(balances[1] - balances[3])
	result.OpeningBridgeDifference = money(balances[0] + openingItems - balances[2])
	result.ClosingBridgeDifference = money(balances[1] + closingItems - balances[3])
	if sd != 0 {
		result.Blockers = append(result.Blockers, "Statement opening balance plus imported movement does not equal its closing balance.")
	}
	if ld != 0 {
		result.Blockers = append(result.Blockers, "Ledger opening balance plus imported movement does not equal its closing balance.")
	}
	if balances[0]+openingItems != balances[2] {
		result.Blockers = append(result.Blockers, "Opening bridge does not tie; inspect cited prior outstanding items.")
	}
	if balances[1]+closingItems != balances[3] {
		result.Blockers = append(result.Blockers, "Closing bridge does not tie; inspect current and uncleared prior items.")
	}
	key := func(line Line) string { return line.Date + "/" + line.Amount }
	scount := map[string]int{}
	lcount := map[string]int{}
	lindex := map[string]Line{}
	for _, line := range statement {
		if usedStatement[line.ID] {
			continue
		}
		scount[key(line)]++
	}
	for _, line := range ledger {
		if usedLedger[line.ID] {
			continue
		}
		lcount[key(line)]++
		lindex[key(line)] = line
	}
	used := map[string]bool{}
	for _, line := range statement {
		if usedStatement[line.ID] {
			continue
		}
		k := key(line)
		if scount[k] == 1 && lcount[k] == 1 {
			peer := lindex[k]
			result.Matches = append(result.Matches, Match{line.ID, peer.ID, "Unique same-date, same signed amount; candidate requires review."})
			used[peer.ID] = true
		} else {
			result.UnmatchedStatement = append(result.UnmatchedStatement, line)
		}
	}
	for _, line := range ledger {
		if !used[line.ID] && !usedLedger[line.ID] {
			result.UnmatchedLedger = append(result.UnmatchedLedger, line)
		}
	}
	result.Exceptions = append(result.Exceptions, investigate("statement", result.UnmatchedStatement, result.UnmatchedLedger)...)
	result.Exceptions = append(result.Exceptions, investigate("ledger", result.UnmatchedLedger, result.UnmatchedStatement)...)
	if len(result.UnmatchedStatement)+len(result.UnmatchedLedger) > 0 {
		result.Blockers = append(result.Blockers, "Unmatched or ambiguous transactions remain. No timing classification or balancing entry has been inferred.")
	}
	return result, nil
}

// These hints identify rows worth comparing; they never turn them into matches.
func investigate(side string, lines, peers []Line) []Exception {
	byExact, byAmount, byDate, byID := map[string][]string{}, map[string][]string{}, map[string][]string{}, map[string][]string{}
	type amountRow struct {
		ID     string
		Amount int64
	}
	dayRows := map[string][]amountRow{}
	dayAmounts := map[string]map[int64][]int{}
	for _, peer := range peers {
		byExact[peer.Date+"/"+peer.Amount] = append(byExact[peer.Date+"/"+peer.Amount], peer.ID)
		byAmount[peer.Amount] = append(byAmount[peer.Amount], peer.ID)
		byDate[peer.Date] = append(byDate[peer.Date], peer.ID)
		byID[peer.ID] = append(byID[peer.ID], peer.ID)
		if amount, err := cents(peer.Amount); err == nil {
			if dayAmounts[peer.Date] == nil {
				dayAmounts[peer.Date] = map[int64][]int{}
			}
			dayAmounts[peer.Date][amount] = append(dayAmounts[peer.Date][amount], len(dayRows[peer.Date]))
			dayRows[peer.Date] = append(dayRows[peer.Date], amountRow{peer.ID, amount})
		}
	}
	result := make([]Exception, 0, len(lines))
	for _, line := range lines {
		item := Exception{Side: side, ID: line.ID, Kind: "no_counterpart", CandidateIDs: []string{}, Question: "Find the supporting transaction or document; determine whether this item belongs in the close period."}
		candidates := byExact[line.Date+"/"+line.Amount]
		if len(byID[line.ID]) > 0 && len(candidates) == 0 {
			candidates = byID[line.ID]
			item.Kind, item.Question = "same_id_different_detail", "The same transaction ID has a different date or amount. Compare the original records before resolving it."
		} else if len(candidates) > 0 {
			item.Kind, item.Question = "ambiguous_exact", "The exact date and amount is not unique across the imported rows. Check transaction identity before choosing a match."
		} else if candidates = byAmount[line.Amount]; len(candidates) > 0 {
			item.Kind, item.Question = "same_amount_other_date", "Same signed amount appears on another date. Check timing and transaction identity; do not assume a match."
		} else if amount, err := cents(line.Amount); err == nil && amount != 0 && len(byAmount[money(-amount)]) > 0 {
			candidates = byAmount[money(-amount)]
			item.Kind, item.Question = "opposite_sign", "The amount appears with the opposite sign. Check sign convention, reversal and source identity; do not assume an offset."
		} else if candidates = byDate[line.Date]; len(candidates) > 0 {
			item.Kind, item.Question = "same_date_other_amount", "Another amount appears on this date. Check source amounts and transaction identity; do not assume a correction."
		}
		item.CandidateCount = len(candidates)
		if len(candidates) > 5 {
			candidates = candidates[:5]
		}
		item.CandidateIDs = append(item.CandidateIDs, candidates...)
		if target, err := cents(line.Amount); err == nil && target != 0 {
			// ponytail: the 2,000-row source limit bounds this pair search; index larger sources if the limit grows.
			for i, peer := range dayRows[line.Date] {
				amount := peer.Amount
				other := target - amount
				if amount == 0 || other == 0 {
					continue
				}
				group := (amount > 0) == (other > 0)
				indices := dayAmounts[line.Date][other]
				start := sort.SearchInts(indices, i+1)
				if group {
					item.GroupCount += len(indices) - start
				} else {
					item.SplitCount += len(indices) - start
				}
				for _, j := range indices[start:] {
					if group {
						if len(item.CandidateGroups) == 5 {
							break
						}
						item.CandidateGroups = append(item.CandidateGroups, []string{peer.ID, dayRows[line.Date][j].ID})
					} else {
						if len(item.SplitGroups) == 5 {
							break
						}
						item.SplitGroups = append(item.SplitGroups, []string{peer.ID, dayRows[line.Date][j].ID})
					}
				}
			}
			if item.GroupCount > 0 && item.Kind == "same_date_other_amount" {
				item.Kind = "grouped_exact"
				item.Question = "Two same-date, same-sign rows sum to this amount. Inspect all original transactions before relying on a grouping; no match is confirmed."
			} else if item.SplitCount > 0 && item.Kind == "same_date_other_amount" {
				item.Kind = "split_settlement"
				item.Question = "Gross and opposite-sign adjustment rows net to this amount. Inspect the settlement and fee records under an approved rule; no match is confirmed."
			}
		}
		result = append(result, item)
	}
	return result
}
