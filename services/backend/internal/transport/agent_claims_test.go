package transport

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
		"t4": read(`{"currency":"USD","lines":[{"name":"Rent","month":{"actual":"8500.00","budget":"8000.00","variance":"500.00","percent":"6.3"},"ytd":{"actual":"85000.00","budget":"80000.00","variance":"5000.00"}},
			{"name":"Marketing","month":{"actual":"540.00","budget":"0.00","variance":"540.00","percent":null}}]}`, "Budget against actual", "budget_comparison", "Bluegrass Dental PLLC", "2026-10"),
		"t5": read(`{"closes":[{"id":"9","entity":"Birch Dental","period":"2026-10","currency":"USD","net_income":"21780.00"}]}`, "List client closes", "list_closes", "", ""),
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
	ok("Opening book cash was 64,120.00 at the last capture, which is stale.", e("t3", 0, "/opening/amount", "64120.00"))
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
	reads["t8"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","basis":"cash","profit_and_loss":{"measures":[{"name":"Net income","amount":"20500.00"}]}}`, "Management pack, cash basis", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	reads["t9"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-09","currency":"USD","basis":"accrual","profit_and_loss":{"measures":[{"name":"Net income","amount":"19940.00"}]}}`, "September pack", "management_pack", "Bluegrass Dental PLLC", "2026-09")
	reads["t10"] = read(`{"state":"ready","changes":[{"account":"Patient fees","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}],"changed":[{"name":"Hygiene fees","this_month":"6000.00","last_month":"5000.00"}],"unattributed":{"this_month":"940.00","last_month":"0.00"}}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	reads["t11"] = agentRead{text: "The marketing campaign drove revenue up in October.", doc: true, link: chatCitation{"Evidence #9", "/dashboard/closes/7"}, title: "Read evidence text"}
	if _, why := agentSupport("facts", "Acme Dental's net income is 21,780.00.", []agentSupportEntry{e("t2", 0, ni+"/amount", "21780.00")}, reads, nil, "Acme Dental", "Bluegrass Dental PLLC"); !strings.Contains(why, "to Acme Dental") {
		t.Fatalf("a firm client no read named is still another client: %q", why)
	}
	// A percentage is held to its line like an amount.
	rejected("Net income margin is 6.3%.", "calls 6.3% net income", e("t4", 0, "/lines/0/month/percent", "6.3"))
	ok("Rent is 6.3% over budget.", e("t4", 0, "/lines/0/month/percent", "6.3"))
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
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "The marketing campaign drove revenue up"})
	ok("Revenue rose 4,940.00 because of the marketing campaign.",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "The marketing campaign drove revenue up"})

	// What a derived figure is called is checked too; a month and its year to date are each qualified on their own;
	// offsetting drivers explain a smaller net change.
	rejected("Rent and marketing make net income 9,040.00.", "nothing it cites measures it", derived(0, "sum", "9040.00", "t4", "/lines/0/month/actual", "t4", "/lines/1/month/actual"))
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
	rejected("Net income is 21,780.00.", "could not be verified", e("t15", 0, "/pack/profit_and_loss/measures/0/amount", "21780.00"))
	ok("The published net income is 21,780.00, which could not be verified against the books.", e("t15", 0, "/pack/profit_and_loss/measures/0/amount", "21780.00"))
	rejected("The stale report's net income is 21,780.00.", "could not be verified", e("t15", 0, "/pack/profit_and_loss/measures/0/amount", "21780.00"))
	rejected("Revenue rose 4,940.00 because of the marketing campaign, and expenses rose due to seasonality.", "due to seasonality",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "The marketing campaign drove revenue up"},
		e("t2", 0, "/profit_and_loss/sections/1/total/change", "3100.00"))
	reads["t16"] = read(`{"state":"ready","changes":[{"account":"Patient fees","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}]},{"account":"Expenses","change":"3100.00"}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Patient fees rose 4,940.00, driven by new patients at 3,000.00, while expenses rose 3,100.00.", "account for 3000.00 of 4940.00",
		e("t16", 0, "/changes/0/change", "4940.00"), e("t16", 0, "/changes/0/new/0/this_month", "3000.00"), e("t16", 0, "/changes/1/change", "3100.00"))
	ok("Year to date, rent was 85,000.00.", e("t4", 0, "/lines/0/ytd/actual", "85000.00"))

	// Round four: what a derived result is said to be; a stale or incomplete cash forecast; a remainder named, not qualified.
	reads["t17"] = read(`{"lines":[{"account_id":"41","name":"Revenue","amount":"100.00"},{"account_id":"42","name":"Revenue","amount":"200.00"}]}`, "Statement", "management_pack", "Bluegrass Dental PLLC", "2026-10")
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

	// Round seven: a shared word names no line; each row's remainder in its own part; every input's currency;
	// incomplete inputs; derived citations link to their rows.
	rejected("Patient fees were 9,860.00.", "the cited field is Lab fees", e("t2", 0, "/profit_and_loss/sections/1/lines/0/amount", "9860.00"))
	ok("Lab fees were 9,860.00.", e("t2", 0, "/profit_and_loss/sections/1/lines/0/amount", "9860.00"))
	reads["t20"] = read(`{"state":"ready","changes":[{"account":"Patient fees","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}]},{"account":"Rent","change":"500.00","changed":[{"name":"Landlord","this_month":"8500.00","last_month":"8300.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Patient fees rose 4,940.00, driven by new patients at 3,000.00, while rent rose 500.00 from the landlord, the rest unexplained.", "account for 3000.00 of 4940.00",
		e("t20", 0, "/changes/0/change", "4940.00"), e("t20", 0, "/changes/0/new/0/this_month", "3000.00"), e("t20", 0, "/changes/1/change", "500.00"))
	reads["t21"] = read(`{"items":[{"label":"Prepaid insurance","amount":"550.00"}]}`, "Drafts", "list_work", "", "")
	rejected("Prepaid insurance and rent total 9,050.00.", "unknown currency", derived(0, "sum", "9050.00", "t21", "/items/0/amount", "t4", "/lines/0/month/actual"))
	reads["t22"] = read(`{"state":"ready","currency":"USD","open_items":{"complete":false},"weeks":[{"start":"2026-12-13","receipts":"26400.00"},{"start":"2026-12-20","receipts":"14200.00"}]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	rejected("Forecast receipts for the two weeks total 40,600.00.", "incomplete inputs", derived(0, "sum", "40600.00", "t22", "/weeks/0/receipts", "t22", "/weeks/1/receipts"))
	ok("Forecast receipts for the two weeks total 40,600.00, with open items incomplete.", derived(0, "sum", "40600.00", "t22", "/weeks/0/receipts", "t22", "/weeks/1/receipts"))
	reads["t23"] = agentRead{text: `{"published":[{"close_id":"9","publication_id":"12","entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","net_income":"21780.00"}]}`, value: decode(`{"published":[{"close_id":"9","publication_id":"12","entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","net_income":"21780.00"}]}`), link: chatCitation{"Overview", "/dashboard/executive"}, title: "Your clients", tool: "executive_overview", executive: true}
	if cited := ok("Published net income is 17.0% of patient fees.", derived(0, "ratio", "17.0%", "t23", "/published/0/net_income", "t2", "/profit_and_loss/sections/0/lines/0/amount")); !strings.Contains(cited[0].Href, "publication=12") {
		t.Fatalf("a derived figure's inputs link to their rows: %v", cited)
	}

	// Round eight: each figure's own month; a negated passage; one client's movement.
	rejected("Net income was 21,780.00 in September versus 19,940.00 in October.", "in another month", e("t2", 0, ni+"/amount", "21780.00"), e("t2", 0, ni+"/prior", "19940.00"))
	ok("Net income was 21,780.00 in October versus 19,940.00 in September.", e("t2", 0, ni+"/amount", "21780.00"), e("t2", 0, ni+"/prior", "19940.00"))
	reads["t24"] = agentRead{text: "The marketing campaign did not affect revenue this month.", doc: true, link: chatCitation{"Evidence #10", "/dashboard/closes/7"}, title: "Read evidence text"}
	rejected("Revenue rose 4,940.00 because of the marketing campaign.", "cause the reads do not show",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t24", sentence: 0, quote: "The marketing campaign did not affect revenue"})
	reads["t25"] = read(`{"closes":[{"id":"10","entity":"Birch Dental","period":"2026-09","currency":"USD","name":"Net income","amount":"19000.00"}]}`, "List client closes", "list_closes", "", "")
	rejected("Net income rose 2,780.00 from Birch Dental in September to Bluegrass Dental PLLC in October.", "between different lines as a movement",
		derived(0, "difference", "2780.00", "t2", ni+"/amount", "t25", "/closes/0/amount"))

	// Round nine: one figure for two clients; a result labelled by one input; a bare "as of"; a passage that names a
	// cause without saying it acted.
	rejected("Birch Dental and Bluegrass Dental PLLC each had net income of 21,780.00.", "nothing it cites is Birch Dental's", e("t2", 0, ni+"/amount", "21780.00"))
	rejected("Net income including rent totals 30,280.00.", "do not all measure", derived(0, "sum", "30280.00", "t2", ni+"/amount", "t4", "/lines/0/month/actual"))
	rejected("Net income as of October is 20,100.00.", "the cited read is stale", e("t7", 0, "/pack/profit_and_loss/measures/0/amount", "20100.00"))
	reads["t26"] = agentRead{text: "The marketing campaign is scheduled for next year.", doc: true, link: chatCitation{"Evidence #11", "/dashboard/closes/7"}, title: "Read evidence text"}
	rejected("Revenue rose 4,940.00 because of the marketing campaign.", "cause the reads do not show",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t26", sentence: 0, quote: "The marketing campaign is scheduled"})

	// Round ten: exact client identity; the effect a causal passage names; a derived change's order; a budget
	// percentage stored bare and signed.
	if _, why := agentSupport("facts", "Acme net income is 21,780.00.", []agentSupportEntry{e("t27", 0, "/closes/0/net_income", "21780.00")},
		map[string]agentRead{"t27": read(`{"closes":[{"id":"11","entity":"Acme Europe","period":"2026-10","currency":"USD","net_income":"21780.00"}]}`, "List client closes", "list_closes", "", "")}, nil, "Acme", "Acme Europe"); !strings.Contains(why, "to Acme") && !strings.Contains(why, "Acme") {
		t.Fatalf("Acme is not Acme Europe: %q", why)
	}
	reads["t28"] = agentRead{text: "The marketing campaign raised payroll expenses.", doc: true, link: chatCitation{"Evidence #12", "/dashboard/closes/7"}, title: "Read evidence text"}
	rejected("Revenue rose 4,940.00 because of the marketing campaign.", "cause the reads do not show",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t28", sentence: 0, quote: "The marketing campaign raised payroll"})
	rejected("Net income rose 1,840.00 from October to September.", "computed from September to October", derived(0, "difference", "1840.00", "t2", ni+"/amount", "t9", "/profit_and_loss/measures/0/amount"))
	rejected("Rent is 6.3.", "bound to no read", e("t4", 0, "/lines/0/month/percent", "6.3"))
	reads["t29"] = read(`{"currency":"USD","lines":[{"name":"Supplies","month":{"actual":"9370.00","budget":"10000.00","variance":"-630.00","percent":"-6.3"}}]}`, "Budget against actual", "budget_comparison", "Bluegrass Dental PLLC", "2026-10")
	ok("Supplies are 6.3% under budget.", e("t29", 0, "/lines/0/month/percent", "-6.3"))
	rejected("Supplies are 6.3% over budget.", "but the variance is -6.3", e("t29", 0, "/lines/0/month/percent", "-6.3"))

	// Round eleven: no month added to its year to date; a figure-less forecast; a stale derived figure said to be stale.
	rejected("Rent totals 93,500.00.", "year-to-date", derived(0, "sum", "93500.00", "t4", "/lines/0/month/actual", "t4", "/lines/0/ytd/actual"))
	reads["t30"] = read(`{"state":"ready","currency":"USD","opening":{"amount":"64120.00","stale":false},"open_items":{"complete":true},"weeks":[{"start":"2026-12-20","closing":"23400.00"}]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	rejected("Cash closes positive next week.", "as a fact", e("t30", 0, "/weeks/0/closing", "23400.00"))
	ok("Cash is forecast to close positive next week.", e("t30", 0, "/weeks/0/closing", "23400.00"))
	reads["t31"] = read(`{"publication":{"id":"6","state":"superseded"},"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"20100.00"},{"name":"Gross profit","amount":"1000.00"}]}}}`, "Published report", "read_publication", "", "")
	ok("The report is stale; its net income and gross profit total 21,100.00.", derived(0, "sum", "21100.00", "t31", "/pack/profit_and_loss/measures/0/amount", "t31", "/pack/profit_and_loss/measures/1/amount"))

	// Round twelve: a driver's name is not the remainder; a hedge qualifies the cause, not something after it.
	reads["t32"] = read(`{"rows":[{"account":"Revenue","change":"4940.00","drivers":[{"payee":"Other services","change":"3000.00"},{"payee":"Hygiene","change":"1000.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Revenue rose 4,940.00, including Other services up 3,000.00 and Hygiene up 1,000.00.", "account for 4000.00 of 4940.00",
		e("t32", 0, "/rows/0/change", "4940.00"), e("t32", 0, "/rows/0/drivers/0/change", "3000.00"), e("t32", 0, "/rows/0/drivers/1/change", "1000.00"))
	rejected("Revenue rose 4,940.00 because of a marketing campaign which may end soon.", "cause the reads do not show", e("t6", 0, "/rows/0/change", "4940.00"))
	ok("Revenue rose 4,940.00, possibly because of a marketing campaign.", e("t6", 0, "/rows/0/change", "4940.00"))

	// Round thirteen: an income line is not net income; a stale prior capture; many clients stay fast.
	reads["t33"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"sections":[{"name":"Other Income","lines":[{"name":"Interest income","amount":"8000.00"}]}]}}`, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Net income is 8,000.00.", "but the cited field is Interest income", e("t33", 0, "/profit_and_loss/sections/0/lines/0/amount", "8000.00"))
	ok("Interest income is 8,000.00.", e("t33", 0, "/profit_and_loss/sections/0/lines/0/amount", "8000.00"))
	reads["t34"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","prior":{"period":"2026-09","stale":true},"profit_and_loss":{"measures":[{"name":"Net income","amount":"100.00","prior":"90.00","change":"10.00"}]}}`, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Net income was 90.00 in September.", "the cited read is stale", e("t34", 0, "/profit_and_loss/measures/0/prior", "90.00"))
	ok("Net income is 100.00 in October.", e("t34", 0, "/profit_and_loss/measures/0/amount", "100.00"))
	many := []string{}
	for i := 0; i < 5000; i++ {
		many = append(many, "Client number "+strings.Repeat("x", i%7)+string(rune('a'+i%26))+" "+strings.Repeat("y", i%5))
	}
	many = append(many, "Bluegrass Dental PLLC")
	began := time.Now()
	long := []agentSupportEntry{}
	text := ""
	for i := 0; i < 20; i++ {
		text += "Bluegrass Dental PLLC's net income is 21,780.00. "
		long = append(long, e("t2", i, ni+"/amount", "21780.00"))
	}
	if _, why := agentSupport("facts", strings.TrimSpace(text), long, reads, nil, many...); why != "" {
		t.Fatalf("twenty figures over 5,000 clients: %s", why)
	}
	if took := time.Since(began); took > 2*time.Second {
		t.Fatalf("twenty figures over 5,000 clients took %s", took)
	}

	// Round fourteen: last month is the prior month; a payee grouping is no cause; a two-letter client.
	rejected("New patients were 0.00 in October.", "in another month", e("t10", 0, "/changes/0/new/0/last_month", "0.00"))
	reads["t35"] = read(`{"state":"ready","changes":[{"account":"Revenue","change":"3000.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Revenue rose 3,000.00 because of new patients.", "cause the reads do not show", e("t35", 0, "/changes/0/change", "3000.00"), e("t35", 0, "/changes/0/new/0/name", "New patients"))
	ok("Revenue rose 3,000.00, all of it from new patients.", e("t35", 0, "/changes/0/change", "3000.00"), e("t35", 0, "/changes/0/new/0/name", "New patients"))
	if _, why := agentSupport("facts", "GE net income is 21,780.00.", []agentSupportEntry{e("t2", 0, ni+"/amount", "21780.00")}, reads, nil, "GE", "Bluegrass Dental PLLC"); !strings.Contains(why, "to GE") {
		t.Fatalf("a two-letter client is still a client: %q", why)
	}

	// Round fifteen: a movement's evidence is the same client's; forecast wording scoped to its figure; a decline as
	// its magnitude; directions per metric; a named variance.
	reads["t36"] = read(`{"closes":[{"id":"12","entity":"Birch Dental","period":"2026-10","currency":"USD","name":"Net income","change":"500.00"}]}`, "List client closes", "list_closes", "", "")
	rejected("Bluegrass Dental PLLC net income rose to 21,780.00.", "nothing cited shows it rose", e("t2", 0, ni+"/amount", "21780.00"), e("t36", 0, "/closes/0/change", "500.00"))
	reads["t37"] = read(`{"state":"ready","currency":"USD","opening":{"amount":"64120.00","stale":false},"open_items":{"complete":true},"weeks":[{"start":"2026-12-20","closing":"23400.00","receipts":"14200.00"}]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	rejected("Cash closed at 23,400.00, while receipts are forecast to rise.", "as an actual", e("t37", 0, "/weeks/0/closing", "23400.00"))
	reads["t38"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-09","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"23780.00"}]}}`, "September pack", "management_pack", "Bluegrass Dental PLLC", "2026-09")
	ok("Net income fell 2,000.00 from September to October.", derived(0, "difference", "-2000.00", "t2", ni+"/amount", "t38", "/profit_and_loss/measures/0/amount"))
	ok("Net income fell 2,000.00 from September to October.", derived(0, "difference", "2000.00", "t2", ni+"/amount", "t38", "/profit_and_loss/measures/0/amount"))
	rejected("Net income rose 2,000.00 from September to October.", "computed difference is -2000.00", derived(0, "difference", "2000.00", "t2", ni+"/amount", "t38", "/profit_and_loss/measures/0/amount"))
	reads["t39"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"sections":[{"name":"Expense","total":{"name":"Total expense","change":"-300.00"}}]}}`, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	ok("Revenue rose while expenses fell.", e("t6", 0, "/rows/0/change", "4940.00"), e("t39", 0, "/profit_and_loss/sections/0/total/change", "-300.00"))
	rejected("Revenue fell while expenses rose.", "moved the other way", e("t6", 0, "/rows/0/change", "4940.00"), e("t39", 0, "/profit_and_loss/sections/0/total/change", "-300.00"))
	ok("Rent's variance against budget is 500.00.", e("t4", 0, "/lines/0/month/variance", "500.00"))

	// Round sixteen: a line named in full; budget totals' year to date and metrics; a Unicode client name.
	reads["t40"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"sections":[{"name":"Expense","lines":[{"name":"Office supplies","amount":"8000.00"}]}]}}`, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Office rent was 8,000.00.", "the cited field is Office supplies", e("t40", 0, "/profit_and_loss/sections/0/lines/0/amount", "8000.00"))
	ok("Office supplies were 8,000.00.", e("t40", 0, "/profit_and_loss/sections/0/lines/0/amount", "8000.00"))
	reads["t41"] = read(`{"currency":"USD","totals":{"income_ytd":{"actual":"1280000.00","budget":"1250000.00"},"net_month":{"actual":"9400.00","budget":"9000.00"}}}`, "Budget against actual", "budget_comparison", "Bluegrass Dental PLLC", "2026-10")
	rejected("Total income in October was 1,280,000.00.", "year-to-date", e("t41", 0, "/totals/income_ytd/actual", "1280000.00"))
	ok("Total income year to date is 1,280,000.00.", e("t41", 0, "/totals/income_ytd/actual", "1280000.00"))
	ok("Net income was 9,400.00.", e("t41", 0, "/totals/net_month/actual", "9400.00"))
	if names := clientsNamed("Revenue at ȺCME is up", []string{"ȺCME", "Acme"}); len(names) != 1 || names[0] != "ȺCME" {
		t.Fatalf("a Unicode client name is matched without shifting offsets: %v", names)
	}

	// Round seventeen: a stale caveat contradicted; a causal verb; a cash plan is not a budget.
	rejected("The old report is stale, but net income is 20,100.00 now.", "the cited read is stale", e("t7", 0, "/pack/profit_and_loss/measures/0/amount", "20100.00"))
	rejected("Marketing raised revenue, which rose 4,940.00.", "cause the reads do not show", e("t6", 0, "/rows/0/change", "4940.00"))
	ok("Marketing may have raised revenue, which rose 4,940.00.", e("t6", 0, "/rows/0/change", "4940.00"))
	ok("The plan has cash at 23,400.00 forecast for that week.", e("t30", 0, "/weeks/0/closing", "23400.00"))

	// Round eighteen: a derived account change is a total; exact decimals; a payee sharing a client's name; May the month.
	reads["t42"] = read(`{"state":"ready","changes":[{"account":"Revenue","this_month":"128400.00","last_month":"123460.00","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Revenue rose 4,940.00, driven by new patients at 3,000.00.", "account for 3000.00 of 4940.00",
		derived(0, "difference", "4940.00", "t42", "/changes/0/this_month", "t42", "/changes/0/last_month"), e("t42", 0, "/changes/0/new/0/this_month", "3000.00"))
	if normalizeFigure("9,999,999,999,999,999.99") == normalizeFigure("10,000,000,000,000,000") || normalizeFigure("(0.00)") != "0" || normalizeFigure("1,250.50") != "1250.5" {
		t.Fatal("figures are normalized exactly, never through a float")
	}
	reads["t43"] = read(`{"currency":"USD","lines":[{"name":"Big","amount":"9999999999999999.99"},{"name":"Zero","amount":"0.00"}]}`, "Statement", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Big and zero total 10,000,000,000,000,000.00.", "not 10000000000000000.00", derived(0, "sum", "10000000000000000.00", "t43", "/lines/0/amount", "t43", "/lines/1/amount"))
	reads["t44"] = read(`{"rows":[{"account":"Revenue","change":"3000.00","drivers":[{"payee":"Birch Dental","change":"3000.00"}]}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	ok("Revenue rose 3,000.00, all of it from Birch Dental up 3,000.00.", e("t44", 0, "/rows/0/change", "3000.00"), e("t44", 0, "/rows/0/drivers/0/change", "3000.00"))
	reads["t45"] = read(`{"rows":[{"account":"Revenue","change":"4940.00"}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-05")
	rejected("May bookings drove revenue, which rose 4,940.00.", "cause the reads do not show", e("t45", 0, "/rows/0/change", "4940.00"))
	ok("Bookings may have driven revenue, which rose 4,940.00 in May.", e("t45", 0, "/rows/0/change", "4940.00"))

	// Round nineteen: one fact read two ways is counted once; a comma before "because".
	reads["t46"] = read(`{"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"21780.00"}]}}}`, "Published report", "read_publication", "", "")
	rejected("Net income totals 43,560.00.", "read two ways", derived(0, "sum", "43560.00", "t2", ni+"/amount", "t46", "/pack/profit_and_loss/measures/0/amount"))
	ok("Revenue rose 4,940.00, because of the marketing campaign.", e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t11", sentence: 0, quote: "The marketing campaign drove revenue up"})

	// Round twenty: every named measure cited; a money variance's sign; a total cited twice; two clients each cited.
	rejected("Net income and receivables are each 8,000.00.", "names net income, but nothing it cites measures it", e("t1", 0, "/receivables", "8000.00"))
	reads["t47"] = read(`{"currency":"USD","lines":[{"name":"Supplies","month":{"actual":"9370.00","budget":"10000.00","variance":"-630.00"}}]}`, "Budget against actual", "budget_comparison", "Bluegrass Dental PLLC", "2026-10")
	rejected("Supplies are 630.00 over budget.", "but the variance is -630", e("t47", 0, "/lines/0/month/variance", "-630.00"))
	ok("Supplies are 630.00 under budget.", e("t47", 0, "/lines/0/month/variance", "-630.00"))
	rejected("Patient fees rose 4,940.00, driven by new patients at 3,000.00.", "account for 3000.00 of 4940.00",
		e("t10", 0, "/changes/0/change", "4940.00"), e("t10", 0, "/changes/0/change", "4940.00"), e("t10", 0, "/changes/0/new/0/this_month", "3000.00"))
	reads["t48"] = read(`{"closes":[{"id":"13","entity":"Birch Dental","period":"2026-10","currency":"USD","net_income":"21780.00"},{"id":"14","entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","net_income":"21780.00"}]}`, "List client closes", "list_closes", "", "")
	ok("Bluegrass Dental PLLC and Birch Dental each had net income of 21,780.00.", e("t48", 0, "/closes/0/net_income", "21780.00"), e("t48", 0, "/closes/1/net_income", "21780.00"))
	rejected("Bluegrass Dental PLLC's net income is 19,000.00, less than Birch Dental's 21,780.00.", "the sentence gives",
		e("t48", 0, "/closes/0/net_income", "21780.00"), e("t25", 0, "/closes/0/amount", "19000.00"))

	// Round twenty-one: timing is not cause; a list of inputs is not the result's label.
	reads["t49"] = agentRead{text: "October revenue rose after the marketing campaign started.", doc: true, link: chatCitation{"Evidence #13", "/dashboard/closes/7"}, title: "Read evidence text"}
	rejected("Revenue rose 4,940.00 because of the marketing campaign.", "cause the reads do not show",
		e("t6", 0, "/rows/0/change", "4940.00"), agentSupportEntry{step: "t49", sentence: 0, quote: "revenue rose after the marketing campaign started"})
	reads["t50"] = read(`{"currency":"USD","lines":[{"name":"Rent","amount":"8500.00"},{"name":"Payroll","amount":"540.00"}]}`, "Statement", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	ok("Rent and payroll total 9,040.00.", derived(0, "sum", "9040.00", "t50", "/lines/0/amount", "t50", "/lines/1/amount"))
	rejected("Payroll including rent totals 9,040.00.", "do not all measure", derived(0, "sum", "9040.00", "t50", "/lines/1/amount", "t50", "/lines/0/amount"))

	// Round twenty-two: a whole number is a count, whatever its key.
	reads["t51"] = read(`{"coverage":{"total":3,"resolved":1}}`, "The hand-over", "get_goal", "Bluegrass Dental PLLC", "2026-10")
	ok("Coverage has 3 accounts.", e("t51", 0, "/coverage/total", "3"))

	// Round twenty-three: an overview's published figure is the report's; one measure's two revisions are not added.
	rejected("Net income is 21,780.00.", "say it is the published report's", e("t23", 0, "/published/0/net_income", "21780.00"))
	ok("The published report shows net income of 21,780.00.", e("t23", 0, "/published/0/net_income", "21780.00"))
	rejected("Net income totals 41,880.00.", "from two revisions", derived(0, "sum", "41880.00", "t2", ni+"/amount", "t7", "/pack/profit_and_loss/measures/0/amount"))

	// Round twenty-four: a cross-client total is not each client's; a count is not net income.
	reads["t52"] = read(`{"closes":[{"id":"15","entity":"Birch Dental","period":"2026-10","currency":"USD","net_income":"200.00"},{"id":"16","entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","net_income":"100.00"}]}`, "List client closes", "list_closes", "", "")
	rejected("Bluegrass Dental PLLC and Birch Dental each had net income of 300.00.", "stated as each client's", derived(0, "sum", "300.00", "t52", "/closes/0/net_income", "t52", "/closes/1/net_income"))
	ok("Bluegrass Dental PLLC and Birch Dental together had net income of 300.00.", derived(0, "sum", "300.00", "t52", "/closes/0/net_income", "t52", "/closes/1/net_income"))
	rejected("Net income is 3.", "nothing it cites measures it", e("t51", 0, "/coverage/total", "3"))

	// Round twenty-five: an unscheduled balance is an actual, not a forecast.
	reads["t53"] = read(`{"state":"ready","currency":"USD","opening":{"amount":"64120.00","stale":false},"open_items":{"complete":true},"unscheduled":[{"label":"Invoice #99","amount":"1200.00"}],"weeks":[]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	ok("Invoice #99 has an unscheduled balance of 1,200.00.", e("t53", 0, "/unscheduled/0/amount", "1200.00"), e("t53", 0, "/unscheduled/0/label", "Invoice #99"))

	// Round twenty-six: a movement backed by its own measure's change; a reason that names the effect.
	rejected("Net income rose while revenue changed.", "not a change", e("t2", 0, ni+"/amount", "21780.00"), e("t6", 0, "/rows/0/change", "4940.00"))
	reads["t54"] = read(`{"publication":{"id":"7","state":"current","reason":"Marketing invoices were coded to rent; corrected."},"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"21780.00","change":"-500.00"}]}}}`, "Published report", "read_publication", "", "")
	rejected("Net income fell 500.00 because of marketing.", "cause the reads do not show",
		e("t54", 0, "/pack/profit_and_loss/measures/0/change", "-500.00"), e("t54", 0, "/publication/reason", "Marketing invoices were coded to rent; corrected."))

	// Round twenty-eight: publication status holds for derived and figure-less claims too.
	reads["t55"] = read(`{"publication":{"id":"8","state":"unverified"},"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"21780.00"},{"name":"Gross profit","amount":"104330.00"}]}}}`, "Published report", "read_publication", "", "")
	rejected("Net income and gross profit total 126,110.00.", "could not be verified", derived(0, "sum", "126110.00", "t55", "/pack/profit_and_loss/measures/0/amount", "t55", "/pack/profit_and_loss/measures/1/amount"))
	rejected("Net income is positive.", "could not be verified", e("t55", 0, "/pack/profit_and_loss/measures/0/amount", "21780.00"))

	// Round twenty-nine: "up" holds a derived movement to its sign; a basis is not the cash measure.
	reads["t56"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-09","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"22040.00"}]}}`, "September pack", "management_pack", "Bluegrass Dental PLLC", "2026-09")
	rejected("Net income up 260.00 from September to October.", "computed difference is -260.00", derived(0, "difference", "260.00", "t2", ni+"/amount", "t56", "/profit_and_loss/measures/0/amount"))
	ok("Net income down 260.00 from September to October.", derived(0, "difference", "260.00", "t2", ni+"/amount", "t56", "/profit_and_loss/measures/0/amount"))
	ok("Net income is 20,500.00 on the cash basis.", e("t8", 0, "/profit_and_loss/measures/0/amount", "20500.00"))

	// Round thirty: a liability named for sales is not sales.
	reads["t57"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","balance_sheet":{"sections":[{"name":"Other Current Liability","lines":[{"name":"Sales tax payable","amount":"500.00"}]}]}}`, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Sales were 500.00.", "the cited field is Sales tax payable", e("t57", 0, "/balance_sheet/sections/0/lines/0/amount", "500.00"))
	ok("Sales tax payable is 500.00.", e("t57", 0, "/balance_sheet/sections/0/lines/0/amount", "500.00"))
	reads["t58"] = read(`{"lines":[{"name":"Sales tax payable","amount":"500.00"}]}`, "Statement", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	rejected("Sales were 500.00.", "the cited field is Sales tax payable", e("t58", 0, "/lines/0/amount", "500.00"))

	// Round thirty-one: one published figure read two ways; a stale state of a source not cited.
	reads["t59"] = agentRead{text: "{}", value: decode(`{"published":[{"close_id":"9","publication_id":"21","entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","net_income":"100.00"}]}`), link: chatCitation{"Overview", "/dashboard/executive"}, title: "Your clients", tool: "executive_overview", executive: true}
	reads["t60"] = read(`{"publication":{"id":"21","state":"current"},"pack":{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","profit_and_loss":{"measures":[{"name":"Net income","amount":"100.00"}]}}}`, "Published report", "read_publication", "", "")
	rejected("The published net income totals 200.00.", "counts the same figure twice", derived(0, "sum", "200.00", "t59", "/published/0/net_income", "t60", "/pack/profit_and_loss/measures/0/amount"))
	rejected("The bank feed is stale; the report's net income is 20,100.00.", "the state stale is not in a cited field", e("t7", 0, "/pack/profit_and_loss/measures/0/amount", "20100.00"))

	// Round thirty-two: an opening dated by its capture; drivers in another sentence.
	reads["t61"] = read(`{"state":"ready","currency":"USD","opening":{"amount":"64120.00","as_of":"2026-12-31","stale":false},"open_items":{"complete":true},"weeks":[]}`, "Cash plan", "cash_plan", "Bluegrass Dental PLLC", "2026-10")
	rejected("Opening book cash in October was 64,120.00.", "in another month", e("t61", 0, "/opening/amount", "64120.00"))
	ok("Opening book cash in December was 64,120.00.", e("t61", 0, "/opening/amount", "64120.00"))
	rejected("Patient fees rose 4,940.00. This was driven by new patients at 3,000.00.", "account for 3000.00 of 4940.00",
		e("t10", 0, "/changes/0/change", "4940.00"), e("t10", 1, "/changes/0/new/0/this_month", "3000.00"))
	ok("Patient fees rose 4,940.00. Of that, new patients brought 3,000.00 and the rest is unexplained.",
		e("t10", 0, "/changes/0/change", "4940.00"), e("t10", 1, "/changes/0/new/0/this_month", "3000.00"))

	// Round thirty-three: the side stated before the figure.
	rejected("Supplies are over budget by 630.00.", "but the variance is -630", e("t47", 0, "/lines/0/month/variance", "-630.00"))
	ok("Supplies are under budget by 630.00.", e("t47", 0, "/lines/0/month/variance", "-630.00"))
	rejected("Supplies are over budget by 6.3%.", "but the variance is -6.3", e("t29", 0, "/lines/0/month/percent", "-6.3"))

	// Round thirty-four: a whole object binds no figure; the cash plan's name is not cash.
	reads["t62"] = read(`{"data":{"receivables":"8000.00"}}`, "Working capital", "working_capital", "Bluegrass Dental PLLC", "2026-10")
	rejected("Net income is 8,000.00.", "bound to no read", e("t62", 0, "/data", `{"receivables":"8000.00"}`))
	ok("The cash plan has 2 unapproved schedules.", e("t19", 0, "/unapproved_schedules", "2"))

	// Round thirty-five: an unstated driver explains nothing; two receipts lines are two facts.
	reads["t63"] = read(`{"state":"ready","changes":[{"account":"Patient fees","change":"4940.00","new":[{"name":"New patients","this_month":"3000.00","last_month":"0.00"}],"unattributed":{"this_month":"1940.00","last_month":"0.00"}}]}`, "Changes from last month", "flux", "Bluegrass Dental PLLC", "2026-10")
	rejected("Patient fees rose 4,940.00, driven by new patients at 3,000.00.", "account for 3000.00 of 4940.00",
		e("t63", 0, "/changes/0/change", "4940.00"), e("t63", 0, "/changes/0/new/0/this_month", "3000.00"), e("t63", 0, "/changes/0/unattributed/last_month", "0.00"))
	reads["t64"] = read(`{"entity":"Bluegrass Dental PLLC","period":"2026-10","currency":"USD","cash_flow":{"sections":[{"name":"Operating","lines":[{"name":"Customer receipts","amount":"9000.00"},{"name":"Other receipts","amount":"1000.00"}]}]}}`, "Management pack", "management_pack", "Bluegrass Dental PLLC", "2026-10")
	ok("Customer receipts and other receipts total 10,000.00.", derived(0, "sum", "10000.00", "t64", "/cash_flow/sections/0/lines/0/amount", "t64", "/cash_flow/sections/0/lines/1/amount"))

	// Round thirty-six: a total cited but not printed is still the total.
	rejected("Patient fees rose, driven entirely by new patients.", "account for 3000.00 of 4940.00",
		e("t63", 0, "/changes/0/change", "4940.00"), e("t63", 0, "/changes/0/new/0/name", "New patients"))

	// Round thirty-seven: a forecast week's month.
	rejected("Cash is forecast to close at 23,400.00 in January.", "for the week of 2026-12-20", e("t30", 0, "/weeks/0/closing", "23400.00"))
	ok("Cash is forecast to close at 23,400.00 in December.", e("t30", 0, "/weeks/0/closing", "23400.00"))

	// Amounts and changes are not added together.
	rejected("Net income and its change total 23,620.00.", "mixes changes and amounts", derived(0, "sum", "23620.00", "t2", ni+"/amount", "t2", ni+"/change"))
}
