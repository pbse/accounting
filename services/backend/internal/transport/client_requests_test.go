package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestScopedClientReplyResumesJob(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	var closeID, source string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES('Private request fixture','2026-09') RETURNING id::text`).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_jobs", "request_drafts", "workpapers", "evidence", "client_closes"} {
			column := "close_id"
			if table == "client_closes" {
				column = "id"
			}
			if _, e := db.Exec("DELETE FROM "+table+" WHERE "+column+"=$1", closeID); e != nil {
				t.Error(e)
			}
		}
	}()
	text := "Invoice amount: 1200.00\nService start: 2026-09-01\nCurrency: USD"
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(text)), text).Scan(&source); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	job, err := preparation.StartIntake(context.Background(), tx, closeID, source, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	auth := strings.Repeat("s", 32)
	h := modelHandler(t, apiWithStore(db, auth))
	call := func(method, path, token string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + closeID + "/work/" + job
	revision := func() int {
		var n int
		if e := db.QueryRow("SELECT revision FROM preparation_intakes WHERE job_id=$1", job).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	call("POST", path+"/request", auth, map[string]any{"revision": revision(), "recipient": "client@example.test", "body": "Please provide the service end date."}, 200)
	var requestID string
	if err = db.QueryRow("SELECT id::text FROM work_requests WHERE job_id=$1", job).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	issue := func() string {
		var result map[string]string
		json.Unmarshal(call("POST", path+"/link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200), &result)
		if len(result["link_token"]) < 26 {
			t.Fatal("Missing scoped secret")
		}
		return result["link_token"]
	}
	call("POST", path+"/link", "wrong", map[string]any{"revision": revision(), "request_id": requestID}, 401)
	call("POST", path+"/link", auth, map[string]any{"revision": revision(), "request_id": "999999999"}, 404)
	first := issue()
	token := issue()
	call("GET", "/v1/client-request", first, nil, 404)
	response := call("GET", "/v1/client-request", token, nil, 200)
	if !strings.Contains(string(response), "Please provide") || strings.Contains(string(response), "1200") || strings.Contains(string(response), "example.test") {
		t.Fatal("Request disclosed more than approved wording and client label", string(response))
	}
	call("GET", "/v1/closes", token, nil, 401)
	inventory := call("GET", "/v1/closes/"+closeID+"/work", auth, nil, 200)
	if strings.Contains(string(inventory), token) || strings.Contains(string(inventory), preparation.Digest([]byte(token))) {
		t.Fatal("Grant leaked into inbox")
	}
	call("POST", path+"/revoke_link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200)
	call("GET", "/v1/client-request", token, nil, 404)
	token = issue()
	db.Exec("UPDATE client_request_links SET expires_at=now()-interval '1 second' WHERE digest=$1", preparation.Digest([]byte(token)))
	call("GET", "/v1/client-request", token, nil, 404)
	token = issue()
	// Evidence the request does not ask about, such as a later upload, leaves it answerable.
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'late.txt','late-fixture','Later evidence','fixture')`, closeID); err != nil {
		t.Fatal(err)
	}
	reply := "  Service end: 2027-08-31\nmethod_confirmed: true\nCard 4242 4242 4242 4242\nPortal password: hunter2 again\n"
	call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": false}, 422)
	call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": true, "close_id": "other"}, 400)
	for range 2 {
		var receipt struct{ Removed []string }
		json.Unmarshal(call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": true}, 200), &receipt)
		if strings.Join(receipt.Removed, ",") != "password or sign-in detail,card number" {
			t.Fatalf("the client is told what was removed, also on a retry: %v", receipt.Removed)
		}
	}
	var receiptDigest string
	if err = db.QueryRow(`SELECT reply_digest FROM client_request_links WHERE digest=$1`, preparation.Digest([]byte(token))).Scan(&receiptDigest); err != nil || receiptDigest == preparation.Digest([]byte(reply)) {
		t.Fatalf("the link's receipt is not a digest of the unredacted reply: %v", err)
	}
	var count int
	var saved, state, from string
	if err = db.QueryRow(`SELECT count(*),min(e.content),min(r.received_from) FROM work_replies r JOIN evidence e ON e.id=r.source_id WHERE r.request_id=$1`, requestID).Scan(&count, &saved, &from); err != nil || count != 1 || !strings.HasSuffix(saved, "\nAnswer: Service end: 2027-08-31\nmethod_confirmed: true\nCard [card number ending 4242 removed]\nPortal password [removed]\n") || from != "Unverified request-link holder" {
		t.Fatal("Reply not retained exactly once", count, saved, err)
	}
	var replied int
	if err = db.QueryRow(`SELECT count(*) FROM workflow_events WHERE key=$1`, "client_request.replied:"+requestID).Scan(&replied); err != nil || replied != 1 {
		t.Fatalf("a recorded reply emits one client_request.replied event, not one per retry: %d %v", replied, err)
	}
	if err = db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", job).Scan(&state); err != nil || state != "waiting_evidence" {
		t.Fatal("Original job not resumed", state, err)
	}
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", job).Scan(&state)
	if state != "needs_confirmation" {
		t.Fatal("Reply authorized preparation", state)
	}
	if !strings.Contains(string(call("GET", "/v1/client-request", token, nil, 200)), "received") {
		t.Fatal("Receipt unavailable")
	}
	// While the work is open, a later reply on the same link replaces the earlier one, which stays in history.
	call("POST", "/v1/client-request", token, map[string]any{"body": "Changed reply", "confirmed": true}, 200)
	var superseded bool
	if err = db.QueryRow(`SELECT e.superseded_by IS NOT NULL FROM work_replies r JOIN evidence e ON e.id=r.source_id WHERE r.request_id=$1 AND e.content LIKE '%Answer: Service end%'`, requestID).Scan(&superseded); err != nil || !superseded {
		t.Fatal("a correction replaces the earlier reply", err)
	}
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	// Withdrawing an unanswered request also invalidates its link.
	call("POST", path+"/request", auth, map[string]any{"revision": revision(), "recipient": "client@example.test", "body": "Please clarify the account."}, 200)
	db.QueryRow("SELECT max(id)::text FROM work_requests WHERE job_id=$1", job).Scan(&requestID)
	token = issue()
	call("POST", path+"/retry", auth, map[string]any{"revision": revision()}, 200)
	call("GET", "/v1/client-request", token, nil, 404)
	// A request approved from a draft keeps to it: other evidence leaves the request answerable, a review of the draft does not.
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	var paper, draft string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','asked-paper','{"input":{"source_reference":"Policy invoice"},"sources":[]}') RETURNING id::text`, closeID).Scan(&paper); err != nil {
		t.Fatal(err)
	}
	tx, _ = db.Begin()
	digest, err := preparation.RequestContext(context.Background(), tx, closeID, []string{paper})
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	// Wording saved with a trailing newline, as generated drafts were, is approved as saved.
	if err = db.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,E'Please send the policy invoice.\n',jsonb_build_array($2::text),$3,'fixture') RETURNING id::text`, closeID, paper, digest).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	call("POST", path+"/request", auth, map[string]any{"revision": revision(), "recipient": "client@example.test", "body": "Please send the policy invoice.\n", "draft_id": draft}, 200)
	db.QueryRow("SELECT max(id)::text FROM work_requests WHERE job_id=$1", job).Scan(&requestID)
	token = issue()
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'later.txt','later-fixture','Other evidence','fixture')`, closeID); err != nil {
		t.Fatal(err)
	}
	call("GET", "/v1/client-request", token, nil, 200)
	// An approval that recorded no drafts' state, as one made before it was recorded, is not taken as current.
	var pinned string
	db.QueryRow(`SELECT approved_papers::text FROM work_requests WHERE id=$1`, requestID).Scan(&pinned)
	db.Exec(`UPDATE work_requests SET approved_papers=NULL WHERE id=$1`, requestID)
	call("GET", "/v1/client-request", token, nil, 409)
	db.Exec(`UPDATE work_requests SET approved_papers=$2::jsonb WHERE id=$1`, requestID, pinned)
	call("GET", "/v1/client-request", token, nil, 200)
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'asked-paper','changes_requested','Use the renewal invoice','fixture')`, paper); err != nil {
		t.Fatal(err)
	}
	if body := call("GET", "/v1/client-request", token, nil, 409); !strings.Contains(string(body), "changed after it was sent") {
		t.Fatalf("a reviewed draft leaves its request without replies: %s", body)
	}
}

func TestLinkPasscodeGuardsTheRequest(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
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
	var closeID, draft, request string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Passcode "+rand.Text()).Scan(&closeID))
	defer func() {
		db.Exec(`UPDATE organizations SET require_link_passcode=false WHERE id='local-development'`)
		db.Exec(`DELETE FROM organization_policy_changes WHERE organization_id='local-development' AND reason='Synthetic passcode policy'`)
		for _, q := range []string{`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, `DELETE FROM request_drafts WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	tx, err := db.Begin()
	must(err)
	digest, err := preparation.RequestContext(context.Background(), tx, closeID, nil)
	must(err)
	must(tx.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send the September statement.','[]',$2,'fixture') RETURNING id::text`, closeID, digest).Scan(&draft))
	must(tx.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,1,'client@example.test','fixture') RETURNING id::text`, draft).Scan(&request))
	must(tx.Commit())
	h := modelHandler(t, apiWithStore(db, strings.Repeat("p", 32)))
	call := func(method, path, token, passcode string, body any, want int) (out struct{ Code, Message, Status, Body string }) {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		if passcode != "" {
			r.Header.Set("X-Request-Passcode", passcode)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	issue := func() (string, string) {
		t.Helper()
		tx, err := db.Begin()
		must(err)
		token := rand.Text()
		passcode, err := issueLink(context.Background(), tx, request, "fixture", token)
		must(err)
		must(tx.Commit())
		return token, passcode
	}
	open, none := issue()
	if none != "" || call("GET", "/v1/client-request", open, "", nil, 200).Status != "open" {
		t.Fatal("without the firm setting a link opens on possession, with no passcode")
	}
	call("POST", "/v1/closes/organization/policy", strings.Repeat("p", 32), "", map[string]any{"link_passcode": true, "reason": "Synthetic passcode policy"}, 200)
	var review, recorded bool
	must(db.QueryRow(`SELECT independent_review,link_passcode FROM organization_policy_changes WHERE organization_id='local-development' ORDER BY id DESC LIMIT 1`).Scan(&review, &recorded))
	if review || !recorded {
		t.Fatalf("the change records both settings, leaving independent review as it was: %v %v", review, recorded)
	}
	if call("GET", "/v1/client-request", open, "", nil, 200).Status != "open" {
		t.Fatal("a link issued before the setting keeps working as issued")
	}

	token, passcode := issue()
	var stored string
	must(db.QueryRow(`SELECT passcode_digest FROM client_request_links WHERE digest=$1`, preparation.Digest([]byte(token))).Scan(&stored))
	if len(passcode) != 8 || stored == preparation.Digest([]byte(passcode)) || strings.Contains(stored, passcode) {
		t.Fatalf("a new link returns an 8-digit passcode once and stores only a digest salted by the link: %q", passcode)
	}
	if got := call("GET", "/v1/client-request", token, "", nil, 401); got.Code != "PASSCODE_REQUIRED" || got.Body != "" {
		t.Fatalf("without the passcode nothing about the request is shown: %+v", got)
	}
	wrong := "00000000"
	if wrong == passcode {
		wrong = "11111111"
	}
	for i := 1; i <= 4; i++ {
		if got := call("GET", "/v1/client-request", token, wrong, nil, 403); !strings.Contains(got.Message, fmt.Sprint(5-i)+" tries left") {
			t.Fatalf("each wrong try is counted: %+v", got)
		}
	}
	call("POST", "/v1/client-request", token, wrong, map[string]any{"body": "Guess", "confirmed": true}, 404)
	call("GET", "/v1/client-request", token, passcode, nil, 404)

	token, passcode = issue()
	if got := call("GET", "/v1/client-request", token, passcode, nil, 200); got.Status != "open" || got.Body != "Please send the September statement." {
		t.Fatalf("the right passcode opens the request: %+v", got)
	}
	call("POST", "/v1/client-request", token, passcode, map[string]any{"body": "Statement to follow.", "confirmed": true}, 200)
	call("POST", "/v1/client-request/scan-text", token, "", nil, 401)
}

func TestPartialClientRepliesKeepAnswersAndResumeOnlyWhenComplete(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_MODEL", "")
	var closeID, source string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES('Partial reply fixture','2026-09') RETURNING id::text`).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_jobs", "request_drafts", "evidence", "client_closes"} {
			column := "close_id"
			if table == "client_closes" {
				column = "id"
			}
			db.Exec(`UPDATE evidence SET superseded_by=NULL WHERE close_id=$1`, closeID)
			db.Exec("DELETE FROM "+table+" WHERE "+column+"=$1", closeID)
		}
	}()
	text := "Invoice amount: 1200.00\nService start: 2026-09-01\nCurrency: USD"
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(text+"partial")), text).Scan(&source); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	job, err := preparation.StartIntake(context.Background(), tx, closeID, source, "fixture")
	if err != nil || tx.Commit() != nil {
		t.Fatal(err)
	}
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	auth := strings.Repeat("s", 32)
	h := modelHandler(t, apiWithStore(db, auth))
	call := func(method, path, token string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + closeID + "/work/" + job
	revision := func() int {
		var n int
		if e := db.QueryRow("SELECT revision FROM preparation_intakes WHERE job_id=$1", job).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	state := func() (s string) {
		db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", job).Scan(&s)
		return
	}
	call("POST", path+"/request", auth, map[string]any{"revision": revision(), "recipient": "client@example.test", "body": "Please help us finish September:\n\n1. Confirm the service end date.\n2. Send the signed\ncontract.\n\nThank you."}, 200)
	var requestID string
	if err = db.QueryRow("SELECT id::text FROM work_requests WHERE job_id=$1", job).Scan(&requestID); err != nil {
		t.Fatal(err)
	}
	var link map[string]string
	json.Unmarshal(call("POST", path+"/link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200), &link)
	token := link["link_token"]
	type view struct {
		Status    string
		Open      bool
		Questions []requestQuestion
	}
	var got view
	json.Unmarshal(call("GET", "/v1/client-request", token, nil, 200), &got)
	if len(got.Questions) != 2 || got.Questions[1].Text != "Send the signed contract." {
		t.Fatalf("the approved request reads as its numbered questions: %+v", got.Questions)
	}
	answer := func(answers []clientAnswer, want int) view {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"answers": answers})
		var v view
		json.Unmarshal(call("POST", "/v1/client-request", token, map[string]any{"body": string(raw), "confirmed": true}, want), &v)
		return v
	}
	call("POST", path+"/resume", auth, map[string]any{"revision": revision()}, 409)
	call("POST", "/v1/client-request", token, map[string]any{"body": `{"answers":[{"position":1,"state":"answered","answer":"x","extra":1}]}`, "confirmed": true}, 422)
	call("POST", "/v1/client-request", token, map[string]any{"body": "It ends 2027-08-31", "confirmed": true}, 422)
	if len(requestQuestions(strings.Repeat("1. Question\n", 51))) != 1 {
		t.Fatal("a request with more than 50 numbered questions is answered as one, not truncated")
	}
	partial := []clientAnswer{{1, "answered", "It ends 2027-08-31"}, {2, "awaiting_document", "Scanning it tomorrow"}}
	answer([]clientAnswer{{1, "answered", ""}}, 422)
	answer([]clientAnswer{{3, "answered", "Extra"}}, 422)
	answer([]clientAnswer{}, 422)
	got = answer(partial, 200)
	json.Unmarshal(call("POST", path+"/link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200), &link)
	call("GET", "/v1/client-request", token, nil, 404)
	token = link["link_token"]
	answer(partial, 200)
	var submissions int
	var complete bool
	var content string
	db.QueryRow(`SELECT count(*),bool_or(complete) FROM request_submissions WHERE request_id::text=$1`, requestID).Scan(&submissions, &complete)
	db.QueryRow(`SELECT e.content FROM request_submissions s JOIN evidence e ON e.id=s.source_id WHERE s.request_id::text=$1`, requestID).Scan(&content)
	if state() != "waiting_client" || submissions != 1 || complete || got.Questions[1].State != "awaiting_document" || !got.Open || !strings.Contains(content, "Will send the document later. Scanning it tomorrow") {
		t.Fatalf("a partial reply is kept once, lists what is outstanding and waits for the firm: %s %d %v %+v %q", state(), submissions, complete, got, content)
	}
	var inbox struct {
		Items []struct {
			Requests []struct {
				Partial bool
				Answers []struct{ Position int }
			}
		}
	}
	json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/work", auth, nil, 200), &inbox)
	if len(inbox.Items) != 1 || !inbox.Items[0].Requests[0].Partial || len(inbox.Items[0].Requests[0].Answers) != 2 {
		t.Fatalf("Work shows the partial reply's answers: %+v", inbox)
	}
	call("POST", path+"/resume", auth, map[string]any{"revision": revision()}, 200)
	if state() != "waiting_evidence" {
		t.Fatal("the firm can prepare from a partial reply", state())
	}
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	got = answer([]clientAnswer{{2, "answered", "The contract is attached"}}, 200)
	var superseded int
	db.QueryRow(`SELECT count(*) FROM request_submissions s JOIN evidence e ON e.id=s.source_id WHERE s.request_id::text=$1 AND e.superseded_by IS NOT NULL`, requestID).Scan(&superseded)
	db.QueryRow(`SELECT bool_or(complete) FROM request_submissions WHERE request_id::text=$1`, requestID).Scan(&complete)
	if got.Questions[0].State != "answered" || got.Questions[1].State != "answered" || superseded != 1 || !complete || state() != "waiting_evidence" {
		t.Fatalf("a later answer completes the reply, replaces the earlier one and resumes the work: %+v superseded %d %v %s", got.Questions, superseded, complete, state())
	}
	if _, err = preparation.ProcessIntake(modelContext(t, context.Background()), db); err != nil {
		t.Fatal(err)
	}
	correction := []clientAnswer{{1, "answered", "It ends 2027-09-30"}}
	answer(correction, 200)
	answer([]clientAnswer{{1, "answered", "It ends 2027-10-31"}}, 200)
	got = answer(correction, 200)
	db.QueryRow(`SELECT count(*) FROM request_submissions WHERE request_id::text=$1`, requestID).Scan(&submissions)
	if submissions != 5 || got.Questions[0].Answer != "It ends 2027-09-30" {
		t.Fatalf("returning to an earlier answer is saved as a correction, not taken for a retry: %d %+v", submissions, got.Questions)
	}
	call("POST", path+"/cancel", auth, map[string]any{"revision": revision()}, 200)
	answer(correction, 200)
	answer([]clientAnswer{{1, "answered", "Actually it ends 2027-11-30"}}, 409)
	call("POST", path+"/revoke_link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200)
	call("GET", "/v1/client-request", token, nil, 404)
}

// readSpy records whether a handler read the request body at all.
type readSpy struct{ read bool }

func (s *readSpy) Read([]byte) (int, error) { s.read = true; return 0, io.EOF }

func TestUnknownOrRevokedLinkIsRefusedBeforeItsUpload(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID, draft, request string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Unread upload "+rand.Text()).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`, `DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, `DELETE FROM request_drafts WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := preparation.RequestContext(context.Background(), tx, closeID, nil)
	if err == nil {
		err = tx.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send the September statement.','[]',$2,'fixture') RETURNING id::text`, closeID, digest).Scan(&draft)
	}
	if err == nil {
		err = tx.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,1,'client@example.test','fixture') RETURNING id::text`, draft).Scan(&request)
	}
	revoked := rand.Text()
	if err == nil {
		_, err = issueLink(context.Background(), tx, request, "fixture", revoked)
	}
	if err == nil {
		_, err = tx.Exec(`UPDATE client_request_links SET revoked_at=now() WHERE digest=$1`, preparation.Digest([]byte(revoked)))
	}
	if err != nil || tx.Commit() != nil {
		t.Fatal(err)
	}
	h := modelHandler(t, apiWithStore(db, strings.Repeat("p", 32)))
	for _, token := range []string{rand.Text(), revoked} {
		for _, path := range []string{"/v1/client-request", "/v1/client-request/scan-text"} {
			body := &readSpy{}
			r := httptest.NewRequest("POST", path, body)
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 404 || body.read {
				t.Fatalf("%s: %d, body read %v", path, w.Code, body.read)
			}
		}
	}
}
