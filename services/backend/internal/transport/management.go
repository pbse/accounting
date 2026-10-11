package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

// packAccount is one ledger account as the management pack groups it. Type is QuickBooks' account type; of a Xero
// capture's accounts, only cash accounts carry one (Bank).
type packAccount struct{ Name, Class, Type, Subtype string }

// books is one month's ledger: the month-end Trial Balance net (debit minus credit) and the month's Profit and Loss
// amount (as the report shows it) by account ID.
type books struct {
	Currency string
	Typed    bool
	Accounts map[string]packAccount
	Balances map[string]*big.Rat
	Activity map[string]*big.Rat
	// CashFlow is the ledger's own Statement of Cash Flows for the month, when the capture has one.
	CashFlow json.RawMessage
	// Imported is the firm's statement for a Xero month; Uncounted names Xero bank accounts that are not cash accounts
	// and not credit cards.
	Imported  *cashFlowImport
	Uncounted []string
	// ActivityUnavailable says why the month's activity is unknown (a month on files without a movement column, say):
	// its profit and loss is then unavailable, never shown as zero.
	ActivityUnavailable string
}

// cashFlowImport is the firm's statement of cash flows as imported, with who imported it and what they attested.
type cashFlowImport struct {
	CSV, Source, Attestation, Actor string
	At                              time.Time
}

type packLine struct {
	AccountID string  `json:"account_id,omitempty"`
	Name      string  `json:"name"`
	Amount    string  `json:"amount"`
	Prior     *string `json:"prior"`
	Change    *string `json:"change"`
}

type packSection struct {
	Name  string     `json:"name"`
	Lines []packLine `json:"lines"`
	Total packLine   `json:"total"`
}

type statement struct {
	Sections []packSection `json:"sections"`
	Measures []packLine    `json:"measures"`
}

var plGroups = []struct{ name, class string }{{"Income", "Revenue"}, {"Cost of Goods Sold", "Expense"}, {"Expense", "Expense"}, {"Other Income", "Revenue"}, {"Other Expense", "Expense"}}

var bsGroups = []struct{ name, class string }{
	{"Bank", "Asset"}, {"Accounts Receivable", "Asset"}, {"Other Current Asset", "Asset"}, {"Fixed Asset", "Asset"}, {"Other Asset", "Asset"},
	{"Accounts Payable", "Liability"}, {"Credit Card", "Liability"}, {"Other Current Liability", "Liability"}, {"Long Term Liability", "Liability"},
	{"Equity", "Equity"},
}

func money(r *big.Rat) string { return r.FloatString(2) }

// line shows amount and, when a prior month exists, its prior amount and change.
func line(id, name string, now, before *big.Rat, compared bool) packLine {
	l := packLine{AccountID: id, Name: name, Amount: money(now)}
	if compared {
		prior, change := money(before), money(new(big.Rat).Sub(now, before))
		l.Prior, l.Change = &prior, &change
	}
	return l
}

func zero() *big.Rat { return new(big.Rat) }

// orZero is m[id], or zero when the month has no amount for the account.
func orZero(m map[string]*big.Rat, id string) *big.Rat {
	if v, ok := m[id]; ok {
		return v
	}
	return zero()
}

// group is the section an account belongs to: its QuickBooks type, its class otherwise (a typed ledger's account of an
// unlisted type falls into its class's last listed section), or "" when unclassified.
func (b *books) group(id string, groups []struct{ name, class string }) string {
	a, ok := b.Accounts[id]
	if !ok {
		return ""
	}
	if !b.Typed {
		return map[string]string{"Revenue": "Income", "Expense": "Expense", "Asset": "Assets", "Liability": "Liabilities", "Equity": "Equity"}[a.Class]
	}
	for i := len(groups) - 1; i >= 0; i-- {
		if a.Type == groups[i].name {
			return a.Type
		}
	}
	for i := len(groups) - 1; i >= 0; i-- {
		if a.Class == groups[i].class {
			return groups[i].name
		}
	}
	return ""
}

