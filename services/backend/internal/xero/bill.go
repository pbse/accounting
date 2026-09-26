package xero

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/url"
	"slices"
)

// BillLine is what a bill line recode sends back and compares; TaxAmount is compared but Xero computes it.
type BillLine struct {
	LineItemID   string          `json:"LineItemID"`
	Description  string          `json:"Description,omitempty"`
	Quantity     json.Number     `json:"Quantity,omitempty"`
	UnitAmount   json.Number     `json:"UnitAmount,omitempty"`
	ItemCode     string          `json:"ItemCode,omitempty"`
	AccountID    string          `json:"AccountID,omitempty"`
	AccountCode  string          `json:"AccountCode,omitempty"`
	TaxType      string          `json:"TaxType,omitempty"`
	TaxAmount    json.Number     `json:"TaxAmount,omitempty"`
	LineAmount   json.Number     `json:"LineAmount"`
	DiscountRate json.Number     `json:"DiscountRate,omitempty"`
	Tracking     json.RawMessage `json:"Tracking,omitempty"`
}

// Bill is the accounting projection of a Xero invoice that a recode must leave unchanged.
type Bill struct {
	InvoiceID       string `json:"InvoiceID"`
	Type            string `json:"Type"`
	Status          string `json:"Status"`
	Contact         struct {
		ContactID string `json:"ContactID"`
	} `json:"Contact"`
	Date            string      `json:"Date"`
	DueDate         string      `json:"DueDate,omitempty"`
	InvoiceNumber   string      `json:"InvoiceNumber,omitempty"`
	Reference       string      `json:"Reference,omitempty"`
	CurrencyCode    string      `json:"CurrencyCode"`
	CurrencyRate    json.Number `json:"CurrencyRate,omitempty"`
	LineAmountTypes string      `json:"LineAmountTypes"`
	SubTotal        json.Number `json:"SubTotal"`
	TotalTax        json.Number `json:"TotalTax"`
	Total           json.Number `json:"Total"`
	AmountDue       json.Number `json:"AmountDue"`
	AmountPaid      json.Number `json:"AmountPaid"`
	AmountCredited  json.Number `json:"AmountCredited"`
	LineItems       []BillLine  `json:"LineItems"`
}

func sameNumber(a, b json.Number) bool {
	if a == "" || b == "" {
		return a == b
	}
	x, ok1 := new(big.Rat).SetString(string(a))
	y, ok2 := new(big.Rat).SetString(string(b))
	return ok1 && ok2 && x.Cmp(y) == 0
}

func sameDay(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	x, err1 := Day(a)
	y, err2 := Day(b)
	return err1 == nil && err2 == nil && x.Equal(y)
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	json.Unmarshal(a, &x)
	json.Unmarshal(b, &y)
	p, _ := json.Marshal(x)
	q, _ := json.Marshal(y)
	return bytes.Equal(p, q)
}

// SameBill reports whether two reads of a bill agree on every compared field and line.
func SameBill(a, b Bill) bool {
	if a.InvoiceID == "" || a.InvoiceID != b.InvoiceID || a.Type != b.Type || a.Status != b.Status || a.Contact != b.Contact || !sameDay(a.Date, b.Date) || !sameDay(a.DueDate, b.DueDate) ||
		a.InvoiceNumber != b.InvoiceNumber || a.Reference != b.Reference || a.CurrencyCode != b.CurrencyCode || !sameNumber(a.CurrencyRate, b.CurrencyRate) || a.LineAmountTypes != b.LineAmountTypes ||
		!sameNumber(a.SubTotal, b.SubTotal) || !sameNumber(a.TotalTax, b.TotalTax) || !sameNumber(a.Total, b.Total) || !sameNumber(a.AmountDue, b.AmountDue) ||
		!sameNumber(a.AmountPaid, b.AmountPaid) || !sameNumber(a.AmountCredited, b.AmountCredited) || len(a.LineItems) != len(b.LineItems) {
		return false
	}
	for _, x := range a.LineItems {
		i := slices.IndexFunc(b.LineItems, func(y BillLine) bool { return y.LineItemID == x.LineItemID })
		if x.LineItemID == "" || i < 0 {
			return false
		}
		y := b.LineItems[i]
		if x.Description != y.Description || !sameNumber(x.Quantity, y.Quantity) || !sameNumber(x.UnitAmount, y.UnitAmount) || x.ItemCode != y.ItemCode || x.AccountID != y.AccountID || x.AccountCode != y.AccountCode ||
			x.TaxType != y.TaxType || !sameNumber(x.TaxAmount, y.TaxAmount) || !sameNumber(x.LineAmount, y.LineAmount) || !sameNumber(x.DiscountRate, y.DiscountRate) || !sameJSON(x.Tracking, y.Tracking) {
			return false
		}
	}
	return true
}

// Recoded is the bill as it should read back after one line moves to another account.
func Recoded(bill Bill, lineID, accountID, code string) Bill {
	bill.LineItems = slices.Clone(bill.LineItems)
	for i := range bill.LineItems {
		if bill.LineItems[i].LineItemID == lineID {
			bill.LineItems[i].AccountID, bill.LineItems[i].AccountCode = accountID, code
		}
	}
	return bill
}

// ParseBill reads the projection from one invoice record.
func ParseBill(raw json.RawMessage) (Bill, error) {
	var bill Bill
	if json.Unmarshal(raw, &bill) != nil || bill.InvoiceID == "" {
		return Bill{}, errors.New("unreadable Xero invoice")
	}
	return bill, nil
}

// ReadBill reads one invoice by ID.
func ReadBill(ctx context.Context, access, tenant, id string) (Bill, error) {
	raw, err := Get(ctx, access, tenant, "Invoices/"+url.PathEscape(id), 1024*1024)
	var result struct{ Invoices []json.RawMessage }
	if err == nil && (json.Unmarshal(raw, &result) != nil || len(result.Invoices) != 1) {
		err = errors.New("Xero did not return the bill")
	}
	if err != nil {
		return Bill{}, err
	}
	return ParseBill(result.Invoices[0])
}

// CanWriteInvoices reports whether an access token may update bills; connections made before corrections must reconnect.
func CanWriteInvoices(access string) bool {
	g := granted(access)
	return slices.Contains(g, "accounting.invoices") || slices.Contains(g, "accounting.transactions")
}

// RecodeBill sends every line of the bill as read, with one line's account changed. Xero deletes lines left out of
// an update, so none is. The caller's saved attempt, not the idempotency key, prevents a second write.
func RecodeBill(ctx context.Context, access, tenant, key string, bill Bill, lineID, accountID, code string) error {
	lines := Recoded(bill, lineID, accountID, code).LineItems
	for i := range lines {
		lines[i].TaxAmount = ""
	}
	body, _ := json.Marshal(map[string]any{"Invoices": []any{map[string]any{"InvoiceID": bill.InvoiceID, "LineItems": lines}}})
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.xero.com/api.xro/2.0/Invoices/"+url.PathEscape(bill.InvoiceID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("xero-tenant-id", tenant)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	_, err := read(req, 1024*1024)
	return err
}
