package quickbooks

import (
	"encoding/json"
	"math/big"
	"sort"
	"strings"
	"time"
)

// AgingLine is one open document: its balance at capture time, aged against the close's month-end by due date
// (invoices and bills) or kept apart as a credit (payments, credit memos, supplier credits).
type AgingLine struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Number  string `json:"number"`
	Date    string `json:"date"`
	Due     string `json:"due,omitempty"`
	Balance string `json:"balance,omitempty"`
	Days    *int   `json:"days_past_due,omitempty"`
	Problem string `json:"problem,omitempty"`
}

// AgingParty groups one customer's or vendor's open documents in one currency, by provider ID.
type AgingParty struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Currency string      `json:"currency"`
	Buckets  [5]string   `json:"buckets"`
	Unaged   string      `json:"unaged"`
	Credits  string      `json:"credits"`
	Open     string      `json:"open"`
	Lines    []AgingLine `json:"lines"`
	// Unresolved counts documents whose balance could not be read; totals then leave them out.
	Unresolved int `json:"unresolved"`
}

// Buckets are current, 1–30, 31–60, 61–90 and over 90 days past due at month-end.
var AgingBuckets = [5]string{"Current", "1–30 days", "31–60 days", "61–90 days", "Over 90 days"}

// Aging arranges a capture's open items into receivables by customer and payables by vendor. Credits stay apart
// from the buckets: netting them in the summary is not evidence of what they settle. A document without a currency
// takes home, when known; one without a party stays on its own; unreadable counts documents that could not be read.
func Aging(open map[string][]json.RawMessage, monthEnd time.Time, home string) (receivables, payables []AgingParty, unreadable int) {
	type sums struct {
		party   *AgingParty
		buckets [5]*big.Rat
		unaged  *big.Rat
		credits *big.Rat
	}
	groups := map[string]*sums{}
	get := func(side string, r ref, currency, document string) *sums {
		key := side + "|" + r.Value + "|" + currency
		if r.Value == "" {
			key += "|" + document
		}
		if groups[key] == nil {
			s := &sums{party: &AgingParty{ID: r.Value, Name: r.Name, Currency: currency, Lines: []AgingLine{}}, unaged: new(big.Rat), credits: new(big.Rat)}
			for i := range s.buckets {
				s.buckets[i] = new(big.Rat)
			}
			groups[key] = s
		}
		return groups[key]
	}
	for _, kind := range []string{"Invoice", "Payment", "CreditMemo", "Bill", "VendorCredit"} {
		for _, raw := range open[kind] {
			// Amounts stay raw so one unreadable balance is reported rather than dropping the document.
			var item struct {
				ID                          string `json:"Id"`
				DocNumber, DueDate, TxnDate string
				Balance, UnappliedAmt       json.RawMessage
				RemainingCredit             json.RawMessage
				CustomerRef, VendorRef      ref
				CurrencyRef                 ref
			}
			if json.Unmarshal(raw, &item) != nil {
				unreadable++
				continue
			}
			side, party := "receivable", item.CustomerRef
			if kind == "Bill" || kind == "VendorCredit" {
				side, party = "payable", item.VendorRef
			}
			value := map[string]json.RawMessage{"Payment": item.UnappliedAmt, "CreditMemo": item.RemainingCredit}[kind]
			if value == nil {
				value = item.Balance
			}
			currency := item.CurrencyRef.Value
			if currency == "" {
				currency = home
			}
			s := get(side, party, currency, kind+item.ID)
			line := AgingLine{Kind: kind, ID: item.ID, Number: item.DocNumber, Date: item.TxnDate, Due: item.DueDate}
			// Each balance is rounded to cents before it is added, so lines and totals agree.
			balance, ok := new(big.Rat).SetString(strings.Trim(string(value), `"`))
			if ok {
				balance.SetString(balance.FloatString(2))
			}
			switch {
			case !ok:
				line.Problem = "balance unreadable; left out of the totals"
				s.party.Unresolved++
			case balance.Sign() <= 0:
				continue
			case kind == "Payment" || kind == "CreditMemo" || kind == "VendorCredit":
				line.Balance = balance.FloatString(2)
				s.credits.Add(s.credits, balance)
			default:
				line.Balance = balance.FloatString(2)
				due, err := time.Parse("2006-01-02", item.DueDate)
				if err != nil {
					line.Problem = "no due date; counted as unaged"
					s.unaged.Add(s.unaged, balance)
					break
				}
				days := int(monthEnd.Sub(due).Hours() / 24)
				line.Days = &days
				bucket := 0
				switch {
				case days > 90:
					bucket = 4
				case days > 60:
					bucket = 3
				case days > 30:
					bucket = 2
				case days > 0:
					bucket = 1
				}
				s.buckets[bucket].Add(s.buckets[bucket], balance)
			}
			s.party.Lines = append(s.party.Lines, line)
		}
	}
	for key, s := range groups {
		if len(s.party.Lines) == 0 {
			continue
		}
		open := new(big.Rat).Set(s.unaged)
		for i, b := range s.buckets {
			s.party.Buckets[i] = b.FloatString(2)
			open.Add(open, b)
		}
		s.party.Unaged, s.party.Credits, s.party.Open = s.unaged.FloatString(2), s.credits.FloatString(2), open.FloatString(2)
		if strings.HasPrefix(key, "receivable|") {
			receivables = append(receivables, *s.party)
		} else {
			payables = append(payables, *s.party)
		}
	}
	for _, list := range [][]AgingParty{receivables, payables} {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Name != list[j].Name {
				return list[i].Name < list[j].Name
			}
			return list[i].ID+list[i].Currency < list[j].ID+list[j].Currency
		})
	}
	return receivables, payables, unreadable
}
