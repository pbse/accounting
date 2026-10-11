package bookfiles

import (
	"regexp"
	"strings"
)

// Mapping a file's accounts (docs/books-sources.md, How an upload becomes the month's books, step 4): each account
// needs a class and, where the checks use one, a type, as a QuickBooks capture's account directory gives them today, so
// a month on files reads in the statements and checks as a connected ledger's does. The reader proposes them; a person
// confirms.

// Types are the types each class's accounts take: the groups the management pack and the checks read.
var Types = map[string][]string{
	"Asset":     {"Bank", "Accounts Receivable", "Other Current Asset", "Fixed Asset", "Other Asset"},
	"Liability": {"Accounts Payable", "Credit Card", "Other Current Liability", "Long Term Liability"},
	"Equity":    {"Equity"},
	"Revenue":   {"Income", "Other Income"},
	"Expense":   {"Cost of Goods Sold", "Expense", "Other Expense"},
}

// ValidType says whether typ is one of class's types.
func ValidType(class, typ string) bool {
	for _, t := range Types[class] {
		if t == typ {
			return true
		}
	}
	return false
}

// generic is the type a class's account takes when nothing says more.
var generic = map[string]string{"Asset": "Other Current Asset", "Liability": "Other Current Liability", "Equity": "Equity", "Revenue": "Income", "Expense": "Expense"}

// Proposal is the class and type proposed for an account, and what they were read from: "type" (the file's own type
// column, Hint), "heading" (the heading the account sits under, Hint), "name", "number", or "" when nothing says.
type Proposal struct {
	Class string `json:"class"`
	Type  string `json:"type"`
	From  string `json:"from"`
	Hint  string `json:"hint,omitempty"`
}

// rule reads a class and type from words: specific when the words name the type, not only the class.
type rule struct {
	words    *regexp.Regexp
	class    string
	typ      string
	specific bool
}

// rules are read in order, the first that matches deciding: narrower words before broader ones ("bank charges" is an
// expense before "bank" is a bank; "cost of sales" before "sales"; "sales tax payable" before "sales").
var rules = func() []rule {
	r := func(words, class, typ string, specific bool) rule {
		return rule{regexp.MustCompile(`(?i)` + words), class, typ, specific}
	}
	return []rule{
		r(`\bbank\s+(charges?|fees?|service|interest\s+(paid|expense))\b|\bmerchant\s+fees?\b|\b(equipment|vehicles?|computers?|office|furniture)\s+(rental|rent|hire|lease|leasing|repairs?|maintenance|expenses?)\b|\bcredit\s*card\s+(processing\s+)?(fees?|charges?|costs?)\b|\b(card\s+)?processing\s+fees?\b|\b(sales|selling)\s+(commissions?|expenses?|costs?|salar(y|ies)|wages|travel|promotions?|meetings?)\b`, "Expense", "Expense", false),
		r(`\baccumulated\s+(depreciation|amorti[sz]ation)\b`, "Asset", "Fixed Asset", true),
		r(`\b(loan|mortgage|note|debt)s?\s+(interest|fees?|costs?|charges?)\b`, "Expense", "Other Expense", true),
		r(`\b(bank\s+)?overdrafts?\b`, "Liability", "Other Current Liability", false),
		r(`\bcredit\s*cards?\b|\b(amex|american\s+express|visa|mastercard)\b`, "Liability", "Credit Card", true),
		r(`\baccounts?\s+receivable\b|\breceivables?\b|\bdebtors?\b`, "Asset", "Accounts Receivable", true),
		r(`\baccounts?\s+payable\b|\btrade\s+(payables?|creditors?)\b|\bcreditors?\b`, "Liability", "Accounts Payable", true),
		r(`\b(non-?current|long[\s-]term)\s+liabilit|\bloans?\b|\bmortgages?\b|\bnotes?\s+payable\b|\bline\s+of\s+credit\b|\bdebentures?\b`, "Liability", "Long Term Liability", true),
		r(`\b(sales|use|income|payroll|vat|gst|hst|pst)\s+tax(es)?\s+payable\b|\btax(es)?\s+payable\b|\bpayroll\s+liabilit|\baccru(ed|als?)\b|\bdeferred\s+revenue\b|\bunearned\b|\bcustomer\s+deposits?\b|\b(current\s+)?liabilit(y|ies)\b|\b(vat|gst|hst)\b|\bpayable\b`, "Liability", "Other Current Liability", false),
		r(`\bcost\s+of\s+(goods|sales|revenue)\b|\bcogs\b|\bdirect\s+costs?\b|\bpurchases\b|\bsubcontract`, "Expense", "Cost of Goods Sold", true),
		r(`\bother\s+income\b|\binterest\s+(income|earned|received)\b|\bgain\s+on\b|\bdividends?\s+(income|received)\b|\bforeign\s+exchange\s+gain\b`, "Revenue", "Other Income", true),
		r(`\bother\s+expenses?\b|\binterest\s+(expense|paid)\b|\bloss\s+on\b|\bincome\s+tax(es)?(\s+expense)?\b`, "Expense", "Other Expense", true),
		r(`\b(retained\s+earnings|owners?'?s?\s+(equity|capital|draws?|drawings|contributions?|investments?)|equity|capital(\s+stock)?|drawings?|distributions?|dividends?(\s+paid)?|share(s|holders?')?\s+(capital|premium)|common\s+stock|opening\s+balance\s+equity|partners?'?\s+capital)\b`, "Equity", "Equity", true),
		r(`\b(sales|revenues?|income|turnover|fees\s+earned|service\s+fees|consulting\s+fees)\b`, "Revenue", "Income", false),
		r(`\b(bank|checking|cheque|savings|cash\s+(at|in)\s+bank|petty\s+cash|cash\s+on\s+hand|money\s+market|paypal|stripe)\b|^cash$`, "Asset", "Bank", true),
		r(`\b(fixed|non-?current|tangible)\s+assets?\b|\b(equipment|furniture|fixtures|vehicles?|buildings?|land|machinery|computers?|leasehold|plant)\b`, "Asset", "Fixed Asset", true),
		r(`\b(intangible|goodwill|other\s+assets?|security\s+deposits?)\b`, "Asset", "Other Asset", true),
		r(`\b(prepaid|prepayments?|inventory|stock\s+on\s+hand|undeposited|deposits?|advances?|current\s+assets?|assets?)\b`, "Asset", "Other Current Asset", false),
		r(`\b(expenses?|overheads?|depreciation|amorti[sz]ation|rent|wages|salar(y|ies)|payroll|utilities|insurance|advertising|marketing|office|travel|meals|software|supplies|repairs|maintenance|professional|legal|accounting|telephone|postage|dues|subscriptions|commissions?|freight|shipping|fuel|training|licen[cs]es?|bad\s+debts?)\b`, "Expense", "Expense", false),
	}
}()

