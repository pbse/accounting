package seed

import (
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"close.local/backend/internal/quickbooks"
)

// Every client-month's books balance and read back through the code a real capture is read with.
func TestEveryMonthBalancesAndReadsAsACapture(t *testing.T) {
	firm := Build()
	for _, c := range firm.Clients {
		books := BooksFor(&c, Months[len(Months)-1])
		for i, b := range books {
			closing := b.Closing()
			total := new(big.Rat)
			for _, v := range closing {
				total.Add(total, v)
			}
			if total.Sign() != 0 {
				t.Errorf("%s %s: trial balance is off by %s", c.Name, b.Period, total.FloatString(2))
			}
			currency, balances, err := quickbooks.TrialBalance(b.Directory(), b.TrialBalance())
			if err != nil || currency != "USD" || len(balances) == 0 {
				t.Fatalf("%s %s: trial balance unreadable: %v", c.Name, b.Period, err)
			}
			if _, activity, err := quickbooks.MonthlyActivity(b.ProfitAndLoss()); err != nil || len(activity) == 0 {
				t.Fatalf("%s %s: profit and loss unreadable: %v", c.Name, b.Period, err)
			}
			if _, totals, err := quickbooks.CashFlow(b.CashFlow()); err != nil {
				t.Fatalf("%s %s: cash flow unreadable: %v", c.Name, b.Period, err)
			} else {
				bank := new(big.Rat)
				for _, a := range b.Accounts {
					if a.Type == "Bank" {
						if v := closing[a.ID]; v != nil {
							bank.Add(bank, v)
						}
					}
				}
				if totals["EndingCash"].Cmp(bank) != 0 {
					t.Errorf("%s %s: cash flow ends at %s but the bank holds %s", c.Name, b.Period, totals["EndingCash"].FloatString(2), bank.FloatString(2))
				}
			}
			if _, _, err := quickbooks.PeriodSnapshot("production", "seed", c.Name, b.Period, b.Directory(), b.Reports(), time.Now()); err != nil {
				t.Fatalf("%s %s: snapshot refused: %v", c.Name, b.Period, err)
			}
			if _, opening, closing, err := quickbooks.LedgerLines(b.Directory(), b.GeneralLedger(), b.TrialBalance(), "10", b.Period); err != nil || opening == "" || closing == "" {
				t.Fatalf("%s %s: the captured ledger cannot stand in for the bank register: %v", c.Name, b.Period, err)
			}
			if _, err := quickbooks.PayeeActivity(b.GeneralLedger(), []string{"70"}); err != nil {
				t.Fatalf("%s %s: general ledger unreadable: %v", c.Name, b.Period, err)
			}
			for _, txn := range b.Txns {
				if !json.Valid(b.Record(txn)) {
					t.Fatalf("%s %s: record %s invalid", c.Name, b.Period, txn.ID)
				}
			}
			if i > 0 && len(b.OpenItems(books[:i])) == 0 {
				t.Fatalf("%s %s: no open items map", c.Name, b.Period)
			}
			for _, w := range c.WorkFor(b.Period, b) {
				if w.File == "" || w.Text == "" || (w.Answer == nil && !w.NoAnswer) {
					t.Errorf("%s %s: work %s incomplete", c.Name, b.Period, w.Kind)
				}
			}
		}
	}
}
