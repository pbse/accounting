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
