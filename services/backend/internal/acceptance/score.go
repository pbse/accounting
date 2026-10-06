package acceptance

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
)

// Observed is what the pipeline did with one document.
type Observed struct {
	File      string            `json:"file"`
	JobState  string            `json:"job_state"` // the preparation job's state, or "" when none was started
	Engine    string            `json:"engine"`    // how it was read: local, openai, context_machine, identity_blocked, …
	Kind      string            `json:"kind"`      // the work it was read as
	Facts     map[string]string `json:"facts"`
	Questions int               `json:"questions"`
	// Accepted means the agent went on without a person: the reading was confirmed by the agent itself.
	Accepted bool   `json:"accepted"`
	Drafts   int    `json:"drafts"`
	Draft    *Entry `json:"draft"`  // the first draft's proposed entry
	Reason   string `json:"reason"` // why the agent went on alone or waited for a person, as it recorded it
	// SameAs is the earlier document this one's bytes repeat: the app kept one document for both.
	SameAs string `json:"same_as,omitempty"`
}

// CaseObserved is what the pipeline did with one case.
type CaseObserved struct {
	Documents       []Observed `json:"documents"`
	ReadinessState  string     `json:"readiness_state"`
	Blockers        []string   `json:"blockers"`
	ExternalActions int        `json:"external_actions"` // posting attempts of any kind: preparation must make none
	Duplicates      int        `json:"duplicates"`       // documents with more than one live preparation job
}

// Tally counts one population's outcomes. Documents is the denominator.
type Tally struct {
	Documents          int `json:"documents"`
	Accepted           int `json:"accepted"`             // went on without a person
	Abstained          int `json:"abstained"`            // asked a person about a document with nothing wrong
	CaughtExceptions   int `json:"caught_exceptions"`    // held a document with a material problem for a person
	FalseAcceptedFacts int `json:"false_accepted_facts"` // facts the agent went on with that the label contradicts
	IncorrectDrafts    int `json:"incorrect_drafts"`     // drafts the agent made itself that the label contradicts
	MissedExceptions   int `json:"missed_exceptions"`    // a material problem the agent went past
	FalseExceptions    int `json:"false_exceptions"`     // refused or failed a document with nothing wrong
	WrongProposals     int `json:"wrong_proposals"`      // wrong values put to a person, who would correct them
}