// sections totals amount(id) by group, in the order given, followed by any unclassified accounts.
func sections(this, last *books, ids []string, groups []string, groupOf func(string) string, amount func(*books, string) *big.Rat) ([]packSection, map[string]*big.Rat, map[string]*big.Rat) {
	compared := last != nil
	totals, priors := map[string]*big.Rat{}, map[string]*big.Rat{}
	byGroup := map[string][]packLine{}
	for _, id := range ids {
		g := groupOf(id)
		now, before := amount(this, id), zero()
		if compared {
			before = amount(last, id)
		}
		if now.Sign() == 0 && before.Sign() == 0 {
			continue
		}
		name := cmp.Or(this.Accounts[id].Name, id)
		if compared {
			name = cmp.Or(this.Accounts[id].Name, last.Accounts[id].Name, id)
		}
		byGroup[g] = append(byGroup[g], line(id, name, now, before, compared))
		if totals[g] == nil {
			totals[g], priors[g] = zero(), zero()
		}
		totals[g].Add(totals[g], now)
		priors[g].Add(priors[g], before)
	}
	out := []packSection{}
	for _, g := range append(slices.Clone(groups), "") {
		if len(byGroup[g]) == 0 {
			continue
		}
		name := g
		if g == "" {
			name = "Unclassified"
		}
		out = append(out, packSection{Name: name, Lines: byGroup[g], Total: line("", "Total "+strings.ToLower(name), totals[g], priors[g], compared)})
	}
	return out, totals, priors
}

func sum(m map[string]*big.Rat, keys ...string) *big.Rat {
	total := zero()
	for _, k := range keys {
		if v := m[k]; v != nil {
			total.Add(total, v)
		}
	}
	return total
}

