package preparation

import "testing"

// A fact's value must be what its quote prints, as that kind of value: a misread decimal, or a matching number that
// is a term, a rate or a reference, becomes a question rather than an accepted fact (handoff, "Extraction consistency").
func TestValueShownHoldsAmountsToTheirMeaning(t *testing.T) {
	for _, c := range []struct {
		field, value, quote string
		want                bool
	}{
		{"amount", "1200.00", "Annual premium USD 1,200.00", true},
		{"amount", "12000.00", "Annual premium USD 1,200.00", false},
		{"amount", "12.00", "Term: 12 months", false},
		{"amount", "4471.00", "Invoice no. 4471", false},
		{"credit_amount", "-250.00", "Credit (250.00)", true},
		{"annual_rate", "5.25", "Rate 5.25% per annum", true},
		{"amount", "5.25", "Rate 5.25% per annum", false},
		{"service_start", "2026-09-01", "Coverage 09/01/2026 to 08/31/2027", true},
	} {
		if got := valueShown(c.field, c.value, c.quote); got != c.want {
			t.Errorf("valueShown(%q, %q, %q) = %v", c.field, c.value, c.quote, got)
		}
	}
}
