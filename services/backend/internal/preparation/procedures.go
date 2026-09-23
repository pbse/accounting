package preparation

import (
	"context"
	"database/sql"
	"fmt"
)

type Procedure struct {
	ID             string `json:"id"`
	ClientEntity   string `json:"client_entity"`
	Kind           string `json:"kind"`
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
	err := tx.QueryRowContext(ctx, `SELECT p.id::text,p.client_entity,p.kind,p.instructions,p.expense_account,p.prepaid_account,p.enabled,p.reason,p.actor,p.created_at::text FROM procedure_versions p JOIN client_closes c ON c.organization_id=p.organization_id WHERE c.id::text=$1 AND p.kind=$2 AND (p.client_entity='' OR lower(p.client_entity)=lower(c.entity)) ORDER BY (p.client_entity<>'') DESC,p.id DESC LIMIT 1`, closeID, kind).Scan(&p.ID, &p.ClientEntity, &p.Kind, &p.Instructions, &p.ExpenseAccount, &p.PrepaidAccount, &p.Enabled, &p.Reason, &p.Actor, &p.CreatedAt)
	return p, err
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
	} else {
		text += "Supported method: exact_bank_month_v1 begins with unique exact-date and signed-amount CSV candidates. Grouped and tolerance matches, timing items and two-account transfer pairs require separate cited accountant-approved rules and source review. Native reconciliation status is separately observed.\n"
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
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))),0) FROM evidence WHERE close_id::text=$1`, closeID).Scan(&count, &size); err != nil {
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
