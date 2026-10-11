package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// A client's books arrive as a file for a month, behind the firm's file-imports setting: the reader proposes a layout, a
// person previews the reading, a reviewer confirms the layout, and the next month's file in the same layout is read with
// it again.
func TestAClientsBooksArriveAsFiles(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
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
	t.Setenv("WORKOS_CLIENT_ID", "")
	org := "org_Files" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "preparer", "user_c": "reviewer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Files fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b','user_c']) s`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_c','Acme Trading Ltd',true,'Synthetic','fixture')`,
		`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Acme Co',true,'2026-08','Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM books_import_deletions WHERE organization_id=$1`, `DELETE FROM books_layouts WHERE organization_id=$1`, `DELETE FROM books_imports WHERE organization_id=$1`, `DELETE FROM platform_audit WHERE organization_id=$1`,
			`DELETE FROM client_roster WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`,
			`DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	partner := []string{"workpapers:review", "clients:all"}
	preparer := []string{"clients:all"}
	send := func(subject string, permissions []string, method, path, contentType string, body []byte, want int) map[string]any {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %s: %d %s", subject, method, path, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	call := func(subject string, permissions []string, method, path string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		return send(subject, permissions, method, path, "application/json", raw, want)
	}
	oneClient := "true"
	replaces, why := "", ""
	upload := func(subject string, permissions []string, name, period, content string, want int) map[string]any {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		must(m.WriteField("period", period))
		must(m.WriteField("one_client", oneClient))
		if replaces != "" {
			must(m.WriteField("replaces", replaces))
			must(m.WriteField("replaces_reason", why))
		}
		f, e := m.CreateFormFile("file", name)
		must(e)
		_, e = f.Write([]byte(content))
		must(e)
		must(m.Close())
		return send(subject, permissions, "POST", "/v1/closes/client/imports?name="+url.QueryEscape("Acme Co"), m.FormDataContentType(), b.Bytes(), want)
	}
	september := "Account,Debit,Credit\nBank,1500.25,\nCapital,,1000.00\nSales,,500.25\nTOTAL,1500.25,1500.25\n"

	// Off for the firm until staff switch it on: nothing can be sent, and the list says it is off.
	if off := call("user_a", partner, "GET", "/v1/closes/client/imports?name=Acme%20Co", nil, 200); off["on"] != false || off["can_confirm"] != false {
		t.Fatalf("the list with file imports off: %v", off)
	}
	upload("user_a", partner, "tb.csv", "2026-09", september, 403)
	_, err = db.Exec(`UPDATE organizations SET file_imports=true WHERE id=$1`, org)
	must(err)

	// Any member who sees the client whole sends a file for a month; one assigned to an alias alone does not.
	upload("user_b", preparer, "tb.csv", "2026-13", september, 422)
	upload("user_b", preparer, "statement.pdf", "2026-09", "%PDF-1.7", 422)
	// The sender confirms the file holds only this client's books: one holding several waits for their split.
	oneClient = ""
	upload("user_b", preparer, "Acme TB September.csv", "2026-09", september, 422)
	oneClient = "true"
	id := upload("user_b", preparer, "Acme TB September.csv", "2026-09", september, 201)["id"].(string)
	_, err = db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Trading Ltd','2026-07')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Acme Co','acme trading ltd','fixture',now())`, org)
	must(err)
	call("user_c", []string{"workpapers:review"}, "GET", "/v1/closes/client/imports?name=Acme%20Co", nil, 404)

	// The reader proposes a layout from the file's own header; the preview reads it, balanced, the TOTAL row checked.
	path := "/v1/closes/client/import?name=" + url.QueryEscape("Acme Co") + "&id=" + id
	read := call("user_b", preparer, "GET", path, nil, 200)
	proposed := read["proposal"].(map[string]any)
	layout := proposed["layout"].(map[string]any)
	if layout["header_row"] != 1.0 || proposed["reused"] != false || len(read["tables"].([]any)) != 1 {
		t.Fatalf("the proposal: %v", read)
	}
	layout["as_of"], layout["currency"] = "2026-09-30", "USD"
	preview := call("user_b", preparer, "POST", path, map[string]any{"action": "preview", "layout": layout}, 200)["reading"].(map[string]any)
	if preview["balanced"] != true || preview["debits"] != "1500.25" || len(preview["problems"].([]any)) != 0 {
		t.Fatalf("the preview: %v", preview)
	}
	// A preview's lines come two hundred at a time, from the one asked for, with how many there are.
	paged := call("user_b", preparer, "POST", path, map[string]any{"action": "preview", "layout": layout, "lines_from": 2}, 200)
	if len(paged["reading"].(map[string]any)["lines"].([]any)) != 2 || paged["line_count"] != 4.0 || paged["accounts"] != 3.0 {
		t.Fatalf("a preview's lines from the third: %v", paged)
	}
	// Further lines come from the preview kept when it was read, without reading the file again.
	_, err = db.Exec(`UPDATE books_imports SET content='not the file' WHERE id::text=$1`, id)
	must(err)
	kept := call("user_b", preparer, "POST", path, map[string]any{"action": "preview", "layout": layout, "lines_from": 3}, 200)
	if lines := kept["reading"].(map[string]any)["lines"].([]any); len(lines) != 1 || kept["line_count"] != 4.0 || kept["reading"].(map[string]any)["balanced"] != true {
		t.Fatalf("a kept preview's last line: %v", kept)
	}
	_, err = db.Exec(`UPDATE books_imports SET content=$2 WHERE id::text=$1`, id, september)
	must(err)
	// An encoding the reader does not know, or one without its delimiter, is refused.
	call("user_b", preparer, "GET", path+"&encoding=latin-9&delimiter=comma", nil, 422)
	call("user_b", preparer, "GET", path+"&encoding=utf-8", nil, 422)
	// A reviewer confirms it with a reason; a preparer cannot.
	call("user_b", preparer, "POST", path, map[string]any{"action": "confirm", "layout": layout, "reason": "Matches the export"}, 403)
	call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "layout": layout, "reason": ""}, 422)
	bad := map[string]any{}
	for k, v := range layout {
		bad[k] = v
	}
	bad["style"] = map[string]any{"decimal_comma": false, "scale": 7}
	call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "layout": bad, "reason": "Wrong scale"}, 422)
	// A confirmed reading is as of a date in the import's month, in a currency.
	for field, wrong := range map[string]string{"as_of": "2026-08-31", "currency": ""} {
		off := map[string]any{}
		for k, v := range layout {
			off[k] = v
		}
		off[field] = wrong
		call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "layout": off, "reason": "Wrong " + field}, 422)
	}
	// A CSV can be read another way a person chooses: by semicolons, this file is one column.
	if rows := call("user_b", preparer, "GET", path+"&encoding=utf-8&delimiter=semicolon", nil, 200)["tables"].([]any)[0].(map[string]any)["rows"].([]any); len(rows[1].(map[string]any)["cells"].([]any)) != 1 {
		t.Fatalf("read by semicolons: %v", rows[1])
	}
	call("user_b", preparer, "GET", path+"&encoding=latin-9&delimiter=comma", nil, 422)
	call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "layout": layout, "reason": "Matches the export"}, 200)
	// Reopened, a confirmed file is read from what was kept the first time it was read so: its rows, what it was read
	// as and its confirmed reading, without reading the file again.
	call("user_a", partner, "GET", path, nil, 200)
	_, err = db.Exec(`UPDATE books_imports SET content='not the file' WHERE id::text=$1`, id)
	must(err)
	reopened := call("user_a", partner, "GET", path, nil, 200)
	if rows := reopened["tables"].([]any)[0].(map[string]any)["rows"].([]any); len(rows) != 5 || reopened["import"].(map[string]any)["confirmed"] == nil || reopened["encoding"] != "utf-8" {
		t.Fatalf("a confirmed file reopened from what was kept: %v", reopened)
	}
	_, err = db.Exec(`UPDATE books_imports SET content=$2 WHERE id::text=$1`, id, september)
	must(err)
	// The file keeps one copy of its rows, however often it is read another way.
	var parses int
	must(db.QueryRow(`SELECT count(DISTINCT parse) FROM books_import_pages WHERE import_id::text=$1`, id).Scan(&parses))
	if parses != 1 {
		t.Fatalf("one copy of the file's rows: %d", parses)
	}
	listed := call("user_a", partner, "GET", "/v1/closes/client/imports?name=Acme%20Co", nil, 200)["imports"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["confirmed"].(map[string]any)["balanced"] != true {
		t.Fatalf("the list: %v", listed)
	}

	// October's file in the same layout is proposed with September's confirmed layout; one with other headers is not.
	october := upload("user_b", preparer, "Acme TB October.csv", "2026-10", "Account,Debit,Credit\nBank,1700.00,\nCapital,,1000.00\nSales,,700.00\n", 201)["id"].(string)
	again := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+october, nil, 200)["proposal"].(map[string]any)
	if again["reused"] != true || again["layout"].(map[string]any)["currency"] != "USD" || again["layout"].(map[string]any)["as_of"] != "" {
		t.Fatalf("the confirmed layout is proposed again, its date asked afresh: %v", again)
	}
	other := upload("user_b", preparer, "Acme TB other.csv", "2026-10", "Account,Balance\nBank,1700.00\nCapital,-1700.00\n", 201)["id"].(string)
	if fresh := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+other, nil, 200)["proposal"].(map[string]any); fresh["reused"] != false {
		t.Fatalf("other headers ask afresh: %v", fresh)
	}
	call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id=999999999", nil, 404)

	// Headers the reader does not know, confirmed by a person, are proposed again when the next file has them.
	custom := "Konto,Soll,Haben\nKasse,100.00,\nKapital,,100.00\n"
	first := upload("user_b", preparer, "Konten Juli.csv", "2026-07", custom, 201)["id"].(string)
	table := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+first, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
	manual := map[string]any{"table": table, "header_row": 1, "columns": map[string]string{"0": "account_name", "1": "debit", "2": "credit"},
		"style": map[string]any{"decimal_comma": false, "scale": 1}, "as_of": "2026-07-31", "currency": "USD"}
	call("user_a", partner, "POST", "/v1/closes/client/import?name=Acme%20Co&id="+first, map[string]any{"action": "confirm", "layout": manual, "reason": "Headers named by hand"}, 200)
	next := upload("user_b", preparer, "Konten August.csv", "2026-08", custom, 201)["id"].(string)
	if p := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+next, nil, 200)["proposal"].(map[string]any); p["reused"] != true {
		t.Fatalf("a layout confirmed by hand is proposed again: %v", p)
	}

	// A long file's rows come in pages, so a row past the first page can be seen and marked.
	long := "Account,Debit,Credit\n"
	for i := 1; i <= 249; i++ {
		long += fmt.Sprintf("Account %d,1.00,\n", i)
	}
	big := upload("user_b", preparer, "Long.csv", "2026-06", long, 201)["id"].(string)
	page := func(from string) map[string]any {
		t.Helper()
		return call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+big+from, nil, 200)["tables"].([]any)[0].(map[string]any)
	}
	if first := page(""); len(first["rows"].([]any)) != 200 || first["more"] != 50.0 {
		t.Fatalf("the first page: %d rows, %v more", len(first["rows"].([]any)), first["more"])
	}
	// The first read keeps the rows in chunks, and later pages are read from them.
	var chunks int
	must(db.QueryRow(`SELECT count(*) FROM books_import_pages WHERE import_id::text=$1`, big).Scan(&chunks))
	if chunks != 2 {
		t.Fatalf("the long file's rows are kept in two chunks: %d", chunks)
	}
	if later := page("&from=240"); len(later["rows"].([]any)) != 11 || later["rows"].([]any)[0].(map[string]any)["number"] != 240.0 || later["more"] != 0.0 {
		t.Fatalf("rows from 240: %v", later)
	}
	call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+big+"&from=0", nil, 422)

	// A file downloads as it was sent, and the firm's export lists each with where it downloads.
	r := httptest.NewRequest("GET", "/v1/closes/client/import/file?name=Acme%20Co&id="+id, nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: partner}))
	w := httptest.NewRecorder()
	store.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != september || !strings.Contains(w.Header().Get("Content-Disposition"), "Acme TB September.csv") {
		t.Fatalf("the file as sent: %d %q %s", w.Code, w.Body.String(), w.Header())
	}
	exported := call("user_a", []string{"clients:all", "data:manage", "workpapers:review"}, "GET", "/v1/closes/firm-export", nil, 200)["records"].(map[string]any)
	files := exported["books_imports"].([]any)
	layouts := exported["books_layouts"].([]any)
	if len(files) == 0 || files[0].(map[string]any)["download"] == nil || files[0].(map[string]any)["content"] != nil || len(layouts) == 0 || layouts[0].(map[string]any)["reading"] == nil {
		t.Fatalf("the export's files and layouts: %v %v", files, layouts)
	}
	// Each layout's reading downloads on its own.
	if version := call("user_a", partner, "GET", layouts[0].(map[string]any)["reading"].(string), nil, 200); version["reading"] == nil {
		t.Fatalf("a layout's reading: %v", version)
	}
	// Switched off again, the firm's files stay readable, not confirmable.
	_, err = db.Exec(`UPDATE organizations SET file_imports=false WHERE id=$1`, org)
	must(err)
	if read := call("user_a", partner, "GET", path, nil, 200); read["on"] != false || read["can_confirm"] != false {
		t.Fatalf("a file read with imports off: %v", read["on"])
	}
	call("user_a", partner, "POST", path, map[string]any{"action": "preview", "layout": layout}, 403)
	_, err = db.Exec(`UPDATE organizations SET file_imports=true WHERE id=$1`, org)
	must(err)
	// The list comes in pages, saying when there are more.
	all := call("user_a", partner, "GET", "/v1/closes/client/imports?name=Acme%20Co", nil, 200)
	rest := call("user_a", partner, "GET", "/v1/closes/client/imports?name=Acme%20Co&offset=1", nil, 200)
	if all["more"] != false || len(rest["imports"].([]any)) != len(all["imports"].([]any))-1 {
		t.Fatalf("the list's pages: %v %v", all["more"], len(rest["imports"].([]any)))
	}

	// A column left out keeps its header: a next file with something else there is asked about afresh.
	memo := upload("user_b", preparer, "Memo May.csv", "2026-05", "Account,Debit,Credit,Memo\nCash,100.00,,petty\nCapital,,100.00,\n", 201)["id"].(string)
	memoTable := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+memo, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
	withMemo := map[string]any{"table": memoTable, "header_row": 1, "columns": map[string]string{"0": "account_name", "1": "debit", "2": "credit"}, "ignored_columns": []int{3},
		"style": map[string]any{"decimal_comma": false, "scale": 1}, "as_of": "2026-05-31", "currency": "USD"}
	call("user_a", partner, "POST", "/v1/closes/client/import?name=Acme%20Co&id="+memo, map[string]any{"action": "confirm", "layout": withMemo, "reason": "Memo left out"}, 200)
	changed := upload("user_b", preparer, "Memo June.csv", "2026-06", "Account,Debit,Credit,Adjustment\nCash,100.00,,5.00\nCapital,,100.00,\n", 201)["id"].(string)
	if p := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+changed, nil, 200)["proposal"].(map[string]any); p["reused"] != false {
		t.Fatalf("a left-out column now headed otherwise is asked about: %v", p)
	}

	// A replacement names one of the client's files; one that names none is refused.
	replaces, why = "999999999", "A file that is not the client's"
	upload("user_b", preparer, "Acme TB September again.csv", "2026-09", september, 422)
	replaces, why = "", ""
	// The same file again for the month is the one kept, not another import.
	if again := upload("user_b", preparer, "Acme TB September.csv", "2026-09", september, 200); again["id"] != id || again["repeat"] != true {
		t.Fatalf("a repeat upload: %v", again)
	}
	replaces = id
	upload("user_b", preparer, "Acme TB September corrected.csv", "2026-09", september+"Suspense,0.00,\n", 422)
	why = "The first export missed the suspense account"
	upload("user_b", preparer, "Acme TB September corrected.csv", "2026-09", september+"Suspense,0.00,\n", 201)
	replaces, why = "", ""

	// A confirmed reading comes without its lines, which come in pages of their own.
	opened := call("user_a", partner, "GET", path, nil, 200)["import"].(map[string]any)["confirmed"].(map[string]any)
	count := opened["line_count"].(float64)
	if len(opened["reading"].(map[string]any)["lines"].([]any)) != 0 || count == 0 || opened["accounts"] != 3.0 {
		t.Fatalf("the reading's lines come apart: %v", opened)
	}
	if lines := call("user_a", partner, "GET", "/v1/closes/client/import/lines?name=Acme%20Co&id="+id, nil, 200); float64(len(lines["lines"].([]any))) != count {
		t.Fatalf("the reading's lines: %v", lines)
	}
	// A file is deleted by a member who manages the firm's data, with a reason, leaving a receipt; one a later file
	// replaces waits for that one.
	manager := []string{"workpapers:review", "clients:all", "data:manage"}
	call("user_b", preparer, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+changed, map[string]any{"reason": "Sent in error"}, 403)
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+changed, map[string]any{"reason": ""}, 422)
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+id, map[string]any{"reason": "Superseded"}, 409)
	// A file of a month whose close is under legal hold is kept with it.
	var june string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,legal_hold) VALUES($1,'Acme Co','2026-06',true) RETURNING id::text`, org).Scan(&june))
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+changed, map[string]any{"reason": "Sent in error"}, 409)
	_, err = db.Exec(`UPDATE client_closes SET legal_hold=false WHERE id::text=$1`, june)
	must(err)
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+changed, map[string]any{"reason": "Sent in error"}, 200)
	call("user_a", partner, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+changed, nil, 404)
	var receipts int
	must(db.QueryRow(`SELECT count(*) FROM books_import_deletions WHERE organization_id=$1 AND reason='Sent in error'`, org).Scan(&receipts))
	if receipts != 1 {
		t.Fatalf("a deleted file leaves a receipt: %d", receipts)
	}

	// Another client's file, its rows kept under the key Acme's own requests would use, is never read through Acme.
	var otherClient, acmeKey string
	must(db.QueryRow(`INSERT INTO books_imports(organization_id,client_entity,period,name,digest,content,format,actor) VALUES($1,'Other Co','2026-09','other.csv',md5(random()::text)||md5(random()::text),'x','csv','fixture') RETURNING id::text`, org).Scan(&otherClient))
	must(db.QueryRow(`SELECT 'reader|'||encoding||'|'||delimiter FROM books_layouts WHERE organization_id=$1 AND lower(client_entity)='acme co' ORDER BY id DESC LIMIT 1`, org).Scan(&acmeKey))
	_, err = db.Exec(`INSERT INTO books_import_pages(organization_id,import_id,parse,position,table_name,hidden,hidden_columns,total,chunk,first_row,rows) VALUES($1,$2::bigint,$3,0,'other.csv',false,'[]',2,0,1,'[{"number":2,"cells":[{"column":0,"ref":"R2C1","kind":"text","value":"Secret"}]}]')`, org, otherClient, acmeKey)
	must(err)
	call("user_a", partner, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+otherClient+"&from=2", nil, 404)

	// A column read under a blank header keeps it blank: a later file with a header there is asked about afresh.
	blank := upload("user_b", preparer, "Blank May.csv", "2026-05", "Account,,Credit\nCash,100.00,\nCapital,,100.00\n", 201)["id"].(string)
	blankTable := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+blank, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
	call("user_a", partner, "POST", "/v1/closes/client/import?name=Acme%20Co&id="+blank, map[string]any{"action": "confirm", "reason": "Debit column unheaded", "layout": map[string]any{"table": blankTable, "header_row": 1,
		"columns": map[string]string{"0": "account_name", "1": "debit", "2": "credit"}, "style": map[string]any{"decimal_comma": false, "scale": 1}, "as_of": "2026-05-31", "currency": "USD"}}, 200)
	headed := upload("user_b", preparer, "Headed June.csv", "2026-06", "Account,Memo,Credit\nCash,100.00,\nCapital,,100.00\n", 201)["id"].(string)
	if p := call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+headed, nil, 200)["proposal"].(map[string]any); p["reused"] != false {
		t.Fatalf("a header where there was none is asked about: %v", p)
	}
}

// A CSV choice is both an encoding and a delimiter the reader knows, and only for a CSV; the rows kept are keyed by what
// decides how the file is read, not by which layout said so.
func TestACSVChoiceIsCheckedAndKeysTheRowsKept(t *testing.T) {
	utf8Comma := csvChoice{Encoding: "utf-8", Delimiter: "comma"}
	if askedChoice("csv", utf8Comma) != "" || askedChoice("xlsx", csvChoice{}) != "" || askedChoice("xlsx", utf8Comma) == "" ||
		askedChoice("csv", csvChoice{Encoding: "utf-8"}) == "" || askedChoice("csv", csvChoice{Encoding: "latin-9", Delimiter: "comma"}) == "" {
		t.Fatal("a choice is both parts, known, for a CSV")
	}
	if parseKey("xlsx", utf8Comma, &confirmedRead{ID: "1"}, nil) != "workbook" ||
		parseKey("csv", csvChoice{}, &confirmedRead{ID: "1", CSV: utf8Comma}, nil) != parseKey("csv", csvChoice{}, &confirmedRead{ID: "2", CSV: utf8Comma}, nil) ||
		parseKey("csv", csvChoice{}, nil, &confirmedRead{ID: "3", CSV: utf8Comma}) != parseKey("csv", csvChoice{}, nil, &confirmedRead{ID: "4", CSV: utf8Comma}) ||
		parseKey("csv", csvChoice{}, nil, &confirmedRead{CSV: utf8Comma}) == parseKey("csv", utf8Comma, nil, nil) {
		t.Fatal("the key is how the file is read")
	}
}
