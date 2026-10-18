package preparation

import (
	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type agentRoundTrip func(*http.Request) (*http.Response, error)

func (f agentRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestInterpretationContractAndCitationBoundary(t *testing.T) {
	sources := []IntakeSource{{Source: Source{ID: "1", Name: "insurance.txt", Digest: "invoice-hash", Role: "evidence", Location: "Full extracted text"}, Content: "Insurance coverage costs USD 1,200.00.\nCoverage starts September 1, 2026."}, {Source: Source{ID: "2", Name: "booking.txt", Digest: "booking-hash", Role: "evidence", Location: "Full extracted text"}, Content: "Insurance expense account 6100."}}
	output := `{"kind":"prepaid","summary":"Prepare the insurance coverage schedule.","questions":["When does coverage end?"],"sources":[{"id":"1","role":"invoice"},{"id":"2","role":"support"}],"facts":[{"field":"amount","value":"1200.00","source_id":"1","quote":"Insurance coverage costs USD 1,200.00."},{"field":"service_start","value":"2026-09-01","source_id":"1","quote":"Coverage starts September 1, 2026."}]}`
	client := &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.openai.com/v1/responses" {
			t.Fatal("Unexpected provider endpoint")
		}
		raw, _ := io.ReadAll(r.Body)
		var request map[string]any
		json.Unmarshal(raw, &request)
		if request["store"] != false || request["max_output_tokens"] != float64(6000) || request["tools"] != nil || strings.Contains(string(raw), "fixture-key") {
			t.Fatal("Unsafe provider contract")
		}
		field := request["text"].(map[string]any)["format"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)["facts"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["field"].(map[string]any)
		names, _ := field["enum"].([]any)
		allowed := map[string]bool{}
		for _, name := range names {
			allowed[name.(string)] = true
		}
		if !allowed["amount"] || !allowed["service_start"] || !allowed["remaining"] || !allowed["principal"] || allowed["useful_life_months"] || allowed["asset_cost_account"] {
			t.Fatal("the proposal contract does not constrain fact names to supported inputs")
		}
		body, _ := json.Marshal(map[string]any{"id": "response-fixture", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": output}}}}, "usage": map[string]int{"input_tokens": 100, "output_tokens": 50}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})}
	p, err := interpret(agentModelContext(t, client), "Example", "2026-09", "USD", "1", sources, false)
	if err != nil || p.Engine != "openai" || p.Facts[0].Value != "1200.00" || p.Facts[1].Candidates[0].Location != "line 2" || len(p.Sources) != 2 || p.Facts[2].Question == "" || p.InputTokens != 100 {
		t.Fatal("Missing validated proposals or provenance", p, err)
	}
	var result agentResult
	json.Unmarshal([]byte(output), &result)
	result.Facts[0].Quote = "Invented invoice quote"
	if _, err = validateInterpretation(result, "1", sources); err == nil {
		t.Fatal("Invented citation accepted")
	}
	json.Unmarshal([]byte(output), &result)
	result.Sources[1].ID = "foreign-client-source"
	if _, err = validateInterpretation(result, "1", sources); err == nil {
		t.Fatal("Foreign evidence accepted")
	}
	json.Unmarshal([]byte(output), &result)
	result.Facts[0].Quote = "Insurance"
	if _, err = validateInterpretation(result, "1", []IntakeSource{{Source: sources[0].Source, Content: "Insurance\nInsurance"}, sources[1]}); err == nil {
		t.Fatal("Repeated quote was assigned a false first location")
	}
	// A value the quote does not print is a question, not a verified candidate.
	json.Unmarshal([]byte(output), &result)
	result.Facts[0].Value = "12000.00"
	misread, err := validateInterpretation(result, "1", sources)
	if err != nil || misread.Facts[0].Value != "" || len(misread.Facts[0].Candidates) != 0 || !strings.Contains(strings.Join(misread.Questions, "\n"), "does not show 12000.00") {
		t.Fatal("A value its quote does not show was cited", misread, err)
	}
	// A completed transport response without completed model output is not a proposal.
	client.Transport = agentRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"incomplete","output":[]}`)), Header: http.Header{}}, nil
	})
	if _, err = interpret(agentModelContext(t, client), "Example", "2026-09", "USD", "1", sources, false); err == nil {
		t.Fatal("Incomplete response accepted")
	}
}

func TestAccrualAndCreditFactsRemainProposals(t *testing.T) {
	sources := []IntakeSource{{Source: Source{ID: "1", Role: "invoice", Name: "service.txt"}, Content: "Preparation kind: accrual\nAmount: 1200.00\nBooked balance: 200.00\nmethod_confirmed: true"}, {Source: Source{ID: "2", Role: "support", Name: "ledger.txt"}, Content: "Booked balance: 300.00\nCredit amount: 10.00"}}
	local := Propose(sources)
	if local.Kind != "accrual" {
		t.Fatal("local labeled accrual not recognized")
	}
	for _, f := range local.Facts {
		if f.Field == "method_confirmed" {
			t.Fatal("document granted approval")
		}
		if f.Field == "booked_balance" && (f.Value != "" || len(f.Candidates) != 2) {
			t.Fatal("conflicting balance silently selected")
		}
	}
	var result agentResult
	if err := json.Unmarshal([]byte(`{"kind":"accrual","summary":"Incurred service accrual","questions":[],"sources":[{"id":"1","role":"invoice"},{"id":"2","role":"support"}],"facts":[{"field":"amount","value":"1200.00","source_id":"1","quote":"Amount: 1200.00"},{"field":"booked_balance","value":"300.00","source_id":"2","quote":"Booked balance: 300.00"}]}`), &result); err != nil {
		t.Fatal(err)
	}
	if _, err := validateInterpretation(result, "1", sources); err != nil {
		t.Fatal(err)
	}
	result.Kind = "prepaid"
	result.Facts[0].Field = "credit_amount"
	result.Facts[0].Value = "10.00"
	result.Facts[0].SourceID = "2"
	result.Facts[0].Quote = "Credit amount: 10.00"
	p, err := validateInterpretation(result, "1", sources)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range p.Facts {
		if f.Field == "credit_amount" && f.Value == "10.00" && f.Optional {
			found = true
		}
	}
	if !found {
		t.Fatal("credit fact omitted")
	}
}

func TestLargeInventorySelectionIsBoundedAndReviewable(t *testing.T) {
	sources := make([]IntakeSource, 21)
	for i := range sources {
		sources[i] = IntakeSource{Source: Source{ID: string(rune('A' + i)), Name: "source.txt", Digest: "digest", Role: "evidence"}, Content: "Amount: 100.00"}
	}
	sources[1].Content = "Paid by card 4111-1111-1111-1111. Account number: 000123456789."
	client := &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "primary_source_id") || !strings.Contains(string(body), "content bytes") {
			t.Fatal("selection lacked inventory or size bound")
		}
		if strings.Contains(string(body), "4111-1111-1111-1111") || strings.Contains(string(body), "000123456789") || !strings.Contains(string(body), "card number ending 1111 removed") {
			t.Fatal("selection excerpts reach the model with card and bank numbers removed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"selected_ids\":[\"A\",\"B\"]}"}]}]}`)), Header: http.Header{}}, nil
	})}
	selected, err := selectEvidence(agentModelContext(t, client), "A", sources)
	if err != nil || len(selected) != 2 || selected[0].ID != "A" || selected[1].ID != "B" {
		t.Fatalf("selection: %d %v", len(selected), err)
	}
	if _, err = selectEvidence(agentModelContext(t, client), "C", sources); err == nil {
		t.Fatal("model omitted primary without a blocker")
	}
}

