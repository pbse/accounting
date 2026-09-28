package transport

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/ledger"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/reconciliation"
	"close.local/backend/internal/xero"
)

type tieSupport struct {
	PaperID string `json:"paper_id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Amount  string `json:"amount"`
	Review  string `json:"review_state"`
	account string
	net     *big.Rat
	settled bool
}

type tieEntry struct {
	PaperID string `json:"paper_id"`
	Kind    string `json:"kind"`
	Amount  string `json:"amount"`
	Posting string `json:"posting"`
	account string
	net     *big.Rat
	settled bool
}

type tieRow struct {
	AccountID   string       `json:"account_id"`
	Account     string       `json:"account"`
	Class       string       `json:"class"`
	Balance     string       `json:"balance"`
	Expected    *string      `json:"expected"`
	Difference  *string      `json:"difference"`
	Status      string       `json:"status"`
	Provisional bool         `json:"provisional"`
	Hint        string       `json:"hint,omitempty"`
	Supports    []tieSupport `json:"supports"`
	Entries     []tieEntry   `json:"entries"`
}

var tieHints = map[string]string{
	"Bank":                    "Reconcile it to the bank statement.",
	"Credit Card":             "Reconcile it to the card statement.",
	"Accounts Receivable":     "Compare it with the receivables aging.",
	"Accounts Payable":        "Compare it with the payables aging.",
	"Fixed Asset":             "Add a depreciation schedule or fixed-asset register.",
	"Long Term Liability":     "Add a loan workpaper from the lender statement.",
	"Other Current Asset":     "Add a prepaid schedule or other support.",
	"Other Current Liability": "Add an accrual or other support.",
}

// tieOutcome is the tie-out of each balance-sheet account against its support.
type tieOutcome struct {
	Snapshot     map[string]any      `json:"snapshot"`
	Currency     string              `json:"currency,omitempty"`
	Error        string              `json:"error,omitempty"`
	Rows         []tieRow            `json:"rows"`
	Unmatched    []map[string]string `json:"unmatched"`
	Unclassified []map[string]string `json:"unclassified"`
}

// tieRefusal is why no tie-out could be computed, with the status the route answers.
type tieRefusal struct {
	status  int
	message string
}

func (e tieRefusal) Error() string { return e.message }

// tieOut compares each balance-sheet account in the newest month-end Trial Balance with the balance the close's latest workpapers support.
func tieOut(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent snapshot.")
			return
		}
		defer tx.Rollback()
		out, err := closeTieOut(r.Context(), tx, r.PathValue("id"))
		if refused, ok := err.(tieRefusal); ok {
			problem(w, refused.status, refused.message)
			return
		}
		if err != nil {
			problem(w, 503, "Tie-out unavailable.")
			return
		}
		jsonReply(w, 200, out)
	}
}

// closeTieOut computes the tie-out inside the caller's transaction, which should be one consistent snapshot.
func closeTieOut(ctx context.Context, tx *sql.Tx, id string) (tieOutcome, error) {
	var snapshotID, realm, xeroID string
	var started, capturedAt, xeroAt time.Time
	var stale, xeroStale bool
	var accounts, trialBalance, xeroPayload []byte
	err := tx.QueryRowContext(ctx, `SELECT s.id::text,s.realm,s.started_at,s.captured_at,e.id IS NULL OR e.superseded_by IS NOT NULL OR x.state<>'active',s.payload->'accounts',s.payload->'reports'->'TrialBalance' FROM quickbooks_snapshots s JOIN quickbooks_connections x ON x.id=s.connection_id LEFT JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id
 WHERE s.close_id::text=$1 AND s.payload ? 'period' AND s.connection_id=COALESCE(`+quickbooks.ReadingConnection("s.close_id")+`,s.connection_id) ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, id).Scan(&snapshotID, &realm, &started, &capturedAt, &stale, &accounts, &trialBalance)
	if err != nil && err != sql.ErrNoRows {
		return tieOutcome{}, tieRefusal{503, "Period snapshot unavailable."}
	}
	var tenant string
	xeroErr := tx.QueryRowContext(ctx, `SELECT s.id::text,s.captured_at,x.state<>'active',s.payload,s.tenant_id FROM xero_snapshots s JOIN xero_connections x ON x.id=s.connection_id WHERE s.close_id::text=$1 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1`, id).Scan(&xeroID, &xeroAt, &xeroStale, &xeroPayload, &tenant)
	if xeroErr != nil && xeroErr != sql.ErrNoRows {
		return tieOutcome{}, tieRefusal{503, "Xero snapshot unavailable."}
	}
	var snapshot map[string]any
	var currency string
	var balances []ledger.AccountBalance
	var readErr error
	switch {
	case xeroErr == nil && (err == sql.ErrNoRows || xeroAt.After(capturedAt)):
		snapshot = map[string]any{"id": xeroID, "captured_at": xeroAt, "stale": xeroStale, "source": "xero"}
		// Without the time the Trial Balance was requested, no journal is taken to be in it.
		realm, started = "", time.Time{}
		if at, ok := xero.RequestedAt(xeroPayload); ok {
			started = at
		}
		currency, balances, readErr = xero.AccountBalances(xeroPayload)
	case err == nil:
		tenant = ""
		snapshot = map[string]any{"id": snapshotID, "captured_at": capturedAt, "stale": stale, "source": "quickbooks"}
		currency, balances, readErr = quickbooks.TrialBalance(accounts, trialBalance)
	default:
		return tieOutcome{Rows: []tieRow{}, Unmatched: []map[string]string{}, Unclassified: []map[string]string{}}, nil
	}
	if readErr != nil {
		return tieOutcome{Snapshot: snapshot, Error: readErr.Error(), Rows: []tieRow{}, Unmatched: []map[string]string{}, Unclassified: []map[string]string{}}, nil
	}

	// A posting already in the snapshot's company before the capture began is in the Trial Balance.
	postings := map[string]string{}
	rows, err := tx.QueryContext(ctx, `SELECT workpaper_id::text,CASE WHEN state='verified' AND verified_at<$3 THEN 'in_balance' WHEN state='verified' THEN 'posted_after_capture' WHEN state IN ('executing','unknown') THEN 'unknown' ELSE 'proposed' END FROM (SELECT workpaper_id,state,verified_at FROM production_postings WHERE close_id::text=$1 AND realm=$2 UNION ALL SELECT workpaper_id,state,verified_at FROM sandbox_postings WHERE close_id::text=$1 AND realm=$2 UNION ALL SELECT workpaper_id,state,verified_at FROM xero_postings WHERE close_id::text=$1 AND tenant_id=$4) p`, id, realm, started, tenant)
	if err != nil {
		return tieOutcome{}, tieRefusal{503, "Could not read posting records."}
	}
	for rows.Next() {
		var paper, state string
		if err = rows.Scan(&paper, &state); err != nil {
			break
		}
		postings[paper] = state
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return tieOutcome{}, tieRefusal{503, "Could not read posting records."}
	}

	type paper struct {
		id, kind, state string
		settled         bool
		raw             json.RawMessage
	}
	latest := map[string]paper{}
	order := []string{}
	rows, err = tx.QueryContext(ctx, `SELECT id::text,kind,review_state,review_state='approved' AND (jsonb_typeof(paper->'blockers') IS DISTINCT FROM 'array' OR paper->'blockers'='[]'::jsonb),paper FROM workpaper_review_state WHERE close_id::text=$1 AND kind IN ('prepaid','accrual','loan','owner','reconciliation','settlement') ORDER BY workpaper_review_state.id`, id)
	if err != nil {
		return tieOutcome{}, tieRefusal{503, "Could not read saved workpapers."}
	}
	for rows.Next() {
		var p paper
		if err = rows.Scan(&p.id, &p.kind, &p.state, &p.settled, &p.raw); err != nil {
			break
		}
		key := p.kind + "\x00" + paperKey(p.kind, p.raw)
		if _, seen := latest[key]; !seen {
			order = append(order, key)
		}
		latest[key] = p
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return tieOutcome{}, tieRefusal{503, "Could not read saved workpapers."}
	}
	var materialityText string
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(o.materiality_amount::text,'') FROM organizations o JOIN client_closes c ON c.organization_id=o.id WHERE c.id::text=$1`, id).Scan(&materialityText); err != nil {
		return tieOutcome{}, tieRefusal{503, "Firm materiality is unavailable."}
	}
	materiality, _ := new(big.Rat).SetString(materialityText)

	supports, entries, unmatched := []tieSupport{}, []tieEntry{}, []map[string]string{}
	bad := false
	amount := func(value string) *big.Rat {
		v, ok := new(big.Rat).SetString(value)
		if !ok {
			bad = true
			return new(big.Rat)
		}
		return v
	}
	for _, key := range order {
		p := latest[key]
		var money struct {
			Input struct{ Currency string } `json:"input"`
		}
		json.Unmarshal(p.raw, &money)
		if money.Input.Currency != "" && currency != "" && !strings.EqualFold(money.Input.Currency, currency) {
			unmatched = append(unmatched, map[string]string{"account": "", "paper_id": p.id, "kind": p.kind, "amount": "", "reason": "Prepared in " + money.Input.Currency + ", but the Trial Balance is in " + currency + "; it is not compared."})
			continue
		}
		support := func(label, account string, net *big.Rat) {
			supports = append(supports, tieSupport{PaperID: p.id, Kind: p.kind, Label: label, Amount: net.FloatString(2), Review: p.state, account: account, net: net, settled: p.settled})
		}
		proposeAs := func(posting string, proposals ...*prepaids.Adjustment) {
			for _, e := range proposals {
				if e == nil {
					continue
				}
				value := amount(e.Amount)
				entries = append(entries,
					tieEntry{PaperID: p.id, Kind: p.kind, Amount: value.FloatString(2), Posting: posting, account: e.DebitAccount, net: value, settled: p.settled},
					tieEntry{PaperID: p.id, Kind: p.kind, Amount: new(big.Rat).Neg(value).FloatString(2), Posting: posting, account: e.CreditAccount, net: new(big.Rat).Neg(value), settled: p.settled})
			}
		}
		propose := func(proposals ...*prepaids.Adjustment) {
			posting := postings[p.id]
			if posting == "in_balance" {
				return
			}
			proposeAs(cmp.Or(posting, "proposed"), proposals...)
		}
		var decodeErr error
		switch p.kind {
		case "prepaid":
			var wp prepaids.Workpaper
			if decodeErr = json.Unmarshal(p.raw, &wp); decodeErr != nil {
				break
			}
			remaining := amount(wp.PrepaidBalance)
			switch wp.Input.Schedule {
			case prepaids.DeferredRevenue:
				support("Deferred revenue · "+wp.Input.SourceReference, wp.Input.PrepaidAccount, remaining.Neg(remaining))
			case prepaids.Depreciation:
				base := amount(wp.Input.Amount)
				if wp.Input.SalvageValue != "" {
					base.Sub(base, amount(wp.Input.SalvageValue))
				}
				net := remaining.Sub(remaining, base)
				// A disposal removes the asset's accumulated depreciation; its lines are proposed like the true-up.
				if len(wp.Disposal) > 0 {
					net = new(big.Rat)
				}
				support("Depreciation · "+wp.Input.SourceReference, wp.Input.PrepaidAccount, net)
				for i := range wp.Disposal {
					proposeAs("proposed", &wp.Disposal[i])
				}
			default:
				support("Prepaid · "+wp.Input.SourceReference, wp.Input.PrepaidAccount, remaining)
			}
			propose(wp.Proposal)
		case "accrual":
			var wp accruals.Workpaper
			if decodeErr = json.Unmarshal(p.raw, &wp); decodeErr != nil {
				break
			}
			target := amount(wp.Input.Amount)
			support("Accrual · "+wp.Input.SourceReference, wp.Input.LiabilityAccount, target.Neg(target))
			propose(wp.Proposal)
		case "loan":
			var wp loans.Workpaper
			if decodeErr = json.Unmarshal(p.raw, &wp); decodeErr != nil {
				break
			}
			in := wp.Input
			closing, current := amount(in.ClosingPrincipal), new(big.Rat)
			if in.ReclassCurrentPortion {
				current = amount(in.PrincipalDueNext12Months)
				if current.Cmp(closing) > 0 {
					current.Set(closing)
				}
				support("Loan current portion · "+in.Lender, in.CurrentPortionAccount, new(big.Rat).Neg(current))
			} else if in.BookedCurrentPortion != "" {
				current = amount(in.BookedCurrentPortion)
			}
			support("Loan · "+in.Lender, in.LoanAccount, new(big.Rat).Sub(current, closing))
			if wp.AccruedInterest != nil {
				interest := amount(wp.AccruedInterest.Amount)
				support("Accrued interest · "+in.Lender, in.AccruedInterestAccount, interest.Neg(interest))
			}
			for i := range wp.Proposals {
				propose(&wp.Proposals[i])
			}
		case "reconciliation":
			var wp reconciliation.Workpaper
			if decodeErr = json.Unmarshal(p.raw, &wp); decodeErr != nil {
				break
			}
			reconciled := amount(wp.Input.LedgerClosing)
			label := "Bank reconciliation"
			if wp.Input.AccountType == "credit_card" {
				label = "Card reconciliation"
				reconciled.Neg(reconciled)
			}
			support(label, wp.Input.Account, reconciled)
		case "owner":
			var wp owners.Workpaper
			if decodeErr = json.Unmarshal(p.raw, &wp); decodeErr == nil {
				propose(wp.Proposal)
			}
		case "settlement":
			var wp reconciliation.SettlementWorkpaper
			if decodeErr = json.Unmarshal(p.raw, &wp); decodeErr == nil {
				propose(wp.Proposal)
				// Refund and revenue drafts never post, so they stay proposed whatever the fee entry's posting.
				proposeAs("proposed", wp.RefundProposal, wp.RevenueProposal)
			}
		}
		if decodeErr != nil || bad {
			return tieOutcome{}, tieRefusal{500, "A saved workpaper could not be read. No partial tie-out was shown."}
		}
	}

	byName := map[string][]int{}
	for i, b := range balances {
		for _, name := range []string{b.Name, b.FullName} {
			if name = strings.ToLower(strings.TrimSpace(name)); name != "" && !slices.Contains(byName[name], i) {
				byName[name] = append(byName[name], i)
			}
		}
	}
	match := func(account, paperID, kind, value string) int {
		found := byName[strings.ToLower(strings.TrimSpace(account))]
		if len(found) == 1 {
			return found[0]
		}
		reason := "No Trial Balance account has this name; correct it in the workpaper."
		if len(found) > 1 {
			reason = "More than one Trial Balance account has this name; use the full account name."
		}
		unmatched = append(unmatched, map[string]string{"account": account, "paper_id": paperID, "kind": kind, "amount": value, "reason": reason})
		return -1
	}
	bySupport, byEntry := map[int][]tieSupport{}, map[int][]tieEntry{}
	for _, s := range supports {
		i := match(s.account, s.PaperID, s.Kind, s.Amount)
		if i >= 0 && balances[i].Class != "Asset" && balances[i].Class != "Liability" {
			unmatched = append(unmatched, map[string]string{"account": s.account, "paper_id": s.PaperID, "kind": s.Kind, "amount": s.Amount, "reason": "This names an account that is not an asset or liability; correct it in the workpaper."})
		} else if i >= 0 {
			bySupport[i] = append(bySupport[i], s)
		}
	}
	for _, e := range entries {
		if i := match(e.account, e.PaperID, e.Kind, e.Amount); i >= 0 {
			byEntry[i] = append(byEntry[i], e)
		}
	}

	out, unclassified := []tieRow{}, []map[string]string{}
	for i, b := range balances {
		if b.Class == "" {
			if b.Net.Sign() != 0 {
				unclassified = append(unclassified, map[string]string{"account_id": b.ID, "account": b.Name, "balance": b.Net.FloatString(2)})
			}
			continue
		}
		found := bySupport[i]
		if b.Class != "Asset" && b.Class != "Liability" || len(found) == 0 && b.Net.Sign() == 0 {
			continue
		}
		row := tieRow{AccountID: b.ID, Account: cmpName(b), Class: b.Class, Balance: b.Net.FloatString(2), Status: "unsupported", Supports: found, Entries: byEntry[i]}
		if row.Entries == nil {
			row.Entries = []tieEntry{}
		}
		if len(found) == 0 {
			row.Hint = tieHints[b.AccountType]
			if row.Hint == "" {
				row.Hint = "Add a workpaper or reconciliation that supports this balance."
			}
			row.Supports = []tieSupport{}
			out = append(out, row)
			continue
		}
		expected, pending, unknown := new(big.Rat), new(big.Rat), false
		for _, s := range found {
			expected.Add(expected, s.net)
			row.Provisional = row.Provisional || !s.settled
		}
		difference := new(big.Rat).Sub(b.Net, expected)
		for _, e := range row.Entries {
			if e.Posting == "unknown" {
				unknown = true
				continue
			}
			pending.Add(pending, e.net)
		}
		shownExpected, shownDifference := expected.FloatString(2), difference.FloatString(2)
		row.Expected, row.Difference = &shownExpected, &shownDifference
		switch {
		case unknown:
			row.Status = "posting_unknown"
		case difference.Sign() == 0:
			row.Status = "ties"
		case pending.Sign() != 0 && new(big.Rat).Add(difference, pending).Sign() == 0:
			row.Status = "ties_after_entries"
			for _, e := range row.Entries {
				row.Provisional = row.Provisional || !e.settled
			}
		case len(row.Entries) == 0 && materiality != nil && new(big.Rat).Abs(difference).Cmp(materiality) < 0:
			row.Status = "within_materiality"
		default:
			row.Status = "differs"
		}
		out = append(out, row)
	}
	slices.SortStableFunc(out, func(a, b tieRow) int { return strings.Compare(a.Class, b.Class) })
	return tieOutcome{Snapshot: snapshot, Currency: currency, Rows: out, Unmatched: unmatched, Unclassified: unclassified}, nil
}

func cmpName(b ledger.AccountBalance) string {
	if b.FullName != "" {
		return b.FullName
	}
	return b.Name
}
