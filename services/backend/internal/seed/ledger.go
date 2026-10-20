// Package seed describes a realistic small accounting firm, its clients and four months of their books, documents and
// decisions, for a demonstration database (docs/seed-data.md). Nothing here touches a database: transport.SeedFirm
// writes the profile through the same routes a person uses, and the capture and model-answer paths the acceptance run
// uses, so every row carries the relationships the product keeps for real work.
package seed

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"
)

// Account is one ledger account as QuickBooks lists it: the type and subtype decide how coverage, the management pack
// and the tie-out treat it.
type Account struct {
	ID, Name, Type, Subtype string
}

// Classification is QuickBooks' classification for an account type.
func (a Account) Classification() string {
	switch a.Type {
	case "Bank", "Accounts Receivable", "Other Current Asset", "Fixed Asset", "Other Asset":
		return "Asset"
	case "Accounts Payable", "Credit Card", "Other Current Liability", "Long Term Liability":
		return "Liability"
	case "Equity":
		return "Equity"
	case "Income", "Other Income":
		return "Revenue"
	default:
		return "Expense"
	}
}

// Posting is one line of a transaction: a debit or credit to an account.
type Posting struct {
	Account string
	Debit   *big.Rat
	Credit  *big.Rat
}

// Txn is one ledger transaction as the capture keeps it, with the postings that explain it.
type Txn struct {
	Kind     string // Bill, Purchase, Invoice, Payment, BillPayment, Deposit, SalesReceipt, JournalEntry, VendorCredit, CreditMemo
	ID       string
	Date     string
	Number   string
	Party    string // vendor or customer name; "" for a purchase with no payee
	PartyID  string
	Memo     string
	Amount   *big.Rat // the transaction's total
	Lines    []Posting
	DueDate  string   // invoices and bills
	Balance  *big.Rat // what is still open on an invoice or bill at capture time; nil when settled
	Holding  bool     // posted to a holding account such as Ask My Accountant
	Currency string
}

// Money parses a decimal amount.
func Money(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(strings.ReplaceAll(s, ",", ""))
	if !ok {
		panic("seed: bad amount " + s)
	}
	return r
}

func show(r *big.Rat) string { return r.FloatString(2) }

func add(a, b *big.Rat) *big.Rat { return new(big.Rat).Add(a, b) }
func sub(a, b *big.Rat) *big.Rat { return new(big.Rat).Sub(a, b) }

// Books is one month of a client's ledger: opening balances, the month's transactions and the derived reports.
type Books struct {
	Period   string
	Currency string
	Accounts []Account
	Opening  map[string]*big.Rat // net (debit positive) balance by account at the start of the month
	Txns     []Txn
	// Posted are the previous month's approved close entries, posted by the client's bookkeeper after that month's
	// capture and dated its month-end: already in Opening, and read back by this month's transaction capture, which
	// picks up every transaction changed since the last one.
	Posted []Txn
	// Settled are the earlier months' invoices paid by the end of this month, which its open items leave out.
	Settled map[string]bool
}

// PostedRecords are the previous month's close entries as the ledger returns them once posted: the readback the
// bookkeeper's posting leaves, kept with the month that opens on them.
func (b Books) PostedRecords() []json.RawMessage {
	var out []json.RawMessage
	for _, t := range b.Posted {
		out = append(out, b.Record(t))
	}
	return out
}

// reportedKind is the label the TransactionList report gives a transaction type.
var reportedKind = map[string]string{"BillPayment": "Bill Payment (Check)", "Purchase": "Expense", "JournalEntry": "Journal Entry", "SalesReceipt": "Sales Receipt", "VendorCredit": "Vendor Credit", "CreditMemo": "Credit Memo", "RefundReceipt": "Refund"}

func reportedLabel(kind string) string {
	if label, ok := reportedKind[kind]; ok {
		return label
	}
	return kind
}

// Closing is the net balance by account at the end of the month.
func (b Books) Closing() map[string]*big.Rat {
	out := map[string]*big.Rat{}
	for id, v := range b.Opening {
		out[id] = new(big.Rat).Set(v)
	}
	for _, t := range b.Txns {
		for _, p := range t.Lines {
			if out[p.Account] == nil {
				out[p.Account] = new(big.Rat)
			}
			if p.Debit != nil {
				out[p.Account] = add(out[p.Account], p.Debit)
			}
			if p.Credit != nil {
				out[p.Account] = sub(out[p.Account], p.Credit)
			}
		}
	}
	return out
}

