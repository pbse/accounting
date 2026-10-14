package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"close.local/backend/internal/llm"
	"close.local/backend/internal/reconciliation"
)

// StraightReconciliation prepares an agent-read bank reconciliation without confirmation only when its facts need no
// person: the client's enabled reconciliation procedure applied to the close, the statement and ledger CSVs (with at most
// one supporting document, such as the statement's PDF, for the balances and pages), every fact one candidate cited from
// them, every statement page present, the home currency, and both roll-forwards and the opening bridge tying to zero,
// which proves the balances and rows complete. When the exact method also matches every row, the draft is complete. When
// some lines have no single exact match, it is still prepared with every match it has, and ask names those lines: the
// draft carries them as blockers, for a person to classify (outstanding, or what they are) and nothing else of it waits.
// Anything else (tolerances, splits, transfers, a prior month's outstanding items, a card the procedure does not cover)
// is a person's to review. It returns the plan, or the reason a person is needed.
func StraightReconciliation(p Proposal, selected []IntakeSource, procedure Procedure, procedureSource Source, currency, entity, period string, unread bool, otherMatter map[string]bool, capture *Ledger) (plan Plan, reason, ask string) {
	switch {
	case !llm.IsModelEngine(p.Engine) || p.Kind != "reconciliation":
		return Plan{}, "only an agent-read reconciliation can be prepared without confirmation", ""
	case len(p.Questions) > 0:
		return Plan{}, "the agent has open questions", ""
	case unread:
		return Plan{}, "evidence in the close is not readable yet, or belongs to another job's request or answer", ""
	case currency == "":
		return Plan{}, "the client's home currency is not detected from a connected ledger", ""
	case !procedure.Enabled || procedure.Kind != "reconciliation":
		return Plan{}, "no enabled reconciliation procedure for this client authorizes preparing reconciliations", ""
	case procedureSource.ID == "":
		return Plan{}, "the approved reconciliation procedure has not been applied to this close as evidence", ""
	}
	unrelated := []string{}
	for _, s := range p.Unselected {
		if s.ID == procedureSource.ID {
			continue
		}
		if !otherMatter[s.ID] {
			return Plan{}, "evidence in the close (" + s.Name + ") was not used; a person confirms it does not bear on this account", ""
		}
		unrelated = append(unrelated, s.ID)
	}
	// The procedure's own evidence copy, which the agent cites as support, is the plan's procedure source, not a document.
	sources, documents := map[string]IntakeSource{}, 0
	for _, s := range p.Sources {
		if s.ID == procedureSource.ID {
			continue
		}
		documents++
		for _, c := range selected {
			if c.ID == s.ID {
				c.Role = s.Role
				sources[s.Role] = c
			}
		}
		if s.Role != "statement" && s.Role != "ledger" && s.Role != "support" {
			return Plan{}, "evidence (" + s.Name + ") was selected as " + s.Role + "; a person reviews it", ""
		}
	}
	statement, ledger := sources["statement"], sources["ledger"]
	supports := map[string]IntakeSource{}
	for _, s := range p.Sources {
		if s.Role == "support" && s.ID != procedureSource.ID {
			for _, c := range selected {
				if c.ID == s.ID {
					supports[c.ID] = c
				}
			}
		}
	}
	if statement.ID == "" || ledger.ID == "" || documents != 2+len(supports) || len(supports) > 2 {
		return Plan{}, "a reconciliation needs the statement and the ledger export, each once, with at most two supporting documents", ""
	}
	// The statement's balances come from the statement or a supporting document (its PDF); the books' balances from the
	// ledger export or a supporting document that gives no statement figure, so the bank's own numbers never stand in for
	// the books'.
	facts, statementSide, ledgerSide := map[string]string{}, map[string]bool{}, map[string]bool{}
	for _, f := range p.Facts {
		if f.Value == "" || len(f.Candidates) != 1 {
			return Plan{}, "the " + f.Label + " is not one candidate read from the source", ""
		}
		from := f.Candidates[0].SourceID
		if from != statement.ID && from != ledger.ID && supports[from].ID == "" || strings.HasPrefix(f.Field, "statement_") && from == ledger.ID || strings.HasPrefix(f.Field, "ledger_") && from == statement.ID {
			return Plan{}, "the " + f.Label + " is not cited from its own document", ""
		}
		if strings.HasPrefix(f.Field, "statement_") {
			statementSide[from] = true
		}
		if strings.HasPrefix(f.Field, "ledger_") {
			ledgerSide[from] = true
			// A books balance taken from a supporting document must be printed as one: its line speaks of the ledger, the
			// books or a trial balance, and not of a statement or the bank.
			quote := strings.ToLower(f.Candidates[0].Quote)
			if from != ledger.ID && (!mentionsAny(quote, []string{"ledger", "books", "book balance", "trial balance"}) || mentionsAny(quote, []string{"statement", "bank balance"})) {
				return Plan{}, "the " + f.Label + " is not printed as the books' balance; a person confirms it from the ledger", ""
			}
		}
		facts[f.Field] = strings.TrimSpace(f.Value)
	}
	for id := range ledgerSide {
		if statementSide[id] {
			return Plan{}, "the books' balances are cited from the same document as the statement's; a person confirms them from the ledger", ""
		}
	}
	for _, field := range []string{"account", "currency", "statement_opening", "statement_closing", "ledger_opening", "ledger_closing", "expected_pages", "pages"} {
		if facts[field] == "" {
			return Plan{}, "the " + strings.ReplaceAll(field, "_", " ") + " was not read from the source", ""
		}
	}
	if !strings.EqualFold(facts["currency"], currency) {
		return Plan{}, "the statement is not in the client's home currency", ""
	}
	// Every page the statement says it has must have been seen: 1 to its count, each once.
	expected, err := strconv.Atoi(facts["expected_pages"])
	if err != nil || expected < 1 || expected > 500 {
		return Plan{}, "the statement's page count is not a whole number of pages", ""
	}
	pages := []int{}
	for _, part := range strings.Split(facts["pages"], ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return Plan{}, "the statement's pages are not whole page numbers", ""
		}
		pages = append(pages, n)
	}
	slices.Sort(pages)
	for i, n := range pages {
		if n != i+1 || len(pages) != expected {
			return Plan{}, "a statement page is missing or repeated", ""
		}
	}
	location := func(s IntakeSource) string {
		if s.Location != "" {
			return s.Name + " · " + s.Location
		}
		return s.Name + " · all rows"
	}
	// A card is a card by the captured chart's type for the account. It is prepared without a person only as a card
	// liability, and only when the client's reconciliation procedure approves card accounts. A statement that reads like a
	// card's (a credit limit, a minimum payment) for an account the chart does not name a card is a person's to place.
	accountType, cardRule := "bank", ""
	if capture != nil {
		ids := capture.IDs[strings.ToLower(facts["account"])]
		if len(ids) > 1 {
			return Plan{}, "the captured chart has more than one account named " + facts["account"] + "; a person confirms which this is", ""
		}
		if len(ids) == 1 && capture.Types[ids[0]] == "Credit Card" {
			accountType = "credit_card"
		}
	}
	if accountType == "bank" {
		for _, s := range append([]IntakeSource{statement}, slices.Collect(maps.Values(supports))...) {
			if mentionsAny(s.Content, []string{"credit limit", "minimum payment"}) {
				return Plan{}, "the statement reads like a card's (a credit limit or a minimum payment), but the captured chart does not name " + facts["account"] + " a card; a person confirms which it is", ""
			}
		}
	}
	if accountType == "credit_card" {
		if !procedure.CoversCards {
			return Plan{}, "the account is a card, and the client's reconciliation procedure does not approve card accounts for preparation without confirmation", ""
		}
		cardRule = "Approved reconciliation procedure v" + procedure.ID
		// The card method reads a balance owed as a positive liability that charges increase: the printed balances, which
		// the rows must roll forward to, say the files are signed that way only when none is negative.
		for _, field := range []string{"statement_opening", "statement_closing", "ledger_opening", "ledger_closing"} {
			if strings.HasPrefix(strings.TrimSpace(facts[field]), "-") {
				return Plan{}, "a card balance is read as negative (" + strings.ReplaceAll(field, "_", " ") + "); a person confirms how the card's files are signed", ""
			}
		}
	}
	input := reconciliation.Input{Entity: entity, Account: facts["account"], AccountType: accountType, CardRuleReference: cardRule, ClosePeriod: period, Currency: strings.ToUpper(facts["currency"]),
		StatementReference: location(statement), LedgerReference: location(ledger), ExpectedPages: expected, Pages: pages,
		StatementOpening: facts["statement_opening"], StatementClosing: facts["statement_closing"], LedgerOpening: facts["ledger_opening"], LedgerClosing: facts["ledger_closing"],
		StatementCSV: statement.Content, LedgerCSV: ledger.Content, PolicyConfirmed: true}
	paper, err := reconciliation.Prepare(input)
	if err != nil {
		return Plan{}, "the reconciliation could not be prepared from these facts: " + err.Error(), ""
	}
	// With every page present, both roll-forwards and the opening bridge at zero, the only blockers left are lines with
	// no single exact match and the closing bridge they leave open.
	complete := paper.StatementRollForwardDifference == "0.00" && paper.LedgerRollForwardDifference == "0.00" && paper.OpeningBridgeDifference == "0.00"
	unmatched := len(paper.UnmatchedStatement) + len(paper.UnmatchedLedger)
	switch {
	case complete && unmatched > 0:
		ask = fmt.Sprintf("%d statement and %d ledger line(s) with no single exact match", len(paper.UnmatchedStatement), len(paper.UnmatchedLedger))
	case len(paper.Exceptions) > 0:
		return Plan{}, fmt.Sprintf("%d row(s) have no single exact match", len(paper.Exceptions)), ""
	case len(paper.Blockers) > 0:
		return Plan{}, paper.Blockers[0], ""
	case unmatched > 0:
		return Plan{}, "unmatched rows remain; outstanding items are a person's to review", ""
	}
	for _, d := range []string{paper.StatementRollForwardDifference, paper.LedgerRollForwardDifference, paper.OpeningDifference, paper.ClosingDifference, paper.OpeningBridgeDifference, paper.ClosingBridgeDifference} {
		if ask == "" && d != "" && d != "0.00" {
			return Plan{}, "the balances do not tie: a difference of " + d + " remains", ""
		}
	}
	raw, _ := json.Marshal(input)
	cited := []Source{statement.Source, ledger.Source}
	for _, s := range p.Sources {
		if supports[s.ID].ID != "" {
			cited = append(cited, s)
		}
	}
	cited = append(cited, Source{ID: procedureSource.ID, Digest: procedureSource.Digest, Name: procedureSource.Name, Role: "procedure", Location: "approved procedure"})
	return Plan{Kind: "reconciliation", Input: raw, Procedure: procedure.ID, Sources: cited, Unrelated: unrelated}, "", ask
}

