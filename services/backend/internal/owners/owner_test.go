package owners

import (
	"strings"
	"testing"
)

func answered() Input {
	return Input{Entity: "Harbor Dental", ClosePeriod: "2026-09", EntityType: "sole_proprietor", TransactionReference: "Purchase 1072", TransactionDate: "2026-09-18", Payee: "Bistro", Amount: "100.01",
		LedgerReference: "General ledger Sep 2026 p3", Direction: "personal_paid_by_business", PurposeStatus: "answered", StatedPurpose: "Family dinner, personal", ClientConfirmationReference: "Client reply 2026-09-22",
		Treatment: "distribution", BookedAccount: "Meals", OwnerAccount: "Owner's draw", Method: Method, TreatmentConfirmed: true}
}

func TestOwnerReclassesFollowTheConfirmedPurpose(t *testing.T) {
	paper, err := Prepare(answered())
	if err != nil || paper.Proposal == nil || paper.Proposal.DebitAccount != "Owner's draw" || paper.Proposal.CreditAccount != "Meals" || paper.Proposal.Amount != "100.01" || paper.Proposal.Date != "2026-09-30" {
		t.Fatalf("a personal cost moves from the expense to the owner's draw: %+v %v", paper.Proposal, err)
	}
	in := answered()
	in.BusinessUsePercent = 33 // 67% of 10,001¢ = 6,700.67¢ → 67.01
	if paper, err = Prepare(in); err != nil || paper.Proposal.Amount != "67.01" {
		t.Fatalf("mixed use reclasses only the personal share: %+v %v", paper, err)
	}
	in = answered()
	in.Direction, in.Treatment, in.BusinessUsePercent, in.BookedAccount, in.OwnerAccount, in.NotBookedConfirmed = "business_paid_personally", "contribution", 33, "Software", "Owner's contribution", true
	if paper, err = Prepare(in); err != nil || paper.Proposal.DebitAccount != "Software" || paper.Proposal.CreditAccount != "Owner's contribution" || paper.Proposal.Amount != "33.00" {
		t.Fatalf("a business cost paid personally books the business share against a contribution: %+v %v", paper, err)
	}
	in.NotBookedConfirmed = false
	if _, err = Prepare(in); err == nil {
		t.Fatal("a personally paid cost must be confirmed as not already booked or reimbursed")
	}
	in = answered()
	in.BusinessUsePercent = 100
	if paper, err = Prepare(in); err != nil || paper.Proposal != nil || paper.Portion != "0.00" {
		t.Fatalf("a fully business cost proposes nothing: %+v %v", paper, err)
	}
	for name, change := range map[string]func(*Input){
		"due from owner without repayment":    func(i *Input) { i.Treatment, i.OwnerAccount = "due_from_owner", "Due from owner" },
		"C corp distribution undeclared":      func(i *Input) { i.EntityType, i.OwnerAccount = "c_corp", "Dividends paid" },
		"partner draw without partner":        func(i *Input) { i.EntityType = "partnership" },
		"no stated purpose":                   func(i *Input) { i.StatedPurpose = "" },
		"transaction outside the close month": func(i *Input) { i.TransactionDate = "2026-08-31" },
		"contribution for a personal cost":    func(i *Input) { i.Treatment = "contribution" },
	} {
		in := answered()
		change(&in)
		if _, err := Prepare(in); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	in = answered()
	in.Treatment, in.OwnerAccount, in.RepaymentReference = "due_from_owner", "Due from owner", "Owner email agreeing to repay"
	if paper, err = Prepare(in); err != nil || paper.Proposal.DebitAccount != "Due from owner" {
		t.Fatalf("a confirmed repayment becomes a receivable from the owner: %+v %v", paper, err)
	}
	open := Input{Entity: "Harbor Dental", ClosePeriod: "2026-09", EntityType: "s_corp", TransactionReference: "Purchase 1072", TransactionDate: "2026-09-18", Payee: "Bistro", Amount: "86.40", LedgerReference: "GL p3", Direction: "personal_paid_by_business", PurposeStatus: "open", Method: Method}
	if paper, err = Prepare(open); err != nil || paper.Proposal != nil || len(paper.Blockers) != 1 || !strings.Contains(paper.Question, "$86.40 payment to Bistro") {
		t.Fatalf("an unanswered purpose stays a question: %+v %v", paper, err)
	}
}
