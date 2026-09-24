// Package ledger holds checks shared by the QuickBooks and Xero readers.
package ledger

import (
	"fmt"
	"math/big"
	"strings"
)

type BalanceException struct {
	Type      string `json:"type"`
	AccountID string `json:"account_id"`
	Account   string `json:"account"`
	Side      string `json:"side"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	Finding   string `json:"finding"`
	NextStep  string `json:"next_step"`
	Question  string `json:"question"`
}

// WrongSide returns a review item when net (debit minus credit) sits opposite the class's
// normal side. Asset and Expense are debit-normal; Liability, Equity and Revenue credit-normal.
// basis names what net is, such as "balance" or "year-to-date balance".
func WrongSide(accountID, name, class string, net *big.Rat, currency, basis string) *BalanceException {
	debitNormal := class == "Asset" || class == "Expense"
	if net.Sign() == 0 || (net.Sign() > 0) == debitNormal {
		return nil
	}
	side := "credit"
	if net.Sign() > 0 {
		side = "debit"
	}
	amount := new(big.Rat).Abs(net).FloatString(2)
	shown := amount + " " + currency
	if currency == "" || currency == "USD" {
		shown = "$" + amount
	}
	return &BalanceException{Type: "abnormal_balance", AccountID: accountID, Account: name, Side: side, Amount: amount, Currency: currency,
		Finding:  fmt.Sprintf("%s (%s) shows a %s %s %s, opposite its normal side.", name, class, shown, side, basis),
		NextStep: "Cause unverified: review this account's activity in the ledger before any correction. Common causes are a misposted payment, a refund or a duplicate. Nothing is changed.",
		Question: fmt.Sprintf("Our records show a %s %s %s in %s at month-end. Do you know of an advance, refund or overpayment that explains it?", shown, side, basis, name)}
}

// Net returns debit minus credit from report cells; a blank cell counts as zero.
func Net(debit, credit string) (*big.Rat, error) {
	net := new(big.Rat)
	for i, value := range []string{debit, credit} {
		value = strings.ReplaceAll(strings.TrimSpace(value), ",", "")
		if value == "" {
			continue
		}
		amount, ok := new(big.Rat).SetString(value)
		if !ok {
			return nil, fmt.Errorf("amount %q is not a number", value)
		}
		if i == 1 {
			amount.Neg(amount)
		}
		net.Add(net, amount)
	}
	return net, nil
}
