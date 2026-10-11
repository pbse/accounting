package bookfiles

import (
	"cmp"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
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
	// Dimension is the department, location or class a row's balance belongs to: an account repeated once per dimension
	// is one account whose balance is the sum, the split kept on its lines (docs/books-sources.md, Hierarchy, dimensions
	// and entities).
	Dimension Meaning = "dimension"
	// Date, Reference, Memo and Party are a General Ledger's: a layout naming a date column reads each posting row as a
	// line of the month's transactions (its date, the transaction's number, its description and the payee), its amount
	// the line's, not an account's month-end balance.
	LineDate  Meaning = "date"
	Reference Meaning = "reference"
	Memo      Meaning = "memo"
	Party     Meaning = "party"
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
	// Postings are rows a person confirmed are accounts though named like totals ("Total Rewards").
	Postings []int `json:"postings,omitempty"`
	// Ignored are rows a person confirmed are not part of the Trial Balance (a footer, a note) though they hold values
	// outside the layout's columns; any other such row is a problem. IgnoredColumns are columns a person confirmed hold
	// nothing of it (a memo, last year's figures); amounts in any other column the layout does not name are a problem.
	Ignored        []int `json:"ignored,omitempty"`
	IgnoredColumns []int `json:"ignored_columns,omitempty"`
	// Headers are the header row's text over each column the layout names or leaves out, kept when it is confirmed: a
	// later file is read with the same layout without a question only when its headers say the same.
	Headers map[int]string `json:"headers,omitempty"`
	// DateOrder is how a ledger's dates written in numbers read (YMD, MDY or DMY), confirmed when they could read two
	// ways.
	DateOrder string `json:"date_order,omitempty"`
}

// Ledger says the layout reads a General Ledger's lines, not a Trial Balance's month-end balances: it names a date.
func (l Layout) Ledger() bool {
	for _, m := range l.Columns {
		if m == LineDate {
			return true
		}
	}
	return false
}

// headerWords names what each column header usually says, for a proposal.
var headerWords = []struct {
	meaning Meaning
	words   *regexp.Regexp
}{
	{AccountNumber, regexp.MustCompile(`(?i)^(account\s*)?(no\.?|number|code|#|nominal code|acct\.?\s*(no|#)?)$`)},
	{AccountType, regexp.MustCompile(`(?i)^(account\s*)?(type|class|category)$`)},
	{Dimension, regexp.MustCompile(`(?i)^(department|dept\.?|location|division|tracking(\s*category)?|cost\s*cent(re|er)|segment|branch|region)$`)},
	{AccountName, regexp.MustCompile(`(?i)^(account(\s*name)?|name|description|ledger|particulars|account title)$`)},
	{Opening, regexp.MustCompile(`(?i)opening|beginning|brought forward|b/f`)},
	{Movement, regexp.MustCompile(`(?i)movement|activity|net change|period|this month`)},
	{YearToDate, regexp.MustCompile(`(?i)ytd|year[\s-]*to[\s-]*date`)},
	{Debit, regexp.MustCompile(`(?i)^(debit|dr\.?|debits)$`)},
	{Credit, regexp.MustCompile(`(?i)^(credit|cr\.?|credits)$`)},
	{Balance, regexp.MustCompile(`(?i)^(closing\s*)?balance|^net$|^amount$|^total$|closing`)},
}

// ledgerWords name a General Ledger's own columns, read before headerWords in a header row that has a date: there,
// "Name" is the payee and "Description" the line's, not the account's, and "Num" the transaction's number.
var ledgerWords = []struct {
	meaning Meaning
	words   *regexp.Regexp
}{
	{LineDate, regexp.MustCompile(`(?i)^(date|txn\.?\s*date|transaction\s*date|posting\s*date|entry\s*date|gl\s*date|journal\s*date|doc(ument)?\s*date)$`)},
	{Reference, regexp.MustCompile(`(?i)^(num|no\.?|#|ref\.?|reference|doc(ument)?\s*(no\.?|number|#)|entry\s*(no\.?|number|#)|journal\s*(no\.?|number|#|id)|transaction\s*(id|no\.?|number|#)|txn\s*(id|no\.?|number|#)|voucher(\s*(no\.?|number|#))?)$`)},
	{Party, regexp.MustCompile(`(?i)^(name|payee|payer|customer|vendor|supplier|contact|customer/vendor|payee/payer)$`)},
	{Memo, regexp.MustCompile(`(?i)^(memo|memo/description|description|narration|details|particulars|line\s*description)$`)},
}

