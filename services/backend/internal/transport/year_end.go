package transport

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/payroll"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/reconciliation"
)

// The anchor close's client, every close from the first month of its fiscal year ($2) through its month.
const yearCloses = `SELECT c.id,c.period FROM client_closes a JOIN client_closes c ON c.organization_id=a.organization_id AND lower(c.entity)=lower(a.entity) AND c.period BETWEEN $2 AND a.period WHERE a.id::text=$1`

var yearEndHTML = template.Must(template.New("year").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>Year-end handoff</title><style>body{font:16px system-ui;max-width:960px;margin:3rem auto;padding:0 1rem;color:#203c33}table{border-collapse:collapse;width:100%}td,th{border-bottom:1px solid #ccc;padding:.4rem;text-align:left}@media print{section{break-inside:avoid}}</style><h1>{{.Entity}} · {{.Year}} year-end handoff</h1><p>Saved-work snapshot: {{.Cut}}. Covers {{.From}} through {{.Through}}.</p><p>Supporting schedules, roll-forwards, reconciled balances, adjustment history and open questions from approved work in this app, for the firm's tax and audit work. This is not an audit opinion or a tax return. Stored posting receipts are not a fresh QuickBooks read. Only the latest version of each workpaper is used; an unapproved latest version is listed as an open question, never replaced by an older one.</p><h2>Months</h2><table><tr><th>Month</th><th>Close</th><th>Sign-off</th><th>Blockers</th></tr>{{range .Months}}<tr><td>{{index . 0}}</td><td>{{index . 1}}</td><td>{{index . 2}}</td><td>{{index . 4}}</td></tr>{{end}}</table><h2>Open questions · {{len .Questions}}</h2>{{if not .Questions}}<p>None.</p>{{end}}<ul>{{range .Questions}}<li>{{index . 0}} · {{index . 1}}: {{index . 2}}</li>{{end}}</ul><h2>Files</h2><ul><li>prepaids.csv (prepaids, deferred revenue and depreciation, by schedule) and loans.csv: monthly roll-forwards from approved schedules. In prepaids.csv, period_amount is the month's expense, revenue or depreciation and scheduled_remaining is the calculated unexpired prepaid, unearned revenue or cost left to depreciate, not a ledger balance.</li><li>reconciliations.csv: approved reconciled balances</li><li>adjustments.csv: entries proposed by approved workpapers</li><li>postings.csv: every stored production posting record, including ones tied to older versions</li><li>open-questions.csv, months.csv, workpapers/{month}/: the approved workpapers</li><li>checksums.sha256: run shasum -a 256 -c checksums.sha256 after extracting</li></ul></html>`))

// fiscalYear returns the first month of the fiscal year holding anchor and its label, named for the year it ends.
func fiscalYear(anchor string, start int) (string, string) {
	a, _ := time.Parse("2006-01", anchor)
	from := time.Date(a.Year(), time.Month(start), 1, 0, 0, 0, 0, time.UTC)
	if from.After(a) {
		from = from.AddDate(-1, 0, 0)
	}
	label := strconv.Itoa(from.AddDate(0, 11, 0).Year())
	if start != 1 {
		label = "FY" + label
	}
	return from.Format("2006-01"), label
}

func yearEndHandoff(db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Could not open a consistent snapshot.")
			return
		}
		defer tx.Rollback()
		var entity, anchor string
		var cut time.Time
		err = tx.QueryRowContext(ctx, `SELECT entity,period,transaction_timestamp() FROM client_closes WHERE id::text=$1`, r.PathValue("id")).Scan(&entity, &anchor, &cut)
		if err == sql.ErrNoRows {
			problem(w, 404, "Close not found.")
			return
		}
		if err != nil {
			problem(w, 503, "Could not read the close.")
			return
		}
		profile, err := preparation.ClientProfile(ctx, tx, r.PathValue("id"))
		if err != nil {
			problem(w, 503, "The client's fiscal year is unavailable.")
			return
		}
		if profile.Unsettled() {
			problem(w, 409, profile.Question()+" first.")
			return
		}
		from, year := fiscalYear(anchor, profile.FiscalStart)
		closes := map[string]string{}
		rows, err := tx.QueryContext(ctx, `WITH y AS (`+yearCloses+`) SELECT id::text,period FROM y`, r.PathValue("id"), from)
		if err == nil {
			for rows.Next() {
				var id, period string
				if err = rows.Scan(&id, &period); err != nil {
					break
				}
				closes[period] = id
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		if err != nil {
			problem(w, 503, "Could not read this year's closes.")
			return
		}

		questions := [][]string{}
		ask := func(period, where, question string) {
			questions = append(questions, []string{period, csvText(where), csvText(question)})
		}
		months := [][]string{}
		for period := from; period <= anchor; period = shiftMonth(period, 1) {
			id, ok := closes[period]
			if !ok {
				months = append(months, []string{period, "", "no close", "", ""})
				ask(period, "months", "No close exists for this month; its work is not in this handoff.")
				continue
			}
			status, err := closeReadiness(ctx, tx, id)
			if err != nil {
				problem(w, 503, "Could not read sign-off history.")
				return
			}
			months = append(months, []string{period, id, status.State, status.DecisionID, csvText(strings.Join(status.Blockers, "; "))})
			if status.State != "signed_off" {
				ask(period, "close "+id, "The close is "+strings.ReplaceAll(status.State, "_", " ")+", not signed off.")
			}
			for _, blocker := range status.Blockers {
				ask(period, "close "+id, blocker)
			}
		}

		type paper struct {
			period, id, kind, key, state string
			raw                          json.RawMessage
		}
		type slot struct{ period, kind, key string }
		latest := map[slot]paper{}
		order := []slot{}
		rows, err = tx.QueryContext(ctx, `WITH y AS (`+yearCloses+`) SELECT y.period,w.id::text,w.kind,w.review_state,w.paper FROM y JOIN workpaper_review_state w ON w.close_id=y.id WHERE w.kind IN ('prepaid','accrual','loan','owner','payroll','reconciliation','settlement') ORDER BY y.period,w.id`, r.PathValue("id"), from)
		if err != nil {
			problem(w, 503, "Could not read saved workpapers.")
			return
		}
		total := 0
		for rows.Next() {
			var p paper
			if err = rows.Scan(&p.period, &p.id, &p.kind, &p.state, &p.raw); err != nil {
				break
			}
			total += len(p.raw)
			if total > 16*1024*1024 {
				rows.Close()
				problem(w, 413, "This year's workpapers exceed the 16 MiB handoff limit. Nothing was exported; no records were silently omitted.")
				return
			}
			p.key = paperKey(p.kind, p.raw)
			s := slot{p.period, p.kind, p.key}
			if _, seen := latest[s]; !seen {
				order = append(order, s)
			}
			latest[s] = p
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Could not finish the workpaper snapshot.")
			return
		}

		prepaidRows := [][]string{{"period", "source", "prepaid_account", "expense_account", "credit", "period_amount", "scheduled_remaining", "currency", "workpaper_id", "schedule"}}
		loanRows := [][]string{{"period", "lender", "loan_account", "opening_principal", "principal_paid", "fees_added", "interest_paid", "closing_principal", "currency", "workpaper_id"}}
		recRows := [][]string{{"period", "account", "account_type", "statement_closing", "ledger_closing", "closing_difference", "status", "workpaper_id"}}
		adjustments := [][]string{{"period", "kind", "workpaper_id", "date", "debit_account", "credit_account", "amount"}}
		files := map[string][]byte{}
		approvedPrepaids := map[slot]prepaids.Workpaper{}
		approvedLoans := map[slot]loans.Workpaper{}
		propose := func(p paper, entries ...*prepaids.Adjustment) {
			for _, e := range entries {
				if e != nil {
					adjustments = append(adjustments, []string{p.period, p.kind, p.id, e.Date, csvText(e.DebitAccount), csvText(e.CreditAccount), e.Amount})
				}
			}
		}
		for _, s := range order {
			p := latest[s]
			where := p.kind + " #" + p.id
			if p.state != "approved" {
				ask(p.period, where, fmt.Sprintf("The latest %s version for %s is %s, not approved. No earlier version is used.", p.kind, strings.ReplaceAll(p.key, "\x00", " / "), strings.ReplaceAll(p.state, "_", " ")))
				continue
			}
			files[fmt.Sprintf("workpapers/%s/%s-%s.json", p.period, p.kind, p.id)] = p.raw
			previous := slot{shiftMonth(p.period, -1), p.kind, p.key}
			switch p.kind {
			case "prepaid":
				var wp prepaids.Workpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved prepaid could not be read. No partial handoff was downloaded.")
					return
				}
				approvedPrepaids[s] = wp
				credit, prior := "", (*prepaids.Row)(nil)
				for i, row := range wp.Schedule {
					if row.Period == p.period {
						credit = row.Credit
					}
					if row.Period == previous.period {
						prior = &wp.Schedule[i]
					}
				}
				// The current schedule's prior-month balance already reflects credits and cancellations.
				before, approvedBefore := approvedPrepaids[previous]
				_, pending := latest[previous]
				switch {
				case approvedBefore && prior == nil:
					ask(p.period, where, fmt.Sprintf("%s has an approved %s workpaper, but this schedule has no %s row.", wp.Input.SourceReference, previous.period, previous.period))
				case approvedBefore && !sameAmount(before.PrepaidBalance, prior.Remaining):
					ask(p.period, where, fmt.Sprintf("Roll-forward break: %s closed %s at %s, but this schedule shows %s for %s.", wp.Input.SourceReference, previous.period, before.PrepaidBalance, prior.Remaining, previous.period))
				case !approvedBefore && !pending && prior != nil && previous.period >= from && closes[previous.period] != "":
					ask(p.period, where, fmt.Sprintf("%s has a %s balance in this schedule but no %s workpaper.", wp.Input.SourceReference, previous.period, previous.period))
				}
				prepaidRows = append(prepaidRows, []string{p.period, csvText(wp.Input.SourceReference), csvText(wp.Input.PrepaidAccount), csvText(wp.Input.ExpenseAccount), credit, wp.PeriodExpense, wp.PrepaidBalance, wp.Input.Currency, p.id, cmp.Or(wp.Input.Schedule, "prepaid")})
				propose(p, wp.Proposal)
				for i := range wp.Disposal {
					propose(p, &wp.Disposal[i])
				}
			case "loan":
				var wp loans.Workpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved loan could not be read. No partial handoff was downloaded.")
					return
				}
				approvedLoans[s] = wp
				before, approvedBefore := approvedLoans[previous]
				_, pending := latest[previous]
				switch {
				case approvedBefore && !sameAmount(before.Input.ClosingPrincipal, wp.Input.OpeningPrincipal):
					ask(p.period, where, fmt.Sprintf("Roll-forward break: %s closed %s at %s principal, but this month opens at %s.", wp.Input.Lender, previous.period, before.Input.ClosingPrincipal, wp.Input.OpeningPrincipal))
				case !approvedBefore && !pending && previous.period >= from && closes[previous.period] != "" && !sameAmount(wp.Input.OpeningPrincipal, "0"):
					ask(p.period, where, fmt.Sprintf("%s opens %s with %s principal, but %s has no loan workpaper.", wp.Input.Lender, p.period, wp.Input.OpeningPrincipal, previous.period))
				}
				principal, interest := new(big.Rat), new(big.Rat)
				for _, row := range wp.Schedule {
					addAmount(principal, row.Principal)
					addAmount(interest, row.Interest)
				}
				fees := wp.Fees
				if fees == "" {
					fees = "0.00"
				}
				loanRows = append(loanRows, []string{p.period, csvText(wp.Input.Lender), csvText(wp.Input.LoanAccount), wp.Input.OpeningPrincipal, principal.FloatString(2), fees, interest.FloatString(2), wp.Input.ClosingPrincipal, wp.Input.Currency, p.id})
				for _, text := range append(wp.Questions, wp.Blockers...) {
					ask(p.period, where, text)
				}
				for i := range wp.Proposals {
					propose(p, &wp.Proposals[i])
				}
			case "accrual":
				var wp accruals.Workpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved accrual could not be read. No partial handoff was downloaded.")
					return
				}
				propose(p, wp.Proposal, wp.Reversal)
			case "owner":
				var wp owners.Workpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved owner item could not be read. No partial handoff was downloaded.")
					return
				}
				propose(p, wp.Proposal)
			case "payroll":
				var wp payroll.Workpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved payroll entry could not be read. No partial handoff was downloaded.")
					return
				}
				for i := range wp.Proposals {
					propose(p, &wp.Proposals[i])
				}
			case "settlement":
				var wp reconciliation.SettlementWorkpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved Stripe settlement could not be read. No partial handoff was downloaded.")
					return
				}
				propose(p, wp.Proposal, wp.RefundProposal, wp.RevenueProposal)
			case "reconciliation":
				var wp reconciliation.Workpaper
				if json.Unmarshal(p.raw, &wp) != nil {
					problem(w, 500, "A saved reconciliation could not be read. No partial handoff was downloaded.")
					return
				}
				kind := wp.Input.AccountType
				if kind == "" {
					kind = "bank"
				}
				recRows = append(recRows, []string{p.period, csvText(wp.Input.Account), kind, wp.Input.StatementClosing, wp.Input.LedgerClosing, wp.ClosingDifference, wp.Status, p.id})
				for _, text := range wp.Blockers {
					ask(p.period, where, text)
				}
			}
		}

		postings := [][]string{{"period", "posting_id", "workpaper_id", "state", "external_id", "verified_at", "lines"}}
		rows, err = tx.QueryContext(ctx, `WITH y AS (`+yearCloses+`) SELECT y.period,p.id::text,p.workpaper_id::text,p.state,COALESCE(p.external_id,''),COALESCE(p.verified_at::text,''),p.journal FROM y JOIN production_postings p ON p.close_id=y.id ORDER BY y.period,p.id`, r.PathValue("id"), from)
		if err != nil {
			problem(w, 503, "Could not read production posting records.")
			return
		}
		for rows.Next() {
			var period, id, paperID, state, external, verified string
			var raw []byte
			if err = rows.Scan(&period, &id, &paperID, &state, &external, &verified, &raw); err != nil {
				break
			}
			var journal quickbooks.Journal
			json.Unmarshal(raw, &journal)
			lines := []string{}
			for _, line := range journal.Line {
				lines = append(lines, fmt.Sprintf("%s account %s %s", line.JournalEntryLineDetail.PostingType, line.JournalEntryLineDetail.AccountRef.Value, line.Amount))
			}
			postings = append(postings, []string{period, id, paperID, state, csvText(external), verified, csvText(strings.Join(lines, "; "))})
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			problem(w, 503, "Could not finish production posting records.")
			return
		}
		if err = tx.Commit(); err != nil {
			problem(w, 503, "Could not complete the snapshot.")
			return
		}

		var page bytes.Buffer
		if err = yearEndHTML.Execute(&page, map[string]any{"Entity": entity, "Year": year, "Cut": cut.UTC().Format(time.RFC3339), "From": from, "Through": anchor, "Months": months, "Questions": questions}); err != nil {
			problem(w, 500, "Could not render the handoff summary.")
			return
		}
		files["handoff.html"] = page.Bytes()
		for name, table := range map[string][][]string{
			"months.csv":          append([][]string{{"period", "close_id", "sign_off_state", "decision_id", "blockers"}}, months...),
			"prepaids.csv":        prepaidRows,
			"loans.csv":           loanRows,
			"reconciliations.csv": recRows,
			"adjustments.csv":     adjustments,
			"postings.csv":        postings,
			"open-questions.csv":  append([][]string{{"period", "where", "question"}}, questions...),
		} {
			var b bytes.Buffer
			csv.NewWriter(&b).WriteAll(table)
			files[name] = b.Bytes()
		}
		var buffer, checksums bytes.Buffer
		archive := zip.NewWriter(&buffer)
		for _, name := range slices.Sorted(maps.Keys(files)) {
			part, err := archive.Create(name)
			if err == nil {
				_, err = part.Write(files[name])
			}
			if err != nil {
				problem(w, 500, "Could not build the handoff archive.")
				return
			}
			fmt.Fprintf(&checksums, "%x  %s\n", sha256.Sum256(files[name]), name)
		}
		part, err := archive.Create("checksums.sha256")
		if err == nil {
			_, err = part.Write(checksums.Bytes())
		}
		if err == nil {
			err = archive.Close()
		}
		if err != nil {
			problem(w, 500, "Could not build the handoff archive.")
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="year-end-handoff-%s-%s.zip"`, r.PathValue("id"), year))
		w.Write(buffer.Bytes())
	}
}

// Each kind keeps one identity across versions and months.
func paperKey(kind string, raw json.RawMessage) string {
	var p struct {
		Input struct {
			SourceReference      string `json:"source_reference"`
			TransactionReference string `json:"transaction_reference"`
			Lender               string `json:"lender"`
			LoanAccount          string `json:"loan_account"`
			Account              string `json:"account"`
			AccountType          string `json:"account_type"`
			ReportReference      string `json:"report_reference"`
			Schedule             string `json:"schedule"`
			Provider             string `json:"provider"`
		} `json:"input"`
	}
	json.Unmarshal(raw, &p)
	in := p.Input
	parts := []string{in.SourceReference}
	switch kind {
	case "payroll":
		parts = []string{strings.ToLower(strings.TrimSpace(in.Provider))}
	case "prepaid":
		if in.Schedule != "" {
			parts = append(parts, in.Schedule)
		}
	case "loan":
		parts = []string{in.Lender, in.LoanAccount}
	case "owner":
		parts = []string{in.TransactionReference}
	case "settlement":
		parts = []string{in.ReportReference}
	case "reconciliation":
		if in.AccountType == "" {
			in.AccountType = "bank"
		}
		parts = []string{in.Account, in.AccountType}
	}
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, "\x00")
}

func shiftMonth(period string, months int) string {
	t, _ := time.Parse("2006-01", period)
	return t.AddDate(0, months, 0).Format("2006-01")
}

func sameAmount(a, b string) bool {
	x, okA := new(big.Rat).SetString(a)
	y, okB := new(big.Rat).SetString(b)
	return okA && okB && x.Cmp(y) == 0
}

func addAmount(sum *big.Rat, value string) {
	if v, ok := new(big.Rat).SetString(value); ok {
		sum.Add(sum, v)
	}
}