// managementPack prepares the month's Profit and Loss, Balance Sheet and working capital from the captured reports, with last month as comparison when given. Every figure comes from the provider's Trial Balance
// and Profit and Loss; nothing is estimated. Complete is false when an account is unclassified or a total does not
// reconcile, and the difference is shown.
func managementPack(this, last *books) map[string]any {
	compared := last != nil
	ids := func(pick func(*books) map[string]*big.Rat) []string {
		set := map[string]bool{}
		for _, b := range []*books{this, last} {
			if b != nil {
				for id := range pick(b) {
					set[id] = true
				}
			}
		}
		return slices.Sorted(maps.Keys(set))
	}
	complete := true
	// Each account is shown under this month's classification, or last month's when it has none this month; an
	// account classified differently in the two months is listed, so the comparison's basis is explicit.
	reclassified := []string{}
	groupOf := func(groups []struct{ name, class string }) func(string) string {
		return func(id string) string {
			g := this.group(id, groups)
			if compared {
				before := last.group(id, groups)
				if g == "" {
					return before
				}
				if before != "" && before != g && !slices.Contains(reclassified, id) {
					reclassified = append(reclassified, id)
				}
			}
			return g
		}
	}

	// Profit and Loss: the month's activity by type, then gross, operating and net income.
	plNames := []string{"Income", "Cost of Goods Sold", "Expense", "Other Income", "Other Expense"}
	if !this.Typed {
		plNames = []string{"Income", "Expense"}
	}
	plSections, pl, plPrior := sections(this, last, ids(func(b *books) map[string]*big.Rat { return b.Activity }), plNames, groupOf(plGroups), func(b *books, id string) *big.Rat { return orZero(b.Activity, id) })
	measure := func(name string, f func(m map[string]*big.Rat) *big.Rat) packLine {
		return line("", name, f(pl), f(plPrior), compared)
	}
	gross := func(m map[string]*big.Rat) *big.Rat {
		return new(big.Rat).Sub(sum(m, "Income"), sum(m, "Cost of Goods Sold"))
	}
	operating := func(m map[string]*big.Rat) *big.Rat { return new(big.Rat).Sub(gross(m), sum(m, "Expense")) }
	net := func(m map[string]*big.Rat) *big.Rat {
		return new(big.Rat).Sub(new(big.Rat).Add(operating(m), sum(m, "Other Income")), sum(m, "Other Expense"))
	}
	measures := []packLine{measure("Net income", net)}
	if this.Typed {
		measures = []packLine{measure("Gross profit", gross), measure("Operating profit", operating), measure("Net income", net)}
	}
	if pl[""] != nil {
		complete = false
	}

	// Balance Sheet: month-end balances, shown on each class's normal side, plus earnings not yet closed to equity.
	bsNames := []string{}
	for _, g := range bsGroups {
		bsNames = append(bsNames, g.name)
	}
	if !this.Typed {
		bsNames = []string{"Assets", "Liabilities", "Equity"}
	}
	isPL := func(b *books, id string) bool { c := b.Accounts[id].Class; return c == "Revenue" || c == "Expense" }
	shown := func(b *books, id string) *big.Rat {
		v := orZero(b.Balances, id)
		if isPL(b, id) {
			return zero()
		}
		if c := b.Accounts[id].Class; c == "Liability" || c == "Equity" {
			return new(big.Rat).Neg(v)
		}
		return v
	}
	earnings := func(b *books) *big.Rat {
		total := zero()
		for id, v := range b.Balances {
			if isPL(b, id) {
				total.Sub(total, v)
			}
		}
		return total
	}
	bsSections, bs, bsPrior := sections(this, last, ids(func(b *books) map[string]*big.Rat { return b.Balances }), bsNames, groupOf(bsGroups), shown)
	priorEarnings := zero()
	if compared {
		priorEarnings = earnings(last)
	}
	earned := line("", "Earnings not yet closed to equity", earnings(this), priorEarnings, compared)
	totals := func(m map[string]*big.Rat, earned *big.Rat) (assets, liabilities, equity *big.Rat) {
		if !this.Typed {
			return sum(m, "Assets"), sum(m, "Liabilities"), new(big.Rat).Add(sum(m, "Equity"), earned)
		}
		return sum(m, "Bank", "Accounts Receivable", "Other Current Asset", "Fixed Asset", "Other Asset"),
			sum(m, "Accounts Payable", "Credit Card", "Other Current Liability", "Long Term Liability"),
			new(big.Rat).Add(sum(m, "Equity"), earned)
	}
	assets, liabilities, equity := totals(bs, earnings(this))
	priorAssets, priorLiabilities, priorEquity := totals(bsPrior, priorEarnings)
	difference := new(big.Rat).Sub(assets, new(big.Rat).Add(liabilities, equity))
	if difference.Sign() != 0 || bs[""] != nil {
		complete = false
	}
	balance := map[string]any{"sections": bsSections, "earnings": earned,
		"total_assets":      line("", "Total assets", assets, priorAssets, compared),
		"total_liabilities": line("", "Total liabilities", liabilities, priorLiabilities, compared),
		"total_equity":      line("", "Total equity, with unclosed earnings", equity, priorEquity, compared),
		"difference":        money(difference)}

	names := []string{}
	for _, id := range reclassified {
		names = append(names, cmp.Or(this.Accounts[id].Name, id))
	}
	out := map[string]any{"profit_and_loss": statement{plSections, measures}, "balance_sheet": balance, "reclassified": names,
		"cash_flow": cashFlow(this, last, compared)}
	if this.ActivityUnavailable != "" {
		out["profit_and_loss"], out["profit_and_loss_unavailable"] = statement{[]packSection{}, []packLine{}}, this.ActivityUnavailable
		complete = false
	}
	// The pack's scope includes a reconciled historical cash-flow statement: without one it is not complete.
	if flow := out["cash_flow"].(map[string]any); flow["reconciled"] != true {
		complete = false
	}
	if !this.Typed {
		out["working_capital"] = map[string]string{"unavailable": "Xero captures do not carry account types, so current and long-term balances are not separated."}
		out["complete"] = complete
		return out
	}
	current := func(m map[string]*big.Rat) (ca, cl *big.Rat) {
		return sum(m, "Bank", "Accounts Receivable", "Other Current Asset"), sum(m, "Accounts Payable", "Credit Card", "Other Current Liability")
	}
	ca, cl := current(bs)
	pca, pcl := current(bsPrior)
	out["working_capital"] = map[string]packLine{
		"book_cash":           line("", "Book cash (ledger bank balances, not bank-available cash)", sum(bs, "Bank"), sum(bsPrior, "Bank"), compared),
		"receivables":         line("", "Accounts receivable", sum(bs, "Accounts Receivable"), sum(bsPrior, "Accounts Receivable"), compared),
		"payables":            line("", "Accounts payable", sum(bs, "Accounts Payable"), sum(bsPrior, "Accounts Payable"), compared),
		"current_assets":      line("", "Current assets", ca, pca, compared),
		"current_liabilities": line("", "Current liabilities", cl, pcl, compared),
		"working_capital":     line("", "Working capital", new(big.Rat).Sub(ca, cl), new(big.Rat).Sub(pca, pcl), compared),
	}

	out["complete"] = complete
	return out
}

