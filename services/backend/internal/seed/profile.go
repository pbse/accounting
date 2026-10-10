package seed

import (
	"fmt"
	"hash/fnv"
	"math/big"
	"strings"
	"time"
)

// Months the demonstration covers: the firm took its clients onto Krith in June 2026; September is the month being
// closed when the seed runs (October 2026).
var Months = []string{"2026-06", "2026-07", "2026-08", "2026-09"}

// Current is the running month, owed but not yet opened.
const Current = "2026-10"

// Party is a vendor or customer.
type Party struct{ ID, Name, Email string }

// Work is one document a client sends and what the agent and the accountant do with it.
type Work struct {
	// Kind is the work the document calls for: prepaid, accrual, loan, payroll, owner, reconciliation or unsupported.
	Kind string
	// File and Text are the document as uploaded.
	File, Text string
	// Answer is the model's recorded reading (a preparation proposal without sources, which the run adds).
	Answer map[string]any
	// NoAnswer leaves the reading unanswered, so the run records a failed reading.
	NoAnswer bool
	// Confirm is the accountant's input when the agent waits for confirmation (accrual) or when the draft is prepared by
	// hand from the document (loan, payroll, owner, reconciliation): the POST input, without entity and close_period.
	Confirm map[string]any
	// Request, when set, is sent to the client instead of confirming: the agent's question needs their answer.
	Request *Request
	// Sources, when set, are the recorded reading's sources: "$primary" is the document itself and "$source:<file>"
	// another of the month's documents.
	Sources []map[string]string
	// Register, for a reconciliation, is the file of the register export that is its ledger side.
	Register string
}

// Request is a question the accountant approves and sends to the client, and the reply when one came.
type Request struct {
	Recipient, Body string
	// Delivered is the secure-channel reference recorded when the request went out; "" leaves it approved but unsent.
	Delivered string
	// Reply is the client's answer, attached as a reply; "" leaves the work waiting on the client.
	Reply, ReplyFrom string
}

// Scenario is where a client's September stands when the seed runs.
type Scenario string

const (
	// Signed: September is signed off like the months before it.
	Signed Scenario = "signed"
	// Review: the agent's drafts wait for the accountant's approval.
	Review Scenario = "review"
	// WaitingClient: a request went to the client and the work waits on their reply.
	WaitingClient Scenario = "waiting_client"
	// Overdue: the close is past its due date with a question still open.
	Overdue Scenario = "overdue"
	// Reopened: the month was signed off, then a document was replaced, so the sign-off no longer stands.
	Reopened Scenario = "reopened"
	// Uncaptured: the month is open with documents in, but its books were never captured.
	Uncaptured Scenario = "uncaptured"
	// NotOpened: the client is on the roster and owes the month, but nobody opened it.
	NotOpened Scenario = "not_opened"
	// Left: the client left the firm; its last months are signed off and the roster marks it inactive.
	Left Scenario = "left"
)

// Client is one of the firm's clients: its books and its months on Krith.
type Client struct {
	Name, Industry, Contact, ContactEmail string
	// Since is the first month the client owes a close; LeftIn the month the roster marked it inactive, or "".
	Since, LeftIn string
	// DueDays overrides the firm's template; 0 keeps the template's.
	DueDays int
	// OwnerSlot picks the member who owns the client's closes, cycling through the firm's members.
	OwnerSlot int
	// EntityType is the client's legal form, for owner items.
	EntityType string
	Accounts   []Account
	Opening    map[string]string
	Vendors    []Party
	Customers  []Party
	// Revenue is the typical monthly billing across the customers; Income the account it lands in. LatePayer is the
	// customer who pays late (net 45 and always behind); the last customer when unnamed.
	Revenue, Income, LatePayer string
	// Prepaids, Loan, Payroll, Accruals, Owners and Reconcile describe the client's recurring work; Extra adds the
	// documents a month brings that fit none of them.
	Prepaids  []PrepaidSpec
	Loan      *LoanSpec
	Payroll   *PayrollSpec
	Accruals  map[string]AccrualSpec
	Owners    map[string]OwnerSpec
	Reconcile bool
	Extra     map[string][]Work
	// Monthly adds a business's own transactions beyond the recurring ones.
	Monthly func(c *Client, g *ids, period string, n int) []Txn
	// Scenario is where September stands.
	Scenario Scenario
	// Budget, CashPlan and Publish turn on the planning features for the client.
	Budget, CashPlan, Publish bool
	// Goal hands September to the agent; Routine does so for every month.
	Goal, Routine bool
	// Notes are what the firm remembers about the client (the close's memory).
	Notes []string
}

