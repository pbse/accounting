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
		for _, q := range []string{`DELETE FROM books_account_maps WHERE organization_id=$1`, `DELETE FROM books_import_pages WHERE organization_id=$1`, `DELETE FROM books_import_previews WHERE organization_id=$1`,
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
		call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "reason": "Layout checked", "layout": map[string]any{"table": table, "header_row": 1,
			"columns": map[string]string{"0": "account_number", "1": "account_name", "2": "account_type", "3": "debit", "4": "credit"}, "style": map[string]any{"decimal_comma": false, "scale": 1},
			"as_of": period + "-30", "currency": "USD"}}, 200)
		return id
	}
	september := month("2026-09", "Number,Account,Type,Debit,Credit\n1000,Business Checking,Bank,1500.25,\n1100,Trade debtors,Current Asset,200.00,\n2000,Amex,Credit Card,,300.00\n3000,Owner's Equity,,,1000.00\n4000,Sales,,,700.25\n6000,Rent,,300.00,\n")
	accountsPath := "/v1/closes/client/import/accounts?name=Acme%20Co&id=" + september
	read := call("user_b", preparer, "GET", accountsPath, nil, 200)
	if read["count"] != 6.0 || read["unmapped"] != 6.0 || read["map"] != nil || read["can_confirm"] != false {
		t.Fatalf("six accounts, none mapped yet: %v", read)
	}
	proposed := map[string]string{}
	for _, raw := range read["accounts"].([]any) {
		a := raw.(map[string]any)
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
	call("user_a", partner, "POST", accountsPath, map[string]any{"accept": true, "reason": " "}, 422)
	call("user_a", partner, "POST", accountsPath, map[string]any{"accounts": map[string]any{"n:6000": map[string]string{"class": "Asset", "type": "Expense"}}, "reason": "Checked"}, 422)
	call("user_a", partner, "POST", accountsPath, map[string]any{"accounts": map[string]any{"n:9999": map[string]string{"class": "Expense", "type": "Expense"}}, "reason": "Checked"}, 422)
	confirmed := call("user_a", partner, "POST", accountsPath, map[string]any{"accept": true, "accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Other Expense"}}, "reason": "Rent is below the line here"}, 200)
	if confirmed["unmapped"] != 0.0 {
		t.Fatalf("every account mapped: %v", confirmed)
	}
	read = call("user_b", preparer, "GET", accountsPath, nil, 200)
	if read["unmapped"] != 0.0 || read["map"].(map[string]any)["reason"] != "Rent is below the line here" {
		t.Fatalf("the map in force: %v", read)
	}
	// Confirming nothing new is refused: a version is a change.
	call("user_a", partner, "POST", accountsPath, map[string]any{"accept": true, "reason": "Again"}, 422)

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
	call("user_a", partner, "POST", "/v1/closes/client/import/accounts?name=Acme%20Co&id="+october, map[string]any{"accept": true, "reason": "Utilities added"}, 200)
	var versions int
	var rent string
	must(db.QueryRow(`SELECT count(*),(SELECT accounts->'n:6000'->>'type' FROM books_account_maps WHERE organization_id=$1 ORDER BY id DESC LIMIT 1) FROM books_account_maps WHERE organization_id=$1`, org).Scan(&versions, &rent))
	if versions != 2 || rent != "Other Expense" {
		t.Fatalf("each confirmation a version holding the whole map: %d versions, rent %s", versions, rent)
	}
	// With file imports switched off, the map is read, not changed.
	_, err = db.Exec(`UPDATE organizations SET file_imports=false WHERE id=$1`, org)
	must(err)
	call("user_b", preparer, "GET", accountsPath, nil, 200)
	call("user_a", partner, "POST", accountsPath, map[string]any{"accounts": map[string]any{"n:6000": map[string]string{"class": "Expense", "type": "Expense"}}, "reason": "Back"}, 403)
}
