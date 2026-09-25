package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
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
		for _, table := range []string{"preparation_jobs", "request_drafts", "evidence", "client_closes"} {
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
	if _, err = preparation.ProcessIntake(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	auth := strings.Repeat("s", 32)
	h := apiWithStore(db, auth)
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
	var extra string
	db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'late.txt','late-fixture','Later evidence','fixture') RETURNING id::text`, closeID).Scan(&extra)
	call("POST", "/v1/client-request", token, map[string]any{"body": "Stale reply", "confirmed": true}, 409)
	db.Exec("DELETE FROM evidence WHERE id=$1", extra)
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
	call("POST", "/v1/client-request", token, map[string]any{"body": "Changed reply", "confirmed": true}, 409)
	var count int
	var saved, state, from string
	if err = db.QueryRow(`SELECT count(*),min(e.content),min(r.received_from) FROM work_replies r JOIN evidence e ON e.id=r.source_id WHERE r.request_id=$1`, requestID).Scan(&count, &saved, &from); err != nil || count != 1 || saved != "  Service end: 2027-08-31\nmethod_confirmed: true\nCard [card number ending 4242 removed]\nPortal password [removed]\n" || from != "Unverified request-link holder" {
		t.Fatal("Reply not retained exactly once", count, saved, err)
	}
	if err = db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", job).Scan(&state); err != nil || state != "waiting_evidence" {
		t.Fatal("Original job not resumed", state, err)
	}
	if _, err = preparation.ProcessIntake(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", job).Scan(&state)
	if state != "needs_confirmation" {
		t.Fatal("Reply authorized preparation", state)
	}
	if !strings.Contains(string(call("GET", "/v1/client-request", token, nil, 200)), "received") {
		t.Fatal("Receipt unavailable")
	}
	// Withdrawing an unanswered request also invalidates its link.
	call("POST", path+"/request", auth, map[string]any{"revision": revision(), "recipient": "client@example.test", "body": "Please clarify the account."}, 200)
	db.QueryRow("SELECT max(id)::text FROM work_requests WHERE job_id=$1", job).Scan(&requestID)
	token = issue()
	call("POST", path+"/retry", auth, map[string]any{"revision": revision()}, 200)
	call("GET", "/v1/client-request", token, nil, 404)
}

func TestRedactSensitiveRemovesCardsBankNumbersAndSecrets(t *testing.T) {
	for _, c := range []struct{ in, out, kinds string }{
		{"Card 4111-1111-1111-1111 exp 12/29", "Card [card number ending 1111 removed] exp 12/29", "card number"},
		{"Invoice 4111111111111112 attached", "Invoice 4111111111111112 attached", ""},
		{"Wire to GB82 WEST 1234 5698 7654 32 please", "Wire to [IBAN ending 5432 removed] please", "IBAN"},
		{"Ref GB82 WEST 1234 5698 7654 33", "Ref GB82 WEST 1234 5698 7654 33", ""},
		{"iban gb82 west 1234 5698 7654 32 okay thanks", "iban [IBAN ending 5432 removed] okay thanks", "IBAN"},
		{"SSN 123-45-6789", "SSN [SSN ending 6789 removed]", "SSN"},
		{"Account number: 000123456789, routing 021000021", "Account number: [bank account number ending 6789 removed], routing 021000021", "bank account number"},
		{"Book to account 400100 and account 6010", "Book to account 400100 and account 6010", ""},
		{"Online banking login is alice; one-time code is 123456\nThanks", "Online banking login [removed]\nThanks", "password or sign-in detail"},
		{"PIN = 4455", "PIN [removed]", "password or sign-in detail"},
		{"Service end: 2027-08-31", "Service end: 2027-08-31", ""},
	} {
		got, kinds := redactSensitive(c.in)
		if got != c.out || strings.Join(kinds, ",") != c.kinds {
			t.Errorf("%q → %q %v", c.in, got, kinds)
		}
		if again, more := redactSensitive(got); again != got || len(more) != 0 {
			t.Errorf("redacted text is stable: %q → %q", got, again)
		}
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
	digest, _, err := preparation.RequestContext(context.Background(), tx, closeID, nil)
	must(err)
	must(tx.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send the September statement.','[]',$2,'fixture') RETURNING id::text`, closeID, digest).Scan(&draft))
	must(tx.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,1,'client@example.test','fixture') RETURNING id::text`, draft).Scan(&request))
	must(tx.Commit())
	h := apiWithStore(db, strings.Repeat("p", 32))
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
