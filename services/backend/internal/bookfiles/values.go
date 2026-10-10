package bookfiles

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// NumberStyle is how a file writes its amounts (docs/books-sources.md, Values): which mark separates the decimals, and
// the scale the amounts are stated in ("in thousands"). The reader states it for each import and a person confirms it;
// it never applies one silently.
type NumberStyle struct {
	// DecimalComma says the decimals follow a comma (1.234,56); otherwise a point (1,234.56).
	DecimalComma bool `json:"decimal_comma"`
	// Scale is what each written amount is multiplied by: 1, 1000 or 1000000.
	Scale int64 `json:"scale"`
}

// Amount is one amount read exactly: its value, and whether it is exact to the cent once scaled (an amount rounded at
// its source, such as 1.2 thousand, is not, and is held from use as a month's books).
type Amount struct {
	Value *big.Rat
	Cents bool
}

// ErrNotAmount is a value that is not an amount in the stated style.
var ErrNotAmount = errors.New("not an amount")

// currencyMarks are a currency's mark, or its ISO code in capitals, beside an amount.
var currencyMarks = regexp.MustCompile(`^(?:[A-Z]{3}\s*)?[$€£¥₹]?|[$€£¥₹]?(?:\s*[A-Z]{3})?$`)

// ParseAmount reads text as an amount in style: negatives in parentheses, with a leading or trailing minus, or as a
// Dr or Cr suffix (a debit is positive, a credit negative, as balances are kept); thousands separators (commas or
// points, spaces and apostrophes, whichever the decimal mark is not); a currency mark or code beside it. The value is
// multiplied by the style's scale with exact decimal arithmetic, so 1.23456 thousand is 1234.56.
func ParseAmount(text string, style NumberStyle) (Amount, error) {
	s := strings.TrimSpace(strings.NewReplacer(" ", " ", "−", "-", " ", " ", " ", " ").Replace(text))
	if s == "" {
		return Amount{}, ErrNotAmount
	}
	negative := false
	// A Dr or Cr suffix (or prefix) says the side: a credit is negative, as balances are kept (debit minus credit).
	upper := strings.ToUpper(s)
	switch {
	case strings.HasSuffix(upper, "CR"):
		negative, s = true, strings.TrimSpace(s[:len(s)-2])
	case strings.HasSuffix(upper, "DR"):
		s = strings.TrimSpace(s[:len(s)-2])
	case strings.HasPrefix(upper, "CR "):
		negative, s = true, strings.TrimSpace(s[3:])
	case strings.HasPrefix(upper, "DR "):
		s = strings.TrimSpace(s[3:])
	}
	// Parentheses, a leading or trailing minus and a currency mark or code can wrap one another in any order
	// ($ (12.00), -$12.00, (12.00) USD); each is taken off until none is left.
	for changed := true; changed; {
		before := s
		s = strings.TrimSpace(currencyMarks.ReplaceAllString(s, ""))
		switch {
		case strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"):
			negative, s = !negative, strings.TrimSpace(s[1:len(s)-1])
		case strings.HasSuffix(s, "-"):
			negative, s = !negative, strings.TrimSpace(s[:len(s)-1])
		case strings.HasPrefix(s, "-"):
			negative, s = !negative, strings.TrimSpace(s[1:])
		case strings.HasPrefix(s, "+"):
			s = strings.TrimSpace(s[1:])
		}
		changed = s != before && s != ""
	}
	// The digits must be grouped as the style groups them, in threes from the decimal mark, or not at all: a value written
	// in the other style (1,2 with a decimal point) or grouped wrongly (1,234,56) is not read as some other amount.
	grouped := pointGrouped
	if style.DecimalComma {
		grouped = commaGrouped
	}
	m := grouped.FindStringSubmatch(s)
	if m == nil || (m[1] == "" && m[2] == "") {
		return Amount{}, ErrNotAmount
	}
	whole, fraction := strings.NewReplacer(",", "", ".", "", " ", "", "'", "").Replace(m[1]), m[2]
	value, ok := new(big.Rat).SetString(whole + "." + fraction + "0")
	if !ok {
		return Amount{}, ErrNotAmount
	}
	scale := style.Scale
	if scale == 0 {
		scale = 1
	}
	value.Mul(value, new(big.Rat).SetInt64(scale))
	if negative {
		value.Neg(value)
	}
	// Exact to the cent: the amount is a whole number of cents and, when it is stated in thousands or millions, written
	// with enough decimals to say each cent (1.23456 thousand does; 1.2 thousand was rounded at its source).
	cents := new(big.Rat).Mul(value, big.NewRat(100, 1)).IsInt()
	if scale > 1 {
		resolution := new(big.Rat).SetFrac(big.NewInt(scale), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(fraction))), nil))
		cents = cents && resolution.Cmp(big.NewRat(1, 100)) <= 0
	}
	return Amount{Value: value, Cents: cents}, nil
}

// pointGrouped and commaGrouped are a number with a decimal point or a decimal comma: its whole part, either plain or
// grouped in threes by commas (points), spaces or apostrophes, and its decimals.
var (
	pointGrouped = regexp.MustCompile(`^(\d+|\d{1,3}(?:,\d{3})+|\d{1,3}(?: \d{3})+|\d{1,3}(?:'\d{3})+|)(?:\.(\d+))?$`)
	commaGrouped = regexp.MustCompile(`^(\d+|\d{1,3}(?:\.\d{3})+|\d{1,3}(?: \d{3})+|\d{1,3}(?:'\d{3})+|)(?:,(\d+))?$`)
)

// GuessNumberStyle proposes how amounts are written from a sample of them: a decimal comma when the values that show a
// decimal mark end in a comma and one or two digits, or group with points; else a decimal point. It also says whether
// the sample could be read either way (every value a whole number of thousands, such as 1,234), for a person to
// confirm. A scale is never guessed: a file's "in thousands" is a person's to confirm.
func GuessNumberStyle(samples []string) (NumberStyle, bool) {
	comma, point, ambiguous := 0, 0, 0
	for _, raw := range samples {
		s := strings.Trim(strings.TrimSpace(raw), "()-+ DdRrCc$€£")
		lastComma, lastPoint := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
		switch {
		case lastComma >= 0 && lastPoint >= 0 && lastComma > lastPoint:
			comma++
		case lastComma >= 0 && lastPoint >= 0:
			point++
		case lastComma >= 0 && len(s)-lastComma-1 != 3:
			comma++
		case lastPoint >= 0 && len(s)-lastPoint-1 != 3:
			point++
		case lastComma >= 0 || lastPoint >= 0:
			ambiguous++
		}
	}
	return NumberStyle{DecimalComma: comma > point, Scale: 1}, comma == point || (comma == 0 && point == 0 && ambiguous > 0)
}
