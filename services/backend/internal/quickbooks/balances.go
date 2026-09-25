package quickbooks

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"close.local/backend/internal/ledger"
)

// Classification by QuickBooks account type; unknown types are skipped rather than guessed.
var accountTypes = map[string]string{
	"Bank": "Asset", "Accounts Receivable": "Asset", "Other Current Asset": "Asset", "Fixed Asset": "Asset", "Other Asset": "Asset",
	"Accounts Payable": "Liability", "Credit Card": "Liability", "Other Current Liability": "Liability", "Long Term Liability": "Liability",
	"Equity": "Equity", "Income": "Revenue", "Other Income": "Revenue",
	"Expense": "Expense", "Other Expense": "Expense", "Cost of Goods Sold": "Expense",
}

// Contra and informational accounts that normally sit on the opposite side.
var oppositeSubtypes = []string{"AccumulatedDepreciation", "AccumulatedAmortization", "AccumulatedAmortizationOfOtherAssets", "AccumulatedDepletion", "RetainedEarnings"}

// TrialBalance returns each report account's net; an account missing from the directory has no class.
func TrialBalance(accounts, trialBalance json.RawMessage) (string, []ledger.AccountBalance, error) {
	var directory []struct {
		ID                                                                    string `json:"Id"`
		Name, FullyQualifiedName, Classification, AccountType, AccountSubType string
	}
	if err := json.Unmarshal(accounts, &directory); err != nil {
		return "", nil, fmt.Errorf("account directory is invalid")
	}
	byID := map[string]ledger.AccountBalance{}
	for _, a := range directory {
		class := a.Classification
		if class == "" {
			class = accountTypes[a.AccountType]
		}
		byID[a.ID] = ledger.AccountBalance{ID: a.ID, Name: a.Name, FullName: a.FullyQualifiedName, Class: class, AccountType: a.AccountType, Subtype: a.AccountSubType}
	}
	var report struct {
		Header  struct{ Currency string }
		Columns struct {
			Column []struct{ ColTitle string }
		}
		Rows reportRows
	}
	if err := json.Unmarshal(trialBalance, &report); err != nil {
		return "", nil, fmt.Errorf("trial balance is invalid")
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
		return "", nil, fmt.Errorf("trial balance has no Debit and Credit columns")
	}
	out := []ledger.AccountBalance{}
	var invalid error
	var visit func(reportRows)
	visit = func(group reportRows) {
		for _, row := range group.Row {
			visit(row.Rows)
			if row.Type != "Data" || len(row.ColData) <= max(debit, credit) {
				continue
			}
			account, known := byID[row.ColData[0].ID]
			if !known {
				account = ledger.AccountBalance{ID: row.ColData[0].ID, Name: row.ColData[0].Value}
			}
			net, err := ledger.Net(row.ColData[debit].Value, row.ColData[credit].Value)
			if err != nil {
				invalid = fmt.Errorf("trial balance %v for %s", err, account.Name)
				return
			}
			account.Net = net
			out = append(out, account)
		}
	}
	visit(report.Rows)
	if invalid != nil {
		return "", nil, invalid
	}
	return report.Header.Currency, out, nil
}

// AbnormalBalances lists Trial Balance accounts whose balance sits opposite their normal side.
// Each item is a candidate for review, not a conclusion.
func AbnormalBalances(accounts, trialBalance json.RawMessage) ([]ledger.BalanceException, error) {
	currency, rows, err := TrialBalance(accounts, trialBalance)
	if err != nil {
		return nil, err
	}
	out := []ledger.BalanceException{}
	for _, account := range rows {
		lower := strings.ToLower(account.Name)
		if account.Class == "" || slices.Contains(oppositeSubtypes, account.Subtype) || account.Class == "Equity" && (strings.Contains(lower, "draw") || strings.Contains(lower, "distribution")) {
			continue
		}
		if item := ledger.Holding(account.ID, account.Name, account.Class, account.Net, currency, "balance"); item != nil {
			out = append(out, *item)
		} else if item := ledger.WrongSide(account.ID, account.Name, account.Class, account.Net, currency, "balance"); item != nil {
			out = append(out, *item)
		}
	}
	slices.SortFunc(out, func(a, b ledger.BalanceException) int { return strings.Compare(a.Account, b.Account) })
	return out, nil
}