// Firm is the demonstration firm.
type Firm struct {
	Name     string
	Clients  []Client
	Template struct{ DueDays int }
}

// seq gives a deterministic small variation for amounts: the same client, period and label always vary the same way.
func seq(parts ...string) int {
	h := fnv.New32a()
	h.Write([]byte(strings.Join(parts, "|")))
	return int(h.Sum32() % 1000)
}

// vary moves an amount by up to ±12 % deterministically, to whole cents.
func vary(amount string, parts ...string) *big.Rat {
	base := Money(amount)
	pct := (seq(parts...) % 25) - 12
	factor := big.NewRat(int64(100+pct), 100)
	v := new(big.Rat).Mul(base, factor)
	cents, _ := new(big.Rat).Mul(v, big.NewRat(100, 1)).Float64()
	return big.NewRat(int64(cents+0.5), 100)
}

func day(period string, d int) string {
	t, _ := time.Parse("2006-01", period)
	last := t.AddDate(0, 1, -1).Day()
	if d > last {
		d = last
	}
	return fmt.Sprintf("%s-%02d", period, d)
}

func nextMonth(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, 1, 0).Format("2006-01")
}

func monthIndex(period string) int {
	for i, m := range Months {
		if m == period {
			return i
		}
	}
	return -1
}

// ids hands out transaction IDs unique within a client, in the QuickBooks range a small company shows.
type ids struct{ next int }

func (g *ids) id() string { g.next++; return fmt.Sprint(g.next) }

// Builders for the transactions a month is made of.

func bill(g *ids, date string, vendor Party, number string, amount *big.Rat, expense, ap string, due string, open bool) Txn {
	t := Txn{Kind: "Bill", ID: g.id(), Date: date, Number: number, Party: vendor.Name, PartyID: vendor.ID, Amount: amount, DueDate: due,
		Lines: []Posting{{Account: expense, Debit: amount}, {Account: ap, Credit: amount}}}
	if open {
		t.Balance = amount
	}
	return t
}

func purchase(g *ids, date string, vendor Party, memo string, amount *big.Rat, expense, bank string) Txn {
	return Txn{Kind: "Purchase", ID: g.id(), Date: date, Number: "", Party: vendor.Name, PartyID: vendor.ID, Memo: memo, Amount: amount,
		Lines: []Posting{{Account: expense, Debit: amount}, {Account: bank, Credit: amount}}}
}

func invoice(g *ids, date string, customer Party, number string, amount *big.Rat, income, ar, due string, open *big.Rat) Txn {
	return Txn{Kind: "Invoice", ID: g.id(), Date: date, Number: number, Party: customer.Name, PartyID: customer.ID, Amount: amount, DueDate: due, Balance: open,
		Lines: []Posting{{Account: ar, Debit: amount}, {Account: income, Credit: amount}}}
}

func payment(g *ids, date string, customer Party, amount *big.Rat, bank, ar string) Txn {
	return Txn{Kind: "Payment", ID: g.id(), Date: date, Party: customer.Name, PartyID: customer.ID, Amount: amount,
		Lines: []Posting{{Account: bank, Debit: amount}, {Account: ar, Credit: amount}}}
}

func billPayment(g *ids, date string, vendor Party, amount *big.Rat, bank, ap string) Txn {
	return Txn{Kind: "BillPayment", ID: g.id(), Date: date, Party: vendor.Name, PartyID: vendor.ID, Amount: amount,
		Lines: []Posting{{Account: ap, Debit: amount}, {Account: bank, Credit: amount}}}
}

func salesReceipt(g *ids, date string, memo string, amount *big.Rat, income, bank string) Txn {
	return Txn{Kind: "SalesReceipt", ID: g.id(), Date: date, Memo: memo, Amount: amount,
		Lines: []Posting{{Account: bank, Debit: amount}, {Account: income, Credit: amount}}}
}

