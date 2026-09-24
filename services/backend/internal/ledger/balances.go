// Package ledger holds checks shared by the QuickBooks and Xero readers.
package ledger

import (
	"fmt"
	"math/big"
	"regexp"
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
	shown := Show(amount, currency)
	return &BalanceException{Type: "abnormal_balance", AccountID: accountID, Account: name, Side: side, Amount: amount, Currency: currency,
		Finding:  fmt.Sprintf("%s (%s) shows a %s %s %s, opposite its normal side.", name, class, shown, side, basis),
		NextStep: "Cause unverified: review this account's activity in the ledger before any correction. Common causes are a misposted payment, a refund or a duplicate. Nothing is changed.",
		Question: fmt.Sprintf("Our records show a %s %s %s in %s at month-end. Do you know of an advance, refund or overpayment that explains it?", shown, side, basis, name)}
}

var holdingName = regexp.MustCompile(`(?i)uncategori[sz]ed|ask my accountant|suspense|undeposited`)

// Holding returns a review item when a holding or clearing account still carries a balance at month-end.
// A balance may be legitimate timing, such as deposits in transit, so it asks for reconciliation, not a correction.
func Holding(accountID, name, class string, net *big.Rat, currency, basis string) *BalanceException {
	lower := strings.ToLower(name)
	clearing := (class == "Asset" || class == "Liability") && strings.Contains(lower, "clearing")
	if net.Sign() == 0 || !clearing && !holdingName.MatchString(name) {
		return nil
	}
	side := "credit"
	if net.Sign() > 0 {
		side = "debit"
	}
	amount := new(big.Rat).Abs(net).FloatString(2)
	shown := Show(amount, currency)
	finding := fmt.Sprintf("%s holds a %s %s %s at month-end. Holding and clearing accounts are normally empty once their items are placed, so this balance needs reconciliation.", name, shown, side, basis)
	if WrongSide(accountID, name, class, net, currency, basis) != nil {
		finding += " It also sits opposite the account's normal side."
	}
	next := "Review this month's ledger rows in the account and recode each to its correct account once its purpose is known. Nothing is changed."
	switch {
	case strings.Contains(lower, "undeposited"):
		next = "Review this month's ledger rows in the account and group the payments into the bank deposits they belong to; anything not on the bank statement yet is a deposit in transit. Nothing is changed."
	case clearing:
		next = "Review this month's ledger rows in the account and match each to its offsetting payroll, processor or transfer entry; what remains is timing or an error. Nothing is changed."
	}
	return &BalanceException{Type: "holding_balance", AccountID: accountID, Account: name, Side: side, Amount: amount, Currency: currency, Finding: finding, NextStep: next,
		Question: fmt.Sprintf("Our records show %s still sitting in %s at month-end. Which deposits, payments or purchases make up this amount?", shown, name)}
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
