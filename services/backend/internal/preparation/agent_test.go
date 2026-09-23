package preparation

import (
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
	sources := []IntakeSource{{Source: Source{ID: "1", Name: "insurance.pdf", Digest: "invoice-hash", Role: "evidence", Location: "Full extracted text"}, Content: "Insurance coverage costs USD 1,200.00.\nCoverage starts September 1, 2026."}, {Source: Source{ID: "2", Name: "booking.txt", Digest: "booking-hash", Role: "evidence", Location: "Full extracted text"}, Content: "Insurance expense account 6100."}}
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
	p, err := interpret(context.Background(), client, "fixture-key", "fixture-model", "Example", "2026-09", "1", sources)
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
	// A completed transport response without completed model output is not a proposal.
	client.Transport = agentRoundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"incomplete","output":[]}`)), Header: http.Header{}}, nil
	})
	if _, err = interpret(context.Background(), client, "fixture-key", "fixture-model", "Example", "2026-09", "1", sources); err == nil {
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
	client := &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "primary_source_id") || !strings.Contains(string(body), "content bytes") {
			t.Fatal("selection lacked inventory or size bound")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"{\"selected_ids\":[\"A\",\"B\"]}"}]}]}`)), Header: http.Header{}}, nil
	})}
	selected, err := selectEvidence(context.Background(), client, "fixture-key", "fixture-model", "A", sources)
	if err != nil || len(selected) != 2 || selected[0].ID != "A" || selected[1].ID != "B" {
		t.Fatalf("selection: %d %v", len(selected), err)
	}
	if _, err = selectEvidence(context.Background(), client, "fixture-key", "fixture-model", "C", sources); err == nil {
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
