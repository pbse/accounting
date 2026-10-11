package bookfiles

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Date orders: how a date written in numbers alone reads, its year, month and day in that order.
const (
	YMD = "ymd"
	MDY = "mdy"
	DMY = "dmy"
)

var (
	numericDate   = regexp.MustCompile(`^(\d{1,4})[/.-](\d{1,2})[/.-](\d{1,4})$`)
	dayMonthYear  = regexp.MustCompile(`^(\d{1,2})[\s-]+([A-Za-z]{3,9})\.?[\s,-]+(\d{2,4})$`)
	monthDayYear  = regexp.MustCompile(`^([A-Za-z]{3,9})\.?\s+(\d{1,2}),?\s+(\d{2,4})$`)
	monthsByName  = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	errNotADate   = fmt.Errorf("not a date")
	errEitherWays = fmt.Errorf("could be read day first or month first")
)

// readDate reads a cell's date as YYYY-MM-DD: a workbook's date as stored; a written one by its month's name, or in
// numbers by the order confirmed (a year written first is always year, month, day). Without an order, a date in numbers
// is read only when it reads one way alone (31/01/2026 is the 31st of January); one that could be read either way
// (03/04/2026) is errEitherWays. A time after the date is left aside.
func readDate(c Cell, order string) (string, error) {
	// A workbook's date is stored as YYYY-MM-DD, with any time after it; anything else stored as a date is none.
	if c.Kind == Date {
		// A zone written with it leaves the date as written.
		for _, form := range []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02T15:04:05.999999999", time.RFC3339Nano, "2006-01-02T15:04Z07:00"} {
			if d, err := time.Parse(form, c.Value); err == nil {
				return d.Format("2006-01-02"), nil
			}
		}
		return "", errNotADate
	}
	text := strings.TrimSpace(c.Value)
	if fields := strings.Fields(text); len(fields) > 1 && numericDate.MatchString(fields[0]) {
		text = fields[0]
	}
	var year, month, day int
	if m := numericDate.FindStringSubmatch(text); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		c, _ := strconv.Atoi(m[3])
		switch {
		case len(m[1]) == 4:
			year, month, day = a, b, c
		case order == YMD && len(m[3]) <= 2:
			year, month, day = a, b, c
		case len(m[3]) < 2 || len(m[1]) > 2 || a > 12 && b > 12:
			return "", errNotADate
		case order == MDY || order == "" && b > 12 && a <= 12:
			year, month, day = c, a, b
		case order == DMY || order == "" && a > 12 && b <= 12:
			year, month, day = c, b, a
		case a == b:
			year, month, day = c, a, b
		default:
			return "", errEitherWays
		}
	} else if m := dayMonthYear.FindStringSubmatch(text); m != nil {
		day, _ = strconv.Atoi(m[1])
		month = monthsByName[strings.ToLower(m[2][:3])]
		year, _ = strconv.Atoi(m[3])
	} else if m := monthDayYear.FindStringSubmatch(text); m != nil {
		month = monthsByName[strings.ToLower(m[1][:3])]
		day, _ = strconv.Atoi(m[2])
		year, _ = strconv.Atoi(m[3])
	} else {
		return "", errNotADate
	}
	if year < 100 {
		year += 2000
	}
	d := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if month < 1 || d.Year() != year || int(d.Month()) != month || d.Day() != day {
		return "", errNotADate
	}
	return d.Format("2006-01-02"), nil
}

// proposeDateOrder is the order a column's dates written in numbers are in, when they say it: one with a day past the
// 12th says which comes first; "" when none does, or they disagree.
func proposeDateOrder(values []string) string {
	monthFirst, dayFirst := false, false
	for _, v := range values {
		fields := strings.Fields(strings.TrimSpace(v))
		if len(fields) == 0 {
			continue
		}
		m := numericDate.FindStringSubmatch(fields[0])
		if m == nil || len(m[1]) == 4 {
			continue
		}
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		monthFirst = monthFirst || b > 12 && a <= 12
		dayFirst = dayFirst || a > 12 && b <= 12
	}
	switch {
	case monthFirst && !dayFirst:
		return MDY
	case dayFirst && !monthFirst:
		return DMY
	}
	return ""
}
