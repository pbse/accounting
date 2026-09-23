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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("close-alert/%d", a.ID))
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

func RunAlerts(ctx context.Context, db *sql.DB) {
	endpoint := os.Getenv("OPERATIONS_ALERT_WEBHOOK_URL")
	if endpoint == "" {
		return
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(endpoint, "\r\n") {
		log.Print("Operations alert webhook URL must be an HTTPS endpoint without credentials or fragment")
		return
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := collectOperationsAlerts(ctx, db); err != nil && ctx.Err() == nil {
			log.Print("Could not collect operations alerts: ", err)
		}
		if err := deliverOperationsAlerts(ctx, db, endpoint, os.Getenv("OPERATIONS_ALERT_WEBHOOK_TOKEN")); err != nil && ctx.Err() == nil {
			log.Print("Could not deliver operations alerts: ", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
