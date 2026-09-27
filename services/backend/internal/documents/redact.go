package documents

import (
	"regexp"
	"slices"
	"strings"
)

var (
	secretPattern  = regexp.MustCompile(`(?i)\b(password|passcode|passphrase|pin|pwd|one[- ]time (?:pass)?code|otp|verification code|security code|security answer|user ?name|user ?id|login)\s*(?::|=|\bis\b|\bwas\b)[^\n]*`)
	accountPattern = regexp.MustCompile(`(?i)(\b(?:bank|checking|savings|chequing|deposit)\s+(?:account|acct)\b|\b(?:account|acct|a/c)\s*(?:number|num|no\b\.?|#))([^\d\n]{0,20})(\d(?:[ -]?\d){5,19})\b`)
	ibanPattern    = regexp.MustCompile(`(?i)\b[a-z]{2}\d{2}(?: ?[a-z0-9]){11,30}\b`)
	ssnPattern     = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	// A card is written as Amex 4-6-5 groups, 4-digit groups ending in a 1-4 digit group, or 13-19 contiguous
	// digits, so posting and value dates side by side are never read as one.
	cardPattern = regexp.MustCompile(`\b(?:\d{4}[ -]\d{6}[ -]\d{5}|\d{4}(?:[ -]\d{4}){2}[ -]\d{1,4}(?:[ -]\d{1,3})?|\d{13,19})\b`)
)

// Redact removes card, IBAN and SSN numbers, bank account numbers written after a bank label, and
// everything after a password or sign-in label on its line. Each removal leaves a marker with at most the last
// four digits; the original is never stored.
func Redact(text string) (string, []string) {
	removed := []string{}
	note := func(kind string) {
		if !slices.Contains(removed, kind) {
			removed = append(removed, kind)
		}
	}
	plain := func(s string) string { return strings.NewReplacer(" ", "", "-", "").Replace(s) }
	last4 := func(s string) string { return s[len(s)-4:] }
	text = secretPattern.ReplaceAllStringFunc(text, func(m string) string {
		note("password or sign-in detail")
		return secretPattern.FindStringSubmatch(m)[1] + " [removed]"
	})
	text = accountPattern.ReplaceAllStringFunc(text, func(m string) string {
		p := accountPattern.FindStringSubmatch(m)
		note("bank account number")
		return p[1] + p[2] + "[bank account number ending " + last4(plain(p[3])) + " removed]"
	})
	// The match may run into the following words; drop trailing ones until the check digits hold.
	text = ibanPattern.ReplaceAllStringFunc(text, func(m string) string {
		for tokens := strings.Fields(m); len(tokens) > 0; tokens = tokens[:len(tokens)-1] {
			iban, remainder := strings.ToUpper(strings.Join(tokens, "")), 0
			if len(iban) < 15 {
				break
			}
			for _, c := range iban[4:] + iban[:4] {
				if c >= 'A' {
					remainder = (remainder*100 + int(c-'A') + 10) % 97
				} else {
					remainder = (remainder*10 + int(c-'0')) % 97
				}
			}
			if remainder == 1 {
				note("IBAN")
				return "[IBAN ending " + last4(iban) + " removed]" + strings.TrimPrefix(m, strings.Join(tokens, " "))
			}
		}
		return m
	})
	text = ssnPattern.ReplaceAllStringFunc(text, func(m string) string {
		note("SSN")
		return "[SSN ending " + last4(m) + " removed]"
	})
	luhn := func(digits string) bool {
		sum := 0
		for i := range len(digits) {
			d := int(digits[len(digits)-1-i] - '0')
			if i%2 == 1 {
				if d *= 2; d > 9 {
					d -= 9
				}
			}
			sum += d
		}
		return sum%10 == 0
	}
	// A trailing group may be a separate number; drop trailing groups until the check digit holds.
	text = cardPattern.ReplaceAllStringFunc(text, func(m string) string {
		for card := m; len(plain(card)) >= 13; {
			if luhn(plain(card)) {
				note("card number")
				return "[card number ending " + last4(plain(card)) + " removed]" + strings.TrimPrefix(m, card)
			}
			cut := strings.LastIndexAny(card, " -")
			if cut < 0 {
				break
			}
			card = card[:cut]
		}
		return m
	})
	return text, removed
}
