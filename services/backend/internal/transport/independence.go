package transport

import (
	"close.local/backend/internal/llm"
	"context"
	"database/sql"
	"net/http"
	"slices"
	"strings"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
)

func actorPermissions(r *http.Request) []string {
	if a := auth.Actor(r.Context()); a != nil {
		return a.Permissions
	}
	return nil
}

type querier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// independenceIssue explains why approver may not approve paperID under the firm's policy, or returns "". The policy
// applies to every draft, or only to drafts whose proposed entries total at least the firm's minimum (an accrual's
// next-month reversal is not counted, as it is never posted).
func independenceIssue(ctx context.Context, q querier, closeID, paperID, approver string) (string, error) {
	var always, large bool
	var minimum string
	// The firm's row stays locked so a policy change cannot slip between this check and the approval.
	err := q.QueryRowContext(ctx, `SELECT o.require_independent_review,COALESCE(o.independent_review_min_amount::text,''),o.independent_review_min_amount IS NOT NULL AND (SELECT COALESCE((w.paper->'proposal'->>'amount')::numeric,0)+COALESCE((SELECT sum((p->>'amount')::numeric) FROM jsonb_array_elements(CASE WHEN jsonb_typeof(w.paper->'proposals')='array' THEN w.paper->'proposals' ELSE '[]'::jsonb END) p),0) FROM workpapers w WHERE w.id::text=$2)>=o.independent_review_min_amount
 FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1 FOR SHARE OF o`, closeID, paperID).Scan(&always, &minimum, &large)
	if err != nil || !always && !large {
		return "", err
	}
	rule := "This firm requires an independent reviewer"
	if !always {
		rule += " for drafts whose proposed entries total at least " + minimum
	}
	var preparers []string
	err = q.QueryRowContext(ctx, `SELECT COALESCE(array_agg(DISTINCT p.who) FILTER (WHERE p.who<>''),'{}') FROM (
 SELECT w.created_by AS who FROM workpapers w WHERE w.id::text=$2 AND w.created_by IS NOT NULL
 UNION ALL SELECT j.created_by FROM preparation_jobs j WHERE j.close_id::text=$1 AND j.workpaper_ids @> jsonb_build_array($2::text)
 UNION ALL SELECT i.confirmed_by FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id::text=$1 AND j.workpaper_ids @> jsonb_build_array($2::text) AND i.confirmed_by IS NOT NULL
) p`, closeID, paperID).Scan(&preparers)
	if err != nil {
		return "", err
	}
	if len(preparers) == 0 {
		return rule + ", and no preparer is recorded for this draft. Prepare it from linked evidence first.", nil
	}
	if slices.Contains(preparers, approver) {
		return rule + ". You prepared or confirmed this draft, so another member must approve it.", nil
	}
	return "", nil
}

// correctionIndependenceIssue applies the firm's independent-review rule to a native QuickBooks correction: when the rule
// covers its amount, whoever prepared the correction cannot approve it.
func correctionIndependenceIssue(ctx context.Context, q querier, closeID, proposer, approver, amount string) (string, error) {
	var always, large bool
	var minimum string
	err := q.QueryRowContext(ctx, `SELECT o.require_independent_review,COALESCE(o.independent_review_min_amount::text,''),o.independent_review_min_amount IS NOT NULL AND $2::numeric>=o.independent_review_min_amount
 FROM client_closes c JOIN organizations o ON o.id=c.organization_id WHERE c.id::text=$1 FOR SHARE OF o`, closeID, amount).Scan(&always, &minimum, &large)
	if err != nil || !always && !large || proposer != "" && proposer != approver {
		return "", err
	}
	rule := "This firm requires an independent reviewer"
	if !always {
		rule += " for amounts of at least " + minimum
	}
	return rule + ". You prepared this correction, so another member must approve it.", nil
}