func deposit(g *ids, date string, memo string, amount *big.Rat, from, bank string) Txn {
	return Txn{Kind: "Deposit", ID: g.id(), Date: date, Memo: memo, Amount: amount,
		Lines: []Posting{{Account: bank, Debit: amount}, {Account: from, Credit: amount}}}
}

func journal(g *ids, date, memo string, lines ...Posting) Txn {
	total := new(big.Rat)
	for _, l := range lines {
		if l.Debit != nil {
			total.Add(total, l.Debit)
		}
	}
	return Txn{Kind: "JournalEntry", ID: g.id(), Date: date, Memo: memo, Amount: total, Lines: lines}
}

// The chart every client shares, with the accounts a particular business adds.
func chart(extra ...Account) []Account {
	base := []Account{
		{"10", "Checking", "Bank", "Checking"},
		{"12", "Accounts Receivable", "Accounts Receivable", "AccountsReceivable"},
		{"13", "Prepaid Expenses", "Other Current Asset", "PrepaidExpenses"},
		{"15", "Equipment", "Fixed Asset", "MachineryAndEquipment"},
		{"20", "Accounts Payable", "Accounts Payable", "AccountsPayable"},
		{"22", "Accrued Liabilities", "Other Current Liability", "OtherCurrentLiabilities"},
		{"40", "Owner Contributions", "Equity", "OwnersEquity"},
		{"41", "Owner Distributions", "Equity", "OwnersEquity"},
		{"42", "Retained Earnings", "Equity", "RetainedEarnings"},
		{"50", "Service Revenue", "Income", "ServiceFeeIncome"},
		{"70", "Rent", "Expense", "RentOrLeaseOfBuildings"},
		{"71", "Utilities", "Expense", "Utilities"},
		{"72", "Insurance", "Expense", "Insurance"},
		{"73", "Software Subscriptions", "Expense", "OfficeGeneralAdministrativeExpenses"},
		{"78", "Advertising", "Expense", "AdvertisingPromotional"},
		{"79", "Professional Fees", "Expense", "LegalProfessionalFees"},
		{"80", "Office Supplies", "Expense", "OfficeGeneralAdministrativeExpenses"},
		{"83", "Ask My Accountant", "Expense", "OtherMiscellaneousServiceCost"},
	}
	return append(base, extra...)
}

var (
	payrollAccounts = []Account{{"23", "Payroll Liabilities", "Other Current Liability", "PayrollTaxPayable"}, {"24", "Payroll Clearing", "Other Current Liability", "OtherCurrentLiabilities"}, {"74", "Wages", "Expense", "PayrollExpenses"}, {"75", "Payroll Taxes", "Expense", "PayrollTaxExpenses"}}
	loanAccounts    = []Account{{"30", "Equipment Loan", "Long Term Liability", "NotesPayable"}, {"76", "Interest Expense", "Expense", "InterestPaid"}, {"26", "Accrued Interest", "Other Current Liability", "OtherCurrentLiabilities"}}
	mealsAccounts   = []Account{{"77", "Meals and Entertainment", "Expense", "EntertainmentMeals"}}
	retailAccounts  = []Account{{"51", "Product Sales", "Income", "SalesOfProductIncome"}, {"60", "Cost of Goods Sold", "Cost of Goods Sold", "SuppliesMaterialsCogs"}, {"21", "Business Credit Card", "Credit Card", "CreditCard"}}
	fuelAccounts    = []Account{{"81", "Repairs and Maintenance", "Expense", "RepairMaintenance"}, {"82", "Fuel", "Expense", "Auto"}}
)

var common = []Party{{"V1", "Lakeside Property Management", ""}, {"V2", "Metro Power & Light", ""}, {"V3", "Northstar Mutual Insurance", ""}, {"V4", "Clearbook Software Inc.", ""}, {"V5", "Pinecone Marketing", ""}, {"V6", "Office Depot", ""}, {"V7", "Hale & Porter CPAs", ""}, {"V8", "Gusto", ""}, {"V9", "Riverbend Bank", ""}, {"V10", "Amazon Business", ""}}

