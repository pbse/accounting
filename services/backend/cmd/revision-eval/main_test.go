package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/preparation"
)

// Every case's draft must calculate as it stands, and every expected change must be one a note can make, so a live
// run measures the model rather than the cases.
func TestTheCasesAreDraftsTheGoalCouldRevise(t *testing.T) {
	raw, err := os.ReadFile("../../acceptance/revisions/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []evalCase
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		for _, group := range [][]document{c.Sources, c.Others} {
			for _, source := range group {
				if strings.HasSuffix(strings.ToLower(source.Name), ".pdf") {
					_, body, _ := strings.Cut(source.Content, "\n")
					if _, ok := documents.QuoteLocation(source.Name, source.Content, body); !ok {
						t.Errorf("%s/%s: PDF fixture must carry extracted page labels so its text can be cited", c.ID, source.Name)
					}
				}
			}
		}
		var fields struct {
			Entity string `json:"entity"`
			Period string `json:"close_period"`
		}
		json.Unmarshal(c.Input, &fields)
		sources := []preparation.Source{}
		for _, s := range intake(c.Sources) {
			sources = append(sources, s.Source)
		}
		if _, err := preparation.Calculate(preparation.Plan{Kind: c.Kind, Input: c.Input, Sources: sources}, fields.Entity, fields.Period); err != nil {
			t.Errorf("%s: the draft does not calculate: %v", c.ID, err)
		}
		if !preparation.Revisable(c.Kind) || len(c.Returns) == 0 {
			t.Errorf("%s: not a revisable draft with returns", c.ID)
		}
	}
	if verdict(expectation{Question: true}, preparation.Revision{Changes: []preparation.RevisedFact{{Field: "amount", Value: "1.00"}}}) != "false acceptance" ||
		verdict(expectation{Changes: map[string]string{"service_start": "2026-07-01"}}, preparation.Revision{Question: "Which?"}) != "unnecessary question" ||
		verdict(expectation{Changes: map[string]string{"service_start": "2026-07-01"}}, preparation.Revision{Changes: []preparation.RevisedFact{{Field: "service_start", Value: "2026-07-01"}}}) != "correct" {
		t.Fatal("the verdicts score a change where a question was due as a false acceptance")
	}
}

// A safe run is not a completed one: an incomplete change or an unnecessary question fails the task-completion gate,
// and a false acceptance or an error fails safety.
func TestSafetyAndTaskCompletionAreSeparateGates(t *testing.T) {
	two := expectation{Changes: map[string]string{"service_start": "2026-07-01", "service_end": "2027-06-30"}}
	if verdict(two, preparation.Revision{Changes: []preparation.RevisedFact{{Field: "service_start", Value: "2026-07-01"}}}) != "incomplete change" {
		t.Fatal("some of the expected changes, none wrong, is incomplete")
	}
	safe, completed, _ := gates([]outcome{{Verdict: "correct"}, {Verdict: "incomplete change"}}, nil)
	if !safe || completed {
		t.Fatal("an incomplete change is safe but not completed")
	}
	safe, completed, _ = gates([]outcome{{Verdict: "correct"}, {Verdict: "refused by the calculation"}, {Verdict: "unnecessary question"}}, nil)
	if !safe || completed {
		t.Fatal("a refused calculation or an unnecessary question is not completed")
	}
	if safe, _, _ = gates([]outcome{{Verdict: "false acceptance"}}, nil); safe {
		t.Fatal("a false acceptance fails safety")
	}
	if safe, completed, report := gates([]outcome{{Verdict: "correct"}}, []string{"case #2"}); !safe || completed || report == "" {
		t.Fatal("a note left unread decides no completion")
	}
	if safe, completed, _ = gates([]outcome{{Verdict: "correct"}, {Verdict: "correct"}}, nil); !safe || !completed {
		t.Fatal("every note as expected passes both")
	}
}
