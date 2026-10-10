package seed

import (
	"fmt"
	"math/big"
	"strings"
	"time"
)

func monthName(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.Format("January 2006")
}

// PrepaidSpec is an annual policy or subscription invoiced in one month and spread over its service period.
type PrepaidSpec struct {
	Month, Vendor, Address, Number, Amount string
	Months                                 int
	Expense, What                          string
}

// LoanSpec is a term loan paid monthly from the lender's statement.
type LoanSpec struct {
	Lender, Agreement, Account string
	OpeningPrincipal           string // at the start of June
	Rate                       string // annual, e.g. "7.250"
	Principal, Interest        string // each month's payment split, as the statement shows it
	PayDay                     int
	AccrueInterest             bool
}

// PayrollSpec is a provider's monthly payroll, debited from the bank and cleared by the agent's entry.
type PayrollSpec struct {
	Provider                                                            string
	Gross, EmployeeTaxes, Deductions, Net, EmployerTaxes, Contributions string
	PayDays                                                             []int
}

// AccrualSpec is an incurred service invoiced after month-end, accrued from the vendor's estimate.
type AccrualSpec struct {
	Vendor, What, Amount, Expense, ExpenseName string
	Reverse                                    bool
}

// OwnerSpec is a personal purchase paid by the business, reclassified to the owner's distributions.
type OwnerSpec struct {
	Payee, Amount, Memo, Day string
	Purpose                  string
}

func (c *Client) prepaidMonthly(p PrepaidSpec) *big.Rat {
	return new(big.Rat).Quo(Money(p.Amount), big.NewRat(int64(p.Months), 1))
}

// TxnsFor is the month's transactions: the shared recurring activity, the client's own, and what the work describes.
func (c *Client) TxnsFor(g *ids, period string, n int) []Txn {
	txns := recurring(c, g, period, c.Revenue, c.Customers, c.Income)
	if c.Monthly != nil {
		txns = append(txns, c.Monthly(c, g, period, n)...)
	}
	for _, p := range c.Prepaids {
		if p.Month == period {
			txns = append(txns, prepaidBill(g, c, period, p.Vendor, p.Number, p.Amount, p.Expense))
		}
	}
	if l := c.Loan; l != nil {
		principal, interest := Money(l.Principal), Money(l.Interest)
		txns = append(txns, Txn{Kind: "Purchase", ID: g.id(), Date: day(period, l.PayDay), Party: l.Lender, PartyID: "L1", Memo: "Loan payment " + l.Agreement, Amount: add(principal, interest),
			Lines: []Posting{{Account: "30", Debit: principal}, {Account: "76", Debit: interest}, {Account: "10", Credit: add(principal, interest)}}})
	}
	if p := c.Payroll; p != nil {
		total := add(Money(p.Gross), Money(p.EmployerTaxes))
		// Split to whole cents, the first debit taking the remainder.
		each := cents(new(big.Rat).Quo(total, big.NewRat(int64(len(p.PayDays)), 1)))
		first := sub(total, new(big.Rat).Mul(each, big.NewRat(int64(len(p.PayDays)-1), 1)))
		for i, d := range p.PayDays {
			amount := each
			if i == 0 {
				amount = first
			}
			txns = append(txns, Txn{Kind: "Purchase", ID: g.id(), Date: day(period, d), Party: p.Provider, PartyID: "V8", Memo: "Payroll debit", Amount: amount,
				Lines: []Posting{{Account: "24", Debit: amount}, {Account: "10", Credit: amount}}})
		}
	}
	if o, ok := c.Owners[period]; ok {
		txns = append(txns, Txn{Kind: "Purchase", ID: g.id(), Date: o.Day, Party: o.Payee, PartyID: "O1", Memo: o.Memo, Amount: Money(o.Amount),
			Lines: []Posting{{Account: "77", Debit: Money(o.Amount)}, {Account: "10", Credit: Money(o.Amount)}}})
	}
	// An accrual reverses on the first of the next month, and the vendor's bill arrives then.
	if a, ok := c.Accruals[previousMonth(period)]; ok && a.Reverse {
		amount := Money(a.Amount)
		txns = append(txns, journal(g, day(period, 1), "Reverse accrual: "+a.What, Posting{Account: "22", Debit: amount}, Posting{Account: a.Expense, Credit: amount}))
		txns = append(txns, bill(g, day(period, 8), Party{ID: "A1", Name: a.Vendor}, "ACT-"+strings.ReplaceAll(previousMonth(period), "-", ""), vary(a.Amount, c.Name, period, "actual"), a.Expense, "20", day(period, 28), false))
	}
	return txns
}

// cents rounds down to whole cents.
func cents(r *big.Rat) *big.Rat {
	scaled := new(big.Rat).Mul(r, big.NewRat(100, 1))
	whole := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	return new(big.Rat).SetFrac(whole, big.NewInt(100))
}

func previousMonthOf(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, -1, 0).Format("2006-01")
}

func previousMonth(period string) string {
	for i, m := range Months {
		if m == period && i > 0 {
			return Months[i-1]
		}
	}
	return ""
}

