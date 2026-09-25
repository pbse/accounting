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

func TestStripeReportArrivesAsRecognizedEvidence(t *testing.T) {
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
	org, entity := "org_Stripe"+rand.Text(), "Stripe live client"
	var closeID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Stripe fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_admin',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM stripe_report_runs WHERE close_id=$1`, `DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`} {
			db.Exec(q, closeID)
		}
		for _, q := range []string{`DELETE FROM stripe_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ef", 32))
	report := "automatic_payout_effective_at,automatic_payout_id,balance_transaction_id,currency,fee,gross,net,reporting_category\n2026-09-03 00:00:00,po_1,txn_1,usd,3.20,100.00,96.80,charge\n2026-09-30 00:00:00,po_2,txn_3,usd,1.46,40.00,38.54,charge\n"
	through, account, runStatus, file, refuseRun, limited := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "acct_123", "pending", "https://files.stripe.com/v1/files/file_1/contents", false, false
	keys, intervals, runs, downloads, leaked := []string{}, []string{}, 0, 0, 0
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		reply := func(status int, body any) (*http.Response, error) {
			raw, _ := json.Marshal(body)
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		}
		if r.URL.Host != "api.stripe.com" && r.URL.Host != "files.stripe.com" {
			leaked++
		}
		if user, _, _ := r.BasicAuth(); !strings.HasPrefix(user, "rk_test_") {
			return reply(401, map[string]any{"error": map[string]string{"message": "Invalid API key"}})
		}
		switch {
		case r.URL.Host == "files.stripe.com":
			downloads++
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(report)), Header: make(http.Header)}, nil
		case strings.HasSuffix(r.URL.Path, "/reporting/report_types/payout_reconciliation.itemized.5"):
			return reply(200, map[string]any{"data_available_end": through.Unix()})
		case r.URL.Path == "/v1/account":
			return reply(200, map[string]any{"id": account, "business_profile": map[string]string{"name": "Stripe live client"}})
		case r.Method == "POST" && limited:
			return reply(429, map[string]any{"error": map[string]string{"message": "Too many requests"}})
		case r.Method == "POST" && r.URL.Path == "/v1/reporting/report_runs":
			r.ParseForm()
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			if refuseRun {
				return reply(400, map[string]any{"error": map[string]string{"message": "interval_end must be <= data_available_end"}})
			}
			intervals = append(intervals, r.PostForm.Get("parameters[interval_start]")+"-"+r.PostForm.Get("parameters[interval_end]"))
			if r.PostForm.Get("report_type") != "payout_reconciliation.itemized.5" || len(r.PostForm["parameters[columns][]"]) != 8 {
				return reply(400, map[string]any{"error": map[string]string{"message": "unexpected report request"}})
			}
			runs++
			return reply(200, map[string]any{"id": "frr_1", "status": "pending"})
		case strings.HasSuffix(r.URL.Path, "/reporting/report_runs/frr_1"):
			return reply(200, map[string]any{"id": "frr_1", "status": runStatus, "error": "Report failed", "result": map[string]string{"url": file}})
		}
		return reply(404, map[string]any{"error": map[string]string{"message": "not found"}})
	})
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_admin"}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}
	mux := http.NewServeMux()
	registerStripe(mux, db)
	var status struct {
		Connection *struct {
			ID, State, Key_last4, Account_id string
		}
		Runs []struct{ State, Error, Evidence_id string }
	}
	call := func(body map[string]any, want int) []byte {
		t.Helper()
		body["confirmed"] = true
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/stripe", bytes.NewReader(raw)).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v: %d %s", body["action"], w.Code, w.Body.String())
		}
		if want == 200 {
			must(json.Unmarshal(w.Body.Bytes(), &status))
		}
		return w.Body.Bytes()
	}
	sweep := func() {
		t.Helper()
		must(sweepStripeReports(context.Background(), db))
	}
	if body := call(map[string]any{"action": "connect", "key": "sk_test_abcdefghijklmnop"}, 422); !strings.Contains(string(body), "Secret keys are refused") {
		t.Fatalf("a secret key is refused: %s", body)
	}
	key := "rk_test_abcdefghijklmnop1234"
	call(map[string]any{"action": "connect", "key": key}, 200)
	if status.Connection == nil || status.Connection.State != "unconfirmed" || status.Connection.Account_id != account || status.Connection.Key_last4 != "1234" {
		t.Fatalf("a checked key waits for a person to confirm its account: %+v", status.Connection)
	}
	var sealed []byte
	must(db.QueryRow(`SELECT credential FROM stripe_connections WHERE id=$1`, status.Connection.ID).Scan(&sealed))
	if plain, e := secrets.Open(org+"/stripe/"+status.Connection.ID, sealed); e != nil || string(plain) != key || bytes.Contains(sealed, []byte(key)) {
		t.Fatal("the key is stored only sealed")
	}
	id := status.Connection.ID
	call(map[string]any{"action": "capture", "connection_id": id}, 409)
	call(map[string]any{"action": "confirm", "connection_id": id}, 200)

	through = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	if body := call(map[string]any{"action": "capture", "connection_id": id}, 409); !strings.Contains(string(body), "whole month") || runs != 0 {
		t.Fatalf("a month Stripe has not finished is not requested: %s", body)
	}
	through, account = time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), "acct_999"
	if body := call(map[string]any{"action": "capture", "connection_id": id}, 409); !strings.Contains(string(body), "another Stripe account") || runs != 0 {
		t.Fatalf("a key that now belongs to another account is refused: %s", body)
	}
	account = "acct_123"
	call(map[string]any{"action": "capture", "connection_id": id}, 200)
	call(map[string]any{"action": "capture", "connection_id": id}, 200)
	if runs != 1 || len(status.Runs) != 1 || status.Runs[0].State != "pending" || keys[0] == "" || intervals[0] != "1788220800-1790812800" {
		t.Fatalf("one report run is requested with an idempotency key, and a repeat reuses it: runs=%d %+v", runs, status.Runs)
	}
	sweep()
	var soon bool
	must(db.QueryRow(`SELECT claimed_at<now()-interval '3 minutes' FROM stripe_report_runs WHERE close_id=$1 AND state='pending'`, closeID).Scan(&soon))
	if downloads != 0 || !soon {
		t.Fatal("a pending report is not downloaded, and is checked again about a minute later")
	}
	runStatus = "succeeded"
	_, err = db.Exec(`UPDATE stripe_report_runs SET claimed_at=NULL WHERE close_id=$1`, closeID)
	must(err)
	sweep()
	sweep()
	var evidenceName, content, jobState string
	must(db.QueryRow(`SELECT e.name,e.content FROM stripe_report_runs r JOIN evidence e ON e.id=r.evidence_id WHERE r.close_id=$1 AND r.state='saved'`, closeID).Scan(&evidenceName, &content))
	if evidenceName != "Stripe payout reconciliation 2026-09.csv" || content != report || downloads != 1 {
		t.Fatalf("the report is saved once as evidence: %s downloads=%d", evidenceName, downloads)
	}
	for n := 0; ; n++ {
		worked, e := preparation.ProcessIntake(context.Background(), db)
		if e != nil || n > 20 {
			t.Fatal("intake did not settle", e)
		}
		if !worked {
			break
		}
	}
	var proposal []byte
	must(db.QueryRow(`SELECT j.state,i.proposal FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN stripe_report_runs r ON r.evidence_id=i.source_id WHERE r.close_id=$1`, closeID).Scan(&jobState, &proposal))
	var recognized preparation.Proposal
	json.Unmarshal(proposal, &recognized)
	if jobState != "needs_confirmation" || recognized.Kind != "settlement" || recognized.Engine != "recognized_export" || !strings.Contains(recognized.Summary, "2 automatic payouts") {
		t.Fatalf("the saved report is recognized as the Stripe export, like an upload: %s %s", jobState, proposal)
	}

	// A refused request, a failed report and a foreign file location end the run with the reason.
	_, err = db.Exec(`UPDATE stripe_report_runs SET period='2026-08' WHERE close_id=$1`, closeID)
	must(err)
	refuseRun = true
	if body := call(map[string]any{"action": "capture", "connection_id": id}, 422); !strings.Contains(string(body), "data_available_end") {
		t.Fatalf("Stripe's refusal is shown: %s", body)
	}
	refuseRun, runStatus = false, "failed"
	_, err = db.Exec(`DELETE FROM stripe_report_runs WHERE close_id=$1 AND state='failed'`, closeID)
	must(err)
	call(map[string]any{"action": "capture", "connection_id": id}, 200)
	sweep()
	call(map[string]any{"action": "capture", "connection_id": id}, 200)
	if len(status.Runs) < 2 || status.Runs[1].State != "failed" || !strings.Contains(status.Runs[1].Error, "Report failed") {
		t.Fatalf("a failed report records Stripe's reason, and a new capture can start: %+v", status.Runs)
	}
	// A lost answer is asked again with the same idempotency key.
	var lost, lostKey string
	must(db.QueryRow(`INSERT INTO stripe_report_runs(close_id,connection_id,period,state,actor,created_at) VALUES($1,$2,'2026-07','requesting','user_admin',now()-interval '2 minutes') RETURNING id::text,idempotency_key::text`, closeID, id).Scan(&lost, &lostKey))
	runStatus = "pending"
	sweep()
	var lostState string
	must(db.QueryRow(`SELECT state FROM stripe_report_runs WHERE id=$1`, lost).Scan(&lostState))
	if lostState != "pending" || keys[len(keys)-1] != lostKey {
		t.Fatalf("a request whose answer was lost is asked again with its own key: %s", lostState)
	}
	_, err = db.Exec(`DELETE FROM stripe_report_runs WHERE id=$1`, lost)
	must(err)
	// A report without the parser's columns is not saved.
	report = "balance_transaction_id,currency,gross\ntxn_1,usd,1.00\n"
	runStatus = "succeeded"
	_, err = db.Exec(`UPDATE stripe_report_runs SET claimed_at=NULL WHERE close_id=$1`, closeID)
	must(err)
	sweep()
	call(map[string]any{"action": "capture", "connection_id": id}, 200)
	if !strings.Contains(status.Runs[0].Error+status.Runs[1].Error, "lacks the column") {
		t.Fatalf("a report without the settlement columns fails with the reason: %+v", status.Runs)
	}
	runStatus, file = "succeeded", "https://evil.example/v1/files/file_1/contents"
	_, err = db.Exec(`UPDATE stripe_report_runs SET claimed_at=NULL WHERE close_id=$1`, closeID)
	must(err)
	sweep()
	if downloads != 2 || leaked != 0 {
		t.Fatalf("the key is never sent to another host: %d %d", downloads, leaked)
	}
	var location, locationState string
	must(db.QueryRow(`SELECT state,error FROM stripe_report_runs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&locationState, &location))
	if locationState != "failed" || !strings.Contains(location, "unexpected report file location") {
		t.Fatalf("a foreign file location ends the run: %q", location)
	}

	// Temporary conditions keep a report waiting with its reason; definitive ones end it.
	file, runStatus = "https://files.stripe.com/v1/files/file_1/contents", "succeeded"
	report = "automatic_payout_effective_at,automatic_payout_id,balance_transaction_id,currency,fee,gross,net,reporting_category\n2026-06-03 00:00:00,po_9,txn_9,usd,1.00,10.00,9.00,charge\n"
	var waiting string
	must(db.QueryRow(`INSERT INTO stripe_report_runs(close_id,connection_id,period,state,actor,run_id) VALUES($1,$2,'2026-06','pending','user_admin','frr_1') RETURNING id::text`, closeID, id).Scan(&waiting))
	check := func(want, reason string) {
		t.Helper()
		_, e := db.Exec(`UPDATE stripe_report_runs SET claimed_at=NULL WHERE id=$1`, waiting)
		must(e)
		run, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		must(sweepStripeReports(run, db))
		var state, message string
		must(db.QueryRow(`SELECT state,error FROM stripe_report_runs WHERE id=$1`, waiting).Scan(&state, &message))
		if state != want || !strings.Contains(message, reason) {
			t.Fatalf("want %s %q, got %s %q", want, reason, state, message)
		}
	}
	t.Setenv("SECRETS_KEY", strings.Repeat("99", 32))
	check("pending", "")
	t.Setenv("SECRETS_KEY", strings.Repeat("ef", 32))
	defer db.Exec(`UPDATE organizations SET max_pending_jobs=250 WHERE id=$1`, org)
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=(SELECT greatest(count(*),1) FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.organization_id=$1 AND j.state IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client')) WHERE id=$1`, org)
	must(err)
	check("pending", "active preparation limit")
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=250 WHERE id=$1`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by,purpose) VALUES($1,'policy.csv',$2,$3,'fixture','procedure')`, closeID, preparation.Digest([]byte(report)), report)
	must(err)
	check("failed", "different purpose")
	_, err = db.Exec(`DELETE FROM evidence WHERE close_id=$1 AND purpose='procedure'`, closeID)
	must(err)
	_, err = db.Exec(`UPDATE stripe_report_runs SET state='pending',error='' WHERE id=$1`, waiting)
	must(err)
	_, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) SELECT $1,'filler.txt','filler-'||g,'x','fixture' FROM generate_series(1,100) g`, closeID)
	must(err)
	check("failed", "evidence limit")
	_, err = db.Exec(`DELETE FROM evidence WHERE close_id=$1 AND name='filler.txt'`, closeID)
	must(err)

	// Another client's connection cannot capture into this close.
	var other string
	must(db.QueryRow(`INSERT INTO stripe_connections(close_id,organization_id,client_entity,actor,state,livemode,key_last4,account_id) VALUES($1,$2,'Another client','fixture','active',false,'9999','acct_other') RETURNING id::text`, closeID, org).Scan(&other))
	_, err = db.Exec(`UPDATE stripe_report_runs SET state='failed' WHERE close_id=$1 AND state IN ('requesting','pending')`, closeID)
	must(err)
	_, err = db.Exec(`UPDATE client_closes SET period='2026-04' WHERE id=$1`, closeID)
	must(err)
	if body := call(map[string]any{"action": "capture", "connection_id": other}, 409); !strings.Contains(string(body), "Connect and confirm") {
		t.Fatalf("another client's connection is refused: %s", body)
	}
	// A rate limit on the request leaves it to be asked again with its key, not failed.
	limited = true
	call(map[string]any{"action": "capture", "connection_id": id}, 200)
	if status.Runs[0].State != "requesting" {
		t.Fatalf("a rate-limited request stays requesting: %+v", status.Runs[0])
	}
	limited = false
	_, err = db.Exec(`UPDATE client_closes SET period='2026-09' WHERE id=$1`, closeID)
	must(err)
	call(map[string]any{"action": "disconnect", "connection_id": id}, 200)
	var pendingLeft int
	must(db.QueryRow(`SELECT count(*) FROM stripe_report_runs WHERE close_id=$1 AND state IN ('requesting','pending')`, closeID).Scan(&pendingLeft))
	var cleared bool
	must(db.QueryRow(`SELECT credential IS NULL AND state='disconnected' FROM stripe_connections WHERE id=$1`, id).Scan(&cleared))
	if pendingLeft != 0 || !cleared || status.Connection != nil {
		t.Fatalf("disconnecting clears the key and ends reports in progress: %d %v", pendingLeft, cleared)
	}
}
