package quickbooks

import (
	"close.local/backend/internal/ledger"
	"encoding/json"
	"fmt"
	"maps"
	"math/big"
	"regexp"
	"slices"
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
	Reference    string   `json:"reference,omitempty"`
	Currency     string   `json:"currency,omitempty"`
	Finding      string   `json:"finding"`
	NextStep     string   `json:"next_step"`
	Question     string   `json:"question"`
}

var holdingAccount = regexp.MustCompile(`(?i)uncategori[sz]ed|ask my accountant|suspense`)

func amount(row TransactionRow) string {
	return ledger.Show(row.Amount, row.Currency)
}

type ref struct{ Value, Name, Type string }

// FindExceptions raises questions from captured ledger rows. It never concludes that
// a transaction is wrong, a duplicate or personal; each item needs evidence or an answer.
func FindExceptions(rows []TransactionRow) []Exception {
	type parsed struct {
		row          TransactionRow
		counterparty ref
		account      string
		coded        []string
		date         time.Time
	}
	groups := map[string][]parsed{}
	coding := map[string][]parsed{}
	accountNames := map[string]string{}
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
				if candidate == record.VendorRef {
					p.counterparty.Type = "Vendor"
				}
				break
			}
		}
		for _, line := range record.Line {
			for key, raw := range line {
				var detail struct{ AccountRef ref }
				if !strings.HasSuffix(key, "Detail") || json.Unmarshal(raw, &detail) != nil || detail.AccountRef.Name == "" {
					continue
				}
				if holdingAccount.MatchString(detail.AccountRef.Name) {
					if p.account == "" {
						p.account = detail.AccountRef.Name
					}
				} else if key == "AccountBasedExpenseLineDetail" && detail.AccountRef.Value != "" && !slices.Contains(p.coded, detail.AccountRef.Value) {
					p.coded = append(p.coded, detail.AccountRef.Value)
					accountNames[detail.AccountRef.Value] = detail.AccountRef.Name
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
				Finding:  fmt.Sprintf("%s %s for %s with %s is posted to %s.", row.Kind, row.ProviderID, amount(row), who, p.account),
				NextStep: "Missing: the receipt or client answer showing what it paid for. Then choose the account under the firm's procedure; nothing has been recategorized.",
				Question: fmt.Sprintf("What was the %s transaction with %s on %s for?", amount(row), who, row.Date)})
		}
		if row.Kind == "Purchase" && p.counterparty.Value == "" {
			out = append(out, Exception{Type: "no_payee", Kind: row.Kind, ProviderIDs: []string{row.ProviderID}, Dates: []string{row.Date}, Amount: row.Amount,
				Finding:  fmt.Sprintf("Purchase %s for %s on %s has no payee.", row.ProviderID, amount(row), row.Date),
				NextStep: "Missing: the receipt or bank detail naming who was paid. Its category is unconfirmed until then.",
				Question: fmt.Sprintf("Who was paid %s on %s, and what was it for?", amount(row), row.Date)})
		}
		if (row.Kind == "Purchase" || row.Kind == "Bill") && p.counterparty.Value != "" && len(p.coded) > 0 {
			slices.Sort(p.coded)
			payee := p.counterparty.Type + "\x00" + p.counterparty.Value
			coding[payee] = append(coding[payee], p)
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
			for end < len(group) && group[end].date.Sub(group[start].date) <= 3*24*time.Hour {
				end++
			}
			if end-start > 1 {
				first := group[start]
				item := Exception{Type: "possible_duplicate", Kind: first.row.Kind, Amount: first.row.Amount, Counterparty: first.counterparty.Name}
				for _, p := range group[start:end] {
					item.ProviderIDs = append(item.ProviderIDs, p.row.ProviderID)
					item.Dates = append(item.Dates, p.row.Date)
				}
				item.Finding = fmt.Sprintf("%d %s transactions with %s for %s within three days (%s).", end-start, first.row.Kind, first.counterparty.Name, amount(first.row), strings.Join(item.Dates, ", "))
				item.NextStep = "Missing: a separate invoice or receipt for each, or the client's answer. Both may be genuine; nothing is deleted or merged."
				item.Question = fmt.Sprintf("We see %d payments of %s with %s on %s. Were they all intended?", end-start, amount(first.row), first.counterparty.Name, strings.Join(item.Dates, " and "))
				out = append(out, item)
			}
			start = end
		}
	}
	// Different transactions with one payee coded to different accounts is a question, not an error.
	for _, payee := range slices.Sorted(maps.Keys(coding)) {
		group := coding[payee]
		accounts := []string{}
		distinct := map[string]bool{}
		for _, p := range group {
			distinct[strings.Join(p.coded, "\x00")] = true
			for _, id := range p.coded {
				accounts = append(accounts, accountNames[id])
			}
		}
		if len(distinct) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].row.Date < group[j].row.Date })
		slices.Sort(accounts)
		item := Exception{Type: "inconsistent_coding", Kind: group[0].row.Kind, Counterparty: group[0].counterparty.Name, Account: strings.Join(slices.Compact(accounts), ", ")}
		for _, p := range group {
			item.ProviderIDs = append(item.ProviderIDs, p.row.ProviderID)
			item.Dates = append(item.Dates, p.row.Date)
		}
		item.Finding = fmt.Sprintf("%d transactions with %s are coded to different accounts: %s.", len(group), item.Counterparty, item.Account)
		item.NextStep = "Missing: the firm's or client's rule for which account these purchases belong in. Nothing is recategorized."
		item.Question = fmt.Sprintf("Purchases from %s went to %s this month. Which account should they use?", item.Counterparty, item.Account)
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Dates[0] < out[j].Dates[0]
	})
	return out
}