func vendor(id string) Party {
	for _, v := range common {
		if v.ID == id {
			return v
		}
	}
	return Party{ID: id, Name: "Vendor " + id}
}

// recurring is the activity most small businesses show every month: rent, utilities, software, supplies, a few
// customer invoices and their payments. revenue is the typical monthly billing; customers are billed in rotation.
func recurring(c *Client, g *ids, period string, revenue string, customers []Party, income string) []Txn {
	var txns []Txn
	rentDue := day(nextMonth(period), 1)
	rent := vary("2400.00", c.Name, "rent")
	txns = append(txns, bill(g, day(period, 1), vendor("V1"), "RENT-"+period, rent, "70", "20", rentDue, false))
	txns = append(txns, billPayment(g, day(period, 3), vendor("V1"), rent, "10", "20"))
	utilities := vary("410.00", c.Name, period, "utilities")
	txns = append(txns, bill(g, day(period, 12), vendor("V2"), fmt.Sprintf("MPL-%s-%03d", strings.ReplaceAll(period, "-", ""), seq(c.Name)%900+100), utilities, "71", "20", day(period, 27), false))
	txns = append(txns, billPayment(g, day(period, 25), vendor("V2"), utilities, "10", "20"))
	txns = append(txns, purchase(g, day(period, 5), vendor("V4"), "Monthly subscription", vary("189.00", c.Name, "software"), "73", "10"))
	txns = append(txns, purchase(g, day(period, 9), vendor("V6"), "Office supplies", vary("86.50", c.Name, period, "supplies"), "80", "10"))
	if seq(c.Name, period, "ads")%3 != 0 {
		// Due next month, so open at month-end and paid then.
		txns = append(txns, bill(g, day(period, 15), vendor("V5"), "PM-"+fmt.Sprint(seq(c.Name, period)%9000+1000), vary("650.00", c.Name, period, "ads"), "78", "20", day(nextMonth(period), 15), true))
	}
	// Invoices: each customer is billed once a month; most pay within the month, one pays late, one stays open.
	n := len(customers)
	for i, cust := range customers {
		amount := vary(revenue, c.Name, cust.Name, period)
		if n > 0 {
			amount = new(big.Rat).Mul(amount, big.NewRat(1, int64(n)))
			amount = big.NewRat(amount.Num().Int64()/amount.Denom().Int64()*100/100, 1)
		}
		issued := day(period, 2+i*3)
		due := day(nextMonth(period), 2+i*3)
		if cust.Name == c.LatePayer {
			// Net 45, and paid after that.
			t, _ := time.Parse("2006-01-02", issued)
			due = t.AddDate(0, 0, 45).Format("2006-01-02")
		}
		var open *big.Rat
		switch {
		case cust.Name == c.LatePayer || (i+seq(c.Name, period))%4 == 0:
			open = amount // still open at month-end; paid in a later month or overdue
			txns = append(txns, invoice(g, issued, cust, fmt.Sprintf("INV-%s-%02d", strings.ReplaceAll(period[2:], "-", ""), i+1), amount, income, "12", due, open))
		default:
			txns = append(txns, invoice(g, issued, cust, fmt.Sprintf("INV-%s-%02d", strings.ReplaceAll(period[2:], "-", ""), i+1), amount, income, "12", due, nil))
			txns = append(txns, payment(g, day(period, 16+i*2), cust, amount, "10", "12"))
		}
	}
	return txns
}

// settleOpenInvoices pays the invoices and bills the prior month left open, except the customer who is always late,
// and says which it paid; the prior month's records stay as they were.
func settleOpenInvoices(g *ids, period string, prior []Txn, late string, settled map[string]bool) ([]Txn, map[string]bool) {
	var txns []Txn
	paid := map[string]bool{}
	for id := range settled {
		paid[id] = true
	}
	for _, t := range prior {
		if paid[t.ID] || t.Balance == nil {
			continue
		}
		switch {
		case t.Kind == "Invoice" && t.Party != late:
			txns = append(txns, payment(g, day(period, 6), Party{ID: t.PartyID, Name: t.Party}, t.Balance, "10", "12"))
			paid[t.ID] = true
		case t.Kind == "Bill":
			// The firm's own bills are paid on their due date, in the month after they were booked.
			txns = append(txns, billPayment(g, day(period, 15), Party{ID: t.PartyID, Name: t.Party}, t.Balance, "10", "20"))
			paid[t.ID] = true
		}
	}
	return txns, paid
}

