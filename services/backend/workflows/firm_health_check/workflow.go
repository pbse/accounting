// Package firm_health_check counts, once a day for each firm, the background work that failed or
// is stuck, provider writes awaiting reconciliation and connections to reconnect, so the admin
// overview shows where a firm needs help. It only reads.
package firm_health_check

import (
	"context"
	"errors"

	"close.local/backend/internal/wf"
)

type Input struct{}

// Report is what one check found for its firm.
type Report struct {
	FailedPreparation  int `json:"failed_preparation"`   // preparation jobs failed in the last 7 days
	StuckPreparation   int `json:"stuck_preparation"`    // queued or interpreting for more than an hour
	EmailProblems      int `json:"email_problems"`       // emails failed or with an unknown outcome, last 7 days
	FailedLedgerSyncs  int `json:"failed_ledger_syncs"`  // QuickBooks captures failed, last 7 days
	FailedForwarded    int `json:"failed_forwarded"`     // forwarded files failed, last 7 days
	ReconnectRequired  int `json:"reconnect_required"`   // QuickBooks and Xero connections to reconnect
	FailedWorkflowRuns int `json:"failed_workflow_runs"` // workflow runs failed in the last 7 days
	UnknownEffects     int `json:"unknown_effects"`      // workflow provider writes awaiting reconciliation
}

var _ = wf.Define(wf.Spec[Input, Report]{
	Name:        "firm_health_check",
	Title:       "Firm health check",
	Description: "Counts the firm's failed or stuck background work, provider writes awaiting reconciliation and connections to reconnect. Reads only.",
	Triggers:    []wf.Trigger{wf.ManualForFirm(), wf.ScheduleEachFirm("15 5 * * *")},
	Concurrency: func(Input) string { return "firm_health_check" },
	Run: func(ctx wf.Context, _ Input) (Report, error) {
		return wf.Step(ctx, "count", count)
	},
})

// count reads as the run's firm; the explicit firm filter is kept as well, so the counts stay the
// firm's own even where row-level security does not apply (a superuser in local development).
func count(ctx context.Context) (Report, error) {
	firm := wf.Organization(ctx)
	if firm == "" {
		return Report{}, wf.Permanent(errors.New("the health check runs for one firm"))
	}
	var r Report
	err := wf.DB(ctx).QueryRowContext(ctx, `SELECT
 (SELECT count(*) FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.organization_id=$1 AND j.state='failed' AND j.state_changed_at>now()-interval '7 days'),
 (SELECT count(*) FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.organization_id=$1 AND j.state IN ('queued','interpreting') AND j.state_changed_at<now()-interval '1 hour'),
 (SELECT count(*) FROM email_outbox m JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id
   WHERE c.organization_id=$1 AND m.state IN ('failed','unknown') AND m.created_at>now()-interval '7 days'),
 (SELECT count(*) FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id WHERE c.organization_id=$1 AND s.state='failed' AND s.created_at>now()-interval '7 days'),
 (SELECT count(*) FROM forwarded_files f JOIN client_closes c ON c.id=f.close_id WHERE c.organization_id=$1 AND f.state='failed' AND f.received_at>now()-interval '7 days'),
 (SELECT count(*) FROM quickbooks_connections WHERE organization_id=$1 AND state='reconnect_required')+(SELECT count(*) FROM xero_connections WHERE organization_id=$1 AND state='reconnect_required'),
 (SELECT count(*) FROM workflow_runs WHERE organization_id=$1 AND state='failed' AND finished_at>now()-interval '7 days'),
 (SELECT count(*) FROM workflow_steps WHERE organization_id=$1 AND state='unknown')`, firm).Scan(
		&r.FailedPreparation, &r.StuckPreparation, &r.EmailProblems, &r.FailedLedgerSyncs, &r.FailedForwarded, &r.ReconnectRequired, &r.FailedWorkflowRuns, &r.UnknownEffects)
	return r, err
}
