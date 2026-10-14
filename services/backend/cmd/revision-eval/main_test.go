package main

import (
	"encoding/json"
	"os"
	"testing"

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
