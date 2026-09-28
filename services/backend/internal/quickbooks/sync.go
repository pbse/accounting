package quickbooks

import (
	"bytes"
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

// ReadingConnection is SQL for the connection a client's books are read from: its production connection
// unless disconnected, else its sandbox one. close is a SQL expression for the close's id.
func ReadingConnection(close string) string {
	return `(SELECT r.id FROM quickbooks_connections r JOIN client_closes rc ON rc.organization_id=r.organization_id AND lower(r.client_entity)=lower(rc.entity) WHERE rc.id=` + close + ` AND r.state<>'disconnected' ORDER BY r.environment='production' DESC,r.id DESC LIMIT 1)`
}

func scheduleSync(ctx context.Context, db *sql.DB) error {
	if !OAuthConfigured() && !OAuthConfiguredFor("production") {
		return nil
	}
	_, err := db.ExecContext(ctx, `WITH due AS (
 SELECT c.id AS close_id,x.id AS connection_id,c.period,x.changed_at>COALESCE(latest.started_at,'-infinity') AS hinted FROM client_closes c
 JOIN organizations o ON o.id=c.organization_id AND o.enabled
 JOIN quickbooks_connections x ON x.id=`+ReadingConnection("c.id")+`
 -- A change hint after the newest capture started makes the close due before its daily refresh.
 LEFT JOIN LATERAL (SELECT s.started_at FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.connection_id=x.id AND s.period=c.period ORDER BY s.id DESC LIMIT 1) latest ON true
 WHERE x.state='active' AND x.credential IS NOT NULL
 -- A close an agent works on is read whatever its month.
 AND (c.period BETWEEN to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND to_char(now() AT TIME ZONE 'UTC','YYYY-MM') OR EXISTS(SELECT 1 FROM close_goals g WHERE g.close_id=c.id AND g.state='active'))
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 AND NOT EXISTS (SELECT 1 FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.connection_id=x.id AND s.period=c.period AND s.state IN ('queued','running'))
 AND (NOT EXISTS (SELECT 1 FROM quickbooks_entity_syncs s WHERE s.close_id=c.id AND s.connection_id=x.id AND s.period=c.period AND s.created_at>now()-interval '24 hours') OR x.changed_at>COALESCE(latest.started_at,'-infinity'))
 ORDER BY c.id FOR UPDATE OF c SKIP LOCKED LIMIT 10
 ) INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by)
 SELECT close_id,connection_id,period,CASE WHEN hinted THEN 'quickbooks-change-hint' ELSE 'scheduled-quickbooks-refresh' END FROM due
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

func syncCredentials(ctx context.Context, db *sql.DB, job syncClaim) (string, string, string, error) {
	var organization, realm, environment, state string
	var encrypted []byte
	var expires sql.NullTime
	var reading bool
	err := db.QueryRowContext(ctx, `SELECT x.organization_id,x.realm,x.environment,x.state,x.credential,x.access_expires_at,x.id=`+ReadingConnection("c.id")+`
 FROM quickbooks_connections x JOIN client_closes c ON c.id=$1 AND c.organization_id=x.organization_id AND lower(c.entity)=lower(x.client_entity) JOIN organizations o ON o.id=x.organization_id AND o.enabled
 WHERE x.id=$2 AND c.period=$3`, job.closeID, job.connectionID, job.period).Scan(&organization, &realm, &environment, &state, &encrypted, &expires, &reading)
	if err != nil || state != "active" || len(encrypted) == 0 {
		return "", "", "", errors.New("Connected QuickBooks company or firm is no longer active")
	}
	if !reading {
		return "", "", "", errors.New("This client's books are now read from its production QuickBooks connection")
	}
	aad := organization + "/qbo/" + job.connectionID
	raw, err := secrets.Open(aad, encrypted)
	var credentials Credentials
	if err != nil || json.Unmarshal(raw, &credentials) != nil || credentials.Access == "" || credentials.Refresh == "" {
		return "", "", "", errors.New("Encrypted QuickBooks credentials are unavailable")
	}
	if !expires.Valid || time.Until(expires.Time) < 2*time.Minute {
		result, err := db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='refreshing',updated_at=now() WHERE id=$1 AND state='active' AND access_expires_at IS NOT DISTINCT FROM $2`, job.connectionID, expires)
		if err != nil {
			return "", "", "", err
		}
		n, _ := result.RowsAffected()
		if n != 1 {
			return "", "", "", errors.New("QuickBooks connection changed during refresh")
		}
		credentials, err = RefreshFor(ctx, environment, credentials.Refresh)
		if err != nil {
			_, _ = db.ExecContext(ctx, `UPDATE quickbooks_connections SET state='reconnect_required',error='Token refresh did not complete; reconnect before syncing',updated_at=now() WHERE id=$1 AND state='refreshing'`, job.connectionID)
			return "", "", "", errors.New("QuickBooks token refresh did not complete; reconnect")
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
			return "", "", "", errors.New("Rotated QuickBooks token could not be persisted; reconnect")
		}
	}
	return environment, realm, credentials.Access, nil
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
	if err = tx.QueryRowContext(ctx, `SELECT $1::jsonb::text,($1::jsonb-'started_at'-'finished_at'-'completeness'-'open_items')::text`, string(raw)).Scan(&canonical, &stable); err != nil {
		return err
	}
	captureDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(stable)))
	indexFields := map[string]any{"source": "quickbooks_transactions", "period": job.period, "realm": job.realm, "counts": capture.Counts, "capture_digest": captureDigest}
	index, _ := json.Marshal(indexFields)
	if len(index) > 512*1024 {
		return errors.New("Transaction evidence index exceeds the supported size")
	}
	var valid bool
	err = tx.QueryRowContext(ctx, `SELECT s.state='running' AND s.claim=$2 AND c.period=s.period AND x.state='active' AND x.id=`+ReadingConnection("c.id")+` AND x.realm=$3 AND o.enabled
 FROM quickbooks_entity_syncs s JOIN client_closes c ON c.id=s.close_id JOIN quickbooks_connections x ON x.id=s.connection_id AND x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) JOIN organizations o ON o.id=c.organization_id WHERE s.id=$1 FOR UPDATE OF s,c,x`, job.id, job.token, job.realm).Scan(&valid)
	if err != nil || !valid {
		return errors.New("Connection, close, or job changed during transaction capture")
	}
	var sourceSync, unchangedSource string
	err = tx.QueryRowContext(ctx, `SELECT s.id::text,s.evidence_id::text FROM quickbooks_entity_syncs s JOIN evidence e ON e.id=s.evidence_id AND e.close_id=s.close_id AND e.superseded_by IS NULL WHERE s.close_id=$1 AND s.connection_id=$2 AND s.period=$3 AND s.state='completed' AND s.digest=$4 AND s.exceptions IS NOT NULL AND (SELECT count(*) FROM quickbooks_transaction_rows t WHERE t.sync_id=s.id)=COALESCE((SELECT sum(value::int) FROM jsonb_each_text(COALESCE(s.payload->'counts','{}'::jsonb))),0) ORDER BY s.id DESC LIMIT 1`, job.closeID, job.connectionID, job.period, captureDigest).Scan(&sourceSync, &unchangedSource)
	if err == nil {
		summary, _ := json.Marshal(map[string]any{"counts": capture.Counts, "started_at": capture.StartedAt, "finished_at": capture.FinishedAt, "completeness": capture.Completeness, "open_items": capture.OpenItems})
		// Unchanged transactions reuse the earlier capture's rows; this capture's open items are new.
		found, err := captureExceptions(ctx, tx, sourceSync, capture.OpenItems, job.period)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='unchanged',payload=$3,digest=$4,evidence_id=$5,finished_at=now(),claim=NULL,error='',exceptions=$6 WHERE id=$1 AND claim=$2 AND state='running'`, job.id, job.token, string(summary), captureDigest, unchangedSource, found)
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
	found, err := captureExceptions(ctx, tx, job.id, capture.OpenItems, job.period)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='completed',payload=$3,digest=$4,evidence_id=$5,finished_at=now(),claim=NULL,error='',exceptions=$6 WHERE id=$1 AND claim=$2 AND state='running'`, job.id, job.token, canonical, captureDigest, sourceID, found)
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
	environment, realm, token, err := syncCredentials(work, db, job)
	job.realm = realm
	if err == nil {
		var capture EntityCapture
		capture, err = PeriodEntities(work, environment, realm, token, job.period)
		if err == nil {
			err = completeSync(work, db, job, capture)
		}
		if err == nil {
			reports, done := context.WithTimeout(ctx, 3*time.Minute)
			if e := captureFirstPeriod(reports, db, job, environment, realm, token); e != nil {
				log.Printf("Month-end snapshot for close %s was not captured; the next scheduled sync retries: %v", job.closeID, e)
				// Kept on the finished capture, where RefreshStopped reads it: until a read succeeds, sign-off needs a person's capture.
				message := "Month-end snapshot not refreshed: " + e.Error()
				if len(message) > 400 {
					message = message[:400]
				}
				if _, saveErr := db.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET error=$2 WHERE id=$1`, job.id, message); saveErr != nil && ctx.Err() == nil {
					log.Printf("Month-end snapshot failure for close %s could not be recorded: %v", job.closeID, saveErr)
				}
			}
			done()
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

// firstPeriod binds an ended, unsigned month to the connection its transactions came from while its period snapshot
// is the scheduler's own: none yet, or a scheduled one at least 20 hours old. The month has ended everywhere
// (UTC-12) before the first one, so a US afternoon on the last day is never taken for the month's end.
var firstPeriod = `SELECT c.organization_id,c.entity,x.company_name FROM client_closes c
 JOIN organizations o ON o.id=c.organization_id AND o.enabled
 JOIN quickbooks_connections x ON x.id=$2 AND x.organization_id=c.organization_id AND lower(x.client_entity)=lower(c.entity) AND x.state='active' AND x.id=` + ReadingConnection("c.id") + `
 WHERE c.id=$1 AND c.period=$3 AND c.period<to_char((now() AT TIME ZONE 'UTC')-interval '12 hours','YYYY-MM')
 AND COALESCE((SELECT decision FROM close_signoffs WHERE close_id=c.id ORDER BY id DESC LIMIT 1),'')<>'signed_off'
 AND NOT EXISTS (SELECT 1 FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.payload ? 'period' AND (s.actor<>'scheduled-quickbooks-refresh' OR s.captured_at>now()-interval '20 hours'))
 -- Six scheduled ones at most, and never the close's last free snapshot, so a person can always take the capture
 -- that RefreshStopped then asks for.
 AND (SELECT count(*) FROM quickbooks_snapshots s WHERE s.close_id=c.id)<19
 AND (SELECT count(*) FROM quickbooks_snapshots s WHERE s.close_id=c.id AND s.actor='scheduled-quickbooks-refresh' AND s.payload ? 'period')<6`

// RefreshStopped is a SQL condition: the close's newest period snapshot on its reading connection is the scheduler's,
// and the scheduler no longer refreshes it: six are taken, only the close's last free snapshot is left, it is past the
// months scheduleSync reads, or the connection's latest finished capture failed or could not refresh the month-end.
// Later entries may then be missing, so sign-off waits for a successful read or a person's capture. A close already
// at its twenty snapshots cannot be captured again, so it is not asked to be.
func RefreshStopped(close string) string {
	return `COALESCE((SELECT s.actor='scheduled-quickbooks-refresh' AND ((SELECT count(*) FROM quickbooks_snapshots z WHERE z.close_id=s.close_id AND z.actor='scheduled-quickbooks-refresh' AND z.payload ? 'period')>=6
 OR (SELECT count(*) FROM quickbooks_snapshots z WHERE z.close_id=s.close_id)=19
 OR COALESCE((SELECT y.error<>'' FROM quickbooks_entity_syncs y WHERE y.close_id=s.close_id AND y.connection_id=s.connection_id AND y.period=sc.period AND y.state IN ('completed','unchanged','failed') ORDER BY y.id DESC LIMIT 1),false)
 OR sc.period<to_char((now() AT TIME ZONE 'UTC')-interval '1 month','YYYY-MM') AND NOT EXISTS(SELECT 1 FROM close_goals g WHERE g.close_id=sc.id AND g.state='active'))
 FROM quickbooks_snapshots s JOIN client_closes sc ON sc.id=s.close_id WHERE s.close_id=` + close + ` AND s.payload ? 'period' AND s.connection_id=` + ReadingConnection(close) + `
 ORDER BY s.captured_at DESC,s.id DESC LIMIT 1),false)`
}

// periodContent digests a period snapshot's accounts and reports, leaving out what changes on every read (a report's
// generation time) or with activity after the month (current balances, sync tokens); "" when it cannot be read.
func periodContent(payload []byte) string {
	var p struct {
		Accounts []map[string]any          `json:"accounts"`
		Reports  map[string]map[string]any `json:"reports"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if decoder.Decode(&p) != nil || len(p.Reports) == 0 {
		return ""
	}
	for _, account := range p.Accounts {
		for _, volatile := range []string{"CurrentBalance", "CurrentBalanceWithSubAccounts", "MetaData", "SyncToken"} {
			delete(account, volatile)
		}
	}
	for _, report := range p.Reports {
		if header, ok := report["Header"].(map[string]any); ok {
			delete(header, "Time")
		}
	}
	normalized, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(normalized))
}

// captureFirstPeriod saves an ended month's period snapshot and, while the scheduler's snapshot is the newest, a fresh
// one when its accounts or reports have changed, so later entries reach the tie-out. A person's capture or refresh is never replaced.
func captureFirstPeriod(ctx context.Context, db *sql.DB, job syncClaim, environment, realm, token string) error {
	var org, entity, company string
	err := db.QueryRowContext(ctx, firstPeriod, job.closeID, job.connectionID, job.period).Scan(&org, &entity, &company)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	started := time.Now()
	snapshot, evidence, err := ReadSnapshot(ctx, environment, realm, token, company, job.period, started)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Same lock order as a person's capture, so the two never wait on each other's row locks.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1||'/'||lower($2)))`, org, entity); err == nil {
		if _, err = tx.ExecContext(ctx, `SELECT 1 FROM quickbooks_connections WHERE id=$1 FOR UPDATE`, job.connectionID); err == nil {
			_, err = tx.ExecContext(ctx, `SELECT 1 FROM client_closes WHERE id=$1 FOR UPDATE`, job.closeID)
		}
	}
	if err != nil {
		return err
	}
	var lockedOrg, lockedEntity, lockedCompany string
	err = tx.QueryRowContext(ctx, firstPeriod, job.closeID, job.connectionID, job.period).Scan(&lockedOrg, &lockedEntity, &lockedCompany)
	if err == sql.ErrNoRows || err == nil && (lockedOrg != org || lockedEntity != entity || lockedCompany != company) {
		return nil
	}
	if err != nil {
		return err
	}
	// Compared with this connection's own newest, so a new reading connection always gets its first snapshot.
	var previous []byte
	var first bool
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT payload FROM quickbooks_snapshots WHERE close_id=$1 AND connection_id=$2 AND payload ? 'period' ORDER BY captured_at DESC,id DESC LIMIT 1),NOT EXISTS(SELECT 1 FROM quickbooks_snapshots WHERE close_id=$1 AND payload ? 'period')`, job.closeID, job.connectionID).Scan(&previous, &first); err != nil {
		return err
	}
	if previous != nil {
		fresh, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if unchanged := periodContent(previous); unchanged != "" && unchanged == periodContent(fresh) {
			return nil
		}
	}
	var count, size int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),COALESCE(sum(COALESCE(octet_length(original),octet_length(content))) FILTER (WHERE NOT (created_by='quickbooks-sync' AND superseded_by IS NOT NULL)),0) FROM evidence WHERE close_id=$1`, job.closeID).Scan(&count, &size); err != nil {
		return err
	}
	if count >= 100 || size+len(evidence) > 20*1024*1024 {
		return errors.New("close evidence capacity cannot hold the period snapshot")
	}
	var sourceID string
	if err = tx.QueryRowContext(ctx, `INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'scheduled-quickbooks-refresh') RETURNING id::text`, job.closeID, "QuickBooks "+job.period+" accrual period evidence.txt", fmt.Sprintf("%x", sha256.Sum256(evidence)), string(evidence)).Scan(&sourceID); err != nil {
		return err
	}
	snapshot["source_id"] = sourceID
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,$3,encode(sha256(convert_to($4::jsonb::text,'UTF8')),'hex'),$4,$5,'scheduled-quickbooks-refresh')`, job.closeID, job.connectionID, realm, string(payload), started); err != nil {
		return err
	}
	if !first {
		return tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `UPDATE preparation_jobs j SET state='waiting_evidence',error='' FROM preparation_intakes i WHERE i.job_id=j.id AND j.close_id=$1 AND j.state='needs_confirmation' AND i.proposal->>'engine'='openai'`, job.closeID); err != nil {
		return err
	}
	return tx.Commit()
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