func TestSelectionCheckpointOnlyResumesSameBoundedInventory(t *testing.T) {
	sources := []IntakeSource{{Source: Source{ID: "1"}, Content: "primary"}, {Source: Source{ID: "2"}, Content: "support"}}
	checkpoint := []byte(`{"inventory_digest":"snapshot-a","selected_ids":["1","2"]}`)
	if selected, ok := resumeSelection(checkpoint, "snapshot-a", "1", sources); !ok || len(selected) != 2 {
		t.Fatal("valid checkpoint was lost")
	}
	if _, ok := resumeSelection(checkpoint, "snapshot-b", "1", sources); ok {
		t.Fatal("stale checkpoint reused")
	}
	if _, ok := resumeSelection([]byte(`{"inventory_digest":"snapshot-a","selected_ids":["1","1"]}`), "snapshot-a", "1", sources); ok {
		t.Fatal("duplicate selection reused")
	}
	if _, ok := resumeSelection([]byte(`{"inventory_digest":"snapshot-a","selected_ids":["2"]}`), "snapshot-a", "1", sources); ok {
		t.Fatal("checkpoint omitted primary")
	}
}

func TestPrimaryClientLabelBlocksBeforeEvidenceSelection(t *testing.T) {
	sources := []IntakeSource{{Source: Source{ID: "1", Name: "invoice.txt"}, Content: "Client entity: Other LLC\nAmount: 100"}, {Source: Source{ID: "2", Name: "support.txt"}, Content: "Client entity: Third LLC"}}
	if p, ok := blockedIdentity("1", "Acme LLC", nil, sources); !ok || p.Kind != "unsupported" || p.Engine != "identity_blocked" {
		t.Fatal("misfiled primary source was not blocked", p)
	}
	sources[0].Content = "Amount: 100"
	if _, ok := blockedIdentity("1", "Acme LLC", nil, sources); ok {
		t.Fatal("unrelated support source blocked this job")
	}
}

