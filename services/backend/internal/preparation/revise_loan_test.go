package preparation

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"close.local/backend/internal/loans"
)

func TestReturnedLoanUsesQuotedScalarFactsWithoutChangingAuthority(t *testing.T) {
	field, value, quote := "closing_principal", "9900.00", "Closing principal: USD 9,900.00."
	client := &http.Client{Transport: agentRoundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": `{"changes":[{"field":"` + field + `","value":"` + value + `","source_id":"1","quote":"` + quote + `"}],"question":""}`}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
	})}
	ctx := agentModelContext(t, client)
	in := loans.Input{Entity: "Example", ClosePeriod: "2026-09", Lender: "First Bank", SourceReference: "September lender statement", AgreementReference: "Note L777", Currency: "USD", AnnualRate: "12", DayCount: "actual_360", OpeningPrincipal: "10000.00", InterestPaidThrough: "2026-08-31", ClosingPrincipal: "9950.00", Payments: []loans.Payment{{Date: "2026-09-30", Principal: "100.00", Interest: "100.00"}}, BookBalance: "9900.00", LoanAccount: "Term Loan", InterestAccount: "Interest Expense", Method: loans.Method, MethodConfirmed: true, RateConfirmed: true, BookingConfirmed: true}
	raw, _ := json.Marshal(in)
	source := IntakeSource{Source: Source{ID: "1", Name: "lender.txt", Digest: "statement", Role: "statement", Location: "line 1"}, Content: "Closing principal: USD 9,900.00."}
	revision, err := Revise(ctx, "loan", "Use the statement's closing principal.", nil, raw, []IntakeSource{source}, nil)
	if err != nil || revision.Question != "" || len(revision.Changes) != 1 {
		t.Fatalf("returned loan did not become a sourced revision: %+v %v", revision, err)
	}
	var changed loans.Input
	if err = json.Unmarshal(revision.Input, &changed); err != nil {
		t.Fatal(err)
	}
	expected := in
	expected.ClosingPrincipal = "9900.00"
	if !reflect.DeepEqual(changed, expected) {
		t.Fatalf("revision changed fields or authority beyond the quoted fact: %+v", changed)
	}
	body, err := Calculate(Plan{Kind: "loan", Input: revision.Input, Sources: []Source{source.Source}, Revises: "42"}, "Example", "2026-09")
	if err != nil {
		t.Fatal(err)
	}
	var paper loans.Workpaper
	if err = json.Unmarshal(body, &paper); err != nil || paper.BookDifference != "0.00" {
		t.Fatalf("loan revision did not recalculate: %s %v", body, err)
	}
	// A changed rate cannot inherit the prior rate confirmation, even when the notice supplies it.
	field, value, quote = "annual_rate", "13", "Annual rate: 13%."
	source.Content += "\nAnnual rate: 13%."
	revision, err = Revise(ctx, "loan", "Use the notice annual rate.", nil, raw, []IntakeSource{source}, nil)
	if err != nil || revision.Question == "" || len(revision.Input) > 0 {
		t.Fatalf("authority change did not remain a question: %+v %v", revision, err)
	}
}
