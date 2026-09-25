package xero

import (
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"close.local/backend/internal/quickbooks"
)

type contact struct{ ContactID, Name string }

type entry struct {
	Type, Status, Reference, InvoiceNumber, CreditNoteNumber, CurrencyCode, LineAmountTypes, PaymentType string
	InvoiceID, CreditNoteID, BankTransactionID, ManualJournalID, PaymentID, OverpaymentID, PrepaymentID  string
	Date, DueDate                                                                                        string
	Contact                                                                                              contact
	LineItems, JournalLines                                                                              []struct {
		AccountID, AccountCode string
		LineAmount, TaxAmount  json.Number
	}
	Total, AmountDue, RemainingCredit, Amount json.Number
	Invoice                                   *struct {
		Contact      contact
		CurrencyCode string
	}
}

// Ledger reshapes a snapshot's Xero transactions into the QuickBooks records the exception, schedule, matching and
// aging checks read: the month's rows, the open items and the account directory, with the base currency. Line
// amounts are net of tax. Refunds, other payment types, and overpayments and prepayments made through the bank stay in
// the snapshot but are not reshaped; their open credits are read as open items.
func Ledger(payload json.RawMessage) (rows []quickbooks.TransactionRow, open map[string][]json.RawMessage, accounts json.RawMessage, home string, err error) {
	var snapshot struct {
		Date         string
		Organisation struct {
			Organisations []struct{ BaseCurrency string }
		}
		Accounts struct {
			Accounts []struct{ AccountID, Code, Name, Type string }
		}
		Transactions *struct {
			Records map[string][]entry `json:"records"`
			Open    map[string][]entry `json:"open"`
		} `json:"transactions"`
		Unread string `json:"transactions_error"`
	}
	if json.Unmarshal(payload, &snapshot) != nil {
		return nil, nil, nil, "", errors.New("Xero snapshot is unreadable")
	}
	if snapshot.Transactions == nil {
		if snapshot.Unread == "" {
			snapshot.Unread = "This Xero snapshot has no transactions; capture again."
		}
		return nil, nil, nil, "", errors.New(snapshot.Unread)
	}
	if len(snapshot.Organisation.Organisations) == 1 {
		home = snapshot.Organisation.Organisations[0].BaseCurrency
	}
	monthEnd, err := time.Parse("2006-01-02", snapshot.Date)
	if err != nil {
		return nil, nil, nil, "", errors.New("Xero snapshot has no month-end date")
	}
	names, byCode, directory := map[string]string{}, map[string]string{}, []map[string]string{}
	for _, a := range snapshot.Accounts.Accounts {
		names[a.AccountID], byCode[a.Code] = a.Name, a.AccountID
		account := map[string]string{"Id": a.AccountID, "Name": a.Name, "AccountType": a.Type}
		switch a.Type {
		case "PREPAYMENT":
			account["AccountSubType"] = "PrepaidExpenses"
		case "FIXED":
			account["AccountType"] = "Fixed Asset"
		}
		directory = append(directory, account)
	}
	accounts, _ = json.Marshal(directory)
	party := func(c contact) map[string]string { return map[string]string{"value": c.ContactID, "name": c.Name} }
	record := func(e entry, id, number, currency string) (map[string]any, bool) {
		date, err := Day(e.Date)
		if err != nil {
			return nil, false
		}
		if currency == "" {
			currency = home
		}
		return map[string]any{"Id": id, "TxnDate": date.Format("2006-01-02"), "DocNumber": number, "CurrencyRef": map[string]string{"value": currency}}, true
	}
	// lines keeps each line's account and amount net of tax; a line without an account posts through an item.
	lines := func(e entry, detail string, journal bool) ([]map[string]any, *big.Rat) {
		out, debits := []map[string]any{}, new(big.Rat)
		for _, l := range append(e.LineItems, e.JournalLines...) {
			amount, ok := new(big.Rat).SetString(l.LineAmount.String())
			if !ok {
				continue
			}
			if tax, taxed := new(big.Rat).SetString(l.TaxAmount.String()); taxed && e.LineAmountTypes == "Inclusive" {
				amount.Sub(amount, tax)
			}
			id := l.AccountID
			if id == "" {
				id = byCode[l.AccountCode]
			}
			if id == "" {
				out = append(out, map[string]any{"Amount": json.Number(amount.FloatString(2)), "ItemBasedExpenseLineDetail": map[string]any{}})
				continue
			}
			info := map[string]any{"AccountRef": map[string]string{"value": id, "name": names[id]}}
			if journal {
				info["PostingType"] = "Debit"
				if amount.Sign() < 0 {
					info["PostingType"] = "Credit"
					amount.Neg(amount)
				} else {
					debits.Add(debits, amount)
				}
			}
			out = append(out, map[string]any{"Amount": json.Number(amount.FloatString(2)), "DetailType": detail, detail: info})
		}
		return out, debits
	}
	add := func(kind, id string, total json.Number, fields map[string]any) {
		raw, _ := json.Marshal(fields)
		amount := ""
		if value, ok := new(big.Rat).SetString(total.String()); ok {
			amount = value.FloatString(2)
		}
		rows = append(rows, quickbooks.TransactionRow{Kind: kind, ProviderID: id, Date: fields["TxnDate"].(string), Currency: fields["CurrencyRef"].(map[string]string)["value"], Amount: amount, Record: raw})
	}
	records := snapshot.Transactions.Records
	for _, e := range records["Invoices"] {
		kind, ref, detail := map[string]string{"ACCREC": "Invoice", "ACCPAY": "Bill"}[e.Type], "CustomerRef", "SalesItemLineDetail"
		if kind == "Bill" {
			ref, detail = "VendorRef", "AccountBasedExpenseLineDetail"
		}
		fields, ok := record(e, e.InvoiceID, e.InvoiceNumber, e.CurrencyCode)
		if kind == "" || !ok || (e.Status != "AUTHORISED" && e.Status != "PAID") {
			continue
		}
		fields[ref], fields["TotalAmt"] = party(e.Contact), e.Total
		fields["Line"], _ = lines(e, detail, false)
		add(kind, e.InvoiceID, e.Total, fields)
	}
	for _, e := range records["CreditNotes"] {
		kind, ref, detail := map[string]string{"ACCRECCREDIT": "CreditMemo", "ACCPAYCREDIT": "VendorCredit"}[e.Type], "CustomerRef", "SalesItemLineDetail"
		if kind == "VendorCredit" {
			ref, detail = "VendorRef", "AccountBasedExpenseLineDetail"
		}
		fields, ok := record(e, e.CreditNoteID, e.CreditNoteNumber, e.CurrencyCode)
		if kind == "" || !ok || (e.Status != "AUTHORISED" && e.Status != "PAID") {
			continue
		}
		fields[ref], fields["TotalAmt"] = party(e.Contact), e.Total
		fields["Line"], _ = lines(e, detail, false)
		add(kind, e.CreditNoteID, e.Total, fields)
	}
	for _, e := range records["BankTransactions"] {
		kind, detail := "", ""
		switch e.Type {
		case "SPEND":
			kind, detail = "Purchase", "AccountBasedExpenseLineDetail"
		case "RECEIVE":
			kind, detail = "Deposit", "DepositLineDetail"
		case "SPEND-TRANSFER", "RECEIVE-TRANSFER":
			kind = "Transfer"
		}
		fields, ok := record(e, e.BankTransactionID, e.Reference, e.CurrencyCode)
		if kind == "" || !ok || e.Status != "AUTHORISED" {
			continue
		}
		fields["TotalAmt"] = e.Total
		if kind != "Transfer" {
			fields["EntityRef"] = party(e.Contact)
			fields["Line"], _ = lines(e, detail, false)
		}
		add(kind, e.BankTransactionID, e.Total, fields)
	}
	for _, e := range records["ManualJournals"] {
		fields, ok := record(e, e.ManualJournalID, "", "")
		if !ok || e.Status != "POSTED" {
			continue
		}
		journal, debits := lines(e, "JournalEntryLineDetail", true)
		fields["Line"], fields["TotalAmt"] = journal, json.Number(debits.FloatString(2))
		add("JournalEntry", e.ManualJournalID, json.Number(debits.FloatString(2)), fields)
	}
	for _, e := range records["Payments"] {
		kind, ref := map[string]string{"ACCRECPAYMENT": "Payment", "ACCPAYPAYMENT": "BillPayment"}[e.PaymentType], "CustomerRef"
		if kind == "BillPayment" {
			ref = "VendorRef"
		}
		if kind == "" || e.Invoice == nil || e.Status != "AUTHORISED" {
			continue
		}
		fields, ok := record(e, e.PaymentID, e.Reference, e.Invoice.CurrencyCode)
		if !ok {
			continue
		}
		fields[ref], fields["TotalAmt"] = party(e.Invoice.Contact), e.Amount
		add(kind, e.PaymentID, e.Amount, fields)
	}
	// Open items: each document still open at capture time, dated on or before month-end. A zero balance is skipped
	// by the checks that read them.
	open = map[string][]json.RawMessage{}
	keep := func(kind string, e entry, id, number string, fields map[string]any) {
		base, ok := record(e, id, number, e.CurrencyCode)
		if !ok || base["TxnDate"].(string) > monthEnd.Format("2006-01-02") {
			return
		}
		for k, v := range fields {
			base[k] = v
		}
		raw, _ := json.Marshal(base)
		open[kind] = append(open[kind], raw)
	}
	for _, e := range snapshot.Transactions.Open["Invoices"] {
		due := ""
		if d, err := Day(e.DueDate); err == nil {
			due = d.Format("2006-01-02")
		}
		switch e.Type {
		case "ACCREC":
			keep("Invoice", e, e.InvoiceID, e.InvoiceNumber, map[string]any{"Balance": e.AmountDue, "DueDate": due, "CustomerRef": party(e.Contact)})
		case "ACCPAY":
			keep("Bill", e, e.InvoiceID, e.InvoiceNumber, map[string]any{"Balance": e.AmountDue, "DueDate": due, "VendorRef": party(e.Contact)})
		}
	}
	for _, e := range snapshot.Transactions.Open["CreditNotes"] {
		switch e.Type {
		case "ACCRECCREDIT":
			keep("CreditMemo", e, e.CreditNoteID, e.CreditNoteNumber, map[string]any{"RemainingCredit": e.RemainingCredit, "CustomerRef": party(e.Contact)})
		case "ACCPAYCREDIT":
			keep("VendorCredit", e, e.CreditNoteID, e.CreditNoteNumber, map[string]any{"Balance": e.RemainingCredit, "VendorRef": party(e.Contact)})
		}
	}
	for _, set := range []string{"Overpayments", "Prepayments"} {
		for _, e := range snapshot.Transactions.Open[set] {
			id := e.OverpaymentID + e.PrepaymentID
			switch e.Type {
			case "RECEIVE-OVERPAYMENT", "RECEIVE-PREPAYMENT":
				keep("Payment", e, id, e.Reference, map[string]any{"UnappliedAmt": e.RemainingCredit, "CustomerRef": party(e.Contact)})
			case "SPEND-OVERPAYMENT", "SPEND-PREPAYMENT":
				keep("VendorCredit", e, id, e.Reference, map[string]any{"Balance": e.RemainingCredit, "VendorRef": party(e.Contact)})
			}
		}
	}
	return rows, open, accounts, home, nil
}
