package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// A month whose books are its files reads its open items from the export of them a reviewer sends: the layout
// proposed from its headings, confirmed, gives the month's aging and open items, and a decision about a document.
func TestAFilesMonthsOpenItemsComeFromAnExport(t *testing.T) {
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
	entity := "Open items synthetic " + rand.Text()
	var cid string
	var imports bool
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2027-01') RETURNING id::text`, entity).Scan(&cid))
	must(db.QueryRow(`SELECT file_imports FROM organizations WHERE id='local-development'`).Scan(&imports))
	_, err = db.Exec(`UPDATE organizations SET file_imports=true WHERE id='local-development'`)
	must(err)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM open_item_actions WHERE client_entity=$1`, `DELETE FROM books_sources WHERE client_entity=$1`, `DELETE FROM client_closes WHERE entity=$1`} {
			if _, e := db.Exec(q, entity); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
		if _, e := db.Exec(`UPDATE organizations SET file_imports=$1 WHERE id='local-development'`, imports); e != nil {
			t.Error(e)
		}
	})
	token := strings.Repeat("k", 32)
	handler := apiWithStore(db, token)
	send := func(method, path, contentType string, body []byte, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
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
	aging := "A/R Aging Detail\nAs of January 31, 2027\n\n,Date,Transaction Type,Num,Customer,Due Date,Amount,Open Balance\n" +
		",01/15/2027,Invoice,1001,Acme Co,02/14/2027,500.00,500.00\n,12/10/2026,Invoice,998,Acme Co,01/09/2027,300.00,300.00\nTOTAL,,,,,,800.00,800.00\n"
	upload := func(layout string, confirmed bool, want int) map[string]any {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		must(m.WriteField("side", "receivable"))
		must(m.WriteField("as_of", "2027-01-31"))
		if layout != "" {
			must(m.WriteField("layout", layout))
		}
		if confirmed {
			must(m.WriteField("confirmed", "true"))
		}
		f, e := m.CreateFormFile("file", "January receivables.csv")
		must(e)
		_, e = f.Write([]byte(aging))
		must(e)
		must(m.Close())
		return send("POST", "/v1/closes/"+cid+"/open-item-files", m.FormDataContentType(), b.Bytes(), want)
	}
	// A month whose books are not its files takes none.
	upload("", false, 200)
	proposed := upload("", false, 200)
	layout, _ := json.Marshal(proposed["layout"])
	upload(string(layout), true, 409)
	_, err = db.Exec(`INSERT INTO books_sources(organization_id,client_entity,period,source,reason,actor) VALUES('local-development',$1,'2027-01','files','Synthetic files month','fixture')`, entity)
	must(err)
	if proposed["documents"] != 2.0 || proposed["total"] != "800.00" {
		t.Fatalf("the proposed reading of the export: %v", proposed)
	}
	kept := upload(string(layout), true, 201)
	if kept["id"] == nil {
		t.Fatalf("the export is kept: %v", kept)
	}
	// Its documents are the month's open receivables, aged against the month's end.
	items := send("GET", "/v1/closes/"+cid+"/open-items", "", nil, 200)
	receivables, _ := items["receivables"].([]any)
	if items["source"] != "files" || len(receivables) != 2 || items["captured_at"] != "2027-01-31T23:59:59Z" {
		t.Fatalf("the month's open items from the export: %v", items)
	}
	// A decision about one of them is kept by document.
	overdue := receivables[0].(map[string]any)
	for _, r := range receivables {
		if r.(map[string]any)["label"] != nil && strings.Contains(r.(map[string]any)["label"].(string), "998") {
			overdue = r.(map[string]any)
		}
	}
	raw, _ := json.Marshal(map[string]any{"kind": "Invoice", "item_id": overdue["id"], "capture_id": items["capture_id"], "status": "promised", "expected_on": "2027-02-10", "note": "Promised by phone"})
	send("POST", "/v1/closes/"+cid+"/open-items", "application/json", raw, 200)
	var status string
	must(db.QueryRow(`SELECT status FROM open_item_actions WHERE client_entity=$1 AND source='files' AND item_id=$2`, entity, overdue["id"]).Scan(&status))
	if status != "promised" {
		t.Fatalf("the decision about the document: %s", status)
	}
}
