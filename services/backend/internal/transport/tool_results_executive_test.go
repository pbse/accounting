package transport

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapResultCutsEveryNamedArray(t *testing.T) {
	raw := []byte(`{"closes":[{"id":"1"},{"id":"2"},{"id":"3"}],"published":[{"close_id":"1"},{"close_id":"2"},{"close_id":"3"}]}`)
	out, truncated, total := capResult(raw, toolCap{array: "closes", limit: 2, arrays: []string{"published"}})
	if !truncated || total != 3 || strings.Count(string(out), "close_id") != 2 || strings.Count(string(out), `"id"`) != 2 || !strings.Contains(string(out), `"truncated":true`) {
		t.Fatalf("both arrays are cut and marked: %s", out)
	}
	if _, truncated, _ = capResult(raw, toolCap{array: "closes", limit: 3, arrays: []string{"published"}}); truncated {
		t.Fatal("arrays within the limit are left whole")
	}
}

func TestProjectPublicationReadsASectionAtATime(t *testing.T) {
	var value any
	json.Unmarshal([]byte(`{"publication":{"id":"9","version":2,"commentary":"A good month."},"pack":{"entity":"Alpha","period":"2026-09","currency":"USD","basis":"Accrual","overall":"signed_off","profit_and_loss":{"sections":[{"rows":[1,2,3]}],"measures":[{"label":"Net income","amount":"10.00"}]},"balance_sheet":{"rows":[1,2]},"working_capital":{"book_cash":{"amount":"5.00"}}}}`), &value)
	summary, _ := json.Marshal(projectPublication(value, ""))
	if !strings.Contains(string(summary), `"Net income"`) || strings.Contains(string(summary), `"rows"`) || !strings.Contains(string(summary), `"book_cash"`) || !strings.Contains(string(summary), `"sections":["profit_and_loss"`) {
		t.Fatalf("the summary keeps the measures and working capital and names the sections: %s", summary)
	}
	pl, _ := json.Marshal(projectPublication(value, "profit_and_loss"))
	if !strings.Contains(string(pl), `"rows":[1,2,3]`) || strings.Contains(string(pl), "book_cash") {
		t.Fatalf("a section is read in full and alone: %s", pl)
	}
	commentary, _ := json.Marshal(projectPublication(value, "commentary"))
	if !strings.Contains(string(commentary), "A good month.") || strings.Contains(string(commentary), "Net income") {
		t.Fatalf("the commentary section is the publication's text: %s", commentary)
	}
}
