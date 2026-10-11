package transport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestApprovedEmailDurableRetryAndScope(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_Email" + rand.Text()
	var closeID string
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Email fixture')`, org); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_sender',true,'Synthetic enrollment','fixture')`, org); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Email synthetic','2026-09') RETURNING id::text`, org).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM coverage_bindings WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM request_drafts WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM email_connections WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_sender", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Organization: org, Permissions: []string{"clients:all", "integrations:manage", "requests:send", "workpapers:review"}}
	mux := http.NewServeMux()
	registerEmail(mux, db)
	registerCoverage(mux, db)
	registerWork(mux, db)
	h := organizationBoundary(db, mux)
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	attempts, created := 0, 0
	seen := map[string]string{}
	unverified := false
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.resend.com" {
			t.Fatalf("unexpected provider %s", r.URL.Host)
		}
		var response string
		if r.Method == "GET" {
			state := "verified"
			if unverified {
				state = "pending"
			}
			response = `{"id":"domain-fixture","name":"firm.example","status":"` + state + `","click_tracking":false,"capabilities":{"sending":"enabled"}}`
		} else {
			attempts++
			key := r.Header.Get("Idempotency-Key")
			if !strings.HasPrefix(key, "close-request/") {
				t.Fatal("missing stable idempotency key")
			}
			raw, _ := io.ReadAll(r.Body)
			var message delivery.Email
			if json.Unmarshal(raw, &message) != nil || message.From != "Accountants <requests@firm.example>" || len(message.To) != 1 || message.To[0] != "client@example.test" || !strings.Contains(message.Text, "https://app.example.test/request#") {
				t.Fatal("incorrect approved email payload")
			}
			var durable string
			if e := db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, strings.TrimPrefix(key, "close-request/")).Scan(&durable); e != nil || durable != "sending" {
				t.Fatal("provider called before durable attempt")
			}
			if prior, ok := seen[key]; ok {
				if prior != string(raw) {
					t.Fatal("retry changed approved payload")
				}
			} else {
				seen[key] = string(raw)
				created++
			}
			if attempts == 1 {
				return nil, errors.New("synthetic lost provider response")
			}
			response = `{"id":"provider-` + strings.TrimPrefix(key, "close-request/") + `"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	connect := map[string]any{"key": "re_synthetic_only_00000", "from": "Accountants <requests@firm.example>", "domain_id": "domain-fixture", "confirmed": true}
	unverified = true
	call("POST", "/v1/closes/email", connect, 422)
	unverified = false
	call("POST", "/v1/closes/email", connect, 200)
	var status struct {
		Connection struct {
			ID string `json:"id"`
		} `json:"connection"`
	}
	rawStatus := call("GET", "/v1/closes/email", nil, 200)
	if strings.Contains(string(rawStatus), "re_synthetic") {
		t.Fatal("credential exposed")
	}
	json.Unmarshal(rawStatus, &status)
	var encrypted []byte
	if err = db.QueryRow(`SELECT credential FROM email_connections WHERE id=$1`, status.Connection.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if _, err = secrets.Open("org_other/resend", encrypted); err == nil {
		t.Fatal("ciphertext moved between firms")
	}
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("synthetic-webhook-secret-only"))
	call("POST", "/v1/closes/email", map[string]any{"previous_id": status.Connection.ID, "webhook_secret": secret, "confirmed": true}, 200)
	webhookMux := http.NewServeMux()
	webhookMux.HandleFunc("POST /v1/webhooks/resend/{connection}", emailWebhook(db))
	event := func(message, kind, eventID string, tamper bool, want int) {
		t.Helper()
		raw := []byte(`{"type":"email.` + kind + `","created_at":"2026-09-01T12:00:00Z","data":{"email_id":"provider-` + message + `","to":["client@example.test"]}}`)
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(eventID + "." + timestamp + "."))
		mac.Write(raw)
		signature := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
		if tamper {
			raw = append(raw, ' ')
		}
		r := httptest.NewRequest("POST", "/v1/webhooks/resend/"+status.Connection.ID, bytes.NewReader(raw))
		r.Header.Set("svix-id", eventID)
		r.Header.Set("svix-timestamp", timestamp)
		r.Header.Set("svix-signature", signature)
		w := httptest.NewRecorder()
		webhookMux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("webhook %s: %d %s", kind, w.Code, w.Body.String())
		}
	}
	makeRequest := func(name string) (string, string) {
		t.Helper()
		ctx := context.Background()
		tx, e := db.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		var source string
		if e = tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$2,'synthetic','fixture') RETURNING id::text`, closeID, name).Scan(&source); e != nil {
			t.Fatal(e)
		}
		job, e := preparation.StartIntake(ctx, tx, closeID, source, "fixture")
		if e != nil {
			t.Fatal(e)
		}
		tx.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, job)
		tx.Exec(`UPDATE preparation_intakes SET revision=1 WHERE job_id=$1`, job)
		digest, _, e := preparation.RequestContext(ctx, tx, closeID, nil)
		if e != nil {
			t.Fatal(e)
		}
		var draft, request string
		if e = tx.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Synthetic approved request','[]',$2,'fixture') RETURNING id::text`, closeID, digest).Scan(&draft); e != nil {
			t.Fatal(e)
		}
		if e = tx.QueryRow(`INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) VALUES($1,$2,1,'client@example.test','user_sender') RETURNING id::text`, job, draft).Scan(&request); e != nil {
			t.Fatal(e)
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
		return job, request
	}
	enqueue := func(job, request string) string {
		var result struct {
			ID string `json:"id"`
		}
		body := map[string]any{"confirmed": true, "revision": 1, "connection_id": status.Connection.ID}
		path := "/v1/closes/" + closeID + "/work/" + job + "/requests/" + request + "/email"
		json.Unmarshal(call("POST", path, body, 200), &result)
		call("POST", path, body, 200)
		return result.ID
	}
	job, request := makeRequest("invoice-one")
	id := enqueue(job, request)
	if _, err = db.Exec(`UPDATE email_outbox SET state='sending',claimed_at=now(),claim='fixture' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	event("unrelated", "bounced", "unrelated-delivery", false, 204)
	var unrelated sql.NullString
	if err = db.QueryRow(`SELECT close_id::text FROM email_delivery_events WHERE event_id='unrelated-delivery' AND connection_id=$1`, status.Connection.ID).Scan(&unrelated); err != nil || unrelated.Valid {
		t.Fatalf("unrelated delivery attributed to client close: %v %v", unrelated, err)
	}
	if _, err = db.Exec(`UPDATE email_outbox SET state='queued',claim=NULL,claimed_at=NULL WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	event(id, "delivered", "first-delivery", true, 401)
	event(id, "delivered", "first-delivery", false, 204)
	event(id, "delivered", "first-delivery", false, 204)
	event(id, "bounced", "first-delivery", false, 409)
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var state string
	db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, id).Scan(&state)
	if state != "queued" {
		t.Fatalf("uncertain response state: %s", state)
	}
	db.Exec(`UPDATE email_outbox SET next_attempt_at=now() WHERE id=$1`, id)
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, id).Scan(&state)
	if state != "accepted" || attempts != 2 || created != 1 {
		t.Fatalf("retry did not preserve one provider create: %s %d %d", state, attempts, created)
	}
	if processed, e := delivery.ProcessOne(context.Background(), db); e != nil || processed {
		t.Fatal("terminal email sent again")
	}
	var deliveryState string
	if e := db.QueryRow(`SELECT delivery FROM email_delivery WHERE id=$1`, id).Scan(&deliveryState); e != nil || deliveryState != "delivered" {
		t.Fatalf("early event lost: %s %v", deliveryState, e)
	}
	var eventClose string
	if e := db.QueryRow(`SELECT close_id::text FROM email_delivery_events WHERE event_id='first-delivery' AND connection_id=$1`, status.Connection.ID).Scan(&eventClose); e != nil || eventClose != closeID {
		t.Fatalf("early delivery event was not linked to its close: %s %v", eventClose, e)
	}
	job, request = makeRequest("invoice-two")
	expired := enqueue(job, request)
	db.Exec(`UPDATE email_outbox SET attempts=1,first_attempt_at=now()-interval '25 hours' WHERE id=$1`, expired)
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, expired).Scan(&state)
	if state != "unknown" || attempts != 2 {
		t.Fatal("out-of-window retry reached provider")
	}
	job, request = makeRequest("invoice-three")
	stale := enqueue(job, request)
	// Replacing the document the request's work was read from stops the queued email.
	db.Exec(`WITH fresh AS (INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'changed','changed','changed','fixture') RETURNING id) UPDATE evidence SET superseded_by=(SELECT id FROM fresh) WHERE close_id=$1 AND digest='invoice-three'`, closeID)
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, stale).Scan(&state)
	if state != "cancelled" || attempts != 2 {
		t.Fatal("stale approved request was sent")
	}
	// With passcodes required the approval shows one once; a retried approval refuses rather than hide it.
	if _, err = db.Exec(`UPDATE organizations SET require_link_passcode=true WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	job, request = makeRequest("invoice-four")
	guarded := "/v1/closes/" + closeID + "/work/" + job + "/requests/" + request + "/email"
	approval := map[string]any{"confirmed": true, "revision": 1, "connection_id": status.Connection.ID}
	var approved struct{ ID, Passcode string }
	json.Unmarshal(call("POST", guarded, approval, 200), &approved)
	var sealed []byte
	if err = db.QueryRow(`SELECT payload FROM email_outbox WHERE id=$1`, approved.ID).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	plain, err := secrets.Open(org+"/email/"+approved.ID, sealed)
	if len(approved.Passcode) != 8 || err != nil || !strings.Contains(string(plain), "passcode your accounting team gives you") || strings.Contains(string(plain), approved.Passcode) {
		t.Fatalf("the approval returns the passcode and the email only says one will be given: %+v %v", approved, err)
	}
	if body := call("POST", guarded, approval, 409); !strings.Contains(string(body), "new private reply link") {
		t.Fatalf("a retried approval cannot show the passcode again: %s", body)
	}
	if _, err = db.Exec(`UPDATE email_outbox SET state='cancelled' WHERE id=$1`, approved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE organizations SET require_link_passcode=false WHERE id=$1`, org); err != nil {
		t.Fatal(err)
	}
	// Group request has no primary job: two pinned jobs share one email/link.
	jobA, _ := makeRequest("group-a")
	jobB, _ := makeRequest("group-b")
	var ledger, conn, snapshot string
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(db.QueryRow(`SELECT source_id::text FROM preparation_intakes WHERE job_id=$1`, jobA).Scan(&ledger))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&conn))
	raw, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": ledger, "coverage": []map[string]string{{"account_id": "a", "name": "Prepaid A", "kind": "prepaid", "question": "Invoice A?"}, {"account_id": "b", "name": "Prepaid B", "kind": "prepaid", "question": "Invoice B?"}}})
	must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123','email-ledger',$3,now(),'fixture') RETURNING id::text`, closeID, conn, string(raw)).Scan(&snapshot))
	coveragePath := "/v1/closes/" + closeID + "/coverage"
	view := func() coverageView {
		t.Helper()
		var v struct {
			Coverage coverageView `json:"coverage"`
		}
		must(json.Unmarshal(call("GET", coveragePath, nil, 200), &v))
		return v.Coverage
	}
	for n, j := range []string{jobA, jobB} {
		_, e := db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id=$1`, j)
		must(e)
		call("POST", coveragePath, map[string]any{"action": "bind", "digest": view().Digest, "account_id": []string{"a", "b"}[n], "jobs": []string{j}, "reason": "Complete synthetic scope", "confirmed": true}, 200)
	}
	var group struct {
		ID string `json:"id"`
	}
	must(json.Unmarshal(call("POST", coveragePath, map[string]any{"action": "request", "digest": view().Digest, "accounts": []string{"a", "b"}, "jobs": []string{}, "recipient": "client@example.test", "body": "Please supply both invoices.", "confirmed": true}, 200), &group))
	path := "/v1/closes/" + closeID + "/requests/" + group.ID + "/email"
	approve := func(previous string, want int) string {
		t.Helper()
		var got struct {
			ID string `json:"id"`
		}
		must(json.Unmarshal(call("POST", path, map[string]any{"confirmed": true, "connection_id": status.Connection.ID, "previous_id": previous}, want), &got))
		return got.ID
	}
	first := approve("", 200)
	_, err = delivery.ProcessOne(context.Background(), db)
	must(err)
	approve(first, 409) // Provider acceptance is not delivery.
	event(first, "delivered", "group-delivered", false, 204)
	event(first, "sent", "group-out-of-order", false, 204)
	rule := func(after, most int) {
		t.Helper()
		_, e := db.Exec(`UPDATE organizations SET reminder_after_days=$2,reminder_max=$3 WHERE id=(SELECT organization_id FROM client_closes WHERE id=$1)`, closeID, after, most)
		must(e)
	}
	reminderState := func() (left int, due *time.Time, expires *time.Time) {
		t.Helper()
		var v struct {
			Requests []struct {
				ID       string `json:"id"`
				Reminder struct {
					Left      int        `json:"left"`
					DueAt     *time.Time `json:"due_at"`
					ExpiresAt *time.Time `json:"expires_at"`
				} `json:"reminder"`
			} `json:"requests"`
		}
		must(json.Unmarshal(call("GET", coveragePath, nil, 200), &v))
		for _, q := range v.Requests {
			if q.ID == group.ID {
				return q.Reminder.Left, q.Reminder.DueAt, q.Reminder.ExpiresAt
			}
		}
		t.Fatal("request missing from coverage")
		return
	}
	// A delegated close's documents say when following up with the client is the firm's decision, as Work does.
	followUp := func() string {
		t.Helper()
		tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
		must(err)
		defer tx.Rollback()
		p, err := readGoalProgress(context.Background(), tx, closeID, "review")
		must(err)
		for _, item := range p.Items {
			if item.JobID == jobA {
				return item.Status + ": " + item.Text
			}
		}
		t.Fatal("the request's document is missing from the goal's progress")
		return ""
	}
	rule(2, 1)
	_, err = db.Exec(`UPDATE email_delivery_events SET occurred_at=now() WHERE event_id='group-delivered'`)
	must(err)
	if left, due, expires := reminderState(); left != 1 || due == nil || due.Before(time.Now().Add(47*time.Hour)) || expires == nil || !expires.After(time.Now()) {
		t.Fatalf("the next reminder is due two days after delivery, with one allowed, before the emailed link expires: %d %v %v", left, due, expires)
	}
	if got := followUp(); got != "client: Waiting on the client's reply to request #"+group.ID+"." {
		t.Fatalf("before a reminder is due the client has it: %q", got)
	}
	approve(first, 409)
	rule(0, 1)
	if got := followUp(); got != "needs_you: A reminder for request #"+group.ID+" is due under the firm's rule. Approve it on the Requests tab." {
		t.Fatalf("a due reminder is the firm's decision: %q", got)
	}
	// Only a reminder that could be approved is offered: not while the firm's sender is off.
	_, err = db.Exec(`UPDATE email_connections SET enabled=false WHERE id::text=$1`, status.Connection.ID)
	must(err)
	if got := followUp(); !strings.HasPrefix(got, "client: ") {
		t.Fatalf("a reminder that cannot be approved is not offered: %q", got)
	}
	_, err = db.Exec(`UPDATE email_connections SET enabled=true WHERE id::text=$1`, status.Connection.ID)
	must(err)
	reminder := approve(first, 200)
	if approve(first, 200) != reminder {
		t.Fatal("duplicate approval queued another reminder")
	}
	_, err = delivery.ProcessOne(context.Background(), db)
	must(err)
	var firstDigest, reminderDigest string
	must(db.QueryRow(`SELECT link_digest FROM email_outbox WHERE id=$1`, first).Scan(&firstDigest))
	must(db.QueryRow(`SELECT link_digest FROM email_outbox WHERE id=$1`, reminder).Scan(&reminderDigest))
	if firstDigest != reminderDigest {
		t.Fatal("reminder replaced approved link")
	}
	event(reminder, "delivered", "reminder-delivered", false, 204)
	if left, _, _ := reminderState(); left != 0 {
		t.Fatalf("the one allowed reminder is used: %d", left)
	}
	if got := followUp(); got != "needs_you: Request #"+group.ID+" is unanswered after all the reminders the firm's rule allows. Contact the client another way." {
		t.Fatalf("with no reminders left the client is contacted another way: %q", got)
	}
	approve(reminder, 409)
	rule(0, 3)
	var expires time.Time
	must(db.QueryRow(`UPDATE client_request_links SET expires_at=now()-interval '1 minute' WHERE digest=$1 RETURNING (SELECT expires_at FROM client_request_links WHERE digest=$1)`, firstDigest).Scan(&expires))
	if got := followUp(); got != "needs_you: The link for request #"+group.ID+" has expired, or expires before the next reminder is due. Contact the client another way." {
		t.Fatalf("a lapsed link is followed up another way: %q", got)
	}
	_, err = db.Exec(`UPDATE client_request_links SET expires_at=$2 WHERE digest=$1`, firstDigest, expires)
	must(err)
	// A request whose work changed takes no reply, so the goal asks for a fresh one rather than a reminder.
	_, err = db.Exec(`UPDATE preparation_intakes SET revision=revision+1 WHERE job_id::text=$1`, jobA)
	must(err)
	if got := followUp(); got != "needs_you: Request #"+group.ID+" no longer takes replies. Request work changed. Review and issue a fresh request." {
		t.Fatalf("a stale request is redone, not reminded: %q", got)
	}
	_, err = db.Exec(`UPDATE preparation_intakes SET revision=revision-1 WHERE job_id::text=$1`, jobA)
	must(err)
	// After a partial reply through the link, a reminder for the questions still open may follow, its period counted
	// from that reply.
	_, err = db.Exec(`INSERT INTO work_replies(request_id,source_id,recorded_by,received_from) VALUES($1,$2,'client','client@example.test')`, group.ID, ledger)
	must(err)
	_, err = db.Exec(`INSERT INTO request_submissions(request_id,receipt,source_id,complete) VALUES($1,'partial-fixture',$2,false)`, group.ID, ledger)
	must(err)
	_, err = db.Exec(`UPDATE client_request_links SET used_at=now() WHERE digest=$1`, firstDigest)
	must(err)
	rule(1, 3)
	if _, due, _ := reminderState(); due == nil || due.Before(time.Now().Add(23*time.Hour)) {
		t.Fatalf("a partial reply restarts the reminder period: %v", due)
	}
	rule(0, 3)
	if got := followUp(); got != "needs_you: The client answered part of request #"+group.ID+". Resume now from Work, or approve a reminder for the rest on the Requests tab." {
		t.Fatalf("a partial reply can be reminded for the rest: %q", got)
	}
	partialReminder := approve(reminder, 200)
	_, err = delivery.ProcessOne(context.Background(), db)
	must(err)
	must(db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, partialReminder).Scan(&state))
	if state != "accepted" {
		t.Fatalf("the reminder after a partial reply is sent, not cancelled: %s", state)
	}
	_, err = db.Exec(`DELETE FROM email_outbox WHERE id=$1`, partialReminder)
	must(err)
	_, err = db.Exec(`DELETE FROM request_submissions WHERE request_id=$1`, group.ID)
	must(err)
	_, err = db.Exec(`UPDATE client_request_links SET used_at=NULL WHERE digest=$1`, firstDigest)
	must(err)
	_, err = db.Exec(`DELETE FROM work_replies WHERE request_id=$1`, group.ID)
	must(err)
	// A staff-recorded answer also blocks a reminder even with an unused link.
	_, err = db.Exec(`INSERT INTO work_replies(request_id,source_id,recorded_by,received_from) VALUES($1,$2,'fixture','client@example.test')`, group.ID, ledger)
	must(err)
	approve(reminder, 409)
	_, err = db.Exec(`DELETE FROM work_replies WHERE request_id=$1`, group.ID)
	must(err)
	// So does an emailed reply still held for review: the client has answered, so no reminder goes out meanwhile.
	_, err = db.Exec(`INSERT INTO inbound_replies(close_id,request_id,outbox_id,message_digest,envelope_from,header_from,body,addresses_match) SELECT d.close_id,q.id,$2,md5(random()::text),'client@example.test','client@example.test','Attached as asked',true FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id::text=$1`, group.ID, first)
	must(err)
	approve(reminder, 409)
	_, err = db.Exec(`DELETE FROM inbound_replies WHERE request_id::text=$1`, group.ID)
	must(err)
	queued := approve(reminder, 200)
	event(first, "bounced", "group-bounce", false, 204)
	before := attempts
	_, err = delivery.ProcessOne(context.Background(), db)
	must(err)
	must(db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, queued).Scan(&state))
	if state != "cancelled" || attempts != before {
		t.Fatal("bounce failed to stop queued reminder")
	}
	// Readbacks must support multiple messages without scalar-subquery errors.
	view()
	call("GET", "/v1/closes/"+closeID+"/work?view=blocked", nil, 200)
	actor.Permissions = []string{"clients:all", "workpapers:review"}
	call("POST", "/v1/closes/"+closeID+"/work/"+job+"/requests/"+request+"/email", map[string]any{}, 403)
}
