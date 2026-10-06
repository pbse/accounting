package xero

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"close.local/backend/internal/quickbooks"
)

// Coverage proposes the evidence each account of a Xero capture needs, as quickbooks.Coverage does for a QuickBooks
// capture: a bank or credit-card account (Xero type BANK) is reconciled, a prepayment account is a prepaid schedule, a
// current liability named as an accrual is an accrual, and every other account is the accountant's to review or exclude.
// Every account in the capture is in scope, archived ones too, and so is an account only the Trial Balance reports. It
// proposes needs; it never proves work required, satisfied or immaterial.
func Coverage(payload json.RawMessage) ([]quickbooks.CoverageItem, error) {
	var snapshot struct {
		Accounts struct {
			Accounts []struct{ AccountID, Name, Type string }
		}
		TrialBalance struct {
			Reports []struct{ Rows []row }
		} `json:"trial_balance"`
	}
	if json.Unmarshal(payload, &snapshot) != nil || len(snapshot.TrialBalance.Reports) != 1 {
		return nil, errors.New("Xero snapshot is unreadable")
	}
	// An account inventory the capture cannot vouch for would drop accounts the Trial Balance does not list: every
	// account needs its own ID.
	seen := map[string]bool{}
	for _, a := range snapshot.Accounts.Accounts {
		if a.AccountID == "" || seen[a.AccountID] {
			return nil, errors.New("Xero snapshot's account list has an account without its own ID")
		}
		seen[a.AccountID] = true
	}
	if len(seen) == 0 {
		return nil, errors.New("Xero snapshot has no account list")
	}
	items := []quickbooks.CoverageItem{}
	byID := map[string]int{}
	for index, a := range snapshot.Accounts.Accounts {
		kind := "accountant_review"
		switch {
		case a.Type == "BANK":
			kind = "reconciliation"
		case a.Type == "PREPAYMENT":
			kind = "prepaid"
		case a.Type == "CURRLIAB" && strings.Contains(strings.ToLower(a.Name), "accru"):
			kind = "accrual"
		}
		items = append(items, quickbooks.NewCoverageItem(a.AccountID, a.Name, kind, fmt.Sprintf("accounts.Accounts[%d]", index)))
		byID[a.AccountID] = index
	}
	// Trial Balance rows link to their accounts by the cell attribute Xero names "account".
	var visit func([]row, string)
	visit = func(group []row, path string) {
		for index, r := range group {
			location := fmt.Sprintf("%s[%d]", path, index)
			if r.RowType == "Row" && len(r.Cells) > 0 {
				for _, attribute := range r.Cells[0].Attributes {
					if attribute.ID != "account" || attribute.Value == "" {
						continue
					}
					if i, found := byID[attribute.Value]; found {
						items[i].ReportLocations = append(items[i].ReportLocations, location)
					} else {
						byID[attribute.Value] = len(items)
						items = append(items, quickbooks.MissingAccount(attribute.Value, r.Cells[0].Value, location))
					}
				}
			}
			visit(r.Rows, location+".Rows")
		}
	}
	visit(snapshot.TrialBalance.Reports[0].Rows, "trial_balance.Reports[0].Rows")
	return items, nil
}
