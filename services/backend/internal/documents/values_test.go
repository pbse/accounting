package documents

import "testing"

func TestAmountIn(t *testing.T) {
	for _, c := range []struct {
		text, amount string
		want         bool
	}{
		{"Total due: USD 1,200.00", "1200.00", true},
		{"Total due: USD 1,200.00", "1200", true},
		{"Total due: USD 1,200.00", "12000.00", false},
		{"Total due: $1,200.99", "1200", false},
		{"Deposit 120.00", "20.00", false},
		{"Fee (5.00)", "-5.00", true},
		{"Principal 1,250,000.50", "1250000.5", true},
		{"Paid 2026-09-02", "2.00", false},
		{"Rate 5.25% per annum", "5.25", true},
		{"Items 1,2,3", "123", false},
		{"2026-09-02 Deposit", "2026.00", false},
		{"9/2/26 Deposit 45.00", "26", false},
		{"Refund 120.00-", "-120.00", true},
		{"Coverage starts Sep 2, 2026", "2.00", false},
		{"Coverage starts Sep 2, 2026", "2026", false},
		{"From 2nd January 2026", "2", false},
		{"Invoice for March 2026: 1,200.00", "1200", true},
		{"Paid 2 Jan 2026, fee 2.00", "2.00", true},
		{"Rent may 5.00 rise", "5.00", true},
	} {
		if AmountIn(c.text, c.amount) != c.want {
			t.Errorf("AmountIn(%q, %q) = %v", c.text, c.amount, !c.want)
		}
	}
}

func TestDateIn(t *testing.T) {
	for _, c := range []struct {
		text, date string
		yearless   bool
		want       bool
	}{
		{"Coverage starts September 1, 2026.", "2026-09-01", false, true},
		{"Coverage starts Sept 1st, 2026.", "2026-09-01", false, true},
		{"Period 01/09/2026 to 31/08/2027", "2027-08-31", false, true},
		{"Invoice date 11/2/2026", "2026-01-02", false, false},
		{"Jan 21, 2026", "2026-01-02", false, false},
		{"Sep 2 Deposit", "2026-09-02", false, false},
		{"Sep 2 Deposit", "2026-09-02", true, true},
		{"Credit applies from March 2026", "2026-03", false, true},
		{"2026-09-03 Deposit", "2026-09-02", true, false},
		{"9/2/26 Deposit", "2026-09-02", false, true},
	} {
		if DateIn(c.text, c.date, c.yearless) != c.want {
			t.Errorf("DateIn(%q, %q, %v) = %v", c.text, c.date, c.yearless, !c.want)
		}
	}
}

// MoneyIn is AmountIn for money: a term, a rate, a count or a reference that prints the same digits is not the amount.
func TestMoneyIn(t *testing.T) {
	for _, c := range []struct {
		text, amount string
		want         bool
	}{
		{"Total due: USD 1,200.00", "1200.00", true},
		{"Total due: USD 1,200.00", "12000.00", false},
		{"Term: 12 months, premium 1,200.00", "12", false},
		{"Term: 12 months, premium 1,200.00", "1200", true},
		{"Rate 5.25% per annum", "5.25", false},
		{"Rate 5.25 percent", "5.25", false},
		{"Invoice #1200", "1200", false},
		{"Invoice #1200 total 1,200.00", "1200", true},
		{"Invoice no. 4471", "4471", false},
		{"See page 3", "3", false},
		{"Policy 3300 premium 3,300.00", "3300", true},
		{"Fee (5.00)", "-5.00", true},
		{"40 hours at 25.00", "40", false},
		{"40 hours at 25.00", "25", true},
		{"Term: 12-month policy", "12", false},
		{"Refund 120.00-", "-120.00", true},
		{"Balance: 100.00\nMonths remaining: 12", "100.00", true},
		{"Policy\n3300\npremium", "3300", true},
	} {
		if MoneyIn(c.text, c.amount) != c.want {
			t.Errorf("MoneyIn(%q, %q) = %v", c.text, c.amount, !c.want)
		}
	}
}
