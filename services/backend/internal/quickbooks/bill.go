package quickbooks

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
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

func simpleBill(b Bill) bool {
	if !identifier.MatchString(b.ID) || b.SyncToken == "" || b.TxnDate == "" || !identifier.MatchString(b.VendorRef.Value) || b.CurrencyRef.Value != "USD" || len(b.Line) != 1 || len(b.LinkedTxn) > 0 || len(b.TxnTaxDetail) > 0 {
		return false
	}
	line := b.Line[0]
	if !identifier.MatchString(line.ID) || line.DetailType != "AccountBasedExpenseLineDetail" || !identifier.MatchString(line.AccountBasedExpenseLineDetail.AccountRef.Value) {
		return false
	}
	amount, ok := new(big.Rat).SetString(string(line.Amount))
	total, totalOK := new(big.Rat).SetString(string(b.TotalAmt))
	balance, balanceOK := new(big.Rat).SetString(string(b.Balance))
	return ok && totalOK && balanceOK && amount.Sign() > 0 && amount.Cmp(total) == 0 && balance.Cmp(total) == 0
}

func (s Sandbox) ReadBill(ctx context.Context, id string) (Bill, error) {
	if s.Production || !identifier.MatchString(id) {
		return Bill{}, errors.New("An exact sandbox Bill ID is required")
	}
	var result struct{ Bill Bill }
	if err := s.request(ctx, "GET", "/bill/"+id, nil, &result); err != nil {
		return Bill{}, err
	}
	if result.Bill.ID != id || !simpleBill(result.Bill) {
		return Bill{}, errors.New("Only a current, unpaid USD Bill with one simple account-based expense line is supported")
	}
	return result.Bill, nil
}

func (s Sandbox) ValidateExpenseAccount(ctx context.Context, id string) (string, error) {
	if s.Production || !identifier.MatchString(id) {
		return "", errors.New("An exact sandbox expense account ID is required")
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
	if a.ID != id || !a.Active || a.Classification != "Expense" || a.Name == "" || (a.CurrencyRef.Value != "" && a.CurrencyRef.Value != "USD") {
		return "", errors.New("Target account is not an active USD expense account")
	}
	return a.Name, nil
}

func (s Sandbox) CorrectBillAccount(ctx context.Context, before Bill, target string) (Bill, error) {
	if s.Production || !simpleBill(before) || !identifier.MatchString(target) || target == before.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value {
		return Bill{}, errors.New("The approved sandbox Bill correction is invalid")
	}
	line := before.Line[0]
	line.AccountBasedExpenseLineDetail.AccountRef = Reference{target}
	body := map[string]any{"Id": before.ID, "SyncToken": before.SyncToken, "sparse": true, "Line": []BillLine{line}}
	var response struct{ Bill Bill }
	if err := s.request(ctx, "POST", "/bill", body, &response); err != nil {
		return Bill{}, err
	}
	if response.Bill.ID != before.ID {
		return Bill{}, errors.New("QuickBooks Bill update response has a different identity")
	}
	return response.Bill, nil
}

func (s Sandbox) VerifyBillCorrection(ctx context.Context, before Bill, target string) (Bill, error) {
	got, err := s.ReadBill(ctx, before.ID)
	if err != nil {
		return Bill{}, err
	}
	if got.SyncToken == before.SyncToken || got.TxnDate != before.TxnDate || got.VendorRef != before.VendorRef || got.CurrencyRef != before.CurrencyRef || len(got.Line) != 1 || got.Line[0].ID != before.Line[0].ID || got.Line[0].DetailType != before.Line[0].DetailType || got.Line[0].Description != before.Line[0].Description || got.Line[0].AccountBasedExpenseLineDetail.AccountRef.Value != target {
		return Bill{}, errors.New("Bill readback differs from the approved identity, date, vendor, line or account")
	}
	for _, pair := range [][2]json.Number{{got.TotalAmt, before.TotalAmt}, {got.Balance, before.Balance}, {got.Line[0].Amount, before.Line[0].Amount}} {
		a, okA := new(big.Rat).SetString(string(pair[0]))
		b, okB := new(big.Rat).SetString(string(pair[1]))
		if !okA || !okB || a.Cmp(b) != 0 {
			return Bill{}, errors.New("Bill readback amount differs from approval")
		}
	}
	return got, nil
}