// Rent is the client's monthly rent as its books record it.
func (c *Client) Rent() string { return show(vary("2400.00", c.Name, "rent")) }

// Build makes the firm's profile.
func Build() Firm {
	f := Firm{Name: "Demonstration firm"}
	f.Template.DueDays = 15
	f.Clients = []Client{harbor(), bluegrass(), cedar(), larkspur(), meridian(), orchard(), summit(), tidewater(), juniper(), northgate()}
	return f
}

// BooksFor builds the client's books for every month through last, carrying balances and the approved adjustments
// forward. The returned slice is indexed like Months.
func BooksFor(c *Client, last string) []Books {
	var out []Books
	opening := map[string]*big.Rat{}
	for id, v := range c.Opening {
		opening[id] = Money(v)
	}
	var priorTxns, posted []Txn
	settled := map[string]bool{}
	for n, period := range Months {
		b := Books{Period: period, Currency: "USD", Accounts: c.Accounts, Opening: opening, Posted: posted}
		g := &ids{next: 1000 + n*200}
		txns := c.TxnsFor(g, period, n)
		if len(priorTxns) > 0 {
			late := c.LatePayer
			if late == "" && len(c.Customers) > 0 {
				late = c.Customers[len(c.Customers)-1].Name
			}
			var payments []Txn
			payments, settled = settleOpenInvoices(g, period, priorTxns, late, settled)
			txns = append(payments, txns...)
		}
		b.Settled = settled
		for i := range txns {
			if txns[i].ID == "" {
				txns[i].ID = g.id()
			}
			txns[i].Currency = "USD"
		}
		b.Txns = txns
		b.Sort()
		out = append(out, b)
		if period == last {
			break
		}
		// The capture is taken before the firm's entries are posted, as the product captures a month; once the month is
		// signed off, its approved entries are posted by the client's bookkeeper dated month-end, so the next month
		// opens on this closing plus them, and its transaction capture reads those postings back (Posted).
		closing := b.Closing()
		adjust := Books{Period: period, Currency: "USD", Accounts: c.Accounts, Opening: closing, Txns: c.AdjustFor(&ids{next: 9000 + n*100}, period)}
		for i := range adjust.Txns {
			adjust.Txns[i].Currency = "USD"
		}
		opening = adjust.Closing()
		posted = adjust.Txns
		priorTxns = b.Txns
	}
	return out
}

// Invoice text shared by the prepaid documents.
func invoiceText(vendor, address, number, date, billTo, lines, total, terms string) string {
	return fmt.Sprintf(`%s
%s

INVOICE %s
Invoice date: %s
Bill to: %s

%s

Total due: USD %s
%s
`, vendor, address, number, date, billTo, lines, total, terms)
}

func prepaidAnswer(vendor, billedTo, number, amount, start, end, quoteAmount, quotePeriod string) map[string]any {
	return map[string]any{"kind": "prepaid", "summary": "Prepaid " + strings.ToLower(vendor) + " schedule", "questions": []string{},
		"facts": cited([]map[string]string{
			{"field": "amount", "value": amount, "quote": quoteAmount},
			{"field": "currency", "value": "USD", "quote": quoteAmount},
			{"field": "service_start", "value": start, "quote": quotePeriod},
			{"field": "service_end", "value": end, "quote": quotePeriod},
			{"field": "vendor", "value": vendor, "quote": vendor + "\n"},
			{"field": "billed_to", "value": billedTo, "quote": "Bill to: " + billedTo},
			{"field": "invoice_number", "value": number, "quote": "INVOICE " + number},
		})}
}

