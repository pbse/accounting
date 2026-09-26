package reconciliation

import (
	"strings"
	"testing"
)

const stripeHeaderRow = "\ufeffautomatic_payout_effective_at,automatic_payout_id,available_on,balance_transaction_id,created,currency,description,fee,gross,net,reporting_category,customer_email\n"

func stripeReport(rows ...string) string {
	return stripeHeaderRow + strings.Join(rows, "\n") + "\n"
}

var settlementRows = []string{
	"2026-09-03 00:00:00,po_A,2026-09-03 00:00:00,txn_1,2026-08-31 10:00:00,usd,Invoice 101,3.20,100.00,96.80,charge,a@example.com",
	"2026-09-03 00:00:00,po_A,2026-09-03 00:00:00,txn_2,2026-09-01 09:00:00,usd,Invoice 102,1.75,50.00,48.25,charge,",
	"2026-09-03 00:00:00,po_A,2026-09-03 00:00:00,txn_3,2026-09-01 12:00:00,usd,\"Refund, partial\",0.00,-20.00,-20.00,refund,",
	"2026-09-10 00:00:00,po_B,2026-09-10 00:00:00,txn_4,2026-09-06 08:00:00,usd,Invoice 103,6.10,200.00,193.90,charge,",
	"2026-09-10 00:00:00,po_B,2026-09-10 00:00:00,txn_5,2026-09-07 08:00:00,usd,Chargeback,15.00,-50.00,-65.00,dispute,",
	"2026-09-10 00:00:00,po_B,2026-09-10 00:00:00,txn_6,2026-09-08 08:00:00,usd,Billing fee,0.00,-2.00,-2.00,fee,",
	"2026-09-17 00:00:00,po_C,2026-09-17 00:00:00,txn_7,2026-09-14 08:00:00,usd,Refund,0.00,-80.00,-80.00,refund,",
	"2026-09-17 00:00:00,po_C,2026-09-17 00:00:00,txn_8,2026-09-14 09:00:00,usd,Invoice 104,1.17,30.00,28.83,charge,",
	"2026-09-24 00:00:00,po_E,2026-09-24 00:00:00,txn_9,2026-09-21 08:00:00,usd,Invoice 105,0.00,10.00,10.00,charge,",
	"2026-09-24 00:00:00,po_F,2026-09-24 00:00:00,txn_10,2026-09-21 09:00:00,usd,Invoice 106,0.00,10.00,10.00,charge,",
	"2026-09-28 00:00:00,po_G,2026-09-28 00:00:00,txn_11,2026-09-25 08:00:00,usd,Invoice 107,3.00,80.00,77.00,charge,",
	"2026-09-30 00:00:00,po_D,2026-09-30 00:00:00,txn_12,2026-09-27 08:00:00,usd,Invoice 108,1.46,40.00,38.54,charge,",
	"2026-09-30 00:00:00,po_D,2026-09-30 00:00:00,txn_13,2026-09-27 09:00:00,usd,Adjustment,0.00,-1.00,-1.00,other_adjustment,",
}

const settlementBank = "id,date,amount\nB1,2026-09-03,125.05\nB2,2026-09-11,126.90\nB3,2026-09-17,-51.17\nB4,2026-09-08,125.05\nB5,2026-09-24,10.00\nB6,2026-09-25,10.00\nB7,2026-10-02,37.54\nB8,2026-09-15,-500.00"

func settlementInput(report string) SettlementInput {
	return SettlementInput{Entity: "Acme Services", ClosePeriod: "2026-09", Currency: "USD", BankAccount: "Operating bank", ReportReference: "stripe.csv", BankReference: "bank.csv", ReportCSV: report, BankCSV: settlementBank, MethodConfirmed: true, CoverageConfirmed: true}
}

