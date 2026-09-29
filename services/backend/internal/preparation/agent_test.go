package preparation

import (
	"close.local/backend/internal/llm"
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
		body, _ := json.Marshal(map[string]any{"id": "response-fixture", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": output}}}}, "usage": map[string]int{"input_tokens": 100, "output_tokens": 50}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})}
	p, err := interpret(agentModelContext(t, client), "Example", "2026-09", "USD", "1", sources)
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
	if _, err = interpret(agentModelContext(t, client), "Example", "2026-09", "USD", "1", sources); err == nil {
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
			r.Facts = append(r.Facts, struct {
				Field    string `json:"field"`
				Value    string `json:"value"`
				SourceID string `json:"source_id"`
				Quote    string `json:"quote"`
			}{field, "118000.00", "1", "Closing principal 118,000.00"})
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