// AdjustFor is the approved entries the bookkeeper posts for the month once it is signed off.
func (c *Client) AdjustFor(g *ids, period string) []Txn {
	var txns []Txn
	for _, p := range c.Prepaids {
		if p.Month <= period && monthsBetween(p.Month, period) < p.Months {
			txns = append(txns, prepaidAdjust(g, period, p.Month, p.Amount, p.Months, p.Expense))
		}
	}
	if p := c.Payroll; p != nil {
		gross, employer := Money(p.Gross), Money(p.EmployerTaxes)
		txns = append(txns, journal(g, monthEnd(period), "Payroll: "+p.Provider+" summary", Posting{Account: "74", Debit: gross}, Posting{Account: "75", Debit: employer}, Posting{Account: "24", Credit: add(gross, employer)}))
	}
	if a, ok := c.Accruals[period]; ok {
		amount := Money(a.Amount)
		txns = append(txns, journal(g, monthEnd(period), "Accrue: "+a.What, Posting{Account: a.Expense, Debit: amount}, Posting{Account: "22", Credit: amount}))
	}
	if o, ok := c.Owners[period]; ok {
		txns = append(txns, journal(g, monthEnd(period), "Owner reclass: "+o.Payee, Posting{Account: "41", Debit: Money(o.Amount)}, Posting{Account: "77", Credit: Money(o.Amount)}))
	}
	return txns
}

// loanState is the loan's principal before and after the month's payment.
func (c *Client) loanState(period string) (opening, closing *big.Rat) {
	l := c.Loan
	opening = Money(l.OpeningPrincipal)
	for _, m := range Months {
		if m == period {
			break
		}
		opening = sub(opening, Money(l.Principal))
	}
	return opening, sub(opening, Money(l.Principal))
}