func accrualAnswer(summary, amount, start, end, quoteAmount, quotePeriod, expense, liability, booked string) map[string]any {
	return map[string]any{"kind": "accrual", "summary": summary, "questions": []string{},
		"facts": cited([]map[string]string{
			{"field": "amount", "value": amount, "quote": quoteAmount},
			{"field": "currency", "value": "USD", "quote": quoteAmount},
			{"field": "service_start", "value": start, "quote": quotePeriod},
			{"field": "service_end", "value": end, "quote": quotePeriod},
			{"field": "expense_account", "value": expense, "quote": "Expense account: " + expense},
			{"field": "liability_account", "value": liability, "quote": "Liability account: " + liability},
			{"field": "booked_balance", "value": booked, "quote": "Booked balance: " + booked},
		})}
}

// cited marks every fact as quoted from the primary document.
func cited(facts []map[string]string) []map[string]string {
	for _, f := range facts {
		f["source_id"] = "$primary"
	}
	return facts
}

func accrualConfirm(amount, start, end, expense, liability, booked, basis string, reverse bool) map[string]any {
	return map[string]any{"source_reference": "Vendor estimate", "service_start": start, "service_end": end, "amount": amount, "booked_balance": booked, "booking_reference": "Trial Balance, " + liability, "estimation_basis": basis,
		"expense_account": expense, "liability_account": liability, "currency": "USD", "basis": "accrual", "method": "fixed_incurred_amount_v1", "method_confirmed": true, "booking_confirmed": true, "examples_confirmed": true, "reverse_next_month": reverse}
}

// prepaidWork is an annual policy or subscription invoiced in a month: the bill is posted to the expense account and
// the agent moves the unexpired part to prepaid.
func prepaidWork(c *Client, vendorName, address, number, period string, amount string, months int, what, expense string) Work {
	start, end := monthStart(period), ""
	t, _ := time.Parse("2006-01", period)
	end = t.AddDate(0, months, -1).Format("2006-01-02")
	quoteAmount := fmt.Sprintf("%s: USD %s", what, amount)
	quotePeriod := fmt.Sprintf("Coverage period %s to %s", start, end)
	text := invoiceText(vendorName, address, number, day(period, 3), c.Name, quoteAmount+"\n"+quotePeriod, amount, "Payment terms: net 30. Thank you for your business.")
	confirm := map[string]any{"amount": amount, "service_start": start, "service_end": end, "currency": "USD", "basis": "accrual", "expense_account": expense, "prepaid_account": "Prepaid Expenses", "method": "equal_full_months_v1", "method_confirmed": true, "booking_confirmed": true}
	return Work{Kind: "prepaid", File: strings.ToLower(strings.ReplaceAll(vendorName, " ", "-")) + "-" + number + ".txt", Text: text, Answer: prepaidAnswer(vendorName, c.Name, number, amount, start, end, quoteAmount, quotePeriod), Confirm: confirm}
}

// ExpenseName is the name of an expense account in the shared chart.
func ExpenseName(code string) string {
	for _, a := range chart() {
		if a.ID == code {
			return a.Name
		}
	}
	return ""
}

// prepaidBill is the bill that goes with prepaidWork, posted to the expense account as the client's bookkeeper did.
func prepaidBill(g *ids, c *Client, period, vendorName, number, amount, expense string) Txn {
	v := Party{ID: "P" + fmt.Sprint(seq(vendorName)%90+10), Name: vendorName}
	return bill(g, day(period, 3), v, number, Money(amount), expense, "20", day(nextMonth(period), 3), false)
}

// prepaidAdjust is the approved entry for a prepaid: in the invoice month it moves the unexpired part to prepaid; in a
// later month it expenses one month.
func prepaidAdjust(g *ids, period, invoiceMonth, amount string, months int, expense string) Txn {
	total := Money(amount)
	monthly := new(big.Rat).Quo(total, big.NewRat(int64(months), 1))
	booked := monthEnd(period)
	if period == invoiceMonth {
		move := sub(total, monthly)
		return journal(g, booked, "Close adjustment "+period+": prepaid, unexpired portion", Posting{Account: "13", Debit: move}, Posting{Account: expense, Credit: move})
	}
	return journal(g, booked, "Close adjustment "+period+": prepaid, monthly expense", Posting{Account: expense, Debit: monthly}, Posting{Account: "13", Credit: monthly})
}

func monthsBetween(from, to string) int {
	a, _ := time.Parse("2006-01", from)
	b, _ := time.Parse("2006-01", to)
	return (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month())
}
