package quickbooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"slices"
)

// RecodeKinds are the transactions whose one line's account a person can change, with the classification the new
// account must have. A deposit's line must already post to income: only income deposit lines are recoded.
var RecodeKinds = map[string]struct{ Path, Detail, Classification string }{
	"Bill":     {"bill", "AccountBasedExpenseLineDetail", "Expense"},
	"Purchase": {"purchase", "AccountBasedExpenseLineDetail", "Expense"},
	"Deposit":  {"deposit", "DepositLineDetail", "Revenue"},
}

type Purchase struct {
	ID           string          `json:"Id"`
	SyncToken    string          `json:"SyncToken"`
	TxnDate      string          `json:"TxnDate"`
	PaymentType  string          `json:"PaymentType"`
	AccountRef   Reference       `json:"AccountRef"`
	CurrencyRef  Reference       `json:"CurrencyRef"`
	TotalAmt     json.Number     `json:"TotalAmt"`
	Line         []BillLine      `json:"Line"`
	LinkedTxn    json.RawMessage `json:"LinkedTxn,omitempty"`
	TxnTaxDetail json.RawMessage `json:"TxnTaxDetail,omitempty"`
}

type Deposit struct {
	ID                  string          `json:"Id"`
	SyncToken           string          `json:"SyncToken"`
	TxnDate             string          `json:"TxnDate"`
	DepositToAccountRef Reference       `json:"DepositToAccountRef"`
	CurrencyRef         Reference       `json:"CurrencyRef"`
	TotalAmt            json.Number     `json:"TotalAmt"`
	Line                []DepositLine   `json:"Line"`
	CashBack            json.RawMessage `json:"CashBack,omitempty"`
	TxnTaxDetail        json.RawMessage `json:"TxnTaxDetail,omitempty"`
}

// DepositLine is a deposit line coded straight to an account; a line that deposits a payment carries LinkedTxn and is
// refused.
type DepositLine struct {
	ID                string      `json:"Id"`
	Amount            json.Number `json:"Amount"`
	DetailType        string      `json:"DetailType"`
	DepositLineDetail struct {
		AccountRef Reference `json:"AccountRef"`
	} `json:"DepositLineDetail"`
}

func (line *DepositLine) UnmarshalJSON(raw []byte) error {
	var fields, detail map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key := range fields {
		if !slices.Contains([]string{"Id", "LineNum", "Amount", "DetailType", "Description", "DepositLineDetail"}, key) {
			return errors.New("Deposit line has unsupported fields")
		}
	}
	if err := json.Unmarshal(fields["DepositLineDetail"], &detail); err != nil || len(detail["AccountRef"]) == 0 {
		return errors.New("Deposit line has no account")
	}
	for key := range detail {
		if !slices.Contains([]string{"AccountRef", "Entity", "PaymentMethodRef", "CheckNum"}, key) {
			return errors.New("Deposit line has unsupported detail")
		}
	}
	type plain DepositLine
	return json.Unmarshal(raw, (*plain)(line))
}

// RecodeLine is one line of a recodable transaction: its ID, amount and account.
type RecodeLine struct{ ID, Amount, Account string }

// simpleLines checks positive cent amounts under unique IDs that sum to the total.
func simpleLines(lines []RecodeLine, total json.Number) bool {
	sum, seen := new(big.Rat), map[string]bool{}
	want, ok := new(big.Rat).SetString(string(total))
	if !ok || want.Sign() <= 0 || !new(big.Rat).Mul(want, big.NewRat(100, 1)).IsInt() || len(lines) < 1 || len(lines) > 10 {
		return false
	}
	for _, line := range lines {
		amount, ok := new(big.Rat).SetString(line.Amount)
		if !identifier.MatchString(line.ID) || seen[line.ID] || !identifier.MatchString(line.Account) || !ok || amount.Sign() <= 0 || !new(big.Rat).Mul(amount, big.NewRat(100, 1)).IsInt() {
			return false
		}
		seen[line.ID] = true
		sum.Add(sum, amount)
	}
	return sum.Cmp(want) == 0
}