// cashFlow is the month's Statement of Cash Flows, the ledger's own or, for Xero, which has none, the firm's import, as
// given, checked against the books: its sections add to the net change, beginning cash plus the change is ending cash,
// ending cash is the month-end book cash and, with last month compared, beginning cash is last month's. Without one it
// says why there is none.
func cashFlow(this, last *books, compared bool) map[string]any {
	var sections []quickbooks.CashSection
	var totals map[string]*big.Rat
	var err error
	uncounted := func(names []string, month string) map[string]any {
		return map[string]any{"unavailable": "The imported statement cannot be checked against book cash: these Xero bank accounts, neither bank accounts nor credit cards, have a balance or movement " + month + ": " + strings.Join(names, ", ") + "."}
	}
	switch {
	case this.Imported != nil && len(this.Uncounted) > 0:
		return uncounted(this.Uncounted, "this month")
	case this.Imported != nil && compared && len(last.Uncounted) > 0:
		return uncounted(last.Uncounted, "last month")
	case this.Imported != nil:
		sections, totals, err = parseCashFlow(this.Imported.CSV)
	case !this.Typed:
		return map[string]any{"unavailable": "Xero's API has no Statement of Cash Flows: import the firm's statement for the month (CSV) to complete the pack. Balance changes alone cannot separate depreciation, assets bought on credit or borrowing from cash. Book cash and its change are on the balance sheet."}
	case len(this.CashFlow) == 0 || string(this.CashFlow) == "null":
		return map[string]any{"unavailable": "A historical cash-flow statement needs the ledger's own Statement of Cash Flows or an accountant-approved preparation method, and neither is captured yet: balance changes alone cannot separate depreciation, assets bought on credit or borrowing from cash. Book cash and its change are on the balance sheet."}
	default:
		sections, totals, err = quickbooks.CashFlow(this.CashFlow)
	}
	if err != nil {
		return map[string]any{"unavailable": "The Statement of Cash Flows could not be read: " + err.Error() + "."}
	}
	// Book cash is each month's own Bank accounts, whatever this month's presentation calls them.
	bank := func(b *books) *big.Rat {
		total := zero()
		for id, balance := range b.Balances {
			if b.Accounts[id].Type == "Bank" {
				total.Add(total, balance)
			}
		}
		return total
	}
	cash := bank(this)
	differences := []string{}
	differ := func(what string, stated, books *big.Rat) {
		if stated.Cmp(books) != 0 {
			differences = append(differences, fmt.Sprintf("%s: the statement says %s, the books %s, a difference of %s.", what, money(stated), money(books), money(new(big.Rat).Sub(stated, books))))
		}
	}
	shown, added := []packSection{}, zero()
	for _, s := range sections {
		lines, detail := []packLine{}, zero()
		for _, l := range s.Lines {
			lines = append(lines, packLine{Name: l.Name, Amount: money(l.Amount)})
			detail.Add(detail, l.Amount)
		}
		if len(s.Lines) > 0 {
			differ(s.Name+" total against its lines", s.Total, detail)
		}
		shown = append(shown, packSection{Name: s.Name, Lines: lines, Total: packLine{Name: "Net cash from " + strings.ToLower(s.Name), Amount: money(s.Total)}})
		added.Add(added, s.Total)
	}
	change, beginning, ending := totals["CashIncrease"], totals["BeginningCash"], totals["EndingCash"]
	differ("Net change in cash", change, added)
	differ("Ending cash", ending, new(big.Rat).Add(beginning, change))
	differ("Ending cash against the month-end book cash", ending, cash)
	if compared {
		differ("Beginning cash against last month's book cash", beginning, bank(last))
	}
	out := map[string]any{"sections": shown, "net_change": packLine{Name: "Net change in cash", Amount: money(change)},
		"beginning": packLine{Name: "Cash at the beginning of the month", Amount: money(beginning)}, "ending": packLine{Name: "Cash at the end of the month", Amount: money(ending)},
		"book_cash": money(cash), "reconciled": len(differences) == 0, "differences": differences}
	if i := this.Imported; i != nil {
		disclosure := fmt.Sprintf("The cash-flow statement is the firm's, imported from %s by %s on %s, who attested: %s It is not read from the ledger.", i.Source, i.Actor, i.At.UTC().Format("2006-01-02"), i.Attestation)
		if !compared {
			disclosure += " Opening cash was not checked against last month's books."
		}
		out["disclosure"] = disclosure
	}
	return out
}

var cashSections = []string{"Operating activities", "Investing activities", "Financing activities", "Effect of exchange rate changes"}

