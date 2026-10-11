package acceptance

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTheReviewPackageSetsExpectedBesideObservedForTheAccountant(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(filepath.Join(dir, "spec.md"), "# Prepaid\n\n1,200.00 over 12 months: 100.00 a month."); err != nil {
		t.Fatal(err)
	}
	c := Case{ID: "prepaid-case", Client: "Harbor Fixture LLC", Period: "2026-09", Split: "held_out", Dir: dir, ExpectReady: true,
		Documents: []Document{{File: "invoice.txt", Label: Label{WorkType: "prepaid", Facts: map[string]string{"amount": "1200.00", "service_end": "2027-08-31"},
			Drafts: []Entry{{DebitAccount: "Prepaid Expenses", CreditAccount: "Insurance", Amount: "1100.00"}, {DebitAccount: "Insurance", CreditAccount: "Accounts Payable", Amount: "50.00"}}}},
			{File: "again.txt", Label: Label{WorkType: "prepaid", Exception: "duplicate"}}}}
	// Observations come in any order, and so do drafts: each is set beside its own label and its matching entry.
	r := Report{Observed: map[string]CaseObserved{"prepaid-case": {ReadinessState: "blocked", Documents: []Observed{{File: "again.txt", SameAs: "invoice.txt"},
		{File: "invoice.txt", Kind: "prepaid", Accepted: true, JobState: "completed", Facts: map[string]string{"amount": "1,200.00"},
			Drafts: []Entry{{DebitAccount: "Insurance", CreditAccount: "Accounts Payable", Amount: "50.00"}, {DebitAccount: "Prepaid Expenses", CreditAccount: "Insurance", Amount: "1000.00"}}}}}},
		Failures: []Failure{{Case: "prepaid-case", Document: "invoice.txt", Kind: "incorrect_draft", Detail: "amount 1000.00, not 1100.00"}}}
	out := ReviewPackage([]Case{c}, r, "Run of the test corpus.")
	for _, want := range []string{
		"builder-specified, not yet confirmed by the accountant",
		"1,200.00 over 12 months",
		"| amount | 1200.00 | 1,200.00 | yes |",
		"| service_end | 2027-08-31 | not read | no |",
		"| 1 | debit Insurance, credit Accounts Payable, 50.00 | debit Insurance, credit Accounts Payable, 50.00 |",
		"| 2 | debit Prepaid Expenses, credit Insurance, 1100.00 | debit Prepaid Expenses, credit Insurance, 1000.00 |",
		"The app read it as prepaid and went on without a person.",
		"The app kept it as the same document as invoice.txt",
		"Ready to sign off: expected yes; the app's readiness blocked.",
		"incorrect_draft (invoice.txt): amount 1000.00, not 1100.00",
		"Minutes spent",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the package shows %q:\n%s", want, out)
		}
	}
}