// ParseRecodable checks a transaction against its kind's strict shape and returns its ID, version, date and lines.
func ParseRecodable(kind string, raw json.RawMessage, currency string) (id, version, date string, lines []RecodeLine, err error) {
	unsupported := map[string]string{
		"Bill":     "Only a current, unpaid Bill in the client's currency with up to ten simple account-based expense lines is supported",
		"Purchase": "Only an expense, check or card charge in the client's currency with up to ten simple account-based expense lines, no tax and nothing linked is supported",
		"Deposit":  "Only a deposit in the client's currency with up to ten lines coded straight to an account, no payments deposited, no cash back and no tax is supported",
	}[kind]
	fail := func() (string, string, string, []RecodeLine, error) { return "", "", "", nil, errors.New(unsupported) }
	switch kind {
	case "Bill":
		var b Bill
		if json.Unmarshal(raw, &b) != nil || !simpleBill(b, currency) {
			return fail()
		}
		for _, l := range b.Line {
			lines = append(lines, RecodeLine{l.ID, string(l.Amount), l.AccountBasedExpenseLineDetail.AccountRef.Value})
		}
		return b.ID, b.SyncToken, b.TxnDate, lines, nil
	case "Purchase":
		var p Purchase
		if json.Unmarshal(raw, &p) != nil || !identifier.MatchString(p.ID) || p.SyncToken == "" || p.TxnDate == "" || p.PaymentType == "" || !identifier.MatchString(p.AccountRef.Value) || p.CurrencyRef.Value != currency || len(p.LinkedTxn) > 0 || len(p.TxnTaxDetail) > 0 {
			return fail()
		}
		for _, l := range p.Line {
			if l.DetailType != "AccountBasedExpenseLineDetail" {
				return fail()
			}
			lines = append(lines, RecodeLine{l.ID, string(l.Amount), l.AccountBasedExpenseLineDetail.AccountRef.Value})
		}
		if !simpleLines(lines, p.TotalAmt) {
			return fail()
		}
		return p.ID, p.SyncToken, p.TxnDate, lines, nil
	case "Deposit":
		var d Deposit
		if json.Unmarshal(raw, &d) != nil || !identifier.MatchString(d.ID) || d.SyncToken == "" || d.TxnDate == "" || !identifier.MatchString(d.DepositToAccountRef.Value) || d.CurrencyRef.Value != currency || len(d.CashBack) > 0 || len(d.TxnTaxDetail) > 0 {
			return fail()
		}
		for _, l := range d.Line {
			if l.DetailType != "DepositLineDetail" {
				return fail()
			}
			lines = append(lines, RecodeLine{l.ID, string(l.Amount), l.DepositLineDetail.AccountRef.Value})
		}
		if !simpleLines(lines, d.TotalAmt) {
			return fail()
		}
		return d.ID, d.SyncToken, d.TxnDate, lines, nil
	}
	return "", "", "", nil, errors.New("Unsupported transaction kind")
}

// SameRecodable reports whether two copies of a transaction agree on what a correction depends on: its ID, version, date
// and every line's ID, amount and account.
func SameRecodable(kind string, a, b json.RawMessage, currency string) bool {
	idA, versionA, dateA, linesA, errA := ParseRecodable(kind, a, currency)
	idB, versionB, dateB, linesB, errB := ParseRecodable(kind, b, currency)
	if errA != nil || errB != nil || idA != idB || versionA != versionB || dateA != dateB || len(linesA) != len(linesB) {
		return false
	}
	for i := range linesA {
		amountA, _ := new(big.Rat).SetString(linesA[i].Amount)
		amountB, _ := new(big.Rat).SetString(linesB[i].Amount)
		if linesA[i].ID != linesB[i].ID || linesA[i].Account != linesB[i].Account || amountA.Cmp(amountB) != 0 {
			return false
		}
	}
	return true
}

// ReadRecodable reads a transaction exactly as QuickBooks returns it and checks its kind's strict shape.
func (s Sandbox) ReadRecodable(ctx context.Context, kind, id string) (json.RawMessage, []RecodeLine, string, error) {
	spec, known := RecodeKinds[kind]
	if !known || !identifier.MatchString(id) {
		return nil, nil, "", errors.New("An exact transaction ID is required")
	}
	var result map[string]json.RawMessage
	if err := s.request(ctx, "GET", "/"+spec.Path+"/"+id, nil, &result); err != nil {
		return nil, nil, "", err
	}
	got, _, date, lines, err := ParseRecodable(kind, result[kind], s.currency())
	if err != nil {
		return nil, nil, "", err
	}
	if got != id {
		return nil, nil, "", errors.New("QuickBooks returned a different transaction")
	}
	return result[kind], lines, date, nil
}

// decoded reads JSON keeping numbers exact.
func decoded(raw json.RawMessage) (map[string]any, bool) {
	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return out, decoder.Decode(&out) == nil
}

