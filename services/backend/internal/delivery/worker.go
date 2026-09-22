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
	var id, requestID, closeID, org string
	var encrypted, credential []byte
	var attempts int
	var valid bool
	var until time.Time
	err = tx.QueryRowContext(ctx, `SELECT m.id,m.request_id::text,d.close_id::text,c.organization_id,m.payload,x.credential,m.attempts,m.approved_until
 FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id
 WHERE (m.state='queued' AND m.next_attempt_at<=now()) OR (m.state='sending' AND m.claimed_at<now()-interval '2 minutes') ORDER BY m.created_at FOR UPDATE OF m SKIP LOCKED LIMIT 1`).Scan(&id, &requestID, &closeID, &org, &encrypted, &credential, &attempts, &until)
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
	if err = tx.QueryRowContext(ctx, `SELECT o.enabled AND x.enabled AND m.approved_until>now() AND (m.first_attempt_at IS NULL OR m.first_attempt_at>now()-interval '23 hours') AND m.attempts<3 AND l.revoked_at IS NULL AND l.used_at IS NULL AND l.expires_at>now() AND (q.coverage_scope IS NOT NULL OR (j.state='waiting_client' AND q.id=(SELECT max(id) FROM work_requests WHERE job_id=j.id))) AND NOT EXISTS(SELECT 1 FROM work_replies WHERE request_id=q.id) AND (m.reminder_of IS NULL OR (EXISTS(SELECT 1 FROM email_delivery prior WHERE prior.id=m.reminder_of AND prior.state='accepted' AND prior.delivery='delivered') AND NOT EXISTS(SELECT 1 FROM email_delivery bad WHERE bad.request_id=q.id AND bad.delivery IN ('bounced','complained','failed','suppressed')))) AND (m.firm_authority OR EXISTS(SELECT 1 FROM current_client_access g WHERE g.organization_id=c.organization_id AND g.subject=m.approved_by AND lower(g.client_entity)=lower(c.entity) AND g.granted)) FROM email_outbox m JOIN email_connections x ON x.id=m.connection_id JOIN work_requests q ON q.id=m.request_id JOIN request_drafts d ON d.id=q.draft_id JOIN client_closes c ON c.id=d.close_id LEFT JOIN preparation_jobs j ON j.id=q.job_id JOIN organizations o ON o.id=c.organization_id JOIN client_request_links l ON l.digest=m.link_digest WHERE m.id=$1`, id).Scan(&valid); err != nil {
		return false, err
	}
	freshErr := preparation.FreshClientRequest(ctx, tx, closeID, requestID)
	if !valid || freshErr != nil {
		state := "cancelled"
		if attempts > 0 {
			state = "unknown"
		}
		_, err = tx.ExecContext(ctx, `UPDATE email_outbox SET state=$2,error='Approval, access, request, delivery, link or sender changed. Inspect history; do not blindly retry.',finished_at=now(),claim=NULL WHERE id=$1`, id, state)
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	plaintext, err := secrets.Open(org+"/resend", credential)
	var connection Connection
	var payload []byte
	if err == nil && json.Unmarshal(plaintext, &connection) != nil {
		err = fmt.Errorf("Invalid encrypted sender configuration")
	}
	if err == nil {
		payload, err = secrets.Open(org+"/email/"+id, encrypted)
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
	sendErr := VerifySender(ctx, connection)
	called := false
	if sendErr == nil && time.Now().Before(until) {
		called = true
		providerID, status, sendErr = Send(ctx, connection.Key, payload, id)
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