func TestSettlementTracesSourcePayoutsToBankRows(t *testing.T) {
	unassigned := "2026-09-12 00:00:00,,2026-09-12 00:00:00,txn_14,2026-09-11 08:00:00,usd,Instant payout charge,0.30,10.00,9.70,charge,"
	paper, err := PrepareSettlement(settlementInput(stripeReport(append(append([]string{}, settlementRows...), unassigned)...)))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"po_A": "matched/B1", "po_B": "matched/B2", "po_C": "matched/B3", "po_D": "matched/B7", "po_E": "ambiguous/", "po_F": "ambiguous/", "po_G": "unmatched/"}
	for _, payout := range paper.Payouts {
		got := payout.Status + "/"
		if payout.Deposit != nil {
			got += payout.Deposit.ID
		}
		if want[payout.ID] != got {
			t.Errorf("%s: got %s want %s (candidates %v)", payout.ID, got, want[payout.ID], payout.CandidateIDs)
		}
	}
	if len(paper.Payouts) != 7 || paper.Payouts[0].ID != "po_A" || paper.Payouts[6].ID != "po_D" {
		t.Fatalf("payouts are not the source batches in expected-date order: %+v", paper.Payouts)
	}
	a := paper.Payouts[0]
	if a.Count != 3 || a.Gross != "130.00" || a.Fee != "4.95" || a.Net != "125.05" || len(a.Categories) != 2 || a.Categories[1].Category != "refund" || a.Categories[1].Net != "-20.00" {
		t.Fatalf("po_A totals: %+v", a)
	}
	if paper.Total.Count != 13 || paper.Total.Gross != "367.00" || paper.Total.Fee != "31.68" || paper.Total.Net != "335.32" {
		t.Fatalf("month totals exclude unassigned rows and tie: %+v", paper.Total)
	}
	if len(paper.Unassigned) != 1 || paper.Unassigned[0] != "txn_14" || len(paper.Blockers) != 4 || !strings.Contains(paper.Blockers[3], "txn_14") {
		t.Fatalf("unassigned row must block and never join a payout: %v %v", paper.Unassigned, paper.Blockers)
	}
	if len(paper.Questions) != 1 || !strings.Contains(paper.Questions[0], "other_adjustment") {
		t.Fatalf("unusual category should be a question: %v", paper.Questions)
	}

	input := settlementInput(stripeReport(settlementRows...))
	input.ReviewedDeposits = []ReviewedDeposit{{PayoutID: "po_E", BankID: "B6", Reason: "Stripe dashboard trace ID matches B6"}, {PayoutID: "po_F", BankID: "B5", Reason: "Stripe dashboard trace ID matches B5"}}
	input.InTransit = []InTransit{{PayoutID: "po_G", PolicyReference: "Firm deposits-in-transit rule", Reason: "October statement line 2 shows $77.00 on Oct 6"}}
	paper, err = PrepareSettlement(input)
	if err != nil || len(paper.Blockers) != 0 || paper.Payouts[3].Status != "reviewed" || paper.Payouts[3].Deposit.ID != "B6" || paper.Payouts[5].Status != "in_transit" {
		t.Fatalf("reviewed decisions should resolve every payout: %v %+v", err, paper)
	}

	for name, change := range map[string]func(*SettlementInput){
		"in transit with candidate": func(in *SettlementInput) {
			in.InTransit = []InTransit{{PayoutID: "po_A", PolicyReference: "Firm deposits-in-transit rule", Reason: "Looks late"}}
		},
		"in transit without rule": func(in *SettlementInput) {
			in.InTransit = []InTransit{{PayoutID: "po_G", Reason: "October statement line 2"}}
		},
		"reviewed wrong amount": func(in *SettlementInput) {
			in.ReviewedDeposits = []ReviewedDeposit{{PayoutID: "po_A", BankID: "B5", Reason: "Chosen by reviewer"}}
		},
		"coverage unconfirmed": func(in *SettlementInput) { in.CoverageConfirmed = false },
		"non-USD row": func(in *SettlementInput) {
			in.ReportCSV = strings.Replace(in.ReportCSV, ",usd,Invoice 103", ",eur,Invoice 103", 1)
		},
		"net not gross less fee": func(in *SettlementInput) {
			in.ReportCSV = strings.Replace(in.ReportCSV, "6.10,200.00,193.90", "6.10,200.00,200.00", 1)
		},
		"duplicate transaction": func(in *SettlementInput) { in.ReportCSV = strings.Replace(in.ReportCSV, "txn_2,", "txn_1,", 1) },
		"payout outside month": func(in *SettlementInput) {
			in.ReportCSV = strings.ReplaceAll(in.ReportCSV, "2026-09-30 00:00:00,po_D", "2026-10-01 00:00:00,po_D")
		},
		"not a payout export": func(in *SettlementInput) { in.ReportCSV = settlementBank },
	} {
		in := settlementInput(stripeReport(settlementRows...))
		change(&in)
		if _, err := PrepareSettlement(in); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestStripeReportSummaryRecognizesOnlyPayoutExports(t *testing.T) {
	summary, ok, err := StripeReportSummary(stripeReport(settlementRows...), "2026-09")
	if !ok || err != nil || !strings.Contains(summary, "7 automatic payouts expected 2026-09-03 to 2026-09-30 from 13 balance transactions") {
		t.Fatalf("summary: %q %v %v", summary, ok, err)
	}
	if _, ok, _ := StripeReportSummary(settlementBank, "2026-09"); ok {
		t.Fatal("a bank CSV is not a Stripe payout export")
	}
	if _, ok, err := StripeReportSummary(stripeReport(settlementRows...), "2026-08"); !ok || err == nil {
		t.Fatal("a recognized export for another month must explain the mismatch")
	}
	payouts := payoutsHeader + "txn_p1,usd,STRIPE PAYOUT,2026-09-02 10:00:00,0.00,-125.05,-125.05,2026-09-03,po_M1,paid,payout\ntxn_p2,usd,Instant payout,2026-09-11 09:00:00,0.00,-126.90,-126.90,2026-09-11,po_I1,paid,payout\n"
	if summary, ok, err := StripeReportSummary(payouts, "2026-09"); !ok || err != nil || !strings.Contains(summary, "Payouts report: 2 payouts expected 2026-09-03 to 2026-09-11") || !strings.Contains(summary, "Payouts report method") {
		t.Fatalf("a Payouts report is recognized in its own currency: %q %v %v", summary, ok, err)
	}
	if _, ok, err := StripeReportSummary(payouts, "2026-08"); !ok || err == nil {
		t.Fatal("a Payouts report for another month must explain the mismatch")
	}
	returns := payoutsHeader + "txn_r0,usd,Payout failure,2026-09-01 08:00:00,0.00,50.00,50.00,2026-08-29,po_A0,failed,payout_reversal\n"
	if summary, ok, err := StripeReportSummary(returns, "2026-09"); !ok || err != nil || !strings.Contains(summary, "1 returns of earlier payouts") {
		t.Fatalf("a month with only returns is described: %q %v", summary, err)
	}
}

func TestStripeFeeEntryCoversFeesOfPayoutsDepositedThisMonth(t *testing.T) {
	resolved := func(entry *FeeEntry) SettlementInput {
		input := settlementInput(stripeReport(settlementRows...))
		input.ReviewedDeposits = []ReviewedDeposit{{PayoutID: "po_E", BankID: "B6", Reason: "Stripe dashboard trace ID matches B6"}, {PayoutID: "po_F", BankID: "B5", Reason: "Stripe dashboard trace ID matches B5"}}
		input.InTransit = []InTransit{{PayoutID: "po_G", PolicyReference: "Firm deposits-in-transit rule", Reason: "October statement line 2 shows $77.00 on Oct 6"}}
		input.FeeEntry = entry
		return input
	}
	entry := func(booked string) *FeeEntry {
		return &FeeEntry{FeeAccount: "Stripe fees", DepositAccount: "Sales", BookedFees: booked, RuleReference: "Engagement letter §4: book Stripe revenue gross"}
	}
	// po_A 4.95 + po_B 21.10 and its 2.00 billing fee row + po_C 1.17; po_G (in transit, 3.00) and
	// po_D (deposited 2 October, 1.46) are left for October.
	paper, err := PrepareSettlement(resolved(entry("0")))
	if err != nil || paper.Fees != "29.22" || paper.Proposal == nil || paper.Proposal.DebitAccount != "Stripe fees" || paper.Proposal.CreditAccount != "Sales" || paper.Proposal.Amount != "29.22" || paper.Proposal.Date != "2026-09-30" {
		t.Fatalf("fees of payouts deposited this month move from the deposit account to fees: %v %+v %+v", err, paper.Proposal, paper.Fees)
	}
	if !strings.Contains(strings.Join(paper.Questions, " "), "$4.46") {
		t.Fatalf("deferred fees are named: %v", paper.Questions)
	}
	if paper, _ = PrepareSettlement(resolved(entry("30.00"))); paper.Proposal == nil || paper.Proposal.DebitAccount != "Sales" || paper.Proposal.Amount != "0.78" {
		t.Fatalf("more booked than charged proposes the excess back: %+v", paper.Proposal)
	}
	if paper, _ = PrepareSettlement(resolved(entry("29.22"))); paper.Proposal != nil {
		t.Fatalf("fees already booked need no entry: %+v", paper.Proposal)
	}
	blocked := settlementInput(stripeReport(settlementRows...))
	blocked.FeeEntry = entry("0")
	if paper, _ = PrepareSettlement(blocked); len(paper.Blockers) == 0 || paper.Proposal != nil || !strings.Contains(strings.Join(paper.Questions, " "), "$3.00 belong to payouts not yet traced") {
		t.Fatalf("an unresolved trace proposes nothing and names the untraced fees: %v %+v %v", paper.Blockers, paper.Proposal, paper.Questions)
	}
	// A payout expected on 1 October whose deposit posted on 30 September belongs to September's deposits,
	// while one expected on 1 September and deposited on 31 August was booked in August.
	early := resolved(entry("0"))
	early.ReportCSV = stripeReport(append(append([]string{}, settlementRows...), "2026-09-01 00:00:00,po_H,2026-09-01 00:00:00,txn_20,2026-08-29 08:00:00,usd,Invoice 90,5.00,100.00,95.00,charge,")...)
	early.BankCSV = settlementBank + "\nB9,2026-08-31,95.00"
	if paper, err = PrepareSettlement(early); err != nil || paper.Fees != "29.22" || !strings.Contains(strings.Join(paper.Questions, " "), "$9.46") {
		t.Fatalf("a deposit dated before the close month is not this month's fee: %v %s %v", err, paper.Fees, paper.Questions)
	}
	if paper, _ = PrepareSettlement(resolved(nil)); paper.Proposal != nil || paper.Fees != "" {
		t.Fatalf("without the rule nothing is proposed: %+v", paper)
	}
	for name, change := range map[string]func(*FeeEntry){
		"no rule":          func(e *FeeEntry) { e.RuleReference = "" },
		"same accounts":    func(e *FeeEntry) { e.DepositAccount = "stripe fees" },
		"no booked fees":   func(e *FeeEntry) { e.BookedFees = "" },
		"negative booking": func(e *FeeEntry) { e.BookedFees = "-1.00" },
		"no fee account":   func(e *FeeEntry) { e.FeeAccount = " " },
	} {
		e := entry("0")
		change(e)
		if _, err := PrepareSettlement(resolved(e)); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

const payoutsHeader = "balance_transaction_id,currency,description,effective_at,fee,gross,net,payout_expected_arrival_date,payout_id,payout_status,reporting_category\n"

func TestManualAndInstantPayoutsAreTracedToTheirDeposits(t *testing.T) {
	report := payoutsHeader + strings.Join([]string{
		"txn_p1,usd,STRIPE PAYOUT,2026-09-02 10:00:00,0.00,-125.05,-125.05,2026-09-03,po_M1,paid,payout",
		"txn_p2,usd,Instant payout,2026-09-11 09:00:00,0.00,-126.90,-126.90,2026-09-11,po_I1,paid,payout",
		"txn_p3,usd,STRIPE PAYOUT,2026-09-20 10:00:00,0.00,-300.00,-300.00,2026-09-22,po_F1,failed,payout",
		"txn_p4,usd,Payout failure,2026-09-23 10:00:00,0.00,300.00,300.00,2026-09-22,po_F1,failed,payout_reversal",
		"txn_p5,usd,STRIPE PAYOUT,2026-09-30 10:00:00,0.00,-40.00,-40.00,2026-10-05,po_T1,in_transit,payout",
		"txn_p6,usd,STRIPE PAYOUT,2026-09-11 11:00:00,0.00,-126.90,-126.90,2026-09-12,po_F2,failed,payout",
	}, "\n") + "\n"
	in := settlementInput(report)
	in.Method = PayoutsMethod
	paper, err := PrepareSettlement(in)
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, p := range paper.Payouts {
		status[p.ID] = p.Status + " " + p.Net
		if p.Deposit != nil {
			status[p.ID] += " " + p.Deposit.ID
		}
	}
	if paper.Method != PayoutsMethod || status["po_M1"] != "matched 125.05 B1" || status["po_I1"] != "matched 126.90 B2" || status["po_F1"] != "failed 300.00" || status["po_F2"] != "failed 126.90" || status["po_T1"] != "unmatched 40.00" || len(paper.Categories) != 0 {
		t.Fatalf("each payout is traced to its deposit; a failed one expects none, even without its reversal in the file, and claims no deposit: %+v", status)
	}
	questions := strings.Join(paper.Questions, " ")
	if len(paper.Blockers) != 1 || !strings.Contains(paper.Blockers[0], "po_T1") || !strings.Contains(questions, "po_F1 ($300.00) as failed or canceled, so no deposit is expected; the file shows its funds returned") || !strings.Contains(questions, "po_F2 ($126.90) as failed or canceled, so no deposit is expected; its return is not in this file") || !strings.Contains(questions, "traced as payouts only") {
		t.Fatalf("an untraced payout blocks; the failed one and the limit of the method are questions: %v %v", paper.Blockers, paper.Questions)
	}
	// A payout expected early next month matches a deposit up to four days after its arrival.
	late := in
	late.BankCSV += "\nB9,2026-10-06,40.00"
	if paper, err = PrepareSettlement(late); err != nil || len(paper.Blockers) != 0 || paper.Payouts[len(paper.Payouts)-1].Deposit == nil || paper.Payouts[len(paper.Payouts)-1].Deposit.ID != "B9" {
		t.Fatalf("a deposit on the sixth of next month matches the payout expected on the fifth: %v %+v", err, paper.Blockers)
	}
	late.ReviewedDeposits = []ReviewedDeposit{{PayoutID: "po_F2", BankID: "B2", Reason: "Same amount as the deposit"}}
	if _, err = PrepareSettlement(late); err == nil {
		t.Fatal("a failed payout cannot take a bank row through review")
	}
	in.FeeEntry = &FeeEntry{FeeAccount: "Stripe fees", DepositAccount: "Sales", BookedFees: "0", RuleReference: "Rule 4.2"}
	if _, err = PrepareSettlement(in); err == nil || !strings.Contains(err.Error(), "fee entry") {
		t.Fatalf("a fee entry needs per-payout fees: %v", err)
	}
	for name, bad := range map[string]string{
		"a positive payout row":          strings.Replace(report, "-125.05,-125.05", "125.05,125.05", 1),
		"funds leaving in another month": strings.Replace(report, "2026-09-02 10:00:00", "2026-08-30 10:00:00", 1),
		"an automatic-payout export":     stripeReport(settlementRows...),
	} {
		changed := settlementInput(bad)
		changed.Method = PayoutsMethod
		if _, err = PrepareSettlement(changed); err == nil {
			t.Fatalf("%s is refused", name)
		}
	}
}

func TestManualPayoutsRollTheStripeBalanceForwardWithTheMonthsActivity(t *testing.T) {
	report := payoutsHeader + strings.Join([]string{
		"txn_r0,usd,Payout failure,2026-09-01 08:00:00,0.00,50.00,50.00,2026-08-29,po_A0,failed,payout_reversal",
		"txn_p4,usd,Payout failure,2026-09-23 10:00:00,0.00,300.00,300.00,2026-09-22,po_F1,failed,payout_reversal",
		"txn_p1,usd,STRIPE PAYOUT,2026-09-02 10:00:00,0.00,-125.05,-125.05,2026-09-03,po_M1,paid,payout",
		"txn_p2,usd,Instant payout,2026-09-11 09:00:00,0.00,-126.90,-126.90,2026-09-11,po_I1,paid,payout",
		"txn_p3,usd,STRIPE PAYOUT,2026-09-20 10:00:00,0.00,-300.00,-300.00,2026-09-22,po_F1,failed,payout",
	}, "\n") + "\n"
	activity := "created,balance_transaction_id,currency,gross,fee,net,reporting_category\n" + strings.Join([]string{
		"2026-09-01 12:00:00,txn_a1,usd,500.00,14.80,485.20,charge",
		"2026-09-05 12:00:00,txn_a2,usd,-60.00,0.00,-60.00,refund",
		"2026-09-09 12:00:00,txn_a3,usd,-4.20,0.00,-4.20,fee",
		"2026-09-30 23:00:00,txn_a4,usd,-10.00,0.00,-10.00,transfer",
	}, "\n") + "\n"
	in := settlementInput(report)
	in.Method, in.ActivityCSV, in.ActivityReference = PayoutsMethod, activity, "activity.csv"
	paper, err := PrepareSettlement(in)
	if err != nil {
		t.Fatal(err)
	}
	questions := strings.Join(paper.Questions, " ")
	if r := paper.RollForward; r == nil || r.Activity != "411.00" || r.PaidOut != "551.95" || r.Returned != "350.00" || r.Change != "209.05" || len(paper.Categories) != 4 || len(paper.Payouts) != 3 {
		t.Fatalf("activity less payouts plus returns is the balance's change, whatever the rows' order: %+v %+v", paper.RollForward, paper.Categories)
	}
	if !strings.Contains(questions, "not tied to Stripe's opening and closing balances") || !strings.Contains(questions, "earlier months returned funds to the Stripe balance this month (po_A0)") || !strings.Contains(questions, "category transfer") || strings.Contains(questions, "Add Stripe's balance change") {
		t.Fatalf("the roll-forward is qualified, an earlier payout's return and an unusual category are questions: %v", paper.Questions)
	}
	unnamed := in
	unnamed.ActivityReference = " "
	if _, err = PrepareSettlement(unnamed); err == nil {
		t.Fatal("the activity report needs its source reference")
	}
	returnsOnly := in
	returnsOnly.ReportCSV = payoutsHeader + "txn_r0,usd,Payout failure,2026-09-01 08:00:00,0.00,50.00,50.00,2026-08-29,po_A0,failed,payout_reversal\n"
	if paper, err = PrepareSettlement(returnsOnly); err != nil || len(paper.Payouts) != 0 || paper.RollForward.Returned != "50.00" {
		t.Fatalf("a month whose only payout movement is an earlier payout's return still rolls forward: %v %+v", err, paper.RollForward)
	}
	twice := in
	twice.ReportCSV = report + "txn_p9,usd,STRIPE PAYOUT,2026-09-12 10:00:00,0.00,-126.90,-126.90,2026-09-11,po_I1,paid,payout\n"
	if _, err = PrepareSettlement(twice); err == nil {
		t.Fatal("a payout with two payout rows is refused")
	}
	automatic := settlementInput(stripeReport(settlementRows...))
	automatic.ActivityCSV, automatic.ActivityReference = activity, "activity.csv"
	if _, err = PrepareSettlement(automatic); err == nil {
		t.Fatal("the automatic-payout export takes no activity report")
	}
	for name, bad := range map[string]string{
		"a row from another month":      strings.Replace(activity, "2026-09-30 23:00:00", "2026-10-01 00:30:00", 1),
		"a payout row":                  strings.Replace(activity, "transfer", "payout", 1),
		"net other than gross less fee": strings.Replace(activity, "485.20", "485.00", 1),
		"a repeated transaction":        strings.Replace(activity, "txn_a2", "txn_a1", 1),
		"another currency":              strings.Replace(activity, "txn_a3,usd", "txn_a3,eur", 1),
	} {
		changed := in
		changed.ActivityCSV = bad
		if _, err = PrepareSettlement(changed); err == nil {
			t.Fatalf("an activity report with %s is refused", name)
		}
	}
}

func TestRefundAndRevenueEntriesCoverPayoutsDepositedThisMonth(t *testing.T) {
	resolved := func() SettlementInput {
		// po_R carries a failed refund: its 5.00 comes back into the balance and reduces the month's refunds.
		input := settlementInput(stripeReport(append(append([]string{}, settlementRows...), "2026-09-20 00:00:00,po_R,2026-09-20 00:00:00,txn_30,2026-09-18 08:00:00,usd,Refund failed,0.00,5.00,5.00,refund_failure,")...))
		input.BankCSV += "\nB10,2026-09-20,5.00"
		input.ReviewedDeposits = []ReviewedDeposit{{PayoutID: "po_E", BankID: "B6", Reason: "Stripe dashboard trace ID matches B6"}, {PayoutID: "po_F", BankID: "B5", Reason: "Stripe dashboard trace ID matches B5"}}
		input.InTransit = []InTransit{{PayoutID: "po_G", PolicyReference: "Firm deposits-in-transit rule", Reason: "October statement line 2 shows $77.00 on Oct 6"}}
		input.FeeEntry = &FeeEntry{FeeAccount: "Stripe fees", DepositAccount: "Stripe clearing", BookedFees: "0", RuleReference: "Rule 4: fees to expense"}
		input.RefundEntry = &RefundEntry{RefundAccount: "Refunds", BookedRefunds: "0", RuleReference: "Rule 5: refunds contra revenue"}
		input.RevenueEntry = &RevenueEntry{RevenueAccount: "Revenue", BookedRevenue: "0", RuleReference: "Rule 6: Stripe clearing account", ClearingConfirmed: true}
		return input
	}
	// Deposited in September: po_A, po_B, po_C, po_E, po_F and po_R. Refunds 20 + 80 - 5; charges 150 + 200 + 30 + 10 + 10;
	// po_B's 50.00 dispute stays in clearing. po_G (in transit) and po_D (deposited 2 October) carry 120.00 of charges.
	paper, err := PrepareSettlement(resolved())
	if err != nil {
		t.Fatal(err)
	}
	if r := paper.RefundProposal; paper.Refunds != "95.00" || r == nil || r.DebitAccount != "Refunds" || r.CreditAccount != "Stripe clearing" || r.Amount != "95.00" {
		t.Fatalf("refunds of deposited payouts move from clearing to refunds: %s %+v", paper.Refunds, r)
	}
	if v := paper.RevenueProposal; paper.Revenue != "400.00" || v == nil || v.DebitAccount != "Stripe clearing" || v.CreditAccount != "Revenue" || v.Amount != "400.00" || v.Date != "2026-09-30" {
		t.Fatalf("gross charges of deposited payouts are revenue out of clearing: %s %+v", paper.Revenue, v)
	}
	questions := strings.Join(paper.Questions, " ")
	if !strings.Contains(questions, "Gross charges of $120.00 belong to payouts in transit or deposited after this month") || !strings.Contains(questions, "stay in Stripe clearing") {
		t.Fatalf("deferred charges and the dispute left in clearing are named: %v", paper.Questions)
	}
	more := resolved()
	more.RefundEntry.BookedRefunds = "30"
	if paper, _ = PrepareSettlement(more); paper.RefundProposal == nil || paper.RefundProposal.Amount != "65.00" {
		t.Fatalf("refunds already booked for these payouts are left out: %+v", paper.RefundProposal)
	}
	more.RevenueEntry.BookedRevenue = "410"
	if paper, _ = PrepareSettlement(more); paper.RevenueProposal == nil || paper.RevenueProposal.DebitAccount != "Revenue" || paper.RevenueProposal.Amount != "10.00" {
		t.Fatalf("more revenue booked than charged proposes the excess back: %+v", paper.RevenueProposal)
	}
	blocked := resolved()
	blocked.ReviewedDeposits = nil
	if paper, _ = PrepareSettlement(blocked); paper.RefundProposal != nil || paper.RevenueProposal != nil {
		t.Fatalf("an unresolved trace proposes nothing: %+v", paper)
	}
	for name, change := range map[string]func(*SettlementInput){
		"a refund entry without the fee entry": func(in *SettlementInput) { in.FeeEntry, in.RevenueEntry = nil, nil },
		"a refund account that is the deposit": func(in *SettlementInput) { in.RefundEntry.RefundAccount = "stripe clearing" },
		"revenue into the deposit account":     func(in *SettlementInput) { in.RevenueEntry.RevenueAccount = "Stripe clearing" },
		"revenue without the clearing rule":    func(in *SettlementInput) { in.RevenueEntry.ClearingConfirmed = false },
		"revenue into the refunds account":     func(in *SettlementInput) { in.RevenueEntry.RevenueAccount = "refunds" },
		"entries on the payouts-report method": func(in *SettlementInput) { in.Method = PayoutsMethod },
	} {
		in := resolved()
		change(&in)
		if _, err := PrepareSettlement(in); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}