func TestRecognizeExportLeavesTheReconciliationRoleToTheReviewer(t *testing.T) {
	intake := func(content string) []IntakeSource {
		return []IntakeSource{{Source: Source{ID: "1", Role: "invoice"}, Content: content}}
	}
	p, ok := RecognizeExport(intake("id,date,amount\nB1,2026-09-05,-50.00\nB2,2026-09-02,200.00\n"), "2026-09", true)
	if !ok || p.Kind != "reconciliation" || p.Engine != "recognized_export" || !strings.Contains(p.Summary, "2 transactions dated 2026-09-02 to 2026-09-05, net 150.00") || !strings.Contains(p.Summary, "bank statement or the ledger export") {
		t.Fatalf("an id,date,amount CSV must be proposed as a reconciliation with an unconfirmed role: %+v", p)
	}
	if p, ok = RecognizeExport(intake("id,date,amount\nB1,2026-10-01,5.00\n"), "2026-09", true); !ok || p.Kind != "unsupported" || len(p.Questions) != 1 {
		t.Fatalf("a malformed reconciliation CSV must stay visibly unsupported: %+v", p)
	}
	if p, ok = RecognizeExport(intake("id,date,amount\n"), "2026-09", true); !ok || p.Kind != "reconciliation" || !strings.Contains(p.Summary, "no transactions") {
		t.Fatalf("a header-only CSV is zero movement, not an error: %+v", p)
	}
	if _, ok = RecognizeExport(intake("id,date,amount\nB1,2026-09-05,-50.00\n"), "2026-09", false); ok {
		t.Fatal("the agent path reads reconciliation CSVs itself")
	}
	if _, ok = RecognizeExport(intake("Invoice amount: 1200.00\nService start: 2026-09-01"), "2026-09", true); ok {
		t.Fatal("an invoice is not a recognized export")
	}
}

