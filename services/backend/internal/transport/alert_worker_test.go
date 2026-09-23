package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestOperationsAlertSendsOnlyScopedMetadata(t *testing.T) {
	var delivered operationsAlert
	var fields map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer alert-secret" || r.Header.Get("Idempotency-Key") != "close-alert/19" {
			t.Error("alert request omitted its authority or durable identity")
		}
		body := json.NewDecoder(r.Body)
		if err := body.Decode(&fields); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(fields)
		if err := json.Unmarshal(raw, &delivered); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	a := operationsAlert{ID: 19, Organization: "org_example", CloseID: "42", Kind: "preparation", ItemID: "71", State: "failed", AgeMinutes: 12}
	if err := sendOperationsAlert(context.Background(), server.URL, "alert-secret", a); err != nil || delivered.Organization != a.Organization || delivered.CloseID != a.CloseID || delivered.ItemID != a.ItemID || delivered.State != a.State || delivered.AgeMinutes != a.AgeMinutes {
		t.Fatalf("alert delivery mismatch: %v %+v", err, delivered)
	}
	if len(fields) != 5 {
		t.Fatalf("alert payload added an unapproved field: %+v", fields)
	}
}

func TestOperationsAlertAttemptDoesNotRepeat(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID, org string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text,organization_id`, "Alert "+rand.Text()).Scan(&closeID, &org); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, closeID)
	if _, err = db.Exec(`INSERT INTO operations_alert_deliveries(organization_id,close_id,kind,item_id,item_state,age_minutes) VALUES($1,$2,'preparation','synthetic-job','failed',11)`, org, closeID); err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusAccepted) }))
	defer server.Close()
	for i := 0; i < 2; i++ {
		if err = deliverOperationsAlerts(context.Background(), db, server.URL, ""); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err = db.QueryRow(`SELECT status FROM operations_alert_deliveries WHERE close_id::text=$1`, closeID).Scan(&state); err != nil || state != "accepted" || calls != 1 {
		t.Fatalf("alert was not single-attempt and durable: %s %d %v", state, calls, err)
	}
	if _, err = db.Exec(`UPDATE operations_alert_deliveries SET resolved_at=now() WHERE close_id::text=$1`, closeID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO operations_alert_deliveries(organization_id,close_id,kind,item_id,item_state,age_minutes) VALUES($1,$2,'preparation','synthetic-job','failed',4)`, org, closeID); err != nil {
		t.Fatal(err)
	}
	if err = deliverOperationsAlerts(context.Background(), db, server.URL, ""); err != nil || calls != 2 {
		t.Fatalf("a new incident was not delivered: %d %v", calls, err)
	}
}
