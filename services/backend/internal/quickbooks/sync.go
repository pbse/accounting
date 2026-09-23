package quickbooks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"close.local/backend/internal/secrets"
)

type syncClaim struct {
	id, closeID, connectionID, period, realm, token string
	attempts                                        int
}

func scheduleSync(ctx context.Context, db *sql.DB) error {
	if !OAuthConfigured() {
		return nil
	}
	_, err := db.ExecContext(ctx, `WITH due AS (
 SELECT c.id AS close_id,x.id AS connection_id,c.period FROM client_closes c
 JOIN organizations o ON o.id=c.organization_id AND o.enabled
 JOIN quickbooks_connections x ON x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity)
 WHERE x.environment='sandbox' AND x.state='active' AND x.credential IS NOT NULL
 AND c.period BETWEEN to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND to_char(now() AT TIME ZONE 'UTC','YYYY-MM')
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 AND NOT EXISTS (SELECT 1 FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.connection_id=x.id AND s.period=c.period AND s.state IN ('queued','running'))
 AND NOT EXISTS (SELECT 1 FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.connection_id=x.id AND s.period=c.period AND s.created_at>now()-interval '24 hours')
 ORDER BY c.id FOR UPDATE OF c SKIP LOCKED LIMIT 10
 ) INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by)
 SELECT close_id,connection_id,period,'scheduled-quickbooks-refresh' FROM due
 ON CONFLICT(close_id,connection_id,period) WHERE state IN ('queued','running') DO NOTHING`)
	return err
}

func claimSync(ctx context.Context, db *sql.DB) (syncClaim, error) {
	var job syncClaim
	job.token = rand.Text()
	if _, err := db.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='failed',claim=NULL,finished_at=now(),error='Transaction capture exhausted retries after a worker interruption' WHERE state='running' AND claimed_at<now()-interval '8 minutes' AND attempts>=3`); err != nil {
		return job, err
	}
	err := db.QueryRowContext(ctx, `UPDATE quickbooks_entity_syncs s SET state='running',claim=$1,claimed_at=now(),started_at=COALESCE(started_at,now()),attempts=attempts+1,error=''
 WHERE s.id=(SELECT id FROM quickbooks_entity_syncs WHERE attempts<3 AND ((state='queued' AND next_attempt_at<=now()) OR (state='running' AND claimed_at<now()-interval '8 minutes')) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
 RETURNING s.id::text,s.close_id::text,s.connection_id::text,s.period,s.attempts`, job.token).Scan(&job.id, &job.closeID, &job.connectionID, &job.period, &job.attempts)
	return job, err
}

func syncCredentials(ctx context.Context, db *sql.DB, job syncClaim) (string, string, error) {
	var organization, realm, environment, state string
	var encrypted []byte
	var expires sql.NullTime
	err := db.QueryRowContext(ctx, `SELECT x.organization_id,x.realm,x.environment,x.state,x.credential,x.access_expires_at
 FROM quickbooks_connections x JOIN client_closes c ON c.id=$1 AND c.organization_id=x.organization_id AND lower(c.entity)=lower(x.client_entity) JOIN organizations o ON o.id=x.organization_id AND o.enabled
 WHERE x.id=$2 AND c.period=$3`, job.closeID, job.connectionID, job.period).Scan(&organization, &realm, &environment, &state, &encrypted, &expires)
	if err != nil || state != "active" || environment != "sandbox" || len(encrypted) == 0 {
		return "", "", errors.New("Connected sandbox company or firm is no longer active")
	}
	aad := organization + "/qbo/" + job.connectionID
	raw, err := secrets.Open(aad, encrypted)
	var credentials Credentials
	if err != nil || json.Unmarshal(raw, &credentials) != nil || credentials.Access == "" || credentials.Refresh == "" {
		return "", "", errors.New("Encrypted QuickBooks credentials are unavailable")
	}
	if !expires.Valid || time.Until(expires.Time) < 2*time.Minute {
		result, err := db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='refreshing',updated_at=now() WHERE id=$1 AND state='active' AND access_expires_at IS NOT DISTINCT FROM $2`, job.connectionID, expires)
		if err != nil {
			return "", "", err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return "", "", errors.New("QuickBooks connection changed during refresh")
		}
		credentials, err = RefreshFor(ctx, environment, credentials.Refresh)
		if err != nil {
			_, _ = db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='reconnect_required',error='Token refresh did not complete; reconnect before syncing',updated_at=now() WHERE id=$1 AND state='refreshing'`, job.connectionID)
			return "", "", errors.New("QuickBooks token refresh did not complete; reconnect")
		}
		raw, _ = json.Marshal(credentials)
		encrypted, err = secrets.Seal(aad, raw)
		if err == nil {
			var saved sql.Result
			saved, err = db.ExecContext(ctx, `UPDATE quickbooks_connections SET credential=$2,access_expires_at=$3,state='active',updated_at=now() WHERE id=$1 AND state='refreshing'`, job.connectionID, encrypted, time.Now().Add(time.Duration(credentials.Expires)*time.Second))
			if err == nil {
				var n int64
				n, err = saved.RowsAffected()
				if err == nil && n != 1 {
					err = errors.New("QuickBooks connection changed during token refresh")
				}
			}
		}
		if err != nil {
			_, _ = db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='reconnect_required',error='Rotated token could not be persisted; reconnect',updated_at=now() WHERE id=$1 AND state='refreshing'`, job.connectionID)
			return "", "", errors.New("Rotated QuickBooks token could not be persisted; reconnect")
		}
	}
	return realm, credentials.Access, nil
}

