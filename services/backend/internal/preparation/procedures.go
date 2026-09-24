package preparation

import (
	"close.local/backend/internal/loans"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
			AccrueInterest bool `json:"accrue_interest"`
		} `json:"input"`
	}
	if json.Unmarshal(paper, &loan) == nil && loan.Input.AccrueInterest {
		return fmt.Sprintf("Accrued loan interest is unsupported for this client's %s basis; prepare the loan without accruing interest.", label), nil
	}
	return "", nil
}

func CheckProcedureMethod(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, closeID, kind string, paper []byte) error {
	if issue, err := BasisIssue(ctx, q, closeID, kind, paper); err != nil || issue != "" {
		if err != nil {
			return err
		}
		return errors.New(issue)
	}
	if kind == "loan" {
		var enabled bool
		var approved string
		err := q.QueryRowContext(ctx, `SELECT p.enabled,p.method FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind='loan' AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, closeID).Scan(&enabled, &approved)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == sql.ErrNoRows || !enabled || approved != loans.Method {
			return fmt.Errorf("Loan schedules need the firm's approved loan procedure for this client (a firm default or client override) before drafts are saved")
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
			Method string `json:"method"`
		} `json:"input"`
	}
	if json.Unmarshal(paper, &result) != nil {
		return fmt.Errorf("Cannot verify the prepared method against the approved procedure")
	}
	used := result.Method
	if used == "" {
		used = result.Input.Method
	}
	if used != method {
		return fmt.Errorf("Prepared method %q differs from the approved procedure method %q; review the procedure before saving", used, method)
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
		text += "Supported allocation: equal_full_months_v1 or actual_days_v1 (USD accrual). Multiple or retrospective credits require separately cited entries and the client accountant's approved rule reference. Method and booking are confirmed per draft.\n"
		if p.ExpenseAccount != "" {
			text += "Expense account: " + p.ExpenseAccount + "\n"
		}
		if p.PrepaidAccount != "" {
			text += "Prepaid account: " + p.PrepaidAccount + "\n"
		}
	} else if p.Kind == "accrual" {
		text += "Supported method: fixed_incurred_amount_v1 or actual_invoice_true_up_v1. Cite the prior estimate and actual invoice; the true-up requires the client accountant's approved rule reference. Confirm incurred amount, booked liability and reversal choice; never infer estimates.\n"
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
