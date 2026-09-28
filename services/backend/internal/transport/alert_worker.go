package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type operationsAlert struct {
	ID           int64  `json:"-"`
	Organization string `json:"organization_id"`
	CloseID      string `json:"close_id"`
	Kind         string `json:"-"`
	ItemID       string `json:"item_id"`
	State        string `json:"state"`
	AgeMinutes   int    `json:"age_minutes"`
}

func collectOperationsAlerts(ctx context.Context, db *sql.DB) error {
	firms, err := db.QueryContext(ctx, `SELECT id FROM organizations WHERE enabled ORDER BY id`)
	if err != nil {
		return err
	}
	ids := []string{}
	for firms.Next() {
		var id string
		if err = firms.Scan(&id); err != nil {
			break
		}
		ids = append(ids, id)
	}
	if err == nil {
		err = firms.Err()
	}
	firms.Close()
	if err != nil {
		return err
	}
	for _, firm := range ids {
		_, err = db.ExecContext(ctx, `UPDATE operations_alert_deliveries d SET resolved_at=now() WHERE d.organization_id=$1 AND d.resolved_at IS NULL AND NOT EXISTS(SELECT 1 FROM (`+operationsAlertQuery+`) current_alerts WHERE current_alerts.kind=d.kind AND current_alerts.item_id=d.item_id AND current_alerts.state=d.item_state)`, firm)
		if err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `INSERT INTO operations_alert_deliveries(organization_id,close_id,kind,item_id,item_state,age_minutes) SELECT $1,close_id::bigint,kind,item_id,state,age_minutes FROM (`+operationsAlertQuery+`) current_alerts WHERE true ON CONFLICT DO NOTHING`, firm)
		if err != nil {
			return err
		}
	}
	return nil
}

func sendOperationsAlert(ctx context.Context, endpoint, token string, a operationsAlert) error {
	content, _ := json.Marshal(a)
	return postAlert(ctx, endpoint, token, fmt.Sprintf("close-alert/%d", a.ID), content)
}

// postAlert posts one alert to the operations webhook, with an idempotency key so a retried delivery is one alert.
func postAlert(ctx context.Context, endpoint, token, key string, content []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Webhook response was not received")
	}
	io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Webhook returned HTTP %d", response.StatusCode)
	}
	return nil
}

