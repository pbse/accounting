package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/ledger"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/xero"
)

// packAccount is one ledger account as the management pack groups it. Type is QuickBooks' account type; Xero
// captures carry only the class.
type packAccount struct{ Name, Class, Type, Subtype string }

// books is one month's ledger: the month-end Trial Balance net (debit minus credit) and the month's Profit and Loss
// amount (as the report shows it) by account ID.
type books struct {
	Currency string
	Typed    bool
	Accounts map[string]packAccount
	Balances map[string]*big.Rat
	Activity map[string]*big.Rat
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
		"cash_flow": map[string]string{"unavailable": "A historical cash-flow statement needs the ledger's own Statement of Cash Flows or an accountant-approved preparation method, and neither is captured yet: balance changes alone cannot separate depreciation, assets bought on credit or borrowing from cash. Book cash and its change are on the balance sheet."}}
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
		b.Typed = true
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
	return b, nil
}

var errCurrency = errors.New("the Trial Balance and Profit and Loss are in different currencies")

// captureStatus is a capture's review status: signed_off only while the close's sign-off stands over this very
// QuickBooks capture, the one the signed readiness digest binds (any newer capture reopens it); a Xero capture is not
// bound by that digest, so it stays provisional. Unknown when readiness cannot be read.
func captureStatus(ctx context.Context, tx *sql.Tx, closeID string, m *fluxMonth) string {
	if _, err := tx.ExecContext(ctx, `SAVEPOINT capture_status`); err != nil {
		return "unknown"
	}
	current, err := closeReadiness(ctx, tx, closeID)
	switch {
	case err != nil:
		tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT capture_status`)
		return "unknown"
	case current.State == "signed_off" && m.Source == "quickbooks" && current.coverage.SnapshotID == m.SnapshotID:
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
	this, err := periodBooks(ctx, tx, closeID, period)
	if err != nil {
		return nil, tieRefusal{503, "Period snapshot unavailable."}
	}
	if this == nil {
		return wait("Capture this month's QuickBooks period snapshot or Xero Trial Balance first.")
	}
	thisBooks, err := readLedgerMonth(this)
	if err != nil {
		return wait("This month's capture could not be read: " + err.Error() + ".")
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
			if lastBooks, err = readLedgerMonth(last); err != nil {
				comparison = "Last month's capture could not be read: " + err.Error() + "."
			} else if lastBooks.Currency != thisBooks.Currency {
				comparison, lastBooks = "Last month's capture is in a different currency.", nil
			}
		}
	}
	pack := managementPack(thisBooks, lastBooks)
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
	pack["note"], pack["overall"], pack["digest"] = note, status, figuresDigest(pack)
	return pack, nil
}
