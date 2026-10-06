package acceptance

import (
	"fmt"
	"math/big"
	"slices"
	"strings"

	"close.local/backend/internal/preparation"
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
	Accepted bool    `json:"accepted"`
	Drafts   []Entry `json:"drafts"` // every draft the job prepared, in order, with its proposed entry
	Reason   string  `json:"reason"` // why the agent went on alone or waited for a person, as it recorded it
	Error    string  `json:"error"`  // why the job failed, when it did
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
	FalseExceptions    int `json:"false_exceptions"`     // refused a document with nothing wrong (as another client's, or as no work)
	WrongProposals     int `json:"wrong_proposals"`      // wrong values put to a person, who would correct them
	// Unprocessed documents never reached a decision: no job started, it failed, or it had not settled. They are neither
	// caught problems nor abstentions, whatever their label.
	Unprocessed int `json:"unprocessed"`
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
// duplicates) fail the run, and so does any document the pipeline never decided on: the run has not evaluated it.
// Abstentions and false exceptions are reported with their denominators but do not fail it.
type Report struct {
	Cases           int                          `json:"cases"`
	Tallies         map[string]map[string]*Tally `json:"tallies"` // split → work type → tally
	FalseReady      int                          `json:"false_ready"`
	NotReady        int                          `json:"not_ready"` // expected ready, still blocked
	ExternalActions int                          `json:"external_actions"`
	Duplicates      int                          `json:"duplicates"`
	Failures        []Failure                    `json:"failures"`
	NotProcessed    []Failure                    `json:"not_processed"`
	Observed        map[string]CaseObserved      `json:"observed"`
}

// Critical is how many critical failures the run found.
func (r Report) Critical() int { return len(r.Failures) }

// Passed is a run with no critical failure in which every document was decided on.
func (r Report) Passed() bool { return len(r.Failures) == 0 && len(r.NotProcessed) == 0 }

// Exceptions are the labeled problems the harness can score, each with the app's own sign that it found that problem.
// A held document counts as a caught problem only when that sign shows: holding it for another reason (say a missing
// ledger capture) leaves the person uninformed, so it is a missed exception. Add a problem here with its sign.
var Exceptions = map[string]func(Observed) bool{
	// The identity block, or the straight-through check that refuses another company's name as billed to.
	"wrong_client": func(o Observed) bool {
		return o.Engine == "identity_blocked" || strings.Contains(o.Reason, "is billed to")
	},
	// The same bytes again are one document: no second piece of work.
	"duplicate": func(o Observed) bool { return o.SameAs != "" },
}

// flagged reports whether the app raised any problem it has a sign for (other than keeping a repeat as one document):
// on a clean document, that is a false exception.
func flagged(o Observed) bool {
	for name, found := range Exceptions {
		if name != "duplicate" && found(o) {
			return true
		}
	}
	return false
}

// noWork is the sign that a document with no accounting work was read as such.
func noWork(o Observed) bool { return o.Kind == "unsupported" }

// Score compares each case's observations with its labels.
func Score(cases []Case, observed []CaseObserved) Report {
	r := Report{Cases: len(cases), Tallies: map[string]map[string]*Tally{}, Failures: []Failure{}, NotProcessed: []Failure{}, Observed: map[string]CaseObserved{}}
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
			// Held: the job settled waiting for a person (or the client), which is how a problem is caught or a clean document
			// asked about. Waiting for evidence is not a decision: the job is pending a read or a retry.
			held := got.JobState == "needs_confirmation" || got.JobState == "waiting_client"
			found := noWork
			if label.Exception != "" {
				found = Exceptions[label.Exception]
			}
			switch {
			case label.Exception == "duplicate" && found(got):
				t.CaughtExceptions++ // folded into the earlier document, whatever became of that one
			case problem && got.Accepted:
				t.MissedExceptions++
				fail(d.File, "missed_exception", fmt.Sprintf("the agent went on past %s", or(label.Exception, "a document with no accounting work")))
			case got.Accepted:
				t.Accepted++
				if got.Kind != label.WorkType {
					t.IncorrectDrafts++
					fail(d.File, "incorrect_draft", fmt.Sprintf("prepared as %s; the label says %s", or(got.Kind, "nothing"), label.WorkType))
				}
				for _, field := range sortedKeys(label.Facts) {
					if want := label.Facts[field]; !sameValue(field, got.Facts[field], want) {
						t.FalseAcceptedFacts++
						fail(d.File, "false_accepted_fact", fmt.Sprintf("%s %q; the label says %q", field, got.Facts[field], want))
					}
				}
				if label.Drafts != nil {
					for _, problem := range compareDrafts(got.Drafts, label.Drafts) {
						t.IncorrectDrafts++
						fail(d.File, "incorrect_draft", problem)
					}
				}
			case !held:
				t.Unprocessed++
				detail := "job " + or(got.JobState, "never started")
				if got.Error != "" {
					detail += ": " + got.Error
				}
				r.NotProcessed = append(r.NotProcessed, Failure{Case: c.ID, Document: d.File, Kind: "not_processed", Detail: detail})
			case problem && found != nil && found(got):
				t.CaughtExceptions++
			case problem:
				t.MissedExceptions++
				fail(d.File, "missed_exception", fmt.Sprintf("held, but not for %s: %s", or(label.Exception, "having no accounting work"), or(got.Reason, "no reason recorded")))
			case got.Kind == "unsupported" || flagged(got):
				t.FalseExceptions++
			default:
				t.Abstained++
				for _, field := range sortedKeys(label.Facts) {
					if value, ok := got.Facts[field]; ok && value != "" && !sameValue(field, value, label.Facts[field]) {
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

// sameValue compares a field as an accountant would: an amount (as the app defines its amount fields) by value, so
// 1,200.00 is 1200; anything else as text ignoring case and spacing, so invoice number 0012 is not 12.
func sameValue(field, got, want string) bool {
	g, w := strings.TrimSpace(got), strings.TrimSpace(want)
	if preparation.AmountField(field) {
		gr, gok := new(big.Rat).SetString(strings.ReplaceAll(g, ",", ""))
		wr, wok := new(big.Rat).SetString(strings.ReplaceAll(w, ",", ""))
		if gok && wok {
			return gr.Cmp(wr) == 0
		}
	}
	return strings.EqualFold(strings.Join(strings.Fields(g), " "), strings.Join(strings.Fields(w), " "))
}

func sortedKeys[V any](m map[string]V) []string {
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
	fmt.Fprintf(&b, "# Acceptance run\n\n%d case(s). Critical failures: %d. Not processed: %d.\n", r.Cases, r.Critical(), len(r.NotProcessed))
	for _, split := range []string{"development", "held_out"} {
		byType := r.Tallies[split]
		if len(byType) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| Work type | Documents | Went on alone | Asked a person (nothing wrong) | Problems held | False accepted facts | Incorrect drafts | Missed problems | False exceptions | Wrong values put to a person | Not processed |\n|---|---|---|---|---|---|---|---|---|---|---|\n", strings.ReplaceAll(split, "_", " "))
		types := make([]string, 0, len(byType))
		for k := range byType {
			types = append(types, k)
		}
		slices.Sort(types)
		for _, k := range types {
			t := byType[k]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |\n", k, t.Documents, t.Accepted, t.Abstained, t.CaughtExceptions, t.FalseAcceptedFacts, t.IncorrectDrafts, t.MissedExceptions, t.FalseExceptions, t.WrongProposals, t.Unprocessed)
		}
	}
	fmt.Fprintf(&b, "\nClose checks: %d falsely ready, %d expected ready but still blocked, %d posting attempt(s), %d duplicate job(s).\n", r.FalseReady, r.NotReady, r.ExternalActions, r.Duplicates)
	for _, section := range []struct {
		title    string
		failures []Failure
	}{{"Critical failures", r.Failures}, {"Not processed", r.NotProcessed}} {
		if len(section.failures) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n", section.title)
		for _, f := range section.failures {
			where := f.Case
			if f.Document != "" {
				where += " / " + f.Document
			}
			fmt.Fprintf(&b, "- %s: %s, %s\n", where, f.Kind, f.Detail)
		}
	}
	return b.String()
}

// compareDrafts sets the drafts a job prepared against the label's: the same number, and each labeled entry matched by a
// different draft on every field it gives (a full matching, so a broad label never takes the draft a narrower one needs).
// It returns one problem for a wrong count and one per labeled entry left unmatched.
func compareDrafts(got, want []Entry) []string {
	problems := []string{}
	if len(got) != len(want) {
		problems = append(problems, fmt.Sprintf("%d draft(s); the label expects %d", len(got), len(want)))
	}
	owner := make([]int, len(got)) // the labeled entry each draft is matched to, or -1
	for i := range owner {
		owner[i] = -1
	}
	var assign func(w int, seen []bool) bool
	assign = func(w int, seen []bool) bool {
		for g := range got {
			if seen[g] || len(entryDifferences(got[g], want[w])) > 0 {
				continue
			}
			seen[g] = true
			if owner[g] < 0 || assign(owner[g], seen) {
				owner[g] = w
				return true
			}
		}
		return false
	}
	unmatched := []int{}
	for w := range want {
		if !assign(w, make([]bool, len(got))) {
			unmatched = append(unmatched, w)
		}
	}
	spare := []Entry{}
	for g, w := range owner {
		if w < 0 {
			spare = append(spare, got[g])
		}
	}
	for i, w := range unmatched {
		closest := Entry{}
		if i < len(spare) {
			closest = spare[i]
		}
		problems = append(problems, fmt.Sprintf("no draft matches the labeled entry: %s", strings.Join(entryDifferences(closest, want[w]), "; ")))
	}
	return problems
}

// entryDifferences names each field the label gives that the entry does not match.
func entryDifferences(got, want Entry) []string {
	differences := []string{}
	for _, f := range []struct{ name, got, want string }{{"date", got.Date, want.Date}, {"debit account", got.DebitAccount, want.DebitAccount},
		{"credit account", got.CreditAccount, want.CreditAccount}, {"amount", got.Amount, want.Amount}} {
		if f.want != "" && !sameValue(f.name, f.got, f.want) {
			differences = append(differences, fmt.Sprintf("%s %q, the label says %q", f.name, f.got, f.want))
		}
	}
	return differences
}
