package reconciliation

import "testing"

func TestPreparationPreservesDifferencesAndAmbiguity(t *testing.T) {
	input := Input{Entity: "Example", Account: "Bank", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "Statement", LedgerReference: "Ledger", ExpectedPages: 2, Pages: []int{1, 2}, StatementOpening: "1000", StatementClosing: "1150", LedgerOpening: "1000", LedgerClosing: "1100", StatementCSV: "id,date,amount\nS1,2026-09-02,200\nS2,2026-09-05,-50", LedgerCSV: "id,date,amount\nL1,2026-09-02,200\nL2,2026-09-05,-50\nL3,2026-09-30,-50", PolicyConfirmed: true}
	result, err := Prepare(input)
	if err != nil || len(result.Matches) != 2 || len(result.UnmatchedLedger) != 1 || result.ClosingDifference != "50.00" || result.StatementRollForwardDifference != "0.00" || result.LedgerRollForwardDifference != "0.00" {
		t.Fatalf("unexpected roll-forward: %+v %v", result, err)
	}
	input.Pages = []int{1}
	input.StatementClosing = "1151"
	input.StatementCSV += "\nS3,2026-09-05,-50"
	result, err = Prepare(input)
	if err != nil || len(result.Matches) != 1 || len(result.UnmatchedStatement) != 2 || result.StatementRollForwardDifference != "-51.00" || len(result.Blockers) != 4 {
		t.Fatalf("lost incomplete or ambiguous evidence: %+v %v", result, err)
	}
	input.StatementCSV = "id,date,amount\nS1,2026-09-02,0.001"
	if _, err = Prepare(input); err == nil {
		t.Fatal("accepted fractional cents")
	}
}

func TestInvestigationHintsKeepAmbiguousRowsUnmatched(t *testing.T) {
	input := Input{Entity: "Example", Account: "Bank", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "Statement", LedgerReference: "Ledger", ExpectedPages: 1, Pages: []int{1}, StatementOpening: "0", StatementClosing: "0", LedgerOpening: "0", LedgerClosing: "0", PolicyConfirmed: true,
		StatementCSV: "id,date,amount\nS1,2026-09-02,200\nS2,2026-09-05,-50\nS3,2026-09-05,-50\nS4,2026-09-10,75\nS5,2026-09-11,-12\nS6,2026-09-12,99",
		LedgerCSV:    "id,date,amount\nL1,2026-09-02,200\nL2,2026-09-05,-50\nL3,2026-09-15,75\nL4,2026-09-11,-13"}
	result, err := Prepare(input)
	if err != nil || len(result.Matches) != 1 || len(result.UnmatchedStatement) != 5 || len(result.UnmatchedLedger) != 3 || len(result.Exceptions) != 8 {
		t.Fatalf("investigation altered matching: %+v %v", result, err)
	}
	want := map[string]string{"S2": "ambiguous_exact", "S3": "ambiguous_exact", "S4": "same_amount_other_date", "S5": "same_date_other_amount", "S6": "no_counterpart", "L2": "ambiguous_exact", "L3": "same_amount_other_date", "L4": "same_date_other_amount"}
	for _, item := range result.Exceptions {
		if item.Kind != want[item.ID] {
			t.Fatalf("unexpected hint for %s: %+v", item.ID, item)
		}
		delete(want, item.ID)
	}
	if len(want) != 0 {
		t.Fatalf("missing hints: %v", want)
	}
}

func TestInvestigationFlagsIDAndOppositeSignWithoutMatching(t *testing.T) {
	got := investigate("statement", []Line{{"shared", "2026-09-02", "10.00"}, {"reverse", "2026-09-03", "5.00"}}, []Line{{"shared", "2026-09-04", "11.00"}, {"other", "2026-09-05", "-5.00"}})
	if len(got) != 2 || got[0].Kind != "same_id_different_detail" || got[1].Kind != "opposite_sign" || got[0].CandidateIDs[0] != "shared" || got[1].CandidateIDs[0] != "other" {
		t.Fatalf("lost reconciliation investigation hints: %+v", got)
	}
}
