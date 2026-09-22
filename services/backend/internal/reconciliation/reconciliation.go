package reconciliation

import (
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Input struct {
	Entity             string `json:"entity"`
	Account            string `json:"account"`
	ClosePeriod        string `json:"close_period"`
	Currency           string `json:"currency"`
	StatementReference string `json:"statement_reference"`
	LedgerReference    string `json:"ledger_reference"`
	ExpectedPages      int    `json:"expected_pages"`
	Pages              []int  `json:"pages"`
	StatementOpening   string `json:"statement_opening"`
	StatementClosing   string `json:"statement_closing"`
	LedgerOpening      string `json:"ledger_opening"`
	LedgerClosing      string `json:"ledger_closing"`
	StatementCSV       string `json:"statement_csv"`
	LedgerCSV          string `json:"ledger_csv"`
	PolicyConfirmed    bool   `json:"policy_confirmed"`
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
type Workpaper struct {
	Status                         string   `json:"status"`
	Method                         string   `json:"method"`
	Input                          Input    `json:"input"`
	Matches                        []Match  `json:"matches"`
	UnmatchedStatement             []Line   `json:"unmatched_statement"`
	UnmatchedLedger                []Line   `json:"unmatched_ledger"`
	Blockers                       []string `json:"blockers"`
	StatementRollForwardDifference string   `json:"statement_roll_forward_difference"`
	LedgerRollForwardDifference    string   `json:"ledger_roll_forward_difference"`
	OpeningDifference              string   `json:"opening_difference"`
	ClosingDifference              string   `json:"closing_difference"`
}

var amountPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,9})(\.[0-9]{1,2})?$`)

func cents(value string) (int64, error) {
	if !amountPattern.MatchString(value) {
		return 0, fmt.Errorf("amounts must be USD decimals with at most two fractional digits and at most 10 whole digits")
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
		if err != nil || date.Format("2006-01") != period {
			return nil, 0, fmt.Errorf("transaction dates must be within the close month")
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
func Prepare(input Input) (Workpaper, error) {
	result := Workpaper{Status: "draft", Method: "exact_bank_month_v1", Input: input, Matches: []Match{}, UnmatchedStatement: []Line{}, UnmatchedLedger: []Line{}, Blockers: []string{}}
	fail := func(message string) (Workpaper, error) { return Workpaper{}, fmt.Errorf("%s", message) }
	for _, value := range []string{input.Entity, input.Account, input.StatementReference, input.LedgerReference} {
		if strings.TrimSpace(value) == "" || len(value) > 200 {
			return fail("Entity, account and both source references require 1–200 characters.")
		}
	}
	if input.Currency != "USD" || !input.PolicyConfirmed {
		return fail("Confirm the USD bank-account method, complete month boundaries and signed cash amounts.")
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
	sd := balances[0] + stotal - balances[1]
	ld := balances[2] + ltotal - balances[3]
	result.StatementRollForwardDifference = money(sd)
	result.LedgerRollForwardDifference = money(ld)
	result.OpeningDifference = money(balances[0] - balances[2])
	result.ClosingDifference = money(balances[1] - balances[3])
	if sd != 0 {
		result.Blockers = append(result.Blockers, "Statement opening balance plus imported movement does not equal its closing balance.")
	}
	if ld != 0 {
		result.Blockers = append(result.Blockers, "Ledger opening balance plus imported movement does not equal its closing balance.")
	}
	if balances[0] != balances[2] {
		result.Blockers = append(result.Blockers, "Opening balances differ; prior-period outstanding items require review.")
	}
	if balances[1] != balances[3] {
		result.Blockers = append(result.Blockers, "Closing balances differ; investigate unmatched items and timing differences.")
	}
	key := func(line Line) string { return line.Date + "/" + line.Amount }
	scount := map[string]int{}
	lcount := map[string]int{}
	lindex := map[string]Line{}
	for _, line := range statement {
		scount[key(line)]++
	}
	for _, line := range ledger {
		lcount[key(line)]++
		lindex[key(line)] = line
	}
	used := map[string]bool{}
	for _, line := range statement {
		k := key(line)
		// ponytail: unique exact candidates only; grouping and date tolerances need a separately confirmed policy.
		if scount[k] == 1 && lcount[k] == 1 {
			peer := lindex[k]
			result.Matches = append(result.Matches, Match{line.ID, peer.ID, "Unique same-date, same signed amount; candidate requires review."})
			used[peer.ID] = true
		} else {
			result.UnmatchedStatement = append(result.UnmatchedStatement, line)
		}
	}
	for _, line := range ledger {
		if !used[line.ID] {
			result.UnmatchedLedger = append(result.UnmatchedLedger, line)
		}
	}
	if len(result.UnmatchedStatement)+len(result.UnmatchedLedger) > 0 {
		result.Blockers = append(result.Blockers, "Unmatched or ambiguous transactions remain. No timing classification or balancing entry has been inferred.")
	}
	return result, nil
}
