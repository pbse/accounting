package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestCoverageRequestsQuarantineAndResolution(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	var cid, conn, ledger, snapshot string
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Coverage synthetic "+rand.Text()).Scan(&cid))
	defer func() {
		for _, q := range []string{
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`,
			`DELETE FROM coverage_bindings WHERE close_id=$1`, `DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM request_drafts WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`,
			`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`,
		} {
			if _, e := db.Exec(q, cid); e != nil {
				t.Error(e)
			}
		}
	}()
	source := func(name, content string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, cid, name, preparation.Digest([]byte(content)), content).Scan(&id))
		return id
	}
	ledger = source("ledger.txt", "Synthetic ledger fixture")
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, cid, rand.Text()).Scan(&conn))
	raw, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": ledger, "coverage": []map[string]string{{"account_id": "a", "name": "Prepaid A", "kind": "prepaid", "question": "Provide invoice A terms."}, {"account_id": "b", "name": "Prepaid B", "kind": "prepaid", "question": "Provide invoice B terms."}, {"account_id": "c", "name": "Prepaid C", "kind": "prepaid", "question": "Provide missing invoice C."}, {"account_id": "d", "name": "Prepaid D", "kind": "prepaid", "question": "Provide missing invoice D."}, {"account_id": "e", "name": "Prepaid E", "kind": "prepaid", "question": "Provide missing invoice E."}}})
	must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123','synthetic-ledger',$3,now(),'fixture') RETURNING id::text`, cid, conn, string(raw)).Scan(&snapshot))
	jobs, sources := []string{}, []string{}
	for _, name := range []string{"A", "B"} {
		sid := source(name+".txt", "Invoice amount: 1200.00\nInvoice: "+name)
		sources = append(sources, sid)
		tx, e := db.Begin()
		must(e)
		job, e := preparation.StartIntake(ctx, tx, cid, sid, "fixture")
		must(e)
		_, e = tx.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id=$1`, job)
		must(e)
		must(tx.Commit())
		jobs = append(jobs, job)
	}
	_, err = db.Exec(`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","facts":[{"field":"prepaid_account","value":"Prepaid A"},{"field":"booking_reference","value":"INV-42"}]}' WHERE job_id=$1`, jobs[0])
	must(err)
	token := strings.Repeat("k", 32)
	handler := apiWithStore(db, token)
	call := func(method, path, bearer string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + cid + "/coverage"
	view := func() coverageView {
		t.Helper()
		var got struct {
			Coverage coverageView `json:"coverage"`
		}
		must(json.Unmarshal(call("GET", path, token, nil, 200), &got))
		return got.Coverage
	}
	suggestion := view()
	if len(suggestion.Items[0].SuggestedJobIDs) != 1 || suggestion.Items[0].SuggestedJobIDs[0] != jobs[0] || len(suggestion.Items[0].SuggestedLedgerQueries) != 1 || suggestion.Items[0].SuggestedLedgerQueries[0] != "INV-42" || suggestion.Items[0].State != "missing_evidence" {
		t.Fatalf("account suggestion must stay unapproved: %+v", suggestion.Items[0])
	}
	for n, account := range []string{"a", "b"} {
		v := view()
		call("POST", path, token, map[string]any{"action": "bind", "digest": v.Digest, "account_id": account, "jobs": []string{jobs[n]}, "reason": "Complete required invoice set for this synthetic account", "confirmed": true}, 200)
	}
	v := view()
	if v.Items[0].State != "awaiting_work" {
		t.Fatal("unfinished work resolved")
	}
	var approved struct {
		ID    string `json:"id"`
		Token string `json:"link_token"`
	}
	must(json.Unmarshal(call("POST", path, token, map[string]any{"action": "request", "digest": v.Digest, "accounts": []string{"a", "b"}, "jobs": []string{}, "recipient": "client@example.test", "body": "Please provide both invoices and service dates.", "confirmed": true}, 200), &approved))
	if approved.Token == "" {
		t.Fatal("missing private link")
	}
	response := call("GET", "/v1/client-request", approved.Token, nil, 200)
	if bytes.Contains(response, []byte("job_ids")) || bytes.Contains(response, []byte("recipient")) {
		t.Fatal("internal request scope exposed")
	}
	if !bytes.Contains(response, []byte(`"rescan":false`)) {
		t.Fatal("no scan retry is offered before any attachment")
	}
	// Unknown fields cannot redirect a scoped request to another close.
	call("POST", "/v1/client-request", approved.Token, map[string]any{"body": "answer", "confirmed": true, "close_id": "other"}, 400)
	submit := func(want int) string {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		must(m.WriteField("body", "Service end: 2027-08-31"))
		must(m.WriteField("confirmed", "true"))
		for _, file := range []struct{ name, body string }{{"invoice.txt", "Invoice amount: 1200.00\nService end: 2027-08-31"}, {"cloud.pdf", "%PDF-1.4\nSynthetic scan fixture\n%%EOF"}} {
			f, e := m.CreateFormFile("files", file.name)
			must(e)
			_, e = f.Write([]byte(file.body))
			must(e)
		}
		must(m.Close())
		r := httptest.NewRequest("POST", "/v1/client-request", &b)
		r.Header.Set("Authorization", "Bearer "+approved.Token)
		r.Header.Set("Content-Type", m.FormDataContentType())
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("submit %d: %s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	if first := submit(200); !strings.Contains(first, `"rescan":true`) {
		t.Fatalf("the reply that saves files already offers a scan retry for them: %s", first)
	}
	submit(200)
	var count int
	must(db.QueryRow(`SELECT count(*) FROM client_attachments WHERE request_id=$1`, approved.ID).Scan(&count))
	if count != 2 {
		t.Fatal("attachment replay duplicated bytes")
	}
	if !bytes.Contains(call("GET", "/v1/client-request", approved.Token, nil, 200), []byte(`"rescan":true`)) {
		t.Fatal("a scan retry is offered while an attachment waits for its scan")
	}
	for _, job := range jobs {
		var state string
		must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state))
		if state != "waiting_evidence" {
			t.Fatal("affected job not resumed")
		}
		tx, e := db.Begin()
		must(e)
		inventory, _, e := preparation.AgentInventory(ctx, tx, cid, job)
		tx.Rollback()
		must(e)
		for _, s := range inventory {
			if s.Name == "invoice.txt" || s.Name == "cloud.pdf" {
				t.Fatal("quarantine leaked into model inventory")
			}
		}
	}
	// The origin accepts only a trusted edge verdict for the exact saved file.
	scanKey := strings.Repeat("s", 32)
	t.Setenv("UPLOAD_SCAN_KEY", scanKey)
	scan := func(key, bearer, body string, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/client-request/scan", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/pdf")
		r.Header.Set("Malicious-Uploads-Detection", "3") // Untrusted on direct origin calls.
		r.Header.Set("X-Close-Upload-Scan-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("scan %d: %s", w.Code, w.Body.String())
		}
	}
	pdf := "%PDF-1.4\nSynthetic scan fixture\n%%EOF"
	scan("", approved.Token, pdf, 503)
	scan("forged", approved.Token, pdf, 503)
	scan(scanKey, strings.Repeat("x", 32), pdf, 404)
	scan(scanKey, approved.Token, "%PDF-1.4\nDifferent bytes\n%%EOF", 404)
	scan(scanKey, approved.Token, pdf, 200)
	scan(scanKey, approved.Token, pdf, 200)
	var scanned bool
	must(db.QueryRow(`SELECT state='clean' AND scanned_digest=digest AND scan_version='Cloudflare WAF malicious uploads detection' FROM client_attachments WHERE request_id=$1 AND name='cloud.pdf'`, approved.ID).Scan(&scanned))
	if !scanned {
		t.Fatal("trusted scan receipt not persisted")
	}
	var attachment string
	must(db.QueryRow(`SELECT min(id)::text FROM client_attachments WHERE request_id=$1`, approved.ID).Scan(&attachment))
	ap := "/v1/closes/" + cid + "/attachments/" + attachment
	decision := map[string]any{"action": "release", "confirmed": true, "reason": "Reviewed client and requested scope"}
	call("POST", ap, token, decision, 409)
	call("GET", ap, token, nil, 409)
	// The separate text service receives only authorized original bytes.
	cleared := false
	revokeDuringScan := false
	scanCalls := 0
	textServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scanCalls++
		if r.Header.Get("Authorization") != "Bearer "+scanKey {
			t.Error("scanner credentials missing")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "Invoice amount: 1200.00\nService end: 2027-08-31" {
			t.Error("scanner received wrong bytes")
		}
		if !cleared {
			http.Error(w, "not clean", 422)
			return
		}
		if revokeDuringScan {
			_, e := db.Exec(`UPDATE client_request_links SET revoked_at=now() WHERE request_id=$1`, approved.ID)
			if e != nil {
				t.Error(e)
			}
		}
		jsonReply(w, 200, map[string]string{"status": "clean", "version": "synthetic test engine"})
	}))
	defer textServer.Close()
	t.Setenv("TEXT_SCANNER_URL", textServer.URL)
	textScan := func(bearer string, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/client-request/scan-text", strings.NewReader("Invoice amount: 1200.00\nService end: 2027-08-31"))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("text scan %d: %s", w.Code, w.Body.String())
		}
	}
	textScan(strings.Repeat("x", 32), 404)
	if scanCalls != 0 {
		t.Fatal("unauthorized request started scanner")
	}
	textScan(approved.Token, 422)
	call("POST", ap, token, decision, 409)
	cleared = true
	revokeDuringScan = true
	textScan(approved.Token, 409)
	revokeDuringScan = false
	_, err = db.Exec(`UPDATE client_request_links SET revoked_at=NULL WHERE request_id=$1`, approved.ID)
	must(err)
	textScan(approved.Token, 200)
	_, err = db.Exec(`UPDATE client_attachments SET scanned_digest='wrong' WHERE id=$1`, attachment)
	must(err)
	call("POST", ap, token, decision, 409)
	_, err = db.Exec(`UPDATE client_attachments SET scanned_digest=digest WHERE id=$1`, attachment)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='disconnected' WHERE id=$1`, conn)
	must(err)
	call("POST", ap, token, decision, 409)
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='active' WHERE id=$1`, conn)
	must(err)
	call("POST", ap, token, decision, 200)
	call("POST", ap, token, decision, 200)
	var sid, newJob string
	must(db.QueryRow(`SELECT source_id::text FROM client_attachments WHERE id=$1`, attachment).Scan(&sid))
	must(db.QueryRow(`SELECT job_id::text FROM preparation_intakes WHERE source_id=$1`, sid).Scan(&newJob))
	for _, job := range append(jobs, newJob) {
		tx, e := db.Begin()
		must(e)
		inventory, _, e := preparation.AgentInventory(ctx, tx, cid, job)
		tx.Rollback()
		must(e)
		found := false
		for _, s := range inventory {
			if s.ID == sid {
				found = true
			}
		}
		if !found {
			t.Fatal("released evidence missing from affected job", job)
		}
	}
	// Existing approved drafts resolve only the accountant-bound required set.
	for n, job := range jobs {
		var paper string
		raw, _ := json.Marshal(map[string]any{"sources": []map[string]string{{"id": sources[n], "digest": preparation.Digest([]byte("Invoice amount: 1200.00\nInvoice: " + []string{"A", "B"}[n]))}}, "blockers": []string{}})
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, cid, "synthetic-"+job, string(raw)).Scan(&paper))
		_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic approved calculation','fixture')`, paper, "synthetic-"+job)
		must(err)
		_, err = db.Exec(`UPDATE preparation_jobs SET state='completed',workpaper_ids=jsonb_build_array($2::text) WHERE id=$1`, job, paper)
		must(err)
	}
	v = view()
	if v.Items[0].State != "resolved" || v.Items[1].State != "resolved" {
		t.Fatal("approved scoped work did not resolve", v.Items)
	}
	// A missing requirement can receive a scoped request before any job exists.
	var noJob struct {
		ID    string `json:"id"`
		Token string `json:"link_token"`
	}
	must(json.Unmarshal(call("POST", path, token, map[string]any{"action": "request", "digest": v.Digest, "accounts": []string{"c"}, "jobs": []string{}, "recipient": "client@example.test", "body": "Please provide missing invoice C.", "confirmed": true}, 200), &noJob))
	call("POST", "/v1/client-request", noJob.Token, map[string]any{"body": "Invoice C will follow.", "confirmed": true}, 200)
	// A request for an account alone keeps taking answers after the first reply, and Coverage says so.
	if !strings.Contains(string(call("GET", "/v1/client-request", noJob.Token, nil, 200)), `"open":true`) {
		t.Fatal("a request for an account alone closed after its first reply")
	}
	call("POST", "/v1/client-request", noJob.Token, map[string]any{"body": "Invoice C is attached now.", "confirmed": true}, 200)
	// A link replaced after the reply keeps taking answers, and Coverage reads the request's replies, not the new link's.
	var replaced struct {
		Token string `json:"link_token"`
	}
	must(json.Unmarshal(call("POST", path, token, map[string]any{"action": "link", "request_id": noJob.ID, "confirmed": true}, 200), &replaced))
	call("POST", "/v1/client-request", replaced.Token, map[string]any{"body": "Invoice C is attached now, again.", "confirmed": true}, 200)
	call("POST", path, token, map[string]any{"action": "link", "request_id": noJob.ID, "confirmed": true}, 200)
	var listed struct {
		Requests []struct {
			Open  bool
			Stale string
		}
	}
	must(json.Unmarshal(call("GET", path, token, nil, 200), &listed))
	if !listed.Requests[0].Open || listed.Requests[0].Stale != "" {
		t.Fatalf("Coverage says the newest request no longer takes answers: %+v", listed.Requests[0])
	}
	replacement := source("replacement.txt", "Changed invoice evidence")
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, sources[0], replacement)
	must(err)
	v = view()
	if v.Items[0].State == "resolved" || v.Items[1].State != "resolved" {
		t.Fatal("source change did not selectively reopen coverage")
	}
	// A reply that arrived by email uses the link without answers; a replacement link then takes follow-ups.
	var emailed struct {
		ID string `json:"id"`
	}
	must(json.Unmarshal(call("POST", path, token, map[string]any{"action": "request", "digest": view().Digest, "accounts": []string{"d"}, "jobs": []string{}, "recipient": "client@example.test", "body": "Please provide missing invoice D.", "confirmed": true}, 200), &emailed))
	_, err = db.Exec(`UPDATE client_request_links SET used_at=now() WHERE request_id=$1`, emailed.ID)
	must(err)
	var replied struct {
		Requests []struct {
			Open  bool
			Stale string
		}
	}
	must(json.Unmarshal(call("GET", path, token, nil, 200), &replied))
	if !replied.Requests[0].Open || replied.Requests[0].Stale != "" {
		t.Fatalf("Coverage misreads a reply that arrived by email: %+v", replied.Requests[0])
	}
	var relinked struct {
		Token string `json:"link_token"`
	}
	must(json.Unmarshal(call("POST", path, token, map[string]any{"action": "link", "request_id": emailed.ID, "confirmed": true}, 200), &relinked))
	call("POST", "/v1/client-request", relinked.Token, map[string]any{"body": "Invoice D follows the email.", "confirmed": true}, 200)
	// Once the firm changes an account's coverage, Coverage says its answered request takes no more answers and its
	// unanswered one can no longer be answered.
	call("POST", path, token, map[string]any{"action": "request", "digest": view().Digest, "accounts": []string{"e"}, "jobs": []string{}, "recipient": "client@example.test", "body": "Please provide missing invoice E.", "confirmed": true}, 200)
	for _, account := range []string{"c", "e"} {
		call("POST", path, token, map[string]any{"action": "bind", "digest": view().Digest, "account_id": account, "jobs": []string{}, "excluded": true, "reason": "Handled outside this close", "confirmed": true}, 200)
	}
	var moved struct {
		Requests []struct {
			Open  bool
			Stale string
		}
	}
	must(json.Unmarshal(call("GET", path, token, nil, 200), &moved))
	if moved.Requests[0].Stale == "" || !moved.Requests[1].Open || moved.Requests[2].Open {
		t.Fatalf("Coverage misreports which requests still take answers: %+v", moved.Requests)
	}
}

func TestClientSubmissionRejectsUnknownFileField(t *testing.T) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("body", "Synthetic reply")
	_ = form.WriteField("confirmed", "true")
	file, _ := form.CreateFormFile("unexpected", "invoice.txt")
	_, _ = file.Write([]byte("Synthetic invoice"))
	_ = form.Close()
	r := httptest.NewRequest("POST", "/v1/client-request", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	if _, _, _, ok := clientSubmission(w, r); ok || w.Code != 400 {
		t.Fatalf("unknown file field accepted: %d", w.Code)
	}
}
