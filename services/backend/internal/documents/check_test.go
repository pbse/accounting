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
	if issue := EntityIssue(content, "Acme LLC"); issue != "" {
		t.Fatal(issue)
	}
	if issue := EntityIssue(content, "Other LLC"); issue == "" {
		t.Fatal("explicit wrong client was not blocked")
	}
	if issue := EntityIssue("Client entity: Acme LLC\nClient entity: Other LLC", "Acme LLC"); issue == "" {
		t.Fatal("contradictory client labels were not blocked")
	}
	if issue := EntityIssue("Client entity: Acme, LLC\nBill to: Other LLC", "Acme LLC"); issue != "" {
		t.Fatal("customer bill-to or punctuation difference blocked the client", issue)
	}
	if issue := EntityIssue("Client entity: Acme LLC        Invoice number: 42", "Acme LLC"); issue != "" {
		t.Fatal("second PDF column became part of the client name", issue)
	}
	if issue := EntityIssue("Client entity: Acme  LLC", "Acme LLC"); issue != "" {
		t.Fatal("spacing inside the client name was treated as another column", issue)
	}
	if issue := EntityIssue("Client entity: Acme LLC  Holdings", "Acme LLC"); issue == "" {
		t.Fatal("different client suffix was mistaken for another column")
	}
}
