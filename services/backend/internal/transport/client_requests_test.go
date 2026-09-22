package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
	reply := "  Service end: 2027-08-31\nmethod_confirmed: true\n"
	call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": false}, 422)
	call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": true, "close_id": "other"}, 400)
	call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": true}, 200)
	call("POST", "/v1/client-request", token, map[string]any{"body": reply, "confirmed": true}, 200)
	call("POST", "/v1/client-request", token, map[string]any{"body": "Changed reply", "confirmed": true}, 409)
	var count int
	var saved, state, from string
	if err = db.QueryRow(`SELECT count(*),min(e.content),min(r.received_from) FROM work_replies r JOIN evidence e ON e.id=r.source_id WHERE r.request_id=$1`, requestID).Scan(&count, &saved, &from); err != nil || count != 1 || saved != reply || from != "Unverified request-link holder" {
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
