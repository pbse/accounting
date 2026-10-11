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
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// A file's checks run on its confirmed reading, each passing, holding what depends on it, or noting, and none refuses;
// a reviewer accepts the reading as the client's books for the month on the reading, attestation and map they saw, and
// the next month's file is checked against it.
func TestAFilesReadingIsCheckedAndAcceptedAsTheMonthsBooks(t *testing.T) {
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
	org := "org_Accept" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "preparer"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name,file_imports) VALUES($1,'Acceptance fixture',true)`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT $1,s,true,'Synthetic','fixture' FROM unnest(ARRAY['user_a','user_b']) s`,
		`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Acme Co',true,'2026-08','Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM books_import_acceptances WHERE organization_id=$1`, `DELETE FROM books_import_scopes WHERE organization_id=$1`, `DELETE FROM books_account_maps WHERE organization_id=$1`,
			`DELETE FROM books_import_deletions WHERE organization_id=$1`, `DELETE FROM books_import_pages WHERE organization_id=$1`, `DELETE FROM books_import_previews WHERE organization_id=$1`,
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
	// A month's file, its layout confirmed as of asOf.
	send1 := func(period, content string) string {
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
		return send("user_b", preparer, "POST", "/v1/closes/client/imports?name=Acme%20Co", m.FormDataContentType(), b.Bytes(), 201)["id"].(string)
	}
	confirmLayout := func(id, asOf string) {
		t.Helper()
		path := "/v1/closes/client/import?name=Acme%20Co&id=" + id
		table := call("user_b", preparer, "GET", path, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
		call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "reason": "Layout checked", "layout": map[string]any{"table": table, "header_row": 1,
			"columns": map[string]string{"0": "account_number", "1": "account_name", "2": "debit", "3": "credit"}, "style": map[string]any{"decimal_comma": false, "scale": 1},
			"as_of": asOf, "currency": "USD"}}, 200)
	}
	attest := func(id string) {
		t.Helper()
		call("user_a", partner, "POST", "/v1/closes/client/import/scope?name=Acme%20Co&id="+id, map[string]any{"book": "General ledger", "complete": true, "basis": "accrual", "reason": "Checked"}, 200)
	}
	mapAll := func(id string) {
		t.Helper()
		path := "/v1/closes/client/import/accounts?name=Acme%20Co&id=" + id
		seen := call("user_b", preparer, "GET", path, nil, 200)
		stamp := ""
		if m, ok := seen["map"].(map[string]any); ok {
			stamp = m["stamp"].(string)
		}
		call("user_a", partner, "POST", path, map[string]any{"accept": true, "reason": "Checked", "based_on": stamp, "reading": seen["reading"]}, 200)
	}
	checksOf := func(read map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, raw := range read["checks"].([]any) {
			c := raw.(map[string]any)
			out[c["key"].(string)] = c
		}
		return out
	}

	// Suspense sits on two rows that net to nothing: October need not name it.
	september := send1("2026-09", "Number,Account,Debit,Credit\n1000,Business Checking,1500.25,\n1100,Trade debtors,200.00,\n3000,Owner's Equity,,1000.00\n4000,Sales,,700.25\n5000,Suspense,100.00,\n5000,Suspense,,100.00\n")
	path := "/v1/closes/client/import/acceptance?name=Acme%20Co&id=" + september
	if read := call("user_b", preparer, "GET", path, nil, 200); read["confirmed"] != false || read["can_accept"] != false {
		t.Fatalf("nothing to check before a confirmed reading: %v", read)
	}
	confirmLayout(september, "2026-09-30")
	read := call("user_a", partner, "GET", path, nil, 200)
	checks := checksOf(read)
	for key, want := range map[string]string{"scope": "holds", "balanced": "passes", "as_of": "passes", "totals": "passes", "classes": "holds", "currency": "passes", "basis": "holds"} {
		if checks[key]["state"] != want {
			t.Fatalf("%s %v, want %s: %v", key, checks[key]["state"], want, read)
		}
	}
	if read["can_accept"] != false {
		t.Fatalf("no acceptance before the scope is attested: %v", read)
	}
	accept := map[string]any{"system": "QuickBooks Desktop", "exported_on": "2026-10-03", "final_adjustments": true, "reason": "Books for September"}
	call("user_a", partner, "POST", path, accept, 409)

	// Attested and mapped, every check passes but the basis, a note while no basis is approved.
	attest(september)
	mapAll(september)
	read = call("user_a", partner, "GET", path, nil, 200)
	checks = checksOf(read)
	if checks["scope"]["state"] != "passes" || checks["classes"]["state"] != "passes" || checks["basis"]["state"] != "note" || read["holds"] != 0.0 || read["can_accept"] != true {
		t.Fatalf("attested and mapped: %v", read)
	}
	with := func(read map[string]any, extra map[string]any) map[string]any {
		body := map[string]any{"reading": read["reading"], "scope": read["scope"], "based_on": read["map_stamp"]}
		for k, v := range accept {
			body[k] = v
		}
		for k, v := range extra {
			body[k] = v
		}
		return body
	}
	// Only a reviewer accepts, naming the system and a past export date, on the reading, scope and map they saw.
	call("user_b", preparer, "POST", path, with(read, nil), 403)
	for _, bad := range []map[string]any{{"system": " "}, {"exported_on": "3 October"}, {"exported_on": time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")}, {"reason": ""}} {
		call("user_a", partner, "POST", path, with(read, bad), 422)
	}
	call("user_a", partner, "POST", path, with(read, map[string]any{"based_on": "1:stale"}), 409)
	call("user_a", partner, "POST", path, with(read, map[string]any{"reading": "0"}), 409)
	if out := call("user_a", partner, "POST", path, with(read, nil), 200); out["holds"] != 0.0 {
		t.Fatalf("accepted with nothing holding: %v", out)
	}
	if a := call("user_b", preparer, "GET", path, nil, 200)["acceptance"].(map[string]any); a["current"] != true || a["system"] != "QuickBooks Desktop" || a["exported_on"] != "2026-10-03" {
		t.Fatalf("the acceptance in force: %v", a)
	}

	// October's file leaves out an account with a balance in September's accepted books, and is dated mid-month.
	october := send1("2026-10", "Number,Account,Debit,Credit\n1000,Business Checking,1700.25,\n3000,Owner's Equity,,1000.00\n4000,Sales,,700.25\n")
	octoberPath := "/v1/closes/client/import/acceptance?name=Acme%20Co&id=" + october
	confirmLayout(october, "2026-10-15")
	attest(october)
	mapAll(october)
	read = call("user_a", partner, "GET", octoberPath, nil, 200)
	checks = checksOf(read)
	items, _ := checks["scope"]["items"].([]any)
	if checks["scope"]["state"] != "holds" || len(items) != 1 || items[0] != "1100 Trade debtors" || checks["as_of"]["state"] != "holds" {
		t.Fatalf("an account missing since September, and a mid-month date: %v", read)
	}
	// Accepting with an explanation makes the missing account a note; the date still holds, and is kept with it.
	out := call("user_a", partner, "POST", octoberPath, with(read, map[string]any{"explained": "Collected in full; the account was closed", "exported_on": "2026-10-05"}), 200)
	kept := checksOf(out)
	if kept["scope"]["state"] != "note" || kept["as_of"]["state"] != "holds" || out["holds"] != 1.0 {
		t.Fatalf("the checks kept with the acceptance: %v", out)
	}
	// Reopened, the accepted file shows the checks it was accepted with, its explanation included.
	if shown := checksOf(call("user_b", preparer, "GET", octoberPath, nil, 200)); shown["scope"]["state"] != "note" {
		t.Fatalf("the accepted file's checks, reopened: %v", shown["scope"])
	}
	// A scope attested again leaves the acceptance on record, no longer current.
	attest(october)
	if a := call("user_b", preparer, "GET", octoberPath, nil, 200)["acceptance"].(map[string]any); a["current"] != false {
		t.Fatalf("an acceptance of an earlier attestation: %v", a)
	}
	// The file list says which files are accepted.
	for _, raw := range call("user_b", preparer, "GET", "/v1/closes/client/imports?name=Acme%20Co", nil, 200)["imports"].([]any) {
		i := raw.(map[string]any)
		if i["id"] == september && i["accepted"] != true || i["id"] == october && i["accepted"] != false {
			t.Fatalf("which files are accepted: %v", i)
		}
	}
	// With file imports switched off, the checks are read, and nothing is accepted.
	_, err = db.Exec(`UPDATE organizations SET file_imports=false WHERE id=$1`, org)
	must(err)
	if read = call("user_b", preparer, "GET", path, nil, 200); read["can_accept"] != false {
		t.Fatalf("no acceptance while file imports are off: %v", read)
	}
	call("user_a", partner, "POST", path, with(read, nil), 403)
}
