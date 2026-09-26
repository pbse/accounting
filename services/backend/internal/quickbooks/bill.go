package quickbooks

import (
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
