package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestStripePayoutsAndActivityReportsAreCapturedByKind(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org, entity := "org_StripeKinds"+rand.Text(), "Stripe kinds client"
	var closeID, connection string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Stripe kinds fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM stripe_report_runs WHERE close_id=$1`, `DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`} {
			db.Exec(q, closeID)
		}
		for _, q := range []string{`DELETE FROM stripe_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ef", 32))
	must(db.QueryRow(`INSERT INTO stripe_connections(close_id,organization_id,client_entity,actor,state,livemode,key_last4,account_id) VALUES($1,$2,$3,'fixture','active',false,'1234','acct_123') RETURNING id::text`, closeID, org, entity).Scan(&connection))
	sealed, err := secrets.Seal(org+"/stripe/"+connection, []byte("rk_test_abcdefghijklmnop1234"))
	must(err)
	_, err = db.Exec(`UPDATE stripe_connections SET credential=$2 WHERE id=$1`, connection, sealed)
	must(err)

	files := map[string]string{
		"payouts":  "balance_transaction_id,currency,effective_at,net,payout_expected_arrival_date,payout_id,payout_status,reporting_category\ntxn_m1,usd,2026-09-02 10:00:00,-125.05,2026-09-03,po_M1,paid,payout\n",
		"activity": "balance_transaction_id,created,currency,gross,fee,net,reporting_category\ntxn_a1,2026-09-01 12:00:00,usd,130.00,4.95,125.05,charge\n",
	}
	types := map[string]string{"payouts.itemized.5": "payouts", "balance_change_from_activity.itemized.7": "activity", "payout_reconciliation.itemized.5": "payout_reconciliation"}
	activityFrom := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	requested := []string{}
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		reply := func(body any) (*http.Response, error) {
			raw, _ := json.Marshal(body)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		}
		switch {
		case r.URL.Host == "files.stripe.com":
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(files[strings.Split(r.URL.Path, "/")[3][len("file_"):]])), Header: make(http.Header)}, nil
		case strings.Contains(r.URL.Path, "/reporting/report_types/"):
			from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			if strings.HasSuffix(r.URL.Path, "activity.itemized.7") {
				from = activityFrom
			}
			return reply(map[string]any{"data_available_start": from.Unix(), "data_available_end": time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC).Unix()})
		case r.URL.Path == "/v1/account":
			return reply(map[string]any{"id": "acct_123"})
		case r.Method == "POST" && r.URL.Path == "/v1/reporting/report_runs":
			r.ParseForm()
			kind := types[r.PostForm.Get("report_type")]
			requested = append(requested, kind+"/"+strings.Join(r.PostForm["parameters[columns][]"], ","))
			return reply(map[string]any{"id": "frr_" + kind, "status": "pending"})
		case strings.Contains(r.URL.Path, "/reporting/report_runs/frr_"):
			kind := strings.TrimPrefix(r.URL.Path, "/v1/reporting/report_runs/frr_")
			return reply(map[string]any{"status": "succeeded", "result": map[string]string{"url": "https://files.stripe.com/v1/files/file_" + kind + "/contents"}})
		}
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"not found"}}`)), Header: make(http.Header)}, nil
	})
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_admin"}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}
	mux := http.NewServeMux()
	registerStripe(mux, db)
	capture := func(report string, want int) string {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"action": "capture", "connection_id": connection, "confirmed": true, "report": report})
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/stripe", bytes.NewReader(raw)).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("capture %s: %d %s", report, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	capture("balance", 422)
	if body := capture("activity", 409); !strings.Contains(body, "Stripe balance activity data from 15 September 2026") {
		t.Fatalf("each report is requested only once its own data covers the month: %s", body)
	}
	activityFrom = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	capture("payouts", 200)
	if body := capture("activity", 200); !strings.Contains(body, `"report": "activity"`) && !strings.Contains(body, `"report":"activity"`) {
		t.Fatalf("the runs list names each report: %s", body)
	}
	if len(requested) != 2 || requested[0] != "payouts/balance_transaction_id,currency,net,reporting_category,payout_id,effective_at,payout_expected_arrival_date,payout_status" || requested[1] != "activity/balance_transaction_id,currency,gross,fee,net,reporting_category,created" {
		t.Fatalf("each report is asked for with its own type and its parser's columns, one of each in flight: %v", requested)
	}
	for range 3 {
		must(sweepStripeReports(context.Background(), db))
		_, err = db.Exec(`UPDATE stripe_report_runs SET claimed_at=NULL WHERE close_id=$1`, closeID)
		must(err)
	}
	var saved string
	must(db.QueryRow(`SELECT string_agg(e.name||':'||(SELECT count(*) FROM preparation_intakes i WHERE i.source_id=e.id),',' ORDER BY e.name) FROM stripe_report_runs r JOIN evidence e ON e.id=r.evidence_id WHERE r.close_id=$1 AND r.state='saved'`, closeID).Scan(&saved))
	if saved != "Stripe balance activity 2026-09.csv:0,Stripe payouts 2026-09.csv:1" {
		t.Fatalf("both reports are saved by name; only the Payouts report starts work: %s", saved)
	}
	for n := 0; ; n++ {
		worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db)
		if e != nil || n > 20 {
			t.Fatal("intake did not settle", e)
		}
		if !worked {
			break
		}
	}
	var proposal []byte
	must(db.QueryRow(`SELECT i.proposal FROM preparation_intakes i JOIN evidence e ON e.id=i.source_id WHERE e.close_id=$1 AND e.name='Stripe payouts 2026-09.csv'`, closeID).Scan(&proposal))
	var recognized preparation.Proposal
	json.Unmarshal(proposal, &recognized)
	if recognized.Kind != "settlement" || recognized.Engine != "recognized_export" || !strings.Contains(recognized.Summary, "Payouts report: 1 payouts expected 2026-09-03") {
		t.Fatalf("the Payouts report is recognized without a model: %s", proposal)
	}
	// A request whose answer was lost is asked again for its own report.
	_, err = db.Exec(`INSERT INTO stripe_report_runs(close_id,connection_id,period,state,actor,report,created_at) VALUES($1,$2,'2026-08','requesting','user_admin','activity',now()-interval '2 minutes')`, closeID, connection)
	must(err)
	must(sweepStripeReports(context.Background(), db))
	if last := requested[len(requested)-1]; !strings.HasPrefix(last, "activity/") {
		t.Fatalf("a retried request keeps its report: %s", last)
	}
}