// ReconciliationOtherMatter is the close's evidence a bank reconciliation of account may leave unused: QuickBooks
// captures and charts, procedure copies, and OtherWork.
func ReconciliationOtherMatter(ctx context.Context, tx *sql.Tx, closeID, jobID, account string) (map[string]bool, error) {
	other, err := OtherWork(ctx, tx, closeID, jobID, account)
	if err != nil {
		return nil, err
	}
	ledger, err := CloseLedger(ctx, tx, closeID)
	if err != nil {
		return nil, err
	}
	if ledger != nil {
		for machine := range ledger.Machine {
			other[machine] = true
		}
		other[ledger.Source.ID], other[ledger.ChartSource.ID] = true, true
	}
	return other, nil
}

// OtherWork is the close's evidence that is another matter for a bank reconciliation of account: a procedure copy,
// another job's own invoice, accrual, payroll or loan document, or a document another job's reconciliation of a
// different account cites. Any other statement, CSV, or unread or unsupported document is not.
func OtherWork(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, closeID, jobID, account string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `SELECT e.id::text FROM evidence e JOIN preparation_intakes i ON i.source_id=e.id AND i.job_id::text<>$2
 WHERE e.close_id::text=$1 AND e.superseded_by IS NULL AND e.extraction_state='ready' AND i.proposal->>'kind' IN ('prepaid','accrual','payroll','loan','deferred_revenue','depreciation','owner')
 AND lower(e.name) NOT LIKE '%.csv'
 UNION SELECT s->>'id' FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id CROSS JOIN jsonb_array_elements(COALESCE(NULLIF(i.proposal->'sources','null'::jsonb),'[]'::jsonb)) s
 WHERE j.close_id::text=$1 AND i.job_id::text<>$2 AND i.proposal->>'kind'='reconciliation'
 AND COALESCE((SELECT lower(btrim(f->>'value')) FROM jsonb_array_elements(COALESCE(NULLIF(i.proposal->'facts','null'::jsonb),'[]'::jsonb)) f WHERE f->>'field'='account' LIMIT 1),'') NOT IN ('',lower(btrim($3)))
 UNION SELECT id::text FROM evidence WHERE close_id::text=$1 AND procedure_id IS NOT NULL AND superseded_by IS NULL`, closeID, jobID, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ReconciliationDrafted says why a straight-through reconciliation for an account waits: the close already has a current
// draft for that account, which a person decides this one replaces or not; "" when it has none.
func ReconciliationDrafted(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID string, plan Plan) (string, error) {
	var in reconciliation.Input
	json.Unmarshal(plan.Input, &in)
	var drafted bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workpapers w WHERE w.close_id::text=$1 AND w.kind='reconciliation' AND lower(w.paper->'input'->>'account')=lower($2)
 AND NOT EXISTS(SELECT 1 FROM workpapers n WHERE n.close_id=w.close_id AND n.paper->>'revises'=w.id::text))`, closeID, in.Account).Scan(&drafted); err != nil || !drafted {
		return "", err
	}
	return "a reconciliation of " + in.Account + " is already drafted this month; a person decides whether this one replaces it", nil
}

// queueReconciliation queues a straight-through reconciliation unless the close already has a current draft for its
// account.
func queueReconciliation(ctx context.Context, tx *sql.Tx, closeID, jobID string, plan Plan) (string, error) {
	if drafted, err := ReconciliationDrafted(ctx, tx, closeID, plan); err != nil || drafted != "" {
		return drafted, err
	}
	return "", queuePlan(ctx, tx, jobID, plan)
}

// queuePlan queues a straight-through plan as the job's, confirmed by the agent.
func queuePlan(ctx context.Context, tx *sql.Tx, jobID string, plan Plan) error {
	plans, _ := json.Marshal([]Plan{plan})
	if _, err := tx.ExecContext(ctx, `UPDATE preparation_jobs SET plans=$2,state='queued',error='',finished_at=NULL WHERE id=$1`, jobID, string(plans)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE preparation_intakes SET confirmed_by='agent',confirmed_at=now() WHERE job_id=$1`, jobID)
	return err
}
