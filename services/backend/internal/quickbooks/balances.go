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

// AbnormalBalances lists Trial Balance accounts whose balance sits opposite their normal side.
// It reads the report's Debit and Credit columns; each item is a candidate for review, not a conclusion.
func AbnormalBalances(accounts, trialBalance json.RawMessage) ([]ledger.BalanceException, error) {
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
		Header  struct{ Currency string }
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
	out := []ledger.BalanceException{}
	var invalid error
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
			net, err := ledger.Net(row.ColData[debit].Value, row.ColData[credit].Value)
			if err != nil {
				invalid = fmt.Errorf("trial balance %v for %s", err, account.name)
				return
			}
			if item := ledger.WrongSide(row.ColData[0].ID, account.name, account.class, net, report.Header.Currency, "balance"); item != nil {
				out = append(out, *item)
			}
		}
	}
	visit(report.Rows)
	if invalid != nil {
		return nil, invalid
	}
	slices.SortFunc(out, func(a, b ledger.BalanceException) int { return strings.Compare(a.Account, b.Account) })
	return out, nil
}
