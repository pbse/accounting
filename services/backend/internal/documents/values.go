package documents

import (
	"math/big"
	"regexp"
	"strings"
	"time"
)

// A number as printed: thousands groups of three, or plain digits, with optional decimals.
var printedNumber = regexp.MustCompile(`\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?`)

// Dates written with a month name, whose day and year are not amounts: Sep 2, 2026; 2nd January 2026; March 2026.
const monthName = `(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sept?(?:ember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\.?`

var writtenDates = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b` + monthName + `[ -]\d{1,2}(?:st|nd|rd|th)?\b(?:,?[ -]\d{2,4}\b)?`),
	regexp.MustCompile(`(?i)\b\d{1,2}(?:st|nd|rd|th)?[ -]` + monthName + `(?:,?[ -]\d{2,4}\b)?`),
	regexp.MustCompile(`(?i)\b` + monthName + `,?[ -]\d{4}\b`),
}

// AmountIn reports whether text prints the amount, ignoring its sign, currency signs and
// thousands separators. A printed 1,200.00 shows 1200 or 1200.00, never 12000.00 or 1200.99.
func AmountIn(text, amount string) bool { return amountIn(text, amount, false) }

// MoneyIn reports whether text prints the amount as money: as AmountIn, except that a number printed as something
// else does not count. "12 months", "5.25%", "#1200" and "page 3" are a term, a rate, a reference and a page, so
// a matching number alone never establishes an amount's meaning.
func MoneyIn(text, amount string) bool { return amountIn(text, amount, true) }

// A number followed by one of these is a quantity, rate or term, not money; one preceded by these is a reference.
var (
	notMoneyAfter  = regexp.MustCompile(`(?i)^\s*-?\s*(?:%|per\s?cent\b|months?\b|mos?\b\.?|years?\b|yrs?\b|days?\b|weeks?\b|hours?\b|hrs?\b|units?\b|pcs\b|pieces\b|items\b|qty\b|employees\b|people\b|shares\b|sq\.?\s?ft\b|feet\b|miles\b|kg\b|lbs?\b|x\b)`)
	notMoneyBefore = regexp.MustCompile(`(?i)(?:#|\bno\.?|\bnumber|\bpage|\bp\.|\bref\.?|\binvoice|\bpolicy|\baccount|\bacct\.?|\bid|\bline)\s*:?\s*$`)
)

func amountIn(text, amount string, money bool) bool {
	want, ok := new(big.Rat).SetString(strings.TrimPrefix(amount, "-"))
	if !ok || strings.ContainsAny(amount, "eE/") {
		return false
	}
	digitAt := func(i int) bool { return i >= 0 && i < len(text) && isDigit(text[i]) }
	inDate := make([]bool, len(text))
	for _, pattern := range writtenDates {
		for _, at := range pattern.FindAllStringIndex(text, -1) {
			// "May 5.00" is a word and an amount, not a date.
			if at[1] < len(text) && text[at[1]] == '.' && digitAt(at[1]+1) {
				continue
			}
			for i := at[0]; i < at[1]; i++ {
				inDate[i] = true
			}
		}
	}
	for _, at := range printedNumber.FindAllStringIndex(text, -1) {
		token, before, after := text[at[0]:at[1]], byte(0), byte(0)
		if at[0] > 0 {
			before = text[at[0]-1]
		}
		if at[1] < len(text) {
			after = text[at[1]]
		}
		switch {
		// The tail of a longer number, or a part of a date such as 2026-09-02, 9/2/26 or 02.01.2026.
		case isDigit(before) || before == '.' || inDate[at[0]],
			(before == '-' || before == '/') && digitAt(at[0]-2),
			(after == '-' || after == '/' || after == '.') && digitAt(at[1]+1),
			// 09 is a day or month, not an amount.
			len(token) > 1 && token[0] == '0' && token[1] != '.':
			continue
		}
		if money && (notMoneyAfter.MatchString(text[at[1]:]) || notMoneyBefore.MatchString(text[:at[0]])) {
			continue
		}
		got, _ := new(big.Rat).SetString(strings.ReplaceAll(token, ",", ""))
		if got != nil && got.Cmp(want) == 0 {
			return true
		}
	}
	return false
}

// Ordinal days (2nd) and the abbreviation Sept, which Go's layouts do not print.
var ordinalDay, sept = regexp.MustCompile(`(\d)(?:st|nd|rd|th)\b`), regexp.MustCompile(`\bsept\b`)

// DateIn reports whether text prints the date (YYYY-MM-DD) or month (YYYY-MM) in a common form with
// its year, or also without one when withoutYear is set, as statement rows print it.
func DateIn(text, date string, withoutYear bool) bool {
	layouts := []string{"2006-01-02", "2006/01/02", "01/02/2006", "1/2/2006", "02/01/2006", "2/1/2006", "01-02-2006", "02-01-2006", "02.01.2006", "01/02/06", "02/01/06", "1/2/06", "2/1/06",
		"Jan 2, 2006", "Jan 02, 2006", "Jan. 2, 2006", "January 2, 2006", "January 02, 2006", "Jan 2 2006", "January 2 2006", "2 Jan 2006", "02 Jan 2006", "2 January 2006", "02-Jan-2006", "2-Jan-2006", "02-Jan-06"}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		if d, err = time.Parse("2006-01", date); err != nil {
			return false
		}
		layouts = []string{"2006-01", "01/2006", "1/2006", "Jan 2006", "January 2006", "Jan-2006", "Jan. 2006"}
	} else if withoutYear {
		layouts = append(layouts, "01/02", "02/01", "Jan 2", "2 Jan")
	}
	text = sept.ReplaceAllString(ordinalDay.ReplaceAllString(strings.ToLower(text), "$1"), "sep")
	for _, layout := range layouts {
		if found(text, strings.ToLower(d.Format(layout))) {
			return true
		}
	}
	return false
}

// found reports whether s occurs in text without a digit running into either end.
func found(text, s string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], s)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(s)
		if (i == 0 || !isDigit(text[i-1])) && (end == len(text) || !isDigit(text[end])) {
			return true
		}
		from = i + 1
	}
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
