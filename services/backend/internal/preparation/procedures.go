package preparation

import (
	"close.local/backend/internal/loans"
	"close.local/backend/internal/payroll"
	"close.local/backend/internal/prepaids"
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type Procedure struct {
	ID             string `json:"id"`
	ClientEntity   string `json:"client_entity"`
	Kind           string `json:"kind"`
	Method         string `json:"method"`
	Instructions   string `json:"instructions"`
	ExpenseAccount string `json:"expense_account"`
	PrepaidAccount string `json:"prepaid_account"`
	Enabled        bool   `json:"enabled"`
	Reason         string `json:"reason"`
	Actor          string `json:"actor"`
	CreatedAt      string `json:"created_at"`
}

// A client override replaces the firm default; disabled overrides suppress it.
func CurrentProcedure(ctx context.Context, tx *sql.Tx, closeID, kind string) (Procedure, error) {
	var p Procedure
	err := tx.QueryRowContext(ctx, `SELECT p.id::text,p.client_entity,p.kind,p.method,p.instructions,p.expense_account,p.prepaid_account,p.enabled,p.reason,p.actor,p.created_at::text FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind=$2 AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, closeID, kind).Scan(&p.ID, &p.ClientEntity, &p.Kind, &p.Method, &p.Instructions, &p.ExpenseAccount, &p.PrepaidAccount, &p.Enabled, &p.Reason, &p.Actor, &p.CreatedAt)
	return p, err
}

// Profile is the client's home currency and first fiscal month for a month: its confirmed profile in force then, else
// what its active ledger connections report. State is confirmed (a confirmed profile, which no connection contradicts),
// differs (a confirmed profile a connection contradicts: a question, never an override), detected, none (no ledger
// connected), unknown (a connection predates detection; USD) or conflict (connections disagree, nothing confirmed).
type Profile struct {
	Currency    string `json:"currency"`
	FiscalStart int    `json:"fiscal_year_start"`
	State       string `json:"state"`
	// EffectiveFrom is the month the confirmed profile in force took effect, when one is.
	EffectiveFrom string `json:"effective_from,omitempty"`
	// Ledger is what the connected ledgers report when it differs from the confirmed profile; Conflict says they also
	// disagree with one another.
	Ledger *LedgerProfile `json:"ledger,omitempty"`
}

// LedgerProfile is what a client's connected ledgers report that differs from its confirmed profile: a currency, a fiscal
// year's first month (0 when that agrees), or that they disagree with one another.
type LedgerProfile struct {
	Currency    string `json:"currency,omitempty"`
	FiscalStart int    `json:"fiscal_year_start,omitempty"`
	Conflict    bool   `json:"conflict,omitempty"`
}

// Unsettled says the profile has an open question that holds what depends on it: connected ledgers that disagree with
// one another, or with the confirmed profile.
func (p Profile) Unsettled() bool { return p.State == "conflict" || p.State == "differs" }

// Question says what the open question is, plainly, or "" when there is none.
func (p Profile) Question() string {
	switch {
	case p.State == "conflict":
		return "This client's connected ledgers report different home currencies or fiscal years; disconnect the wrong one"
	case p.State == "differs" && p.Ledger.Conflict:
		return fmt.Sprintf("This client's confirmed profile is %s from month %d, but its connected ledgers disagree with each other; disconnect the wrong one", p.Currency, p.FiscalStart)
	case p.State == "differs":
		var reports []string
		if p.Ledger.Currency != "" {
			reports = append(reports, p.Ledger.Currency)
		}
		if p.Ledger.FiscalStart != 0 {
			reports = append(reports, fmt.Sprintf("a fiscal year from month %d", p.Ledger.FiscalStart))
		}
		return fmt.Sprintf("This client's confirmed profile is %s with its year from month %d, but its connected ledger reports %s; correct the profile or the connection", p.Currency, p.FiscalStart, strings.Join(reports, " and "))
	}
	return ""
}

// ClientProfile is the profile of a close's client for the close's month, from its connections under any of the
// client's names (a month opened under an alias reads the client's connections).
func ClientProfile(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID string) (Profile, error) {
	return clientProfile(ctx, q, `WITH c AS (SELECT organization_id,entity,period FROM client_closes WHERE id::text=$1)`, closeID)
}

// ClientProfileOf is ClientProfile for a client by its name in firm org, with no close needed, for month (YYYY-MM), read
// from its connections under any of its names (a connection is kept under the name of the close it was made from, which
// may since have been confirmed as an alias): the client page reads it before any month is opened.
func ClientProfileOf(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, org, entity, month string) (Profile, error) {
	return clientProfile(ctx, q, `WITH c AS (SELECT $1::text AS organization_id,$2::text AS entity,$3::text AS period)`, org, entity, month)
}

// clientProfile reads the profile of the one client the WITH clause c names (its organization_id, entity and the month,
// period), from its confirmed profile and its active connections, both matched by client identity.
func clientProfile(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, client string, args ...any) (Profile, error) {
	p := Profile{Currency: "USD", FiscalStart: 1}
	var raw []byte
	var confirmed, effective sql.NullString
	var confirmedStart sql.NullInt64
	var superseded bool
	err := q.QueryRowContext(ctx, client+`,
 x AS (SELECT q.home_currency,q.fiscal_year_start FROM quickbooks_connections q JOIN c ON c.organization_id=q.organization_id AND client_identity(q.organization_id,q.client_entity)=client_identity(c.organization_id,c.entity) WHERE q.state='active'
 UNION ALL SELECT r.home_currency,r.fiscal_year_start FROM xero_connections r JOIN c ON c.organization_id=r.organization_id AND client_identity(r.organization_id,r.client_entity)=client_identity(c.organization_id,c.entity) WHERE r.state='active'),
 f AS (SELECT f.currency,f.fiscal_year_start,f.effective_from FROM client_profiles f, c WHERE f.organization_id=c.organization_id AND client_identity(f.organization_id,f.client_entity)=client_identity(c.organization_id,c.entity) AND f.effective_from<=c.period ORDER BY f.effective_from DESC,f.id DESC LIMIT 1)
 SELECT COALESCE((SELECT jsonb_agg(jsonb_build_object('currency',home_currency,'start',fiscal_year_start)) FROM x),'[]'::jsonb),(SELECT currency FROM f),(SELECT fiscal_year_start FROM f),(SELECT effective_from FROM f),
 EXISTS(SELECT 1 FROM client_profiles l, c WHERE l.organization_id=c.organization_id AND client_identity(l.organization_id,l.client_entity)=client_identity(c.organization_id,c.entity) AND l.effective_from>(SELECT effective_from FROM f))`, args...).Scan(&raw, &confirmed, &confirmedStart, &effective, &superseded)
	if err != nil {
		return p, err
	}
	var connections []struct {
		Currency *string `json:"currency"`
		Start    *int    `json:"start"`
	}
	json.Unmarshal(raw, &connections)
	currencies, starts := map[string]bool{}, map[int]bool{}
	known := 0
	for _, c := range connections {
		if c.Currency != nil {
			currencies[*c.Currency] = true
			known++
		}
		if c.Start != nil {
			starts[*c.Start] = true
		}
	}
	switch {
	case len(connections) == 0:
		p.State = "none"
	case len(currencies) > 1 || len(starts) > 1:
		p.State = "conflict"
	case known < len(connections):
		p.State = "unknown"
	default:
		p.State = "detected"
		for c := range currencies {
			p.Currency = c
		}
		for s := range starts {
			p.FiscalStart = s
		}
	}
	if !confirmed.Valid {
		return p, nil
	}
	// The confirmed profile stands; a connection that reports otherwise, in any field it reports, asks a question. A
	// connection reports the client's books as they are now, which the newest profile answers for: a month under a
	// profile a later one has superseded keeps its own, unasked.
	confirmedProfile := Profile{Currency: confirmed.String, FiscalStart: int(confirmedStart.Int64), State: "confirmed", EffectiveFrom: effective.String}
	if superseded {
		return confirmedProfile, nil
	}
	if len(currencies) > 1 || len(starts) > 1 {
		confirmedProfile.State, confirmedProfile.Ledger = "differs", &LedgerProfile{Conflict: true}
		return confirmedProfile, nil
	}
	// Ledger names only what differs: the currency, the fiscal year's first month, or both.
	ledger := LedgerProfile{}
	differs := false
	for c := range currencies {
		if c != confirmedProfile.Currency {
			ledger.Currency, differs = c, true
		}
	}
	for s := range starts {
		if s != confirmedProfile.FiscalStart {
			ledger.FiscalStart, differs = s, true
		}
	}
	if differs {
		confirmedProfile.State, confirmedProfile.Ledger = "differs", &ledger
	}
	return confirmedProfile, nil
}

// CurrencyIssue refuses a draft that is not in the client's detected home currency.
func CurrencyIssue(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID string, paper []byte) error {
	profile, err := ClientProfile(ctx, q, closeID)
	if err != nil {
		return err
	}
	var money struct {
		Input struct{ Currency string } `json:"input"`
	}
	json.Unmarshal(paper, &money)
	currency := cmp.Or(money.Input.Currency, "USD")
	if profile.Unsettled() {
		return fmt.Errorf("%s before saving drafts", profile.Question())
	}
	if profile.State != "none" && currency != profile.Currency {
		return fmt.Errorf("This draft is in %s but the client's books are kept in %s; prepare it in %s", currency, profile.Currency, profile.Currency)
	}
	return nil
}

// ClientAliases returns the close client's confirmed aliases that are not retired, normalized.
func ClientAliases(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, closeID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.alias FROM client_aliases a JOIN client_closes c ON c.organization_id=a.organization_id AND lower(c.entity)=lower(a.client_entity) WHERE c.id::text=$1 AND a.retired_at IS NULL`, closeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	aliases := []string{}
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	return aliases, rows.Err()
}

// BasisIssue refuses work the client's approved basis does not support. Firms that have
// not set any basis keep per-draft accrual confirmations; basis versions are never disabled.
func BasisIssue(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID, kind string, paper []byte) (string, error) {
	if kind != "accrual" && kind != "prepaid" && kind != "loan" {
		return "", nil
	}
	var basis string
	err := q.QueryRowContext(ctx, `SELECT p.method FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind='basis' AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, closeID).Scan(&basis)
	if err == sql.ErrNoRows || basis == "accrual" {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	label := strings.ReplaceAll(basis, "_", " ")
	switch kind {
	case "accrual":
		return fmt.Sprintf("Accrued expense schedules are unsupported for this client's %s basis.", label), nil
	case "prepaid":
		return fmt.Sprintf("Prepaid schedules are unsupported for this client's %s basis.", label), nil
	}
	var loan struct {
		Input struct {
			AccrueInterest bool             `json:"accrue_interest"`
			IssuanceCosts  *json.RawMessage `json:"issuance_costs"`
		} `json:"input"`
	}
	if json.Unmarshal(paper, &loan) == nil && loan.Input.AccrueInterest {
		return fmt.Sprintf("Accrued loan interest is unsupported for this client's %s basis; prepare the loan without accruing interest.", label), nil
	}
	if loan.Input.IssuanceCosts != nil {
		return fmt.Sprintf("Amortizing debt issuance costs is unsupported for this client's %s basis.", label), nil
	}
	return "", nil
}

var requiredMethods = map[string][]string{"loan": {loans.Method, loans.VariableMethod, loans.CostsMethod}, "payroll": {payroll.Method}}

// PolicyRequired refuses work because an accountant-approved procedure is missing or contradicted.
type PolicyRequired string

func (p PolicyRequired) Error() string { return string(p) }

func CheckProcedureMethod(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID, kind string, paper []byte) error {
	if issue, err := BasisIssue(ctx, q, closeID, kind, paper); err != nil || issue != "" {
		if err != nil {
			return err
		}
		return errors.New(issue)
	}
	if err := CurrencyIssue(ctx, q, closeID, paper); err != nil {
		return err
	}
	if kind == "owner" {
		var entityType struct {
			Input struct {
				EntityType string `json:"entity_type"`
			} `json:"input"`
		}
		json.Unmarshal(paper, &entityType)
		var recorded string
		err := q.QueryRowContext(ctx, `SELECT CASE WHEN p.enabled THEN p.method ELSE '' END FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind='owner' AND lower(p.client_entity)=lower(c.entity) ORDER BY p.id DESC LIMIT 1`, closeID).Scan(&recorded)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if recorded == "" {
			return PolicyRequired("Record this client's entity type under Owner treatments before preparing owner reclassifications")
		}
		if recorded != entityType.Input.EntityType {
			return PolicyRequired(fmt.Sprintf("The draft's entity type differs from the client's recorded entity type (%s)", recorded))
		}
		return nil
	}
	if methods := requiredMethods[kind]; methods != nil {
		var enabled bool
		var approved string
		err := q.QueryRowContext(ctx, `SELECT p.enabled,p.method FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind=$2 AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, closeID, kind).Scan(&enabled, &approved)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows || !enabled || !slices.Contains(methods, approved) {
			return PolicyRequired(map[string]string{"loan": "Loan schedules", "payroll": "Payroll entries"}[kind] + " need the firm's approved " + kind + " procedure for this client (a firm default or client override) before drafts are saved")
		}
	}
	var method string
	err := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT p.method FROM evidence e JOIN procedure_versions p ON p.id=e.procedure_id WHERE e.close_id::text=$1 AND p.kind=$2 AND e.superseded_by IS NULL ORDER BY e.id DESC LIMIT 1),(SELECT p.method FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind=$2 AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1),'')`, closeID, kind).Scan(&method)
	if err != nil || method == "" {
		return err
	}
	var result struct {
		Method string `json:"method"`
		Input  struct {
			Method, Convention string
		} `json:"input"`
	}
	if json.Unmarshal(paper, &result) != nil {
		return fmt.Errorf("Cannot verify the prepared method against the approved procedure")
	}
	used := result.Method
	if used == "" {
		used = result.Input.Method
	}
	// A convention changes the calculation, so it is part of what the procedure must approve.
	used = prepaids.Boundary(used, result.Input.Convention)
	if used != method {
		return PolicyRequired(fmt.Sprintf("Prepared method %q differs from the approved procedure method %q; review the procedure before saving", used, method))
	}
	return nil
}

// Approved instructions become immutable, cited context. They cannot confirm
// invoice booking or authorize a journal, and do not change calculator methods.
func AttachProcedure(ctx context.Context, tx *sql.Tx, closeID string, p Procedure, replace bool, actor string) (string, error) {
	var current string
	err := tx.QueryRowContext(ctx, `SELECT e.id::text FROM evidence e JOIN procedure_versions p ON p.id=e.procedure_id WHERE e.close_id::text=$1 AND p.kind=$2 AND e.superseded_by IS NULL ORDER BY e.id DESC LIMIT 1`, closeID, p.Kind).Scan(&current)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if current != "" && !replace {
		return current, nil
	}
	if !p.Enabled {
		return "", nil
	}
	text := fmt.Sprintf("Approved firm procedure version: %s\nWork kind: %s\nInstructions: %s\nApproval actor: %s\nApproval reason: %s\nThese instructions do not prove invoice booking, source completeness or ledger state. Human method and booking confirmations remain required.\n", p.ID, p.Kind, p.Instructions, p.Actor, p.Reason)
	if p.Method != "" {
		text += "Approved method boundary: " + p.Method + "\n"
	}
	if p.Kind == "prepaid" {
		text += "Supported allocation: equal_full_months_v1 or actual_days_v1; depreciation may also use declining_balance_v1 and the mid-month convention (written method+mid_month) under the client accountant's cited depreciation policy (accrual, client's home currency). Multiple or retrospective credits require separately cited entries and the client accountant's approved rule reference. Method and booking are confirmed per draft.\n"
		if p.ExpenseAccount != "" {
			text += "Expense account: " + p.ExpenseAccount + "\n"
		}
		if p.PrepaidAccount != "" {
			text += "Prepaid account: " + p.PrepaidAccount + "\n"
		}
	} else if p.Kind == "accrual" {
		text += "Supported method: fixed_incurred_amount_v1, actual_invoice_true_up_v1, fixed_recurring_incurred_v1 or pro_rata_service_days_v1 (the elapsed share of a contract amount less what is invoiced). For a true-up, cite the prior estimate and actual invoice; the true-up, recurring and pro-rata methods require the client accountant's approved rule reference. Confirm incurred amount, booked liability and reversal choice; never infer estimates.\n"
	} else if p.Kind == "loan" {
		text += "Supported method: lender_statement_v1. The lender statement's principal/interest split is authoritative; recomputed interest raises questions. Accrued interest and current portion entries need the preparer's per-draft choices and balances that tie to the lender.\n"
	} else {
		text += "Supported methods: exact_bank_month_v1 uses positive deposits; card_liability_month_v1 uses positive charges and negative payments/credits under a cited accountant-approved card rule. Both begin with unique exact-date and signed-amount CSV candidates. Grouped matches, net splits, tolerances, timing and clearing items, and transfers require cited accountant-approved rules and source review. Native reconciliation status is separately observed.\n"
	}
	var source string
	var usable bool
	err = tx.QueryRowContext(ctx, `SELECT id::text,superseded_by IS NULL FROM evidence WHERE close_id::text=$1 AND digest=$2`, closeID, Digest([]byte(text))).Scan(&source, &usable)
	if err == nil {
		if !usable {
			return "", fmt.Errorf("This procedure copy was superseded. Publish a new reviewed version before applying it again.")
		}
		return source, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	var count, size int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id::text=$1`, closeID).Scan(&count, &size); err != nil {
		return "", err
	}
	if count >= 100 || size+len(text) > 20*1024*1024 {
		return "", fmt.Errorf("Close evidence capacity cannot hold the approved procedure.")
	}
	if current != "" {
		var active bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sandbox_postings p JOIN workpapers w ON w.id=p.workpaper_id WHERE w.close_id::text=$1 AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',$2::text)) AND p.state IN ('executing','unknown'))`, closeID, current).Scan(&active)
		if err != nil {
			return "", err
		}
		if active {
			return "", fmt.Errorf("Resolve the affected sandbox attempt before changing procedure context.")
		}
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by,procedure_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING id::text`, closeID, "Approved "+p.Kind+" procedure v"+p.ID+".txt", Digest([]byte(text)), text, actor, p.ID).Scan(&source)
	if err != nil {
		return "", err
	}
	if current != "" {
		_, err = tx.ExecContext(ctx, `UPDATE evidence SET superseded_by=$2,replacement_reason='Explicitly applied newer approved procedure',replaced_by=$3,replaced_at=now() WHERE id::text=$1`, current, source, actor)
		if err != nil {
			return "", err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sandbox_postings p SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM workpapers w WHERE w.id=p.workpaper_id AND w.close_id::text=$1 AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',$2::text)) AND p.state='approved'`, closeID, current)
	}
	return source, err
}