// WorkFor is the month's documents and what is done with them.
func (c *Client) WorkFor(period string, books Books) []Work {
	var work []Work
	for _, p := range c.Prepaids {
		if p.Month == period {
			work = append(work, prepaidWork(c, p.Vendor, p.Address, p.Number, period, p.Amount, p.Months, p.What, ExpenseName(p.Expense)))
		}
	}
	if l := c.Loan; l != nil {
		opening, closing := c.loanState(period)
		payDate := day(period, l.PayDay)
		paidThrough := day(previousMonthOf(period), l.PayDay)
		text := fmt.Sprintf(`%s
Commercial Lending · Loan statement

Borrower: %s
Loan: %s (%s)
Statement period: %s to %s
Annual rate: %s%% fixed · Day count: actual/360

Opening principal: USD %s
Payment received %s: principal USD %s, interest USD %s
Closing principal: USD %s
Interest paid through: %s

Next payment due %s.
`, l.Lender, c.Name, l.Agreement, l.Account, monthStart(period), monthEnd(period), l.Rate, show(opening), payDate, l.Principal, l.Interest, show(closing), payDate, day(nextMonth(period), l.PayDay))
		answer := map[string]any{"kind": "loan", "summary": "Loan statement from " + l.Lender, "questions": []string{},
			"facts": cited([]map[string]string{
				{"field": "lender", "value": l.Lender, "quote": l.Lender + "\nCommercial Lending"},
				{"field": "agreement_reference", "value": l.Agreement, "quote": "Loan: " + l.Agreement},
				{"field": "annual_rate", "value": l.Rate, "quote": "Annual rate: " + l.Rate + "% fixed"},
				{"field": "opening_principal", "value": show(opening), "quote": "Opening principal: USD " + show(opening)},
				{"field": "interest_paid_through", "value": payDate, "quote": "Interest paid through: " + payDate},
				{"field": "interest_account", "value": "Interest Expense", "quote": "Payment received " + payDate},
				{"field": "closing_principal", "value": show(closing), "quote": "Closing principal: USD " + show(closing)},
				{"field": "book_balance", "value": show(closing), "quote": "Closing principal: USD " + show(closing)},
				{"field": "loan_account", "value": "Equipment Loan", "quote": "(" + l.Account + ")"},
			})}
		input := map[string]any{"lender": l.Lender, "source_reference": "Loan statement " + period, "agreement_reference": l.Agreement, "currency": "USD",
			"annual_rate": l.Rate, "day_count": "actual_360", "opening_principal": show(opening), "interest_paid_through": paidThrough,
			"payments":          []map[string]string{{"date": payDate, "principal": l.Principal, "interest": l.Interest}},
			"closing_principal": show(closing), "book_balance": show(closing), "loan_account": "Equipment Loan", "interest_account": "Interest Expense",
			"accrue_interest": l.AccrueInterest, "method": "lender_statement_v1", "method_confirmed": true, "rate_confirmed": true, "booking_confirmed": true}
		if l.AccrueInterest {
			input["accrued_interest_account"] = "Accrued Interest"
			input["booked_accrued_interest"] = "0.00"
		}
		item := Work{Kind: "loan", File: fmt.Sprintf("%s-loan-statement-%s.txt", slug(l.Lender), period), Text: text, Answer: answer, Confirm: input}
		if c.Scenario == WaitingClient && period == Months[len(Months)-1] {
			// Only the payment confirmation came; the statement with the closing balance has to be asked for.
			item.File = fmt.Sprintf("%s-payment-confirmation-%s.txt", slug(l.Lender), period)
			item.Text = fmt.Sprintf("%s\nPayment confirmation\n\nBorrower: %s\nLoan: %s\nPayment received %s: principal USD %s, interest USD %s\n\nYour statement will be available in online banking after month-end.\n", l.Lender, c.Name, l.Agreement, payDate, l.Principal, l.Interest)
			item.Answer = map[string]any{"kind": "loan", "summary": "Payment confirmation from " + l.Lender + ", without the statement", "questions": []string{"The confirmation shows the " + payDate + " payment but not the closing principal or the interest paid through date. The " + period + " statement from " + l.Lender + " is needed."},
				"facts": cited([]map[string]string{{"field": "lender", "value": l.Lender, "quote": l.Lender + "\nPayment confirmation"}, {"field": "agreement_reference", "value": l.Agreement, "quote": "Loan: " + l.Agreement}})}
			item.Request = &Request{Recipient: c.ContactEmail, Body: "Hi " + strings.Split(c.Contact, " ")[len(strings.Split(c.Contact, " "))-1] + ",\n\nFor the " + monthName(period) + " close we need the " + l.Lender + " loan statement for " + l.Agreement + " showing the closing principal after the " + payDate + " payment. The payment confirmation alone does not give the balance.\n\nCould you download it from online banking and reply with it attached? Thank you.", Delivered: "Sent from the firm's mailbox on " + day(nextMonth(period), 6) + " (message ID demo-" + period + ")"}
		}
		work = append(work, item)
	}
	if p := c.Payroll; p != nil {
		first, last := day(period, p.PayDays[0]), day(period, p.PayDays[len(p.PayDays)-1])
		total := show(add(Money(p.Gross), Money(p.EmployerTaxes)))
		text := fmt.Sprintf(`%s · Payroll summary
Company: %s
Pay period: %s to %s (pay dates %s, %s)

Gross wages: %s
Employee taxes withheld: %s
Employee deductions: %s
Net pay: %s
Employer taxes: %s
Employer contributions: %s

Total debited from Checking: %s
`, p.Provider, c.Name, monthStart(period), monthEnd(period), first, last, p.Gross, p.EmployeeTaxes, p.Deductions, p.Net, p.EmployerTaxes, p.Contributions, total)
		answer := map[string]any{"kind": "payroll", "summary": p.Provider + " payroll summary for " + period, "questions": []string{},
			"facts": cited([]map[string]string{
				{"field": "provider", "value": p.Provider, "quote": p.Provider + " · Payroll summary"},
				{"field": "source_reference", "value": "Payroll summary " + period, "quote": p.Provider + " · Payroll summary"},
				{"field": "first_pay_date", "value": first, "quote": "pay dates " + first},
				{"field": "last_pay_date", "value": last, "quote": ", " + last + ")"},
				{"field": "gross_wages", "value": p.Gross, "quote": "Gross wages: " + p.Gross},
				{"field": "employee_taxes", "value": p.EmployeeTaxes, "quote": "Employee taxes withheld: " + p.EmployeeTaxes},
				{"field": "employee_deductions", "value": p.Deductions, "quote": "Employee deductions: " + p.Deductions},
				{"field": "net_pay", "value": p.Net, "quote": "Net pay: " + p.Net},
				{"field": "employer_taxes", "value": p.EmployerTaxes, "quote": "Employer taxes: " + p.EmployerTaxes},
				{"field": "employer_contributions", "value": p.Contributions, "quote": "Employer contributions: " + p.Contributions},
			})}
		input := map[string]any{"currency": "USD", "provider": p.Provider, "source_reference": "Payroll summary " + period, "first_pay_date": first, "last_pay_date": last,
			"gross_wages": p.Gross, "employee_taxes": p.EmployeeTaxes, "employee_deductions": p.Deductions, "net_pay": p.Net, "employer_taxes": p.EmployerTaxes, "employer_contributions": p.Contributions,
			"provider_debits": total, "ledger_debits": total, "ledger_reference": "Payroll Clearing, General Ledger " + period, "wage_account": "Wages", "tax_expense_account": "Payroll Taxes",
			"clearing_account": "Payroll Clearing", "liability_account": "Payroll Liabilities", "method": "payroll_summary_v1", "approved_confirmed": true, "not_booked_confirmed": true}
		work = append(work, Work{Kind: "payroll", File: fmt.Sprintf("%s-payroll-summary-%s.txt", slug(p.Provider), period), Text: text, Answer: answer, Confirm: input})
	}
	if a, ok := c.Accruals[period]; ok {
		booked := "0.00"
		text := fmt.Sprintf(`%s
Estimate for the period %s to %s

Customer: %s
Service: %s
Estimated charge for the period: USD %s
Expense account: %s
Liability account: Accrued Liabilities
Booked balance: %s

The invoice for this period will be issued on %s.
`, a.Vendor, monthStart(period), monthEnd(period), c.Name, a.What, a.Amount, a.ExpenseName, booked, day(nextMonth(period), 8))
		answer := accrualAnswer(a.What+" estimate for "+period, a.Amount, monthStart(period), monthEnd(period), "Estimated charge for the period: USD "+a.Amount, fmt.Sprintf("Estimate for the period %s to %s", monthStart(period), monthEnd(period)), a.ExpenseName, "Accrued Liabilities", booked)
		confirm := accrualConfirm(a.Amount, monthStart(period), monthEnd(period), a.ExpenseName, "Accrued Liabilities", booked, "Vendor estimate for the service period", a.Reverse)
		work = append(work, Work{Kind: "accrual", File: fmt.Sprintf("%s-estimate-%s.txt", slug(a.Vendor), period), Text: text, Answer: answer, Confirm: confirm})
	}
	if o, ok := c.Owners[period]; ok {
		text := fmt.Sprintf(`From: %s <%s>
Subject: Re: %s charge on %s

Hi, that %s charge of $%s on %s was %s. Please treat it as a personal expense.

Thanks,
%s
`, c.Contact, c.ContactEmail, o.Payee, o.Day, o.Payee, o.Amount, o.Day, o.Purpose, c.Contact)
		answer := map[string]any{"kind": "owner", "summary": "Owner's reply about a " + o.Payee + " charge", "questions": []string{},
			"facts": cited([]map[string]string{
				{"field": "transaction_reference", "value": o.Payee + " " + o.Day, "quote": o.Payee + " charge on " + o.Day},
				{"field": "transaction_date", "value": o.Day, "quote": "charge on " + o.Day},
				{"field": "payee", "value": o.Payee, "quote": "Re: " + o.Payee},
				{"field": "amount", "value": o.Amount, "quote": "$" + o.Amount},
				{"field": "stated_purpose", "value": o.Purpose, "quote": "was " + o.Purpose},
				{"field": "client_confirmation_reference", "value": "Email from " + c.Contact, "quote": "From: " + c.Contact},
				{"field": "ledger_reference", "value": "General Ledger " + period, "quote": "Please treat it as a personal expense."},
			})}
		input := map[string]any{"entity_type": c.EntityType, "transaction_reference": "Purchase " + o.Payee + " " + o.Day, "transaction_date": o.Day, "payee": o.Payee, "amount": o.Amount, "currency": "USD", "ledger_reference": "General Ledger " + period + ", Meals and Entertainment",
			"direction": "personal_paid_by_business", "purpose_status": "answered", "stated_purpose": o.Purpose, "client_confirmation_reference": "Email from " + c.Contact + " on " + day(nextMonth(period), 4),
			"treatment": "distribution", "business_use_percent": 0, "booked_account": "Meals and Entertainment", "owner_account": "Owner Distributions", "method": "owner_reclass_v1", "treatment_confirmed": true}
		if c.EntityType == "partnership" {
			input["partner"] = c.Contact
		}
		work = append(work, Work{Kind: "owner", File: fmt.Sprintf("owner-reply-%s.txt", period), Text: text, Answer: answer, Confirm: input})
	}
	if c.Reconcile {
		work = append(work, reconciliationWork(c, period, books))
	}
	work = append(work, c.Extra[period]...)
	return work
}

