package transport

import (
	"encoding/json"
	"strings"
	"testing"
)

// The support gate holds a financial sentence to what its cited field measures, not merely to an equal number
// (docs/agent-first-implementation-handoff.md, "Financial answers that support the actual claim"). Each case is one
// the handoff names: the reproduced wrong-metric defect, then wrong client, month, currency, basis, sign, balance
// versus change, forecast versus actual, a stale read, an omitted remainder and an unsupported cause, and the answers
// that must still pass: an exact fact, a source-linked sum and difference, a ratio, a multi-period comparison and a
// zero-base budget line.
func TestAgentClaimsBindWhatAFigureMeasures(t *testing.T) {
	decode := func(raw string) any {
		var v any
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	read := func(raw, title, tool, entity, period string) agentRead {
		return agentRead{text: raw, value: decode(raw), link: chatCitation{title, "/dashboard/closes/7"}, title: title, tool: tool, entity: entity, period: period}
	}
	pack := `{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","basis":"accrual","status":"provisional",
		"profit_and_loss":{"measures":[{"name":"Net income","amount":"21780.00","prior":"19940.00","change":"1840.00"},{"name":"Gross profit","amount":"104330.00","prior":"99460.00","change":"4870.00"}],
		"sections":[{"name":"Income","lines":[{"name":"Patient fees","amount":"128400.00","prior":"123460.00","change":"4940.00"}],"total":{"name":"Total income","amount":"128400.00","prior":"123460.00","change":"4940.00"}},
		{"name":"Expense","lines":[{"name":"Lab fees","amount":"9860.00","prior":"10120.00","change":"-260.00"}],"total":{"name":"Total expense","amount":"106620.00","prior":"103520.00","change":"3100.00"}}]}}`
	reads := map[string]agentRead{
		// The handoff's reproduction: one read holding receivables of 8,000.00.
		"t1": read(`{"receivables":"8000.00"}`, "Working capital", "working_capital", "Bluegrass Dental PLLC", "2026-10"),
		"t2": read(pack, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10"),
		"t3": read(`{"state":"ready","entity":"Bluegrass Dental PLLC","currency":"USD","opening":{"amount":"64120.00","as_of":"2026-10-31","stale":true},
			"weeks":[{"start":"2026-12-20","end":"2026-12-26","opening":"69620.00","receipts":"14200.00","disbursements":"60420.00","closing":"23400.00","lowest":"19850.00","lowest_on":"2026-12-19"}],
			"scenario":{"name":"Bonus slips","weeks":[{"start":"2026-12-20","closing":"41400.00"}]}}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10"),
		"t4": read(`{"lines":[{"name":"Rent","month":{"actual":"8500.00","budget":"8000.00","variance":"500.00","percent":"6.3%"},"ytd":{"actual":"85000.00","budget":"80000.00","variance":"5000.00"}},
			{"name":"Marketing","month":{"actual":"540.00","budget":"0.00","variance":"540.00","percent":null}}]}`, "Budget against actual", "budget_comparison", "Bluegrass Dental PLLC", "2026-10"),
		"t5": read(`{"closes":[{"id":"9","entity":"Birch Dental","period":"2026-10","net_income":"21780.00"}]}`, "List client closes", "list_closes", "", ""),
		"t6": read(`{"rows":[{"account":"Revenue","change":"4940.00","drivers":[{"payee":"New patients","change":"3000.00"},{"payee":"Hygiene fees","change":"1000.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10"),
		"t7": read(`{"publication":{"id":"3","state":"superseded"},"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","profit_and_loss":{"measures":[{"name":"Net income","amount":"20100.00"}]}}}`, "Published report", "read_publication", "", ""),
	}
	e := func(step string, sentence int, path, value string) agentSupportEntry {
		return agentSupportEntry{step: step, sentence: sentence, path: path, value: value}
	}
	derived := func(sentence int, op, value string, of ...string) agentSupportEntry {
		in := []agentInput{}
		for i := 0; i+1 < len(of); i += 2 {
			in = append(in, agentInput{of[i], of[i+1]})
		}
		return agentSupportEntry{sentence: sentence, value: value, derive: op, of: in}
	}
	ok := func(text string, support ...agentSupportEntry) []chatCitation {
		t.Helper()
		cited, why := agentSupport("facts", text, support, reads, nil)
		if why != "" {
			t.Fatalf("%q rejected: %s", text, why)
		}
		return cited
	}
	rejected := func(text, reason string, support ...agentSupportEntry) {
		t.Helper()
		_, why := agentSupport("facts", text, support, reads, nil)
		if why == "" || !strings.Contains(why, reason) {
			t.Fatalf("%q was not rejected for %q: %q", text, reason, why)
		}
	}
	const ni = "/profit_and_loss/measures/0"

	// The reproduced defect: an equal number under another metric.
	rejected("Net income is 8,000.00.", "calls 8000 net income", e("t1", 0, "/receivables", "8000.00"))
	ok("Receivables are 8,000.00.", e("t1", 0, "/receivables", "8000.00"))
	// A figure whose sentence does not say what it measures.
	rejected("Bluegrass is at 21,780.00.", "does not say what 21780 measures", e("t2", 0, ni+"/amount", "21780.00"))
	// Gross profit is not net income; an expense line is not revenue.
	rejected("Net income is 104,330.00.", "but the cited field is Gross profit", e("t2", 0, "/profit_and_loss/measures/1/amount", "104330.00"))
	rejected("Revenue is 9,860.00.", "but the cited field is Lab fees", e("t2", 0, "/profit_and_loss/sections/1/lines/0/amount", "9860.00"))

	// The correct metric for the wrong client or month.
	rejected("Birch Dental's net income for October is 21,780.00.", "but the cited figure is Bluegrass Dental PLLC's", e("t2", 0, ni+"/amount", "21780.00"))
	rejected("September net income was 21,780.00.", "in another month", e("t2", 0, ni+"/amount", "21780.00"))
	ok("Birch Dental's net income is 21,780.00.", e("t5", 0, "/closes/0/net_income", "21780.00"))
	// The wrong currency or basis.
	rejected("Net income is €21,780.00.", "in €", e("t2", 0, ni+"/amount", "21780.00"))
	rejected("On the cash basis, net income is 21,780.00.", "on the cash basis", e("t2", 0, ni+"/amount", "21780.00"))
	ok("Net income is USD 21,780.00 on the accrual basis.", e("t2", 0, ni+"/amount", "21780.00"))

	// Negative versus positive: the direction stated is the change's sign.
	rejected("Lab fees rose 260.00.", "rise of 260", e("t2", 0, "/profit_and_loss/sections/1/lines/0/change", "-260.00"))
	ok("Lab fees fell 260.00.", e("t2", 0, "/profit_and_loss/sections/1/lines/0/change", "-260.00"))
	// A balance versus a change.
	rejected("Net income rose 21,780.00.", "as a change, but the cited field is an amount", e("t2", 0, ni+"/amount", "21780.00"))
	rejected("Net income is 1,840.00.", "as an amount, but the cited field is a change", e("t2", 0, ni+"/change", "1840.00"))
	// The prior month's amount stated as current.
	rejected("Net income is 19,940.00.", "prior month's 19940 as current", e("t2", 0, ni+"/prior", "19940.00"))
	// An authorized multi-period comparison passes, each month on its own figure.
	cited := ok("Net income was 21,780.00 in October, up 1,840.00 from 19,940.00 in September.",
		e("t2", 0, ni+"/amount", "21780.00"), e("t2", 0, ni+"/change", "1840.00"), e("t2", 0, ni+"/prior", "19940.00"))
	if cited[0].Label != "Management pack › Net income" || cited[1].Label != "Management pack › Net income · change" || cited[2].Label != "Management pack › Net income · prior month" {
		t.Fatalf("citations name each figure from its typed facts: %v", cited)
	}

	// A forecast is never stated as an actual, nor a scenario as the base plan.
	rejected("Cash closes the week of 2026-12-20 at 23,400.00.", "forecast 23400 as an actual", e("t3", 0, "/weeks/0/closing", "23400.00"), e("t3", 0, "/weeks/0/start", "2026-12-20"))
	ok("Cash is forecast to close the week of 2026-12-20 at 23,400.00.", e("t3", 0, "/weeks/0/closing", "23400.00"), e("t3", 0, "/weeks/0/start", "2026-12-20"))
	rejected("Cash is forecast to close that week at 41,400.00.", "without saying it is a scenario", e("t3", 0, "/scenario/weeks/0/closing", "41400.00"))
	ok("If the bonus slips, cash is forecast to close that week at 41,400.00.", e("t3", 0, "/scenario/weeks/0/closing", "41400.00"))
	// Book cash is not available bank cash; a stale capture is said to be stale.
	rejected("Available cash is 64,120.00 as of the last capture.", "available at the bank", e("t3", 0, "/opening/amount", "64120.00"))
	rejected("Opening book cash is 64,120.00.", "the cited read is stale", e("t3", 0, "/opening/amount", "64120.00"))
	ok("Opening book cash was 64,120.00 as of the last capture, which is stale.", e("t3", 0, "/opening/amount", "64120.00"))
	// A superseded report revision is said to be one.
	rejected("Net income is 20,100.00.", "the cited read is stale", e("t7", 0, "/pack/profit_and_loss/measures/0/amount", "20100.00"))
	ok("The superseded report showed net income of 20,100.00.", e("t7", 0, "/pack/profit_and_loss/measures/0/amount", "20100.00"))

	// A budget is said to be one, and year to date is not a month.
	rejected("The rent budget was 8,500.00.", "calls 8500 a budget", e("t4", 0, "/lines/0/month/actual", "8500.00"))
	rejected("Rent was 8,000.00.", "without saying it is the budget", e("t4", 0, "/lines/0/month/budget", "8000.00"))
	ok("Rent was 8,500.00 against a budget of 8,000.00.", e("t4", 0, "/lines/0/month/actual", "8500.00"), e("t4", 0, "/lines/0/month/budget", "8000.00"))
	rejected("Rent was 85,000.00.", "year-to-date 85000 as a month's", e("t4", 0, "/lines/0/ytd/actual", "85000.00"))
	ok("Rent is 85,000.00 year to date.", e("t4", 0, "/lines/0/ytd/actual", "85000.00"))
	// A zero-base budget line: the same number as an amount and as the variance, each bound by its own field.
	ok("Marketing had a budget of 0.00 and spent 540.00, 540.00 over budget.",
		e("t4", 0, "/lines/1/month/budget", "0.00"), e("t4", 0, "/lines/1/month/actual", "540.00"), e("t4", 0, "/lines/1/month/variance", "540.00"))
	ok("Rent is 500.00 over budget.", e("t4", 0, "/lines/0/month/variance", "500.00"))

	// Drivers that do not account for a change must say what remains; a cause must be cited or hedged.
	rejected("Revenue rose 4,940.00, driven by new patients up 3,000.00 and hygiene fees up 1,000.00.", "account for 4000.00 of 4940.00",
		e("t6", 0, "/rows/0/change", "4940.00"), e("t6", 0, "/rows/0/drivers/0/change", "3000.00"), e("t6", 0, "/rows/0/drivers/1/change", "1000.00"))
	ok("Revenue rose 4,940.00, driven by new patients up 3,000.00 and hygiene fees up 1,000.00; the rest is unexplained.",
		e("t6", 0, "/rows/0/change", "4940.00"), e("t6", 0, "/rows/0/drivers/0/change", "3000.00"), e("t6", 0, "/rows/0/drivers/1/change", "1000.00"))
	rejected("Revenue rose 4,940.00 because of new patients.", "cause the reads do not show", e("t6", 0, "/rows/0/change", "4940.00"))
	ok("Revenue rose 4,940.00, which may reflect new patients.", e("t6", 0, "/rows/0/change", "4940.00"))

	// Arithmetic across fields is computed by the server, exactly.
	ok("Patient fees of 128,400.00 less expenses of 106,620.00 leave 21,780.00.",
		e("t2", 0, "/profit_and_loss/sections/0/lines/0/amount", "128400.00"), e("t2", 0, "/profit_and_loss/sections/1/total/amount", "106620.00"),
		derived(0, "difference", "21780.00", "t2", "/profit_and_loss/sections/0/lines/0/amount", "t2", "/profit_and_loss/sections/1/total/amount"))
	ok("Rent and marketing total 9,040.00.", derived(0, "sum", "9040.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
	rejected("Rent and marketing total 9,400.00.", "is 9040.00, not 9400.00", derived(0, "sum", "9400.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
	rejected("Rent and payroll total 9,040.00.", "does not name Marketing", derived(0, "sum", "9040.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
	ok("Net income is 17.0% of patient fees.", derived(0, "ratio", "17.0%", "t2", ni+"/amount", "t2", "/profit_and_loss/sections/0/lines/0/amount"))
	rejected("Net income is 16.9% of patient fees.", "not 16.9%", derived(0, "ratio", "16.9%", "t2", ni+"/amount", "t2", "/profit_and_loss/sections/0/lines/0/amount"))
	// A ratio to a zero base is refused rather than invented.
	rejected("Marketing is 100% of its budget.", "cannot be computed", derived(0, "ratio", "100%", "t4", "/lines/1/month/actual", "t4", "/lines/1/month/budget"))
	// Amounts and changes are not added together.
	rejected("Net income and its change total 23,620.00.", "mixes changes and amounts", derived(0, "sum", "23620.00", "t2", ni+"/amount", "t2", ni+"/change"))
}
