package acceptance

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// ReviewPackage is the accounting-review package for a run (docs/slice6-evidence.md): for each case, its treatment
// specification (spec.md beside case.json), each document's expected values beside what the app produced, the case's
// critical findings, and the places where the independent accountant confirms or corrects the specification and each
// expected value, judges the result and records the time spent. Until the accountant confirms a case, its labels are the
// builder's, and the package says so.
func ReviewPackage(cases []Case, r Report, run string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Accounting review package\n\n%s\n\n", run)
	b.WriteString("For each case: confirm or correct the treatment specification and every expected value, then judge what the app produced. ")
	b.WriteString("Corrections become the case's labels. A case not yet confirmed is builder-labelled and is never reported as accountant-accepted.\n")
	for _, c := range cases {
		observed := r.Observed[c.ID]
		fmt.Fprintf(&b, "\n---\n\n## %s\n\n%s · %s · split %s", c.ID, c.Client, c.Period, c.Split)
		if len(c.Tags) > 0 {
			fmt.Fprintf(&b, " · %s", strings.Join(c.Tags, ", "))
		}
		b.WriteString("\n\nLabels: **builder-specified, not yet confirmed by the accountant.**\n\n")
		if spec, err := os.ReadFile(filepath.Join(c.Dir, "spec.md")); err == nil {
			fmt.Fprintf(&b, "### Treatment specification\n\n%s\n", strings.TrimSpace(strings.TrimPrefix(string(spec), "#")))
		} else {
			b.WriteString("### Treatment specification\n\nNone written yet: write the treatment and how each expected value is derived.\n")
		}
		seen := map[string]Observed{}
		for _, o := range observed.Documents {
			seen[o.File] = o
		}
		for _, d := range c.Documents {
			o := seen[d.File]
			fmt.Fprintf(&b, "\n### %s\n\nExpected work: %s.", d.File, or(d.Label.WorkType, "none"))
			if d.Label.Exception != "" {
				fmt.Fprintf(&b, " Expected problem: %s.", d.Label.Exception)
			}
			fmt.Fprintf(&b, " %s", outcome(o))
			b.WriteString("\n\n| Fact | Expected | The app's | Same | Accountant |\n| --- | --- | --- | --- | --- |\n")
			for _, field := range sortedKeys(d.Label.Facts) {
				got, ok := o.Facts[field]
				same := "no"
				if ok && sameValue(field, got, d.Label.Facts[field]) {
					same = "yes"
				}
				fmt.Fprintf(&b, "| %s | %s | %s | %s | ☐ confirmed ☐ corrected: |\n", field, d.Label.Facts[field], or(got, "not read"), same)
			}
			if len(d.Label.Drafts) > 0 || len(o.Drafts) > 0 {
				// Rows pair drafts as the scorer does, whatever their order: matched first, then the closest left over.
				b.WriteString("\n| Draft | Expected | The app's | Accountant |\n| --- | --- | --- | --- |\n")
				owner, unmatched, spare := matchDrafts(o.Drafts, d.Label.Drafts)
				row := 0
				line := func(want, got string) {
					row++
					fmt.Fprintf(&b, "| %d | %s | %s | ☐ confirmed ☐ corrected: |\n", row, want, got)
				}
				for w := range d.Label.Drafts {
					if g := slices.Index(owner, w); g >= 0 {
						line(entryText(d.Label.Drafts[w]), entryText(o.Drafts[g]))
					}
				}
				for i, w := range unmatched {
					got := "none"
					if i < len(spare) {
						got = entryText(o.Drafts[spare[i]])
					}
					line(entryText(d.Label.Drafts[w]), got)
				}
				for _, g := range spare[min(len(unmatched), len(spare)):] {
					line("none", entryText(o.Drafts[g]))
				}
			}
		}
		fmt.Fprintf(&b, "\nReady to sign off: expected %s; the app's readiness %s.", map[bool]string{true: "yes", false: "no"}[c.ExpectReady], or(observed.ReadinessState, "not read"))
		found := []string{}
		for _, f := range append(append([]Failure{}, r.Failures...), r.NotProcessed...) {
			if f.Case == c.ID {
				found = append(found, fmt.Sprintf("%s (%s): %s", f.Kind, or(f.Document, "the close"), f.Detail))
			}
		}
		if len(found) > 0 {
			fmt.Fprintf(&b, " Critical findings: %s.", strings.Join(found, "; "))
		}
		b.WriteString("\n\n**Accountant.** ☐ Specification confirmed ☐ corrected (write it). ☐ The app's result is a sound starting point for review ☐ it is not, because: ______. Minutes spent: ____. Name and date: ______.\n")
	}
	return b.String()
}

func entryText(e Entry) string {
	return strings.TrimSpace(fmt.Sprintf("%s debit %s, credit %s, %s", e.Date, e.DebitAccount, e.CreditAccount, e.Amount))
}

// outcome says what the pipeline did with a document, as the scorer reads it.
func outcome(o Observed) string {
	switch {
	case o.SameAs != "":
		return fmt.Sprintf("The app kept it as the same document as %s: no second piece of work.", o.SameAs)
	case o.JobState == "":
		return "No preparation job started for it: not processed."
	case o.Error != "" || o.JobState == "failed":
		return fmt.Sprintf("Its preparation failed: %s Not processed.", or(o.Error, "no reason recorded."))
	case o.Accepted:
		return fmt.Sprintf("The app read it as %s and went on without a person.%s", or(o.Kind, "nothing"), because(o))
	case o.JobState == "needs_confirmation" || o.JobState == "waiting_client":
		return fmt.Sprintf("The app read it as %s and put it to a person.%s", or(o.Kind, "nothing"), because(o))
	}
	return fmt.Sprintf("The app read it as %s, but its preparation had not settled (%s): not processed.", or(o.Kind, "nothing"), o.JobState)
}

func because(o Observed) string {
	if o.Reason == "" {
		return ""
	}
	return " Its reason: " + o.Reason
}