// Activity is the month's net movement by account.
func (b Books) Activity() map[string]*big.Rat {
	out := map[string]*big.Rat{}
	for _, t := range b.Txns {
		for _, p := range t.Lines {
			if out[p.Account] == nil {
				out[p.Account] = new(big.Rat)
			}
			if p.Debit != nil {
				out[p.Account] = add(out[p.Account], p.Debit)
			}
			if p.Credit != nil {
				out[p.Account] = sub(out[p.Account], p.Credit)
			}
		}
	}
	return out
}

func (b Books) account(id string) Account {
	for _, a := range b.Accounts {
		if a.ID == id {
			return a
		}
	}
	return Account{ID: id, Name: "Account " + id}
}

func monthEnd(period string) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, 1, -1).Format("2006-01-02")
}

func monthStart(period string) string { return period + "-01" }

// Directory is the chart of accounts as the capture stores it.
func (b Books) Directory() json.RawMessage {
	rows := []map[string]any{}
	closing := b.Closing()
	for _, a := range b.Accounts {
		balance := "0.00"
		if v := closing[a.ID]; v != nil {
			balance = show(v)
		}
		rows = append(rows, map[string]any{"Id": a.ID, "Name": a.Name, "FullyQualifiedName": a.Name, "Classification": a.Classification(), "AccountType": a.Type, "AccountSubType": a.Subtype, "CurrentBalance": json.Number(balance), "Active": true, "CurrencyRef": map[string]string{"value": b.Currency}})
	}
	raw, _ := json.Marshal(rows)
	return raw
}

func header(name, period, currency string) map[string]any {
	return map[string]any{"ReportName": name, "StartPeriod": monthStart(period), "EndPeriod": monthEnd(period), "ReportBasis": "Accrual", "Currency": currency, "Time": monthEnd(period) + "T23:59:00Z", "Option": []map[string]string{{"Name": "NoReportData", "Value": "false"}}}
}