// ProposeLayout guesses how to read a table as a Trial Balance: the first of its first 30 rows whose cells name at
// least two Trial Balance columns heads it, each column's meaning comes from its header, and the number style from the
// amounts beneath. It returns what a person must still confirm, said as questions; a table with no header it can find
// is left to them whole.
func ProposeLayout(t Table) (Layout, []string) {
	layout := Layout{Table: t.Name, Columns: map[int]Meaning{}, Style: NumberStyle{Scale: 1}}
	var repeats []string
	for i, r := range t.Rows {
		if i >= 30 {
			break
		}
		columns, repeated := map[int]Meaning{}, map[Meaning][]int{}
		// A header row with a date heads a General Ledger: its own columns are read first.
		ledger := slices.ContainsFunc(r.Cells, func(c Cell) bool { return ledgerWords[0].words.MatchString(strings.TrimSpace(c.Value)) })
		for _, c := range r.Cells {
			text := strings.TrimSpace(c.Value)
			words := headerWords
			if ledger {
				words = append(slices.Clone(ledgerWords), headerWords...)
			}
			// A ledger's running balance beside its lines' amounts, and its transaction type and split account (QuickBooks'),
			// are no columns of the month's figures: left out.
			if ledger && regexp.MustCompile(`(?i)^((running\s*)?balance|transaction\s*type|split)$`).MatchString(text) {
				layout.IgnoredColumns = append(layout.IgnoredColumns, c.Column)
				continue
			}
			for _, h := range words {
				if h.words.MatchString(text) {
					// A header repeated (a multi-period export's Balance for each month) proposes its first column only,
					// and asks which one is meant.
					if len(repeated[h.meaning]) == 0 || h.meaning == Dimension {
						columns[c.Column] = h.meaning
					}
					repeated[h.meaning] = append(repeated[h.meaning], c.Column)
					break
				}
			}
		}
		if len(columns) >= 2 {
			layout.HeaderRow, layout.Columns = r.Number, columns
			for _, m := range []Meaning{AccountNumber, AccountName, AccountType, Debit, Credit, Balance, Opening, Movement, YearToDate} {
				if cols := repeated[m]; len(cols) > 1 {
					letters := make([]string, len(cols))
					for i, c := range cols {
						letters[i] = strings.TrimRight(cellRef(c, 1), "1")
					}
					repeats = append(repeats, fmt.Sprintf("Columns %s are all headed as %s: which one is this month's?", strings.Join(letters, ", "), strings.ReplaceAll(string(m), "_", " ")))
				}
			}
			break
		}
	}
	questions := append([]string{}, repeats...)
	// A ledger's running balance was left out with the header row it heads, not another's.
	if layout.HeaderRow != 0 {
		layout.IgnoredColumns = slices.DeleteFunc(layout.IgnoredColumns, func(c int) bool {
			return !regexp.MustCompile(`(?i)^((running\s*)?balance|transaction\s*type|split)$`).MatchString(strings.TrimSpace(headerText(t, layout.HeaderRow, c)))
		})
	} else {
		layout.IgnoredColumns = nil
	}
	if len(layout.IgnoredColumns) == 0 {
		layout.IgnoredColumns = nil
	}
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
	// Once the account column is known, a column headed "Class" is a class the rows are split by when an account appears under two of its values ("Sales"
	// and "Marketing" alike), or when its values do not say a classification ("Retail", "East"); else it is the
	// account's classification ("Asset", "Expense").
	for column, m := range layout.Columns {
		if m != AccountType || !strings.EqualFold(strings.TrimSpace(headerText(t, layout.HeaderRow, column)), "class") {
			continue
		}
		said, classes := 0, 0
		under := map[string]map[string]bool{}
		for _, r := range t.Rows {
			if r.Number <= layout.HeaderRow {
				continue
			}
			account, value := "", ""
			for _, c := range r.Cells {
				switch v := strings.TrimSpace(c.Value); {
				case c.Column == column:
					value = v
				case layout.Columns[c.Column] == AccountName || layout.Columns[c.Column] == AccountNumber:
					account += "|" + strings.ToLower(v)
				}
			}
			if value == "" {
				continue
			}
			said++
			if class, _, _ := classOf(value); class != "" {
				classes++
			}
			if under[account] == nil {
				under[account] = map[string]bool{}
			}
			under[account][strings.ToLower(value)] = true
		}
		split := false
		for account, values := range under {
			split = split || account != "" && len(values) > 1
		}
		if said > 0 && (split || classes*2 < said) {
			layout.Columns[column] = Dimension
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
	if layout.Ledger() {
		// A ledger's dates in numbers are read in the order they show; one they do not show is asked.
		var dates []string
		for _, r := range t.Rows {
			if r.Number <= layout.HeaderRow {
				continue
			}
			for _, c := range r.Cells {
				if layout.Columns[c.Column] == LineDate && c.Kind != Date {
					dates = append(dates, c.Value)
				}
			}
		}
		layout.DateOrder = proposeDateOrder(dates)
		for _, d := range dates {
			if _, err := readDate(Cell{Value: d}, layout.DateOrder); err == errEitherWays {
				questions = append(questions, "Are the dates written day first (31/01/2026) or month first (01/31/2026)?")
				break
			}
		}
		for _, c := range layout.IgnoredColumns {
			questions = append(questions, fmt.Sprintf("Column %s (%s) is left out as no figure of the month's: is it?", strings.TrimRight(cellRef(c, 1), "1"), strings.TrimSpace(headerText(t, layout.HeaderRow, c))))
		}
		return layout, append(questions, "Which month-end is this ledger's, and in which currency?", "Are the amounts in units, or in thousands?")
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
	// Cells is where each value of the row was read, by what the column holds; Ref is the cell the balance came from
	// (the balance column's, else the debit's, else the credit's), or the row's first cell when it has no amount.
	Cells map[Meaning]string `json:"cells"`
	// Debit and Credit are the row's two sides, a negative amount in one column counted on the other; writtenDebit and
	// writtenCredit are its two columns exactly as written, which a total row adds up.
	Debit, Credit               *big.Rat `json:"-"`
	writtenDebit, writtenCredit *big.Rat
	// Amount is Balance as an exact decimal, for storing.
	Amount string `json:"balance"`
	// Other balances the file carries for the account, by meaning, and those of them not exact to the cent.
	Others  map[Meaning]string `json:"others,omitempty"`
	Rounded []Meaning          `json:"rounded,omitempty"`
	// Unread are the other balances whose cells could not be read as amounts.
	Unread []Meaning `json:"unread,omitempty"`
	Cents  bool      `json:"cents"`
	Hidden bool      `json:"hidden,omitempty"`
	// Dimension is the department, location or class the row's balance belongs to, when the layout reads one.
	Dimension string `json:"dimension,omitempty"`
	// Formulas are the amounts read from a formula's stored result, by meaning: the last calculation, which may not be
	// the current one (docs/books-sources.md, Formulas).
	Formulas []Meaning `json:"formulas,omitempty"`
	// Date, Reference, Memo and Party are a ledger line's: its date as YYYY-MM-DD, its transaction's number, its
	// description and its payee.
	Date      string `json:"date,omitempty"`
	Reference string `json:"reference,omitempty"`
	Memo      string `json:"memo,omitempty"`
	Party     string `json:"party,omitempty"`
}

// Label is what names the row: its account name, else its account number (a Trial Balance with codes alone writes its
// totals there).
func (l Line) Label() string {
	if l.Name != "" {
		return l.Name
	}
	return l.Number
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
	// FormulasMarked says each line marks the amounts read from formula results: a reading saved before lines did says
	// nothing of its formulas.
	FormulasMarked bool `json:"formulas_marked"`
}

var totalName = regexp.MustCompile(`(?i)^(grand\s+|sub[\s-]?)?totals?\b|\b(sub[\s-]?)?totals?$`)

// totalLabel is what a total row totals: "Total Current Assets" and "Total for Current Assets" total "Current Assets";
// "Total", "TOTAL", "Subtotal" and "Grand total" total everything, or the section since the total before.
var totalLabel = regexp.MustCompile(`(?i)^(grand\s+|sub[\s-]?)?totals?\b(\s+for\b)?[\s:-]*|[\s:-]*\b(sub[\s-]?)?totals?$`)

// TotalOf is what a total row's label totals ("Current Assets" for "Total Current Assets"), "" when it names nothing
// ("Total", "Subtotal"), and whether it is a grand total, of the whole table.
func TotalOf(label string) (of string, grand bool) {
	if grandTotal.MatchString(label) {
		return "", true
	}
	return strings.TrimSpace(totalLabel.ReplaceAllString(label, "")), false
}

// headerText is what the table's header row says over column.
func headerText(t Table, headerRow, column int) string {
	for _, r := range t.Rows {
		if r.Number != headerRow {
			continue
		}
		for _, c := range r.Cells {
			if c.Column == column {
				return c.Value
			}
		}
	}
	return ""
}

// grandTotal is a total of the whole table, never of a section.
var grandTotal = regexp.MustCompile(`(?i)^grand\s+totals?\b`)

// checkTotal checks the total at lines[i] against what it covers, and says how it differs. A total named for a heading
// or a parent account above it ("Total Current Assets" under "Current Assets") covers the posting rows from there,
// nested sections included; a parent account's own balance counts with its children's. A grand total covers every
// posting row, and, written as two columns, each column must equal its side. A total whose label matches nothing
// covers the posting rows since the total before it.
func checkTotal(lines []Line, i int, debit, credit *big.Rat) []string {
	t := lines[i]
	label := strings.ToLower(strings.TrimSpace(totalLabel.ReplaceAllString(t.Label(), "")))
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
		// An unlabeled total ("Total", "TOTAL", "Subtotal") closes the whole file, or the section since the total before
		// it: it checks when either holds. A grand total closes the whole file alone.
		since := 0
		for j := i - 1; j >= 0 && !grandTotal.MatchString(strings.TrimSpace(t.Label())); j-- {
			if lines[j].Kind == Total {
				since = j + 1
				break
			}
		}
		if debit != nil && credit != nil {
			debits, credits := writtenSums(lines[:i])
			section, sectionCredits := writtenSums(lines[since:i])
			if (debit.Cmp(debits) == 0 && credit.Cmp(credits) == 0) || (debit.Cmp(section) == 0 && credit.Cmp(sectionCredits) == 0) {
				return nil
			}
			return []string{fmt.Sprintf("Row %d (%s) shows debits of %s and credits of %s, but the rows above come to %s and %s", t.Row, t.Label(), exact(debit), exact(credit), exact(debits), exact(credits))}
		}
		if all := sum(0); all.Cmp(t.Balance) == 0 || sum(since).Cmp(t.Balance) == 0 {
			return nil
		}
		return []string{fmt.Sprintf("Row %d (%s) is %s, but the rows above come to %s", t.Row, t.Label(), exact(t.Balance), exact(sum(0)))}
	}
	from := coveredFrom(lines, i)
	if debit != nil && credit != nil {
		// Written as two columns, each column must equal its column's sum over the rows it totals.
		debits, credits := writtenSums(lines[from:i])
		if debit.Cmp(debits) != 0 || credit.Cmp(credits) != 0 {
			return []string{fmt.Sprintf("Row %d (%s) shows debits of %s and credits of %s, but the rows it totals come to %s and %s", t.Row, t.Label(), exact(debit), exact(credit), exact(debits), exact(credits))}
		}
		return nil
	}
	if covered := sum(from); covered.Cmp(t.Balance) != 0 {
		return []string{fmt.Sprintf("Row %d (%s) is %s, but the rows it totals come to %s", t.Row, t.Label(), exact(t.Balance), exact(covered))}
	}
	return nil
}

// combinedLabel splits a total's label into the sections it combines: "Liabilities & Equity", "Liabilities and Equity".
var combinedLabel = regexp.MustCompile(`\s*(?:&|\+|\band\b)\s*`)

// coveredFrom is where the rows the total at lines[i] covers start: the heading or parent account it is named for; for
// a total combining sections ("Total Liabilities & Equity"), the first of what each section's own heading or total
// covers; else the row after the total before it.
func coveredFrom(lines []Line, i int) int {
	label := strings.ToLower(strings.TrimSpace(totalLabel.ReplaceAllString(lines[i].Label(), "")))
	named := func(name string) int {
		for j := i - 1; j >= 0; j-- {
			if (lines[j].Kind == Heading || lines[j].Kind == Posting) && strings.EqualFold(strings.TrimSpace(lines[j].Label()), name) {
				return j
			}
		}
		return -1
	}
	if j := named(label); j >= 0 {
		return j
	}
	if parts := combinedLabel.Split(label, -1); len(parts) > 1 {
		from := i
		for _, part := range parts {
			j := named(part)
			if j < 0 {
				// A section with no heading is known by its own total.
				for k := i - 1; k >= 0 && j < 0; k-- {
					if lines[k].Kind == Total && strings.EqualFold(strings.TrimSpace(totalLabel.ReplaceAllString(lines[k].Label(), "")), part) {
						j = coveredFrom(lines, k)
					}
				}
			}
			if j < 0 {
				from = -1
				break
			}
			from = min(from, j)
		}
		if from >= 0 {
			return from
		}
	}
	// Otherwise it covers the rows since the total before it, and each section just before whose total names a kind of
	// what it totals ("Total Current Assets" before "Total Assets"), with the rows between them.
	since := func(end int) int {
		for j := end - 1; j >= 0; j-- {
			if lines[j].Kind == Total {
				return j + 1
			}
		}
		return 0
	}
	from := since(i)
	for from > 0 {
		child := strings.ToLower(strings.TrimSpace(totalLabel.ReplaceAllString(lines[from-1].Label(), "")))
		if label == "" || !strings.HasSuffix(child, " "+label) {
			break
		}
		from = since(coveredFrom(lines, from-1))
	}
	return from
}

// Read applies a confirmed layout to its table: each row below the header becomes a line, a total (a row named
// "Total …", or whose amount is a formula that sums the rows above) is checked against the posting rows it covers and
// then left out of the balances, and every amount is read exactly in the layout's style. Nothing is dropped: a value
// that cannot be read is a problem with its location.
func (l Layout) Read(t Table) Reading {
	reading := Reading{Lines: []Line{}, Problems: []string{}, Notes: []string{}, FormulasMarked: true}
	var dimensionColumns []int
	for column, m := range l.Columns {
		if m == Dimension {
			dimensionColumns = append(dimensionColumns, column)
		}
	}
	sort.Ints(dimensionColumns)
	// A layout reads the sheet it was confirmed for: another with the same columns is another sheet's figures.
	if l.Table != "" && t.Name != l.Table {
		reading.Problems = append(reading.Problems, fmt.Sprintf("The layout reads the sheet %q, not %q", l.Table, t.Name))
		return reading
	}
	localNoted := false
	// The month-end balance is what a Trial Balance is read for: a layout naming no column for it reads nothing, rather
	// than every account as zero.
	meanings := map[Meaning]bool{}
	var twice []string
	for _, m := range l.Columns {
		// Dimensions alone may be several columns (department and location), read together as one split.
		if m != Dimension && meanings[m] && !slices.Contains(twice, string(m)) {
			twice = append(twice, string(m))
		}
		meanings[m] = true
	}
	// Each meaning is one column: two balance columns (two months of a multi-period export) would leave one month's
	// figures read as the other's.
	if len(twice) > 0 {
		sort.Strings(twice)
		reading.Problems = append(reading.Problems, "The layout names more than one column for: "+strings.ReplaceAll(strings.Join(twice, ", "), "_", " ")+"; choose one each")
		return reading
	}
	if !meanings[Balance] && !(meanings[Debit] && meanings[Credit]) {
		reading.Problems = append(reading.Problems, "The layout names no month-end balance: choose the debit and credit columns, or the one signed balance column")
		return reading
	}
	// The names a total row totals ("Total Current Assets" totals "Current Assets"): a row by such a name with no amounts
	// heads what the total covers. Rows above the header, left out, or marked accounts total nothing.
	totalled := map[string]bool{}
	for _, r := range t.Rows {
		if r.Number <= l.HeaderRow || slices.Contains(l.Ignored, r.Number) || slices.Contains(l.Postings, r.Number) {
			continue
		}
		for _, c := range r.Cells {
			if (l.Columns[c.Column] == AccountName || l.Columns[c.Column] == AccountNumber) && totalName.MatchString(strings.TrimSpace(c.Value)) {
				totalled[strings.ToLower(strings.TrimSpace(totalLabel.ReplaceAllString(strings.TrimSpace(c.Value), "")))] = true
			}
		}
	}
	debits, credits := new(big.Rat), new(big.Rat)
	// A ledger's lines are each dated, within the month the file is as of; a date that reads two ways waits for its
	// order.
	ledger := l.Ledger()
	var eitherWays, outsideMonth []int
	// heading is the account a ledger's heading row names (QuickBooks' General Ledger heads each account's lines with
	// its name), the account of the lines beneath it that name none, until its total; headed are those lines, named
	// after the table is read, so its totals are checked as written.
	var heading *Line
	headed := map[int]*Line{}
	// beside is each column the layout does not name, with the rows that hold an amount in it beside the row's own.
	beside := map[int][]int{}
	var columns []int
	for c := range l.Columns {
		columns = append(columns, c)
	}
	sort.Ints(columns)
	for _, r := range t.Rows {
		if r.Number <= l.HeaderRow {
			continue
		}
		if slices.Contains(l.Ignored, r.Number) {
			continue
		}
		line := Line{Row: r.Number, Hidden: r.Hidden, Others: map[Meaning]string{}, Cells: map[Meaning]string{}, Cents: true}
		dimensions := map[int]string{}
		var debit, credit, balance *big.Rat
		hasAmount, signed, dateUnread := false, false, false
		// outside are the row's values in columns the layout does not name, and strays their columns: nothing is dropped
		// unseen.
		var outside []string
		var strays []int
		// side keeps one amount per side: a second (a Cr-marked amount in the debit column beside a credit) is a problem,
		// not an overwrite.
		side := func(to **big.Rat, value *big.Rat, name string, ref string) {
			if *to != nil {
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d has more than one %s amount (%s)", r.Number, name, ref))
				return
			}
			*to = value
		}
		for _, c := range r.Cells {
			meaning, ok := l.Columns[c.Column]
			if !ok {
				if strings.TrimSpace(c.Value) != "" && !slices.Contains(l.IgnoredColumns, c.Column) {
					// Any value outside the chosen columns, an amount or a scope such as an entity or class, is the person's to
					// place or ignore: rows of two entities must not read as one client's.
					outside = append(outside, c.Ref)
					strays = append(strays, c.Column)
				}
				continue
			}
			if line.Ref == "" {
				line.Ref = c.Ref
			}
			line.Cells[meaning] = c.Ref
			switch meaning {
			case AccountNumber:
				line.Number = strings.TrimSpace(c.Value)
			case AccountName:
				line.Name = strings.TrimSpace(c.Value)
			case AccountType:
				line.Type = strings.TrimSpace(c.Value)
			case Dimension:
				dimensions[c.Column] = strings.TrimSpace(c.Value)
			case LineDate:
				if strings.TrimSpace(c.Value) == "" {
					break
				}
				d, err := readDate(c, l.DateOrder)
				switch {
				case err == errEitherWays:
					eitherWays, dateUnread = append(eitherWays, r.Number), true
				case err != nil:
					reading.Problems, dateUnread = append(reading.Problems, fmt.Sprintf("%s: %q is not a date", c.Ref, c.Value)), true
				default:
					line.Date = d
				}
			case Reference:
				line.Reference = strings.TrimSpace(c.Value)
			case Memo:
				line.Memo = strings.TrimSpace(c.Value)
			case Party:
				line.Party = strings.TrimSpace(c.Value)
			default:
				if c.Formula != "" {
					line.Formulas = append(line.Formulas, meaning)
				}
				a, err := cellAmount(c, l.Style)
				if err != nil && (meaning == Debit || meaning == Credit || meaning == Balance) {
					reading.Problems = append(reading.Problems, fmt.Sprintf("%s: %q is not an amount", c.Ref, c.Value))
					continue
				}
				// Another balance the file carries that cannot be read holds only what uses it: it is left out, and said so.
				if err != nil {
					line.Unread = append(line.Unread, meaning)
					reading.Notes = append(reading.Notes, fmt.Sprintf("%s: %q in the %s column is not an amount, so that balance is left out", c.Ref, c.Value, strings.ReplaceAll(string(meaning), "_", " ")))
					continue
				}
				hasAmount = true
				// Exact to the cent is asked of the month-end balance; another balance the file carries that is not is
				// said of that column alone, held only from what uses it.
				switch {
				case meaning == Debit || meaning == Credit || meaning == Balance:
					line.Cents = line.Cents && a.Cents
				case !a.Cents && !slices.Contains(line.Rounded, meaning):
					line.Rounded = append(line.Rounded, meaning)
				}
				// A currency written beside an amount must be the Trial Balance's: books in two currencies never add up.
				// A workbook amount formatted in the local currency names none: it is said, to be confirmed, once.
				if a.Mark == LocalCurrency {
					if !localNoted {
						localNoted = true
						reading.Notes = append(reading.Notes, fmt.Sprintf("%s and others are formatted in the workbook's local currency, which the file does not name: confirm it is %s", c.Ref, cmp.Or(l.Currency, "the Trial Balance's currency")))
					}
				} else if l.Currency != "" && a.Mark != "" && !MarkAgrees(a.Mark, l.Currency) {
					reading.Problems = append(reading.Problems, fmt.Sprintf("%s: %q is marked in another currency than %s, or one the reader cannot match to it (%s)", c.Ref, c.Value, l.Currency, a.Mark))
				}
				// A Dr or Cr marker says the side itself, whichever column the amount sits in: its value is then already
				// signed as balances are, a credit negative.
				// The column's amount as a total row adds it: signed as its column reads, so a Dr or Cr marker counts as
				// that side (100 Cr in the credit column is 100 there; 100 Dr in it is a debit, -100 there).
				if meaning == Debit {
					line.writtenDebit = a.Value
				} else if meaning == Credit && a.Side != "" {
					line.writtenCredit = new(big.Rat).Neg(a.Value)
				} else if meaning == Credit {
					line.writtenCredit = a.Value
				}
				switch {
				case a.Side != "" && (meaning == Debit || meaning == Credit):
					if a.Value.Sign() >= 0 {
						side(&debit, a.Value, "debit", c.Ref)
					} else {
						side(&credit, new(big.Rat).Neg(a.Value), "credit", c.Ref)
					}
				// A negative amount in a side's column is the other side's (a contra account's credit written as a negative
				// debit), said in a note.
				case meaning == Debit && a.Value.Sign() < 0:
					side(&credit, new(big.Rat).Neg(a.Value), "credit", c.Ref)
					reading.Notes = append(reading.Notes, fmt.Sprintf("%s: %s in the debit column is read as a credit of %s", c.Ref, c.Value, exact(new(big.Rat).Neg(a.Value))))
				case meaning == Credit && a.Value.Sign() < 0:
					side(&debit, new(big.Rat).Neg(a.Value), "debit", c.Ref)
					reading.Notes = append(reading.Notes, fmt.Sprintf("%s: %s in the credit column is read as a debit of %s", c.Ref, c.Value, exact(new(big.Rat).Neg(a.Value))))
				case meaning == Debit:
					side(&debit, a.Value, "debit", c.Ref)
				case meaning == Credit:
					side(&credit, a.Value, "credit", c.Ref)
				case meaning == Balance:
					balance = a.Value
					signed = a.Side != ""
				default:
					// Another balance is kept as debit minus credit: a Dr or Cr mark says its side, else the balance's sign
					// convention does.
					v := a.Value
					if a.Side == "" && l.BalanceSign == "credit" {
						v = new(big.Rat).Neg(v)
					}
					line.Others[meaning] = exact(v)
				}
			}
		}
		// The split: one dimension's value, or several named by their headers, a blank shown as one, so splits that
		// differ by column stay apart.
		if len(dimensionColumns) == 1 {
			line.Dimension = dimensions[dimensionColumns[0]]
		} else if len(dimensionColumns) > 1 {
			parts, any := make([]string, len(dimensionColumns)), false
			for i, column := range dimensionColumns {
				v := dimensions[column]
				if v != "" {
					any = true
				} else {
					v = "—"
				}
				parts[i] = cmp.Or(strings.TrimSpace(headerText(t, l.HeaderRow, column)), strings.TrimRight(cellRef(column, 1), "1")) + ": " + v
			}
			if any {
				line.Dimension = strings.Join(parts, " · ")
			}
		}
		if line.Name == "" && line.Number == "" && !hasAmount {
			if len(outside) > 0 {
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d has values only outside the chosen columns (%s): choose its columns, or mark it ignored", r.Number, strings.Join(outside, ", ")))
			}
			continue
		}
		// Amounts beside a row's own are said once per column, after the table.
		if hasAmount {
			for _, c := range strays {
				beside[c] = append(beside[c], r.Number)
			}
		}
		// A row with an account but no amount in the chosen columns, and a value beside them, may have shifted.
		if !hasAmount && len(outside) > 0 {
			reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d (%s) has no amount in the chosen columns but values beside them (%s): choose its columns, or mark it ignored", r.Number, line.Name, strings.Join(outside, ", ")))
		}
		// Other balances without the month-end one leave the month-end balance unknown, never zero.
		if hasAmount && debit == nil && credit == nil && balance == nil && len(line.Others) > 0 {
			reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d (%s) has other balances but no month-end balance", r.Number, line.Label()))
		}
		switch {
		case balance != nil:
			line.Balance = new(big.Rat).Set(balance)
			if l.BalanceSign == "credit" && !signed {
				line.Balance.Neg(line.Balance)
			}
			// A row with its debit and credit beside its balance must agree with them; its columns are kept as written,
			// for totals checked by column.
			if debit != nil || credit != nil {
				line.Debit, line.Credit = new(big.Rat), new(big.Rat)
				if debit != nil {
					line.Debit.Set(debit)
				}
				if credit != nil {
					line.Credit.Set(credit)
				}
				if net := new(big.Rat).Sub(line.Debit, line.Credit); net.Cmp(line.Balance) != 0 {
					reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d's balance (%s) is not its debit less its credit (%s)", r.Number, exact(line.Balance), exact(net)))
				}
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
		// A total is named so ("Total …", in either account column) or marked so, unless a person marked the row an
		// account. One named so in its number column ("TOTAL", "Assets") is the total of what its name says.
		total := (totalName.MatchString(line.Name) || totalName.MatchString(line.Number) || slices.Contains(l.Totals, r.Number)) && !slices.Contains(l.Postings, r.Number)
		if total && !totalName.MatchString(line.Name) && totalName.MatchString(line.Number) {
			line.Name, line.Number = strings.TrimSpace(line.Number+" "+line.Name), ""
		}
		// In a ledger, a row with neither an amount nor a date heads the lines beneath it (an account's, say).
		if line.Balance == nil && !total && !(ledger && line.Date == "" && !dateUnread) && (!totalled[strings.ToLower(line.Label())] || slices.Contains(l.Postings, r.Number)) && !slices.Contains(l.Headings, r.Number) {
			line.Balance = new(big.Rat)
			if line.Number == "" {
				reading.Notes = append(reading.Notes, fmt.Sprintf("Row %d (%s) has no amounts: read as an account with a zero balance; mark it a heading if it is one", r.Number, line.Name))
			}
		}
		switch {
		case line.Balance == nil && total:
			// A total with no amount says the export was cut short, or the layout misses its column.
			line.Kind = Total
			reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d (%s) is a total with no amount", r.Number, line.Label()))
		case line.Balance == nil:
			line.Kind = Heading
		case total:
			line.Kind = Total
		default:
			line.Kind = Posting
		}
		if line.Balance != nil {
			line.Amount = exact(line.Balance)
		}
		for _, m := range []Meaning{Credit, Debit, Balance} {
			if ref, ok := line.Cells[m]; ok && hasAmount {
				line.Ref = ref
			}
		}
		reading.Lines = append(reading.Lines, line)
		i := len(reading.Lines) - 1
		if ledger {
			switch {
			case line.Kind == Heading:
				h := line
				heading = &h
			case line.Kind == Total:
				heading = nil
			case line.Kind == Posting && line.Name == "" && line.Number == "" && heading != nil:
				headed[i] = heading
			}
		}
		switch line.Kind {
		case Posting:
			if _, ok := headed[i]; line.Name == "" && line.Number == "" && !ok {
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d has amounts but no account", r.Number))
			}
			if !line.Cents {
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d is not exact to the cent", r.Number))
			}
			switch {
			case !ledger || dateUnread:
			case line.Date == "":
				reading.Problems = append(reading.Problems, fmt.Sprintf("Row %d has an amount but no date", r.Number))
			case len(l.AsOf) >= 7 && (line.Date > l.AsOf || line.Date[:7] != l.AsOf[:7]):
				outsideMonth = append(outsideMonth, r.Number)
			}
			// Each side as written: a row with both a debit and a credit counts each on its own side.
			d, c := columnSums([]Line{line})
			debits.Add(debits, d)
			credits.Add(credits, c)
		case Total:
			// In a layout with both columns, a total with one side blank shows nothing on that side: both are checked.
			if meanings[Debit] && meanings[Credit] && (debit != nil || credit != nil) {
				if debit == nil {
					debit = new(big.Rat)
				}
				if credit == nil {
					credit = new(big.Rat)
				}
			}
			if line.Balance != nil {
				reading.Problems = append(reading.Problems, checkTotal(reading.Lines, i, debit, credit)...)
			}
		}
	}
	for _, c := range slices.Sorted(maps.Keys(beside)) {
		reading.Problems = append(reading.Problems, fmt.Sprintf("Column %s holds values in rows %s outside the chosen columns: choose what it holds, or mark it ignored", strings.TrimRight(cellRef(c, 1), "1"), rowList(beside[c])))
	}
	// Each headed line takes its heading's account, its number and name, and the cells they came from.
	for i, h := range headed {
		reading.Lines[i].Number, reading.Lines[i].Name = h.Number, h.Name
		for _, m := range []Meaning{AccountNumber, AccountName} {
			if ref, ok := h.Cells[m]; ok {
				reading.Lines[i].Cells[m] = ref
			}
		}
	}
	if len(eitherWays) > 0 {
		reading.Problems = append(reading.Problems, fmt.Sprintf("The dates in rows %s could be read day first or month first: confirm how the dates are written", rowList(eitherWays)))
	}
	if len(outsideMonth) > 0 {
		reading.Problems = append(reading.Problems, fmt.Sprintf("Rows %s are dated outside the month of %s: a ledger is read for its month alone", rowList(outsideMonth), l.AsOf))
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

// rowList names rows briefly: the first few, and how many more.
func rowList(rows []int) string {
	shown := make([]string, 0, 3)
	for i, r := range rows {
		if i == 3 {
			return strings.Join(shown, ", ") + fmt.Sprintf(" and %d more", len(rows)-3)
		}
		shown = append(shown, strconv.Itoa(r))
	}
	return strings.Join(shown, ", ")
}

// writtenSums is the posting rows' debit and credit columns summed exactly as written, signs included, as a total row
// adds them up (a row in a signed-balance layout counts on its balance's side).
func writtenSums(lines []Line) (*big.Rat, *big.Rat) {
	debits, credits := new(big.Rat), new(big.Rat)
	for _, l := range lines {
		switch {
		case l.Kind != Posting:
		case l.writtenDebit != nil || l.writtenCredit != nil:
			if l.writtenDebit != nil {
				debits.Add(debits, l.writtenDebit)
			}
			if l.writtenCredit != nil {
				credits.Add(credits, l.writtenCredit)
			}
		case l.Balance.Sign() > 0:
			debits.Add(debits, l.Balance)
		default:
			credits.Sub(credits, l.Balance)
		}
	}
	return debits, credits
}
