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

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// A file's accounts are mapped to the classes and types the month's books carry: the reader proposes each from the
// file's type column, its heading, its name or its number; a reviewer confirms, setting any it would map otherwise; and
// next month's file finds its accounts mapped already.
func TestAFilesAccountsAreMappedOnceAndKnownNextMonth(t *testing.T) {
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
	org := "org_Maps" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "preparer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name,file_imports) VALUES($1,'Maps fixture',true)`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b']) s`,
		`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Acme Co',true,'2026-08','Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM books_account_maps WHERE organization_id=$1`, `DELETE FROM books_import_deletions WHERE organization_id=$1`, `DELETE FROM books_import_pages WHERE organization_id=$1`, `DELETE FROM books_import_previews WHERE organization_id=$1`,
			`DELETE FROM books_layouts WHERE organization_id=$1`, `DELETE FROM books_imports WHERE organization_id=$1`, `DELETE FROM platform_audit WHERE organization_id=$1`,
			`DELETE FROM client_roster WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`,
			`DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
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
	// A month's file, its layout confirmed: number, name, type, debit and credit.
	confirmLayoutWith := func(id string, table any, period string, marks map[string]any) {
		t.Helper()
		layout := map[string]any{"table": table, "header_row": 1,
			"columns": map[string]string{"0": "account_number", "1": "account_name", "2": "account_type", "3": "debit", "4": "credit"}, "style": map[string]any{"decimal_comma": false, "scale": 1},
			"as_of": period + "-30", "currency": "USD"}
		for k, v := range marks {
			layout[k] = v
		}
		call("user_a", partner, "POST", "/v1/closes/client/import?name=Acme%20Co&id="+id, map[string]any{"action": "confirm", "reason": "Layout checked", "layout": layout}, 200)
	}
	confirmLayout := func(id string, table any, period string) { t.Helper(); confirmLayoutWith(id, table, period, nil) }
	month := func(period, content string) string {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		must(m.WriteField("period", period))
		must(m.WriteField("one_client", "true"))
		f, e := m.CreateFormFile("file", "Acme "+period+".csv")
		must(e)
		_, e = f.Write([]byte(content))
		must(e)
		must(m.Close())
		id := send("user_b", preparer, "POST", "/v1/closes/client/imports?name=Acme%20Co", m.FormDataContentType(), b.Bytes(), 201)["id"].(string)
		path := "/v1/closes/client/import?name=Acme%20Co&id=" + id
		table := call("user_b", preparer, "GET", path, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
		call("user_b", preparer, "GET", "/v1/closes/client/import/accounts?name=Acme%20Co&id="+id, nil, 409)
		confirmLayout(id, table, period)
		return id
	}
	// confirm confirms a file's map as a reviewer who just opened it would: on the map and reading they saw, unless the
	// body names others.
	confirm := func(path string, body map[string]any, want int) map[string]any {
		t.Helper()
		seen := call("user_b", preparer, "GET", path, nil, 200)
		if _, ok := body["based_on"]; !ok {
			body["based_on"] = ""
			if m, ok := seen["map"].(map[string]any); ok {
				body["based_on"] = m["stamp"]
			}
		}
		if _, ok := body["reading"]; !ok {
			body["reading"] = seen["reading"]
		}
		return call("user_a", partner, "POST", path, body, want)
	}
	// readingOf is a file's confirmed reading, as the map records it.
	readingOf := func(id string) string {
		t.Helper()
		var reading string
		must(db.QueryRow(`SELECT max(id)::text FROM books_layouts WHERE import_id::text=$1`, id).Scan(&reading))
		return reading
	}
	// Rent comes on two rows, one per office: one account, its balance their sum.
	september := month("2026-09", "Number,Account,Type,Debit,Credit\n1000,Business Checking,Bank,1500.25,\n1100,Trade debtors,Current Asset,200.00,\n2000,Amex,Credit Card,,300.00\n3000,Owner's Equity,,,1000.00\n4000,Sales,,,750.25\n6000,Rent,,300.00,\n6000,Rent,,50.00,\n")
	accountsPath := "/v1/closes/client/import/accounts?name=Acme%20Co&id=" + september
	read := call("user_b", preparer, "GET", accountsPath, nil, 200)
	if read["count"] != 6.0 || read["unmapped"] != 6.0 || read["map"] != nil || read["can_confirm"] != false {
		t.Fatalf("six accounts, none mapped yet: %v", read)
	}
	proposed := map[string]string{}
	for _, raw := range read["accounts"].([]any) {
		a := raw.(map[string]any)
		if a["key"] == "n:6000" && a["balance"] != "350.00" {
			t.Fatalf("rent's two rows sum: %v", a)
		}
		p := a["proposal"].(map[string]any)
		proposed[a["key"].(string)] = p["class"].(string) + "/" + p["type"].(string) + "/" + p["from"].(string)
	}
	for key, want := range map[string]string{"n:1000": "Asset/Bank/type", "n:1100": "Asset/Accounts Receivable/type", "n:2000": "Liability/Credit Card/type",
		"n:3000": "Equity/Equity/name", "n:4000": "Revenue/Income/name", "n:6000": "Expense/Expense/name"} {
		if proposed[key] != want {
			t.Fatalf("%s proposed as %s, want %s", key, proposed[key], want)
		}
	}
	// Only a reviewer confirms, with a reason, classes and types that belong together, for the file's own accounts.
	call("user_b", preparer, "POST", accountsPath, map[string]any{"accept": true, "reason": "Checked"}, 403)
	// A confirmation says which version of the map it was made on: none yet, here.
	confirm(accountsPath, map[string]any{"accept": true, "reason": "Checked", "based_on": "1:x"}, 409)
	// It also says which reading of the file it was made on.
	confirm(accountsPath, map[string]any{"accept": true, "reason": "Checked", "reading": "0"}, 409)
	confirm(accountsPath, map[string]any{"accept": true, "reason": " "}, 422)
	confirm(accountsPath, map[string]any{"accounts": map[string]any{"n:6000": map[string]string{"class": "Asset", "type": "Expense"}}, "reason": "Checked"}, 422)
	confirm(accountsPath, map[string]any{"accounts": map[string]any{"n:9999": map[string]string{"class": "Expense", "type": "Expense"}}, "reason": "Checked"}, 422)
	confirmed := confirm(accountsPath, map[string]any{"accept": true, "accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Other Expense"}}, "reason": "Rent is below the line here"}, 200)
	// Choices made on the map before that confirmation never overwrite it.
	confirm(accountsPath, map[string]any{"accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Expense"}}, "reason": "Stale", "based_on": ""}, 409)
	if confirmed["stamp"] == nil {
		t.Fatalf("a confirmation answers with the map's stamp: %v", confirmed)
	}
	if confirmed["unmapped"] != 0.0 {
		t.Fatalf("every account mapped: %v", confirmed)
	}
	read = call("user_b", preparer, "GET", accountsPath, nil, 200)
	if read["unmapped"] != 0.0 || read["map"].(map[string]any)["reason"] != "Rent is below the line here" {
		t.Fatalf("the map in force: %v", read)
	}
	// Confirming nothing new is refused: a version is a change.
	confirm(accountsPath, map[string]any{"accept": true, "reason": "Again"}, 422)

	// Next month's file finds its accounts mapped, as set, and asks only about the new one.
	october := month("2026-10", "Number,Account,Type,Debit,Credit\n1000,Business Checking,Bank,1600.25,\n1100,Trade debtors,Current Asset,200.00,\n2000,Amex,Credit Card,,300.00\n3000,Owner's Equity,,,1000.00\n4000,Sales,,,900.25\n6000,Rent,,300.00,\n6100,Utilities,,100.00,\n")
	read = call("user_b", preparer, "GET", "/v1/closes/client/import/accounts?name=Acme%20Co&id="+october, nil, 200)
	if read["count"] != 7.0 || read["unmapped"] != 1.0 {
		t.Fatalf("one new account to map: %v", read)
	}
	for _, raw := range read["accounts"].([]any) {
		a := raw.(map[string]any)
		if a["key"] == "n:6000" && a["mapped"].(map[string]any)["type"] != "Other Expense" {
			t.Fatalf("rent keeps the type it was set to: %v", a)
		}
		if a["key"] == "n:6100" && a["mapped"] != nil {
			t.Fatalf("utilities is new: %v", a)
		}
	}
	if read["recorded"] != false {
		t.Fatalf("October's file is not confirmed on yet: %v", read)
	}
	// Rent changes type on October's file, keeping the file it was confirmed on before.
	confirm("/v1/closes/client/import/accounts?name=Acme%20Co&id="+october, map[string]any{"accept": true, "accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Expense"}},
		"reason": "Utilities added; rent back above the line"}, 200)
	files := func(key string) (string, string) {
		t.Helper()
		var typ, from sql.NullString
		must(db.QueryRow(`SELECT accounts->$2->>'type',accounts->$2->>'readings' FROM books_account_maps WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org, key).Scan(&typ, &from))
		return typ.String, from.String
	}
	septemberReading, octoberReading := readingOf(september), readingOf(october)
	if typ, from := files("n:6000"); typ != "Expense" || from != `["`+septemberReading+`", "`+octoberReading+`"]` {
		t.Fatalf("rent's new type, on both files: %s %s", typ, from)
	}
	// November's file names only accounts mapped already: confirming it records that it names them, and changes nothing
	// else.
	// A row naming no account (no number, no name) is counted, never mapped.
	november := month("2026-11", "Number,Account,Type,Debit,Credit\n1000,Business Checking,Bank,1500.25,\n1100,Trade debtors,Current Asset,200.00,\n2000,Amex,Credit Card,,300.00\n3000,Owner's Equity,,,1000.00\n4000,Sales,,,725.25\n6000,Rent,,300.00,\n,,,25.00,\n")
	novemberPath := "/v1/closes/client/import/accounts?name=Acme%20Co&id=" + november
	if read = call("user_b", preparer, "GET", novemberPath, nil, 200); read["unmapped"] != 0.0 || read["recorded"] != false || read["unidentified"] != 1.0 || read["count"] != 6.0 {
		t.Fatalf("every account mapped, none on November's file yet, one row naming none: %v", read)
	}
	confirm(novemberPath, map[string]any{"accept": true, "reason": "Same accounts as before"}, 200)
	if read = call("user_b", preparer, "GET", novemberPath, nil, 200); read["recorded"] != true {
		t.Fatalf("November's file recorded: %v", read)
	}
	// November's layout confirmed again asks for its map again; confirming it records the new reading in place of the
	// one it supersedes.
	novemberFirst := readingOf(november)
	confirmLayout(november, call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+november, nil, 200)["import"].(map[string]any)["confirmed"].(map[string]any)["layout"].(map[string]any)["table"], "2026-11")
	if read = call("user_b", preparer, "GET", novemberPath, nil, 200); read["recorded"] != false {
		t.Fatalf("a reading confirmed again is mapped again: %v", read)
	}
	// Choices made on the earlier reading are refused.
	confirm(novemberPath, map[string]any{"accept": true, "reason": "Layout reconfirmed", "reading": novemberFirst}, 409)
	confirm(novemberPath, map[string]any{"accept": true, "reason": "Layout reconfirmed"}, 200)
	novemberSecond := readingOf(november)
	var versions int
	must(db.QueryRow(`SELECT count(*) FROM books_account_maps WHERE organization_id=$1`, org).Scan(&versions))
	if versions != 4 {
		t.Fatalf("each confirmation a version holding the whole map: %d", versions)
	}
	// A section's total closes the heading it names and those inside it: accounts after "Total Assets" are proposed
	// from their names and numbers, not as assets.
	december := month("2026-12", "Number,Account,Type,Debit,Credit\n,Assets,,,\n,Current Assets,,,\n1000,Business Checking,Bank,1500.25,\n,Fixed Assets,,,\n1500,Equipment,,500.00,\n,Total Assets,,2000.25,\n2000,Suspense,,,500.00\n7000,Consulting income,,,1500.25\n")
	// The rows naming sections without amounts are marked as headings, as a reviewer marks them.
	confirmLayoutWith(december, call("user_b", preparer, "GET", "/v1/closes/client/import?name=Acme%20Co&id="+december, nil, 200)["import"].(map[string]any)["confirmed"].(map[string]any)["layout"].(map[string]any)["table"],
		"2026-12", map[string]any{"headings": []int{2, 3, 5}})
	var headings, totals int
	must(db.QueryRow(`SELECT count(*) FILTER (WHERE e->>'kind'='heading'),count(*) FILTER (WHERE e->>'kind'='total') FROM books_layouts l,jsonb_array_elements(l.reading->'lines') e WHERE l.id=(SELECT max(id) FROM books_layouts WHERE import_id::text=$1)`, december).Scan(&headings, &totals))
	if headings != 3 || totals != 1 {
		var kinds string
		db.QueryRow(`SELECT string_agg((e->>'kind')||':'||(e->>'name'),' | ') FROM books_layouts l,jsonb_array_elements(l.reading->'lines') e WHERE l.id=(SELECT max(id) FROM books_layouts WHERE import_id::text=$1)`, december).Scan(&kinds)
		t.Fatalf("December's reading has its headings and its total: %d %d (%s)", headings, totals, kinds)
	}
	for _, raw := range call("user_b", preparer, "GET", "/v1/closes/client/import/accounts?name=Acme%20Co&id="+december, nil, 200)["accounts"].([]any) {
		a := raw.(map[string]any)
		p := a["proposal"].(map[string]any)
		if a["key"] == "n:7000" && (p["class"] != "Revenue" || p["from"] != "name") || a["key"] == "n:2000" && (p["class"] != "Liability" || p["from"] != "number") ||
			a["key"] == "n:1500" && (p["type"] != "Fixed Asset" || p["from"] != "heading") {
			t.Fatalf("an account after a section's total, or inside a nested one: %v", a)
		}
	}
	// A deletion that changes the map in place refuses choices made on it before.
	before := call("user_b", preparer, "GET", novemberPath, nil, 200)["map"].(map[string]any)["stamp"]
	// Deleting October's file takes from the map what only it named, utilities, from every version; deleting
	// September's then leaves what November's names too.
	manager := []string{"workpapers:review", "clients:all", "data:manage"}
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+october, map[string]any{"reason": "Sent in error"}, 200)
	confirm(novemberPath, map[string]any{"accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Other Expense"}}, "reason": "Stale", "based_on": before}, 409)
	var utilities int
	must(db.QueryRow(`SELECT count(*) FILTER (WHERE accounts::text LIKE '%Utilities%' OR accounts ? 'n:6100') FROM books_account_maps WHERE organization_id=$1`, org).Scan(&utilities))
	if typ, from := files("n:6000"); utilities != 0 || typ != "Expense" || from != `["`+septemberReading+`", "`+novemberSecond+`"]` {
		t.Fatalf("a deleted file leaves nothing only it named: %d versions name utilities; rent %s from %s", utilities, typ, from)
	}
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+september, map[string]any{"reason": "Sent in error"}, 200)
	if typ, from := files("n:1000"); typ != "Bank" || from != `["`+novemberSecond+`"]` {
		t.Fatalf("the bank stays mapped, on November's file: %s %s", typ, from)
	}
	accountsPath = novemberPath
	// With file imports switched off, the map is read, not changed.
	_, err = db.Exec(`UPDATE organizations SET file_imports=false WHERE id=$1`, org)
	must(err)
	call("user_b", preparer, "GET", accountsPath, nil, 200)
	confirm(accountsPath, map[string]any{"accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Expense"}}, "reason": "Back"}, 403)
}