// Recode changes one line's account by resending the transaction's lines exactly as read, with only that account
// changed. A production grant must allow both the line's current and new accounts.
func (s Sandbox) Recode(ctx context.Context, kind string, before json.RawMessage, lineID, target string) error {
	spec := RecodeKinds[kind]
	id, version, _, lines, err := ParseRecodable(kind, before, s.currency())
	fields, ok := decoded(before)
	if err != nil || !ok || !identifier.MatchString(target) {
		return errors.New("The approved correction is invalid")
	}
	i := slices.IndexFunc(lines, func(l RecodeLine) bool { return l.ID == lineID })
	if i < 0 || lines[i].Account == target {
		return errors.New("Choose one line and a different account")
	}
	if s.Production && (!slices.Contains(s.Accounts, target) || !slices.Contains(s.Accounts, lines[i].Account)) {
		return errors.New("Both the line's current and new accounts must be allowed by this client's production grant")
	}
	raw := fields["Line"].([]any)
	raw[i].(map[string]any)[spec.Detail].(map[string]any)["AccountRef"] = map[string]string{"value": target}
	var response map[string]json.RawMessage
	if err := s.request(ctx, "POST", "/"+spec.Path, map[string]any{"Id": id, "SyncToken": version, "sparse": true, "Line": raw}, &response); err != nil {
		return err
	}
	var echoed struct {
		ID string `json:"Id"`
	}
	if json.Unmarshal(response[kind], &echoed) != nil || echoed.ID != id {
		return errors.New("QuickBooks update response has a different identity")
	}
	return nil
}

// same compares decoded JSON, numbers by value.
func same(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		p, okP := new(big.Rat).SetString(string(x))
		q, okQ := new(big.Rat).SetString(string(y))
		return ok && okP && okQ && p.Cmp(q) == 0
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for key, value := range x {
			if other, found := y[key]; !found || !same(value, other) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !same(x[i], y[i]) {
				return false
			}
		}
		return true
	}
	return a == b
}

// VerifyRecode reads the transaction back and requires a new version with everything as approved: every field of the
// transaction and of every other line unchanged, and only the chosen line's account now the new one.
func (s Sandbox) VerifyRecode(ctx context.Context, kind string, before json.RawMessage, lineID, target string) (json.RawMessage, error) {
	spec := RecodeKinds[kind]
	id, version, _, _, err := ParseRecodable(kind, before, s.currency())
	if err != nil {
		return nil, err
	}
	after, _, _, err := s.ReadRecodable(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	was, okWas := decoded(before)
	now, okNow := decoded(after)
	if !okWas || !okNow || now["SyncToken"] == version {
		return nil, errors.New("Readback version is unchanged or unreadable")
	}
	for key, value := range was {
		if key != "SyncToken" && key != "MetaData" && key != "Line" && !same(value, now[key]) {
			return nil, errors.New("Readback differs from the approved transaction: " + key)
		}
	}
	for key := range now {
		if _, found := was[key]; !found && key != "MetaData" {
			return nil, errors.New("Readback adds a field the approved transaction did not have: " + key)
		}
	}
	oldLines, _ := was["Line"].([]any)
	newLines, _ := now["Line"].([]any)
	if len(oldLines) != len(newLines) {
		return nil, errors.New("Readback line count differs from approval")
	}
	for i := range oldLines {
		o, n := oldLines[i].(map[string]any), newLines[i].(map[string]any)
		if o["Id"] == lineID {
			od, _ := o[spec.Detail].(map[string]any)
			nd, _ := n[spec.Detail].(map[string]any)
			ref, _ := nd["AccountRef"].(map[string]any)
			if ref == nil || ref["value"] != target {
				return nil, errors.New("Readback shows the line on a different account")
			}
			// The new account's name comes from QuickBooks; everything else on the line must be as it was.
			od, nd = copied(od), copied(nd)
			delete(od, "AccountRef")
			delete(nd, "AccountRef")
			o, n = copied(o), copied(n)
			o[spec.Detail], n[spec.Detail] = od, nd
		}
		if !same(o, n) {
			return nil, errors.New("Readback changed a line that was not approved")
		}
	}
	return after, nil
}

func copied(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ValidateTargetAccount returns the name of an active account of the given classification in the client's currency;
// in production, the grant must allow it.
func (s Sandbox) ValidateTargetAccount(ctx context.Context, id, classification string) (string, error) {
	if !identifier.MatchString(id) || s.Production && !slices.Contains(s.Accounts, id) {
		return "", errors.New("Choose an exact account allowed by this client's production grant")
	}
	var result struct {
		Account struct {
			ID             string `json:"Id"`
			Name           string
			Active         bool
			Classification string
			CurrencyRef    Reference
		}
	}
	if err := s.request(ctx, "GET", "/account/"+id, nil, &result); err != nil {
		return "", err
	}
	a := result.Account
	if a.ID != id || !a.Active || a.Classification != classification || a.Name == "" || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != s.currency()) {
		return "", errors.New("The account is not an active " + map[string]string{"Expense": "expense", "Revenue": "income"}[classification] + " account in the client's currency")
	}
	return a.Name, nil
}
