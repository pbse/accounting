package bookfiles

import (
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Meaning is what a column of a Trial Balance holds (docs/books-sources.md, Values): a column's meaning is confirmed,
// never inferred from its position alone.
type Meaning string

const (
	AccountNumber Meaning = "account_number"
	AccountName   Meaning = "account_name"
	AccountType   Meaning = "account_type"
	Debit         Meaning = "debit"
	Credit        Meaning = "credit"
	// Balance is one signed month-end balance; BalanceSign says which side is positive.
	Balance Meaning = "balance"
	// Opening, Movement and YearToDate are other balances a file can carry beside the month-end one.
	Opening    Meaning = "opening"
	Movement   Meaning = "movement"
	YearToDate Meaning = "year_to_date"
)

// Layout is how to read one table as a Trial Balance: which row heads it, what each column means, how its amounts are
// written, and as of when and in what currency it is. The reader proposes one; a person confirms it, and the confirmed
// layout is saved for the client and proposed again next month.
type Layout struct {
	Table     string          `json:"table"`
	HeaderRow int             `json:"header_row"`
	Columns   map[int]Meaning `json:"columns"`
	Style     NumberStyle     `json:"style"`
	// BalanceSign is debit (a positive Balance is a debit, the default) or credit.
	BalanceSign string `json:"balance_sign,omitempty"`
	AsOf        string `json:"as_of"`
	Currency    string `json:"currency"`
	// Headings are rows a person confirmed are headings though nothing totals them; any other row with an account and
	// no amounts is an account with a zero balance. Totals are rows a person confirmed are totals though not named so;
	// a total is otherwise known by its name ("Total …"), never by its amount being a formula.
	Headings []int `json:"headings,omitempty"`
	Totals   []int `json:"totals,omitempty"`
}

// headerWords names what each column header usually says, for a proposal.
var headerWords = []struct {
	meaning Meaning
	words   *regexp.Regexp
}{
	{AccountNumber, regexp.MustCompile(`(?i)^(account\s*)?(no\.?|number|code|#|nominal code|acct\.?\s*(no|#)?)$`)},
	{AccountType, regexp.MustCompile(`(?i)^(account\s*)?(type|class|category)$`)},
	{AccountName, regexp.MustCompile(`(?i)^(account(\s*name)?|name|description|ledger|particulars|account title)$`)},
	{Opening, regexp.MustCompile(`(?i)opening|beginning|brought forward|b/f`)},
	{Movement, regexp.MustCompile(`(?i)movement|activity|net change|period|this month`)},
	{YearToDate, regexp.MustCompile(`(?i)ytd|year[\s-]*to[\s-]*date`)},
	{Debit, regexp.MustCompile(`(?i)^(debit|dr\.?|debits)$`)},
	{Credit, regexp.MustCompile(`(?i)^(credit|cr\.?|credits)$`)},
	{Balance, regexp.MustCompile(`(?i)^(closing\s*)?balance|^net$|^amount$|^total$|closing`)},
}

// ProposeLayout guesses how to read a table as a Trial Balance: the first of its first 30 rows whose cells name at
// least two Trial Balance columns heads it, each column's meaning comes from its header, and the number style from the
// amounts beneath. It returns what a person must still confirm, said as questions; a table with no header it can find
// is left to them whole.
func ProposeLayout(t Table) (Layout, []string) {
	layout := Layout{Table: t.Name, Columns: map[int]Meaning{}, Style: NumberStyle{Scale: 1}}
	for i, r := range t.Rows {
		if i >= 30 {
			break
		}
		columns := map[int]Meaning{}
		for _, c := range r.Cells {
			text := strings.TrimSpace(c.Value)
			for _, h := range headerWords {
				if h.words.MatchString(text) {
					columns[c.Column] = h.meaning
					break
				}
			}
		}
		if len(columns) >= 2 {
			layout.HeaderRow, layout.Columns = r.Number, columns
			break
		}
	}
	questions := []string{}
	if layout.HeaderRow == 0 {
		return layout, append(questions, "Which row heads the Trial Balance, and what does each column hold?")
	}
	has := func(m Meaning) bool {
		for _, v := range layout.Columns {
			if v == m {
				return true
			}
		}
		return false
	}
	if !has(AccountName) && !has(AccountNumber) {
		// An export that leaves the account column's header empty (QuickBooks Online's does) usually has the accounts
		// in its first column with text beneath the header: proposed, and asked about.
		first := -1
		for _, r := range t.Rows {
			if r.Number <= layout.HeaderRow {
				continue
			}
			for _, c := range r.Cells {
				if _, taken := layout.Columns[c.Column]; !taken && c.Kind != Number && (first < 0 || c.Column < first) {
					if _, err := ParseAmount(c.Value, layout.Style); err != nil {
						first = c.Column
					}
				}
			}
		}
		if first >= 0 {
			layout.Columns[first] = AccountName
			questions = append(questions, fmt.Sprintf("Does column %s name the account?", strings.TrimRight(cellRef(first, 1), "1")))
		} else {
			questions = append(questions, "Which column names the account?")
		}
	}
	if !(has(Debit) && has(Credit)) && !has(Balance) {
		questions = append(questions, "Which columns hold the month-end balance: debit and credit, or one signed balance?")
	}
	if has(Balance) && !(has(Debit) && has(Credit)) {
		layout.BalanceSign = "debit"
		questions = append(questions, "Is a positive balance a debit?")
	}
	var amounts []string
	for _, r := range t.Rows {
		if r.Number <= layout.HeaderRow {
			continue
		}
		for _, c := range r.Cells {
			if m := layout.Columns[c.Column]; m == Debit || m == Credit || m == Balance {
				amounts = append(amounts, c.Value)
			}
		}
	}
	style, unsure := GuessNumberStyle(amounts)
	layout.Style = style
	if unsure {
		questions = append(questions, "Are the amounts written with a decimal point (1,234.56) or a decimal comma (1.234,56)?")
	}
	questions = append(questions, "As of which date is this Trial Balance, and in which currency?", "Are the amounts in units, or in thousands?")
	return layout, questions
}

// RowKind is what a row of a Trial Balance is: an account's posting row, a total of the rows above it (a parent's, a
// section's, or the whole Trial Balance's), or a heading with no amounts.
type RowKind string

const (
	Posting RowKind = "posting"
	Total   RowKind = "total"
	Heading RowKind = "heading"
)

// Line is one row of a Trial Balance as read: the account, its month-end balance (debit minus credit), what kind of
// row it is, and where it came from.
type Line struct {
	Row     int      `json:"row"`
	Ref     string   `json:"ref"`
	Kind    RowKind  `json:"kind"`
	Number  string   `json:"number,omitempty"`
	Name    string   `json:"name"`
	Type    string   `json:"type,omitempty"`
	Balance *big.Rat `json:"-"`
	// Debit and Credit are the row's two columns as written, in a two-column layout, for checking totals by column.
	Debit, Credit *big.Rat `json:"-"`
	// Amount is Balance as an exact decimal, for storing.
	Amount string `json:"balance"`
	// Other balances the file carries for the account, by meaning.
	Others map[Meaning]string `json:"others,omitempty"`
	Cents  bool               `json:"cents"`
	Hidden bool               `json:"hidden,omitempty"`
}

// Reading is a table read with a confirmed layout: its lines, and the checks that hold what depends on them.
type Reading struct {
	Lines []Line `json:"lines"`
	// Debits and Credits are the posting rows' totals, each side's sum; Balanced says they agree to the cent.
	Debits   string `json:"debits"`
	Credits  string `json:"credits"`
	Balanced bool   `json:"balanced"`
	// Problems are what holds the reading from use as the month's books, said plainly with where each is.
	Problems []string `json:"problems"`
	// Notes are how the reader read what it could have read another way, for a person to confirm.
	Notes []string `json:"notes"`
}

var totalName = regexp.MustCompile(`(?i)^(grand\s+)?total\b|\btotals?$`)

// totalLabel is what a total row totals: "Total Current Assets" totals "Current Assets"; "Total", "TOTAL" and
// "Grand total" total everything.
var totalLabel = regexp.MustCompile(`(?i)^(grand\s+)?totals?\b[\s:-]*|[\s:-]*\btotals?$`)

// checkTotal checks the total at lines[i] against what it covers, and says how it differs. A total named for a heading
// or a parent account above it ("Total Current Assets" under "Current Assets") covers the posting rows from there,
// nested sections included; a parent account's own balance counts with its children's. A grand total covers every
// posting row, and, written as two columns, each column must equal its side. A total whose label matches nothing
// covers the posting rows since the total before it.
func checkTotal(lines []Line, i int, debit, credit *big.Rat) []string {
	t := lines[i]
	label := strings.ToLower(strings.TrimSpace(totalLabel.ReplaceAllString(t.Name, "")))
	sum := func(from int) *big.Rat {
		covered := new(big.Rat)
		for _, l := range lines[from:i] {
			if l.Kind == Posting {
				covered.Add(covered, l.Balance)
			}
		}
		return covered
	}
	if label == "" {
		debits, credits := new(big.Rat), new(big.Rat)
		for _, l := range lines[:i] {
			if l.Kind == Posting && l.Balance.Sign() > 0 {
				debits.Add(debits, l.Balance)
			} else if l.Kind == Posting {
				credits.Sub(credits, l.Balance)
			}
		}
		if debit != nil && credit != nil {
			debits, credits = columnSums(lines[:i])
			if debit.Cmp(debits) != 0 || credit.Cmp(credits) != 0 {
				return []string{fmt.Sprintf("Row %d (%s) shows debits of %s and credits of %s, but the rows above come to %s and %s", t.Row, t.Name, exact(debit), exact(credit), exact(debits), exact(credits))}
			}
			return nil
		}
		if net := new(big.Rat).Sub(debits, credits); net.Cmp(t.Balance) != 0 {
			return []string{fmt.Sprintf("Row %d (%s) is %s, but the rows above come to %s", t.Row, t.Name, exact(t.Balance), exact(net))}
		}
		return nil
	}
	from := -1
	for j := i - 1; j >= 0 && from < 0; j-- {
		if (lines[j].Kind == Heading || lines[j].Kind == Posting) && strings.EqualFold(strings.TrimSpace(lines[j].Name), label) {
			from = j
		}
	}
	if from < 0 {
		from = 0
		for j := i - 1; j >= 0; j-- {
			if lines[j].Kind == Total {
				from = j + 1
				break
			}
		}
	}
	if debit != nil && credit != nil {
		// Written as two columns, each column must equal its column's sum over the rows it totals.
		debits, credits := columnSums(lines[from:i])
		if debit.Cmp(debits) != 0 || credit.Cmp(credits) != 0 {
			return []string{fmt.Sprintf("Row %d (%s) shows debits of %s and credits of %s, but the rows it totals come to %s and %s", t.Row, t.Name, exact(debit), exact(credit), exact(debits), exact(credits))}
		}
		return nil
	}
	if covered := sum(from); covered.Cmp(t.Balance) != 0 {
		return []string{fmt.Sprintf("Row %d (%s) is %s, but the rows it totals come to %s", t.Row, t.Name, exact(t.Balance), exact(covered))}
	}
	return nil
}

// Read applies a confirmed layout to its table: each row below the header becomes a line, a total (a row named
// "Total …", or whose amount is a formula that sums the rows above) is checked against the posting rows it covers and
// then left out of the balances, and every amount is read exactly in the layout's style. Nothing is dropped: a value
// that cannot be read is a problem with its location.
func (l Layout) Read(t Table) Reading {
	reading := Reading{Lines: []Line{}, Problems: []string{}, Notes: []string{}}
	// The month-end balance is what a Trial Balance is read for: a layout naming no column for it reads nothing, rather
	// than every account as zero.
	meanings := map[Meaning]bool{}
	for _, m := range l.Columns {
		meanings[m] = true
	}
	if !meanings[Balance] && !(meanings[Debit] && meanings[Credit]) {
		reading.Problems = append(reading.Problems, "The layout names no month-end balance: choose the debit and credit columns, or the one signed balance column")
		return reading
	}
	// The names a total row totals ("Total Current Assets" totals "Current Assets"): a row by such a name with no amounts
	// heads what the total covers.
	totalled := map[string]bool{}
	for _, r := range t.Rows {
		for _, c := range r.Cells {
			if l.Columns[c.Column] == AccountName && totalName.MatchString(strings.TrimSpace(c.Value)) {
				totalled[strings.ToLower(strings.TrimSpace(totalLabel.ReplaceAllString(strings.TrimSpace(c.Value), "")))] = true
			}
		}
	}
	debits, credits := new(big.Rat), new(big.Rat)
	var columns []int
	for c := range l.Columns {
		columns = append(columns, c)
	}
	sort.Ints(columns)
	for _, r := range t.Rows {
		if r.Number <= l.HeaderRow {
			continue
		}
		line := Line{Row: r.Number, Hidden: r.Hidden, Others: map[Meaning]string{}, Cents: true}
		var debit, credit, balance *big.Rat
		hasAmount, signed := false, false
		for _, c := range r.Cells {
			meaning, ok := l.Columns[c.Column]
			if !ok {
				continue
			}
			if line.Ref == "" {
				line.Ref = c.Ref
			}
			switch meaning {
			case AccountNumber:
				line.Number = strings.TrimSpace(c.Value)
			case AccountName:
				line.Name = strings.TrimSpace(c.Value)
			case AccountType:
				line.Type = strings.TrimSpace(c.Value)
			default:
				a, err := ParseAmount(c.Value, l.Style)
				if err != nil {
					reading.Problems = append(reading.Problems, fmt.Sprintf("%s: %q is not an amount", c.Ref, c.Value))
					continue
				}
				hasAmount = true
				line.Cents = line.Cents && a.Cents
				// A Dr or Cr marker says the side itself, whichever column the amount sits in: its value is then already
				// signed as balances are, a credit negative.
				switch {
				case a.Side != "" && (meaning == Debit || meaning == Credit):
					if a.Value.Sign() >= 0 {
						debit = a.Value
					} else {
						credit = new(big.Rat).Neg(a.Value)
					}
				case meaning == Debit:
					debit = a.Value
				case meaning == Credit:
					credit = a.Value
				case meaning == Balance:
					balance = a.Value
					signed = a.Side != ""
				default:
					line.Others[meaning] = exact(a.Value)
				}
			}
		}
		if line.Name == "" && line.Number == "" && !hasAmount {
			continue
		}
		switch {
		case balance != nil:
			line.Balance = new(big.Rat).Set(balance)
			if l.BalanceSign == "credit" && !signed {
				line.Balance.Neg(line.Balance)
			}
		case debit != nil || credit != nil:
			line.Balance, line.Debit, line.Credit = new(big.Rat), new(big.Rat), new(big.Rat)
			if debit != nil {
				line.Balance.Add(line.Balance, debit)
				line.Debit.Set(debit)
			}
			if credit != nil {
				line.Balance.Sub(line.Balance, credit)
				line.Credit.Set(credit)
			}
		}
		// A row with no amounts heads a section when a total names it or a person said it does; otherwise it is an
		// account with a zero balance (an export leaves a zero blank), said so when it has no number to show it is one.
		if line.Balance == nil && !totalName.MatchString(line.Name) && !totalled[strings.ToLower(line.Name)] && !slices.Contains(l.Headings, r.Number) {
			line.Balance = new(big.Rat)
			if line.Number == "" {
				reading.Notes = append(reading.Notes, fmt.Sprintf("Row %d (%s) has no amounts: read as an account with a zero balance; mark it a heading if it is one", r.Number, line.Name))
			}
		}
		switch {
		case line.Balance == nil:
			line.Kind = Heading
		case totalName.MatchString(line.Name) || slices.Contains(l.Totals, r.Number):
			line.Kind = Total
		default:
			line.Kind = Posting
		}
		if line.Balance != nil {
			line.Amount = exact(line.Balance)
		}
		reading.Lines = append(reading.Lines, line)
		i := len(reading.Lines) - 1
		switch line.Kind {
		case Posting:
			if line.Name == "" && line.Number == "" {
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d has amounts but no account", r.Number))
			}
			if !line.Cents {
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d is not exact to the cent", r.Number))
			}
			if line.Balance.Sign() > 0 {
				debits.Add(debits, line.Balance)
			} else {
				credits.Sub(credits, line.Balance)
			}
		case Total:
			reading.Problems = append(reading.Problems, checkTotal(reading.Lines, i, debit, credit)...)
		}
	}
	reading.Debits, reading.Credits = exact(debits), exact(credits)
	reading.Balanced = debits.Cmp(credits) == 0
	if !reading.Balanced {
		reading.Problems = append(reading.Problems, fmt.Sprintf("Debits (%s) do not equal credits (%s)", exact(debits), exact(credits)))
	}
	if !slices.ContainsFunc(reading.Lines, func(l Line) bool { return l.Kind == Posting }) {
		reading.Problems = append(reading.Problems, "No account rows were found under the header")
	}
	return reading
}

// exact writes an amount exactly: as a decimal when it has one, else as a fraction.
func exact(r *big.Rat) string {
	for places := 0; places <= 12; places++ {
		scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)))
		if scaled.IsInt() {
			return r.FloatString(places)
		}
	}
	return r.RatString()
}

// columnSums is the posting rows' debit and credit columns summed as written (a row in a signed-balance layout counts
// on its balance's side).
func columnSums(lines []Line) (*big.Rat, *big.Rat) {
	debits, credits := new(big.Rat), new(big.Rat)
	for _, l := range lines {
		switch {
		case l.Kind != Posting:
		case l.Debit != nil || l.Credit != nil:
			if l.Debit != nil {
				debits.Add(debits, l.Debit)
			}
			if l.Credit != nil {
				credits.Add(credits, l.Credit)
			}
		case l.Balance.Sign() > 0:
			debits.Add(debits, l.Balance)
		default:
			credits.Sub(credits, l.Balance)
		}
	}
	return debits, credits
}
