package transport

import (
	"context"
	"database/sql"
	"maps"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
)

// A figure in an answer is a claim about one measure: what it measures (a named metric or a row's line), whether it is
// a balance, a change, a prior month's amount, a budget or a forecast, and for which client, month, currency and
// basis, from a current or a stale read. claimOf reads that from where the cited field sits in its read; checkClaim
// holds the sentence to it. Equal numbers are not enough: a receivables amount never supports a sentence about net
// income, a balance never supports a stated change, and a forecast is never stated as an actual.

// metricTerm is a phrase people use for a measure, and the measures it may mean ("profit" may be any of three).
type metricTerm struct {
	term string
	keys []string
	re   *regexp.Regexp
}

var agentMetricTerms = func() []metricTerm {
	raw := []struct {
		keys  []string
		terms []string
	}{
		{[]string{"net_income"}, []string{"net income", "net profit", "net loss", "net earnings", "bottom line"}},
		{[]string{"operating_profit"}, []string{"operating profit", "operating income", "net operating income", "operating loss"}},
		{[]string{"gross_profit"}, []string{"gross profit", "gross margin", "gross loss"}},
		{[]string{"net_income", "operating_profit", "gross_profit"}, []string{"profit", "profits", "earnings", "loss"}},
		{[]string{"cash_change"}, []string{"net change in cash", "change in cash", "cash flow", "net cash"}},
		{[]string{"revenue"}, []string{"total income", "revenue", "revenues", "sales", "turnover"}},
		{[]string{"revenue", "net_income", "operating_profit"}, []string{"income"}},
		{[]string{"cogs"}, []string{"cost of goods sold", "total cost of goods sold", "cost of sales", "cogs"}},
		{[]string{"expenses"}, []string{"total expense", "total expenses", "operating expenses", "expenses", "expense", "costs", "spending", "overheads"}},
		{[]string{"receivables"}, []string{"accounts receivable", "receivables", "receivable", "a/r", "ar", "debtors"}},
		{[]string{"payables"}, []string{"accounts payable", "payables", "payable", "a/p", "ap", "creditors"}},
		{[]string{"working_capital"}, []string{"working capital"}},
		{[]string{"assets"}, []string{"total assets", "assets"}},
		{[]string{"liabilities"}, []string{"total liabilities", "liabilities"}},
		{[]string{"equity"}, []string{"total equity", "equity"}},
		{[]string{"cash"}, []string{"cash balance", "book cash", "cash", "checking"}},
		{[]string{"receipts"}, []string{"receipts", "collections", "inflows", "money in", "cash in"}},
		{[]string{"disbursements"}, []string{"disbursements", "payments", "outflows", "money out", "cash out"}},
		{[]string{"payroll"}, []string{"payroll", "wages", "salaries"}},
	}
	out := []metricTerm{}
	for _, group := range raw {
		for _, term := range group.terms {
			out = append(out, metricTerm{term, group.keys, regexp.MustCompile(`(?:^|[^\p{L}\p{N}/])` + regexp.QuoteMeta(term) + `(?:$|[^\p{L}\p{N}/])`)})
		}
	}
	// Longer phrases first, so "net income" is taken before "income" and "cash flow" before "cash".
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].term) > len(out[j].term) })
	return out
}()

// lineMetrics is the measures a report line's name gives it. A bare "income" names an income line ("Interest
// income"), never net or operating income, which people may mean by the word but a line's name does not.
func lineMetrics(label string) map[string]bool {
	found := metricsIn(label)
	if found["revenue"] && found["net_income"] && found["operating_profit"] {
		lower := strings.ToLower(label)
		if !strings.Contains(lower, "net income") && !strings.Contains(lower, "operating income") && !strings.Contains(lower, "net operating income") {
			delete(found, "net_income")
			delete(found, "operating_profit")
		}
	}
	return found
}

// metricsIn is the measures a text names, each phrase taken once at its longest.
func metricsIn(text string) map[string]bool {
	lower := []byte(strings.ToLower(text))
	found := map[string]bool{}
	for _, m := range agentMetricTerms {
		for _, at := range m.re.FindAllIndex(lower, -1) {
			for _, k := range m.keys {
				found[k] = true
			}
			// Blank the phrase so a shorter one inside it is not read again.
			for i := at[0]; i < at[1]; i++ {
				if lower[i] != ' ' {
					lower[i] = '#'
				}
			}
		}
	}
	return found
}

// moneyEntry is a cited money field of a sentence: what it measures, its value and the value normalized.
type moneyEntry struct {
	claim      claim
	got        string
	n          string
	step, path string
}

// metricAt is a measure named in a text, where.
type metricAt struct {
	start, end int
	keys       []string
}