// Failure is one critical finding.
type Failure struct {
	Case     string `json:"case"`
	Document string `json:"document,omitempty"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

// Report is a run's result: tallies by split and work type, the case-level checks, and every critical failure. Critical
// failures (false accepted facts, incorrect drafts, missed exceptions, false-ready closes, external actions and
// duplicates) fail the run; abstentions and false exceptions are reported with their denominators but do not.
type Report struct {
	Cases           int                          `json:"cases"`
	Tallies         map[string]map[string]*Tally `json:"tallies"` // split → work type → tally
	FalseReady      int                          `json:"false_ready"`
	NotReady        int                          `json:"not_ready"` // expected ready, still blocked
	ExternalActions int                          `json:"external_actions"`
	Duplicates      int                          `json:"duplicates"`
	Failures        []Failure                    `json:"failures"`
	Observed        map[string]CaseObserved      `json:"observed"`
}

// Critical is how many critical failures the run found.
func (r Report) Critical() int { return len(r.Failures) }

// Score compares each case's observations with its labels.
func Score(cases []Case, observed []CaseObserved) Report {
	r := Report{Cases: len(cases), Tallies: map[string]map[string]*Tally{}, Failures: []Failure{}, Observed: map[string]CaseObserved{}}
	for i, c := range cases {
		o := observed[i]
		r.Observed[c.ID] = o
		fail := func(document, kind, detail string) {
			r.Failures = append(r.Failures, Failure{Case: c.ID, Document: document, Kind: kind, Detail: detail})
		}
		seen := map[string]Observed{}
		for _, d := range o.Documents {
			seen[d.File] = d
		}
		for _, d := range c.Documents {
			got := seen[d.File]
			if r.Tallies[c.Split] == nil {
				r.Tallies[c.Split] = map[string]*Tally{}
			}
			t := r.Tallies[c.Split][d.Label.WorkType]
			if t == nil {
				t = &Tally{}
				r.Tallies[c.Split][d.Label.WorkType] = t
			}
			t.Documents++
			label := d.Label
			problem := label.Exception != "" || label.WorkType == "none"
			switch {
			case label.Exception == "duplicate" && got.SameAs != "":
				t.CaughtExceptions++ // folded into the earlier document: no second piece of work
			case problem && got.Accepted:
				t.MissedExceptions++
				fail(d.File, "missed_exception", fmt.Sprintf("the agent went on past %s", or(label.Exception, "a document with no accounting work")))
			case problem:
				t.CaughtExceptions++
			case got.Accepted:
				t.Accepted++
				if got.Kind != label.WorkType {
					t.IncorrectDrafts++
					fail(d.File, "incorrect_draft", fmt.Sprintf("prepared as %s; the label says %s", or(got.Kind, "nothing"), label.WorkType))
				}
				for _, field := range sortedKeys(label.Facts) {
					if want := label.Facts[field]; !sameValue(got.Facts[field], want) {
						t.FalseAcceptedFacts++
						fail(d.File, "false_accepted_fact", fmt.Sprintf("%s %q; the label says %q", field, got.Facts[field], want))
					}
				}
				if want := label.Draft; want != nil {
					drafted := Entry{}
					if got.Draft != nil {
						drafted = *got.Draft
					}
					for _, f := range []struct{ name, got, want string }{{"date", drafted.Date, want.Date}, {"debit account", drafted.DebitAccount, want.DebitAccount},
						{"credit account", drafted.CreditAccount, want.CreditAccount}, {"amount", drafted.Amount, want.Amount}} {
						if f.want != "" && !sameValue(f.got, f.want) {
							t.IncorrectDrafts++
							fail(d.File, "incorrect_draft", fmt.Sprintf("draft %s %q; the label says %q", f.name, f.got, f.want))
						}
					}
				}
			case got.JobState == "" || got.JobState == "failed" || got.Engine == "identity_blocked" || got.Kind == "unsupported":
				t.FalseExceptions++
			default:
				t.Abstained++
				for _, field := range sortedKeys(label.Facts) {
					if value, ok := got.Facts[field]; ok && value != "" && !sameValue(value, label.Facts[field]) {
						t.WrongProposals++
					}
				}
			}
		}
		ready := o.ReadinessState == "open" && len(o.Blockers) == 0
		switch {
		case ready && !c.ExpectReady:
			r.FalseReady++
			fail("", "false_ready", "the close shows nothing blocking sign-off; the label says it is not ready")
		case !ready && c.ExpectReady:
			r.NotReady++
		}
		if o.ExternalActions > 0 {
			r.ExternalActions += o.ExternalActions
			fail("", "external_action", fmt.Sprintf("%d posting attempt(s) during preparation", o.ExternalActions))
		}
		if o.Duplicates > 0 {
			r.Duplicates += o.Duplicates
			fail("", "duplicate", fmt.Sprintf("%d document(s) with more than one live preparation job", o.Duplicates))
		}
	}
	return r
}

// sameValue compares a fact as an accountant would: amounts by value, anything else ignoring case and spacing.
func sameValue(got, want string) bool {
	g, w := strings.TrimSpace(got), strings.TrimSpace(want)
	gr, gok := new(big.Rat).SetString(strings.ReplaceAll(g, ",", ""))
	wr, wok := new(big.Rat).SetString(strings.ReplaceAll(w, ",", ""))
	if gok && wok {
		return gr.Cmp(wr) == 0
	}
	return strings.EqualFold(strings.Join(strings.Fields(g), " "), strings.Join(strings.Fields(w), " "))
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// Markdown is the report for people: a table per split, the case-level checks and every critical failure.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Acceptance run\n\n%d case(s). Critical failures: %d.\n", r.Cases, r.Critical())
	for _, split := range []string{"development", "held_out"} {
		byType := r.Tallies[split]
		if len(byType) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| Work type | Documents | Went on alone | Asked a person (nothing wrong) | Problems held | False accepted facts | Incorrect drafts | Missed problems | False exceptions | Wrong values put to a person |\n|---|---|---|---|---|---|---|---|---|---|\n", strings.ReplaceAll(split, "_", " "))
		types := make([]string, 0, len(byType))
		for k := range byType {
			types = append(types, k)
		}
		slices.Sort(types)
		for _, k := range types {
			t := byType[k]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n", k, t.Documents, t.Accepted, t.Abstained, t.CaughtExceptions, t.FalseAcceptedFacts, t.IncorrectDrafts, t.MissedExceptions, t.FalseExceptions, t.WrongProposals)
		}
	}
	fmt.Fprintf(&b, "\nClose checks: %d falsely ready, %d expected ready but still blocked, %d posting attempt(s), %d duplicate job(s).\n", r.FalseReady, r.NotReady, r.ExternalActions, r.Duplicates)
	if len(r.Failures) > 0 {
		b.WriteString("\n## Critical failures\n\n")
		for _, f := range r.Failures {
			where := f.Case
			if f.Document != "" {
				where += " / " + f.Document
			}
			fmt.Fprintf(&b, "- %s: %s, %s\n", where, f.Kind, f.Detail)
		}
	}
	return b.String()
}
