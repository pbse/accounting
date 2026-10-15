package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestReplyTextAcceptsPlainTextAndRejectsAttachments(t *testing.T) {
	from, body, err := replyText([]byte("From: Client <client@example.com>\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nPlease use the September invoice."))
	if err != nil || from != "client@example.com" || body != "Please use the September invoice." {
		t.Fatalf("plain text: %q %q %v", from, body, err)
	}
	_, _, err = replyText([]byte("From: client@example.com\r\nContent-Type: multipart/mixed; boundary=x\r\n\r\n--x\r\nContent-Type: text/plain\r\n\r\nReply\r\n--x--"))
	if err == nil {
		t.Fatal("attachment-capable multipart accepted")
	}
}

func TestInboundReplyHeldUntilSenderAndReviewerMatch(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID, source, job, draft, request, connection string
	entity := "Email reply " + rand.Text()
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, `DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM request_drafts WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			if _, e := db.Exec(q, closeID); e != nil {
				t.Error(e)
			}
		}
		db.Exec(`DELETE FROM email_connections WHERE id::text=$1`, connection)
	}()
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Synthetic invoice','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&source); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err = preparation.StartIntake(context.Background(), tx, closeID, source, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`UPDATE preparation_intakes SET revision=1 WHERE job_id=$1`, job); err != nil {
		t.Fatal(err)
	}
	digest, err := preparation.RequestContext(context.Background(), tx, closeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please clarify','[]',$2,'fixture') RETURNING id::text`, closeID, digest).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,1,'client@example.test','fixture') RETURNING id::text`, draft).Scan(&request); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO work_request_targets(request_id,job_id,revision) VALUES($1,$2,1)`, request, job); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO client_request_links(digest,request_id,issued_by) VALUES($1,$2,'fixture')`, "reply-test-"+request, request); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,actor) VALUES('local-development','firm@example.test','synthetic','dummy','fixture') RETURNING id::text`).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	outbox := rand.Text()
	if _, err = tx.Exec(`INSERT INTO email_outbox(id,request_id,connection_id,link_digest,payload,state,approved_by,firm_authority,approved_until) VALUES($1,$2,$3,$4,'dummy','accepted','fixture',true,now()+interval '1 hour')`, outbox, request, connection, "reply-test-"+request); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EMAIL_INTAKE_ADDRESS", "inbox@example.test")
	t.Setenv("EMAIL_INTAKE_KEY", strings.Repeat("k", 32))
	h := apiWithStore(db, "email-reply-fixture-workspace-token-000000")
	internal := func(sender, text string, headers ...string) {
		t.Helper()
		raw := []byte("From: " + sender + "\r\n" + strings.Join(headers, "") + "Content-Type: text/plain; charset=UTF-8\r\n\r\n" + text)
		r := httptest.NewRequest("POST", "/internal/email-reply", bytes.NewReader(raw))
		r.Header.Set("X-Close-Email-Key", strings.Repeat("k", 32))
		r.Header.Set("X-Close-Envelope-To", "inbox+reply-"+strings.ToLower(outbox)+"@example.test")
		r.Header.Set("X-Close-Envelope-From", sender)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 202 {
			t.Fatalf("intake %d %s", w.Code, w.Body.String())
		}
	}
	staff := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer email-reply-fixture-workspace-token-000000")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("review %d %s", w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	internal("intruder@example.test", "Please use the wrong invoice.")
	internal("client@example.test", "Please use the September invoice.\nOur checking account 1234 5678 90 is new.")
	internal("client@example.test", "Please use the September invoice.\nOur checking account 1234 5678 90 is new.")
	var stored string
	if err = db.QueryRow(`SELECT string_agg(body,'|') FROM inbound_replies WHERE close_id=$1 AND header_from='client@example.test'`, closeID).Scan(&stored); err != nil || stored != "Please use the September invoice.\nOur checking account [bank account number ending 7890 removed] is new." {
		t.Fatalf("a held email reply keeps only the redacted text, once per delivered message: %q %v", stored, err)
	}
	var fingerprints int
	raw := "From: client@example.test\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\nPlease use the September invoice.\nOur checking account 1234 5678 90 is new."
	if err = db.QueryRow(`SELECT count(*) FROM inbound_replies WHERE message_digest=$1`, preparation.Digest([]byte(raw))).Scan(&fingerprints); err != nil || fingerprints != 0 {
		t.Fatalf("no digest of the raw message is kept: %d %v", fingerprints, err)
	}
	var held []struct {
		ID             string `json:"id"`
		AddressesMatch bool   `json:"addresses_match"`
	}
	base := "/v1/closes/" + closeID + "/email-replies"
	if err = json.Unmarshal(staff("GET", base, nil, 200), &held); err != nil || len(held) != 2 {
		t.Fatalf("held replies %v %v", held, err)
	}
	for _, item := range held {
		if !item.AddressesMatch {
			staff("POST", base+"/"+item.ID, map[string]any{"action": "release", "reason": "Synthetic reviewer decision", "verification_reference": "Confirmed with known contact on 2026-09-23", "confirmed": true}, 409)
			staff("POST", base+"/"+item.ID, map[string]any{"action": "reject", "reason": "Sender does not match the recipient", "confirmed": true}, 200)
		}
	}
	for _, item := range held {
		if item.AddressesMatch {
			staff("POST", base+"/"+item.ID, map[string]any{"action": "release", "reason": "Reviewed exact sender and answer", "verification_reference": "Confirmed with known contact on 2026-09-23", "confirmed": true}, 200)
		}
	}
	var state string
	var replies int
	var used bool
	if err = db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state); err != nil || state != "waiting_evidence" {
		t.Fatalf("job not resumed: %s %v", state, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM work_replies r JOIN evidence e ON e.id=r.source_id WHERE r.request_id=$1 AND e.content NOT LIKE '%1234 5678%'`, request).Scan(&replies); err != nil || replies != 1 {
		t.Fatalf("wrong reply count: %d %v", replies, err)
	}
	if err = db.QueryRow(`SELECT used_at IS NOT NULL FROM client_request_links WHERE digest=$1`, "reply-test-"+request).Scan(&used); err != nil || !used {
		t.Fatalf("link remained reusable: %v", err)
	}
	internal("client@example.test", "Please use the September invoice.\nOur checking account 1234 5678 90 is new.", "Message-Id: <second@example.test>\r\n")
	internal("client@example.test", "Please use the September invoice.\nOur checking account 1234 5678 90 is new.", "Date: Fri, 25 Sep 2026 10:00:00 +0000\r\n")
	if err = db.QueryRow(`SELECT count(*) FROM inbound_replies WHERE close_id=$1 AND header_from='client@example.test'`, closeID).Scan(&replies); err != nil || replies != 3 {
		t.Fatalf("a separately sent email with the same text is held separately: %d %v", replies, err)
	}
	// The link is used now, but the request still takes answers, so a later emailed answer can be released too.
	var later string
	if err = db.QueryRow(`SELECT id::text FROM inbound_replies WHERE close_id=$1 AND state='held' AND addresses_match ORDER BY id LIMIT 1`, closeID).Scan(&later); err != nil {
		t.Fatal(err)
	}
	staff("POST", base+"/"+later, map[string]any{"action": "release", "reason": "Reviewed the later answer", "verification_reference": "Confirmed with known contact on 2026-09-25", "confirmed": true}, 200)
	// A new sender (a colleague of the client) is released only once the firm confirms it verified them through a known
	// contact channel, and the saved reply says so.
	internal("colleague@example.test", "Here are the remaining figures.")
	var colleague string
	if err = db.QueryRow(`SELECT id::text FROM inbound_replies WHERE close_id=$1 AND state='held' AND header_from='colleague@example.test'`, closeID).Scan(&colleague); err != nil {
		t.Fatal(err)
	}
	newSender := map[string]any{"action": "release", "reason": "Colleague answered for the client", "verification_reference": "Called the client's known number on 2026-09-26", "confirmed": true}
	staff("POST", base+"/"+colleague, newSender, 409)
	newSender["new_sender_verified"] = true
	staff("POST", base+"/"+colleague, newSender, 200)
	if err = db.QueryRow(`SELECT count(*) FROM work_replies WHERE request_id=$1 AND received_from='Email from colleague@example.test (new sender, firm-verified)'`, request).Scan(&replies); err != nil || replies != 1 {
		t.Fatalf("the released reply names its new, verified sender: %d %v", replies, err)
	}
}
