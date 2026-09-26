package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
)

// OpenDocument is an invoice, bill, customer payment or vendor credit with what is still open on it.
type OpenDocument struct {
	ID        string    `json:"Id"`
	SyncToken string    `json:"SyncToken"`
	TxnDate   string    `json:"TxnDate"`
	Party     Reference `json:"-"`
	Currency  Reference `json:"CurrencyRef"`
	// Open is the Balance of an invoice, bill or vendor credit, or the UnappliedAmt of a payment.
	Open *big.Rat `json:"-"`
	Raw  json.RawMessage
}

// applyKinds names each application's source and target: what is applied, and the document it settles.
var applyKinds = map[string]struct{ source, target, party string }{
	"payment":       {"Payment", "Invoice", "CustomerRef"},
	"vendor_credit": {"VendorCredit", "Bill", "VendorRef"},
}

// ReadOpenDocument reads one document of the given QuickBooks type and what is open on it, in the client's currency.
func (s Sandbox) ReadOpenDocument(ctx context.Context, entity, id string) (OpenDocument, error) {
	paths := map[string]string{"Payment": "payment", "Invoice": "invoice", "VendorCredit": "vendorcredit", "Bill": "bill"}
	if paths[entity] == "" || !identifier.MatchString(id) {
		return OpenDocument{}, errors.New("An exact document ID is required")
	}
	var result map[string]json.RawMessage
	if err := s.request(ctx, "GET", "/"+paths[entity]+"/"+id, nil, &result); err != nil {
		return OpenDocument{}, err
	}
	return ParseOpenDocument(entity, result[entity], s.currency())
}

// ParseOpenDocument reads a document's identity, party, currency and open amount.
func ParseOpenDocument(entity string, raw json.RawMessage, currency string) (OpenDocument, error) {
	var fields struct {
		ID                     string `json:"Id"`
		SyncToken, TxnDate     string
		CustomerRef, VendorRef Reference
		CurrencyRef            Reference
		Balance, UnappliedAmt  json.Number
	}
	if json.Unmarshal(raw, &fields) != nil || !identifier.MatchString(fields.ID) || fields.SyncToken == "" || fields.TxnDate == "" {
		return OpenDocument{}, errors.New("The " + entity + " could not be read")
	}
	party, open := fields.CustomerRef, fields.Balance
	if entity == "Bill" || entity == "VendorCredit" {
		party = fields.VendorRef
	}
	if entity == "Payment" {
		open = fields.UnappliedAmt
	}
	amount, ok := new(big.Rat).SetString(string(open))
	if !ok || !identifier.MatchString(party.Value) || fields.CurrencyRef.Value != currency {
		return OpenDocument{}, errors.New("The " + entity + " has no readable party or open amount in the client's currency")
	}
	return OpenDocument{ID: fields.ID, SyncToken: fields.SyncToken, TxnDate: fields.TxnDate, Party: party, Currency: fields.CurrencyRef, Open: amount, Raw: raw}, nil
}

// ApplyPayment links an amount of a customer payment to an invoice by resending the payment's lines as read plus one
// line for the invoice.
func (s Sandbox) ApplyPayment(ctx context.Context, payment OpenDocument, invoiceID, amount string) error {
	fields, ok := decoded(payment.Raw)
	lines, isList := fields["Line"].([]any)
	if !ok || (fields["Line"] != nil && !isList) || !identifier.MatchString(invoiceID) {
		return errors.New("The approved payment application is invalid")
	}
	lines = append(lines, map[string]any{"Amount": json.Number(amount), "LinkedTxn": []map[string]string{{"TxnId": invoiceID, "TxnType": "Invoice"}}})
	var response map[string]json.RawMessage
	if err := s.request(ctx, "POST", "/payment", map[string]any{"Id": payment.ID, "SyncToken": payment.SyncToken, "sparse": true, "Line": lines}, &response); err != nil {
		return err
	}
	var echoed struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(response["Payment"], &echoed) != nil || echoed.ID != payment.ID {
		return errors.New("QuickBooks payment update response has a different identity")
	}
	return nil
}

