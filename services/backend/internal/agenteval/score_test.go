package agenteval

import (
	"strings"
	"testing"
)

func TestScoreMatchesFactsLooselyAndKeepsTheBar(t *testing.T) {
	set := Set{Questions: []Question{
		{ID: "a", Case: "c", Scope: "close", Question: "Amount?", Tools: []string{"tie_out"}, Facts: []string{"1,100.00"}, Card: ""},
		{ID: "b", Case: "c", Scope: "firm", Question: "Prepare it", Tools: []string{"preview_close_preparation"}, Card: "prepare"},
		{ID: "x", Case: "c", Scope: "firm", Question: "Weather?", OffScope: true},
	}}
	if err := set.Check(); err != nil {
		t.Fatal(err)
	}
	report := Score(set, []Observed{
		{ID: "a", Tools: []string{"tie_out", "get_close"}, Kind: "facts", Answer: "Prepaid Expenses expects 1100.00.", Citations: 2, Outcome: "kept"},
		{ID: "b", Tools: []string{"preview_close_preparation", "prepare_close"}, Kind: "card", Answer: "Confirm the card.", Outcome: "kept", Card: "prepare"},
		{ID: "x", Kind: "refusal", Answer: "I don't read the weather.", Outcome: "kept"},
	})
	if !report.Passed() || report.QuestionsPass != 2 || report.OffScopePass != 1 {
		t.Fatalf("a thousands separator aside, the facts match and the run passes: %+v", report)
	}
	report = Score(set, []Observed{
		{ID: "a", Tools: []string{"get_close"}, Kind: "facts", Answer: "Prepaid Expenses expects 1,100.00.", Citations: 0, Outcome: "kept"},
		{ID: "b", Tools: []string{"preview_close_preparation"}, Kind: "card", Answer: "Confirm the card.", Outcome: "kept", Card: "portfolio"},
		{ID: "x", Kind: "facts", Answer: "It is sunny.", Outcome: "kept", Card: "prepare"},
	})
	if report.Passed() || report.QuestionsPass != 0 || report.OffScopePass != 0 {
		t.Fatalf("a missed tool, a missing citation, a wrong card and an answered off-scope prompt all fail: %+v", report)
	}
	reasons := strings.Join(report.Results[0].Reasons, "; ")
	if !strings.Contains(reasons, "did not call tie_out") || !strings.Contains(reasons, "no citation") {
		t.Fatalf("the reasons are named: %s", reasons)
	}
	if reasons = strings.Join(report.Results[2].Reasons, "; "); !strings.Contains(reasons, "not a refusal") || !strings.Contains(reasons, "proposed a prepare card") {
		t.Fatalf("an off-scope prompt's failure is named: %s", reasons)
	}
}

func TestCheckRefusesAnUnscorableSet(t *testing.T) {
	for _, q := range []Question{
		{ID: "", Case: "c", Scope: "firm", Question: "?"},
		{ID: "a", Case: "", Scope: "firm", Question: "?"},
		{ID: "a", Case: "c", Scope: "both", Question: "?"},
		{ID: "a", Case: "c", Scope: "firm", Question: "?", OffScope: true, Card: "prepare"},
		{ID: "a", Case: "c", Scope: "firm", Question: "?", Card: "signoff"},
		{ID: "a", Case: "c", Scope: "close", Question: "?", Card: "open_close"},
	} {
		if err := (Set{Questions: []Question{q}}).Check(); err == nil {
			t.Fatalf("%+v is not scorable", q)
		}
	}
	if err := (Set{Questions: []Question{{ID: "a", Case: "c", Scope: "firm", Question: "?"}, {ID: "a", Case: "c", Scope: "firm", Question: "?"}}}).Check(); err == nil {
		t.Fatal("a repeated id is refused")
	}
}

func TestLookupMatchesTheRequestExactly(t *testing.T) {
	var r Record
	r.Add(Read{Tool: "select_closes_preview", Method: "POST", Path: "/v1/closes/portfolio", Body: map[string]any{"text": "Show all visible", "preview": true}, Output: "{}", OK: true})
	r.Add(Read{Tool: "select_closes_preview", Method: "POST", Path: "/v1/closes/portfolio", Body: map[string]any{"preview": true, "text": "Show all visible"}, Output: "again", OK: true})
	if len(r.Reads) != 1 {
		t.Fatalf("the same request is kept once: %d", len(r.Reads))
	}
	if out, ok, found := r.Lookup("POST", "/v1/closes/portfolio", map[string]any{"preview": true, "text": "Show all visible"}); !found || !ok || out != "{}" {
		t.Fatal("the request is found whatever its key order")
	}
	if _, _, found := r.Lookup("POST", "/v1/closes/portfolio", map[string]any{"preview": true, "text": "show all visible"}); found {
		t.Fatal("other words are another request")
	}
	if _, _, found := r.Lookup("GET", "/v1/closes/7/brief", nil); found {
		t.Fatal("an unrecorded read is not found")
	}
}
