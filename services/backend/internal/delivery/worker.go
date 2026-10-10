package delivery

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
)

// One durable attempt, one immutable payload/key. A lost response never becomes
// an unqualified failure, and the 24-hour provider deduplication window is bounded.
func ProcessOne(ctx context.Context, db *sql.DB) (bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var id, requestID, collectionID, closeID, org, provider string
	var encrypted, credential []byte
	var attempts int
	var valid bool
	var until time.Time
	err = tx.QueryRowContext(ctx, `SELECT m.id,COALESCE(m.request_id::text,''),COALESCE(m.collection_id::text,''),c.id::text,c.organization_id,m.payload,x.provider,x.credential,m.attempts,m.approved_until
 FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id LEFT JOIN work_requests q ON q.id=m.request_id LEFT JOIN request_drafts d ON d.id=q.draft_id
 LEFT JOIN collection_messages cm ON cm.id=m.collection_id JOIN client_closes c ON c.id=COALESCE(d.close_id,cm.close_id)
 WHERE (m.state='queued' AND m.next_attempt_at<=now()) OR (m.state='sending' AND m.claimed_at<now()-interval '2 minutes') ORDER BY m.created_at FOR UPDATE OF m SKIP LOCKED LIMIT 1`).Scan(&id, &requestID, &collectionID, &closeID, &org, &encrypted, &provider, &credential, &attempts, &until)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var locked string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM client_closes WHERE id=$1 FOR UPDATE`, closeID).Scan(&locked); err != nil {
		return false, err
	}
	var freshErr error
	if collectionID != "" {
		// A collection message goes out only while the client's authority for this sender stands, the address is not
		// marked do not contact and has never bounced, complained or been suppressed for this client, no newer capture of
		// the books replaced the one it was drafted from, and the approval still holds.
		if err = tx.QueryRowContext(ctx, `SELECT o.enabled AND x.enabled AND m.approved_until>now() AND (m.first_attempt_at IS NULL OR m.first_attempt_at>now()-interval '23 hours') AND m.attempts<3
 AND (SELECT a.granted AND a.sender=x.sender FROM customer_contact_authority a WHERE a.organization_id=c.organization_id AND lower(a.client_entity)=lower(c.entity) ORDER BY a.id DESC LIMIT 1) IS TRUE
 AND NOT COALESCE((SELECT p.do_not_contact FROM collection_contact_preferences p WHERE p.organization_id=c.organization_id AND lower(p.client_entity)=lower(c.entity) AND lower(p.recipient)=lower(cm.recipient) ORDER BY p.id DESC LIMIT 1),false)
 AND NOT EXISTS(SELECT 1 FROM collection_bounces b WHERE b.organization_id=c.organization_id AND b.client_entity=lower(c.entity) AND b.recipient=lower(cm.recipient))
 AND NOT EXISTS(SELECT 1 FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.state IN ('completed','unchanged') AND (cm.company NOT LIKE 'xero:%' AND s.id>cm.capture_id::bigint OR cm.company LIKE 'xero:%' AND s.finished_at>cm.created_at))
 AND NOT EXISTS(SELECT 1 FROM xero_snapshots s WHERE s.close_id=c.id AND (cm.company LIKE 'xero:%' AND s.id>cm.capture_id::bigint OR cm.company NOT LIKE 'xero:%' AND s.captured_at>cm.created_at))
 AND (m.firm_authority OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=m.approved_by AND lower(g.client_entity)=lower(c.entity) AND g.granted))
 FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id JOIN collection_messages cm ON cm.id=m.collection_id JOIN client_closes c ON c.id=cm.close_id JOIN organizations o ON o.id=c.organization_id WHERE m.id=$1`, id).Scan(&valid); err != nil {
			return false, err
		}
	} else {
		if err = tx.QueryRowContext(ctx, `SELECT o.enabled AND x.enabled AND m.approved_until>now() AND (m.first_attempt_at IS NULL OR m.first_attempt_at>now()-interval '23 hours') AND m.attempts<3 AND l.revoked_at IS NULL AND l.used_at IS NULL AND l.expires_at>now() AND (q.coverage_scope IS NOT NULL OR (j.state='waiting_client' AND q.id=(SELECT max(id) FROM work_requests WHERE job_id=j.id))) AND NOT EXISTS(SELECT 1 FROM work_replies WHERE request_id=q.id) AND (m.reminder_of IS NULL OR (EXISTS(SELECT 1 FROM email_delivery prior WHERE prior.id=m.reminder_of AND prior.state='accepted' AND prior.delivery='delivered') AND NOT EXISTS(SELECT 1 FROM email_delivery bad WHERE bad.request_id=q.id AND bad.delivery IN ('bounced','complained','failed','suppressed')))) AND (m.firm_authority OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=m.approved_by AND lower(g.client_entity)=lower(c.entity) AND g.granted)) FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id LEFT JOIN preparation_jobs j ON j.id=q.job_id JOIN organizations o ON o.id=c.organization_id JOIN client_request_links l ON l.digest=m.link_digest WHERE m.id=$1`, id).Scan(&valid); err != nil {
			return false, err
		}
		freshErr = preparation.FreshClientRequest(ctx, tx, closeID, requestID)
	}
	if !valid || freshErr != nil {
		state := "cancelled"
		if attempts > 0 {
			state = "unknown"
		}
		_, err = tx.ExecContext(ctx, `UPDATE email_outbox SET state=$2,error='Approval, access, authority, request, recipient, delivery, link or sender changed. Inspect history; do not blindly retry.',finished_at=now(),claim=NULL WHERE id=$1`, id, state)
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	// The sender is Krith's own or the integration the firm connected; either sends the same approved email.
	sender, err := Open(org, provider, credential)
	var email Email
	if err == nil {
		var payload []byte
		if payload, err = secrets.Open(org+"/email/"+id, encrypted); err == nil && json.Unmarshal(payload, &email) != nil {
			err = fmt.Errorf("Invalid encrypted approved email")
		}
	}
	if err != nil {
		if _, e := tx.ExecContext(ctx, `UPDATE email_outbox SET state='queued',claim=NULL,error=$2,next_attempt_at=now()+interval '1 minute' WHERE id=$1`, id, err.Error()); e != nil {
			return false, e
		}
		return true, tx.Commit()
	}
	claim := rand.Text()
	_, err = tx.ExecContext(ctx, `UPDATE email_outbox SET state='sending',attempts=attempts+1,claim=$2,claimed_at=now(),first_attempt_at=COALESCE(first_attempt_at,now()) WHERE id=$1`, id, claim)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	var providerID string
	status := 0
	sendErr := sender.Ready(ctx)
	called := false
	if sendErr == nil && time.Now().Before(until) {
		called = true
		providerID, status, sendErr = sender.Send(ctx, email, id)
	} else if sendErr == nil {
		sendErr = fmt.Errorf("Email approval expired before sending")
	}
	state, message := "accepted", ""
	if sendErr != nil {
		message = sendErr.Error()
		state = "failed"
		uncertain := called && (status == 0 || status >= 500 || status == 408 || status == 409 || status == 429 || (status >= 200 && status < 300))
		if uncertain {
			state = "queued"
		}
		if attempts > 0 || uncertain {
			if !uncertain || attempts >= 2 {
				state = "unknown"
			}
		}
	}
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err = db.ExecContext(persist, `UPDATE email_outbox SET state=$3,provider_id=NULLIF($4,''),error=$5,claim=NULL,next_attempt_at=now()+interval '30 seconds',finished_at=CASE WHEN $3='queued' THEN NULL ELSE now() END WHERE id=$1 AND claim=$2 AND state='sending'`, id, claim, state, providerID, message)
	return true, err
}
func Run(ctx context.Context, db *sql.DB) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := ProcessOne(ctx, db); err != nil {
				log.Printf("Email processing unavailable: %v", err)
			}
		}
	}
}
