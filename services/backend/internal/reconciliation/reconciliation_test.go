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

func TestExactGroupedCandidatesRemainUnmatched(t *testing.T) {
	input := Input{Entity: "Example", Account: "Bank", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "Statement", LedgerReference: "Ledger", ExpectedPages: 1, Pages: []int{1}, StatementOpening: "0", StatementClosing: "150", LedgerOpening: "0", LedgerClosing: "150", PolicyConfirmed: true,
		StatementCSV: "id,date,amount\nS1,2026-09-02,100\nS2,2026-09-02,20\nS3,2026-09-02,30",
		LedgerCSV:    "id,date,amount\nL1,2026-09-02,40\nL2,2026-09-02,60\nL3,2026-09-02,50"}
	result, err := Prepare(input)
	if err != nil || len(result.Matches) != 0 || len(result.UnmatchedStatement) != 3 || len(result.UnmatchedLedger) != 3 || len(result.Blockers) == 0 {
		t.Fatalf("group candidates became confirmed matches: %+v %v", result, err)
	}
	want := map[string][]string{"statement/S1": {"L1", "L2"}, "ledger/L3": {"S2", "S3"}}
	for _, exception := range result.Exceptions {
		key := exception.Side + "/" + exception.ID
		if ids, ok := want[key]; ok {
			if exception.Kind != "grouped_exact" || len(exception.CandidateGroups) != 1 || len(exception.CandidateGroups[0]) != 2 || exception.CandidateGroups[0][0] != ids[0] || exception.CandidateGroups[0][1] != ids[1] {
				t.Fatalf("missing source-linked group for %s: %+v", key, exception)
			}
			delete(want, key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing grouped candidates: %v", want)
	}
	input.StatementClosing = "10"
	input.LedgerClosing = "10"
	input.StatementCSV = "id,date,amount\nS1,2026-09-02,10"
	input.LedgerCSV = "id,date,amount\nL1,2026-09-02,20\nL2,2026-09-02,-10"
	result, err = Prepare(input)
	if err != nil || len(result.Exceptions) == 0 || result.Exceptions[0].GroupCount != 0 {
		t.Fatalf("mixed-sign offset was suggested as a grouped match: %+v %v", result, err)
	}
}

func TestReviewedOutstandingBridgeAndTransferEvidence(t *testing.T) {
	base := Input{Entity: "Example", Account: "Operating", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "Statement", LedgerReference: "Ledger", ExpectedPages: 1, Pages: []int{1}, StatementOpening: "1000", LedgerOpening: "900", StatementClosing: "900", LedgerClosing: "900", StatementCSV: "id,date,amount\nS1,2026-09-03,-100", LedgerCSV: "id,date,amount", PolicyConfirmed: true}
	base.PriorOutstanding = []PriorItem{{Side: "ledger", ID: "aug-check", Amount: "-100", SourceReference: "August workpaper row 7", PolicyReference: "Approved timing rule 1", ClearedBy: "S1", Reason: "Bank cleared the August check"}}
	got, err := Prepare(base)
	if err != nil || len(got.Blockers) != 0 || got.OpeningBridgeDifference != "0.00" || got.ClosingBridgeDifference != "0.00" || len(got.UnmatchedStatement) != 0 {
		t.Fatalf("prior clearing did not tie reviewed bridge: %+v %v", got, err)
	}
	bad := base
	bad.PriorOutstanding = append([]PriorItem{}, base.PriorOutstanding...)
	bad.PriorOutstanding[0].Amount = "-90"
	if _, err := Prepare(bad); err == nil {
		t.Fatal("accepted a clearing row with a different signed amount")
	}
	current := base
	current.LedgerOpening = "1000"
	current.StatementClosing = "1000"
	current.LedgerClosing = "950"
	current.StatementCSV = "id,date,amount"
	current.LedgerCSV = "id,date,amount\nL1,2026-09-28,-50"
	current.PriorOutstanding = nil
	current.CurrentOutstanding = []CurrentItem{{Side: "ledger", ID: "L1", PolicyReference: "Approved timing rule 2", Reason: "Check has not cleared bank"}}
	got, err = Prepare(current)
	if err != nil || len(got.Blockers) != 0 || got.ClosingBridgeDifference != "0.00" || len(got.UnmatchedLedger) != 0 {
		t.Fatalf("current outstanding did not tie reviewed bridge: %+v %v", got, err)
	}
	transfer := base
	transfer.LedgerOpening = "1000"
	transfer.StatementClosing = "800"
	transfer.LedgerClosing = "800"
	transfer.StatementCSV = "id,date,amount\nS1,2026-09-12,-200"
	transfer.LedgerCSV = "id,date,amount\nL1,2026-09-12,-200"
	transfer.PriorOutstanding = nil
	transfer.OtherAccount = "Savings"
	transfer.OtherStatementRef = "Savings statement"
	transfer.OtherLedgerRef = "Savings ledger"
	transfer.OtherStatementCSV = "id,date,amount\nOS1,2026-09-12,200"
	transfer.OtherLedgerCSV = "id,date,amount\nOL1,2026-09-12,200"
	transfer.Transfers = []TransferPair{{StatementID: "S1", LedgerID: "L1", OtherStatementID: "OS1", OtherLedgerID: "OL1", PolicyReference: "Approved transfer rule", Reason: "Both account records identify transfer"}}
	got, err = Prepare(transfer)
	if err != nil || len(got.Blockers) != 0 || len(got.Matches) != 0 {
		t.Fatalf("transfer pair did not preserve distinct reviewed method: %+v %v", got, err)
	}
	transfer.OtherLedgerCSV = "id,date,amount\nOL1,2026-09-12,201"
	if _, err := Prepare(transfer); err == nil {
		t.Fatal("accepted transfer without offsetting counterpart records")
	}
}