func slug(s string) string {
	return strings.ToLower(strings.NewReplacer(" ", "-", ".", "", ",", "", "&", "and", "'", "").Replace(s))
}

// reconciliationWork is the month's bank statement for Checking and the ledger page for it, prepared by hand as an
// exact match.
func reconciliationWork(c *Client, period string, books Books) Work {
	var statement, ledger []string
	statement = append(statement, "id,date,amount")
	ledger = append(ledger, "id,date,amount")
	opening := books.Opening["10"]
	if opening == nil {
		opening = new(big.Rat)
	}
	closing := books.Closing()["10"]
	for _, t := range books.Txns {
		for _, p := range t.Lines {
			if p.Account != "10" {
				continue
			}
			amount := p.Debit
			if amount == nil {
				amount = new(big.Rat).Neg(p.Credit)
			}
			statement = append(statement, fmt.Sprintf("B%s,%s,%s", t.ID, t.Date, show(amount)))
			ledger = append(ledger, fmt.Sprintf("%s,%s,%s", t.ID, t.Date, show(amount)))
		}
	}
	text := strings.Join(statement, "\n") + "\n"
	input := map[string]any{"account": "Checking", "account_type": "bank", "currency": "USD", "statement_reference": "Riverbend Bank statement " + period + ", account 4471", "ledger_reference": "QuickBooks capture, Checking, " + period,
		"expected_pages": 1, "pages": []int{1}, "statement_opening": show(opening), "statement_closing": show(closing), "ledger_opening": show(opening), "ledger_closing": show(closing),
		"policy_confirmed": true, "reviewed_matches": []any{}, "prior_outstanding": []any{}, "current_outstanding": []any{}, "transfers": []any{}}
	answer := map[string]any{"kind": "unsupported", "summary": "Bank statement export for Checking, " + period, "questions": []string{"This is the Riverbend Bank export for Checking ····4471 for " + period + ". Reconcile it against the captured register on the reconciliation page."}, "facts": []map[string]string{}}
	return Work{Kind: "reconciliation", File: fmt.Sprintf("riverbend-checking-statement-%s.csv", period), Text: text, Answer: answer, Confirm: input}
}

// The clients.

func harbor() Client {
	c := Client{Name: "Harbor Fixture LLC", Industry: "Lighting fixture wholesaler", Contact: "Dana Whitfield", ContactEmail: "dana@harborfixture.example", Since: "2026-06", DueDays: 0, OwnerSlot: 0, EntityType: "s_corp",
		Accounts: chart(retailAccounts...), Revenue: "38000.00", Income: "51", Scenario: Review,
		Opening:   map[string]string{"10": "48210.55", "12": "22400.00", "15": "31500.00", "20": "-16890.20", "42": "-85220.35"},
		Customers: []Party{{"C1", "Brightway Electric Supply", "ap@brightway.example"}, {"C2", "Coastal Builders Group", "accounts@coastalbuilders.example"}, {"C3", "Union Square Hospitality", "finance@usq.example"}, {"C4", "Kessler Interiors", "billing@kessler.example"}},
		Prepaids: []PrepaidSpec{{"2026-06", "Clearbook Software Inc.", "400 Market Street, Suite 900, San Francisco, CA 94111", "CB-77120", "1188.00", 12, "73", "Annual Clearbook Inventory plan"},
			{"2026-07", "Northstar Mutual Insurance", "P.O. Box 4410, Hartford, CT 06101", "NM-2201-26", "4800.00", 12, "72", "Commercial package policy, annual premium"}},
		Notes:  []string{"Dana approves anything over $5,000 herself; the warehouse manager answers inventory questions.", "Inventory counts happen on the last Friday of each month."},
		Budget: true, Goal: true}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		cogs := vary("14200.00", c.Name, period, "cogs")
		return []Txn{
			bill(g, day(period, 4), Party{ID: "V20", Name: "Lumen Components Ltd."}, "LC-"+fmt.Sprint(5100+n*7), cogs, "60", "20", day(nextMonth(period), 4), n == 3),
			billPayment(g, day(period, 28), Party{ID: "V20", Name: "Lumen Components Ltd."}, vary("13900.00", c.Name, period, "cogs-pay"), "10", "20"),
			purchase(g, day(period, 19), Party{ID: "V21", Name: "FreightLine Logistics"}, "Outbound freight", vary("1320.00", c.Name, period, "freight"), "60", "21"),
			purchase(g, day(period, 22), Party{}, "Card purchase 9921", vary("212.35", c.Name, period, "nopayee"), "83", "21"),
		}
	}
	return c
}

