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
