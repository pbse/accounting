package ledger

import (
	"regexp"
	"slices"
)

var currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)

// Every calculation is in two-decimal minor units, so ISO 4217 currencies with other minor units are refused.
var otherDecimals = []string{"BHD", "BIF", "CLF", "CLP", "DJF", "GNF", "IQD", "ISK", "JOD", "JPY", "KMF", "KRW", "KWD", "LYD", "OMR", "PYG", "RWF", "TND", "UGX", "UYI", "UYW", "VND", "VUV", "XAF", "XOF", "XPF"}

// ValidCurrency reports whether a workpaper can be prepared in this ISO 4217 currency.
func ValidCurrency(code string) bool {
	return currencyCode.MatchString(code) && !slices.Contains(otherDecimals, code)
}

// Show formats a two-decimal amount for people: "$1200.00" in US dollars, "1200.00 CAD" otherwise.
func Show(amount, currency string) string {
	if currency == "" || currency == "USD" {
		return "$" + amount
	}
	return amount + " " + currency
}
