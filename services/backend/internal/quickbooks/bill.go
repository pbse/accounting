package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"slices"
)

type Bill struct {
	ID           string          `json:"Id"`
	SyncToken    string          `json:"SyncToken"`
	TxnDate      string          `json:"TxnDate"`
	VendorRef    Reference       `json:"VendorRef"`
	CurrencyRef  Reference       `json:"CurrencyRef"`
	TotalAmt     json.Number     `json:"TotalAmt"`
	Balance      json.Number     `json:"Balance"`
	Line         []BillLine      `json:"Line"`
	LinkedTxn    json.RawMessage `json:"LinkedTxn,omitempty"`
	TxnTaxDetail json.RawMessage `json:"TxnTaxDetail,omitempty"`
}

type BillLine struct {
	ID                            string      `json:"Id"`
	Amount                        json.Number `json:"Amount"`
	DetailType                    string      `json:"DetailType"`
	Description                   string      `json:"Description,omitempty"`
	AccountBasedExpenseLineDetail struct {
		AccountRef Reference `json:"AccountRef"`
	} `json:"AccountBasedExpenseLineDetail"`
}

func (line *BillLine) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key := range fields {
		if key != "Id" && key != "LineNum" && key != "Amount" && key != "DetailType" && key != "Description" && key != "AccountBasedExpenseLineDetail" {
			return errors.New("Bill line has unsupported fields")
		}
	}
	var detail map[string]json.RawMessage
	if err := json.Unmarshal(fields["AccountBasedExpenseLineDetail"], &detail); err != nil {
		return err
	}
	if len(detail) != 1 || len(detail["AccountRef"]) == 0 {
		return errors.New("Bill line has unsupported account detail")
	}
	type plain BillLine
	return json.Unmarshal(raw, (*plain)(line))
}

func simpleBill(b Bill, currency string) bool {
	if !identifier.MatchString(b.ID) || b.SyncToken == "" || b.TxnDate == "" || !identifier.MatchString(b.VendorRef.Value) || b.CurrencyRef.Value != currency || len(b.Line) < 1 || len(b.Line) > 10 || len(b.LinkedTxn) > 0 || len(b.TxnTaxDetail) > 0 {
		return false
	}
	total, totalOK := new(big.Rat).SetString(string(b.TotalAmt))
	balance, balanceOK := new(big.Rat).SetString(string(b.Balance))
	if !totalOK || !balanceOK || total.Sign() <= 0 || balance.Cmp(total) != 0 || !new(big.Rat).Mul(total, big.NewRat(100, 1)).IsInt() {
		return false
	}
	sum := new(big.Rat)
	seen := map[string]bool{}
	for _, line := range b.Line {
		amount, ok := new(big.Rat).SetString(string(line.Amount))
		if !identifier.MatchString(line.ID) || seen[line.ID] || line.DetailType != "AccountBasedExpenseLineDetail" || !identifier.MatchString(line.AccountBasedExpenseLineDetail.AccountRef.Value) || !ok || amount.Sign() <= 0 || !new(big.Rat).Mul(amount, big.NewRat(100, 1)).IsInt() {
			return false
		}
		seen[line.ID] = true
		sum.Add(sum, amount)
	}
	return sum.Cmp(total) == 0
}

func (s Sandbox) ReadBill(ctx context.Context, id string) (Bill, error) {
	if !identifier.MatchString(id) {
		return Bill{}, errors.New("An exact Bill ID is required")
	}
	var result struct{ Bill Bill }
	if err := s.request(ctx, "GET", "/bill/"+id, nil, &result); err != nil {
		return Bill{}, err
	}
	if result.Bill.ID != id || !simpleBill(result.Bill, s.currency()) {
		return Bill{}, errors.New("Only a current, unpaid Bill in the client's currency with up to ten simple account-based expense lines is supported")
	}
	return result.Bill, nil
}

func (s Sandbox) ValidateExpenseAccount(ctx context.Context, id string) (string, error) {
	if !identifier.MatchString(id) || s.Production && !slices.Contains(s.Accounts, id) {
		return "", errors.New("Choose an exact expense account allowed by this client's production grant")
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
	if a.ID != id || !a.Active || a.Classification != "Expense" || a.Name == "" || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != s.currency()) {
		return "", errors.New("Target account is not an active expense account in the client's currency")
	}
	return a.Name, nil
}

func (s Sandbox) CorrectBillAccount(ctx context.Context, before Bill, lineID, target string) (Bill, error) {
	if !simpleBill(before, s.currency()) || !identifier.MatchString(target) {
		return Bill{}, errors.New("The approved Bill correction is invalid")
	}
	lines := append([]BillLine(nil), before.Line...)
	found := false
	for i := range lines {
		if lines[i].ID == lineID {
			if lines[i].AccountBasedExpenseLineDetail.AccountRef.Value == target {
				return Bill{}, errors.New("Target account must differ from the selected Bill line")
			}
			// A recode moves the line out of one account and into another; a production grant must allow both.
			if s.Production && (!slices.Contains(s.Accounts, target) || !slices.Contains(s.Accounts, lines[i].AccountBasedExpenseLineDetail.AccountRef.Value)) {
				return Bill{}, errors.New("Both the Bill line's current and target expense accounts must be allowed by this client's production grant")
			}
			lines[i].AccountBasedExpenseLineDetail.AccountRef = Reference{target}
			found = true
		}
	}
	if !found {
		return Bill{}, errors.New("Selected Bill line is missing")
	}
	body := map[string]any{"Id": before.ID, "SyncToken": before.SyncToken, "sparse": true, "Line": lines}
	var response struct{ Bill Bill }
	if err := s.request(ctx, "POST", "/bill", body, &response); err != nil {
		return Bill{}, err
	}
	if response.Bill.ID != before.ID {
		return Bill{}, errors.New("QuickBooks Bill update response has a different identity")
	}
	return response.Bill, nil
}

func (s Sandbox) VerifyBillCorrection(ctx context.Context, before Bill, lineID, target string) (Bill, error) {
	got, err := s.ReadBill(ctx, before.ID)
	if err != nil {
		return Bill{}, err
	}
	if got.SyncToken == before.SyncToken || len(got.Line) != len(before.Line) {
		return Bill{}, errors.New("Bill readback version or line count differs from approval")
	}
	expected := before
	expected.SyncToken = got.SyncToken
	expected.Line = append([]BillLine(nil), before.Line...)
	for _, pair := range [][2]json.Number{{got.TotalAmt, before.TotalAmt}, {got.Balance, before.Balance}} {
		a, okA := new(big.Rat).SetString(string(pair[0]))
		b, okB := new(big.Rat).SetString(string(pair[1]))
		if !okA || !okB || a.Cmp(b) != 0 {
			return Bill{}, errors.New("Bill readback amount differs from approval")
		}
	}
	expected.TotalAmt, expected.Balance = got.TotalAmt, got.Balance
	found := false
	for i := range expected.Line {
		a, okA := new(big.Rat).SetString(string(got.Line[i].Amount))
		b, okB := new(big.Rat).SetString(string(expected.Line[i].Amount))
		if !okA || !okB || a.Cmp(b) != 0 {
			return Bill{}, errors.New("Bill readback line amount differs from approval")
		}
		expected.Line[i].Amount = got.Line[i].Amount
		if expected.Line[i].ID == lineID {
			expected.Line[i].AccountBasedExpenseLineDetail.AccountRef = Reference{target}
			found = true
		}
	}
	if !found || !reflect.DeepEqual(expected, got) {
		return Bill{}, errors.New("Bill readback differs from the selected account change or preserved lines")
	}
	return got, nil
}
