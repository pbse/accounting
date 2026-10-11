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

// A reviewer attests what a file's confirmed reading covers and what basis the export is on; the basis is compared with
// the client's approved basis, and a layout confirmed again asks for the attestation afresh.
func TestAFilesScopeAndBasisAreAttestedAgainstItsReading(t *testing.T) {
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
	org := "org_Scope" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "preparer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name,file_imports) VALUES($1,'Scope fixture',true)`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b']) s`,
		`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Acme Co',true,'2026-08','Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM books_import_scopes WHERE organization_id=$1`, `DELETE FROM books_import_deletions WHERE organization_id=$1`, `DELETE FROM books_import_pages WHERE organization_id=$1`,
			`DELETE FROM books_import_previews WHERE organization_id=$1`, `DELETE FROM books_layouts WHERE organization_id=$1`, `DELETE FROM books_imports WHERE organization_id=$1`,
			`DELETE FROM procedure_versions WHERE organization_id=$1`, `DELETE FROM platform_audit WHERE organization_id=$1`, `DELETE FROM client_roster WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`,
			`DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	partner := []string{"workpapers:review", "clients:all", "data:manage"}
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
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	must(m.WriteField("period", "2026-09"))
	must(m.WriteField("one_client", "true"))
	f, e := m.CreateFormFile("file", "Acme September.csv")
	must(e)
	_, e = f.Write([]byte("Account,Debit,Credit\nBank,1500.25,\nCapital,,1000.00\nSales,,500.25\n"))
	must(e)
	must(m.Close())
	id := send("user_b", preparer, "POST", "/v1/closes/client/imports?name=Acme%20Co", m.FormDataContentType(), b.Bytes(), 201)["id"].(string)
	importPath := "/v1/closes/client/import?name=Acme%20Co&id=" + id
	scopePath := "/v1/closes/client/import/scope?name=Acme%20Co&id=" + id
	attest := map[string]any{"book": "General ledger", "filters": "", "complete": true, "basis": "accrual", "reason": "Exported by the client's controller"}

	// Nothing is attested before the reading is confirmed.
	if read := call("user_a", partner, "GET", scopePath, nil, 200); read["confirmed"] != false || read["scope"] != nil || read["can_confirm"] != false {
		t.Fatalf("before a confirmed reading: %v", read)
	}
	call("user_a", partner, "POST", scopePath, attest, 409)
	table := call("user_b", preparer, "GET", importPath, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
	layout := map[string]any{"table": table, "header_row": 1, "columns": map[string]string{"0": "account_name", "1": "debit", "2": "credit"}, "style": map[string]any{"decimal_comma": false, "scale": 1}, "as_of": "2026-09-30", "currency": "USD"}
	call("user_a", partner, "POST", importPath, map[string]any{"action": "confirm", "layout": layout, "reason": "Checked"}, 200)

	// Only a reviewer attests, saying the basis, and what is missing when it is not the complete books.
	call("user_b", preparer, "POST", scopePath, attest, 403)
	for _, bad := range []map[string]any{{"basis": "tax"}, {"complete": false, "missing": " "}, {"reason": ""}, {"book": strings.Repeat("x", 201)}} {
		body := map[string]any{}
		for k, v := range attest {
			body[k] = v
		}
		for k, v := range bad {
			body[k] = v
		}
		call("user_a", partner, "POST", scopePath, body, 422)
	}
	// With no basis approved for the client, the file's is recorded and compared once one is.
	if out := call("user_a", partner, "POST", scopePath, attest, 200); !strings.HasPrefix(out["note"].(string), "No basis is approved") {
		t.Fatalf("no approved basis yet: %v", out)
	}
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,'','basis','Synthetic',true,'Synthetic basis','fixture','accrual')`, org)
	must(err)
	read := call("user_b", preparer, "GET", scopePath, nil, 200)
	if read["current"] != true || read["note"] != "" || read["approved_basis"].(map[string]any)["method"] != "accrual" || read["scope"].(map[string]any)["book"] != "General ledger" || read["can_confirm"] != false {
		t.Fatalf("an accrual file for an accrual client: %v", read)
	}
	// A cash export of part of the books, for an accrual client, says what it holds.
	part := map[string]any{"book": "General ledger", "filters": "Department: Retail", "complete": false, "missing": "The Wholesale department", "basis": "cash", "reason": "Client sent one department"}
	call("user_a", partner, "POST", scopePath, part, 200)
	read = call("user_a", partner, "GET", scopePath, nil, 200)
	if s := read["scope"].(map[string]any); s["complete"] != false || s["missing"] != "The Wholesale department" || !strings.Contains(read["note"].(string), "held until") {
		t.Fatalf("a partial cash export: %v", read)
	}
	// A layout confirmed again asks for the attestation afresh.
	call("user_a", partner, "POST", importPath, map[string]any{"action": "confirm", "layout": layout, "reason": "Confirmed again"}, 200)
	if read = call("user_a", partner, "GET", scopePath, nil, 200); read["current"] != false {
		t.Fatalf("an attestation of an earlier reading: %v", read)
	}
	// Deleting the file deletes what was attested of it.
	call("user_a", partner, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+id, map[string]any{"reason": "Sent in error"}, 200)
	var left int
	must(db.QueryRow(`SELECT count(*) FROM books_import_scopes WHERE organization_id=$1`, org).Scan(&left))
	if left != 0 {
		t.Fatalf("a deleted file's attestations go with it: %d", left)
	}
}