// CapturedRows reads a capture's indexed transaction rows.
func CapturedRows(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, syncID string) ([]TransactionRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT kind,provider_id,txn_date::text,COALESCE(currency,''),COALESCE(amount,''),record FROM quickbooks_transaction_rows WHERE sync_id=$1`, syncID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	scanned := []TransactionRow{}
	for rows.Next() {
		var row TransactionRow
		if err = rows.Scan(&row.Kind, &row.ProviderID, &row.Date, &row.Currency, &row.Amount, &row.Record); err != nil {
			return nil, err
		}
		scanned = append(scanned, row)
	}
	return scanned, rows.Err()
}

// captureExceptions records the transaction rows' exceptions and the open items' past-due questions.
func captureExceptions(ctx context.Context, tx *sql.Tx, rowsSync string, open map[string][]json.RawMessage, period string) (string, error) {
	scanned, err := CapturedRows(ctx, tx, rowsSync)
	if err != nil {
		return "", err
	}
	month, err := time.Parse("2006-01", period)
	if err != nil {
		return "", err
	}
	items := append(FindExceptions(scanned), OpenItemExceptions(open, month.AddDate(0, 1, -1))...)
	if items == nil {
		items = []Exception{}
	}
	found, _ := json.Marshal(items)
	return string(found), nil
}
