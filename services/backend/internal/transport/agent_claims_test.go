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
		"t4": read(`{"currency":"USD","lines":[{"name":"Rent","month":{"actual":"8500.00","budget":"8000.00","variance":"500.00","percent":"6.3%"},"ytd":{"actual":"85000.00","budget":"80000.00","variance":"5000.00"}},
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
	ok("From the stale opening, cash is forecast to close the week of 2026-12-20 at 23,400.00.", e("t3", 0, "/weeks/0/closing", "23400.00"), e("t3", 0, "/weeks/0/start", "2026-12-20"))
	rejected("From the stale opening, cash is forecast to close that week at 41,400.00.", "without saying it is a scenario", e("t3", 0, "/scenario/weeks/0/closing", "41400.00"))
	ok("If the bonus slips, cash is forecast from the stale opening to close that week at 41,400.00.", e("t3", 0, "/scenario/weeks/0/closing", "41400.00"))
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
	rejected("Rent and utilities total 9,040.00.", "does not name Marketing", derived(0, "sum", "9040.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
	ok("Net income is 17.0% of patient fees.", derived(0, "ratio", "17.0%", "t2", ni+"/amount", "t2", "/profit_and_loss/sections/0/lines/0/amount"))
	rejected("Net income is 16.9% of patient fees.", "not 16.9%", derived(0, "ratio", "16.9%", "t2", ni+"/amount", "t2", "/profit_and_loss/sections/0/lines/0/amount"))
	// A ratio to a zero base is refused rather than invented.
	rejected("Marketing is 100% of its budget.", "cannot be computed", derived(0, "ratio", "100%", "t4", "/lines/1/month/actual", "t4", "/lines/1/month/budget"))
	// Review follow-ups. A client the reads never mention, named from the firm's clients, is still another client.
	reads["t8"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","basis":"cash","profit_and_loss":{"measures":[{"name":"Net income","amount":"20500.00"}]}}`, "Management pack, cash basis", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	reads["t9"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-09","basis":"accrual","profit_and_loss":{"measures":[{"name":"Net income","amount":"19940.00"}]}}`, "September pack", "management_pack", "Bluegrass Dental PLLC", "2026-09")
	reads["t10"] = read(`{"state":"ready","changes":[{"account":"Patient fees","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}],"changed":[{"name":"Hygiene fees","this_month":"6000.00","last_month":"5000.00"}],"unattributed":{"this_month":"940.00","last_month":"0.00"}}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	reads["t11"] = agentRead{text: "October patient visits rose after the marketing campaign started.", doc: true, link: chatCitation{"Evidence #9", "/dashboard/closes/7"}, title: "Read evidence text"}
	if _, why := agentSupport("facts", "Acme Dental's net income is 21,780.00.", []agentSupportEntry{e("t2", 0, ni+"/amount", "21780.00")}, reads, nil, "Acme Dental", "Bluegrass Dental PLLC"); !strings.Contains(why, "to Acme Dental") {
		t.Fatalf("a firm client no read named is still another client: %q", why)
	}
	// A percentage is held to its line like an amount.
	rejected("Net income margin is 6.3%.", "calls 6.3% net income", e("t4", 0, "/lines/0/month/percent", "6.3%"))
	ok("Rent is 6.3% over budget.", e("t4", 0, "/lines/0/month/percent", "6.3%"))
	// A derived figure stays within one client and basis, and names every month it combines.
	rejected("Net income totals 43,560.00 for Bluegrass Dental PLLC.", "must name each", derived(0, "sum", "43560.00", "t2", ni+"/amount", "t5", "/closes/0/net_income"))
	ok("Net income of Bluegrass Dental PLLC and Birch Dental totals 43,560.00.", derived(0, "sum", "43560.00", "t2", ni+"/amount", "t5", "/closes/0/net_income"))
	rejected("Net income totals 42,280.00 on both bases.", "different accounting bases", derived(0, "sum", "42280.00", "t2", ni+"/amount", "t8", "/profit_and_loss/measures/0/amount"))
	rejected("Net income totals 41,720.00.", "must name September 2026", derived(0, "sum", "41720.00", "t2", ni+"/amount", "t9", "/profit_and_loss/measures/0/amount"))
	ok("Net income totals 41,720.00 across September and October.", derived(0, "sum", "41720.00", "t2", ni+"/amount", "t9", "/profit_and_loss/measures/0/amount"))
	// The changes read's payee rows are drivers: their movements must add up to the change, or the rest is named.
	rejected("Patient fees rose 4,940.00, driven by new patients at 3,000.00 and hygiene fees rising 1,000.00.", "account for 4000.00 of 4940.00",
		e("t10", 0, "/changes/0/change", "4940.00"), e("t10", 0, "/changes/0/new/0/this_month", "3000.00"),
		derived(0, "difference", "1000.00", "t10", "/changes/0/changed/0/this_month", "t10", "/changes/0/changed/0/last_month"))
	ok("Patient fees rose 4,940.00, driven by new patients at 3,000.00 and hygiene fees rising 1,000.00; 940.00 of patient fees is unattributed.",
		e("t10", 0, "/changes/0/change", "4940.00"), e("t10", 0, "/changes/0/new/0/this_month", "3000.00"),
		derived(0, "difference", "1000.00", "t10", "/changes/0/changed/0/this_month", "t10", "/changes/0/changed/0/last_month"),
		e("t10", 0, "/changes/0/unattributed/this_month", "940.00"))
	// A quote supports a cause only when it names it.
	rejected("Revenue rose 4,940.00 because of higher fees.", "cause the reads do not show",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "patient visits rose after"})
	ok("Revenue rose 4,940.00 because of the marketing campaign.",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "after the marketing campaign started"})

	// What a derived figure is called is checked too; a month and its year to date are each qualified on their own;
	// offsetting drivers explain a smaller net change.
	rejected("Rent and marketing make net income 9,040.00.", "which none of its fields measures", derived(0, "sum", "9040.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
	ok("Rent is 8,500.00 this month and 85,000.00 year to date.", e("t4", 0, "/lines/0/month/actual", "8500.00"), e("t4", 0, "/lines/0/ytd/actual", "85000.00"))
	reads["t12"] = read(`{"rows":[{"account":"Revenue","change":"100.00","drivers":[{"payee":"Acme","change":"500.00"},{"payee":"Zenith","change":"-400.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	ok("Revenue rose 100.00, driven by Acme up 500.00 and Zenith down 400.00.",
		e("t12", 0, "/rows/0/change", "100.00"), e("t12", 0, "/rows/0/drivers/0/change", "500.00"), e("t12", 0, "/rows/0/drivers/1/change", "-400.00"))

	// Each figure takes its nearest metric; month and currency words in any case; no budget joins an actual; a hedge
	// elsewhere in the sentence does not hedge the cause.
	rejected("Net income is 8,000.00 versus receivables at 8,000.00.", "calls 8000 net income", e("t1", 0, "/receivables", "8000.00"))
	rejected("Net income in september was 21,780.00.", "in another month", e("t2", 0, ni+"/amount", "21780.00"))
	rejected("Net income is eur 21,780.00.", "in eur", e("t2", 0, ni+"/amount", "21780.00"))
	ok("Net income may be 21,780.00 in October.", e("t2", 0, ni+"/amount", "21780.00"))
	rejected("Rent and marketing total 8,540.00.", "mixes a level with a budget", derived(0, "sum", "8540.00", "t4", "/lines/0/month/budget", "t4", "/lines/1/month/actual"))
	rejected("Revenue rose 4,940.00 because of higher fees, which may be reviewed.", "cause the reads do not show", e("t6", 0, "/rows/0/change", "4940.00"))
	ok("Revenue rose 4,940.00, which may be due to higher fees.", e("t6", 0, "/rows/0/change", "4940.00"))

	// Round three: a direction stated without its figure; a publication section keeps currency and basis; a ratio's
	// written precision; an unverified publication; every cause; drivers per row; an introductory year to date.
	rejected("Lab fees rose.", "moved the other way", e("t2", 0, "/profit_and_loss/sections/1/lines/0/change", "-260.00"))
	rejected("Net income rose.", "not a change", e("t2", 0, ni+"/amount", "21780.00"))
	ok("Net income rose.", e("t2", 0, ni+"/change", "1840.00"))
	section := projectPublication(decode(`{"publication":{"id":"4","state":"current"},"pack":`+pack+`}`), "profit_and_loss")
	raw, _ := json.Marshal(section)
	reads["t14"] = agentRead{text: string(raw), value: decode(string(raw)), link: chatCitation{"Published report", "/dashboard/closes/7"}, title: "Published report", tool: "read_publication"}
	rejected("Net income is €21,780.00.", "in €", e("t14", 0, "/profit_and_loss/measures/0/amount", "21780.00"))
	rejected("On the cash basis, net income is 21,780.00.", "on the cash basis", e("t14", 0, "/profit_and_loss/measures/0/amount", "21780.00"))
	reads["t13"] = read(`{"lines":[{"name":"Payroll","amount":"1749.00"},{"name":"Total income","amount":"10000.00"}]}`, "Statement", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Payroll is 17.0% of revenue.", "is 17.49, not 17.0%", derived(0, "ratio", "17.0%", "t13", "/lines/0/amount", "t13", "/lines/1/amount"))
	ok("Payroll is 17.5% of revenue.", derived(0, "ratio", "17.5%", "t13", "/lines/0/amount", "t13", "/lines/1/amount"))
	ok("Payroll is 17% of revenue.", derived(0, "ratio", "17%", "t13", "/lines/0/amount", "t13", "/lines/1/amount"))
	reads["t15"] = read(`{"publication":{"id":"5","state":"unverified"},"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","profit_and_loss":{"measures":[{"name":"Net income","amount":"21780.00"}]}}}`, "Published report", "read_publication", "", "")
	rejected("Net income is 21,780.00.", "the cited read is stale", e("t15", 0, "/pack/profit_and_loss/measures/0/amount", "21780.00"))
	ok("The published net income is 21,780.00, which could not be verified against the books.", e("t15", 0, "/pack/profit_and_loss/measures/0/amount", "21780.00"))
	rejected("Revenue rose 4,940.00 because of the marketing campaign, and expenses rose due to seasonality.", "due to seasonality",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "after the marketing campaign started"})
	reads["t16"] = read(`{"state":"ready","changes":[{"account":"Patient fees","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}]},{"account":"Expenses","change":"3100.00"}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Patient fees rose 4,940.00, driven by new patients at 3,000.00, while expenses rose 3,100.00.", "account for 3000.00 of 4940.00",
		e("t16", 0, "/changes/0/change", "4940.00"), e("t16", 0, "/changes/0/new/0/this_month", "3000.00"), e("t16", 0, "/changes/1/change", "3100.00"))
	ok("Year to date, rent was 85,000.00.", e("t4", 0, "/lines/0/ytd/actual", "85000.00"))

	// Round four: what a derived result is said to be; a stale or incomplete cash forecast; a remainder named, not qualified.
	reads["t17"] = read(`{"lines":[{"name":"Revenue","amount":"100.00"},{"name":"Revenue","amount":"200.00"}]}`, "Statement", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Revenue rose by 300.00.", "sum 300 as a change", derived(0, "sum", "300.00", "t17", "/lines/0/amount", "t17", "/lines/1/amount"))
	ok("Net income rose 1,840.00 from September to October.", derived(0, "difference", "1840.00", "t2", ni+"/amount", "t9", "/profit_and_loss/measures/0/amount"))
	rejected("Net income fell 1,840.00 from September to October.", "computed difference is 1840.00", derived(0, "difference", "1840.00", "t2", ni+"/amount", "t9", "/profit_and_loss/measures/0/amount"))
	rejected("Patient fees rose 21,780.00 over total expense.", "between different lines as a movement",
		derived(0, "difference", "21780.00", "t2", "/profit_and_loss/sections/0/lines/0/amount", "t2", "/profit_and_loss/sections/1/total/amount"))
	ok("Patient fees exceed total expense by 21,780.00.",
		derived(0, "difference", "21780.00", "t2", "/profit_and_loss/sections/0/lines/0/amount", "t2", "/profit_and_loss/sections/1/total/amount"))
	rejected("Rent and marketing total €9,040.00.", "but its fields are in USD", derived(0, "sum", "9040.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
	reads["t18"] = read(`{"state":"ready","entity":"Bluegrass Dental PLLC","currency":"USD","opening":{"amount":"64120.00","stale":true},"open_items":{"complete":false},"weeks":[{"start":"2026-12-20","closing":"23400.00"}]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	rejected("Cash is forecast to close that week at 23,400.00.", "the cited read is stale", e("t18", 0, "/weeks/0/closing", "23400.00"))
	rejected("From a stale opening, cash is forecast to close that week at 23,400.00.", "inputs are incomplete", e("t18", 0, "/weeks/0/closing", "23400.00"))
	ok("From a stale opening and incomplete open items, cash is forecast to close that week at 23,400.00.", e("t18", 0, "/weeks/0/closing", "23400.00"))
	rejected("Revenue rose 4,940.00, including new patients up 3,000.00 and hygiene fees up 1,000.00.", "account for 4000.00 of 4940.00",
		e("t6", 0, "/rows/0/change", "4940.00"), e("t6", 0, "/rows/0/drivers/0/change", "3000.00"), e("t6", 0, "/rows/0/drivers/1/change", "1000.00"))

	// Round five: "rose to" needs the rise shown; one field is never counted twice.
	rejected("Net income rose to 21,780.00.", "nothing cited shows it rose", e("t2", 0, ni+"/amount", "21780.00"))
	ok("Net income rose to 21,780.00, up 1,840.00.", e("t2", 0, ni+"/amount", "21780.00"), e("t2", 0, ni+"/change", "1840.00"))
	ok("Net income rose to 21,780.00 from 19,940.00.", e("t2", 0, ni+"/amount", "21780.00"), e("t2", 0, ni+"/prior", "19940.00"))
	rejected("Net income fell to 21,780.00 from 19,940.00.", "nothing cited shows it fell", e("t2", 0, ni+"/amount", "21780.00"), e("t2", 0, ni+"/prior", "19940.00"))
	rejected("Net income totals 43,560.00.", "same field twice", derived(0, "sum", "43560.00", "t2", ni+"/amount", "t2", ni+"/amount"))

	// Round six: a difference is said to be one; money is not added to a count.
	rejected("Across September and October, net income is 1,840.00.", "as an amount", derived(0, "difference", "1840.00", "t2", ni+"/amount", "t9", "/profit_and_loss/measures/0/amount"))
	reads["t19"] = read(`{"state":"ready","currency":"USD","unapproved_schedules":2,"weeks":[{"start":"2026-12-20","receipts":"14200.00"}]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	rejected("Forecast receipts and unapproved schedules total 14,202.00.", "combines money with a count", derived(0, "sum", "14202.00", "t19", "/weeks/0/receipts", "t19", "/unapproved_schedules"))

	// Amounts and changes are not added together.
	rejected("Net income and its change total 23,620.00.", "mixes changes and amounts", derived(0, "sum", "23620.00", "t2", ni+"/amount", "t2", ni+"/change"))
}
