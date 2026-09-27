package documents

import (
	"strings"
	"testing"
)

func TestRedactRemovesCardsBankNumbersAndSecrets(t *testing.T) {
	for _, c := range []struct{ in, out, kinds string }{
		{"Card 4111-1111-1111-1111 exp 12/29", "Card [card number ending 1111 removed] exp 12/29", "card number"},
		{"Invoice 4111111111111112 attached", "Invoice 4111111111111112 attached", ""},
		{"Wire to GB82 WEST 1234 5698 7654 32 please", "Wire to [IBAN ending 5432 removed] please", "IBAN"},
		{"Ref GB82 WEST 1234 5698 7654 33", "Ref GB82 WEST 1234 5698 7654 33", ""},
		{"iban gb82 west 1234 5698 7654 32 okay thanks", "iban [IBAN ending 5432 removed] okay thanks", "IBAN"},
		{"SSN 123-45-6789", "SSN [SSN ending 6789 removed]", "SSN"},
		{"Account number: 000123456789, routing 021000021", "Account number: [bank account number ending 6789 removed], routing 021000021", "bank account number"},
		{"Book to account 400100 and account 6010", "Book to account 400100 and account 6010", ""},
		{"Online banking login is alice; one-time code is 123456\nThanks", "Online banking login [removed]\nThanks", "password or sign-in detail"},
		{"PIN = 4455", "PIN [removed]", "password or sign-in detail"},
		{"Service end: 2027-08-31", "Service end: 2027-08-31", ""},
		{"Paid 4111 1111 1111 1111 today", "Paid [card number ending 1111 removed] today", "card number"},
		{"Card 4222 2222 2222 2", "Card [card number ending 2222 removed]", "card number"},
		{"Amex 3782 822463 10005", "Amex [card number ending 0005 removed]", "card number"},
		{"2026-08-03 2026-08-03 Deposit Customer A 2,500.00", "2026-08-03 2026-08-03 Deposit Customer A 2,500.00", ""},
		{"2026-08-01 2026-08-05 4111 1111 1111 1111", "2026-08-01 2026-08-05 [card number ending 1111 removed]", "card number"},
		{"Paid 4111 1111 1111 1111 123 today", "Paid [card number ending 1111 removed] 123 today", "card number"},
	} {
		got, kinds := Redact(c.in)
		if got != c.out || strings.Join(kinds, ",") != c.kinds {
			t.Errorf("%q → %q %v", c.in, got, kinds)
		}
		if again, more := Redact(got); again != got || len(more) != 0 {
			t.Errorf("redacted text is stable: %q → %q", got, again)
		}
	}
}
