package transport

import (
	"math/big"
	"strings"
	"testing"
	"time"
)

// The handoff's matching fixtures (docs/agent-first-implementation-handoff.md, "Matching cases"), each on its own: a
// schedule's date counts what the decisions about bills leave of it, and a bill from its vendor that week that nobody
// has decided about is a question, never a silent replacement.
func TestSettleOccurrenceCountsOnlyWhatDecisionsSay(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	sameWeek := func(a, b string) bool {
		x, errX := time.Parse("2006-01-02", a)
		y, errY := time.Parse("2006-01-02", b)
		return errX == nil && errY == nil && !x.Before(start) && !y.Before(start) && int(x.Sub(start).Hours()/24)/7 == int(y.Sub(start).Hours()/24)/7
	}
	// all: every capture can say whether a decided bill is still open; none: no capture can.
	all := func(cashMatch) bool { return true }
	none := func(cashMatch) bool { return false }
	rent := cashObligation{ID: "1", Name: "Rent", Amount: "10000.00", Vendor: "Landlord", Direction: "out"}
	parking := cashObligation{ID: "2", Name: "Parking", Amount: "200.00", Vendor: "Landlord", Direction: "out"}
	const on = "2026-10-07"
	bill := func(key, balance, due, plan string, counted bool, reason string) cashBill {
		v, _ := new(big.Rat).SetString(balance)
		return cashBill{Key: key, Label: "Bill " + key + " · Landlord", Vendor: "Landlord", Due: due, On: plan, Balance: v, Counted: counted, Reason: reason}
	}
	match := func(id, obligation, document, relation, amount string) cashMatch {
		return cashMatch{ID: id, Obligation: obligation, On: on, Document: document, Label: "Bill " + document + " · Landlord", Relation: relation, Amount: amount}
	}
	// counted is what the plan pays out that week: the schedule's residual plus the counted bills.
	counted := func(residual *big.Rat, bills map[string]cashBill) string {
		total := new(big.Rat).Set(residual)
		for _, b := range bills {
			if b.Counted {
				total.Add(total, b.Balance)
			}
		}
		return total.FloatString(2)
	}

	// Approved rent of 10,000 and a separate 500 maintenance bill from the same landlord that week.
	bills := map[string]cashBill{"bill:20": bill("bill:20", "500.00", on, on, true, "")}
	residual, matched, question := settleOccurrence(rent, on, sameWeek, bills, nil, all)
	if residual.FloatString(2) != "10000.00" || matched != nil || question == nil || len(question.Bills) != 1 || question.Bills[0].Document != "bill:20" || counted(residual, bills) != "10500.00" ||
		!strings.Contains(question.Effect, "fall by 10,000.00") {
		t.Fatalf("a landlord bill that week is a question, and until it is answered both count (10,500.00): %v %+v %+v", residual, matched, question)
	}
	residual, matched, question = settleOccurrence(rent, on, sameWeek, bills, []cashMatch{match("1", "1", "bill:20", "separate", "500.00")}, all)
	if residual.FloatString(2) != "10000.00" || question != nil || matched == nil || counted(residual, bills) != "10500.00" {
		t.Fatalf("decided separate, the known distinct commitments total 10,500.00 with no question: %v %+v %+v", residual, matched, question)
	}

	// A 4,000 bill invoicing part of the 10,000: the bill and a 6,000 residual.
	bills = map[string]cashBill{"bill:21": bill("bill:21", "4000.00", on, on, true, "")}
	part := []cashMatch{match("2", "1", "bill:21", "part", "4000.00")}
	residual, matched, question = settleOccurrence(rent, on, sameWeek, bills, part, all)
	if residual.FloatString(2) != "6000.00" || question != nil || counted(residual, bills) != "10000.00" || matched.Counted != "6000.00" || !strings.Contains(matched.Note, "remaining 6,000.00") {
		t.Fatalf("the bill plus a 6,000.00 residual: %v %+v", residual, matched)
	}
	// A 500 vendor credit applied to that bill lowers what it pays, not what is still to be invoiced.
	bills["bill:21"] = bill("bill:21", "3500.00", on, on, true, "")
	if residual, _, _ = settleOccurrence(rent, on, sameWeek, bills, part, all); residual.FloatString(2) != "6000.00" || counted(residual, bills) != "9500.00" {
		t.Fatalf("a credit on the part bill leaves the 6,000.00 residual: %v", residual)
	}
	// A part bill that left the open items may have been paid, voided or credited: until someone confirms it was paid,
	// its part counts again; once confirmed, only the residual does.
	if residual, matched, _ = settleOccurrence(rent, on, sameWeek, map[string]cashBill{}, part, all); residual.FloatString(2) != "10000.00" || !matched.Bills[0].Confirm {
		t.Fatalf("a vanished bill settles nothing until confirmed paid: %v %+v", residual, matched)
	}
	// A separate bill that left the open items decided nothing about the date, so nothing is asked.
	if _, matched, _ = settleOccurrence(rent, on, sameWeek, map[string]cashBill{}, []cashMatch{match("9", "1", "bill:20", "separate", "500.00")}, all); matched.Bills[0].Confirm || matched.Bills[0].State != "No longer open." {
		t.Fatalf("only a paying bill is confirmed: %+v", matched)
	}
	paid := []cashMatch{part[0]}
	paid[0].Paid = true
	if residual, matched, _ = settleOccurrence(rent, on, sameWeek, map[string]cashBill{}, paid, all); residual.FloatString(2) != "6000.00" || matched.Bills[0].State != "paid" || matched.Bills[0].Confirm {
		t.Fatalf("a part bill confirmed paid keeps the residual: %v %+v", residual, matched)
	}

	// A part bill missing from a capture that cannot say (partial, taken before the decision, or not covering the bill's
	// date) is not taken as paid: its part still counts, and nothing is asked.
	if residual, matched, _ = settleOccurrence(rent, on, sameWeek, map[string]cashBill{}, part, none); residual.FloatString(2) != "10000.00" || !strings.Contains(matched.Bills[0].State, "not taken as paid") || matched.Bills[0].Confirm {
		t.Fatalf("absence from a partial capture is not payment: %v %+v", residual, matched)
	}

	// Two obligations for one vendor and week: only the date the bill is decided against is replaced.
	bills = map[string]cashBill{"bill:22": bill("bill:22", "10000.00", on, on, true, "")}
	replaces := []cashMatch{match("3", "1", "bill:22", "replaces", "10000.00")}
	residual, matched, question = settleOccurrence(rent, on, sameWeek, bills, replaces, all)
	if residual.Sign() != 0 || question != nil || matched.Counted != "0.00" || counted(residual, bills) != "10000.00" {
		t.Fatalf("full replacement: the bill alone counts: %v %+v", residual, matched)
	}
	if residual, matched, question = settleOccurrence(parking, on, sameWeek, bills, replaces, all); residual.FloatString(2) != "200.00" || matched != nil || question != nil {
		t.Fatalf("the parking schedule from the same landlord that week is untouched and asks nothing: %v %+v %+v", residual, matched, question)
	}
	// With the rent paid by one bill, another landlord bill that week is still asked about: it counts until someone
	// says it is separate or a repeat.
	bills["bill:20"] = bill("bill:20", "500.00", on, on, true, "")
	if residual, _, question = settleOccurrence(rent, on, sameWeek, bills, replaces, all); residual.Sign() != 0 || question == nil || question.Amount != "0.00" || len(question.Bills) != 1 || question.Bills[0].Document != "bill:20" || !strings.HasPrefix(question.Effect, "Another bill already pays this date") {
		t.Fatalf("a second bill on a paid date is still a question: %v %+v", residual, question)
	}
	delete(bills, "bill:20")

	// A duplicate capture: the rent bill entered twice; one replaces the rent, the other is decided a repeat and left
	// out of the weeks (the plan marks it not counted), so the rent is paid once.
	bills = map[string]cashBill{"bill:22": bill("bill:22", "10000.00", on, on, true, ""), "bill:23": bill("bill:23", "10000.00", on, on, false, "Left out: the same payment entered twice.")}
	twice := append(append([]cashMatch{}, replaces...), match("4", "1", "bill:23", "duplicate", "10000.00"))
	if residual, matched, question = settleOccurrence(rent, on, sameWeek, bills, twice, all); residual.Sign() != 0 || question != nil || len(matched.Bills) != 2 || counted(residual, bills) != "10000.00" {
		t.Fatalf("the repeat is left out and the rent is paid once: %v %+v %+v", residual, matched, question)
	}

	// A disputed or undated bill does not stand in for the payment it was decided to replace: the estimate counts.
	for _, reason := range []string{"It is disputed, so no date is assumed.", "It has no due date and no agreed date."} {
		bills = map[string]cashBill{"bill:22": bill("bill:22", "10000.00", on, "", false, reason)}
		residual, matched, question = settleOccurrence(rent, on, sameWeek, bills, replaces, all)
		if residual.FloatString(2) != "10000.00" || question != nil || matched.Bills[0].State != reason || !strings.Contains(matched.Note, "estimate counts for it") {
			t.Fatalf("a bill left out of the weeks leaves the estimate standing (%s): %v %+v", reason, residual, matched)
		}
	}
	// An undated or disputed bill nobody decided about is no question either: it is not counted, so nothing is twice.
	if _, _, question = settleOccurrence(rent, on, sameWeek, bills, nil, all); question != nil {
		t.Fatalf("a bill left out of the weeks asks nothing: %+v", question)
	}

	// A moved payment date keeps the bill's identity: handed to bill pay two weeks later, it still replaces the rent.
	bills = map[string]cashBill{"bill:22": bill("bill:22", "10000.00", on, "2026-10-21", true, "")}
	if residual, matched, _ = settleOccurrence(rent, on, sameWeek, bills, replaces, all); residual.Sign() != 0 || matched.Bills[0].On != "2026-10-21" {
		t.Fatalf("the moved bill still pays the rent, on its new date: %v %+v", residual, matched)
	}
	// Undecided, a bill due that week but moved later is still asked about.
	if _, _, question = settleOccurrence(rent, on, sameWeek, bills, nil, all); question == nil {
		t.Fatal("a bill due that week is a question wherever its payment moved")
	}
	// Bills invoicing more than the schedule leave nothing and say by how much.
	bills = map[string]cashBill{"bill:21": bill("bill:21", "6000.00", on, on, true, ""), "bill:24": bill("bill:24", "5000.00", on, on, true, "")}
	over := []cashMatch{match("5", "1", "bill:21", "part", "6000.00"), match("6", "1", "bill:24", "part", "5000.00")}
	if residual, matched, _ = settleOccurrence(rent, on, sameWeek, bills, over, all); residual.Sign() != 0 || !strings.Contains(matched.Note, "1,000.00 more than the schedule") {
		t.Fatalf("parts beyond the schedule leave nothing and are named: %v %+v", residual, matched)
	}
	// A bill from another vendor, a credit (no balance owed) or one in another week is never a question.
	other := bill("bill:25", "700.00", on, on, true, "")
	other.Vendor = "Cleaner"
	credit := bill("bill:26", "0.00", on, on, true, "")
	later := bill("bill:27", "900.00", "2026-10-14", "2026-10-14", true, "")
	if _, _, question = settleOccurrence(rent, on, sameWeek, map[string]cashBill{"bill:25": other, "bill:26": credit, "bill:27": later}, nil, all); question != nil {
		t.Fatalf("no other vendor, credit or week is asked about: %+v", question)
	}
}