// OpenItemExceptions asks about invoices more than 60 days and bills more than 30 days past due at
// month-end. Balances are as of the capture, so an item paid since then raises nothing.
func OpenItemExceptions(open map[string][]json.RawMessage, monthEnd time.Time) []Exception {
	out := []Exception{}
	for _, kind := range []string{"Invoice", "Bill", "Payment", "CreditMemo", "VendorCredit"} {
		for _, raw := range open[kind] {
			var item struct {
				ID                          string `json:"Id"`
				DocNumber, DueDate, TxnDate string
				Balance, UnappliedAmt       json.Number
				RemainingCredit             json.Number
				CustomerRef, VendorRef      ref
				CurrencyRef                 ref
			}
			if json.Unmarshal(raw, &item) != nil {
				continue
			}
			value := map[string]json.Number{"Payment": item.UnappliedAmt, "CreditMemo": item.RemainingCredit}[kind]
			date := item.TxnDate
			if kind == "Invoice" || kind == "Bill" {
				date = item.DueDate
			}
			if value == "" {
				value = item.Balance
			}
			at, err := time.Parse("2006-01-02", date)
			balance, ok := new(big.Rat).SetString(value.String())
			if err != nil || !ok || balance.Sign() <= 0 {
				continue
			}
			days := int(monthEnd.Sub(at).Hours() / 24)
			number := item.DocNumber
			if number == "" {
				number = item.ID
			}
			shown := amount(TransactionRow{Amount: balance.FloatString(2), Currency: item.CurrencyRef.Value})
			e := Exception{Kind: kind, ProviderIDs: []string{item.ID}, Dates: []string{date}, Amount: balance.FloatString(2)}
			switch {
			case kind == "Invoice" && days > 60:
				e.Type, e.Counterparty = "past_due_receivable", item.CustomerRef.Name
				e.Finding = fmt.Sprintf("Invoice %s to %s has %s open, %d days past due at month-end.", number, e.Counterparty, shown, days)
				e.NextStep = "Missing: the client's view on collection. Nothing is written off."
				if days > 90 {
					e.NextStep += " Past 90 days, check the firm's allowance or write-off policy before the close."
				}
				e.Question = fmt.Sprintf("Is invoice %s to %s (%s, %d days past due) still collectible, and was anything agreed about payment?", number, e.Counterparty, shown, days)
			case kind == "Bill" && days > 30:
				e.Type, e.Counterparty = "past_due_payable", item.VendorRef.Name
				e.Finding = fmt.Sprintf("Bill %s from %s has %s open, %d days past due at month-end.", number, e.Counterparty, shown, days)
				e.NextStep = "Missing: whether it is still owed or was paid outside QuickBooks. Nothing is paid or changed."
				e.Question = fmt.Sprintf("Is bill %s from %s (%s, %d days past due) still owed, or was it paid outside QuickBooks?", number, e.Counterparty, shown, days)
			case kind == "Payment":
				e.Type, e.Counterparty = "unapplied_payment", item.CustomerRef.Name
				e.Finding = fmt.Sprintf("%s of the payment from %s on %s is not applied to an invoice.", shown, e.Counterparty, date)
				e.NextStep = "Missing: what it settles. Match it to the invoice it pays, or record it as a customer advance or credit. Nothing is changed."
				e.Question = fmt.Sprintf("What does the %s payment from %s on %s settle: an open invoice, or is it an advance or deposit?", shown, e.Counterparty, date)
			case kind == "CreditMemo" && days > 30:
				e.Type, e.Counterparty = "open_customer_credit", item.CustomerRef.Name
				e.Finding = fmt.Sprintf("Credit memo %s for %s has %s unused since %s.", number, e.Counterparty, shown, date)
				e.NextStep = "Missing: whether to apply it to an invoice or refund it. Nothing is changed."
				e.Question = fmt.Sprintf("Should the %s credit for %s from %s be applied to an invoice or refunded?", shown, e.Counterparty, date)
			case kind == "VendorCredit" && days > 30:
				e.Type, e.Counterparty = "open_supplier_credit", item.VendorRef.Name
				e.Finding = fmt.Sprintf("Supplier credit %s from %s has %s unused since %s.", number, e.Counterparty, shown, date)
				e.NextStep = "Missing: whether it will be applied to a bill or was refunded. Nothing is changed."
				e.Question = fmt.Sprintf("Will the %s credit from %s dated %s be applied to a bill, or was it refunded?", shown, e.Counterparty, date)
			default:
				continue
			}
			out = append(out, e)
		}
	}
	return out
}

