package documents

import (
	"strings"
	"testing"
)

func TestCheckPageAndFactConflicts(t *testing.T) {
	content := "--- Page 1 · PDF text ---\nPage 1 of 3\nInvoice date: 2026-09-01\n--- Page 2 · PDF text ---\nPage 3 of 3\nInvoice date: 2026-09-02\n"
	check := Check("invoice.pdf", content)
	if check.Pages != 2 || len(check.Questions) != 3 || !strings.Contains(check.Questions[0], "missing") {
		t.Fatalf("missing page and contradictory fact must be visible: %+v", check)
	}
	clean := Check("invoice.pdf", "--- Page 1 · PDF text ---\nPage 1 of 2\n--- Page 2 · PDF text ---\nPage 2 of 2\n")
	if clean.Pages != 2 || len(clean.Questions) != 0 || len(clean.Warnings) != 0 {
		t.Fatalf("complete printed sequence should have no issue: %+v", clean)
	}
	duplicate := Check("invoice.pdf", "--- Page 1 · PDF text ---\nPage 1 of 2\nPage 1 of 2\n--- Page 2 · PDF text ---\nNo printed number\n")
	if len(duplicate.Warnings) != 1 || !strings.Contains(duplicate.Warnings[0], "Some PDF pages") {
		t.Fatalf("two labels on one page cannot mask an unnumbered page: %+v", duplicate)
	}
	unknown := Check("invoice.pdf", "--- Page 1 · OCR ---\nInvoice date: 2026-09-01\n")
	if len(unknown.Warnings) != 1 || !strings.Contains(unknown.Warnings[0], "unverified") && !strings.Contains(unknown.Warnings[0], "confirm completeness") {
		t.Fatalf("unnumbered PDF must not be called complete: %+v", unknown)
	}
}

func TestQuoteLocationAndExplicitClientBoundary(t *testing.T) {
	content := "--- Page 1 · PDF text ---\nClient entity: Acme LLC\nTotal: 100\n--- Page 2 · OCR ---\nInvoice date: 2026-09-01\n"
	if at, ok := QuoteLocation("invoice.pdf", content, "Invoice date: 2026-09-01"); !ok || at != "page 2, line 1" {
		t.Fatalf("wrong page citation: %q %v", at, ok)
	}
	if _, ok := QuoteLocation("invoice.txt", "Amount: 100\nAmount: 100\n", "Amount: 100"); ok {
		t.Fatal("repeated quote has no unique source location")
	}
	if _, ok := QuoteLocation("invoice.pdf", "--- Page 1 · PDF text ---\n--- Page 2 · spoofed ---\n--- Page 2 · PDF text ---\nAmount: 100", "Amount: 100"); ok {
		t.Fatal("spoofed page marker accepted")
	}
	if issue, _ := EntityIssue(content, "Acme LLC", nil); issue != "" {
		t.Fatal(issue)
	}
	if issue, _ := EntityIssue(content, "Other LLC", nil); issue == "" {
		t.Fatal("explicit wrong client was not blocked")
	}
	if issue, _ := EntityIssue("Client entity: Acme LLC\nClient entity: Other LLC", "Acme LLC", nil); issue == "" {
		t.Fatal("contradictory client labels were not blocked")
	}
	if issue, _ := EntityIssue("Client entity: Acme, LLC\nBill to: Other LLC", "Acme LLC", nil); issue != "" {
		t.Fatal("customer bill-to or punctuation difference blocked the client", issue)
	}
	if issue, _ := EntityIssue("Client entity: Acme LLC        Invoice number: 42", "Acme LLC", nil); issue != "" {
		t.Fatal("second PDF column became part of the client name", issue)
	}
	if issue, _ := EntityIssue("Client entity: Acme  LLC", "Acme LLC", nil); issue != "" {
		t.Fatal("spacing inside the client name was treated as another column", issue)
	}
	if issue, _ := EntityIssue("Client entity: Acme LLC  Holdings", "Acme LLC", nil); issue == "" {
		t.Fatal("different client suffix was mistaken for another column")
	}
}

func TestConfirmedAliasesAcceptOnlyAnUnambiguousSource(t *testing.T) {
	issue, alias := EntityIssue("Client entity: Acme Services LLC", "Acme Services", nil)
	if issue == "" || alias != "Acme Services LLC" {
		t.Fatalf("an unconfirmed other name blocks and is offered for confirmation: %q %q", issue, alias)
	}
	confirmed := []string{NormalizeEntity("Acme Services, LLC")}
	if issue, _ := EntityIssue("Client entity: Acme Services LLC", "Acme Services", confirmed); issue != "" {
		t.Fatalf("a confirmed alias is the same client: %q", issue)
	}
	if issue, alias := EntityIssue("Client entity: Acme Services LLC\nClient entity: Other LLC", "Acme Services", confirmed); issue == "" || alias != "" {
		t.Fatalf("a source naming two entities stays blocked and offers no alias: %q %q", issue, alias)
	}
	if issue, alias := EntityIssue("Client entity: ---", "Acme Services", nil); issue == "" || alias != "" {
		t.Fatalf("a name with no letters or digits is never offered as an alias: %q %q", issue, alias)
	}
}