// parseCashFlow reads a statement of cash flows from CSV: the header Section,Line,Amount; lines under the sections above,
// outflows negative, which it totals itself; and one Beginning cash and one Ending cash row with no line. It returns
// them as quickbooks.CashFlow does, and refuses anything it would have to guess.
func parseCashFlow(text string) ([]quickbooks.CashSection, map[string]*big.Rat, error) {
	reader := csv.NewReader(strings.NewReader(text))
	rows, at := [][]string{}, []int{}
	for {
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the file is not a readable CSV of three columns: %v", err)
		}
		line, _ := reader.FieldPos(0)
		rows, at = append(rows, row), append(at, line)
	}
	if len(rows) < 3 || len(rows) > 501 || len(rows[0]) != 3 || strings.TrimPrefix(strings.Join(rows[0], ","), "\ufeff") != "Section,Line,Amount" {
		return nil, nil, errors.New("the first row must be Section,Line,Amount, followed by up to 500 rows")
	}
	lines, totals := make([][]quickbooks.CashLine, len(cashSections)), map[string]*big.Rat{}
	for i, row := range rows[1:] {
		n := at[i+1]
		section, name, cell := strings.TrimSpace(row[0]), strings.TrimSpace(row[1]), strings.TrimSpace(row[2])
		if !budgetAmount.MatchString(cell) {
			return nil, nil, fmt.Errorf("row %d: %q is not an amount such as -1200.50, without separators or symbols", n, row[2])
		}
		amount, _ := new(big.Rat).SetString(cell)
		s := slices.IndexFunc(cashSections, func(c string) bool { return strings.EqualFold(c, section) })
		total := map[string]string{"beginning cash": "BeginningCash", "ending cash": "EndingCash"}[strings.ToLower(section)]
		switch {
		case total != "" && (name != "" || totals[total] != nil):
			return nil, nil, fmt.Errorf("row %d: give one %s row, with an empty Line", n, section)
		case total != "":
			totals[total] = amount
		case s < 0:
			return nil, nil, fmt.Errorf("row %d: %q is not Operating activities, Investing activities, Financing activities, Effect of exchange rate changes, Beginning cash or Ending cash", n, row[0])
		case name == "" || len(name) > 200 || strings.HasPrefix(strings.ToLower(name), "total") || strings.EqualFold(name, cashSections[s]):
			return nil, nil, fmt.Errorf("row %d: name the line in up to 200 characters; section totals are added up here, not imported", n)
		default:
			lines[s] = append(lines[s], quickbooks.CashLine{Name: name, Amount: amount})
		}
	}
	if totals["BeginningCash"] == nil || totals["EndingCash"] == nil {
		return nil, nil, errors.New("give one Beginning cash and one Ending cash row")
	}
	sections, change := []quickbooks.CashSection{}, zero()
	for s, name := range cashSections {
		if len(lines[s]) == 0 {
			continue
		}
		total := zero()
		for _, l := range lines[s] {
			total.Add(total, l.Amount)
		}
		sections = append(sections, quickbooks.CashSection{Name: name, Lines: lines[s], Total: total})
		change.Add(change, total)
	}
	totals["CashIncrease"] = change
	return sections, totals, nil
}