var schedulePrompts = map[string][3]string{
	"unscheduled_prepaid":          {"a prepaid account", "Missing: the invoice or agreement showing the service period. The agent then drafts the amortization schedule for review; nothing is posted.", "What period does the %s paid to %s on %s cover? Please send the invoice or agreement."},
	"unscheduled_fixed_asset":      {"a fixed asset account", "Missing: the invoice, the date the asset was placed in service and its useful life under the firm's policy. The agent then drafts the depreciation schedule for review; nothing is posted.", "What was the %s purchase from %s on %s, and when was it placed in service? Please send the invoice."},
	"unscheduled_deferred_revenue": {"a deferred revenue account", "Missing: the contract or invoice showing when the revenue is earned. The agent then drafts the recognition schedule for review; nothing is posted.", "What service period does the %s received from %s on %s cover? Please send the contract or invoice."},
}

// ScheduleCandidates finds additions to prepaid, fixed-asset or deferred-revenue accounts; it also returns accounts missing from the directory and item lines, whose accounts are not captured.
func ScheduleCandidates(rows []TransactionRow, accounts json.RawMessage) ([]Exception, []string, int, error) {
	var directory []struct {
		ID                                string `json:"Id"`
		Name, AccountType, AccountSubType string
	}
	if err := json.Unmarshal(accounts, &directory); err != nil {
		return nil, nil, 0, fmt.Errorf("account directory is invalid")
	}
	purpose := map[string]string{}
	names := map[string]string{}
	for _, a := range directory {
		names[a.ID] = a.Name
		switch {
		case a.AccountSubType == "PrepaidExpenses":
			purpose[a.ID] = "unscheduled_prepaid"
		case a.AccountType == "Fixed Asset" && !slices.Contains(oppositeSubtypes, a.AccountSubType):
			purpose[a.ID] = "unscheduled_fixed_asset"
		case a.AccountSubType == "DeferredRevenue":
			purpose[a.ID] = "unscheduled_deferred_revenue"
		default:
			purpose[a.ID] = ""
		}
	}
	out, unknown, items := []Exception{}, []string{}, 0
	for _, row := range rows {
		if row.Kind != "Bill" && row.Kind != "Purchase" && row.Kind != "JournalEntry" && row.Kind != "Deposit" {
			continue
		}
		var record struct {
			DocNumber                         string
			Credit                            bool
			EntityRef, VendorRef, CustomerRef *ref
			Line                              []map[string]json.RawMessage
		}
		if json.Unmarshal(row.Record, &record) != nil {
			continue
		}
		net := map[string]map[string]*big.Rat{}
		for _, line := range record.Line {
			if line["ItemBasedExpenseLineDetail"] != nil {
				items++
			}
			var value json.Number
			if json.Unmarshal(line["Amount"], &value) != nil {
				continue
			}
			for key, raw := range line {
				var detail struct {
					AccountRef  ref
					PostingType string
				}
				if key != "AccountBasedExpenseLineDetail" && key != "JournalEntryLineDetail" && key != "DepositLineDetail" || json.Unmarshal(raw, &detail) != nil || detail.AccountRef.Value == "" {
					continue
				}
				kind, known := purpose[detail.AccountRef.Value]
				if !known {
					if !slices.Contains(unknown, detail.AccountRef.Name) {
						unknown = append(unknown, detail.AccountRef.Name)
					}
					continue
				}
				amount, ok := new(big.Rat).SetString(value.String())
				if kind == "" || !ok {
					continue
				}
				if key == "DepositLineDetail" || detail.PostingType == "Credit" || record.Credit {
					amount.Neg(amount)
				}
				if net[kind] == nil {
					net[kind] = map[string]*big.Rat{}
				}
				if net[kind][detail.AccountRef.Value] == nil {
					net[kind][detail.AccountRef.Value] = new(big.Rat)
				}
				net[kind][detail.AccountRef.Value].Add(net[kind][detail.AccountRef.Value], amount)
			}
		}
		who := "an unnamed party"
		for _, candidate := range []*ref{record.EntityRef, record.VendorRef, record.CustomerRef} {
			if candidate != nil && candidate.Name != "" {
				who = candidate.Name
				break
			}
		}
		for _, kind := range slices.Sorted(maps.Keys(net)) {
			// A transaction that moves or releases more than it adds to a kind of account adds nothing new.
			total := new(big.Rat)
			for _, id := range slices.Sorted(maps.Keys(net[kind])) {
				total.Add(total, net[kind][id])
			}
			if kind == "unscheduled_deferred_revenue" {
				total.Neg(total)
			}
			if total.Sign() <= 0 {
				continue
			}
			for _, id := range slices.Sorted(maps.Keys(net[kind])) {
				added := new(big.Rat).Set(net[kind][id])
				if kind == "unscheduled_deferred_revenue" {
					added.Neg(added)
				}
				if added.Sign() <= 0 {
					continue
				}
				prompt, shown := schedulePrompts[kind], ledger.Show(added.FloatString(2), row.Currency)
				out = append(out, Exception{Type: kind, Kind: row.Kind, ProviderIDs: []string{row.ProviderID}, Dates: []string{row.Date}, Amount: added.FloatString(2), Counterparty: who, Account: names[id], Reference: record.DocNumber, Currency: row.Currency,
					Finding:  fmt.Sprintf("%s %s adds %s from %s to %s, %s, and no schedule in this close cites it.", row.Kind, row.ProviderID, shown, who, names[id], prompt[0]),
					NextStep: prompt[1],
					Question: fmt.Sprintf(prompt[2], shown, who, row.Date)})
			}
		}
	}
	return out, unknown, items, nil
}