func completeSync(ctx context.Context, db *sql.DB, job syncClaim, capture EntityCapture) error {
	raw, err := json.Marshal(capture)
	if err != nil {
		return err
	}
	if len(raw) > 12*1024*1024 {
		return errors.New("Transaction capture exceeds the 12 MiB per-close limit")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var canonical, stable string
	if err = tx.QueryRowContext(ctx, `SELECT $1::jsonb::text,($1::jsonb-'started_at'-'finished_at'-'completeness')::text`, string(raw)).Scan(&canonical, &stable); err != nil {
		return err
	}
	captureDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(stable)))
	indexFields := map[string]any{"source": "quickbooks_transactions", "period": job.period, "realm": job.realm, "counts": capture.Counts, "capture_digest": captureDigest}
	index, _ := json.Marshal(indexFields)
	if len(index) > 512*1024 {
		return errors.New("Transaction evidence index exceeds the supported size")
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT s.state='running' AND s.claim=$2 AND c.period=s.period AND x.state='active' AND x.environment='sandbox' AND x.realm=$3 AND o.enabled
 FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id JOIN quickbooks_connections x ON x.id=s.connection_id AND x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) JOIN organizations o ON o.id=c.organization_id WHERE s.id=$1 FOR UPDATE OF s,c,x`, job.id, job.token, job.realm).Scan(&valid)
	if err != nil || !valid {
		return errors.New("Connection, close, or job changed during transaction capture")
	}
	var unchangedSource string
	err = tx.QueryRowContext(ctx, `SELECT s.evidence_id::text FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL WHERE s.close_id=$1 AND s.connection_id=$2 AND s.period=$3 AND s.state='completed' AND s.digest=$4 AND (SELECT count(*) FROM quickbooks_transaction_rows t WHERE t.sync_id=s.id)=COALESCE((SELECT sum(value::int) FROM jsonb_each_text(COALESCE(s.payload->'counts','{}'::jsonb))),0) ORDER BY s.id DESC LIMIT 1`, job.closeID, job.connectionID, job.period, captureDigest).Scan(&unchangedSource)
	if err == nil {
		summary, _ := json.Marshal(map[string]any{"counts": capture.Counts, "started_at": capture.StartedAt, "finished_at": capture.FinishedAt, "completeness": capture.Completeness})
		_, err = tx.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='unchanged',payload=$3,digest=$4,evidence_id=$5,finished_at=now(),claim=NULL,error='' WHERE id=$1 AND claim=$2 AND state='running'`, job.id, job.token, string(summary), captureDigest, unchangedSource)
		if err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != sql.ErrNoRows {
		return err
	}
	var sourceID string
	digest := fmt.Sprintf("%x", sha256.Sum256(index))
	var replaced bool
	err = tx.QueryRowContext(ctx, `SELECT id::text,superseded_by IS NOT NULL FROM evidence WHERE close_id=$1 AND digest=$2`, job.closeID, digest).Scan(&sourceID, &replaced)
	if err == nil && replaced {
		indexFields["recaptured_after_revision"] = job.id
		index, _ = json.Marshal(indexFields)
		digest = fmt.Sprintf("%x", sha256.Sum256(index))
		err = sql.ErrNoRows
	}
	if err == sql.ErrNoRows {
		var count, size int
		err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))),0) FROM evidence WHERE close_id=$1 AND created_by<>'quickbooks-sync'`, job.closeID).Scan(&count, &size)
		if err != nil || count >= 100 || size+len(index) > 20*1024*1024 {
			return errors.New("Close evidence capacity is full")
		}
		err = tx.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, job.closeID, "QuickBooks transactions "+job.period, digest, string(index), "quickbooks-sync").Scan(&sourceID)
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record)
 SELECT $1,k.kind,r.item->>'Id',r.item->>'SyncToken',(r.item->>'TxnDate')::date,(r.item->'MetaData'->>'LastUpdatedTime')::timestamptz,r.item->'CurrencyRef'->>'value',r.item->>'TotalAmt',r.item
 FROM jsonb_each($2::jsonb->'records') AS k(kind,items) CROSS JOIN LATERAL jsonb_array_elements(k.items) AS r(item)`, job.id, canonical)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM quickbooks_transaction_rows t USING quickbooks_entity_syncs s WHERE t.sync_id=s.id AND s.close_id=$1 AND s.id<>$2`, job.closeID, job.id); err != nil {
		return err
	}
	var uncertain bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quickbooks_entity_syncs old JOIN evidence e ON e.id=old.evidence_id JOIN workpapers w ON w.close_id=old.close_id JOIN sandbox_postings p ON p.workpaper_id=w.id WHERE old.close_id=$1 AND old.state='completed' AND e.id::text<>$2 AND e.superseded_by IS NULL AND p.state IN ('executing','unknown') AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',e.id::text))) OR EXISTS(SELECT 1 FROM quickbooks_entity_syncs old JOIN evidence e ON e.id=old.evidence_id JOIN vendor_credit_postings p ON p.close_id=old.close_id JOIN workpapers w ON w.id=p.workpaper_id WHERE old.close_id=$1 AND old.state='completed' AND e.id::text<>$2 AND e.superseded_by IS NULL AND p.state IN ('executing','unknown') AND (p.evidence_id=e.id OR w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',e.id::text))) )`, job.closeID, sourceID).Scan(&uncertain)
	if err != nil {
		return err
	}
	if uncertain {
		return errors.New("Resolve uncertain posting attempts before replacing transaction evidence")
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM bill_corrections b JOIN evidence e ON e.id=b.evidence_id WHERE b.close_id=$1 AND e.id::text<>$2 AND e.superseded_by IS NULL AND b.state IN ('executing','unknown'))`, job.closeID, sourceID).Scan(&uncertain)
	if err != nil {
		return err
	}
	if uncertain {
		return errors.New("Resolve the Bill correction attempt before replacing transaction evidence")
	}
	var productionPending bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM quickbooks_entity_syncs old JOIN evidence e ON e.id=old.evidence_id JOIN workpapers w ON w.close_id=old.close_id JOIN production_postings p ON p.workpaper_id=w.id WHERE old.close_id=$1 AND old.state='completed' AND e.id::text<>$2 AND e.superseded_by IS NULL AND (p.state IN ('executing','unknown') OR (p.state='approved' AND p.approval_expires_at>now())) AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',e.id::text)))`, job.closeID, sourceID).Scan(&productionPending)
	if err != nil {
		return err
	}
	if productionPending {
		return errors.New("Resolve the linked production posting approval or attempt before replacing transaction evidence")
	}
	_, err = tx.ExecContext(ctx, `UPDATE evidence e SET superseded_by=$2::bigint,replacement_reason='New QuickBooks transaction capture',replaced_by='quickbooks-sync',replaced_at=now() WHERE e.close_id=$1 AND e.id<>$2::bigint AND e.superseded_by IS NULL AND e.id IN (SELECT old.evidence_id FROM quickbooks_entity_syncs old WHERE old.close_id=$1 AND old.state='completed' AND old.evidence_id IS NOT NULL)`, job.closeID, sourceID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE sandbox_postings p SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM workpapers w WHERE w.id=p.workpaper_id AND w.close_id=$1 AND p.state='approved' AND EXISTS(SELECT 1 FROM evidence e WHERE e.close_id=$1 AND e.superseded_by::text=$2 AND e.replaced_by='quickbooks-sync' AND w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',e.id::text)))`, job.closeID, sourceID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE vendor_credit_postings p SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM workpapers w WHERE w.id=p.workpaper_id AND w.close_id=$1 AND p.state='approved' AND EXISTS(SELECT 1 FROM evidence e WHERE e.close_id=$1 AND e.superseded_by::text=$2 AND e.replaced_by='quickbooks-sync' AND (p.evidence_id=e.id OR w.paper->'sources' @> jsonb_build_array(jsonb_build_object('id',e.id::text))))`, job.closeID, sourceID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE bill_corrections b SET state='preview',approved_by=NULL,approval_expires_at=NULL FROM evidence e WHERE b.evidence_id=e.id AND b.close_id=$1 AND b.state='approved' AND e.superseded_by::text=$2 AND e.replaced_by='quickbooks-sync'`, job.closeID, sourceID)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='completed',payload=$3,digest=$4,evidence_id=$5,finished_at=now(),claim=NULL,error='' WHERE id=$1 AND claim=$2 AND state='running'`, job.id, job.token, canonical, captureDigest, sourceID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return errors.New("Transaction sync claim expired")
	}
	return tx.Commit()
}

