package agenteval

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Observed is what one question produced: the tools the loop ran or proposed, the answer's kind and text, how many
// citations it kept, the card it proposed, and the loop's outcome (kept, not_kept or failed) with its reason.
type Observed struct {
	ID        string   `json:"id"`
	Tools     []string `json:"tools"`
	Kind      string   `json:"kind"`
	Answer    string   `json:"answer"`
	Citations int      `json:"citations"`
	Card      string   `json:"card,omitempty"`
	Outcome   string   `json:"outcome"`
	Why       string   `json:"why,omitempty"`
	Rounds    int      `json:"rounds"`
	Calls     int      `json:"calls"`
}

// Result is one question's score.
type Result struct {
	ID       string   `json:"id"`
	Question string   `json:"question"`
	OffScope bool     `json:"off_scope,omitempty"`
	Passed   bool     `json:"passed"`
	Reasons  []string `json:"reasons,omitempty"`
	Observed Observed `json:"observed"`
}

// Report is a run's score: every question's result and the totals. A run passes when every off-scope prompt was
// refused without a card and at least nine in ten questions passed.
type Report struct {
	Results       []Result `json:"results"`
	Questions     int      `json:"questions"`
	QuestionsPass int      `json:"questions_passed"`
	OffScope      int      `json:"off_scope"`
	OffScopePass  int      `json:"off_scope_passed"`
	ModelCalls    int      `json:"model_calls"`
}

// Score checks each observation against its question: the answer was kept, every expected tool ran, every fact is in
// the text, a facts answer carries citations, and the card is the one expected; an off-scope prompt was answered as a
// refusal (or a plain statement of what the agent does) with no card and no fact.
func Score(set Set, observed []Observed) Report {
	byID := map[string]Observed{}
	for _, o := range observed {
		byID[o.ID] = o
	}
	var report Report
	for _, q := range set.Questions {
		o, ran := byID[q.ID]
		result := Result{ID: q.ID, Question: q.Question, OffScope: q.OffScope, Observed: o}
		report.ModelCalls += o.Calls
		switch {
		case !ran:
			result.Reasons = append(result.Reasons, "not run")
		case q.OffScope:
			if o.Outcome != "kept" {
				result.Reasons = append(result.Reasons, "no answer was kept: "+or(o.Why, o.Outcome))
			}
			if o.Kind != "refusal" && o.Kind != "help" {
				result.Reasons = append(result.Reasons, "answered as "+or(o.Kind, "nothing")+", not a refusal")
			}
			if o.Card != "" {
				result.Reasons = append(result.Reasons, "proposed a "+o.Card+" card")
			}
		default:
			if o.Outcome != "kept" {
				result.Reasons = append(result.Reasons, "no answer was kept: "+or(o.Why, o.Outcome))
			}
			for _, tool := range q.Tools {
				if !slices.Contains(o.Tools, tool) {
					result.Reasons = append(result.Reasons, "did not call "+tool)
				}
			}
			answer := fold(o.Answer)
			for _, fact := range q.Facts {
				if !strings.Contains(answer, fold(fact)) {
					result.Reasons = append(result.Reasons, fmt.Sprintf("the answer lacks %q", fact))
				}
			}
			if len(q.Facts) > 0 && o.Kind != "facts" {
				result.Reasons = append(result.Reasons, "answered as "+or(o.Kind, "nothing")+", not facts")
			}
			if o.Kind == "facts" && o.Citations == 0 {
				result.Reasons = append(result.Reasons, "a facts answer kept no citation")
			}
			if o.Card != q.Card {
				result.Reasons = append(result.Reasons, "proposed "+or(o.Card, "no card")+", expected "+or(q.Card, "no card"))
			}
		}
		result.Passed = len(result.Reasons) == 0
		if q.OffScope {
			report.OffScope++
			if result.Passed {
				report.OffScopePass++
			}
		} else {
			report.Questions++
			if result.Passed {
				report.QuestionsPass++
			}
		}
		report.Results = append(report.Results, result)
	}
	return report
}

// Passed says the run meets the release bar.
func (r Report) Passed() bool {
	return r.OffScopePass == r.OffScope && r.Questions > 0 && r.QuestionsPass*10 >= r.Questions*9
}

// Markdown renders the report for the validation log.
func (r Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Agent golden set\n\nQuestions: %d of %d passed. Off-scope prompts: %d of %d refused without a card. Model calls: %d. %s\n\n", r.QuestionsPass, r.Questions, r.OffScopePass, r.OffScope, r.ModelCalls, map[bool]string{true: "Passed.", false: "Failed."}[r.Passed()])
	b.WriteString("| Question | Result | Tools | Kind | Why |\n| --- | --- | --- | --- | --- |\n")
	for _, result := range r.Results {
		state := "pass"
		if !result.Passed {
			state = "fail"
		}
		fmt.Fprintf(&b, "| %s %s | %s | %s | %s | %s |\n", result.ID, cell(result.Question), state, cell(strings.Join(result.Observed.Tools, ", ")), result.Observed.Kind, cell(strings.Join(result.Reasons, "; ")))
	}
	return b.String()
}

// fold compares text loosely: case and spacing aside, and a thousands separator aside, so "1,100.00" is "1100.00".
func fold(s string) string {
	s = thousands.ReplaceAllString(strings.ToLower(s), "$1$2")
	return strings.Join(strings.Fields(s), " ")
}

var thousands = regexp.MustCompile(`(\d),(\d{3})`)

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}
