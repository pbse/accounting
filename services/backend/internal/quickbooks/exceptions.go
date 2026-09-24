package quickbooks

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type TransactionRow struct {
	Kind       string
	ProviderID string
	Date       string
	Currency   string
	Amount     string
	Record     json.RawMessage
}

type Exception struct {
	Type         string   `json:"type"`
	Kind         string   `json:"kind"`
	ProviderIDs  []string `json:"provider_ids"`
	Dates        []string `json:"dates"`
	Amount       string   `json:"amount"`
	Counterparty string   `json:"counterparty,omitempty"`
	Account      string   `json:"account,omitempty"`
	Finding      string   `json:"finding"`
	NextStep     string   `json:"next_step"`
	Question     string   `json:"question"`
}

var holdingAccount = regexp.MustCompile(`(?i)uncategori[sz]ed|ask my accountant|suspense`)

type ref struct{ Value, Name string }

// FindExceptions raises questions from captured ledger rows. It never concludes that
// a transaction is wrong, a duplicate or personal; each item needs evidence or an answer.
func FindExceptions(rows []TransactionRow) []Exception {
	type parsed struct {
		row          TransactionRow
		counterparty ref
		account      string
		date         time.Time
	}
	groups := map[string][]parsed{}
	var out []Exception
	for _, row := range rows {
		var record struct {
			EntityRef, VendorRef, CustomerRef *ref
			Line                              []map[string]json.RawMessage
		}
		if json.Unmarshal(row.Record, &record) != nil {
			continue
		}
		p := parsed{row: row}
		for _, candidate := range []*ref{record.EntityRef, record.VendorRef, record.CustomerRef} {
			if candidate != nil && candidate.Value != "" {
				p.counterparty = *candidate
				break
			}
		}
		for _, line := range record.Line {
			for key, raw := range line {
				var detail struct{ AccountRef ref }
				if strings.HasSuffix(key, "Detail") && json.Unmarshal(raw, &detail) == nil && holdingAccount.MatchString(detail.AccountRef.Name) && p.account == "" {
					p.account = detail.AccountRef.Name
				}
			}
		}
		date, err := time.Parse("2006-01-02", row.Date)
		if err != nil {
			continue
		}
		p.date = date
		who := p.counterparty.Name
		if who == "" {
			who = "an unnamed party"
		}
		if p.account != "" {
			out = append(out, Exception{Type: "uncategorized", Kind: row.Kind, ProviderIDs: []string{row.ProviderID}, Dates: []string{row.Date}, Amount: row.Amount, Counterparty: p.counterparty.Name, Account: p.account,
				Finding:  fmt.Sprintf("%s %s for $%s with %s is posted to %s.", row.Kind, row.ProviderID, row.Amount, who, p.account),
				NextStep: "Missing: the receipt or client answer showing what it paid for. Then choose the account under the firm's procedure; nothing has been recategorized.",
				Question: fmt.Sprintf("What was the $%s transaction with %s on %s for?", row.Amount, who, row.Date)})
		}
		if row.Kind == "Purchase" && p.counterparty.Value == "" {
			out = append(out, Exception{Type: "no_payee", Kind: row.Kind, ProviderIDs: []string{row.ProviderID}, Dates: []string{row.Date}, Amount: row.Amount,
				Finding:  fmt.Sprintf("Purchase %s for $%s on %s has no payee.", row.ProviderID, row.Amount, row.Date),
				NextStep: "Missing: the receipt or bank detail naming who was paid. Its category is unconfirmed until then.",
				Question: fmt.Sprintf("Who was paid $%s on %s, and what was it for?", row.Amount, row.Date)})
		}
		if p.counterparty.Value != "" && row.Amount != "" {
			key := row.Kind + "\x00" + p.counterparty.Value + "\x00" + row.Currency + "\x00" + row.Amount
			groups[key] = append(groups[key], p)
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			if !group[i].date.Equal(group[j].date) {
				return group[i].date.Before(group[j].date)
			}
			return group[i].row.ProviderID < group[j].row.ProviderID
		})
		// Nearby equal payments are a question; monthly recurring payments are never flagged.
		for start := 0; start < len(group); {
			end := start + 1
			for end < len(group) && group[end].date.Sub(group[end-1].date) <= 3*24*time.Hour {
				end++
			}
			if end-start > 1 {
				first := group[start]
				item := Exception{Type: "possible_duplicate", Kind: first.row.Kind, Amount: first.row.Amount, Counterparty: first.counterparty.Name}
				for _, p := range group[start:end] {
					item.ProviderIDs = append(item.ProviderIDs, p.row.ProviderID)
					item.Dates = append(item.Dates, p.row.Date)
				}
				item.Finding = fmt.Sprintf("%d %s transactions with %s for $%s within three days (%s).", end-start, first.row.Kind, first.counterparty.Name, first.row.Amount, strings.Join(item.Dates, ", "))
				item.NextStep = "Missing: a separate invoice or receipt for each, or the client's answer. Both may be genuine; nothing is deleted or merged."
				item.Question = fmt.Sprintf("We see %d payments of $%s with %s on %s. Were they all intended?", end-start, first.row.Amount, first.counterparty.Name, strings.Join(item.Dates, " and "))
				out = append(out, item)
			}
			start = end
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Dates[0] < out[j].Dates[0]
	})
	return out
}
