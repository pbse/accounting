package documents

import (
	"math/big"
	"regexp"
	"strings"
	"time"
)

// A number as printed: thousands groups of three, or plain digits, with optional decimals.
var printedNumber = regexp.MustCompile(`\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?`)

// AmountIn reports whether text prints the amount, ignoring its sign, currency signs and
// thousands separators. A printed 1,200.00 shows 1200 or 1200.00, never 12000.00 or 1200.99.
func AmountIn(text, amount string) bool {
	want, ok := new(big.Rat).SetString(strings.TrimPrefix(amount, "-"))
	if !ok || strings.ContainsAny(amount, "eE/") {
		return false
	}
	for _, at := range printedNumber.FindAllStringIndex(text, -1) {
		token := text[at[0]:at[1]]
		// A digit or point before it means it is the tail of a longer number; 09 is a day or month.
		if at[0] > 0 && strings.ContainsRune("0123456789.", rune(text[at[0]-1])) || len(token) > 1 && token[0] == '0' && token[1] != '.' {
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
	layouts := []string{"2006-01-02", "2006/01/02", "01/02/2006", "1/2/2006", "02/01/2006", "2/1/2006", "01-02-2006", "02-01-2006", "02.01.2006", "01/02/06", "02/01/06",
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