// ApplyVendorCredit applies an amount of a vendor credit to a bill with a zero-amount bill payment that links both. The
// request ID makes a repeated request return the same bill payment.
func (s Sandbox) ApplyVendorCredit(ctx context.Context, credit, bill OpenDocument, amount, bankAccount, date, requestID string) (string, error) {
	if !identifier.MatchString(bankAccount) || credit.Party != bill.Party {
		return "", errors.New("The approved credit application is invalid")
	}
	body := map[string]any{
		"VendorRef": credit.Party, "PayType": "Check", "CheckPayment": map[string]any{"BankAccountRef": Reference{bankAccount}},
		"TotalAmt": json.Number("0"), "TxnDate": date, "CurrencyRef": credit.Currency,
		"Line": []map[string]any{
			{"Amount": json.Number(amount), "LinkedTxn": []map[string]string{{"TxnId": bill.ID, "TxnType": "Bill"}}},
			{"Amount": json.Number(amount), "LinkedTxn": []map[string]string{{"TxnId": credit.ID, "TxnType": "VendorCredit"}}},
		},
	}
	var response struct {
		BillPayment struct {
			ID string `json:"Id"`
		}
	}
	if err := s.request(ctx, "POST", "/billpayment?requestid="+url.QueryEscape(requestID), body, &response); err != nil {
		return "", err
	}
	if !identifier.MatchString(response.BillPayment.ID) {
		return "", errors.New("QuickBooks did not return a bill payment ID")
	}
	return response.BillPayment.ID, nil
}

// VerifyApplication reads both documents back and requires each open amount to have fallen by exactly the applied
// amount, with the same party; for a vendor credit it also reads the bill payment and requires it to link exactly the
// bill and the credit for that amount, with nothing paid.
func (s Sandbox) VerifyApplication(ctx context.Context, kind string, source, target OpenDocument, amount, billPayment string) (json.RawMessage, error) {
	spec, known := applyKinds[kind]
	applied, ok := new(big.Rat).SetString(amount)
	if !known || !ok || applied.Sign() <= 0 {
		return nil, errors.New("The application to verify is invalid")
	}
	receipt := map[string]json.RawMessage{}
	for _, pair := range []struct {
		entity string
		before OpenDocument
	}{{spec.source, source}, {spec.target, target}} {
		after, err := s.ReadOpenDocument(ctx, pair.entity, pair.before.ID)
		if err != nil {
			return nil, err
		}
		want := new(big.Rat).Sub(pair.before.Open, applied)
		if after.Party != pair.before.Party || after.Open.Cmp(want) != 0 {
			return nil, errors.New("The " + pair.entity + "'s open amount did not fall by exactly the applied amount")
		}
		receipt[pair.entity] = after.Raw
	}
	if kind == "vendor_credit" {
		if !identifier.MatchString(billPayment) {
			return nil, errors.New("The bill payment's ID is needed to verify the credit application")
		}
		var result struct {
			BillPayment struct {
				VendorRef Reference
				TotalAmt  json.Number
				Line      []struct {
					Amount    json.Number
					LinkedTxn []struct{ TxnId, TxnType string }
				}
			}
		}
		raw := map[string]json.RawMessage{}
		if err := s.request(ctx, "GET", "/billpayment/"+billPayment, nil, &raw); err != nil {
			return nil, err
		}
		payment := &result.BillPayment
		if json.Unmarshal(raw["BillPayment"], payment) != nil || payment.VendorRef != source.Party || payment.TotalAmt.String() == "" || len(payment.Line) != 2 {
			return nil, errors.New("The bill payment does not match the approved application")
		}
		paid, _ := new(big.Rat).SetString(payment.TotalAmt.String())
		linked := map[string]string{}
		for _, line := range payment.Line {
			lineAmount, ok := new(big.Rat).SetString(line.Amount.String())
			if !ok || lineAmount.Cmp(applied) != 0 || len(line.LinkedTxn) != 1 {
				return nil, errors.New("The bill payment's lines do not match the approved application")
			}
			linked[line.LinkedTxn[0].TxnType] = line.LinkedTxn[0].TxnId
		}
		if paid == nil || paid.Sign() != 0 || linked["Bill"] != target.ID || linked["VendorCredit"] != source.ID {
			return nil, errors.New("The bill payment pays money or links other documents")
		}
		receipt["BillPayment"] = raw["BillPayment"]
	}
	return json.Marshal(receipt)
}

// ValidateBankAccount returns the name of an active bank account in the client's currency, for a bill payment that
// moves no money.
func (s Sandbox) ValidateBankAccount(ctx context.Context, id string) (string, error) {
	if !identifier.MatchString(id) {
		return "", errors.New("Choose an exact bank account")
	}
	var result struct {
		Account struct {
			ID          string `json:"Id"`
			Name        string
			Active      bool
			AccountType string
			CurrencyRef Reference
		}
	}
	if err := s.request(ctx, "GET", "/account/"+id, nil, &result); err != nil {
		return "", err
	}
	a := result.Account
	if a.ID != id || !a.Active || a.AccountType != "Bank" || a.Name == "" || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != s.currency()) {
		return "", errors.New("The account is not an active bank account in the client's currency")
	}
	return a.Name, nil
}