func bluegrass() Client {
	c := Client{Name: "Bluegrass Dental PLLC", Industry: "Dental practice", Contact: "Dr. Priya Raman", ContactEmail: "priya@bluegrassdental.example", Since: "2026-06", OwnerSlot: 1, EntityType: "s_corp",
		Accounts: chart(append(payrollAccounts, loanAccounts...)...), Revenue: "61000.00", Income: "50", Scenario: WaitingClient, Routine: true,
		Opening:   map[string]string{"10": "71880.40", "12": "9800.00", "15": "142000.00", "20": "-8120.00", "30": "-48000.00", "42": "-167560.40"},
		Customers: []Party{{"C1", "Delta Dental of Kentucky", "claims@deltaky.example"}, {"C2", "Anthem Blue Cross", "provider@anthem.example"}, {"C3", "Patient receipts", ""}},
		Prepaids:  []PrepaidSpec{{"2026-06", "Curve Dental Software", "1 Curve Way, Orem, UT 84057", "CD-30981", "3588.00", 12, "73", "Curve Hero practice management, annual subscription"}},
		Loan:      &LoanSpec{Lender: "Riverbend Bank", Agreement: "Note 4471-EQ", Account: "Equipment Loan", OpeningPrincipal: "48000.00", Rate: "7.250", Principal: "830.00", Interest: "290.00", PayDay: 15},
		Payroll:   &PayrollSpec{Provider: "Gusto", Gross: "24000.00", EmployeeTaxes: "3600.00", Deductions: "800.00", Net: "19600.00", EmployerTaxes: "1836.00", Contributions: "0.00", PayDays: []int{15, 30}},
		Notes:     []string{"Payroll runs on the 15th and the last day of the month through Gusto.", "The equipment note with Riverbend Bank is paid on the 15th."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		return []Txn{
			bill(g, day(period, 7), Party{ID: "V30", Name: "Henry Schein Dental"}, "HS-"+fmt.Sprint(88100+n*13), vary("4150.00", c.Name, period, "supplies"), "80", "20", day(nextMonth(period), 7), false),
			billPayment(g, day(period, 26), Party{ID: "V30", Name: "Henry Schein Dental"}, vary("4150.00", c.Name, period, "supplies"), "10", "20"),
			purchase(g, day(period, 11), Party{ID: "V31", Name: "Sterling Lab Services"}, "Crowns and bridges", vary("2890.00", c.Name, period, "lab"), "79", "10"),
		}
	}
	return c
}

func cedar() Client {
	c := Client{Name: "Cedar & Pine Coffee Co.", Industry: "Café with two locations", Contact: "Marcus Oyelaran", ContactEmail: "marcus@cedarpine.example", Since: "2026-06", DueDays: 10, OwnerSlot: 0, EntityType: "partnership",
		Accounts: chart(append(retailAccounts, mealsAccounts...)...), Revenue: "6400.00", Income: "51", Scenario: Signed, Reconcile: true,
		Opening:   map[string]string{"10": "23410.18", "15": "64000.00", "20": "-5230.00", "21": "-1840.22", "42": "-80339.96"},
		Customers: []Party{{"C1", "Downtown Alliance (catering)", "events@downtownalliance.example"}, {"C2", "Hollis & Reed LLP (catering)", "office@hollisreed.example"}},
		Owners:    map[string]OwnerSpec{"2026-07": {Payee: "Harvest Table Bistro", Amount: "186.40", Memo: "Dinner", Day: "2026-07-18", Purpose: "a family dinner, personal"}, "2026-09": {Payee: "Alpine Outfitters", Amount: "412.90", Memo: "Camping gear", Day: "2026-09-12", Purpose: "a personal purchase"}},
		Notes:     []string{"Marcus and his partner Lena split distributions evenly; personal card use happens most months.", "Daily sales come in as one Square deposit per location."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		var txns []Txn
		for d := 1; d <= 28; d += 2 {
			txns = append(txns, salesReceipt(g, day(period, d), "Square deposit, Main St", vary("1480.00", c.Name, period, fmt.Sprint(d)), "51", "10"))
			if d%4 == 1 {
				txns = append(txns, salesReceipt(g, day(period, d), "Square deposit, Riverside", vary("960.00", c.Name, period, "r", fmt.Sprint(d)), "51", "10"))
			}
		}
		txns = append(txns, bill(g, day(period, 6), Party{ID: "V40", Name: "Highline Coffee Roasters"}, "HCR-"+fmt.Sprint(2210+n*9), vary("3860.00", c.Name, period, "beans"), "60", "20", day(nextMonth(period), 6), false))
		txns = append(txns, billPayment(g, day(period, 20), Party{ID: "V40", Name: "Highline Coffee Roasters"}, vary("3860.00", c.Name, period, "beans"), "10", "20"))
		txns = append(txns, purchase(g, day(period, 14), Party{ID: "V41", Name: "Sysco"}, "Dairy and pastry", vary("2140.00", c.Name, period, "food"), "60", "21"))
		txns = append(txns, Txn{Kind: "Purchase", ID: g.id(), Date: day(period, 27), Party: "Capital One", PartyID: "V42", Memo: "Card payment", Amount: vary("2900.00", c.Name, period, "cardpay"), Lines: []Posting{{Account: "21", Debit: vary("2900.00", c.Name, period, "cardpay")}, {Account: "10", Credit: vary("2900.00", c.Name, period, "cardpay")}}})
		return txns
	}
	return c
}

func larkspur() Client {
	c := Client{Name: "Larkspur Landscaping Inc.", Industry: "Commercial landscaping", Contact: "Tom Alvarez", ContactEmail: "tom@larkspurlandscaping.example", Since: "2026-06", DueDays: 5, OwnerSlot: 1, EntityType: "s_corp",
		Accounts: chart(append(loanAccounts, fuelAccounts...)...), Revenue: "54000.00", Income: "50", Scenario: Overdue, CashPlan: true,
		Opening:   map[string]string{"10": "19880.72", "12": "41200.00", "15": "188000.00", "20": "-12640.00", "30": "-96000.00", "42": "-140440.72"},
		Customers: []Party{{"C1", "Meadowbrook HOA", "treasurer@meadowbrook.example"}, {"C2", "City of Fairview Parks", "ap@fairview.example"}, {"C3", "Ridgeline Office Park", "pm@ridgeline.example"}, {"C4", "Sunset Plaza Retail", "ap@sunsetplaza.example"}, {"C5", "Greenway Apartments", "office@greenway.example"}},
		Prepaids:  []PrepaidSpec{{"2026-06", "Northstar Mutual Insurance", "P.O. Box 4410, Hartford, CT 06101", "NM-1877-26", "2940.00", 12, "72", "Commercial auto and equipment policy, annual premium"}},
		Loan:      &LoanSpec{Lender: "Deere Financial", Agreement: "Contract 77-2201", Account: "Equipment Loan", OpeningPrincipal: "96000.00", Rate: "5.900", Principal: "1950.00", Interest: "472.00", PayDay: 20},
		Accruals:  map[string]AccrualSpec{"2026-09": {Vendor: "Stonegate Hardscapes", What: "Subcontracted paver installation, Ridgeline Office Park", Amount: "8400.00", Expense: "79", ExpenseName: "Professional Fees"}},
		Extra: map[string][]Work{"2026-09": {{Kind: "unsupported", File: "fairview-parks-contract-amendment.txt", Text: "CITY OF FAIRVIEW\nParks and Recreation Department\n\nAmendment No. 2 to Landscaping Services Agreement 2025-114\nContractor: Larkspur Landscaping Inc.\n\nEffective 2026-09-01 the monthly service fee is amended from $6,200.00 to $6,850.00 to include the Riverside Trail extension. Retainage of 5% applies to each invoice until final acceptance of the trail planting, expected 2026-11-30.\n\nAuthorized by the Director of Parks.\n",
			Answer: map[string]any{"kind": "unsupported", "summary": "Contract amendment with a 5% retainage clause", "questions": []string{"The amendment holds back 5% of each Fairview Parks invoice until trail planting is accepted. Should the retainage be recorded as a separate receivable, and is the 2026-09 invoice already net of it?"}, "facts": []map[string]string{}}}}},
		Notes: []string{"Fairview Parks pays net 45 and always late; do not chase before day 50.", "Trucks and mowers are financed with Deere Financial; the contract has a balloon in 2029."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		return []Txn{
			purchase(g, day(period, 8), Party{ID: "V50", Name: "Shell Fleet"}, "Fleet fuel", vary("3120.00", c.Name, period, "fuel"), "82", "10"),
			purchase(g, day(period, 21), Party{ID: "V50", Name: "Shell Fleet"}, "Fleet fuel", vary("2890.00", c.Name, period, "fuel2"), "82", "10"),
			bill(g, day(period, 10), Party{ID: "V51", Name: "Valley Nursery Wholesale"}, "VN-"+fmt.Sprint(7700+n*21), vary("6380.00", c.Name, period, "plants"), "80", "20", day(nextMonth(period), 10), n == 3),
			purchase(g, day(period, 17), Party{ID: "V52", Name: "Fairview Equipment Repair"}, "Mower repair", vary("640.00", c.Name, period, "repair"), "81", "10"),
			purchase(g, day(period, 24), Party{ID: "V53", Name: "Tractor Supply"}, "Unknown purchase", vary("318.00", c.Name, period, "ama"), "83", "10"),
		}
	}
	return c
}

func meridian() Client {
	c := Client{Name: "Meridian Physical Therapy PC", Industry: "Outpatient physical therapy", Contact: "Dr. Elena Vasquez", ContactEmail: "elena@meridianpt.example", Since: "2026-06", OwnerSlot: 0, EntityType: "c_corp",
		Accounts: chart(payrollAccounts...), Revenue: "47000.00", Income: "50", Scenario: Reopened,
		Opening:   map[string]string{"10": "38440.10", "12": "26100.00", "15": "52000.00", "20": "-4410.00", "42": "-112130.10"},
		Customers: []Party{{"C1", "UnitedHealthcare", "provider@uhc.example"}, {"C2", "Aetna", "claims@aetna.example"}, {"C3", "Medicare Part B", ""}, {"C4", "Patient copays", ""}},
		Prepaids:  []PrepaidSpec{{"2026-06", "HPSO Professional Liability", "159 East County Line Road, Hatboro, PA 19040", "HP-556120", "6000.00", 12, "72", "Professional liability policy, annual premium"}},
		Payroll:   &PayrollSpec{Provider: "ADP", Gross: "31500.00", EmployeeTaxes: "4725.00", Deductions: "1260.00", Net: "25515.00", EmployerTaxes: "2409.75", Contributions: "0.00", PayDays: []int{14, 28}},
		Notes:     []string{"Insurance remittances arrive weekly; the practice manager reconciles them in the EHR."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		return []Txn{
			bill(g, day(period, 9), Party{ID: "V60", Name: "Performance Health"}, "PH-"+fmt.Sprint(33100+n*17), vary("1870.00", c.Name, period, "supplies"), "80", "20", day(nextMonth(period), 9), false),
			billPayment(g, day(period, 29), Party{ID: "V60", Name: "Performance Health"}, vary("1870.00", c.Name, period, "supplies"), "10", "20"),
			purchase(g, day(period, 13), Party{ID: "V61", Name: "WebPT"}, "EHR subscription", vary("549.00", c.Name, period, "ehr"), "73", "10"),
		}
	}
	return c
}

func orchard() Client {
	c := Client{Name: "Orchard Street Books LLC", Industry: "Independent bookstore", Contact: "Naomi Feld", ContactEmail: "naomi@orchardstreetbooks.example", Since: "2026-06", OwnerSlot: 1, EntityType: "partnership",
		Accounts: chart(retailAccounts...), Revenue: "2200.00", Income: "51", Scenario: Uncaptured,
		Opening:   map[string]string{"10": "14220.90", "15": "18000.00", "20": "-9870.00", "21": "-2210.40", "42": "-20140.50"},
		Customers: []Party{{"C1", "Orchard Elementary PTA", "pta@orchardelementary.example"}, {"C2", "Riverside Public Library", "acquisitions@riversidelibrary.example"}},
		Prepaids:  []PrepaidSpec{{"2026-06", "Bookline POS", "22 Chambers Street, New York, NY 10007", "BL-44090", "1440.00", 12, "73", "Bookline point-of-sale, annual plan"}},
		Accruals:  map[string]AccrualSpec{"2026-08": {Vendor: "Metro Power & Light", What: "Electricity, August", Amount: "612.00", Expense: "71", ExpenseName: "Utilities", Reverse: true}},
		Notes:     []string{"Naomi uploads the Square and Bookline exports herself around the 5th."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		var txns []Txn
		for d := 1; d <= 28; d += 3 {
			txns = append(txns, salesReceipt(g, day(period, d), "Square deposit", vary("1120.00", c.Name, period, fmt.Sprint(d)), "51", "10"))
		}
		txns = append(txns, bill(g, day(period, 5), Party{ID: "V70", Name: "Ingram Content Group"}, "IC-"+fmt.Sprint(910200+n*31), vary("7640.00", c.Name, period, "books"), "60", "20", day(nextMonth(period), 5), n == 3))
		txns = append(txns, billPayment(g, day(period, 24), Party{ID: "V70", Name: "Ingram Content Group"}, vary("7400.00", c.Name, period, "bookpay"), "10", "20"))
		txns = append(txns, purchase(g, day(period, 18), Party{ID: "V71", Name: "Penguin Random House"}, "Direct order", vary("1290.00", c.Name, period, "prh"), "60", "21"))
		return txns
	}
	return c
}

func summit() Client {
	c := Client{Name: "Summit Peak Fitness LLC", Industry: "Fitness studio", Contact: "Jordan Blake", ContactEmail: "jordan@summitpeakfitness.example", Since: "2026-06", OwnerSlot: 0, EntityType: "s_corp",
		Accounts: chart(payrollAccounts...), Revenue: "28600.00", Income: "50", Scenario: Signed, Budget: true, CashPlan: true, Publish: true,
		Opening:   map[string]string{"10": "27650.33", "12": "3400.00", "15": "96000.00", "20": "-6180.00", "42": "-120870.33"},
		Customers: []Party{{"C1", "Member dues (ClassPass)", "partners@classpass.example"}, {"C2", "Corporate wellness, Vantage Tech", "benefits@vantagetech.example"}, {"C3", "Personal training packages", ""}},
		Prepaids:  []PrepaidSpec{{"2026-08", "Northstar Mutual Insurance", "P.O. Box 4410, Hartford, CT 06101", "NM-3304-26", "3600.00", 12, "72", "General liability policy, annual premium"}},
		Payroll:   &PayrollSpec{Provider: "Gusto", Gross: "16800.00", EmployeeTaxes: "2520.00", Deductions: "420.00", Net: "13860.00", EmployerTaxes: "1285.20", Contributions: "0.00", PayDays: []int{15, 30}},
		Accruals:  map[string]AccrualSpec{"2026-06": {Vendor: "Metro Power & Light", What: "Electricity and gas, June", Amount: "1340.00", Expense: "71", ExpenseName: "Utilities", Reverse: true}, "2026-07": {Vendor: "Metro Power & Light", What: "Electricity and gas, July", Amount: "1410.00", Expense: "71", ExpenseName: "Utilities", Reverse: true}, "2026-08": {Vendor: "Metro Power & Light", What: "Electricity and gas, August", Amount: "1480.00", Expense: "71", ExpenseName: "Utilities", Reverse: true}, "2026-09": {Vendor: "Metro Power & Light", What: "Electricity and gas, September", Amount: "1290.00", Expense: "71", ExpenseName: "Utilities", Reverse: true}},
		Notes:     []string{"Jordan reads the management pack on the executive page and acknowledges it before the board call on the 20th.", "Utilities are billed a month in arrears, so the agent accrues from Metro's estimate and reverses."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		txns := []Txn{
			purchase(g, day(period, 2), Party{ID: "V80", Name: "Mindbody"}, "Studio software", vary("329.00", c.Name, period, "mindbody"), "73", "10"),
			purchase(g, day(period, 16), Party{ID: "V81", Name: "Rogue Fitness"}, "Equipment and parts", vary("890.00", c.Name, period, "rogue"), "80", "10"),
		}
		if n == 1 {
			txns = append(txns, deposit(g, day(period, 10), "Owner contribution", Money("15000.00"), "40", "10"))
			txns = append(txns, Txn{Kind: "Purchase", ID: g.id(), Date: day(period, 12), Party: "Rogue Fitness", PartyID: "V81", Memo: "Squat racks and platforms", Amount: Money("14200.00"), Lines: []Posting{{Account: "15", Debit: Money("14200.00")}, {Account: "10", Credit: Money("14200.00")}}})
		}
		return txns
	}
	return c
}

func tidewater() Client {
	c := Client{Name: "Tidewater Marine Services Inc.", Industry: "Boat repair and storage", Contact: "Gus Pereira", ContactEmail: "gus@tidewatermarine.example", Since: "2026-06", OwnerSlot: 1, EntityType: "s_corp",
		Accounts: chart(append(loanAccounts, fuelAccounts...)...), Revenue: "33000.00", Income: "50", Scenario: Review,
		Opening:   map[string]string{"10": "16720.44", "12": "18900.00", "15": "240000.00", "20": "-21110.00", "30": "-156000.00", "42": "-98510.44"},
		Customers: []Party{{"C1", "Bayview Yacht Club", "manager@bayviewyc.example"}, {"C2", "Harbor Charters LLC", "ops@harborcharters.example"}, {"C3", "Private owners", ""}},
		Prepaids:  []PrepaidSpec{{"2026-06", "Northstar Mutual Insurance", "P.O. Box 4410, Hartford, CT 06101", "NM-0912-26", "9600.00", 12, "72", "Marina operators policy, annual premium"}},
		Loan:      &LoanSpec{Lender: "Riverbend Bank", Agreement: "Note 8820-TL", Account: "Equipment Loan", OpeningPrincipal: "156000.00", Rate: "6.750", Principal: "2100.00", Interest: "877.50", PayDay: 10, AccrueInterest: true},
		Extra:     map[string][]Work{"2026-09": {{Kind: "unsupported", File: "travelift-invoice-scan.txt", Text: "[Scanned image, 2 pages, 300 dpi. No text layer. File: travelift-invoice-scan.pdf]\n", NoAnswer: true}}},
		Notes:     []string{"Gus sends scans from the yard's copier; ask for the PDF export when the scan has no text."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		return []Txn{
			bill(g, day(period, 6), Party{ID: "V90", Name: "West Marine Pro"}, "WM-"+fmt.Sprint(441000+n*29), vary("5230.00", c.Name, period, "parts"), "80", "20", day(nextMonth(period), 6), n >= 2),
			billPayment(g, day(period, 25), Party{ID: "V90", Name: "West Marine Pro"}, vary("4800.00", c.Name, period, "partspay"), "10", "20"),
			purchase(g, day(period, 12), Party{ID: "V91", Name: "Chesapeake Fuel Dock"}, "Diesel for yard equipment", vary("1180.00", c.Name, period, "fuel"), "82", "10"),
			purchase(g, day(period, 19), Party{ID: "V92", Name: "Mobile Hydraulics Inc."}, "Travelift service", vary("1640.00", c.Name, period, "repair"), "81", "10"),
		}
	}
	return c
}

func juniper() Client {
	return Client{Name: "Juniper Design Studio", Industry: "Brand and web design", Contact: "Sam Okafor", ContactEmail: "sam@juniperdesign.example", Since: "2026-09", OwnerSlot: 0, EntityType: "sole_proprietor",
		Accounts: chart(), Revenue: "18000.00", Income: "50", Scenario: NotOpened,
		Opening:   map[string]string{"10": "9120.00", "12": "12500.00", "42": "-21620.00"},
		Customers: []Party{{"C1", "Alder & Vine Winery", "hello@aldervine.example"}, {"C2", "Northgate Auto Repair", "mike@northgateauto.example"}},
		Notes:     []string{"Signed in September 2026; first month to close is September."}}
}

func northgate() Client {
	c := Client{Name: "Northgate Auto Repair", Industry: "Auto repair shop", Contact: "Mike Sorensen", ContactEmail: "mike@northgateauto.example", Since: "2026-06", LeftIn: "2026-08", OwnerSlot: 1, EntityType: "s_corp",
		Accounts: chart(fuelAccounts...), Revenue: "26000.00", Income: "50", Scenario: Left,
		Opening:   map[string]string{"10": "12980.60", "12": "5400.00", "15": "41000.00", "20": "-7320.00", "42": "-52060.60"},
		Customers: []Party{{"C1", "Fairview Taxi Co-op", "dispatch@fairviewtaxi.example"}, {"C2", "Walk-in customers", ""}},
		Prepaids:  []PrepaidSpec{{"2026-06", "Northstar Mutual Insurance", "P.O. Box 4410, Hartford, CT 06101", "NM-0455-26", "2400.00", 12, "72", "Garage keepers policy, annual premium"}},
		Notes:     []string{"Left for an in-house bookkeeper in August 2026."}}
	c.Monthly = func(c *Client, g *ids, period string, n int) []Txn {
		return []Txn{
			bill(g, day(period, 5), Party{ID: "V95", Name: "NAPA Auto Parts"}, "NAPA-"+fmt.Sprint(20200+n*11), vary("6120.00", c.Name, period, "parts"), "80", "20", day(nextMonth(period), 5), false),
			billPayment(g, day(period, 27), Party{ID: "V95", Name: "NAPA Auto Parts"}, vary("6120.00", c.Name, period, "parts"), "10", "20"),
			purchase(g, day(period, 15), Party{ID: "V96", Name: "Safety-Kleen"}, "Waste oil pickup", vary("240.00", c.Name, period, "waste"), "81", "10"),
		}
	}
	return c
}
