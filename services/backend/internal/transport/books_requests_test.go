package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// A client asked for the month's books sends its Trial Balance through the request link; scanned whole and released, it
// is one of the client's files for that month.
func TestAClientSendsTheMonthsBooksThroughARequest(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	entity := "Books request synthetic " + rand.Text()
	var cid string
	var imports bool
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2027-04') RETURNING id::text`, entity).Scan(&cid))
	must(db.QueryRow(`SELECT file_imports FROM organizations WHERE id='local-development'`).Scan(&imports))
	_, err = db.Exec(`UPDATE organizations SET file_imports=true WHERE id='local-development'`)
	must(err)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM client_attachments WHERE close_id=$1::bigint`, `DELETE FROM books_imports WHERE client_entity=(SELECT entity FROM client_closes WHERE id=$1::bigint)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1::bigint)`, `DELETE FROM request_drafts WHERE close_id=$1::bigint`, `DELETE FROM evidence WHERE close_id=$1::bigint`, `DELETE FROM client_closes WHERE id=$1::bigint`} {
			if _, e := db.Exec(q, cid); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
		if _, e := db.Exec(`UPDATE organizations SET file_imports=$1 WHERE id='local-development'`, imports); e != nil {
			t.Error(e)
		}
	})
	token := strings.Repeat("k", 32)
	handler := apiWithStore(db, token)
	send := func(method, path, bearer, contentType string, body []byte, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	call := func(method, path, bearer string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		return send(method, path, bearer, "application/json", raw, want)
	}
	ask := "/v1/closes/client/books-requests?name=" + url.QueryEscape(entity)
	call("POST", ask, token, map[string]any{"period": "2027-05", "recipient": "owner@example.com", "confirmed": true}, 409)
	asked := call("POST", ask, token, map[string]any{"period": "2027-04", "recipient": "owner@example.com", "confirmed": true}, 201)
	link := asked["link_token"].(string)
	if opened := call("GET", "/v1/client-request", link, nil, 200); opened["books"] != true || !strings.Contains(opened["body"].(string), "books for April 2027") {
		t.Fatalf("the client sees a request for the month's books: %v", opened)
	}
	reply := func(name, content string, want int) {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		must(m.WriteField("body", ""))
		must(m.WriteField("confirmed", "true"))
		f, e := m.CreateFormFile("files", name)
		must(e)
		_, e = f.Write([]byte(content))
		must(e)
		must(m.Close())
		send("POST", "/v1/client-request", link, m.FormDataContentType(), b.Bytes(), want)
	}
	// A document is not the month's books.
	reply("statement.pdf", "%PDF-1.4\nSynthetic\n%%EOF", 422)
	trialBalance := "Account,Debit,Credit\nBusiness Checking,1500.00,\nOwner's Equity,,1500.00\n"
	reply("April trial balance.csv", trialBalance, 200)
	// The file answers the request: the reply is complete.
	var complete bool
	must(db.QueryRow(`SELECT complete FROM request_submissions WHERE request_id=$1::bigint ORDER BY id DESC LIMIT 1`, asked["id"]).Scan(&complete))
	if !complete {
		t.Fatal("a books file alone completes the request for the month's books")
	}
	// Scanned whole by the antivirus service's binary endpoint.
	scanner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/scan-file" {
			http.Error(w, "wrong endpoint", 404)
			return
		}
		jsonReply(w, 200, map[string]string{"status": "clean", "version": "synthetic test engine"})
	}))
	defer scanner.Close()
	t.Setenv("TEXT_SCANNER_URL", scanner.URL+"/scan")
	t.Setenv("UPLOAD_SCAN_KEY", strings.Repeat("s", 32))
	send("POST", "/v1/client-request/scan-file", link, "application/octet-stream", []byte(trialBalance), 200)
	var attachment string
	must(db.QueryRow(`SELECT id::text FROM client_attachments WHERE close_id=$1::bigint AND state='clean'`, cid).Scan(&attachment))
	released := call("POST", "/v1/closes/"+cid+"/attachments/"+attachment, token, map[string]any{"action": "release", "reason": "The client's April trial balance", "confirmed": true}, 200)
	var period, name, kept string
	must(db.QueryRow(`SELECT i.period,i.name,a.import_id::text FROM client_attachments a JOIN books_imports i ON i.id=a.import_id WHERE a.id=$1::bigint AND a.state='released' AND a.source_id IS NULL`, attachment).Scan(&period, &name, &kept))
	if period != "2027-04" || name != "April trial balance.csv" || kept != released["import_id"] {
		t.Fatalf("the released file is the client's April file: %s %s %s %v", period, name, kept, released)
	}
	// Deleting the file removes the attachment's bytes too; the attachment keeps its request, scan and release.
	call("POST", "/v1/closes/client/import/delete?name="+url.QueryEscape(entity)+"&id="+kept, token, map[string]any{"reason": "The client asked us to remove it"}, 200)
	var state string
	var emptied bool
	must(db.QueryRow(`SELECT state,body IS NULL AND import_id IS NULL FROM client_attachments WHERE id=$1::bigint`, attachment).Scan(&state, &emptied))
	if state != "released" || !emptied {
		t.Fatalf("a deleted file's attachment keeps its record without its bytes: %s %v", state, emptied)
	}
}
