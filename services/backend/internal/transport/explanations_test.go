package transport

import (
	"strings"
	"testing"
)

func TestLineExplanationsAreLedgerFactsAndCoverMaterialMovements(t *testing.T) {
	ptr := func(s string) *string { return &s }
	rent := packLine{AccountID: "5", Name: "Rent", Amount: "500.00", Prior: ptr("300.00"), Change: ptr("200.00")}
	wages := packLine{AccountID: "6", Name: "Wages", Amount: "4000.00", Prior: ptr("3000.00"), Change: ptr("1000.00")}
	sales := packLine{AccountID: "4", Name: "Sales", Amount: "800.00", Prior: ptr("800.00"), Change: ptr("0.00")}
	pl := statement{Sections: []packSection{{Name: "Income", Lines: []packLine{sales}}, {Name: "Expense", Lines: []packLine{rent, wages}}}}

	// Drafts state the ledger's payees, never a cause it does not record, and name no one behind payroll.
	drivers := map[string]any{"new": []payeeAmount{{"Acme Properties", "200.00", "0.00"}}, "changed": []payeeAmount{}, "missing": []payeeAmount{}, "reconciled": true, "omitted": 0}
	if e := explainLine(rent, drivers, true, "USD", true); !strings.Contains(e.Text, "By payee in the ledger: Acme Properties") || strings.Contains(e.Text, "not attributed") || !e.Material {
		t.Fatalf("a reconciled movement is explained by its payees: %+v", e)
	}
	drivers["new"] = []payeeAmount{{"Acme Properties", "150.00", "0.00"}}
	if e := explainLine(rent, drivers, true, "USD", false); !strings.Contains(e.Text, "50.00 of the movement is not attributed to these payees") {
		t.Fatalf("the exact amount the payees shown do not account for is said, whatever the report reconciles: %+v", e)
	}
	staff := map[string]any{"new": []payeeAmount{{"Jordan Lee", "1000.00", "0.00"}}, "changed": []payeeAmount{}, "missing": []payeeAmount{}, "reconciled": true, "omitted": 0}
	if e := explainLine(wages, staff, true, "USD", true); strings.Contains(e.Text, "Jordan") || !strings.Contains(e.Text, "Payroll detail is not shown") {
		t.Fatalf("a payroll line names no one: %+v", e)
	}
	if e := explainLine(rent, nil, false, "USD", true); !strings.Contains(e.Text, "The cause is not identified from the books.") {
		t.Fatalf("without the ledger's payees the cause is said to be unknown: %+v", e)
	}

	// Publishing explains every material movement, largest first, each a line that moved, frozen with its figures.
	if _, refusal := reviewedExplanations(pl, "100", []lineExplanation{{AccountID: "5", Text: "Rent rose with the new lease."}}); !strings.Contains(refusal, "starting with Wages") {
		t.Fatalf("an unexplained material movement is refused: %q", refusal)
	}
	if _, refusal := reviewedExplanations(pl, "100", []lineExplanation{{AccountID: "4", Text: "Sales held."}}); refusal == "" {
		t.Fatal("a line that did not move is not explained")
	}
	explained, refusal := reviewedExplanations(pl, "100", []lineExplanation{{AccountID: "6", Text: "Two hires in September; the cause of the rest is not known."}, {AccountID: "5", Text: "Rent rose with the new lease."}})
	if refusal != "" || len(explained) != 2 || explained[0].Name != "Wages" || explained[0].Change != "1000.00" || !explained[0].Material || !explained[1].Material {
		t.Fatalf("explained material movements are kept with their figures: %q %+v", refusal, explained)
	}
	if explained, refusal = reviewedExplanations(pl, "", nil); refusal != "" || len(explained) != 0 {
		t.Fatalf("without a materiality amount nothing must be explained: %q %+v", refusal, explained)
	}
}