// metricsAt is every measure a text names, each phrase taken once at its longest, in order.
func metricsAt(text string) []metricAt {
	lower := []byte(strings.ToLower(text))
	out := []metricAt{}
	for _, m := range agentMetricTerms {
		for _, at := range m.re.FindAllIndex(lower, -1) {
			out = append(out, metricAt{at[0], at[1], m.keys})
			for i := at[0]; i < at[1]; i++ {
				if lower[i] != ' ' {
					lower[i] = '#'
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// nearestMetric is the measure named closest to a figure within its clause: the last one before it, else the first
// after. near is the text where the field's own line, named there, is nearer the figure than that measure: between
// the measure and the figure, or, when the measure follows the figure, all of the clause before the figure.
func nearestMetric(line string, o occurrence) (map[string]bool, string) {
	start := strings.Index(line, o.clause)
	if start < 0 {
		return nil, ""
	}
	var before, after *metricAt
	for _, m := range metricsAt(o.clause) {
		m := m
		if start+m.end <= o.start {
			before = &m
		} else if start+m.start >= o.end && after == nil {
			after = &m
		}
	}
	pick, near := before, ""
	if pick != nil {
		near = line[start+pick.end : o.start]
	} else if pick = after; pick != nil {
		near = line[start:o.start] + " " + line[o.end:start+pick.start]
	}
	if pick == nil {
		return nil, ""
	}
	out := map[string]bool{}
	for _, k := range pick.keys {
		out[k] = true
	}
	return out, near
}

// claim is what a cited field measures.
type claim struct {
	label    string
	field    string // the field's own words (ledger balance, amount)
	keys     map[string]bool
	kind     string // "level", "change", "prior", "budget" or "percent"
	forecast bool
	scenario bool
	ytd      bool
	period   string // the month the figure is for, YYYY-MM, when known
	entity   string
	currency string
	basis    string
	stale    bool
	// driver says the field is a driver of a movement (a payee row of a changes read), attributing it, not explaining it.
	driver bool
	// variance says a percentage is a variance against a budget (the budget comparison's signed percent).
	variance bool
	// incomplete is a forecast built while some of its inputs were not captured.
	incomplete bool
	cause      bool
	money      bool
}

var (
	claimChangeKeys  = map[string]bool{"change": true, "variance": true, "difference": true, "delta": true, "movement": true, "net_change": true, "increase": true, "decrease": true, "unexplained": true}
	claimPriorKeys   = map[string]bool{"prior": true, "prior_month": true, "previous": true, "prior_amount": true, "last_month": true}
	claimPercentKeys = map[string]bool{"percent": true, "ratio": true, "margin_percent": true}
	claimMoneyKeys   = map[string]bool{"amount": true, "balance": true, "ledger_balance": true, "statement_balance": true, "supported": true, "trial_balance": true, "total": true, "actual": true, "budget": true, "opening": true, "closing": true, "lowest": true, "receipts": true, "disbursements": true, "net": true, "open": true, "due": true}
	// A cash plan's week fields name what they measure.
	claimKeyMetrics = map[string]string{"opening": "cash", "closing": "cash", "lowest": "cash", "receipts": "receipts", "disbursements": "disbursements",
		// The budget comparison's totals.
		"net_month": "net_income", "net_ytd": "net_income", "income_month": "revenue", "income_ytd": "revenue", "expense_month": "expenses", "expense_ytd": "expenses"}
	claimLabelKeys  = []string{"name", "payee", "account", "label", "line", "title", "customer", "vendor"}
	claimCauseRe    = regexp.MustCompile(`cause|reason|explanation|because|why`)
	claimMoneyValue = regexp.MustCompile(`^[(−-]?\$?\d[\d,]*\.\d{2}\)?$`)
)

// claimOf reads what the field at path measures from the field's key and the objects that hold it.
func claimOf(read agentRead, path string) claim {
	c := claim{keys: map[string]bool{}, kind: "level", period: read.period, entity: read.entity}
	keys := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, k := range keys {
		keys[i] = strings.ReplaceAll(strings.ReplaceAll(k, "~1", "/"), "~0", "~")
	}
	last := keys[len(keys)-1]
	// Walk from the read's root to the field, the nearest object's facts winning.
	value := read.value
	ancestors := []map[string]any{}
	for _, k := range keys {
		if object, ok := value.(map[string]any); ok {
			ancestors = append(ancestors, object)
			value = object[k]
		} else if list, ok := value.([]any); ok {
			n := 0
			for _, r := range k {
				if r < '0' || r > '9' {
					n = -1
					break
				}
				n = n*10 + int(r-'0')
			}
			if n < 0 || n >= len(list) {
				break
			}
			value = list[n]
		} else {
			break
		}
	}
	text := func(o map[string]any, key string) string {
		s, _ := o[key].(string)
		return strings.TrimSpace(s)
	}
	for _, o := range ancestors {
		if s := text(o, "period"); len(s) == 7 {
			c.period = s
		}
		for _, key := range []string{"entity", "client_entity"} {
			if s := text(o, key); s != "" {
				c.entity = s
			}
		}
		if s := text(o, "currency"); s != "" {
			c.currency = strings.ToUpper(s)
		}
		if s := text(o, "basis"); s != "" {
			c.basis = strings.ToLower(s)
		}
		if stale, _ := o["stale"].(bool); stale {
			c.stale = true
		}
		for _, key := range []string{"state", "status"} {
			if s := strings.ToLower(text(o, key)); s == "stale" || s == "superseded" || s == "outdated" {
				c.stale = true
			}
		}
	}
	// A published report's figures are not current when the publication was superseded, the books changed after it,
	// or its sign-off lapsed; the publication sits beside the pack, not above the figure.
	if root, ok := read.value.(map[string]any); ok {
		if pub, ok := root["publication"].(map[string]any); ok {
			switch strings.ToLower(text(pub, "state")) {
			case "superseded", "needs_correction", "signoff_lapsed", "unverified":
				c.stale = true
			}
		}
	}
	// The row's own line names it, from the nearest object that has one.
	for i := len(ancestors) - 1; i >= 0 && c.label == ""; i-- {
		for _, key := range claimLabelKeys {
			if s := text(ancestors[i], key); s != "" {
				c.label = s
				break
			}
		}
	}
	for k := range lineMetrics(c.label) {
		c.keys[k] = true
	}
	words := strings.ReplaceAll(last, "_", " ")
	if len(c.keys) == 0 {
		for k := range metricsIn(words) {
			c.keys[k] = true
		}
	}
	if m, ok := claimKeyMetrics[last]; ok && len(c.keys) == 0 {
		c.keys[m] = true
	}
	// A generic field under a known total (/totals/net_month/actual) is that total's.
	if len(keys) > 1 {
		if m, ok := claimKeyMetrics[keys[len(keys)-2]]; ok && len(c.keys) == 0 {
			c.keys[m] = true
		}
	}
	// A generic field (amount, change) under a named object (/lowest/amount, /working_capital/amount) is that object's.
	if len(c.keys) == 0 && c.label == "" && len(keys) > 1 {
		parent := keys[len(keys)-2]
		for k := range metricsIn(strings.ReplaceAll(parent, "_", " ")) {
			c.keys[k] = true
		}
		if m, ok := claimKeyMetrics[parent]; ok && len(c.keys) == 0 {
			c.keys[m] = true
		}
	}
	c.field = words
	if c.label == "" {
		c.label = words
	}
	switch {
	case claimChangeKeys[last]:
		c.kind = "change"
	case claimPriorKeys[last]:
		c.kind = "prior"
	case last == "budget":
		c.kind = "budget"
	case claimPercentKeys[last]:
		c.kind = "percent"
		if len(ancestors) > 0 {
			_, c.variance = ancestors[len(ancestors)-1]["budget"]
		}
	}
	for i, k := range keys {
		switch {
		case k == "weeks" || k == "forecast" || k == "projection":
			c.forecast = true
		case strings.Contains(k, "scenario"):
			c.forecast, c.scenario = true, true
		case k == "ytd" || strings.HasSuffix(k, "_ytd") || strings.HasPrefix(k, "ytd_"):
			c.ytd = true
		case k == "prior_month" && i == len(keys)-1:
			c.kind = "prior"
		}
		// A field that states a cause (a reason, an explanation) supports one; a driver row (a changes read's payees,
		// a row's drivers) attributes a movement without establishing why.
		if claimCauseRe.MatchString(k) {
			c.cause = true
		}
		if k == "drivers" || i == 2 && keys[0] == "changes" && (k == "new" || k == "missing" || k == "changed" || k == "unattributed") {
			c.driver = true
		}
	}
	// A cash plan is a forecast, except the captured opening balance it starts from. Every figure of it is as fresh as
	// that opening, and the forecast is incomplete while its open items are.
	if read.tool == "cash_plan" {
		if keys[0] != "opening" && keys[0] != "open_items" {
			c.forecast = true
		}
		if root, ok := read.value.(map[string]any); ok {
			if opening, ok := root["opening"].(map[string]any); ok && opening["stale"] == true {
				c.stale = true
			}
			if items, ok := root["open_items"].(map[string]any); ok && items["complete"] == false && c.forecast {
				c.incomplete = true
			}
		}
	}
	// A prior month's figure, and a change made from it, are as fresh as the prior month's capture.
	if c.kind == "prior" || c.kind == "change" {
		for _, o := range ancestors {
			if prior, ok := o["prior"].(map[string]any); ok && prior["stale"] == true {
				c.stale = true
			}
		}
	}
	if c.kind == "prior" && len(c.period) == 7 {
		if m, err := time.Parse("2006-01", c.period); err == nil {
			c.period = m.AddDate(0, -1, 0).Format("2006-01")
		}
	}
	got, _ := valueAt(read.value, path)
	c.money = claimMoneyKeys[last] || claimChangeKeys[last] || claimPriorKeys[last] || claimMoneyValue.MatchString(strings.TrimSpace(got)) || len(c.keys) > 0 && c.kind != "percent"
	return c
}

// describe is how a citation names the field: its line and what of it, from the typed facts rather than the path.
func (c claim) describe() string {
	what := map[string]string{"change": "change", "prior": "prior month", "budget": "budget", "percent": "percent"}[c.kind]
	parts := []string{c.label}
	if what == "" && c.field != "" && c.field != c.label && c.field != "amount" && c.field != "value" && c.field != "total" && !strings.Contains(strings.ToLower(c.label), c.field) {
		what = c.field
	}
	if what != "" && !strings.Contains(strings.ToLower(c.label), what) {
		parts = append(parts, what)
	}
	if c.ytd {
		parts = append(parts, "year to date")
	}
	if c.scenario {
		parts = append(parts, "scenario")
	} else if c.forecast {
		parts = append(parts, "forecast")
	}
	if c.stale {
		parts = append(parts, "stale")
	}
	return strings.Join(parts, " · ")
}

var (
	// The verbs of movement a figure-less sentence states ("net income rose").
	claimRiseVerb = map[string]bool{"rose": true, "risen": true, "rises": true, "rising": true, "increased": true, "increases": true, "grew": true, "grown": true, "grows": true, "climbed": true, "jumped": true, "gained": true, "improved": true}
	claimFallVerb = map[string]bool{"fell": true, "fallen": true, "falls": true, "falling": true, "decreased": true, "decreases": true, "dropped": true, "drops": true, "declined": true, "declines": true, "slipped": true, "lost": true, "worsened": true}
	claimRise     = map[string]bool{"rose": true, "risen": true, "rises": true, "rising": true, "increased": true, "increases": true, "grew": true, "grown": true, "grows": true, "climbed": true, "jumped": true, "gained": true, "improved": true, "up": true, "higher": true, "more": true, "above": true, "ahead": true, "better": true, "increase": true}
	claimFall     = map[string]bool{"fell": true, "fallen": true, "falls": true, "falling": true, "decreased": true, "decreases": true, "dropped": true, "drops": true, "declined": true, "declines": true, "slipped": true, "lost": true, "worsened": true, "down": true, "lower": true, "less": true, "below": true, "behind": true, "worse": true, "decrease": true, "short": true}
	claimNeutral  = map[string]bool{"changed": true, "moved": true, "swung": true, "change": true, "movement": true, "swing": true, "variance": true, "difference": true, "differs": true, "differ": true, "off": true, "gap": true, "exceeds": true, "exceed": true, "over": true, "under": true}
	claimBefore   = map[string]bool{"from": true, "versus": true, "vs": true, "against": true, "than": true}
	claimForecast = regexp.MustCompile(`\b(forecast|forecasts|projected|projection|projects|expected|expect|expects|plan|planned|plans|will|would|could|estimate|estimated|scenario|if|likely|assumes|assumed|assumption)\b`)
	claimActual   = regexp.MustCompile(`\b(actual|actually|recorded|booked|posted|was|were|ended|closed)\b`)
	claimBudget   = regexp.MustCompile(`\b(budget|budgets|budgeted|plan|planned|target)\b`)
	claimYTD      = regexp.MustCompile(`\b(year to date|year-to-date|ytd|so far this year)\b`)
	// Words that disclose a figure is not current: never a bare "as of", which dates without disclosing.
	claimStaleSaid = regexp.MustCompile(`\b(stale|superseded|out of date|outdated|not current|no longer current|earlier version|older version|before the latest|needs correction|needs a correction|sign-off lapsed|lapsed|unverified|not verified|could not be verified|may have changed|has changed since)\b`)
	claimAvailable = regexp.MustCompile(`\b(available|in the bank|spendable|on hand at the bank)\b`)
	// Words that assert a cause, which needs causal evidence or a hedge. "Driven by", "from" and "including" attribute
	// a change to its drivers instead, which must add up or name the rest.
	claimCausal = regexp.MustCompile(`\b(because|due to|caused by|as a result of|owing to|thanks to|explained by|stems from|result of|on the back of)\b`)
	claimHedge  = regexp.MustCompile(`\b(may|might|could|possibly|perhaps|likely|appears|appear|seems|seem|suggests|suggest|probably|worth checking|hypothesis|unconfirmed|not confirmed|not yet confirmed|to confirm|one explanation)\b`)
	// The remainder named: words that state what is left unexplained, not qualifiers ("including", "mostly").
	claimRemainder = regexp.MustCompile(`\b(the rest|remainder|remaining|unexplained|unattributed|not explained|unaccounted|other changes|other movements|everything else)\b`)
	// Words that say a computed figure is a difference.
	claimDifferenceSaid = regexp.MustCompile(`\b(difference|differ|differs|gap|less|leave|leaves|leaving|left|net of|minus|exceed|exceeds|short|more than|less than|over|under|after|change|changed|up|down|higher|lower|ahead|behind)\b`)
	// Words that attribute a total to drivers, which must then add up or name the rest.
	claimAttribution    = regexp.MustCompile(`\b(from|of which|including|includes|mainly|mostly|largely|chiefly|primarily|led by|driven by|reflecting)\b`)
	claimCausalEvidence = regexp.MustCompile(`\b(because|due to|caused|causes|driven|drove|drives|resulted|result of|results from|led to|leads to|owing to|thanks to|after|following|boosted|lifted|raised|increased|reduced|lowered|cut|added|brought|grew|rose|fell|dropped)\b`)
	// Words that name a change as such, beside its figure ("Rent's variance against budget is 500.00").
	claimVarianceNamed  = regexp.MustCompile(`\b(variance|difference|change|movement|swing|gap)\b`)
	claimScenario       = regexp.MustCompile(`\b(scenario|what-if|if)\b`)
	claimFromTo         = regexp.MustCompile(`(?i)\bfrom\s+(\w+(?:\s+\d{4})?)\s+to\s+(\w+(?:\s+\d{4})?)`)
	claimNegation       = regexp.MustCompile(`\b(not|no|never|without|neither|nor|unrelated|isn't|wasn't|didn't|doesn't|don't|won't|cannot|can't|hasn't|haven't)\b|n't\b`)
	claimIncompleteSaid = regexp.MustCompile(`\b(incomplete|missing|partial|not complete|provisional|not yet captured|not all)\b`)
	// Month names in any case, except "may", which is a month only as May.
	claimMonth    = regexp.MustCompile(`\b((?i:january|february|march|april|june|july|august|september|october|november|december|jan|feb|mar|apr|jun|jul|aug|sep|sept|oct|nov|dec)|May)\b(?:\s+(\d{4}))?|\b(\d{4})-(\d{2})\b(?:-\d{2})?`)
	claimCurrency = regexp.MustCompile(`(?i)\b(USD|EUR|GBP|CAD|AUD|NZD|INR|JPY|CHF|SGD|HKD|MXN|SEK|NOK|DKK|ZAR)\b|[€£₹¥$]`)
	claimBasis    = regexp.MustCompile(`\b(cash|accrual|modified cash)[ -]basis\b`)
	claimClause   = regexp.MustCompile(`[;,:]\s+|\s+(?:and|but|while|whereas|with)\s+`)
	monthNumbers  = map[string]int{"january": 1, "jan": 1, "february": 2, "feb": 2, "march": 3, "mar": 3, "april": 4, "apr": 4, "may": 5, "june": 6, "jun": 6, "july": 7, "jul": 7, "august": 8, "aug": 8, "september": 9, "sep": 9, "sept": 9, "october": 10, "oct": 10, "november": 11, "nov": 11, "december": 12, "dec": 12}
	dollarFamily  = map[string]bool{"USD": true, "CAD": true, "AUD": true, "NZD": true, "SGD": true, "HKD": true, "MXN": true}
)

// occurrence is where a figure sits in its sentence: its clause, the words just before it and just after.
type occurrence struct {
	clause         string
	before, after  []string
	start, end     int
	normalized     string
	value          *big.Rat
	sentenceClause int
}

func wordsOf(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && r != '-' && r != '/' })
}

// figuresIn finds every figure of a sentence with its context.
func figuresIn(line string) []occurrence {
	out := []occurrence{}
	bounds := claimClause.FindAllStringIndex(line, -1)
	for _, at := range agentFigure.FindAllStringIndex(line, -1) {
		raw := line[at[0]:at[1]]
		n := normalizeFigure(raw)
		if n == "" {
			continue
		}
		start, end, clauseIndex := 0, len(line), 0
		for i, b := range bounds {
			if b[1] <= at[0] {
				start, clauseIndex = b[1], i+1
			}
			if b[0] >= at[1] && end == len(line) {
				end = b[0]
			}
		}
		before := wordsOf(line[start:at[0]])
		if len(before) > 4 {
			before = before[len(before)-4:]
		}
		after := wordsOf(line[at[1]:end])
		if len(after) > 2 {
			after = after[:2]
		}
		o := occurrence{clause: line[start:end], before: before, after: after, start: at[0], end: at[1], normalized: n, sentenceClause: clauseIndex}
		if r, ok := new(big.Rat).SetString(strings.TrimSuffix(n, "%")); ok {
			o.value = r
		}
		out = append(out, o)
	}
	return out
}

// direction is how the words around a figure state it: "rise", "fall", "change" (a change of no stated direction),
// "before" (another period's amount) or "" (a level).
func (o occurrence) direction() string {
	last := func(n int) string {
		if len(o.before) >= n {
			return o.before[len(o.before)-n]
		}
		return ""
	}
	// After a figure: "1,840 higher", "540 over budget", "500 more than"; "128,400 less expenses" is arithmetic.
	if len(o.after) > 0 {
		next := ""
		if len(o.after) > 1 {
			next = o.after[1]
		}
		switch w := o.after[0]; {
		case w == "higher" || w == "up" || w == "above" || w == "ahead" || w == "better" || w == "more" && next == "than":
			return "rise"
		case w == "lower" || w == "down" || w == "below" || w == "behind" || w == "worse" || w == "less" && next == "than":
			return "fall"
		case w == "over" || w == "under":
			return "change"
		}
	}
	w1, w2, w3 := last(1), last(2), last(3)
	// "rose to 21,780.00": the figure is the level after the movement.
	if w1 == "to" && (claimRiseVerb[w2] || w2 == "up") {
		return "rise-to"
	}
	if w1 == "to" && (claimFallVerb[w2] || w2 == "down") {
		return "fall-to"
	}
	switch {
	case claimRise[w1]:
		return "rise"
	case claimFall[w1]:
		return "fall"
	case claimNeutral[w1] && w1 != "over" && w1 != "under":
		return "change"
	case w1 == "by":
		// "rose by", "differs by", "exceed total expense by": the verb "by" hangs on, up to three words back.
		for _, w := range []string{w2, w3, last(4)} {
			switch {
			case claimRise[w]:
				return "rise"
			case claimFall[w]:
				return "fall"
			case claimNeutral[w]:
				return "change"
			}
		}
	case w1 == "of":
		// "an increase of": the word "of" hangs on, not one further back ("128,400 less expenses of 106,620" is an
		// amount of expenses).
		w := w2
		if w == "a" || w == "an" || w == "the" {
			w = w3
		}
		switch {
		case claimRise[w]:
			return "rise"
		case claimFall[w]:
			return "fall"
		case claimNeutral[w]:
			return "change"
		}
	case claimBefore[w1]:
		return "before"
	}
	if w2 == "last" && w1 == "month" || w2 == "prior" && w1 == "month" || w2 == "previous" && w1 == "month" {
		return "before"
	}
	return ""
}

// nearestMonth is the month named closest to a figure within its clause, by distance either side; none when the clause
// names no month.
func nearestMonth(line string, o occurrence) [][2]int {
	start := strings.Index(line, o.clause)
	if start < 0 {
		return nil
	}
	best, distance := [][2]int(nil), -1
	for _, at := range claimMonth.FindAllStringSubmatchIndex(o.clause, -1) {
		from, to := start+at[0], start+at[1]
		d := o.start - to
		if from >= o.end {
			d = from - o.end
		}
		if d < 0 {
			continue
		}
		if distance < 0 || d < distance {
			distance = d
			best = namedMonths(line[from:to])
		}
	}
	return best
}

// namedMonths is the months a text names, as month numbers with the year when given (0 when not).
func namedMonths(text string) [][2]int {
	out := [][2]int{}
	for _, m := range claimMonth.FindAllStringSubmatch(text, -1) {
		if m[1] != "" {
			year := 0
			if m[2] != "" {
				year = atoiSafe(m[2])
			}
			out = append(out, [2]int{monthNumbers[strings.ToLower(m[1])], year})
		} else if m[3] != "" {
			out = append(out, [2]int{atoiSafe(m[4]), atoiSafe(m[3])})
		}
	}
	return out
}

func atoiSafe(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func keysNamed(keys map[string]bool) string {
	out := []string{}
	for k := range keys {
		out = append(out, strings.ReplaceAll(k, "_", " "))
	}
	sort.Strings(out)
	return strings.Join(out, " or ")
}

func overlaps(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// genericLineWords are words many lines share ("Lab fees", "Patient fees"), which never identify one line alone.
var genericLineWords = map[string]bool{"the": true, "and": true, "for": true, "was": true, "are": true, "its": true, "not": true, "but": true, "has": true, "had": true, "per": true, "all": true, "any": true, "our": true, "out": true, "off": true, "own": true, "via": true,
	"total": true, "fees": true, "fee": true, "expense": true, "expenses": true, "income": true, "costs": true, "cost": true, "account": true, "accounts": true, "other": true, "payable": true, "receivable": true, "balance": true, "amount": true, "net": true, "charges": true, "charge": true, "revenue": true, "sales": true}

// labelNamed says whether a text names a row's line: every one of the line's distinctive words (three letters or more,
// not filler and not a word many lines share), so "Office rent" never names "Office supplies"; or, when the line has
// none ("Other expense"), the line's whole name.
func labelNamed(text, label string) bool {
	in := map[string]bool{}
	for _, w := range wordsOf(text) {
		in[strings.Trim(w, "-")] = true
	}
	distinctive, all := false, true
	for _, w := range wordsOf(label) {
		w = strings.Trim(w, "-")
		if len(w) < 3 || agentStopWords[w] || genericLineWords[w] {
			continue
		}
		distinctive = true
		all = all && in[w]
	}
	if distinctive {
		return all
	}
	return strings.TrimSpace(label) != "" && strings.Contains(strings.ToLower(text), strings.ToLower(strings.TrimSpace(label)))
}

// sharesWord says whether two texts share a distinctive word: looser than naming a line, for whether a passage
// speaks of a stated cause or effect.
func sharesWord(text, other string) bool {
	in := map[string]bool{}
	for _, w := range wordsOf(text) {
		in[strings.Trim(w, "-")] = true
	}
	for _, w := range wordsOf(other) {
		w = strings.Trim(w, "-")
		if len(w) >= 3 && !agentStopWords[w] && !genericLineWords[w] && in[w] {
			return true
		}
	}
	return false
}

// introduced says whether a qualifier stands in a clause before the figure's that holds no figure of its own, as an
// introductory phrase governs what follows ("Year to date, rent was 85,000.00").
func introduced(line string, o occurrence, qualifier *regexp.Regexp) bool {
	start := 0
	for _, b := range claimClause.FindAllStringIndex(line, -1) {
		if b[1] > o.start {
			break
		}
		clause := line[start:b[0]]
		if !agentFigure.MatchString(clause) && qualifier.MatchString(strings.ToLower(clause)) {
			return true
		}
		start = b[1]
	}
	return false
}

// checkClaim holds one figure of a sentence to the field it cites. It returns why the sentence misstates the field,
// or "" when the sentence says what the field measures. got is the field's value; o is where the sentence states it.
func checkClaim(line string, o occurrence, c claim, got string, entities []string) string {
	lowerLine, lowerClause := strings.ToLower(line), strings.ToLower(o.clause)
	// What it measures: the metric named nearest the figure in its clause (before it, else after), else in the
	// sentence, must be the field's; "net income is 8,000 versus receivables at 8,000" names two.
	named, near := nearestMetric(line, o)
	if len(named) == 0 {
		named, near = metricsIn(line), line
	}
	if len(c.keys) > 0 {
		if len(named) > 0 && !overlaps(named, c.keys) && !labelNamed(near, c.label) {
			return "the sentence calls " + o.normalized + " " + keysNamed(named) + ", but the cited field is " + c.describe()
		}
		if len(named) == 0 && !labelNamed(line, c.label) {
			return "the sentence does not say what " + o.normalized + " measures; the cited field is " + c.describe()
		}
	} else if c.money || c.kind == "percent" {
		// The line must be named, and nearer the figure than any other measure the clause names.
		if !labelNamed(line, c.label) || len(named) > 0 && !labelNamed(near, c.label) {
			if len(named) > 0 {
				return "the sentence calls " + o.normalized + " " + keysNamed(named) + ", but the cited field is " + c.describe()
			}
			return "the sentence does not say what " + o.normalized + " measures; the cited field is " + c.describe()
		}
	}
	// A balance is not a change, a change is not a balance, and the change's direction is its sign.
	direction := o.direction()
	field, _ := new(big.Rat).SetString(strings.TrimSuffix(normalizeFigure(got), "%"))
	switch c.kind {
	case "change":
		named := direction == "" && claimVarianceNamed.MatchString(lowerClause)
		if !named && (direction == "" || direction == "before" || direction == "rise-to" || direction == "fall-to") {
			return "the sentence states " + o.normalized + " as an amount, but the cited field is a change (" + c.describe() + ")"
		}
		if field != nil {
			if direction == "rise" && field.Sign() < 0 || direction == "fall" && field.Sign() > 0 {
				return "the sentence states a " + direction + " of " + o.normalized + ", but the cited change is " + normalizeFigure(got)
			}
		}
	case "level", "budget":
		if direction == "rise" || direction == "fall" || direction == "change" {
			return "the sentence states " + o.normalized + " as a change, but the cited field is an amount (" + c.describe() + ")"
		}
	case "prior":
		if direction == "rise" || direction == "fall" || direction == "change" || direction == "rise-to" || direction == "fall-to" {
			return "the sentence states " + o.normalized + " as a change, but the cited field is the prior month's amount"
		}
		if direction != "before" && len(namedMonths(line)) == 0 && !strings.Contains(lowerLine, "prior") && !strings.Contains(lowerLine, "last month") && !strings.Contains(lowerLine, "previous") {
			return "the sentence states the prior month's " + o.normalized + " as current"
		}
	}
	// A budget variance percentage is said to be against the budget, over in its sign or under.
	if c.variance {
		if !claimBudget.MatchString(lowerClause) {
			return "the sentence states the budget variance " + o.normalized + " without saying it is against the budget"
		}
		if field != nil && len(o.after) > 0 {
			over := o.after[0] == "over" || o.after[0] == "above" || o.after[0] == "ahead"
			under := o.after[0] == "under" || o.after[0] == "below" || o.after[0] == "behind" || o.after[0] == "short"
			if over && field.Sign() < 0 || under && field.Sign() > 0 {
				return "the sentence states " + o.normalized + " " + o.after[0] + " budget, but the variance is " + normalizeFigure(got)
			}
		}
	}
	// A budget is said to be one; an actual is not called a budget.
	if c.kind == "budget" && !claimBudget.MatchString(lowerClause) {
		return "the sentence states the budget " + o.normalized + " without saying it is the budget"
	}
	// An actual amount is not called a budget; a variance is rightly said to be against the budget.
	if (c.kind == "level" || c.kind == "prior") && len(o.before) > 0 && claimBudget.MatchString(strings.Join(o.before, " ")) {
		return "the sentence calls " + o.normalized + " a budget, but the cited field is " + c.describe()
	}
	// A forecast is said to be one, in the figure's own clause or an introduction before it ("cash closed at 23,400.00,
	// while receipts are forecast to rise" says nothing of the cash); an actual is never said to be forecast.
	if c.forecast && !claimForecast.MatchString(lowerClause) && !introduced(line, o, claimForecast) {
		return "the sentence states the forecast " + o.normalized + " as an actual (" + c.describe() + ")"
	}
	if c.scenario && !claimScenario.MatchString(lowerClause) && !introduced(line, o, claimScenario) {
		return "the sentence states the scenario's " + o.normalized + " without saying it is a scenario"
	}
	if !c.forecast && claimForecast.MatchString(lowerClause) && !claimActual.MatchString(lowerClause) && (strings.Contains(lowerClause, "forecast") || strings.Contains(lowerClause, "projected") || strings.Contains(lowerClause, "will be") || strings.Contains(lowerClause, "expected")) {
		return "the sentence states " + o.normalized + " as a forecast, but the cited field is an actual (" + c.describe() + ")"
	}
	if c.ytd != (claimYTD.MatchString(lowerClause) || introduced(line, o, claimYTD)) {
		if c.ytd {
			return "the sentence states the year-to-date " + o.normalized + " as a month's"
		}
		return "the sentence states the month's " + o.normalized + " as year to date"
	}
	// Book cash is the ledger's, not money available at the bank.
	if c.keys["cash"] && claimAvailable.MatchString(lowerClause) {
		return "the sentence calls book cash of " + o.normalized + " available at the bank"
	}
	// The month named is the field's month: the month nearest the figure in its clause ("21,780.00 in October versus
	// 19,940.00 in September" gives each its own), else any month the sentence names.
	if !c.forecast && len(c.period) == 7 {
		if months := nearestMonth(line, o); len(months) > 0 || len(namedMonths(line)) > 0 {
			if len(months) == 0 {
				months = namedMonths(line)
			}
			p, _ := time.Parse("2006-01", c.period)
			before := p.AddDate(0, -1, 0)
			match := false
			for _, m := range months {
				if m[0] == int(p.Month()) && (m[1] == 0 || m[1] == p.Year()) {
					match = true
				}
				// A change runs from the month before: naming either month places it.
				if c.kind == "change" && m[0] == int(before.Month()) && (m[1] == 0 || m[1] == before.Year()) {
					match = true
				}
			}
			if !match {
				return "the sentence puts " + o.normalized + " in another month; the cited field is for " + p.Format("January 2006")
			}
		}
	}
	// The client named is the field's client, exactly: every client named beside the figure (else in the sentence).
	if c.entity != "" {
		named := clientsNamed(o.clause, entities)
		if len(named) == 0 {
			named = clientsNamed(line, entities)
		}
		for _, e := range named {
			if !isSameClient(e, c.entity) {
				return "the sentence gives " + o.normalized + " to " + e + ", but the cited figure is " + c.entity + "'s"
			}
		}
	}
	// The currency and basis named are the field's.
	if c.currency != "" {
		for _, m := range claimCurrency.FindAllString(o.clause, -1) {
			code := map[string]string{"€": "EUR", "£": "GBP", "₹": "INR", "¥": "JPY"}[m]
			if code == "" {
				code = strings.ToUpper(m)
			}
			if code == "$" && !dollarFamily[c.currency] || code != "$" && code != c.currency {
				return "the sentence states " + o.normalized + " in " + m + ", but the cited figure is in " + c.currency
			}
		}
	}
	if c.basis != "" {
		for _, m := range claimBasis.FindAllStringSubmatch(lowerLine, -1) {
			if !strings.HasPrefix(strings.ReplaceAll(c.basis, "_", " "), m[1]) {
				return "the sentence states " + o.normalized + " on the " + m[1] + " basis, but the cited figure is on the " + c.basis + " basis"
			}
		}
	}
	// A stale read is said to be one; a forecast built from incomplete inputs is said to be incomplete.
	if c.stale && !claimStaleSaid.MatchString(lowerLine) {
		return "the sentence states " + o.normalized + " as current, but the cited read is stale"
	}
	if c.incomplete && !claimIncompleteSaid.MatchString(lowerLine) {
		return "the sentence states the forecast " + o.normalized + " without saying its inputs are incomplete"
	}
	return ""
}

// checkCauses holds a sentence's stated cause to its support: a cause must be cited (a driver, a reason, a document's
// words) or said as a possibility, never asserted from an amount alone.
func checkCauses(line string, entries []agentSupportEntry, claims map[int]claim, reads map[string]agentRead) string {
	lower := strings.ToLower(line)
	bounds := claimClause.FindAllStringIndex(lower, -1)
	// Each cause the sentence states ("because of…", "due to…") is checked on its own.
	for _, at := range claimCausal.FindAllStringIndex(lower, -1) {
		// A hedge counts only in the cause's own clause ("may be due to"), not elsewhere ("…, which may be reviewed").
		clauseStart, clauseEnd := 0, len(lower)
		for _, b := range bounds {
			if b[1] <= at[0] {
				clauseStart = b[1]
			}
			if b[0] >= at[1] && clauseEnd == len(lower) {
				clauseEnd = b[0]
			}
		}
		// The hedge qualifies the cause itself: in the clause up to the connective ("may be due to", "possibly
		// because of"), not after it ("because of a campaign which may end soon").
		if claimHedge.MatchString(lower[clauseStart:at[1]]) {
			continue
		}
		// The cause is what follows the connective in its clause; a cited passage or cause field must name it.
		cause := line[at[1]:clauseEnd]
		supported := false
		for i, s := range entries {
			switch {
			case s.quote != "" && claimNegation.MatchString(strings.ToLower(s.quote)):
				// "The campaign did not affect revenue" contradicts the cause rather than supporting it.
			case s.quote != "" && sharesWord(cause, s.quote) && claimCausalEvidence.MatchString(strings.ToLower(s.quote)) && effectNamed(line[clauseStart:at[0]], s.quote, claims):
				// The passage names the cause, says it acted, and names the effect the sentence claims ("revenue rose
				// after the campaign started"); "the campaign raised payroll" supports no claim about revenue.
				supported = true
			case claims[i].cause:
				got, _ := valueAt(reads[s.step].value, s.path)
				supported = supported || sharesWord(cause, got) || labelNamed(cause, claims[i].label)
			}
		}
		if !supported {
			return "the sentence states a cause the reads do not show (" + strings.TrimSpace(line[at[0]:clauseEnd]) + "); cite it, or say it as a possibility"
		}
	}
	return ""
}

// driverMovement is a cited payee row's movement in a changes read: this month less last month, whichever of its fields
// is cited, so a sentence naming drivers is held to the total they explain. ok is false for any other field.
func driverMovement(read agentRead, path string) (string, *big.Rat, bool) {
	keys := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(keys) < 4 || keys[0] != "changes" {
		return "", nil, false
	}
	switch keys[2] {
	case "new", "missing", "changed":
		if len(keys) < 5 {
			return "", nil, false
		}
	case "unattributed":
		row := "/" + strings.Join(keys[:3], "/")
		this, _ := valueAt(read.value, row+"/this_month")
		last, _ := valueAt(read.value, row+"/last_month")
		movement, ok := derive("difference", []string{this, last})
		return row, movement, ok
	default:
		return "", nil, false
	}
	row := "/" + strings.Join(keys[:4], "/")
	this, _ := valueAt(read.value, row+"/this_month")
	last, _ := valueAt(read.value, row+"/last_month")
	movement, ok := derive("difference", []string{this, last})
	return row, movement, ok
}

// checkRemainder holds a stated movement and its drivers together: when a sentence attributes a cited total change to
// drivers of the same row whose movements do not add up to it, it must say what remains. The total is the row's cited
// change that is not a driver; drivers may offset (+500 and −400 explain a net 100). Each row is held on its own, so a
// second total in the sentence ("while expenses rose 3,100") never excuses the first's missing remainder.
func checkRemainder(line string, totals, drivers map[string][]*big.Rat, at map[string]int, labels []string) string {
	lower := strings.ToLower(line)
	for _, label := range labels {
		if l := strings.ToLower(strings.TrimSpace(label)); len(l) >= 3 {
			lower = strings.ReplaceAll(lower, l, strings.Repeat("#", len(l)))
		}
	}
	if !claimCausal.MatchString(lower) && !claimAttribution.MatchString(lower) {
		return ""
	}
	for _, group := range slices.Sorted(maps.Keys(totals)) {
		if len(totals[group]) != 1 || len(drivers[group]) == 0 {
			continue
		}
		total, rest := totals[group][0], new(big.Rat)
		for _, d := range drivers[group] {
			rest.Add(rest, d)
		}
		// The row's part of the sentence runs from where its total is stated to where the next row's is.
		from, to := at[group], len(lower)
		for other, start := range at {
			if other != group && start > from && start < to {
				to = start
			}
		}
		if rest.Cmp(total) == 0 || claimRemainder.MatchString(lower[from:to]) {
			continue
		}
		gap := new(big.Rat).Sub(total, rest)
		return "the drivers stated account for " + rest.FloatString(2) + " of " + total.FloatString(2) + "; say what the remaining " + gap.FloatString(2) + " is"
	}
	return ""
}

// rowGroup is the row a change or driver belongs to: the path before its drivers ("/changes/0" for a payee of the
// first changed account, "/rows/0" for /rows/0/drivers/1/change), or the object holding the field.
func rowGroup(step, path string) string {
	keys := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, k := range keys {
		if k == "drivers" || k == "new" || k == "missing" || k == "changed" || k == "unattributed" {
			return step + "/" + strings.Join(keys[:i], "/")
		}
	}
	return step + "/" + strings.Join(keys[:max(0, len(keys)-1)], "/")
}

// figurelessClaim holds a sentence that states a money field's direction or standing without its figure ("net income
// rose", "cash is available"): the direction is the change's sign, an amount is never said to have moved, book cash is
// never called available, and a stale read is said to be stale. changeCited says another field of the sentence is a
// change, which a direction may then describe.
func figurelessClaim(line string, c claim, got string, changeCited bool) string {
	lower := strings.ToLower(line)
	// The directions said of this field: those in the clauses that name it ("revenue rose while expenses fell"),
	// else the sentence's.
	scope := ""
	start := 0
	for _, b := range append(claimClause.FindAllStringIndex(line, -1), []int{len(line), len(line)}) {
		clause := line[start:b[0]]
		if labelNamed(clause, c.label) || overlaps(metricsIn(clause), c.keys) {
			scope += " " + clause
		}
		start = b[1]
	}
	if scope == "" {
		scope = line
	}
	rise, fall := false, false
	for _, w := range wordsOf(scope) {
		rise = rise || claimRiseVerb[w]
		fall = fall || claimFallVerb[w]
	}
	field, _ := new(big.Rat).SetString(strings.TrimSuffix(normalizeFigure(got), "%"))
	switch {
	case c.kind == "change" && field != nil && (rise && field.Sign() < 0 || fall && field.Sign() > 0):
		return "the sentence says " + c.describe() + " moved the other way: the cited change is " + normalizeFigure(got)
	case c.kind != "change" && (rise || fall) && !changeCited:
		return "the sentence says " + c.describe() + " moved, but the cited field is an amount, not a change"
	case c.keys["cash"] && claimAvailable.MatchString(lower):
		return "the sentence calls book cash available at the bank"
	case c.stale && !claimStaleSaid.MatchString(lower):
		return "the sentence states " + c.describe() + " as current, but the cited read is stale"
	case c.forecast && !claimForecast.MatchString(lower):
		return "the sentence states the forecast " + c.describe() + " as a fact"
	case c.scenario && !strings.Contains(lower, "scenario") && !strings.Contains(lower, "if ") && !strings.Contains(lower, "what-if"):
		return "the sentence states the scenario " + c.describe() + " without saying it is a scenario"
	case c.incomplete && !claimIncompleteSaid.MatchString(lower):
		return "the sentence states the forecast without saying its inputs are incomplete"
	}
	return ""
}

// derive computes a figure the answer states from fields it names: their sum, the first less the others, or the first
// as a percentage of the second, exactly. ok is false when an input is not a figure or the operation cannot apply.
func derive(op string, inputs []string) (*big.Rat, bool) {
	values := []*big.Rat{}
	for _, in := range inputs {
		n := normalizeFigure(in)
		if n == "" || strings.HasSuffix(n, "%") || strings.Count(n, "-") > 1 {
			return nil, false
		}
		r, ok := new(big.Rat).SetString(n)
		if !ok {
			return nil, false
		}
		values = append(values, r)
	}
	switch op {
	case "sum":
		out := new(big.Rat)
		for _, v := range values {
			out.Add(out, v)
		}
		return out, len(values) >= 2
	case "difference":
		if len(values) < 2 {
			return nil, false
		}
		out := new(big.Rat).Set(values[0])
		for _, v := range values[1:] {
			out.Sub(out, v)
		}
		return out, true
	case "ratio":
		if len(values) != 2 || values[1].Sign() == 0 {
			return nil, false
		}
		out := new(big.Rat).Quo(values[0], values[1])
		return out.Mul(out, big.NewRat(100, 1)), true
	}
	return nil, false
}

// derivedMatches says whether a stated figure is the derived value: exactly for amounts, and for a percentage at the
// precision it is stated to.
func derivedMatches(op, stated string, value *big.Rat) bool {
	n := normalizeFigure(stated)
	if op == "ratio" {
		if !strings.HasSuffix(n, "%") {
			return false
		}
		n = strings.TrimSuffix(n, "%")
		// The precision is the one written ("17.0%" is to one place), not the normalized figure's.
		decimals := 0
		written := strings.Trim(stated, "()−-$% ")
		if i := strings.Index(written, "."); i >= 0 {
			decimals = len(strings.TrimRight(written[i+1:], "% )"))
		}
		want, ok := new(big.Rat).SetString(n)
		if !ok {
			return false
		}
		// Round half away from zero at the stated precision.
		scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
		scaled := new(big.Rat).Mul(value, scale)
		half := big.NewRat(1, 2)
		if scaled.Sign() < 0 {
			scaled.Sub(scaled, half)
		} else {
			scaled.Add(scaled, half)
		}
		whole := new(big.Int).Quo(scaled.Num(), scaled.Denom())
		return new(big.Rat).SetFrac(whole, scale.Num()).Cmp(want) == 0
	}
	want, ok := new(big.Rat).SetString(n)
	return ok && !strings.HasSuffix(n, "%") && want.Cmp(value) == 0
}

// entitiesIn collects the client names the reads carry, so a sentence naming another client is caught.
func entitiesIn(reads map[string]agentRead) []string {
	seen := map[string]bool{}
	out := []string{}
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > 8 {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			for key, value := range t {
				if s, ok := value.(string); ok && (key == "entity" || key == "client_entity") && strings.TrimSpace(s) != "" && !seen[s] {
					seen[s] = true
					out = append(out, s)
				} else {
					walk(value, depth+1)
				}
			}
		case []any:
			for i, value := range t {
				if i >= 500 {
					break
				}
				walk(value, depth+1)
			}
		}
	}
	for _, read := range reads {
		if read.entity != "" && !seen[read.entity] {
			seen[read.entity] = true
			out = append(out, read.entity)
		}
		walk(read.value, 0)
	}
	return out
}

var calendarFigure = regexp.MustCompile(`^\d{4}-\d{2}(?:-\d{2})?$`)

// isCalendar says whether a normalized figure is a month or a day rather than an amount.
func isCalendar(n string) bool { return calendarFigure.MatchString(n) }

// deriveSupport checks a figure the answer computed from fields: the server computes it exactly from the fields named,
// the sentence states that figure and names every field it is computed from, and the fields agree in kind, currency,
// forecast and freshness. It binds the figure and cites each field.
func deriveSupport(s agentSupportEntry, line string, reads map[string]agentRead, entities []string, bound map[string]bool, cite func(label, href string)) string {
	if s.path != "" || s.quote != "" {
		return "a derived figure cites its fields in of, not a path or quote of its own"
	}
	if s.derive != "sum" && s.derive != "difference" && s.derive != "ratio" {
		return "a derived figure's operation is not sum, difference or ratio"
	}
	if len(s.of) < 2 || len(s.of) > 8 || s.derive == "ratio" && len(s.of) != 2 {
		return "a derived figure names too few or too many fields"
	}
	values, claims := []string{}, []claim{}
	seenInputs := map[string]bool{}
	for _, in := range s.of {
		// A field counted twice is not a second figure.
		if seenInputs[in.step+in.path] {
			return "a derived figure names the same field twice"
		}
		seenInputs[in.step+in.path] = true
		read, ok := reads[in.step]
		if !ok {
			return "a derived figure names a read that did not run"
		}
		got, found := valueAt(read.value, in.path)
		if read.doc || !found {
			return "a derived figure names a field that does not exist"
		}
		if n := normalizeFigure(got); n == "" || isCalendar(n) || strings.HasSuffix(n, "%") {
			return "a derived figure names a field that is not an amount"
		}
		values = append(values, got)
		c := claimOf(read, in.path)
		// Money is computed with money and counts with counts; a ratio may compare two counts, never a count and money.
		if len(claims) > 0 && c.money != claims[0].money {
			return "a derived figure combines money with a count"
		}
		if !c.money && s.derive != "ratio" {
			return "a derived money figure names a count (" + c.describe() + ")"
		}
		claims = append(claims, c)
		cite(read.title+" › "+c.describe(), fieldHref(read, in.path))
	}
	computed, ok := derive(s.derive, values)
	if !ok {
		return "a derived figure cannot be computed from its fields"
	}
	// A difference may be stated as its magnitude ("fell 2,000.00" for -2,000.00); its direction is then checked
	// against the sign below.
	magnitude := new(big.Rat).Abs(computed)
	if !derivedMatches(s.derive, s.value, computed) && !(s.derive == "difference" && derivedMatches(s.derive, s.value, magnitude)) {
		return "the " + s.derive + " of the cited fields is " + computed.FloatString(2) + ", not " + s.value
	}
	stated := normalizeFigure(s.value)
	if s.derive == "difference" && computed.Sign() < 0 && !strings.HasPrefix(stated, "-") {
		stated = "-" + stated
	}
	found, matched := false, []string{}
	for _, o := range figuresIn(line) {
		// As written in the sentence, at its written precision.
		raw := line[o.start:o.end]
		exact := o.normalized == stated && derivedMatches(s.derive, raw, computed)
		// A magnitude needs a stated direction, which derivedResult holds to the sign.
		asMagnitude := s.derive == "difference" && computed.Sign() < 0 && derivedMatches(s.derive, raw, magnitude) && o.direction() != ""
		if exact || asMagnitude {
			found = true
			matched = append(matched, o.normalized)
			if why := derivedResult(s.derive, o, claims, computed); why != "" {
				return why
			}
		}
	}
	if !found {
		return "a derived figure is not in its sentence as computed"
	}
	named := metricsIn(line)
	lower := strings.ToLower(line)
	// What the result is called must be a measure every input shares (for a ratio, the numerator's): rent plus
	// marketing is never net income, nor net income plus rent. A measure with an input's figure between it and the
	// result names that input ("less expenses of 106,620.00 leave 21,780.00").
	shared := map[string]bool{}
	for k := range claims[0].keys {
		shared[k] = true
	}
	if s.derive != "ratio" {
		for _, c := range claims[1:] {
			for k := range shared {
				if !c.keys[k] {
					delete(shared, k)
				}
			}
		}
	}
	for _, o := range figuresIn(line) {
		if o.normalized != stated {
			continue
		}
		// A gap ("patient fees exceed total expense by 21,780.00") names what it compares, not what it is.
		if s.derive == "difference" && o.direction() == "change" {
			continue
		}
		if label := resultLabel(line, o); len(label) > 0 && !overlaps(label, shared) {
			return "the sentence calls the " + s.derive + " " + stated + " " + keysNamed(label) + ", which its fields do not all measure"
		}
	}
	clientsOf, bases, months := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, c := range claims {
		if c.entity != "" {
			clientsOf[strings.ToLower(c.entity)] = true
		}
		if c.basis != "" {
			bases[c.basis] = true
		}
		if !c.forecast && len(c.period) == 7 {
			months[c.period] = true
		}
	}
	if len(bases) > 1 {
		return "a derived figure combines figures on different accounting bases"
	}
	// Every input's currency is the same, and a known currency is never mixed with an unknown one from another read.
	currencies, unknownFrom, knownFrom := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i, c := range claims {
		if c.currency != "" {
			currencies[c.currency] = true
			knownFrom[s.of[i].step] = true
		} else {
			unknownFrom[s.of[i].step] = true
		}
	}
	if len(currencies) > 1 {
		return "a derived figure mixes currencies"
	}
	if len(currencies) == 1 {
		for step := range unknownFrom {
			if !knownFrom[step] {
				return "a derived figure combines a figure of unknown currency with one in " + slices.Sorted(maps.Keys(currencies))[0]
			}
		}
	}
	if len(clientsOf) > 1 {
		named := map[string]bool{}
		for _, e := range clientsNamed(line, entities) {
			named[strings.ToLower(e)] = true
		}
		for e := range clientsOf {
			if !named[e] {
				return "a derived figure combines several clients' figures; the sentence must name each"
			}
		}
	}
	// Figures of several months are named month by month, except one line's change between its two months.
	movement := s.derive == "difference" && len(claims) == 2 && strings.EqualFold(claims[0].label, claims[1].label) && strings.EqualFold(claims[0].entity, claims[1].entity)
	if len(months) > 1 && !movement {
		named := namedMonths(line)
		ordered := []string{}
		for m := range months {
			ordered = append(ordered, m)
		}
		sort.Strings(ordered)
		for _, m := range ordered {
			p, _ := time.Parse("2006-01", m)
			found := false
			for _, n := range named {
				found = found || n[0] == int(p.Month()) && (n[1] == 0 || n[1] == p.Year())
			}
			if !found {
				return "a derived figure combines several months; the sentence must name " + p.Format("January 2006")
			}
		}
	}
	for i, c := range claims {
		if len(c.keys) > 0 && overlaps(named, c.keys) || labelNamed(line, c.label) {
		} else {
			return "the sentence does not name " + c.describe() + ", which its figure is computed from"
		}
		if (c.kind == "change") != (claims[0].kind == "change") {
			return "a derived figure mixes changes and amounts"
		}
		amountKind := func(k string) string {
			if k == "prior" {
				return "level"
			}
			return k
		}
		if amountKind(c.kind) != amountKind(claims[0].kind) {
			return "a derived figure mixes a " + c.kind + " with a " + claims[0].kind
		}
		if c.forecast != claims[0].forecast || c.scenario != claims[0].scenario {
			return "a derived figure mixes forecast and actual figures"
		}
		if c.ytd != claims[0].ytd {
			return "a derived figure mixes a month's figure with a year-to-date one"
		}

		if c.forecast && !claimForecast.MatchString(lower) {
			return "the sentence computes from a forecast without saying so"
		}
		if c.stale && !claimStaleSaid.MatchString(lower) {
			return "the sentence computes from a stale read without saying so"
		}
		if c.incomplete && !claimIncompleteSaid.MatchString(lower) {
			return "the sentence computes from a forecast with incomplete inputs without saying so"
		}
		if !c.forecast && len(c.period) == 7 && len(months) == 1 {
			if months := namedMonths(line); len(months) > 0 {
				p, _ := time.Parse("2006-01", c.period)
				match := false
				for _, m := range months {
					match = match || m[0] == int(p.Month()) && (m[1] == 0 || m[1] == p.Year())
				}
				if !match {
					return "the sentence puts a figure it computes from in another month; field " + strconvItoa(i) + " is for " + p.Format("January 2006")
				}
			}
		}
		// A client the sentence names must be one the inputs are of, exactly.
		if c.entity != "" {
			for _, e := range clientsNamed(line, entities) {
				if !clientsOf[strings.ToLower(e)] {
					return "the sentence names " + e + ", but its figure is computed from " + c.entity + "'s"
				}
			}
		}
	}
	for _, n := range matched {
		bound[n] = true
	}
	return ""
}

func strconvItoa(n int) string { return big.NewInt(int64(n)).String() }

// firmClients is every client name the firm has closes for, so the support gate knows a client a sentence names even
// when no read mentioned it. A failed read returns none; the reads' own clients are still checked.
func firmClients(ctx context.Context, db *sql.DB, org string) []string {
	rows, err := db.QueryContext(ctx, `SELECT DISTINCT entity FROM client_closes WHERE organization_id=$1 LIMIT 5000`, org)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil && strings.TrimSpace(name) != "" {
			out = append(out, name)
		}
	}
	return out
}

// derivedResult holds what the sentence says a computed figure is: a sum or a ratio is an amount, never a movement; a
// difference may be stated as a rise or fall only between the same line's two months, in its sign; a gap between
// different lines is stated as a gap ("exceeds by", "less than"). A currency stated beside it is the inputs'.
func derivedResult(op string, o occurrence, claims []claim, computed *big.Rat) string {
	direction := o.direction()
	// The same line at two times: two months' amounts, or one row's current and prior fields (amount and prior,
	// this month and last month).
	sameLine := false
	if len(claims) == 2 && strings.EqualFold(claims[0].label, claims[1].label) && strings.EqualFold(claims[0].entity, claims[1].entity) {
		a, b := claims[0], claims[1]
		sameLine = a.period != "" && b.period != "" && a.period != b.period ||
			a.kind != b.kind && (a.kind == "prior" || b.kind == "prior") ||
			a.field == "this month" && b.field == "last month" || a.field == "last month" && b.field == "this month"
	}
	// A movement verb before the figure ("rose", "fell"), not a comparison ("exceeds by", "more than").
	rose, fell := false, false
	for _, w := range o.before {
		rose = rose || claimRiseVerb[w]
		fell = fell || claimFallVerb[w]
	}
	moved := rose || fell
	if rose {
		direction = "rise"
	} else if fell {
		direction = "fall"
	}
	switch op {
	case "sum", "ratio":
		if direction == "rise" || direction == "fall" || direction == "change" {
			return "the sentence states the " + op + " " + o.normalized + " as a change"
		}
	case "difference":
		// A difference is said to be one: a movement, a gap or a remainder, never a plain balance.
		if direction == "" && !claimDifferenceSaid.MatchString(strings.ToLower(o.clause)) {
			return "the sentence states the difference " + o.normalized + " as an amount; say it is a difference, a change or what is left"
		}
		if moved && !sameLine {
			return "the sentence states the difference " + o.normalized + " between different lines as a movement"
		}
		// "from September to October" is the second operand's month to the first's.
		if m := claimFromTo.FindStringSubmatch(o.clause); m != nil && len(claims) == 2 {
			from, to := namedMonths(m[1]), namedMonths(m[2])
			if len(from) == 1 && len(to) == 1 && claims[0].period != "" && claims[1].period != "" {
				first, _ := time.Parse("2006-01", claims[0].period)
				second, _ := time.Parse("2006-01", claims[1].period)
				if to[0][0] != int(first.Month()) || from[0][0] != int(second.Month()) {
					return "the sentence states the difference from " + m[1] + " to " + m[2] + ", but it is computed from " + second.Format("January") + " to " + first.Format("January")
				}
			}
		}
		if moved && (direction == "rise" && computed.Sign() < 0 || direction == "fall" && computed.Sign() > 0) {
			return "the sentence states a " + direction + " of " + o.normalized + ", but the computed difference is " + computed.FloatString(2)
		}
	}
	for _, m := range claimCurrency.FindAllString(o.clause, -1) {
		code := map[string]string{"€": "EUR", "£": "GBP", "₹": "INR", "¥": "JPY"}[m]
		if code == "" {
			code = strings.ToUpper(m)
		}
		for _, c := range claims {
			if c.currency != "" && (code == "$" && !dollarFamily[c.currency] || code != "$" && code != c.currency) {
				return "the sentence states " + o.normalized + " in " + m + ", but its fields are in " + c.currency
			}
		}
	}
	return ""
}

// movedLevel is a level a sentence states as the result of a movement ("rose to"), awaiting the movement's evidence.
type movedLevel struct {
	claim     claim
	got       string
	direction string
}

// movementShown says whether the sentence's other cited fields show the movement a level is said to be the result of:
// a change of the same line in that direction, or its prior month's amount on the right side of it.
func movementShown(c claim, got, direction string, cited []moneyEntry) string {
	level, ok := new(big.Rat).SetString(strings.TrimSuffix(normalizeFigure(got), "%"))
	if !ok {
		return ""
	}
	for _, m := range cited {
		if !strings.EqualFold(m.claim.label, c.label) || m.claim.entity != "" && c.entity != "" && !isSameClient(m.claim.entity, c.entity) {
			continue
		}
		// A change is of the level's month; a prior amount is of the month before it.
		if c.period != "" && m.claim.period != "" {
			level, _ := time.Parse("2006-01", c.period)
			want := c.period
			if m.claim.kind == "prior" {
				want = level.AddDate(0, -1, 0).Format("2006-01")
			}
			if m.claim.period != want {
				continue
			}
		}
		v, ok := new(big.Rat).SetString(strings.TrimSuffix(m.n, "%"))
		if !ok {
			continue
		}
		switch m.claim.kind {
		case "change":
			if direction == "rise-to" && v.Sign() > 0 || direction == "fall-to" && v.Sign() < 0 {
				return ""
			}
		case "prior":
			if direction == "rise-to" && level.Cmp(v) > 0 || direction == "fall-to" && level.Cmp(v) < 0 {
				return ""
			}
		}
	}
	verb := map[string]string{"rise-to": "rose", "fall-to": "fell"}[direction]
	return "the sentence says " + c.describe() + " " + verb + " to " + normalizeFigure(got) + ", but nothing cited shows it " + verb
}

// resultLabel is the measure that names a computed figure: the one nearest before it in its clause with no other
// figure between (else the nearest after, with none between); a measure separated by an input's figure names that input.
func resultLabel(line string, o occurrence) map[string]bool {
	start := strings.Index(line, o.clause)
	if start < 0 {
		return nil
	}
	var pick *metricAt
	for _, m := range metricsAt(o.clause) {
		m := m
		if start+m.end <= o.start {
			if !agentFigure.MatchString(line[start+m.end : o.start]) {
				pick = &m
			}
		} else if start+m.start >= o.end && pick == nil && !agentFigure.MatchString(line[o.end:start+m.start]) {
			pick = &m
			break
		}
	}
	if pick == nil {
		return nil
	}
	out := map[string]bool{}
	for _, k := range pick.keys {
		out[k] = true
	}
	return out
}

// clientsNamed is the clients a text names, matched whole and longest first, so "Acme Europe" is never read as "Acme"
// and "Acme" never matches inside "Acme Europe".
func clientsNamed(text string, clients []string) []string {
	ordered := append([]string(nil), clients...)
	sort.SliceStable(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	// Rune by rune, lowercased one rune to one rune, so offsets in the lowered and original text always agree.
	original := []rune(text)
	lower := make([]rune, len(original))
	for i, r := range original {
		lower[i] = unicode.ToLower(r)
	}
	out := []string{}
	boundary := func(b []rune, i int) bool {
		return i < 0 || i >= len(b) || !unicode.IsLetter(b[i]) && !unicode.IsDigit(b[i])
	}
	index := func(haystack, needle []rune, from int) int {
		for i := from; i+len(needle) <= len(haystack); i++ {
			match := true
			for j := range needle {
				if haystack[i+j] != needle[j] {
					match = false
					break
				}
			}
			if match {
				return i
			}
		}
		return -1
	}
	for _, name := range ordered {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		// A plain search, then whole-word edges: no pattern is compiled per client. A short name ("GE") is matched in
		// its own case, so the word "ge" in a sentence is not a client.
		want, haystack := []rune(trimmed), original
		if len(want) >= 3 {
			for i, r := range want {
				want[i] = unicode.ToLower(r)
			}
			haystack = lower
		}
		for from := 0; ; {
			i := index(haystack, want, from)
			if i < 0 {
				break
			}
			if boundary(haystack, i-1) && boundary(haystack, i+len(want)) {
				out = append(out, trimmed)
				for j := i; j < i+len(want); j++ {
					if lower[j] != ' ' {
						lower[j], original[j] = '#', '#'
					}
				}
				break
			}
			from = i + 1
		}
	}
	return out
}

// isSameClient says whether two client names are the same client, exactly.
func isSameClient(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// effectNamed says whether a passage names the effect a causal sentence claims: a measure the effect names, or the line
// of a field cited in the sentence that the effect names.
func effectNamed(effect, passage string, claims map[int]claim) bool {
	if overlaps(metricsIn(passage), metricsIn(effect)) {
		return true
	}
	for _, c := range claims {
		if c.label != "" && labelNamed(effect, c.label) && labelNamed(passage, c.label) {
			return true
		}
	}
	return false
}
