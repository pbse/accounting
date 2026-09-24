package quickbooks

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
)

type BalanceException struct {
	Type      string `json:"type"`
	AccountID string `json:"account_id"`
	Account   string `json:"account"`
	Side      string `json:"side"`
	Amount    string `json:"amount"`
	Finding   string `json:"finding"`
	NextStep  string `json:"next_step"`
	Question  string `json:"question"`
}

// Classification by QuickBooks account type; unknown types are skipped rather than guessed.
var accountTypes = map[string]string{
	"Bank": "Asset", "Accounts Receivable": "Asset", "Other Current Asset": "Asset", "Fixed Asset": "Asset", "Other Asset": "Asset",
	"Accounts Payable": "Liability", "Credit Card": "Liability", "Other Current Liability": "Liability", "Long Term Liability": "Liability",
	"Equity": "Equity", "Income": "Revenue", "Other Income": "Revenue",
	"Expense": "Expense", "Other Expense": "Expense", "Cost of Goods Sold": "Expense",
}

// Contra and informational accounts that normally sit on the opposite side.
var oppositeSubtypes = []string{"AccumulatedDepreciation", "AccumulatedAmortization", "AccumulatedAmortizationOfOtherAssets", "AccumulatedDepletion", "RetainedEarnings"}

// AbnormalBalances lists Trial Balance accounts whose balance sits opposite their normal side.
// It reads the report's Debit and Credit columns; each item is a candidate for review, not a conclusion.
func AbnormalBalances(accounts, trialBalance json.RawMessage) ([]BalanceException, error) {
	var directory []struct {
		ID                                                string `json:"Id"`
		Name, Classification, AccountType, AccountSubType string
	}
	if err := json.Unmarshal(accounts, &directory); err != nil {
		return nil, fmt.Errorf("account directory is invalid")
	}
	type info struct{ name, class, subtype string }
	byID := map[string]info{}
	for _, a := range directory {
		class := a.Classification
		if class == "" {
			class = accountTypes[a.AccountType]
		}
		byID[a.ID] = info{a.Name, class, a.AccountSubType}
	}
	var report struct {
		Columns struct {
			Column []struct{ ColTitle string }
		}
		Rows reportRows
	}
	if err := json.Unmarshal(trialBalance, &report); err != nil {
		return nil, fmt.Errorf("trial balance is invalid")
	}
	debit, credit := -1, -1
	for i, column := range report.Columns.Column {
		switch strings.TrimSpace(column.ColTitle) {
		case "Debit":
			debit = i
		case "Credit":
			credit = i
		}
	}
	if debit < 1 || credit < 1 {
		return nil, fmt.Errorf("trial balance has no Debit and Credit columns")
	}
	out := []BalanceException{}
	var visit func(reportRows)
	visit = func(group reportRows) {
		for _, row := range group.Row {
			visit(row.Rows)
			if row.Type != "Data" || len(row.ColData) <= max(debit, credit) || row.ColData[0].ID == "" {
				continue
			}
			account, known := byID[row.ColData[0].ID]
			if !known || account.class == "" || slices.Contains(oppositeSubtypes, account.subtype) {
				continue
			}
			lower := strings.ToLower(account.name)
			if account.class == "Equity" && (strings.Contains(lower, "draw") || strings.Contains(lower, "distribution")) {
				continue
			}
			net := new(big.Rat)
			valid := true
			for index, sign := range map[int]int64{debit: 1, credit: -1} {
				value := strings.ReplaceAll(strings.TrimSpace(row.ColData[index].Value), ",", "")
				if value == "" {
					continue
				}
				amount, ok := new(big.Rat).SetString(value)
				if !ok {
					valid = false
					break
				}
				net.Add(net, amount.Mul(amount, big.NewRat(sign, 1)))
			}
			debitNormal := account.class == "Asset" || account.class == "Expense"
			if !valid || net.Sign() == 0 || (net.Sign() > 0) == debitNormal {
				continue
			}
			side := "credit"
			if net.Sign() > 0 {
				side = "debit"
			}
			amount := new(big.Rat).Abs(net).FloatString(2)
			out = append(out, BalanceException{Type: "abnormal_balance", AccountID: row.ColData[0].ID, Account: account.name, Side: side, Amount: amount,
				Finding:  fmt.Sprintf("%s (%s) shows a $%s %s balance, opposite its normal side.", account.name, account.class, amount, side),
				NextStep: "Cause unverified: review this account's activity in the captured General Ledger before any correction. Common causes are a misposted payment, a refund or a duplicate. Nothing is changed.",
				Question: fmt.Sprintf("Our records show a $%s %s balance in %s at month-end. Do you know of an advance, refund or overpayment that explains it?", amount, side, account.name)})
		}
	}
	visit(report.Rows)
	slices.SortFunc(out, func(a, b BalanceException) int { return strings.Compare(a.Account, b.Account) })
	return out, nil
}