// TrialBalance is the month-end Trial Balance in QuickBooks' report shape.
func (b Books) TrialBalance() json.RawMessage {
	closing := b.Closing()
	rows := []map[string]any{}
	var debits, credits big.Rat
	for _, a := range b.Accounts {
		v := closing[a.ID]
		if v == nil || v.Sign() == 0 {
			if a.Type == "Income" || a.Type == "Expense" || a.Type == "Cost of Goods Sold" {
				continue
			}
			v = new(big.Rat)
		}
		debit, credit := "", ""
		if v.Sign() >= 0 {
			debit = show(v)
			debits.Add(&debits, v)
		} else {
			credit = show(new(big.Rat).Neg(v))
			credits.Sub(&credits, v)
		}
		rows = append(rows, map[string]any{"type": "Data", "ColData": []map[string]string{{"id": a.ID, "value": a.Name}, {"value": debit}, {"value": credit}}})
	}
	rows = append(rows, map[string]any{"type": "Section", "group": "GrandTotal", "Summary": map[string]any{"ColData": []map[string]string{{"value": "TOTAL"}, {"value": debits.FloatString(2)}, {"value": credits.FloatString(2)}}}})
	report := map[string]any{"Header": header("TrialBalance", b.Period, b.Currency), "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "", "ColType": "Account"}, {"ColTitle": "Debit", "ColType": "Money"}, {"ColTitle": "Credit", "ColType": "Money"}}}, "Rows": map[string]any{"Row": rows}}
	raw, _ := json.Marshal(report)
	return raw
}

// ProfitAndLoss is the month's single-period Profit and Loss: each income and expense account's amount as the report
// shows it (income positive, expenses positive), in sections with their totals.
func (b Books) ProfitAndLoss() json.RawMessage {
	activity := b.Activity()
	section := func(name string, types []string, sign int) (map[string]any, *big.Rat) {
		rows := []map[string]any{}
		total := new(big.Rat)
		for _, a := range b.Accounts {
			ok := false
			for _, t := range types {
				ok = ok || a.Type == t
			}
			v := activity[a.ID]
			if !ok || v == nil || v.Sign() == 0 {
				continue
			}
			shown := new(big.Rat).Set(v)
			if sign < 0 {
				shown.Neg(shown)
			}
			total.Add(total, shown)
			rows = append(rows, map[string]any{"type": "Data", "ColData": []map[string]string{{"id": a.ID, "value": a.Name}, {"value": show(shown)}}})
		}
		return map[string]any{"type": "Section", "group": strings.ReplaceAll(name, " ", ""), "Header": map[string]any{"ColData": []map[string]string{{"value": name}, {"value": ""}}}, "Rows": map[string]any{"Row": rows}, "Summary": map[string]any{"ColData": []map[string]string{{"value": "Total " + name}, {"value": show(total)}}}}, total
	}
	income, totalIncome := section("Income", []string{"Income"}, -1)
	cogs, totalCogs := section("Cost of Goods Sold", []string{"Cost of Goods Sold"}, 1)
	expenses, totalExpenses := section("Expenses", []string{"Expense"}, 1)
	other, totalOther := section("Other Income", []string{"Other Income"}, -1)
	net := sub(sub(sub(totalIncome, totalCogs), totalExpenses), new(big.Rat).Neg(totalOther))
	rows := []map[string]any{income, cogs, expenses, other,
		{"type": "Section", "group": "NetIncome", "Summary": map[string]any{"ColData": []map[string]string{{"value": "Net Income"}, {"value": show(net)}}}}}
	report := map[string]any{"Header": header("ProfitAndLoss", b.Period, b.Currency), "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "", "ColType": "Account"}, {"ColTitle": "Total", "ColType": "Money"}}}, "Rows": map[string]any{"Row": rows}}
	raw, _ := json.Marshal(report)
	return raw
}

// NetIncome is the month's profit.
func (b Books) NetIncome() *big.Rat {
	activity := b.Activity()
	net := new(big.Rat)
	for _, a := range b.Accounts {
		v := activity[a.ID]
		if v == nil {
			continue
		}
		switch a.Type {
		case "Income", "Other Income":
			net.Sub(net, v)
		case "Expense", "Cost of Goods Sold", "Other Expense":
			net.Sub(net, v)
		}
	}
	return net
}

// GeneralLedger lists each account's transactions for the month under an account header, with the running balance
// the ledger-line reader needs and a Name column for the payee, as the flux and the ledger search read it.
func (b Books) GeneralLedger() json.RawMessage {
	type line struct {
		date, kind, id, num, party, partyID, memo string
		amount                                    *big.Rat
	}
	byAccount := map[string][]line{}
	for _, t := range b.Txns {
		for _, p := range t.Lines {
			amount := p.Debit
			if amount == nil {
				amount = new(big.Rat).Neg(p.Credit)
			}
			byAccount[p.Account] = append(byAccount[p.Account], line{t.Date, t.Kind, t.ID, t.Number, t.Party, t.PartyID, t.Memo, amount})
		}
	}
	rows := []map[string]any{}
	closing := b.Closing()
	cells := func(date, kind, id, num, party, partyID, memo, amount, balance string) []map[string]string {
		return []map[string]string{{"value": date}, {"value": kind, "id": id}, {"value": num}, {"value": party, "id": partyID}, {"value": memo}, {"value": amount}, {"value": balance}}
	}
	for _, a := range b.Accounts {
		lines := byAccount[a.ID]
		if len(lines) == 0 {
			continue
		}
		opening := b.Opening[a.ID]
		if opening == nil {
			opening = new(big.Rat)
		}
		running := new(big.Rat).Set(opening)
		section := []map[string]any{{"type": "Data", "ColData": cells("", "", "", "", "", "", "Beginning Balance", "", show(opening))}}
		for _, l := range lines {
			running = add(running, l.amount)
			section = append(section, map[string]any{"type": "Data", "ColData": cells(l.date, l.kind, l.id, l.num, l.party, l.partyID, l.memo, show(l.amount), show(running))})
		}
		rows = append(rows, map[string]any{"type": "Section", "Header": map[string]any{"ColData": []map[string]string{{"value": a.Name, "id": a.ID}}},
			"Rows": map[string]any{"Row": section}, "Summary": map[string]any{"ColData": cells("", "", "", "", "", "Total for "+a.Name, "", show(sub(closing[a.ID], opening)), show(closing[a.ID]))}})
	}
	report := map[string]any{"Header": header("GeneralLedger", b.Period, b.Currency), "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "Date", "ColType": "Date"}, {"ColTitle": "Transaction Type", "ColType": "String"}, {"ColTitle": "Num", "ColType": "String"}, {"ColTitle": "Name", "ColType": "String"}, {"ColTitle": "Memo/Description", "ColType": "String"}, {"ColTitle": "Amount", "ColType": "Money"}, {"ColTitle": "Balance", "ColType": "Money"}}}, "Rows": map[string]any{"Row": rows}}
	raw, _ := json.Marshal(report)
	return raw
}

// CashFlow is the month's Statement of Cash Flows, derived from the balance movements so it always reconciles to the
// bank accounts' change.
func (b Books) CashFlow() json.RawMessage {
	activity := b.Activity()
	opening, closing := b.Opening, b.Closing()
	beginning, ending := new(big.Rat), new(big.Rat)
	operating := []map[string]any{{"type": "Data", "ColData": []map[string]string{{"value": "Net Income"}, {"value": show(b.NetIncome())}}}}
	investing, financing := []map[string]any{}, []map[string]any{}
	opTotal, invTotal, finTotal := b.NetIncome(), new(big.Rat), new(big.Rat)
	for _, a := range b.Accounts {
		v := activity[a.ID]
		switch a.Type {
		case "Bank":
			if o := opening[a.ID]; o != nil {
				beginning.Add(beginning, o)
			}
			if c := closing[a.ID]; c != nil {
				ending.Add(ending, c)
			}
			continue
		}
		if v == nil || v.Sign() == 0 {
			continue
		}
		switch a.Type {
		case "Accounts Receivable", "Other Current Asset", "Other Asset":
			change := new(big.Rat).Neg(v)
			operating = append(operating, map[string]any{"type": "Data", "ColData": []map[string]string{{"value": a.Name}, {"value": show(change)}}})
			opTotal.Add(opTotal, change)
		case "Accounts Payable", "Credit Card", "Other Current Liability":
			change := new(big.Rat).Neg(v)
			operating = append(operating, map[string]any{"type": "Data", "ColData": []map[string]string{{"value": a.Name}, {"value": show(change)}}})
			opTotal.Add(opTotal, change)
		case "Fixed Asset":
			change := new(big.Rat).Neg(v)
			investing = append(investing, map[string]any{"type": "Data", "ColData": []map[string]string{{"value": a.Name}, {"value": show(change)}}})
			invTotal.Add(invTotal, change)
		case "Long Term Liability", "Equity":
			change := new(big.Rat).Neg(v)
			financing = append(financing, map[string]any{"type": "Data", "ColData": []map[string]string{{"value": a.Name}, {"value": show(change)}}})
			finTotal.Add(finTotal, change)
		}
	}
	section := func(group, name string, lines []map[string]any, total *big.Rat) map[string]any {
		return map[string]any{"type": "Section", "group": group, "Header": map[string]any{"ColData": []map[string]string{{"value": name}, {"value": ""}}}, "Rows": map[string]any{"Row": lines}, "Summary": map[string]any{"ColData": []map[string]string{{"value": "Net cash provided by " + strings.ToLower(name)}, {"value": show(total)}}}}
	}
	increase := add(add(opTotal, invTotal), finTotal)
	rows := []map[string]any{
		section("OperatingActivities", "Operating Activities", operating, opTotal),
		section("InvestingActivities", "Investing Activities", investing, invTotal),
		section("FinancingActivities", "Financing Activities", financing, finTotal),
		{"type": "Section", "group": "CashIncrease", "Summary": map[string]any{"ColData": []map[string]string{{"value": "Net cash increase for period"}, {"value": show(increase)}}}},
		{"type": "Section", "group": "BeginningCash", "Summary": map[string]any{"ColData": []map[string]string{{"value": "Cash at beginning of period"}, {"value": show(beginning)}}}},
		{"type": "Section", "group": "EndingCash", "Summary": map[string]any{"ColData": []map[string]string{{"value": "Cash at end of period"}, {"value": show(ending)}}}},
	}
	report := map[string]any{"Header": header("CashFlow", b.Period, b.Currency), "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "", "ColType": "Account"}, {"ColTitle": "Total", "ColType": "Money"}}}, "Rows": map[string]any{"Row": rows}}
	raw, _ := json.Marshal(report)
	return raw
}

// TransactionList is the month's transactions as the report lists them.
func (b Books) TransactionList() json.RawMessage {
	rows := []map[string]any{}
	for _, t := range b.Txns {
		rows = append(rows, map[string]any{"type": "Data", "ColData": []map[string]string{{"value": t.Date}, {"value": reportedLabel(t.Kind), "id": t.ID}, {"value": t.Number}, {"value": "No"}, {"value": t.Party, "id": t.PartyID}, {"value": t.Memo}, {"value": show(t.Amount)}}})
	}
	column := func(title, kind, key string) map[string]any {
		return map[string]any{"ColTitle": title, "ColType": kind, "MetaData": []map[string]string{{"Name": "ColKey", "Value": key}}}
	}
	report := map[string]any{"Header": header("TransactionList", b.Period, b.Currency), "Columns": map[string]any{"Column": []map[string]any{column("Date", "Date", "tx_date"), column("Transaction Type", "String", "txn_type"), column("Num", "String", "doc_num"), column("Posting", "String", "is_no_post"), column("Name", "String", "name"), column("Memo/Description", "String", "memo"), column("Amount", "Money", "subt_nat_amount")}}, "Rows": map[string]any{"Row": rows}}
	raw, _ := json.Marshal(report)
	return raw
}

// Reports are the month-end reports a capture holds.
func (b Books) Reports() map[string]json.RawMessage {
	return map[string]json.RawMessage{"TrialBalance": b.TrialBalance(), "ProfitAndLoss": b.ProfitAndLoss(), "GeneralLedger": b.GeneralLedger(), "CashFlow": b.CashFlow(), "TransactionList": b.TransactionList()}
}

// Record is a transaction as the QuickBooks API returns it, with the fields the exceptions, aging and search read.
func (b Books) Record(t Txn) json.RawMessage {
	lines := []map[string]any{}
	for i, p := range t.Lines {
		a := b.account(p.Account)
		amount := p.Debit
		if amount == nil {
			amount = p.Credit
		}
		var line map[string]any
		switch t.Kind {
		case "Bill", "Purchase", "VendorCredit":
			if a.Type == "Accounts Payable" || a.Type == "Bank" || a.Type == "Credit Card" {
				continue
			}
			line = map[string]any{"Id": fmt.Sprint(i + 1), "Amount": json.Number(show(amount)), "DetailType": "AccountBasedExpenseLineDetail", "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"value": a.ID, "name": a.Name}}}
		case "Invoice", "SalesReceipt", "CreditMemo":
			if a.Type == "Accounts Receivable" || a.Type == "Bank" {
				continue
			}
			line = map[string]any{"Id": fmt.Sprint(i + 1), "Amount": json.Number(show(amount)), "DetailType": "SalesItemLineDetail", "SalesItemLineDetail": map[string]any{"ItemRef": map[string]string{"value": "1", "name": "Services"}, "ItemAccountRef": map[string]string{"value": a.ID, "name": a.Name}}}
		case "Deposit":
			if a.Type == "Bank" {
				continue
			}
			detail := map[string]any{"AccountRef": map[string]string{"value": a.ID, "name": a.Name}}
			if t.Party != "" {
				detail["Entity"] = map[string]string{"value": t.PartyID, "name": t.Party}
			}
			line = map[string]any{"Id": fmt.Sprint(i + 1), "Amount": json.Number(show(amount)), "DetailType": "DepositLineDetail", "DepositLineDetail": detail}
		case "JournalEntry":
			posting := "Debit"
			if p.Debit == nil {
				posting = "Credit"
			}
			line = map[string]any{"Id": fmt.Sprint(i + 1), "Amount": json.Number(show(amount)), "DetailType": "JournalEntryLineDetail", "JournalEntryLineDetail": map[string]any{"PostingType": posting, "AccountRef": map[string]string{"value": a.ID, "name": a.Name}}}
		default:
			continue
		}
		lines = append(lines, line)
	}
	record := map[string]any{"Id": t.ID, "SyncToken": "0", "TxnDate": t.Date, "DocNumber": t.Number, "PrivateNote": t.Memo, "TotalAmt": json.Number(show(t.Amount)),
		"CurrencyRef": map[string]string{"value": b.Currency, "name": "United States Dollar"}, "MetaData": map[string]string{"CreateTime": t.Date + "T09:00:00-05:00", "LastUpdatedTime": t.Date + "T09:00:00-05:00"}, "Line": lines, "domain": "QBO", "sparse": false}
	switch t.Kind {
	case "Bill", "VendorCredit", "BillPayment":
		if t.Party != "" {
			record["VendorRef"] = map[string]string{"value": t.PartyID, "name": t.Party}
		}
		for _, p := range t.Lines {
			if a := b.account(p.Account); a.Type == "Accounts Payable" {
				record["APAccountRef"] = map[string]string{"value": a.ID, "name": a.Name}
			}
		}
	case "Purchase":
		if t.Party != "" {
			record["EntityRef"] = map[string]string{"value": t.PartyID, "name": t.Party, "type": "Vendor"}
		}
		record["PaymentType"] = "Check"
		for _, p := range t.Lines {
			if a := b.account(p.Account); a.Type == "Bank" || a.Type == "Credit Card" {
				record["AccountRef"] = map[string]string{"value": a.ID, "name": a.Name}
				if a.Type == "Credit Card" {
					record["PaymentType"] = "CreditCard"
				}
			}
		}
	case "Deposit":
		for _, p := range t.Lines {
			if a := b.account(p.Account); a.Type == "Bank" && p.Debit != nil {
				record["DepositToAccountRef"] = map[string]string{"value": a.ID, "name": a.Name}
				break
			}
		}
	case "Invoice", "Payment", "SalesReceipt", "CreditMemo":
		if t.Party != "" {
			record["CustomerRef"] = map[string]string{"value": t.PartyID, "name": t.Party}
		}
	}
	if t.DueDate != "" {
		record["DueDate"] = t.DueDate
	}
	if t.Kind == "Invoice" || t.Kind == "Bill" {
		balance := "0"
		if t.Balance != nil {
			balance = show(t.Balance)
		}
		record["Balance"] = json.Number(balance)
	}
	if t.Kind == "Payment" {
		record["UnappliedAmt"] = json.Number("0")
	}
	raw, _ := json.Marshal(record)
	return raw
}

// OpenItems are the invoices and bills still open at month-end, as the capture's open-items read returns them.
func (b Books) OpenItems(earlier []Books) map[string][]json.RawMessage {
	open := map[string][]json.RawMessage{"Invoice": {}, "Bill": {}, "Payment": {}, "CreditMemo": {}, "VendorCredit": {}}
	all := append(append([]Books{}, earlier...), b)
	for _, month := range all {
		for _, t := range month.Txns {
			if (t.Kind == "Invoice" || t.Kind == "Bill") && t.Balance != nil && t.Balance.Sign() > 0 && !b.Settled[t.ID] {
				open[t.Kind] = append(open[t.Kind], month.Record(t))
			}
		}
	}
	return open
}

// Counts is the number of records of each kind captured for the month, every supported kind counted.
func (b Books) Counts() map[string]int {
	counts := map[string]int{}
	for _, kind := range []string{"Bill", "BillPayment", "CreditMemo", "Deposit", "Invoice", "JournalEntry", "Payment", "Purchase", "RefundReceipt", "SalesReceipt", "Transfer", "VendorCredit"} {
		counts[kind] = 0
	}
	for _, t := range b.Txns {
		counts[t.Kind]++
	}
	return counts
}

// Sorted returns the transactions in date order with stable IDs.
func (b *Books) Sort() {
	sort.SliceStable(b.Txns, func(i, j int) bool { return b.Txns[i].Date < b.Txns[j].Date })
}
