package acceptance

import (
	"strings"
	"testing"
)

// Each outcome lands in its own count, with the document as the denominator; only the critical ones fail the run.
func TestScoreCountsEachOutcomeAndFailsOnlyOnCriticalOnes(t *testing.T) {
	prepaid := func(file, exception string) Document {
		return Document{File: file, Label: Label{WorkType: "prepaid", Facts: map[string]string{"amount": "1200.00", "service_start": "2026-09-01"}, DraftAmount: "100.00", Exception: exception}}
	}
	cases := []Case{{ID: "c1", Split: "development", Documents: []Document{
		prepaid("right.txt", ""), prepaid("wrong-amount.txt", ""), prepaid("asked.txt", ""), prepaid("held.txt", "wrong_client"),
		prepaid("missed.txt", "ocr_decimal"), prepaid("refused.txt", ""), {File: "letter.txt", Label: Label{WorkType: "none"}},
	}}, {ID: "c2", Split: "held_out", ExpectReady: false, Documents: []Document{prepaid("ok.txt", "")}}}
	good := map[string]string{"amount": "1,200.00", "service_start": "2026-09-01"}
	observed := []CaseObserved{{ReadinessState: "open", Blockers: []string{"Define the supported scope"}, Documents: []Observed{
		{File: "right.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, Drafts: 1, DraftAmount: "100"},
		{File: "wrong-amount.txt", JobState: "completed", Kind: "prepaid", Facts: map[string]string{"amount": "12000.00", "service_start": "2026-09-01"}, Accepted: true, Drafts: 1, DraftAmount: "1000.00"},
		{File: "asked.txt", JobState: "needs_confirmation", Kind: "prepaid", Facts: map[string]string{"amount": "1300.00"}},
		{File: "held.txt", JobState: "needs_confirmation", Kind: "unsupported", Engine: "identity_blocked"},
		{File: "missed.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, Drafts: 1, DraftAmount: "100.00"},
		{File: "refused.txt", JobState: "failed"},
		{File: "letter.txt", JobState: "needs_confirmation", Kind: "unsupported"},
	}}, {ReadinessState: "open", ExternalActions: 1, Documents: []Observed{{File: "ok.txt", JobState: "completed", Kind: "prepaid", Facts: good, Accepted: true, DraftAmount: "100.00"}}}}

	r := Score(cases, observed)
	dev := r.Tallies["development"]["prepaid"]
	if *dev != (Tally{Documents: 6, Accepted: 2, Abstained: 1, CaughtExceptions: 1, FalseAcceptedFacts: 1, IncorrectDrafts: 1, MissedExceptions: 1, FalseExceptions: 1, WrongProposals: 1}) {
		t.Fatalf("development prepaid tally: %+v", *dev)
	}
	if none := r.Tallies["development"]["none"]; none.Documents != 1 || none.CaughtExceptions != 1 {
		t.Fatalf("a document with no work, held for a person: %+v", *none)
	}
	if r.FalseReady != 1 || r.ExternalActions != 1 {
		t.Fatalf("close checks: %+v", r)
	}
	kinds := []string{}
	for _, f := range r.Failures {
		kinds = append(kinds, f.Case+":"+f.Document+":"+f.Kind)
	}
	if got := strings.Join(kinds, ","); got != "c1:wrong-amount.txt:false_accepted_fact,c1:wrong-amount.txt:incorrect_draft,c1:missed.txt:missed_exception,c2::false_ready,c2::external_action" {
		t.Fatalf("critical failures: %s", got)
	}
	if md := r.Markdown(); !strings.Contains(md, "| prepaid | 6 | 2 | 1 | 1 | 1 | 1 | 1 | 1 | 1 |") || !strings.Contains(md, "Critical failures: 5") {
		t.Fatalf("report:\n%s", md)
	}
}

func TestLoadRefusesACaseItCannotScore(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := writeFile(dir+"/"+name, body); err != nil {
			t.Fatal(err)
		}
	}
	write("bad/case.json", `{"id":"Bad Case","split":"tuning","client":"","period":"2026-13","setup":{},"documents":[{"file":"missing.txt","label":{"work_type":""}}]}`)
	_, err := Load(dir)
	for _, want := range []string{"id \"Bad Case\"", "split \"tuning\"", "client and its month", "missing.txt", "work type"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want %q in %v", want, err)
		}
	}
}
