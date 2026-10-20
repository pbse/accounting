package preparation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// RecordPayrollSupport finishes only the source-selection step of a ledger-only intake. The payroll remains its own work.
// The caller holds the close lock. Existing human answers, requests, confirmations and handoffs remain decisions.
func RecordPayrollSupport(ctx context.Context, tx *sql.Tx, closeID, actor string) error {
	rows, err := tx.QueryContext(ctx, `SELECT j.id::text,u.payroll_job_id::text,u.summary_source_id::text,u.summary_digest,u.ledger_digest
 FROM payroll_ledger_readings u JOIN preparation_jobs j ON j.id=u.ledger_job_id JOIN preparation_intakes i ON i.job_id=j.id
 WHERE j.close_id::text=$1 AND j.state='needs_confirmation' AND jsonb_array_length(j.plans)=0 AND jsonb_array_length(j.workpaper_ids)=0
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(i.proposal->'sources') s WHERE COALESCE(s->>'id','') NOT IN (u.ledger_source_id::text,u.summary_source_id::text)
  AND NOT EXISTS(SELECT 1 FROM evidence policy WHERE policy.id::text=s->>'id' AND policy.close_id=j.close_id AND policy.procedure_id IS NOT NULL AND policy.superseded_by IS NULL AND policy.digest=s->>'digest' AND s->>'role'='support'))
 AND i.confirmed_by IS NULL AND i.confirmed_at IS NULL AND i.agent_claim IS NULL
 AND NOT EXISTS(SELECT 1 FROM preparation_answers a WHERE a.job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM request_jobs r WHERE r.job_id=j.id)
 AND NOT EXISTS(SELECT 1 FROM goal_handoffs h WHERE h.job_id=j.id AND h.closed_at IS NULL)
 AND NOT EXISTS(SELECT 1 FROM payroll_ledger_readings other WHERE other.ledger_job_id=j.id AND other.payroll_job_id<>u.payroll_job_id)
 FOR UPDATE OF j,i SKIP LOCKED`, closeID)
	if err != nil {
		return err
	}
	type use struct{ job, payroll, summary, summaryDigest, ledgerDigest string }
	var uses []use
	for rows.Next() {
		var u use
		if err = rows.Scan(&u.job, &u.payroll, &u.summary, &u.summaryDigest, &u.ledgerDigest); err != nil {
			rows.Close()
			return err
		}
		uses = append(uses, u)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, u := range uses {
		source, _ := json.Marshal(map[string]string{"job_id": u.payroll, "source_id": u.summary, "summary_digest": u.summaryDigest, "ledger_digest": u.ledgerDigest, "actor": actor})
		note := fmt.Sprintf("Used as ledger support for payroll work #%s; no separate preparation is needed.", u.payroll)
		if _, err = tx.ExecContext(ctx, `UPDATE preparation_intakes SET revision=revision+1,agent_retry_at=NULL,agent_checkpoint=agent_checkpoint||jsonb_build_object('stage','supporting','support_for',$2::jsonb,'straight_through',$3::text) WHERE job_id::text=$1`, u.job, string(source), note); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs SET state='supporting',error='',finished_at=now() WHERE id::text=$1`, u.job); err != nil {
			return err
		}
	}
	return nil
}