func deliverOperationsAlerts(ctx context.Context, db *sql.DB, endpoint, token string) error {
	for {
		var a operationsAlert
		err := db.QueryRowContext(ctx, `UPDATE operations_alert_deliveries SET status='sending',claimed_at=now() WHERE id=(SELECT id FROM operations_alert_deliveries WHERE status='queued' AND resolved_at IS NULL ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,organization_id,close_id::text,kind,item_id,item_state,age_minutes`).Scan(&a.ID, &a.Organization, &a.CloseID, &a.Kind, &a.ItemID, &a.State, &a.AgeMinutes)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		sendErr := sendOperationsAlert(ctx, endpoint, token, a)
		state, detail := "accepted", ""
		if sendErr != nil {
			state, detail = "unknown", sendErr.Error()
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err = db.ExecContext(persist, `UPDATE operations_alert_deliveries SET status=$2,error=$3,finished_at=now() WHERE id=$1 AND status='sending'`, a.ID, state, detail)
		cancel()
		if err != nil {
			return err
		}
	}
}

// platformAlertQuery is the platform's own problems now, as (kind, item_id, detail): a loop whose heartbeat stopped (only
// the worker process and the workflow engine report one; the other job loops' stalls show as their stuck items, in
// Stuck work and the firms' alerts), workflow runs stuck as the admin panel's stuck view counts them, and provider
// writes whose outcome is unknown. The detail may change without
// re-alerting; a new alert needs a new kind or item.
const platformAlertQuery = `SELECT 'platform.loop_stale',loop,'last beat '||beat_at::text FROM worker_heartbeats WHERE beat_at<now()-interval '5 minutes'
 UNION ALL SELECT 'platform.runs_stuck','workflow_runs',count(*)||' runnable run(s) not picked up' FROM workflow_runs r WHERE ` + stuckRuns + ` HAVING count(*)>0
 UNION ALL SELECT 'platform.unknown_effects','workflow_steps',count(*)||' provider write(s) to reconcile' FROM workflow_steps WHERE state='unknown' HAVING count(*)>0`

// collectPlatformAlerts resolves platform alerts whose problem cleared and opens one for each new problem.
func collectPlatformAlerts(ctx context.Context, db *sql.DB) error {
	ctx = context.WithValue(ctx, systemKey{}, true)
	if _, err := db.ExecContext(ctx, `UPDATE platform_alerts a SET resolved_at=now() WHERE a.resolved_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM (`+platformAlertQuery+`) p(kind,item_id,detail) WHERE p.kind=a.kind AND p.item_id=a.item_id)`); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO platform_alerts(kind,item_id,detail) SELECT kind,item_id,detail FROM (`+platformAlertQuery+`) p(kind,item_id,detail) ON CONFLICT DO NOTHING`)
	return err
}

// deliverPlatformAlerts sends each queued platform alert to the operations webhook, once, like a firm's alert. A claim
// the worker stopped holding before the webhook answered (a send takes at most 10 seconds) becomes unknown: it may have
// arrived, so it is not sent again, and the Overview shows it as unconfirmed for someone to check.
func deliverPlatformAlerts(ctx context.Context, db *sql.DB, endpoint, token string) error {
	ctx = context.WithValue(ctx, systemKey{}, true)
	if _, err := db.ExecContext(ctx, `UPDATE platform_alerts SET status='unknown',error='the worker stopped before the webhook answered',finished_at=now()
 WHERE status='sending' AND claimed_at<now()-interval '2 minutes'`); err != nil {
		return err
	}
	for {
		var id int64
		var kind, item, detail string
		err := db.QueryRowContext(ctx, `UPDATE platform_alerts SET status='sending',claimed_at=now() WHERE id=(SELECT id FROM platform_alerts WHERE status='queued' AND resolved_at IS NULL ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING id,kind,item_id,detail`).Scan(&id, &kind, &item, &detail)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		content, _ := json.Marshal(map[string]string{"scope": "platform", "kind": kind, "item_id": item, "detail": detail})
		sendErr := postAlert(ctx, endpoint, token, fmt.Sprintf("close-platform-alert/%d", id), content)
		state, failure := "accepted", ""
		if sendErr != nil {
			state, failure = "unknown", sendErr.Error()
		}
		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err = db.ExecContext(persist, `UPDATE platform_alerts SET status=$2,error=$3,finished_at=now() WHERE id=$1 AND status='sending'`, id, state, failure)
		cancel()
		if err != nil {
			return err
		}
	}
}

// alertWebhook is the operations webhook alerts go to; ok is false when none is set or the setting is not a plain
// HTTPS endpoint.
func alertWebhook() (endpoint, token string, ok bool) {
	endpoint = os.Getenv("OPERATIONS_ALERT_WEBHOOK_URL")
	if endpoint == "" {
		return "", "", false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(endpoint, "\r\n") {
		log.Print("Operations alert webhook URL must be an HTTPS endpoint without credentials or fragment")
		return "", "", false
	}
	return endpoint, os.Getenv("OPERATIONS_ALERT_WEBHOOK_TOKEN"), true
}

// RunPlatformAlerts checks the platform's own problems every minute and sends each new one to the operations webhook.
// It is its own loop, so a backlog of firm alerts never delays it.
func RunPlatformAlerts(ctx context.Context, db *sql.DB) {
	endpoint, token, ok := alertWebhook()
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := collectPlatformAlerts(ctx, db); err != nil && ctx.Err() == nil {
			log.Print("Could not collect platform alerts: ", err)
		}
		if err := deliverPlatformAlerts(ctx, db, endpoint, token); err != nil && ctx.Err() == nil {
			log.Print("Could not deliver platform alerts: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func RunAlerts(ctx context.Context, db *sql.DB) {
	endpoint, token, ok := alertWebhook()
	if !ok {
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := collectOperationsAlerts(ctx, db); err != nil && ctx.Err() == nil {
			log.Print("Could not collect operations alerts: ", err)
		}
		if err := deliverOperationsAlerts(ctx, db, endpoint, token); err != nil && ctx.Err() == nil {
			log.Print("Could not deliver operations alerts: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