// importCashFlow saves the firm's statement of cash flows for a close whose books are read from Xero, with the importer's
// attestation of what it is. The sign-off covers it, so it needs sign-off authority and an open close. The reply is the
// pack, with the statement checked against the books.
func importCashFlow(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		who, ok := reviewActor(w, r)
		if !ok {
			return
		}
		if a := auth.Actor(ctx); a != nil && a.Organization != "" && !slices.Contains(a.Permissions, "close:signoff") {
			problem(w, 403, "Your organization role lacks close:signoff.")
			return
		}
		var in struct {
			CSV       string `json:"csv"`
			Source    string `json:"source"`
			Confirmed bool   `json:"confirmed"`
		}
		if !decode(w, r, &in) {
			return
		}
		in.Source = strings.TrimSpace(in.Source)
		if len(in.CSV) > 64*1024 || in.Source == "" || len(in.Source) > 300 {
			problem(w, 422, "Name the statement's source (up to 300 characters) and import a CSV of at most 64 KiB.")
			return
		}
		if _, _, err := parseCashFlow(in.CSV); err != nil {
			problem(w, 422, "The statement could not be read: "+err.Error()+".")
			return
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			problem(w, 503, "The statement could not be saved.")
			return
		}
		defer tx.Rollback()
		var closeID, entity, period string
		if err = tx.QueryRowContext(ctx, `SELECT id::text,entity,period FROM client_closes WHERE id::text=$1 FOR UPDATE`, r.PathValue("id")).Scan(&closeID, &entity, &period); err != nil {
			problem(w, 404, "Close not found.")
			return
		}
		done, err := signedOff(ctx, tx, closeID)
		if err != nil {
			problem(w, 503, "Close readiness unavailable.")
			return
		}
		if done {
			problem(w, 409, "The close is signed off, and its sign-off covers the statement: reopen it before importing another.")
			return
		}
		m, err := periodBooks(ctx, tx, closeID, period)
		if err != nil {
			problem(w, 503, "Period snapshot unavailable.")
			return
		}
		if m == nil || m.Source != "xero" || m.Stale {
			problem(w, 409, "Capture this month's Xero Trial Balance first, from a current connection.")
			return
		}
		b, err := recordBooks(ctx, tx, m)
		if err != nil {
			problem(w, 409, "This month's capture could not be read: "+err.Error()+".")
			return
		}
		attestation := fmt.Sprintf("I reviewed this statement's classification and cash scope; it is for %s, %s, in %s, with amounts in whole %s units, not thousands.", entity, period, b.Currency, b.Currency)
		if !in.Confirmed {
			problem(w, 422, "Confirm: "+attestation)
			return
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO cash_flow_imports(close_id,company,currency,csv,source,attestation,actor) VALUES($1,$2,$3,$4,$5,$6,$7)`, closeID, m.realm, b.Currency, in.CSV, in.Source, attestation, who); err != nil {
			problem(w, 503, "The statement could not be saved.")
			return
		}
		pack, err := closeManagementPack(ctx, tx, closeID)
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "The statement could not be saved.")
			return
		}
		jsonReply(w, 200, pack)
	}
}

// readLedgerMonth reads a capture's Trial Balance and Profit and Loss into books.
func readLedgerMonth(m *fluxMonth) (*books, error) {
	b := &books{Accounts: map[string]packAccount{}, Balances: map[string]*big.Rat{}, Activity: map[string]*big.Rat{}}
	var balances []ledger.AccountBalance
	var err error
	if m.Source == "xero" {
		if b.Currency, balances, err = xero.AccountBalances(m.xero); err != nil {
			return nil, err
		}
		_, activity, err := xero.MonthlyActivity(m.xero)
		if err != nil {
			return nil, err
		}
		for _, a := range activity {
			b.Activity[a.ID] = a.Net
			b.Accounts[a.ID] = packAccount{Name: a.Name, Class: a.Class}
		}
	} else {
		b.Typed, b.CashFlow = true, m.cashFlow
		if b.Currency, balances, err = quickbooks.TrialBalance(m.accounts, m.tb); err != nil {
			return nil, err
		}
		currency, activity, err := quickbooks.MonthlyActivity(m.pl)
		if err != nil {
			return nil, err
		}
		if currency != b.Currency {
			return nil, errCurrency
		}
		for id, a := range activity {
			b.Activity[id] = a.Amount
			b.Accounts[id] = packAccount{Name: a.Name}
		}
		var accounts []struct {
			ID                                                string `json:"Id"`
			Name, Classification, AccountType, AccountSubType string
		}
		json.Unmarshal(m.accounts, &accounts)
		// The whole chart, so an account with no activity yet can still be named (and a budget line mapped to it).
		for _, a := range accounts {
			b.Accounts[a.ID] = packAccount{Name: a.Name, Class: a.Classification, Type: a.AccountType, Subtype: a.AccountSubType}
		}
	}
	for _, a := range balances {
		b.Balances[a.ID] = a.Net
		b.Accounts[a.ID] = packAccount{Name: cmp.Or(a.FullName, a.Name), Class: a.Class, Type: a.AccountType, Subtype: a.Subtype}
	}
	if m.Source == "xero" {
		cash, other, err := xero.CashAccounts(m.xero)
		if err != nil {
			return nil, err
		}
		for _, id := range cash {
			if a, ok := b.Accounts[id]; ok {
				a.Type = "Bank"
				b.Accounts[id] = a
			}
		}
		b.Uncounted = other
	}
	return b, nil
}

var errCurrency = errors.New("the Trial Balance and Profit and Loss are in different currencies")

// captureStatus is a capture's review status: signed_off only while the close's sign-off stands over this very
// capture, QuickBooks or Xero, the one the signed readiness digest binds through ledger coverage (any newer capture
// reopens it). Unknown when readiness cannot be read.
func captureStatus(ctx context.Context, tx *sql.Tx, closeID string, m *fluxMonth) string {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT capture_status`); err != nil {
		return "unknown"
	}
	bound := m.SnapshotID
	if m.Source == "xero" {
		bound = xeroCapture + m.SnapshotID
	}
	current, err := closeReadiness(ctx, tx, closeID)
	switch {
	case err != nil:
		tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT capture_status`)
		return "unknown"
	case current.State == "signed_off" && current.coverage.SnapshotID == bound:
		return "signed_off"
	}
	return "provisional"
}

// managementReport serves the month's management pack from the close's newest capture, compared with last month's
// close when its newest capture comes from the same ledger and company in the same currency. Everything is read from
// one snapshot.
func managementReport(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			problem(w, 503, "Management pack unavailable.")
			return
		}
		defer tx.Rollback()
		pack, err := closeManagementPack(r.Context(), tx, r.PathValue("id"))
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "Management pack unavailable.")
			return
		}
		jsonReply(w, 200, pack)
	}
}

// closeManagementPack builds the pack inside the caller's transaction, which should be one consistent snapshot. A pack
// not yet ready is {state: waiting, reason}; a ready one carries the digest of its figures and its overall status.
func closeManagementPack(ctx context.Context, tx *sql.Tx, closeID string) (map[string]any, error) {
	wait := func(reason string) (map[string]any, error) {
		return map[string]any{"state": "waiting", "reason": reason}, nil
	}
	var org, entity, period string
	if err := tx.QueryRowContext(ctx, `SELECT organization_id,entity,period FROM client_closes WHERE id::text=$1`, closeID).Scan(&org, &entity, &period); err != nil {
		return nil, tieRefusal{404, "Close not found."}
	}
	// A month whose books are its files reads them from its accepted file.
	if source, err := closeBooksSource(ctx, tx, closeID); err != nil {
		return nil, tieRefusal{503, "The month's books source is unavailable."}
	} else if source == "files" {
		return closeFilesPack(ctx, tx, closeID, org, entity, period)
	}
	this, err := periodBooks(ctx, tx, closeID, period)
	if err != nil {
		return nil, tieRefusal{503, "Period snapshot unavailable."}
	}
	if this == nil {
		return wait("Capture this month's QuickBooks period snapshot or Xero Trial Balance first.")
	}
	thisBooks, err := recordBooks(ctx, tx, this)
	if err != nil {
		return wait("This month's capture could not be read: " + err.Error() + ".")
	}
	if this.Source == "xero" {
		var i cashFlowImport
		var company, currency string
		switch err = tx.QueryRowContext(ctx, `SELECT csv,source,attestation,actor,created_at,company,currency FROM cash_flow_imports WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&i.CSV, &i.Source, &i.Attestation, &i.Actor, &i.At, &company, &currency); {
		case err == nil && company == this.realm && currency == thisBooks.Currency:
			thisBooks.Imported = &i
		case err != nil && err != sql.ErrNoRows:
			return nil, tieRefusal{503, "The imported cash-flow statement is unavailable."}
		}
	}
	var last *fluxMonth
	var lastBooks *books
	comparison := ""
	var priorClose string
	switch err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE organization_id=$1 AND lower(entity)=lower($2) AND period=$3`, org, entity, shiftMonth(period, -1)).Scan(&priorClose); {
	case err == sql.ErrNoRows:
		comparison = "Last month has no close in this workspace."
	case err != nil:
		return nil, tieRefusal{503, "Last month's close is unavailable."}
	default:
		if last, err = periodBooks(ctx, tx, priorClose, shiftMonth(period, -1)); err != nil {
			return nil, tieRefusal{503, "Last month's snapshot is unavailable."}
		}
		switch {
		case last == nil:
			comparison = "Last month's close has no month-end capture."
		case last.Source != this.Source || last.realm != this.realm:
			comparison = "Last month was captured from a different ledger or company."
		default:
			if lastBooks, err = recordBooks(ctx, tx, last); err != nil {
				comparison = "Last month's capture could not be read: " + err.Error() + "."
			} else if lastBooks.Currency != thisBooks.Currency {
				comparison, lastBooks = "Last month's capture is in a different currency.", nil
			}
		}
	}
	pack := managementPack(thisBooks, lastBooks)
	signed, err := signedOff(ctx, tx, closeID)
	if err != nil {
		return nil, tieRefusal{503, "Close readiness unavailable."}
	}
	pack["open"] = !signed
	status := captureStatus(ctx, tx, closeID, this)
	pack["state"], pack["entity"], pack["period"], pack["currency"], pack["basis"] = "ready", entity, period, thisBooks.Currency, "Accrual"
	pack["source"], pack["snapshot_id"], pack["as_of"], pack["stale"], pack["status"] = this.Source, this.SnapshotID, this.CapturedAt, this.Stale, status
	note := "Figures are the ledger as captured; approved drafts not yet posted are excluded."
	if lastBooks != nil {
		prior := captureStatus(ctx, tx, priorClose, last)
		pack["prior"] = map[string]any{"close_id": priorClose, "period": last.Period, "snapshot_id": last.SnapshotID, "as_of": last.CapturedAt, "stale": last.Stale, "status": prior}
		if prior != "signed_off" {
			status = prior
		}
	} else {
		pack["comparison_unavailable"] = comparison
	}
	if status != "signed_off" {
		note += " Provisional until the sign-off of every month shown stands."
	}
	if disclosure, ok := pack["cash_flow"].(map[string]any)["disclosure"].(string); ok {
		note += " " + disclosure
	}
	return finishPack(ctx, tx, org, pack, note, status)
}

// finishPack completes a pack: its note, with the cash-flow statement's disclosure, its overall status and the digest of
// its figures, and the draft commentary under the firm's materiality.
func finishPack(ctx context.Context, tx *sql.Tx, org string, pack map[string]any, note, status string) (map[string]any, error) {
	pack["note"], pack["overall"], pack["digest"] = note, status, figuresDigest(pack)
	var materiality sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT materiality_amount::text FROM organizations WHERE id=$1`, org).Scan(&materiality); err != nil {
		return nil, tieRefusal{503, "The firm's policy is unavailable."}
	}
	pack["draft_commentary"] = draftCommentary(pack, materiality.String)
	return pack, nil
}