func ProcessSync(ctx context.Context, db *sql.DB) (bool, error) {
	job, err := claimSync(ctx, db)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	work, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	realm, token, err := syncCredentials(work, db, job)
	job.realm = realm
	if err == nil {
		var capture EntityCapture
		capture, err = PeriodEntities(work, realm, token, job.period)
		if err == nil {
			err = completeSync(work, db, job, capture)
		}
	}
	if err != nil {
		state := "queued"
		if job.attempts >= 3 {
			state = "failed"
		}
		message := err.Error()
		if len(message) > 400 {
			message = message[:400]
		}
		_, saveErr := db.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state=$3,error=$4,claim=NULL,next_attempt_at=now()+interval '5 minutes',finished_at=CASE WHEN $3='failed' THEN now() ELSE NULL END WHERE id=$1 AND claim=$2 AND state='running'`, job.id, job.token, state, message)
		return true, saveErr
	}
	return true, nil
}

func RunSync(ctx context.Context, db *sql.DB) {
	nextSchedule := time.Time{}
	for ctx.Err() == nil {
		if !time.Now().Before(nextSchedule) {
			if err := scheduleSync(ctx, db); err != nil && ctx.Err() == nil {
				log.Print("Transaction refresh scheduler could not queue due closes")
			}
			nextSchedule = time.Now().Add(5 * time.Minute)
		}
		worked, err := ProcessSync(ctx, db)
		if err != nil && ctx.Err() == nil {
			log.Print("Transaction sync worker could not update durable state")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}