// classOf reads a class and type from words, and whether the words name the type.
func classOf(words string) (class, typ string, specific bool) {
	words = strings.TrimSpace(words)
	if words == "" {
		return "", "", false
	}
	// A type a ledger writes out exactly, as QuickBooks Desktop's and Online's are, is that type.
	for c, types := range Types {
		for _, t := range types {
			if strings.EqualFold(words, t) {
				return c, t, true
			}
		}
	}
	for _, r := range rules {
		if r.words.MatchString(words) {
			return r.class, r.typ, r.specific
		}
	}
	return "", "", false
}

// numbered reads a class from an account number in the common numbering (1 assets, 2 liabilities, 3 equity, 4 revenue,
// 5 cost of sales, 6 and 7 expenses): a guess, said to be one, for a person to check.
func numbered(number string) (class, typ string) {
	digits := strings.TrimLeft(strings.TrimSpace(number), "0")
	if len(digits) < 3 || digits[0] < '1' || digits[0] > '7' {
		return "", ""
	}
	switch digits[0] {
	case '1':
		return "Asset", generic["Asset"]
	case '2':
		return "Liability", generic["Liability"]
	case '3':
		return "Equity", "Equity"
	case '4':
		return "Revenue", "Income"
	case '5':
		return "Expense", "Cost of Goods Sold"
	}
	return "Expense", "Expense"
}

// Propose proposes a class and type for an account from what the file says of it: its type column first, then the
// heading it sits under, then its name, then its number. The first that names a class decides it; when that one names
// only the class, the name refines the type if it names one of the same class ("Checking" under "Current Assets" is a
// bank).
func Propose(typeText, heading, name, number string) Proposal {
	byName, nameType, nameSpecific := classOf(name)
	refine := func(p Proposal, specific bool) Proposal {
		if !specific && byName == p.Class && nameSpecific {
			p.Type = nameType
		}
		return p
	}
	if c, t, specific := classOf(typeText); c != "" {
		return refine(Proposal{Class: c, Type: t, From: "type", Hint: strings.TrimSpace(typeText)}, specific)
	}
	if c, t, specific := classOf(heading); c != "" {
		return refine(Proposal{Class: c, Type: t, From: "heading", Hint: strings.TrimSpace(heading)}, specific)
	}
	if byName != "" {
		return Proposal{Class: byName, Type: nameType, From: "name"}
	}
	if c, t := numbered(number); c != "" {
		return Proposal{Class: c, Type: t, From: "number"}
	}
	return Proposal{}
}

// AccountKey is how an account is known from file to file: its number when it has one, else its name, ignoring case
// and spacing.
func AccountKey(number, name string) string {
	if n := strings.Join(strings.Fields(number), ""); n != "" {
		return "n:" + strings.ToLower(n)
	}
	return "a:" + strings.ToLower(strings.Join(strings.Fields(name), " "))
}
