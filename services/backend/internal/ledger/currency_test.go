package ledger

import "testing"

func TestCurrencyNeedsThreeLettersAndTwoDecimals(t *testing.T) {
	for code, want := range map[string]bool{"USD": true, "CAD": true, "GBP": true, "JPY": false, "KWD": false, "XOF": false, "IQD": false, "usd": false, "": false} {
		if ValidCurrency(code) != want {
			t.Fatalf("%q valid=%v", code, !want)
		}
	}
	if Show("12.00", "USD") != "$12.00" || Show("12.00", "CAD") != "12.00 CAD" {
		t.Fatal("amounts show in their own currency")
	}
}