func TestGroupedAmountWritesExactCents(t *testing.T) {
	for in, want := range map[string]string{"10000": "10,000.00", "-1250.5": "-1,250.50", "999.99": "999.99", "1234567.891": "1,234,567.89", "0": "0.00"} {
		v, _ := new(big.Rat).SetString(in)
		if got := groupedAmount(v); got != want {
			t.Fatalf("%s: %s, want %s", in, got, want)
		}
	}
}

func TestCashReadinessNamesWhatIsUnresolved(t *testing.T) {
	if r := cashReadiness(nil); r["state"] != "complete" || len(r["unresolved"].([]cashGap)) != 0 {
		t.Fatalf("nothing unresolved is complete: %+v", r)
	}
	bonus := cashGap{"decision", "Bonus is proposed but not approved, so it is not counted.", "18000.00"}
	undated := cashGap{"source", "Bill B-7 · Supplier: It has no due date and no agreed date.", "6420.00"}
	if r := cashReadiness([]cashGap{bonus}); r["state"] != "draft" {
		t.Fatalf("an unapproved bonus leaves a draft: %+v", r)
	}
	if r := cashReadiness([]cashGap{bonus, undated}); r["state"] != "incomplete" || len(r["unresolved"].([]cashGap)) != 2 {
		t.Fatalf("an undated bill leaves the forecast incomplete, and both are named: %+v", r)
	}
}

func TestReviewedStateIsCurrentOnlyForACompleteForecast(t *testing.T) {
	for state, want := range map[string]string{"complete": "current", "draft": "draft_reviewed", "incomplete": "draft_reviewed"} {
		if got := reviewedState(map[string]any{"readiness": map[string]any{"state": state}}); got != want {
			t.Fatalf("%s: %s, want %s", state, got, want)
		}
	}
}
