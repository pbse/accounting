package transport

import (
	"database/sql"
	"net/http"
	"slices"

	"close.local/backend/internal/auth"
)

const operationsAlertQuery = `SELECT kind,close_id,item_id,state,age_minutes FROM (
 SELECT 'preparation' AS kind,j.close_id::text AS close_id,j.id::text AS item_id,j.state, GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(i.agent_started_at,j.created_at))/60)::int) AS age_minutes
 FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id LEFT JOIN preparation_intakes i ON i.job_id=j.id
 WHERE c.organization_id=$1 AND (j.state='failed' OR (j.state='interpreting' AND COALESCE(i.agent_started_at,j.created_at)<now()-interval '2 minutes') OR (j.state='queued' AND j.created_at<now()-interval '10 minutes') OR (j.state='waiting_evidence' AND j.created_at<now()-interval '1 hour'))
 UNION ALL SELECT 'email',d.close_id::text,m.id,m.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(m.claimed_at,m.created_at))/60)::int)
 FROM email_outbox m JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id
 WHERE c.organization_id=$1 AND (m.state='unknown' OR (m.state='sending' AND m.claimed_at<now()-interval '2 minutes'))
 UNION ALL SELECT 'sandbox_posting',p.close_id::text,p.id::text,p.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(p.attempted_at,now()))/60)::int)
 FROM sandbox_postings p JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state IN ('unknown','executing')
 UNION ALL SELECT 'vendor_credit',p.close_id::text,p.id::text,p.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(p.attempted_at,now()))/60)::int)
 FROM vendor_credit_postings p JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state IN ('unknown','executing')
 UNION ALL SELECT 'bill_correction',p.close_id::text,p.id::text,p.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(p.attempted_at,now()))/60)::int)
 FROM bill_corrections p JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state IN ('unknown','executing')
 UNION ALL SELECT 'production_posting',p.close_id::text,p.id::text,p.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(p.attempted_at,now()))/60)::int)
 FROM production_postings p JOIN client_closes c ON c.id=p.close_id WHERE c.organization_id=$1 AND p.state IN ('unknown','executing')
 UNION ALL SELECT 'client_attachment',a.close_id::text,a.id::text,a.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(a.started_at,a.created_at))/60)::int)
 FROM client_attachments a JOIN client_closes c ON c.id=a.close_id WHERE c.organization_id=$1 AND (a.state IN ('failed','infected') OR (a.state='scanning' AND a.started_at<now()-interval '5 minutes'))
 UNION ALL SELECT 'quickbooks_sync',s.close_id::text,s.id::text,s.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(s.claimed_at,s.created_at))/60)::int)
 FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id WHERE c.organization_id=$1 AND ((s.state='failed' AND NOT EXISTS(SELECT 1 FROM quickbooks_entity_syncs n WHERE n.close_id=s.close_id AND n.connection_id=s.connection_id AND n.period=s.period AND n.id>s.id)) OR (s.state='running' AND s.claimed_at<now()-interval '8 minutes') OR (s.state='queued' AND s.created_at<now()-interval '10 minutes'))
 UNION ALL SELECT 'forwarded_scan',f.close_id::text,f.id::text,f.state,GREATEST(0,(EXTRACT(EPOCH FROM now()-COALESCE(f.claimed_at,f.received_at))/60)::int)
 FROM forwarded_files f JOIN client_closes c ON c.id=f.close_id WHERE c.organization_id=$1 AND (f.state='failed' OR (f.state='scanning' AND f.claimed_at<now()-interval '8 minutes') OR (f.state='queued' AND f.received_at<now()-interval '10 minutes'))
 ) alerts ORDER BY age_minutes DESC,kind,item_id`

func registerOperations(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("GET /v1/closes/operations", func(w http.ResponseWriter, r *http.Request) {
		a := auth.Actor(r.Context())
		if !firmAccess(r) || a == nil || (a.Organization != "" && !slices.Contains(a.Permissions, "operations:read")) {
			problem(w, 403, "Firm operations require clients:all and operations:read.")
			return
		}
		tx, err := db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
		if err != nil {
			problem(w, 503, "Operations snapshot unavailable.")
			return
		}
		defer tx.Rollback()
		var maxCloses, maxJobs, closes, jobs, unmapped, undelivered int
		err = tx.QueryRowContext(r.Context(), `SELECT o.max_closes,o.max_pending_jobs,
 (SELECT count(*) FROM client_closes c WHERE c.organization_id=o.id),
 (SELECT count(*) FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.organization_id=o.id AND j.state IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client')),
 (SELECT count(*) FROM email_delivery_events e JOIN email_connections x ON x.id=e.connection_id WHERE x.organization_id=o.id AND e.close_id IS NULL)
 FROM organizations o WHERE o.id=$1`, organization(r)).Scan(&maxCloses, &maxJobs, &closes, &jobs, &unmapped)
		if err != nil {
			problem(w, 503, "Firm limits unavailable.")
			return
		}
		if err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM operations_alert_deliveries WHERE organization_id=$1 AND resolved_at IS NULL AND (status='unknown' OR (status='sending' AND claimed_at<now()-interval '2 minutes'))`, organization(r)).Scan(&undelivered); err != nil {
			problem(w, 503, "Alert delivery state unavailable.")
			return
		}
		rows, err := tx.QueryContext(r.Context(), operationsAlertQuery+" LIMIT 101", organization(r))
		if err != nil {
			problem(w, 503, "Operational alerts unavailable.")
			return
		}
		alerts := []map[string]any{}
		for rows.Next() {
			var kind, closeID, itemID, state string
			var age int
			if rows.Scan(&kind, &closeID, &itemID, &state, &age) != nil {
				rows.Close()
				problem(w, 503, "Operational alerts unavailable.")
				return
			}
			alerts = append(alerts, map[string]any{"kind": kind, "close_id": closeID, "item_id": itemID, "state": state, "age_minutes": age})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			problem(w, 503, "Operational alerts unavailable.")
			return
		}
		more := len(alerts) > 100
		if more {
			alerts = alerts[:100]
		}
		jsonReply(w, 200, map[string]any{"limits": map[string]int{"closes": maxCloses, "active_jobs": maxJobs}, "usage": map[string]int{"closes": closes, "active_jobs": jobs}, "unmapped_delivery_events": unmapped, "undelivered_alerts": undelivered, "alerts": alerts, "has_more": more})
	})
}
