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

func TestProjectPublicationsKeepsEveryReportCompact(t *testing.T) {
	var value any
	json.Unmarshal([]byte(`{"publications":[{"id":"9","version":2,"published_by":"pat","published_at":"2026-10-01T00:00:00Z","state":"current","commentary":"`+strings.Repeat("x", 400)+`","acknowledgments":[{"subject":"a"},{"subject":"b"}]},{"id":"8","version":1,"commentary":"short"}]}`), &value)
	out, _ := json.Marshal(projectPublications(value))
	if strings.Count(string(out), `"id"`) != 2 || len(string(out)) > 500 || !strings.Contains(string(out), `"acknowledged_by":2`) || strings.Contains(string(out), `"acknowledgments"`) {
		t.Fatalf("each report is listed compactly: %s", out)
	}
}

func TestAnExecutiveCitationStaysOnTheirPages(t *testing.T) {
	var value any
	d := json.NewDecoder(strings.NewReader(`{"closes":[{"id":"7","entity":"Alpha"}],"published":[{"close_id":"7","net_income":"10.00"}]}`))
	d.UseNumber()
	d.Decode(&value)
	reads := map[string]agentRead{"t1": {text: "{}", value: value, link: chatCitation{"Your business overview", "/dashboard/executive"}, title: "Your business overview", tool: "executive_overview", executive: true}}
	cited, why := agentSupport("facts", "Net income is 10.00.", []agentSupportEntry{{"t1", 0, "/published/0/net_income", "10.00", ""}}, reads, nil)
	if why != "" || len(cited) != 1 || cited[0].Href != "/dashboard/executive/reports?close=7" {
		t.Fatalf("a published row cites the executive's reports page: %v %s", cited, why)
	}
	if executiveRowLink("/dashboard/closes/7?tab=evidence") != "/dashboard/executive/reports?close=7" || executiveRowLink("/dashboard") != "/dashboard" {
		t.Fatal("only a close's page is moved to the reports page")
	}
}

func TestProjectCashPlanKeepsEveryWeekAndCutsItsItems(t *testing.T) {
	var value any
	json.Unmarshal([]byte(`{"opening":{"amount":"1.00"},"weeks":[{"week":"2026-10-12","receipts":"9.00","items":[1,2,3,4,5,6,7,8,9,10]},{"week":"2026-10-19","items":[1]}]}`), &value)
	out, _ := json.Marshal(projectCashPlan(value))
	if !strings.Contains(string(out), `"items":[1,2,3,4,5,6,7,8],"more_items":2`) || !strings.Contains(string(out), `"items":[1],`) || strings.Count(string(out), `"week"`) != 2 || strings.Count(string(out), "more_items") != 1 {
		t.Fatalf("each week keeps eight items and counts the rest: %s", out)
	}
}
