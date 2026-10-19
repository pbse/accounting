package preparation

import (
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/loans"
)

func TestStraightLoanUsesTheStatedSplitAndCurrentCashBooking(t *testing.T) {
	statement := IntakeSource{Source: Source{ID: "1", Digest: "statement", Name: "loan.txt", Role: "statement"}, Content: "First Bank\nBorrower: Example\nLoan reference: L777\nCurrency: USD\nStatement period: 2026-09-01 to 2026-09-30\nAnnual rate: 12%\nOpening principal: USD 10000.00\nOpening interest paid through: 2026-08-31\nNumber of payments: 1\nPayment received date: 2026-09-30\nTotal payments received: USD 200.00\nPrincipal paid: USD 100.00\nInterest paid: USD 100.00\nClosing principal: USD 9900.00"}
	instructions := "QuickBooks company: 123\nLender ID for First Bank: 9\nLoan L777 account: Term Loan\nLoan L777 interest account: Interest Expense\nLoan L777 payment account: Checking\nLoan L777 day count: actual_360\nLoan L777 annual rate: 12\nLoan L777 agreement reference: Note L777\nLoan L777 accrue interest: false\nLoan L777 reclass current portion: false"
	rule := Source{ID: "2", Digest: "policy", Name: "Loan policy", Role: "procedure"}
	proc := Procedure{ID: "3", ClientEntity: "Example", Kind: "loan", Method: loans.Method, Enabled: true, Instructions: instructions, LoanExamples: []LoanExample{{Precedent: Precedent{PaperID: "10", ReviewID: "20", Digest: "example"}, Input: loans.Input{Method: loans.Method, DayCount: "actual_360", Currency: "USD"}}}}
	p := Proposal{Kind: "loan", Engine: "context_machine", Sources: []Source{statement.Source, rule}}
	for _, f := range []struct{ field, value, quote, source string }{
		{"currency", "USD", "Currency: USD", "1"}, {"lender", "First Bank", "First Bank", "1"}, {"borrower", "Example", "Borrower: Example", "1"}, {"loan_reference", "L777", "Loan reference: L777", "1"},
		{"opening_principal", "10000.00", "Opening principal: USD 10000.00", "1"}, {"closing_principal", "9900.00", "Closing principal: USD 9900.00", "1"}, {"annual_rate", "12", "Annual rate: 12%", "1"}, {"interest_paid_through", "2026-08-31", "Opening interest paid through: 2026-08-31", "1"},
		{"payment_count", "1", "Number of payments: 1", "1"}, {"payment_date", "2026-09-30", "Payment received date: 2026-09-30", "1"}, {"payment_total", "200.00", "Total payments received: USD 200.00", "1"}, {"principal_paid", "100.00", "Principal paid: USD 100.00", "1"}, {"interest_paid", "100.00", "Interest paid: USD 100.00", "1"},
		{"agreement_reference", "Note L777", "Loan L777 agreement reference: Note L777", "2"}, {"loan_account", "Term Loan", "Loan L777 account: Term Loan", "2"}, {"interest_account", "Interest Expense", "Loan L777 interest account: Interest Expense", "2"},
	} {
		p.Facts = append(p.Facts, Fact{Field: f.field, Value: f.value, Candidates: []Candidate{{Value: f.value, SourceID: f.source, Quote: f.quote}}})
	}
	l := &Ledger{Source: Source{ID: "4", Digest: "capture"}, Chart: "5", ChartSource: Source{ID: "6", Digest: "books"}, Realm: "123", CaptureRealm: "123", ChartRealm: "123", ReportPeriod: "2026-09", Accounts: map[string]string{"20": "Term Loan", "30": "Checking", "40": "Interest Expense"}, Types: map[string]string{"20": "Long Term Liability", "30": "Bank", "40": "Expense"}, IDs: map[string][]string{"term loan": {"20"}, "checking": {"30"}, "interest expense": {"40"}}}
	l.AccountCatalog = json.RawMessage(`[{"Id":"20","Name":"Term Loan","AccountType":"Long Term Liability"},{"Id":"30","Name":"Checking","AccountType":"Bank","CurrencyRef":{"value":"USD"}},{"Id":"40","Name":"Interest Expense","AccountType":"Expense"}]`)
	l.TrialBalance = json.RawMessage(`{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"20","value":"Term Loan"},{"value":""},{"value":"9900.00"}]}]}}`)
	l.Rows = []LedgerRow{{Kind: "Purchase", ProviderID: "41", Date: "2026-09-30", Currency: "USD", Record: json.RawMessage(`{"TotalAmt":200,"PaymentType":"Cash","AccountRef":{"value":"30","name":"Checking"},"EntityRef":{"value":"9","name":"First Bank","type":"Vendor"},"Line":[{"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"20","name":"Term Loan"}}},{"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40","name":"Interest Expense"}}}]}`)}}
	l.GeneralLedger = json.RawMessage(`{"Header":{"ReportName":"GeneralLedger","Currency":"USD","ReportBasis":"Accrual","StartPeriod":"2026-09-01","EndPeriod":"2026-09-30"},"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Num"},{"ColTitle":"Name"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[{"type":"Section","Header":{"ColData":[{"id":"20","value":"Term Loan"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":""},{"value":"Beginning Balance"},{"value":""},{"value":""},{"value":""},{"value":"10000.00"}]},{"type":"Data","ColData":[{"value":"2026-09-30"},{"id":"41","value":"Expense"},{"value":""},{"id":"9","value":"First Bank"},{"value":"-100.00"},{"value":"9900.00"}]}]}}]}}`)
	plan, reason := StraightLoan(p, []IntakeSource{statement, {Source: rule, Content: instructions}}, proc, rule, "USD", "Example", "2026-09", nil, false, l)
	if reason != "" {
		t.Fatal("the verified monthly loan still needs confirmation:", reason)
	}
	var in loans.Input
	if json.Unmarshal(plan.Input, &in) != nil || len(in.Payments) != 1 || in.Payments[0].Principal != "100.00" || in.Payments[0].Interest != "100.00" || in.BookBalance != "9900.00" || in.DayCount != "actual_360" {
		t.Fatalf("wrong loan input: %+v", in)
	}
	raw, err := Calculate(plan, "Example", "2026-09")
	var paper loans.Workpaper
	if err != nil || json.Unmarshal(raw, &paper) != nil || len(paper.Questions) > 0 || len(paper.Blockers) > 0 || len(paper.Proposals) > 0 || paper.BookDifference != "0.00" {
		t.Fatalf("wrong loan schedule: %s %v", raw, err)
	}

	for name, check := range map[string]struct{ content, reason string }{
		"unpaid charge":     {statement.Content + "\nLate charge assessed: USD 25.00", "fees or escrow"},
		"unread page":       {statement.Content + "\n" + documents.UnreadPage, "fully read"},
		"scheduled payment": {strings.Replace(statement.Content, "Payment received date:", "SCHEDULED Payment received date:", 1), "actual statement activity"},
	} {
		t.Run(name, func(t *testing.T) {
			changed := statement
			changed.Content = check.content
			if _, reason := StraightLoan(p, []IntakeSource{changed, {Source: rule, Content: instructions}}, proc, rule, "USD", "Example", "2026-09", nil, false, l); !strings.Contains(reason, check.reason) {
				t.Fatalf("want %q hold; got %q", check.reason, reason)
			}
		})
	}

	original := statement.Content
	statement.Content = strings.Replace(statement.Content, "Borrower: Example", "Borrower: Another Example", 1)
	for i := range p.Facts {
		if p.Facts[i].Field == "borrower" {
			p.Facts[i].Candidates[0].Quote = "Borrower: Another Example"
		}
	}
	if _, reason := StraightLoan(p, []IntakeSource{statement, {Source: rule, Content: instructions}}, proc, rule, "USD", "Example", "2026-09", nil, false, l); !strings.Contains(reason, "complete borrower") {
		t.Fatal("a truncated borrower name advanced automatically")
	}
	statement.Content = original
	for i := range p.Facts {
		if p.Facts[i].Field == "borrower" {
			p.Facts[i].Candidates[0].Quote = "Borrower: Example"
		}
	}

	l.Rows[0].Record = json.RawMessage(strings.Replace(string(l.Rows[0].Record), `"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"20"`, `"Amount":99,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"20"`, 1))
	if _, reason := StraightLoan(p, []IntakeSource{statement, {Source: rule, Content: instructions}}, proc, rule, "USD", "Example", "2026-09", nil, false, l); !strings.Contains(reason, "cash/principal/interest booking") {
		t.Fatal("a misbooked principal split advanced automatically")
	}

}