func registerIndependence(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/organization/policy", func(w http.ResponseWriter, r *http.Request) {
		var required, passcode, written, firmWritten, investigations, ledgerWrites bool
		var minimum, materiality, owner string
		var after, most int
		if err := db.QueryRowContext(r.Context(), `SELECT require_independent_review,require_link_passcode,COALESCE(independent_review_min_amount::text,''),COALESCE(materiality_amount::text,''),reminder_after_days,reminder_max,assistant_model_answers,assistant_firm_answers,assistant_investigations,support_owner,allow_ledger_writes FROM organizations WHERE id=$1`, organization(r)).Scan(&required, &passcode, &minimum, &materiality, &after, &most, &written, &firmWritten, &investigations, &owner, &ledgerWrites); err != nil {
			problem(w, 503, "Firm policy is unavailable.")
			return
		}
		local := organization(r) == "local-development"
		manage := slices.Contains(actorPermissions(r), "org:manage")
		jsonReply(w, 200, map[string]any{"independent_review": required, "independent_review_min_amount": minimum, "materiality_amount": materiality, "link_passcode": passcode, "reminder_after_days": after, "reminder_max": most, "model_provider": llm.ProviderLabel(r.Context(), "written_answer"), "model_answers": written, "firm_answers": firmWritten, "investigations": investigations, "support_owner": owner, "ledger_writes": ledgerWrites, "hosted": !local, "can_manage": !local && manage, "can_manage_links": local || manage, "can_manage_model_answers": local || manage, "can_manage_ledger_writes": local || manage})
	})
	// Each change names only the settings it changes and records the resulting state of both.
	mux.HandleFunc("POST /v1/closes/organization/policy", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			IndependentReview *bool   `json:"independent_review"`
			MinAmount         *string `json:"independent_review_min_amount"`
			Materiality       *string `json:"materiality_amount"`
			LinkPasscode      *bool   `json:"link_passcode"`
			ReminderAfterDays *int    `json:"reminder_after_days"`
			ReminderMax       *int    `json:"reminder_max"`
			ModelAnswers      *bool   `json:"model_answers"`
			FirmAnswers       *bool   `json:"firm_answers"`
			Investigations    *bool   `json:"investigations"`
			LedgerWrites      *bool   `json:"ledger_writes"`
			Reason            string  `json:"reason"`
		}
		if !decode(w, r, &input) {
			return
		}
		input.Reason = strings.TrimSpace(input.Reason)
		local := organization(r) == "local-development"
		if input.IndependentReview == nil && input.MinAmount == nil && input.LinkPasscode == nil && input.Materiality == nil && input.ReminderAfterDays == nil && input.ReminderMax == nil && input.ModelAnswers == nil && input.FirmAnswers == nil && input.Investigations == nil && input.LedgerWrites == nil {
			problem(w, 422, "Choose the firm policy to change.")
			return
		}
		if (input.ReminderAfterDays != nil && (*input.ReminderAfterDays < 0 || *input.ReminderAfterDays > 6)) || (input.ReminderMax != nil && (*input.ReminderMax < 1 || *input.ReminderMax > 5)) {
			problem(w, 422, "A reminder can wait 0–6 days after delivery (the link lasts seven), and a request can have 1–5 reminders.")
			return
		}
		// An empty minimum clears it; a set one is a positive amount in the draft's own currency.
		minimum := ""
		if input.MinAmount != nil && strings.TrimSpace(*input.MinAmount) != "" {
			cents, err := prepaids.Amount(strings.TrimSpace(*input.MinAmount))
			if err != nil || cents <= 0 {
				problem(w, 422, "Enter the minimum as a positive amount such as 1000.00.")
				return
			}
			minimum = prepaids.Money(cents)
		}
		materiality := ""
		if input.Materiality != nil && strings.TrimSpace(*input.Materiality) != "" {
			cents, err := prepaids.Amount(strings.TrimSpace(*input.Materiality))
			if err != nil || cents <= 0 {
				problem(w, 422, "Enter materiality as a positive amount such as 500.00.")
				return
			}
			materiality = prepaids.Money(cents)
		}
		if local && (input.IndependentReview != nil || input.MinAmount != nil) {
			problem(w, 409, "Local development shares one identity, so independent review can only be enforced for hosted firms.")
			return
		}
		if !local && !slices.Contains(actorPermissions(r), "org:manage") {
			problem(w, 403, "Changing firm policy requires org:manage.")
			return
		}
		if len(input.Reason) < 5 || len(input.Reason) > 500 {
			problem(w, 422, "Explain the policy change in 5–500 characters.")
			return
		}
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			problem(w, 503, "Firm policy is unavailable.")
			return
		}
		defer tx.Rollback()
		var review, passcode, written, firmWritten, investigations, ledgerWrites bool
		var current, currentMateriality string
		var after, most int
		if err = tx.QueryRowContext(r.Context(), `UPDATE organizations SET require_independent_review=COALESCE($2,require_independent_review),require_link_passcode=COALESCE($3,require_link_passcode),independent_review_min_amount=CASE WHEN $4 THEN NULLIF($5,'')::numeric ELSE independent_review_min_amount END,materiality_amount=CASE WHEN $6 THEN NULLIF($7,'')::numeric ELSE materiality_amount END,reminder_after_days=COALESCE($8,reminder_after_days),reminder_max=COALESCE($9,reminder_max),assistant_model_answers=COALESCE($10,assistant_model_answers),assistant_firm_answers=COALESCE($11,assistant_firm_answers),assistant_investigations=CASE WHEN NOT COALESCE($10,assistant_model_answers) THEN false ELSE COALESCE($12,assistant_investigations) END,allow_ledger_writes=COALESCE($13,allow_ledger_writes) WHERE id=$1 RETURNING require_independent_review,require_link_passcode,COALESCE(independent_review_min_amount::text,''),COALESCE(materiality_amount::text,''),reminder_after_days,reminder_max,assistant_model_answers,assistant_firm_answers,assistant_investigations,allow_ledger_writes`, organization(r), input.IndependentReview, input.LinkPasscode, input.MinAmount != nil, minimum, input.Materiality != nil, materiality, input.ReminderAfterDays, input.ReminderMax, input.ModelAnswers, input.FirmAnswers, input.Investigations, input.LedgerWrites).Scan(&review, &passcode, &current, &currentMateriality, &after, &most, &written, &firmWritten, &investigations, &ledgerWrites); err == nil {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO organization_policy_changes(organization_id,independent_review,link_passcode,independent_review_min_amount,materiality_amount,reminder_after_days,reminder_max,assistant_model_answers,assistant_firm_answers,reason,actor,assistant_investigations,ledger_writes) VALUES($1,$2,$3,NULLIF($4,'')::numeric,NULLIF($5,'')::numeric,$6,$7,$8,$9,$10,$11,$12,$13)`, organization(r), review, passcode, current, currentMateriality, after, most, written, firmWritten, input.Reason, subject(r), investigations, ledgerWrites)
		}
		if err != nil || tx.Commit() != nil {
			problem(w, 503, "Firm policy change could not be confirmed.")
			return
		}
		jsonReply(w, 200, map[string]any{"independent_review": review, "independent_review_min_amount": current, "materiality_amount": currentMateriality, "link_passcode": passcode, "reminder_after_days": after, "reminder_max": most, "model_provider": llm.ProviderLabel(r.Context(), "written_answer"), "model_answers": written, "firm_answers": firmWritten, "investigations": investigations, "ledger_writes": ledgerWrites})
	})
}
