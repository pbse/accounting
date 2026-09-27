package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strings"
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
var applyKinds = map[string]struct{ source, target string }{
	"payment":       {"Payment", "Invoice"},
	"vendor_credit": {"VendorCredit", "Bill"},
}

// linkedTotals sums a document's line amounts by the transaction each line links, keyed "Type/ID".
func linkedTotals(raw json.RawMessage) map[string]*big.Rat {
	var doc struct {
		Line []struct {
			Amount    json.Number
			LinkedTxn []struct{ TxnId, TxnType string }
		}
	}
	json.Unmarshal(raw, &doc)
	totals := map[string]*big.Rat{}
	for _, line := range doc.Line {
		amount, ok := new(big.Rat).SetString(line.Amount.String())
		for _, link := range line.LinkedTxn {
			key := link.TxnType + "/" + link.TxnId
			if totals[key] == nil {
				totals[key] = new(big.Rat)
			}
			if ok {
				totals[key].Add(totals[key], amount)
			}
		}
	}
	return totals
}

func sameTotals(a, b map[string]*big.Rat) bool {
	return maps.EqualFunc(a, b, func(x, y *big.Rat) bool { return x.Cmp(y) == 0 })
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
	if s.Production && !slices.Contains(s.Accounts, bankAccount) {
		return "", errors.New("The bank account must be allowed by this client's production grant")
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
// amount, with the same party. A payment must keep every link it had and link the invoice for the amount more; a vendor
// credit's bill payment must link exactly the bill and the credit for that amount, pay nothing, and carry the approved
// date, bank account and currency.
func (s Sandbox) VerifyApplication(ctx context.Context, kind string, source, target OpenDocument, amount, billPayment, date, bankAccount string) (json.RawMessage, error) {
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
	if kind == "payment" {
		want := linkedTotals(source.Raw)
		key := "Invoice/" + target.ID
		if want[key] == nil {
			want[key] = new(big.Rat)
		}
		want[key].Add(want[key], applied)
		if !sameTotals(want, linkedTotals(receipt["Payment"])) {
			return nil, errors.New("The payment's links are not its earlier links plus the invoice for the applied amount")
		}
	}
	if kind == "vendor_credit" {
		if !identifier.MatchString(billPayment) {
			return nil, errors.New("The bill payment's ID is needed to verify the credit application")
		}
		var payment struct {
			ID, TxnDate, PayType   string
			VendorRef, CurrencyRef Reference
			TotalAmt               json.Number
			CheckPayment           struct{ BankAccountRef Reference }
			Line                   []struct{ LinkedTxn []json.RawMessage }
		}
		raw := map[string]json.RawMessage{}
		if err := s.request(ctx, "GET", "/billpayment/"+billPayment, nil, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw["BillPayment"], &payment) != nil || payment.ID != billPayment || payment.VendorRef != source.Party || payment.TxnDate != date || payment.PayType != "Check" || payment.CheckPayment.BankAccountRef.Value != bankAccount || payment.CurrencyRef.Value != s.currency() {
			return nil, errors.New("The bill payment does not match the approved application")
		}
		paid, ok := new(big.Rat).SetString(payment.TotalAmt.String())
		links := map[string]*big.Rat{"Bill/" + target.ID: applied, "VendorCredit/" + source.ID: applied}
		oneLinkEach := len(payment.Line) == 2 && len(payment.Line[0].LinkedTxn) == 1 && len(payment.Line[1].LinkedTxn) == 1
		if !ok || paid.Sign() != 0 || !oneLinkEach || !sameTotals(links, linkedTotals(raw["BillPayment"])) {
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
	if s.Production && !slices.Contains(s.Accounts, id) {
		return "", errors.New("The bank account must be allowed by this client's production grant")
	}
	if a.ID != id || !a.Active || a.AccountType != "Bank" || a.Name == "" || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != s.currency()) {
		return "", errors.New("The account is not an active bank account in the client's currency")
	}
	return a.Name, nil
}

// Suggestion is an application the open items support by a deterministic rule, with no competing reading.
type Suggestion struct {
	Kind     string `json:"kind"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Amount   string `json:"amount"`
	Basis    string `json:"basis"`
}

// referenceToken splits remittance text into whole references: letters, digits and . - _ / stay together.
var referenceToken = regexp.MustCompile(`[A-Za-z0-9._/-]+`)

// SuggestApplications proposes applying a payment's unapplied amount, or a vendor credit, to the one open invoice or
// bill of the same party that either equals it exactly or is named by its reference. A source with more than one
// possible target, or a target claimed by more than one source, gets no suggestion.
func SuggestApplications(open map[string][]json.RawMessage, currency string) []Suggestion {
	type doc struct {
		OpenDocument
		number, reference string
	}
	read := func(entity string) []doc {
		out := []doc{}
		for _, raw := range open[entity] {
			parsed, err := ParseOpenDocument(entity, raw, currency)
			var text struct{ DocNumber, PaymentRefNum, PrivateNote string }
			if err != nil || json.Unmarshal(raw, &text) != nil || parsed.Open.Sign() <= 0 {
				continue
			}
			// A credit's own number is not a reference to a bill; only its note can name one.
			reference := text.PaymentRefNum + " " + text.PrivateNote
			if entity == "VendorCredit" {
				reference = text.PrivateNote
			}
			out = append(out, doc{parsed, strings.ToLower(strings.TrimSpace(text.DocNumber)), reference})
		}
		return out
	}
	suggestions := []Suggestion{}
	for _, kind := range []struct{ kind, source, target, label, name string }{{"payment", "Payment", "Invoice", "invoice", "payment"}, {"vendor_credit", "VendorCredit", "Bill", "bill", "vendor credit"}} {
		targets := read(kind.target)
		for _, s := range read(kind.source) {
			tokens := map[string]bool{}
			for _, token := range referenceToken.FindAllString(s.reference, -1) {
				tokens[strings.ToLower(strings.TrimRight(token, "._/-"))] = true
			}
			matched, basis := []doc{}, map[string][]string{}
			for _, t := range targets {
				if t.Party != s.Party {
					continue
				}
				equal := t.Open.Cmp(s.Open) == 0
				named := t.number != "" && tokens[t.number]
				if equal {
					basis[t.ID] = append(basis[t.ID], "the "+kind.name+"'s open "+s.Open.FloatString(2)+" equals its balance")
				}
				if named {
					basis[t.ID] = append(basis[t.ID], "the "+kind.name+"'s reference names it")
				}
				if equal || named {
					matched = append(matched, t)
				}
			}
			if len(matched) != 1 {
				continue
			}
			t := matched[0]
			amount := s.Open
			if t.Open.Cmp(amount) < 0 {
				amount = t.Open
			}
			if !new(big.Rat).Mul(amount, big.NewRat(100, 1)).IsInt() {
				continue
			}
			suggestions = append(suggestions, Suggestion{kind.kind, s.ID, t.ID, amount.FloatString(2), "Same party; " + strings.Join(basis[t.ID], " and ") + " (" + kind.label + " #" + t.ID + ")."})
		}
	}
	claims := map[string]int{}
	for _, s := range suggestions {
		claims[s.Kind+"\x00"+s.TargetID]++
	}
	return slices.DeleteFunc(suggestions, func(s Suggestion) bool { return claims[s.Kind+"\x00"+s.TargetID] > 1 })
}