func TestFormPreparedKindsKeepTheirFormsSources(t *testing.T) {
	sources := []IntakeSource{
		{Source: Source{ID: "1", Name: "statement.txt"}, Content: "Closing principal 118,000.00"},
		{Source: Source{ID: "2", Name: "note.txt"}, Content: "Interest at 6.000% per annum, Actual/360."},
		{Source: Source{ID: "3", Name: "ledger.txt"}, Content: "Loan payable 100,000.00"},
	}
	proposal := func(kind string, roles map[string]string, fields ...string) agentResult {
		var r agentResult
		r.Kind, r.Summary = kind, "Fixture"
		for _, id := range []string{"1", "2", "3"} {
			if role := roles[id]; role != "" {
				r.Sources = append(r.Sources, struct {
					ID   string `json:"id"`
					Role string `json:"role"`
				}{id, role})
			}
		}
		for _, field := range fields {
			r.Facts = append(r.Facts, agentFact{Field: field, Value: "118000.00", SourceID: "1", Quote: "Closing principal 118,000.00"})
		}
		return r
	}
	loan := map[string]string{"1": "statement", "2": "support", "3": "ledger"}
	p, err := validateInterpretation(proposal("loan", loan, "closing_principal"), "1", sources)
	if err != nil || len(p.Questions) != 0 || p.Facts[5].Field != "closing_principal" || p.Facts[5].Value != "118000.00" {
		t.Fatalf("a loan proposal from the statement with its note and ledger: %+v %v", p, err)
	}
	if p, err = validateInterpretation(proposal("loan", map[string]string{"1": "statement", "2": "support"}), "1", sources); err != nil || len(p.Questions) != 1 || !strings.Contains(p.Questions[0], "ledger page") {
		t.Fatalf("a loan without its ledger page asks for it: %+v %v", p, err)
	}
	for name, bad := range map[string]agentResult{
		"a loan from the note":             proposal("loan", map[string]string{"1": "support", "2": "statement", "3": "ledger"}),
		"a loan with an invoice":           proposal("loan", map[string]string{"1": "statement", "2": "invoice", "3": "ledger"}),
		"an owner item from a statement":   proposal("owner", map[string]string{"1": "statement", "3": "ledger"}),
		"an owner item with a statement":   proposal("owner", map[string]string{"1": "support", "2": "statement", "3": "ledger"}),
		"depreciation from a ledger":       proposal("depreciation", map[string]string{"1": "invoice", "3": "ledger"}),
		"a field the form does not have":   proposal("depreciation", map[string]string{"1": "invoice"}, "useful_life"),
		"a loan field on deferred revenue": proposal("deferred_revenue", map[string]string{"1": "invoice"}, "closing_principal"),
	} {
		if _, err = validateInterpretation(bad, "1", sources); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if p, err = validateInterpretation(proposal("owner", map[string]string{"1": "support"}, "amount"), "1", sources); err != nil || len(p.Questions) != 1 || p.Facts[3].Value != "118000.00" {
		t.Fatalf("an owner item without its ledger page asks for it: %+v %v", p, err)
	}
	for _, c := range []struct {
		name      string
		p         agentResult
		questions int
	}{
		{"depreciation without the booked accumulated depreciation's support", proposal("depreciation", map[string]string{"1": "invoice"}), 1},
		{"depreciation with it", proposal("depreciation", map[string]string{"1": "invoice", "2": "support"}), 0},
		{"deferred revenue booked to revenue", proposal("deferred_revenue", map[string]string{"1": "invoice"}, "amount"), 0},
		{"deferred revenue with a booked balance but no support", proposal("deferred_revenue", map[string]string{"1": "invoice"}, "booked_balance"), 1},
	} {
		if p, err = validateInterpretation(c.p, "1", sources); err != nil || len(p.Questions) != c.questions {
			t.Errorf("%s: %v %v", c.name, p.Questions, err)
		}
	}
}

func agentModelContext(t *testing.T, httpClient *http.Client) context.Context {
	t.Helper()
	client, err := llm.New(llm.Config{HTTP: httpClient, APIKey: "fixture-key", Model: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	return llm.WithClient(context.Background(), client)
}

func TestPriorScheduleIsReadRowByRowOnTheOpeningMonth(t *testing.T) {
	content := "Prior schedule of prepaid expenses at 2026-08-31 (USD)\nDescription,Amount,Start,End,Expense account,Prepaid account,Remaining\nInsurance,1200.00,2026-01-01,2026-12-31,Insurance expense,Prepaid insurance,400.00\nSoftware,2400.00,2026-03-01,2027-02-28,Software expense,Prepaid software,1400.00\nTotal,,,,,,1800.00"
	sources := []IntakeSource{{Source: Source{ID: "1", Name: "schedule.csv", Digest: "schedule-hash", Role: "evidence", Location: "Full extracted text"}, Content: content}}
	row := func(n int, label, amount, start, end, expense, prepaid, remaining string) []agentFact {
		return []agentFact{{"label", label, "1", label + "," + amount, n}, {"amount", amount, "1", label + "," + amount, n}, {"service_start", start, "1", amount + "," + start, n}, {"service_end", end, "1", start + "," + end, n},
			{"expense_account", expense, "1", end + "," + expense, n}, {"prepaid_account", prepaid, "1", expense + "," + prepaid, n}, {"remaining", remaining, "1", prepaid + "," + remaining, n}, {"currency", "USD", "1", "(USD)", n}}
	}
	output := func() agentResult {
		var r agentResult
		json.Unmarshal([]byte(`{"kind":"prepaid","summary":"Prior prepaid schedule","questions":[],"sources":[{"id":"1","role":"schedule"}],"facts":[],"lines":[{"line":1,"row":"Insurance,1200.00,2026-01-01,2026-12-31,Insurance expense,Prepaid insurance,400.00"},{"line":2,"row":"Software,2400.00,2026-03-01,2027-02-28,Software expense,Prepaid software,1400.00"}]}`), &r)
		r.Facts = append(row(1, "Insurance", "1200.00", "2026-01-01", "2026-12-31", "Insurance expense", "Prepaid insurance", "400.00"), row(2, "Software", "2400.00", "2026-03-01", "2027-02-28", "Software expense", "Prepaid software", "1400.00")...)
		return r
	}
	p, err := validateSchedule(output(), "1", sources)
	if err != nil || len(p.Lines) != 2 || len(p.Questions) != 0 || p.Lines[1].Facts[7].Value != "1400.00" || p.Lines[0].Facts[0].Value != "Insurance" || p.Lines[0].Facts[1].Candidates[0].Location == "" {
		t.Fatal("each row is a line with its cited facts", p, err)
	}
	for _, f := range p.Lines[0].Facts {
		if f.Question != "" {
			t.Fatal("a fully cited row has no questions", f)
		}
	}
	// A value cited from another row is refused, so rows cannot borrow each other's figures.
	swapped := output()
	swapped.Facts[1] = agentFact{"amount", "2400.00", "1", "Software,2400.00", 1}
	if _, err = validateSchedule(swapped, "1", sources); err == nil {
		t.Fatal("a fact cited outside its row was accepted")
	}
	// A label its quote does not show is a question for that row, not a value.
	borrowed := output()
	borrowed.Facts[8] = agentFact{"label", "Insurance", "1", "Software,2400.00", 2}
	if p, err = validateSchedule(borrowed, "1", sources); err != nil || p.Lines[1].Facts[0].Value != "" || !strings.Contains(p.Lines[1].Facts[0].Question, "does not show Insurance") {
		t.Fatal("a label not in its quote is a question", p, err)
	}
	// A row must be one whole line of the document, so it cannot take in another row's figures.
	longer := []IntakeSource{{Source: sources[0].Source, Content: strings.Replace(content, "Prepaid insurance,400.00", "Prepaid insurance,400.00 (renewed in January)", 1)}}
	if _, err = validateSchedule(output(), "1", longer); err == nil {
		t.Fatal("a row that is only part of a line was accepted")
	}
	// A CSV cell spanning lines can hold a line that looks like a row, so such a schedule is not read.
	celled := []IntakeSource{{Source: sources[0].Source, Content: content + "\nMemo,\"Renewed\nInsurance,1200.00 note\""}}
	if p, err = validateSchedule(output(), "1", celled); err != nil || p.Kind != "unsupported" || len(p.Lines) != 0 || !strings.Contains(strings.Join(p.Questions, " "), "spans several lines") {
		t.Fatal("a CSV cell spanning lines is a question", p, err)
	}
	// Two rows printed with one label cannot be told apart later, so they are a question for the document.
	twice := []IntakeSource{{Source: sources[0].Source, Content: strings.Replace(content, "Software,2400.00", "Insurance,2400.00", 1)}}
	same := output()
	same.Lines[1].Row = strings.Replace(same.Lines[1].Row, "Software,2400.00", "Insurance,2400.00", 1)
	for i := 8; i < 16; i++ {
		same.Facts[i].Quote = strings.Replace(same.Facts[i].Quote, "Software,2400.00", "Insurance,2400.00", 1)
	}
	same.Facts[8].Value = "Insurance"
	if p, err = validateSchedule(same, "1", twice); err != nil || !strings.Contains(strings.Join(p.Questions, "\n"), "share the label") {
		t.Fatal("rows sharing a label are a question", p, err)
	}

	// Only the opening month reads a schedule: elsewhere it is unsupported, with a question saying where to upload it.
	raw, _ := json.Marshal(output())
	var sent map[string]any
	var request string
	client := &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		request = string(body)
		json.Unmarshal(body, &sent)
		reply, _ := json.Marshal(map[string]any{"id": "response-fixture", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(raw)}}}}, "usage": map[string]int{"input_tokens": 10, "output_tokens": 10}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(reply))), Header: http.Header{}}, nil
	})}
	if p, err = interpret(agentModelContext(t, client), "Example", "2026-08", "USD", "1", sources, true); err != nil || len(p.Lines) != 2 || sent["max_output_tokens"] != float64(16000) || !strings.Contains(request, "the client's opening month, its first in the firm") {
		t.Fatal("the opening month reads the schedule", p, err)
	}
	if p, err = interpret(agentModelContext(t, client), "Example", "2026-09", "USD", "1", sources, false); err != nil || p.Kind != "unsupported" || len(p.Lines) != 0 || !strings.Contains(strings.Join(p.Questions, " "), "opening month") || strings.Contains(request, "the client's opening month, its first in the firm") {
		t.Fatal("another month does not read a schedule", p, err)
	}
}