// draftCommentary is a starting point for the reviewer's commentary from the pack's own figures: net income, the P&L
// lines that moved most against last month (those at or above the firm's materiality, when it sets one, else the five
// largest), and the month's change in cash when the statement of cash flows reconciles. It says what moved, never why.
func draftCommentary(pack map[string]any, materiality string) string {
	pl, _ := pack["profit_and_loss"].(statement)
	currency, _ := pack["currency"].(string)
	show := func(amount string) string { return ledger.Show(amount, currency) }
	moved := func(l packLine) string {
		change, _ := new(big.Rat).SetString(*l.Change)
		switch change.Sign() {
		case 0:
			return l.Name + " " + show(l.Amount) + ", unchanged from last month"
		case -1:
			return fmt.Sprintf("%s %s, down %s from %s", l.Name, show(l.Amount), show(money(change.Abs(change))), show(*l.Prior))
		}
		return fmt.Sprintf("%s %s, up %s from %s", l.Name, show(l.Amount), show(money(change)), show(*l.Prior))
	}
	var out []string
	if n := len(pl.Measures); n > 0 {
		net := pl.Measures[n-1]
		if net.Change != nil {
			out = append(out, moved(net)+".")
		} else {
			out = append(out, net.Name+" "+show(net.Amount)+"; last month is not compared.")
		}
	}
	var lines []packLine
	for _, s := range pl.Sections {
		for _, l := range s.Lines {
			if change, ok := new(big.Rat).SetString(cmp.Or(deref(l.Change), "0")); ok && change.Sign() != 0 {
				lines = append(lines, l)
			}
		}
	}
	size := func(l packLine) *big.Rat { r, _ := new(big.Rat).SetString(*l.Change); return r.Abs(r) }
	slices.SortStableFunc(lines, func(a, b packLine) int { return size(b).Cmp(size(a)) })
	floor, limited := new(big.Rat), false
	if _, ok := floor.SetString(materiality); ok && floor.Sign() > 0 {
		lines = slices.DeleteFunc(lines, func(l packLine) bool { return size(l).Cmp(floor) < 0 })
	} else if len(lines) > 5 {
		lines, limited = lines[:5], true
	}
	if len(lines) > 0 {
		said := []string{}
		for _, l := range lines {
			said = append(said, moved(l))
		}
		lead := "Lines that moved by at least the firm's materiality of " + show(materiality) + ": "
		if floor.Sign() <= 0 {
			lead = map[bool]string{true: "The five largest movements: ", false: "Movements: "}[limited]
		}
		out = append(out, lead+strings.Join(said, "; ")+".")
	}
	if flow, _ := pack["cash_flow"].(map[string]any); flow["reconciled"] == true {
		if change, ok := flow["net_change"].(packLine); ok {
			out = append(out, "Net change in cash "+show(change.Amount)+".")
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, " ") + " " + draftWhy
}

// draftWhy ends a drafted commentary; a commentary still carrying it has not been written by the reviewer.
const draftWhy = "Why: (add what caused each movement, as you have verified it.)"

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