func TestAnImportedFixedAssetTiesToTheRegistersNetBookValue(t *testing.T) {
	asset := func(nbv string) Plan {
		input, _ := json.Marshal(prepaids.Input{Basis: "accrual", Entity: "Example", ClosePeriod: "2026-08", SourceReference: "Prior schedule #1 · Van", Amount: "3600.00", ServiceStart: "2026-01-01", ServiceEnd: "2028-12-31",
			Currency: "USD", ExpenseAccount: "Depreciation expense", PrepaidAccount: "Accumulated depreciation", Method: prepaids.Method, Schedule: prepaids.Depreciation, SalvageValue: "360.00"})
		return Plan{Kind: "prepaid", Input: input, Imported: &Imported{RootSourceID: "1", Label: "Van", Remaining: nbv}}
	}
	// Cost 3,600, salvage 360, 36 months: 90 a month, 720 by August, so 2,520 left to depreciate and a net book value of 2,880.
	raw, err := Calculate(asset("2880.00"), "Example", "2026-08")
	var paper struct {
		Balance  string          `json:"prepaid_balance"`
		Proposal json.RawMessage `json:"proposal"`
	}
	json.Unmarshal(raw, &paper)
	if err != nil || paper.Balance != "2520.00" || (len(paper.Proposal) > 0 && string(paper.Proposal) != "null") {
		t.Fatalf("an asset whose register states its net book value is a schedule-only draft: %s %v", raw, err)
	}
	if _, err = Calculate(asset("2520.00"), "Example", "2026-08"); err == nil || !strings.Contains(err.Error(), "gives 2880.00") {
		t.Fatalf("a register figure that is not the net book value is refused: %v", err)
	}

	// A register's rows may state a salvage value; a prepaid schedule's may not.
	content := "Fixed asset register at 2026-08-31 (USD)\nVan,3600.00,2026-01-01,2028-12-31,Depreciation expense,Accumulated depreciation,2880.00,360.00"
	sources := []IntakeSource{{Source: Source{ID: "1", Name: "register.csv", Digest: "h", Role: "evidence"}, Content: content}}
	row := "Van,3600.00,2026-01-01,2028-12-31,Depreciation expense,Accumulated depreciation,2880.00,360.00"
	register := agentResult{Kind: "depreciation", Summary: "Register", Questions: []string{}, Sources: []struct {
		ID   string `json:"id"`
		Role string `json:"role"`
	}{{"1", "schedule"}}, Lines: []struct {
		Line int    `json:"line"`
		Row  string `json:"row"`
	}{{1, row}}, Facts: []agentFact{{"label", "Van", "1", "Van,3600.00", 1}, {"amount", "3600.00", "1", "Van,3600.00", 1}, {"service_start", "2026-01-01", "1", "3600.00,2026-01-01", 1},
		{"service_end", "2028-12-31", "1", "2026-01-01,2028-12-31", 1}, {"expense_account", "Depreciation expense", "1", "2028-12-31,Depreciation expense", 1}, {"prepaid_account", "Accumulated depreciation", "1", "expense,Accumulated depreciation", 1},
		{"remaining", "2880.00", "1", "depreciation,2880.00", 1}, {"currency", "USD", "1", "(USD)", 1}}}
	p, err := validateSchedule(register, "1", sources)
	if err != nil || len(p.Lines) != 1 || len(p.Lines[0].Facts) != 9 || p.Lines[0].Facts[8].Field != "salvage_value" || p.Lines[0].Facts[8].Question != "" {
		t.Fatal("a register row reads with an optional salvage value", p, err)
	}
	register.Facts = append(register.Facts, agentFact{"salvage_value", "360.00", "1", "2880.00,360.00", 1})
	if p, err = validateSchedule(register, "1", sources); err != nil || p.Lines[0].Facts[8].Value != "360.00" {
		t.Fatal("a cited salvage value is read", p, err)
	}
	register.Kind = "prepaid"
	if _, err = validateSchedule(register, "1", sources); err == nil {
		t.Fatal("a prepaid schedule row citing a salvage value was accepted")
	}
}
