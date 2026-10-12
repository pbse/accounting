package transport

import (
	"archive/zip"
	"bytes"
	"close.local/backend/internal/bookfiles"
	"close.local/backend/internal/preparation"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"slices"
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
		for _, q := range []string{`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`, `DELETE FROM books_company_links WHERE organization_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM coverage_bindings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM books_versions WHERE organization_id=$1`, `DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM books_import_acceptances WHERE organization_id=$1`, `DELETE FROM books_sources WHERE organization_id=$1`, `DELETE FROM client_profiles WHERE organization_id=$1`, `DELETE FROM books_import_scopes WHERE organization_id=$1`, `DELETE FROM books_account_maps WHERE organization_id=$1`,
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
	confirmColumns := func(id, asOf string, columns map[string]string) {
		t.Helper()
		path := "/v1/closes/client/import?name=Acme%20Co&id=" + id
		table := call("user_b", preparer, "GET", path, nil, 200)["tables"].([]any)[0].(map[string]any)["name"]
		call("user_a", partner, "POST", path, map[string]any{"action": "confirm", "reason": "Layout checked", "layout": map[string]any{"table": table, "header_row": 1,
			"columns": columns, "style": map[string]any{"decimal_comma": false, "scale": 1}, "as_of": asOf, "currency": "USD"}}, 200)
	}
	confirmLayout := func(id, asOf string) {
		t.Helper()
		confirmColumns(id, asOf, map[string]string{"0": "account_number", "1": "account_name", "2": "debit", "3": "credit"})
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

	// Attested and mapped, every check passes but the basis, a note while no basis is approved, and the month's activity,
	// which a mid-year Trial Balance alone cannot give.
	attest(september)
	mapAll(september)
	read = call("user_a", partner, "GET", path, nil, 200)
	checks = checksOf(read)
	if checks["scope"]["state"] != "passes" || checks["classes"]["state"] != "passes" || checks["basis"]["state"] != "note" || read["holds"] != 1.0 || read["can_accept"] != true {
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
	if out := call("user_a", partner, "POST", path, with(read, nil), 200); out["holds"] != 1.0 {
		t.Fatalf("accepted with only the activity holding: %v", out)
	}
	if a := call("user_b", preparer, "GET", path, nil, 200)["acceptance"].(map[string]any); a["current"] != true || a["system"] != "QuickBooks Desktop" || a["exported_on"] != "2026-10-03" {
		t.Fatalf("the acceptance in force: %v", a)
	}
	// A newer account map that classifies one of September's accounts differently puts the acceptance out of date (the
	// month's books keep the accepted map until it is accepted again); the map going back restores it.
	var reclassified string
	must(db.QueryRow(`INSERT INTO books_account_maps(organization_id,client_entity,period,accounts,reason,actor) SELECT organization_id,client_entity,period,jsonb_set(accounts,'{n:5000,class}','"Asset"'),'Reclassified','fixture' FROM books_account_maps WHERE organization_id=$1 ORDER BY id DESC LIMIT 1 RETURNING id::text`, org).Scan(&reclassified))
	if a := call("user_b", preparer, "GET", path, nil, 200)["acceptance"].(map[string]any); a["current"] != false || a["map_changed"] != true {
		t.Fatalf("a reclassified account puts the acceptance out of date: %v", a)
	}
	_, err = db.Exec(`DELETE FROM books_account_maps WHERE id::text=$1`, reclassified)
	must(err)
	if a := call("user_b", preparer, "GET", path, nil, 200)["acceptance"].(map[string]any); a["current"] != true {
		t.Fatalf("the acceptance stands on its map again: %v", a)
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
	if kept["scope"]["state"] != "note" || kept["as_of"]["state"] != "holds" || out["holds"] != 2.0 {
		t.Fatalf("the checks kept with the acceptance: %v", out)
	}
	// For a month whose books come from a live ledger, the accepted file is a supplement, reconciled to its books: not
	// while checks it was accepted with hold its statements, nor with no capture yet.
	var octoberClose string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2026-10') RETURNING id::text`, org).Scan(&octoberClose))
	supplementOf := func() map[string]any {
		t.Helper()
		supplement, _ := call("user_a", partner, "GET", octoberPath, nil, 200)["supplement"].(map[string]any)
		if supplement["state"] != "unavailable" || supplement["close_id"] != octoberClose {
			t.Fatalf("October's file as a supplement: %v", supplement)
		}
		return supplement
	}
	if reason := fmt.Sprint(supplementOf()["reason"]); !strings.Contains(reason, "not compared") {
		t.Fatalf("a held file is not compared: %s", reason)
	}
	_, err = db.Exec(`UPDATE books_import_acceptances SET checks=(SELECT jsonb_agg(CASE WHEN c->>'state'='holds' THEN jsonb_set(c,'{state}','"passes"') ELSE c END) FROM jsonb_array_elements(checks) c) WHERE import_id::text=$1`, october)
	must(err)
	if reason := fmt.Sprint(supplementOf()["reason"]); !strings.Contains(reason, "no captured books") {
		t.Fatalf("a month with no capture: %s", reason)
	}
	// Reopened, the accepted file shows the checks it was accepted with, its explanation included.
	if shown := checksOf(call("user_b", preparer, "GET", octoberPath, nil, 200)); shown["scope"]["state"] != "note" {
		t.Fatalf("the accepted file's checks, reopened: %v", shown["scope"])
	}
	// September accepted again with an account October does not name: the explanation does not cover it, so it holds.
	_, err = db.Exec(`UPDATE books_import_acceptances SET checks=jsonb_set(checks,'{0,items}','[]') WHERE import_id::text=$1`, october)
	must(err)
	if shown := checksOf(call("user_b", preparer, "GET", octoberPath, nil, 200)); shown["scope"]["state"] != "holds" {
		t.Fatalf("an account the explanation never covered: %v", shown["scope"])
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
	// A workbook whose balance is a formula's stored result holds its use as the month's books until a reviewer attests,
	// with its scope, that it is a recalculated copy.
	const main = `xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`
	var zipped bytes.Buffer
	z := zip.NewWriter(&zipped)
	for name, body := range map[string]string{
		"xl/workbook.xml":            `<workbook ` + main + `><sheets><sheet name="TB" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml": `<worksheet ` + main + `><sheetData><row r="1"><c r="A1" t="inlineStr"><is><t>Number</t></is></c><c r="B1" t="inlineStr"><is><t>Account</t></is></c><c r="C1" t="inlineStr"><is><t>Debit</t></is></c><c r="D1" t="inlineStr"><is><t>Credit</t></is></c></row>` +
			`<row r="2"><c r="A2"><v>1000</v></c><c r="B2" t="inlineStr"><is><t>Business Checking</t></is></c><c r="C2"><f>SUM(F2:F9)</f><v>500</v></c></row>` +
			`<row r="3"><c r="A3"><v>3000</v></c><c r="B3" t="inlineStr"><is><t>Owner's Equity</t></is></c><c r="D3"><v>500</v></c></row></sheetData></worksheet>`,
	} {
		w, e := z.Create(name)
		must(e)
		_, e = w.Write([]byte(body))
		must(e)
	}
	must(z.Close())
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	must(m.WriteField("period", "2026-08"))
	must(m.WriteField("one_client", "true"))
	f, e := m.CreateFormFile("file", "Acme August.xlsx")
	must(e)
	_, e = f.Write(zipped.Bytes())
	must(e)
	must(m.Close())
	workbookFile := send("user_b", preparer, "POST", "/v1/closes/client/imports?name=Acme%20Co", m.FormDataContentType(), b.Bytes(), 201)["id"].(string)
	confirmLayout(workbookFile, "2026-08-31")
	attest(workbookFile)
	mapAll(workbookFile)
	scopeRead := call("user_a", partner, "GET", "/v1/closes/client/import/scope?name=Acme%20Co&id="+workbookFile, nil, 200)
	if scopeRead["formulas"] != 1.0 {
		t.Fatalf("one month-end amount is a formula result: %v", scopeRead)
	}
	workbookPath := "/v1/closes/client/import/acceptance?name=Acme%20Co&id=" + workbookFile
	if c := checksOf(call("user_a", partner, "GET", workbookPath, nil, 200))["formulas"]; c["state"] != "holds" || len(c["items"].([]any)) != 1 {
		t.Fatalf("a formula balance holds: %v", c)
	}
	call("user_a", partner, "POST", "/v1/closes/client/import/scope?name=Acme%20Co&id="+workbookFile, map[string]any{"book": "Firm workbook", "complete": true, "basis": "accrual", "recalculated": true, "reason": "Recalculated after refreshing its links"}, 200)
	if c := checksOf(call("user_a", partner, "GET", workbookPath, nil, 200))["formulas"]; c["state"] != "note" {
		t.Fatalf("an attested recalculated copy releases it: %v", c)
	}
	if c := checksOf(call("user_a", partner, "GET", path, nil, 200))["formulas"]; c["state"] != "passes" {
		t.Fatalf("a CSV's amounts are stored values: %v", c)
	}
	// A workbook reading saved before formula results were marked says nothing of them, so it holds.
	_, err = db.Exec(`UPDATE books_layouts SET reading=reading-'formulas_marked' WHERE import_id::text=$1`, workbookFile)
	must(err)
	if c := checksOf(call("user_a", partner, "GET", workbookPath, nil, 200))["formulas"]; c["state"] != "holds" {
		t.Fatalf("an unmarked workbook reading holds: %v", c)
	}
	// The month's activity: none from a Trial Balance alone mid-year; from the year-to-date balances in the fiscal year's
	// first month; from a movement column whenever the file has one.
	if c := checksOf(call("user_a", partner, "GET", path, nil, 200))["activity"]; c["state"] != "holds" {
		t.Fatalf("September's activity is unavailable: %v", c)
	}
	january := send1("2027-01", "Number,Account,Debit,Credit\n1000,Business Checking,1500.25,\n3000,Owner's Equity,,1000.00\n4000,Sales,,700.25\n6000,Rent,200.00,\n")
	confirmLayout(january, "2027-01-31")
	attest(january)
	mapAll(january)
	januaryPath := "/v1/closes/client/import/acceptance?name=Acme%20Co&id=" + january
	// Only once the client's fiscal year is confirmed is January known to be its first month.
	if c := checksOf(call("user_a", partner, "GET", januaryPath, nil, 200))["activity"]; c["state"] != "holds" || !strings.Contains(c["detail"].(string), "not confirmed") {
		t.Fatalf("January with no confirmed fiscal year: %v", c)
	}
	_, err = db.Exec(`INSERT INTO client_profiles(organization_id,client_entity,effective_from,currency,fiscal_year_start,reason,actor) VALUES($1,'Acme Co','2026-08','USD',1,'Calendar year','fixture')`, org)
	must(err)
	if c := checksOf(call("user_a", partner, "GET", januaryPath, nil, 200))["activity"]; c["state"] != "passes" || !strings.Contains(c["detail"].(string), "revenue 700.25, expenses 200.00") {
		t.Fatalf("January's activity is its year-to-date balances: %v", c)
	}
	december := send1("2026-12", "Number,Account,Debit,Credit,Movement\n1000,Business Checking,1600.25,,\n3000,Owner's Equity,,1000.00,\n4000,Sales,,900.25,-200.00\n6000,Rent,300.00,,100.00\n")
	confirmColumns(december, "2026-12-31", map[string]string{"0": "account_number", "1": "account_name", "2": "debit", "3": "credit", "4": "movement"})
	attest(december)
	mapAll(december)
	if c := checksOf(call("user_a", partner, "GET", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+december, nil, 200))["activity"]; c["state"] != "passes" || !strings.Contains(c["detail"].(string), "movement column: revenue 200.00, expenses 100.00") {
		t.Fatalf("December's activity is its movement column: %v", c)
	}
	// A movement that does not read holds the month's activity rather than counting as nothing.
	november := send1("2026-11", "Number,Account,Debit,Credit,Movement\n1000,Business Checking,1600.25,,\n3000,Owner's Equity,,1000.00,\n4000,Sales,,900.25,-200.00\n6000,Rent,300.00,,n/a\n")
	confirmColumns(november, "2026-11-30", map[string]string{"0": "account_number", "1": "account_name", "2": "debit", "3": "credit", "4": "movement"})
	attest(november)
	mapAll(november)
	if c := checksOf(call("user_a", partner, "GET", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+november, nil, 200))["activity"]; c["state"] != "holds" || !strings.Contains(c["detail"].(string), "6000 Rent") {
		t.Fatalf("an unreadable movement holds: %v", c)
	}
	// Months on files: January accepted, its month open and its source its files, reads the books of its acceptance.
	januaryRead := call("user_a", partner, "GET", januaryPath, nil, 200)
	januaryAccepted := call("user_a", partner, "POST", januaryPath, with(januaryRead, map[string]any{"exported_on": "2026-10-05"}), 200)["id"].(string)
	var januaryClose string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2027-01') RETURNING id::text`, org).Scan(&januaryClose))
	call("user_a", []string{"integrations:manage", "clients:all"}, "POST", "/v1/closes/client/books-source", map[string]any{"client": "Acme Co", "period": "2027-01", "source": "files", "reason": "The client sends its books as files", "rev": ""}, 200)
	var bank, sales, class, unavailable, source string
	must(db.QueryRow(`SELECT balances->>'n:1000',activity->>'n:4000',accounts->'n:4000'->>'class',activity_unavailable,source_id FROM books_versions WHERE organization_id=$1 AND source='files' AND close_id::text=$2`, org, januaryClose).
		Scan(&bank, &sales, &class, &unavailable, &source))
	if bank != "1500.25" || sales != "700.25" || class != "Revenue" || unavailable != "" || source != januaryAccepted {
		t.Fatalf("January's books from its file: bank %s, sales %s (%s), unavailable %q, from %s", bank, sales, class, unavailable, source)
	}
	// A newer map reclassifying one of January's accounts holds the month's statements, the books staying as accepted;
	// the map going back releases them.
	var newer string
	must(db.QueryRow(`INSERT INTO books_account_maps(organization_id,client_entity,period,accounts,reason,actor) SELECT organization_id,client_entity,period,jsonb_set(accounts,'{n:4000,class}','"Expense"'),'Reclassified','fixture' FROM books_account_maps WHERE organization_id=$1 ORDER BY id DESC LIMIT 1 RETURNING id::text`, org).Scan(&newer))
	holds := func() []string {
		t.Helper()
		_, month, err := fileMonthBooks(t.Context(), db, januaryClose)
		must(err)
		return month.Holds
	}
	if h := holds(); !slices.Contains(h, "the account map now classifies one of its accounts differently; accept the file again") {
		t.Fatalf("a reclassified account holds the month's statements: %q", h)
	}
	_, err = db.Exec(`DELETE FROM books_account_maps WHERE id::text=$1`, newer)
	must(err)
	if h := holds(); slices.Contains(h, "the account map now classifies one of its accounts differently; accept the file again") {
		t.Fatalf("the month's statements stand on their map again: %q", h)
	}
	// They are the month's books, not a supplement to them.
	if supplement := call("user_a", partner, "GET", januaryPath, nil, 200)["supplement"]; supplement != nil {
		t.Fatalf("a files month's file is no supplement: %v", supplement)
	}
	// A replacement of January's accepted file that can stand as its books is the evidence that January changed; a file
	// sent beside it, or one not yet attested, is not.
	replace := func(content string) string {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		must(m.WriteField("period", "2027-01"))
		must(m.WriteField("one_client", "true"))
		must(m.WriteField("replaces", january))
		must(m.WriteField("replaces_reason", "Re-exported after a late correction"))
		f, e := m.CreateFormFile("file", "Acme 2027-01 corrected.csv")
		must(e)
		_, e = f.Write([]byte(content))
		must(e)
		must(m.Close())
		return send("user_b", preparer, "POST", "/v1/closes/client/imports?name=Acme%20Co", m.FormDataContentType(), b.Bytes(), 201)["id"].(string)
	}
	evidence := func() *priorReexport {
		t.Helper()
		found, err := priorReexportOf(t.Context(), db, org, "Acme Co", "2027-01", january)
		must(err)
		return found
	}
	beside := send1("2027-01", "Number,Account,Debit,Credit\n1000,Business Checking,1550.25,\n3000,Owner's Equity,,1000.00\n4000,Sales,,750.25\n6000,Rent,200.00,\n")
	confirmLayout(beside, "2027-01-31")
	attest(beside)
	if found := evidence(); found != nil {
		t.Fatalf("a file sent beside the accepted one is no re-export: %v", found)
	}
	corrected := replace("Number,Account,Debit,Credit\n1000,Business Checking,1600.25,\n3000,Owner's Equity,,1000.00\n4000,Sales,,800.25\n6000,Rent,200.00,\n")
	confirmLayout(corrected, "2027-01-31")
	if found := evidence(); found != nil {
		t.Fatalf("a replacement not yet attested is no evidence: %v", found)
	}
	attest(corrected)
	if found := evidence(); found == nil || found.name != "Acme 2027-01 corrected.csv" || found.balances["n:1000"] != "1600.25" {
		t.Fatalf("an attested replacement is the evidence of a change: %v", found)
	}
	// A newer replacement not yet attested leaves the earlier one the evidence.
	draft := replace("Number,Account,Debit,Credit\n1000,Business Checking,1700.25,\n3000,Owner's Equity,,1000.00\n4000,Sales,,900.25\n6000,Rent,200.00,\n")
	confirmLayout(draft, "2027-01-31")
	if found := evidence(); found == nil || found.balances["n:1000"] != "1600.25" {
		t.Fatalf("the newest replacement that can stand as the books: %v", found)
	}
	for _, id := range []string{draft, corrected, beside} {
		call("user_a", []string{"workpapers:review", "clients:all", "data:manage"}, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+id, map[string]any{"reason": "Checked"}, 200)
	}
	// The month's management pack reads them: its profit and loss from the file's activity, provisional.
	packOf := func() map[string]any {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		must(err)
		defer tx.Rollback()
		pack, err := closeManagementPack(t.Context(), tx, januaryClose)
		must(err)
		return pack
	}
	pack := packOf()
	net := pack["profit_and_loss"].(statement).Measures
	if pack["source"] != "files" || pack["file"] != "Acme 2027-01.csv" || pack["overall"] != "provisional" || pack["basis"] != "Accrual" || len(net) == 0 || net[len(net)-1].Amount != "500.25" || pack["profit_and_loss_unavailable"] != nil {
		t.Fatalf("January's pack from its file: %v", pack)
	}
	// December, no close, accepted from its file, is a reviewed comparative: January is compared with it, labelled, and
	// its status is left alone.
	if comparison := pack["comparison_unavailable"]; comparison != "Last month has no close in this workspace." {
		t.Fatalf("no comparison before December is accepted: %v", comparison)
	}
	call("user_a", partner, "POST", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+december, with(call("user_a", partner, "GET", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+december, nil, 200), map[string]any{"exported_on": "2026-10-05"}), 200)
	// Only once December's books are its files: until then its file supplements whatever its books are.
	if pack = packOf(); pack["prior"] != nil {
		t.Fatalf("a file of a month whose books are not its files is no comparative: %v", pack["prior"])
	}
	var rev string
	must(db.QueryRow(`SELECT COALESCE(max(id)::text,'') FROM books_sources WHERE organization_id=$1`, org).Scan(&rev))
	call("user_a", []string{"integrations:manage", "clients:all"}, "POST", "/v1/closes/client/books-source", map[string]any{"client": "Acme Co", "period": "2026-12", "source": "files", "reason": "The takeover's comparatives are files", "rev": rev}, 200)
	pack = packOf()
	if prior, _ := pack["prior"].(map[string]any); prior["status"] != "comparative" || prior["file"] != "Acme 2026-12.csv" || prior["as_of"] != "2026-12-31" || !strings.Contains(pack["note"].(string), "2026-12 is a reviewed comparative") || pack["overall"] != "provisional" {
		t.Fatalf("December as a reviewed comparative: %v %v", pack["prior"], pack["note"])
	}
	// February's ledger detail: its balances are January's accepted closing balances moved by its lines, January's
	// accounts without lines carried, and its activity its lines.
	february := send1("2027-02", "Date,Number,Account,Description,Debit,Credit\n02/20/2027,6000,Rent,February rent,200.00,\n02/20/2027,1000,Business Checking,February rent,,200.00\n")
	confirmColumns(february, "2027-02-28", map[string]string{"0": "date", "1": "account_number", "2": "account_name", "3": "memo", "4": "debit", "5": "credit"})
	attest(february)
	mapAll(february)
	februaryRead := call("user_a", partner, "GET", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+february, nil, 200)
	if februaryRead["can_accept"] != true || checksOf(februaryRead)["derived"]["state"] != "passes" {
		t.Fatalf("February's ledger rolls from January's books: %v", februaryRead)
	}
	februaryState, found, err := readFileState(t.Context(), db, org, "Acme Co", february)
	must(err)
	derived := map[string]string{}
	for _, a := range februaryState.reading.Accounts {
		derived[a.Key] = a.Balance
	}
	if activity, from, _ := monthActivity(februaryState); !found || derived["n:1000"] != "1300.25" || derived["n:6000"] != "400" || derived["n:3000"] != "-1000" || derived["n:4000"] != "-700.25" ||
		from != "movement" || activity["n:6000"].FloatString(2) != "200.00" || activity["n:4000"].Sign() != 0 {
		t.Fatalf("February's derived balances and activity: %v %v", derived, activity)
	}
	// Accepted for a February whose books are its files, it is the month's books version, its balances the derived ones.
	var februaryClose, sourceRev string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2027-02') RETURNING id::text`, org).Scan(&februaryClose))
	must(db.QueryRow(`SELECT COALESCE(max(id)::text,'') FROM books_sources WHERE organization_id=$1`, org).Scan(&sourceRev))
	call("user_a", []string{"integrations:manage", "clients:all"}, "POST", "/v1/closes/client/books-source", map[string]any{"client": "Acme Co", "period": "2027-02", "source": "files", "reason": "The client sends its ledger", "rev": sourceRev}, 200)
	call("user_a", partner, "POST", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+february, with(februaryRead, map[string]any{"exported_on": "2026-10-05"}), 200)
	februaryBooks, februaryMonth, err := fileMonthBooks(t.Context(), db, februaryClose)
	must(err)
	if februaryBooks == nil || februaryBooks.Balances["n:1000"].FloatString(2) != "1300.25" || februaryBooks.Balances["n:4000"].FloatString(2) != "-700.25" || len(februaryMonth.Holds) != 0 {
		t.Fatalf("February's books from its ledger: %v %v", februaryBooks, februaryMonth)
	}
	// Its lines are kept with its books: the transactions behind a figure, by account, searchable.
	lines := call("user_a", partner, "GET", "/v1/closes/"+februaryClose+"/files/ledger?account_id=n:6000", nil, 200)
	if rows, _ := lines["rows"].([]any); lines["total"] != 1.0 || len(rows) != 1 || fmt.Sprint(rows[0]) != "map[account:6000 Rent amount:200 date:2027-02-20 location:R2C5 memo:February rent]" {
		t.Fatalf("February's rent lines: %v", lines)
	}
	if found := call("user_a", partner, "GET", "/v1/closes/"+februaryClose+"/files/ledger?account_id=n:1000&query=FEBRUARY", nil, 200); found["total"] != 1.0 {
		t.Fatalf("a line found by its description: %v", found)
	}
	// A ledger's version written before its lines were kept has them added by the worker.
	_, err = db.Exec(`DELETE FROM books_lines WHERE version_id IN (SELECT id FROM books_versions WHERE close_id=$1::bigint AND source='files')`, februaryClose)
	must(err)
	_, err = writeFileVersions(t.Context(), db, org, "", "")
	must(err)
	if again := call("user_a", partner, "GET", "/v1/closes/"+februaryClose+"/files/ledger?account_id=n:6000", nil, 200); again["total"] != 1.0 {
		t.Fatalf("the lines of a version written before them: %v", again)
	}
	// February's approved accrual has no ledger to post to: it is exported in a journal batch, each line's memo carrying
	// the journal's reference, then recorded as entered in the client's ledger.
	var accrual string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1::bigint,'accrual','feb-accrual','{"input":{"source_reference":"INV-55"},"proposal":{"date":"2027-02-28","debit_account":"6000 Rent","credit_account":"2100 Accrued expenses","amount":"150.00"},"blockers":[]}') RETURNING id::text`, februaryClose).Scan(&accrual))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest) VALUES($1::bigint,'approved','Synthetic approval','fixture','feb-accrual')`, accrual)
	must(err)
	journalsPath := "/v1/closes/" + februaryClose + "/journals"
	toExport := call("user_a", partner, "GET", journalsPath, nil, 200)
	if pending, _ := toExport["pending"].([]any); len(pending) != 1 || pending[0].(map[string]any)["held"] != nil || len(pending[0].(map[string]any)["lines"].([]any)) != 2 {
		t.Fatalf("an approved entry waits to be exported: %v", toExport)
	}
	batch := call("user_a", partner, "POST", journalsPath, map[string]any{"action": "export", "confirmed": true}, 200)
	reference := "KR-" + februaryClose + "-1-1"
	if csvText := batch["csv"].(string); !strings.Contains(csvText, reference+",2027-02-28,6000 Rent,150.00,,"+reference+" accrual INV-55") || !strings.Contains(csvText, ",2100 Accrued expenses,,150.00,") {
		t.Fatalf("the batch of journals: %v", batch)
	}
	call("user_a", partner, "POST", journalsPath, map[string]any{"action": "export", "confirmed": true}, 409)
	// The batch downloads again as it was exported.
	r := httptest.NewRequest("GET", journalsPath+"/batches/1", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_b"}, Organization: org, Permissions: preparer}))
	w := httptest.NewRecorder()
	store.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != batch["csv"].(string) {
		t.Fatalf("the batch again: %d %s", w.Code, w.Body.String())
	}
	// Another draft of the same source is held: the source is already in a journal.
	var sameSource string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1::bigint,'accrual','feb-accrual-2','{"input":{"source_reference":"INV-55"},"proposal":{"date":"2027-02-28","debit_account":"6000 Rent","credit_account":"2100 Accrued expenses","amount":"150.00"},"blockers":[]}') RETURNING id::text`, februaryClose).Scan(&sameSource))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest) VALUES($1::bigint,'approved','Synthetic approval','fixture','feb-accrual-2')`, sameSource)
	must(err)
	exported := call("user_a", partner, "GET", journalsPath, nil, 200)
	journal := exported["journals"].([]any)[0].(map[string]any)
	if held := exported["pending"].([]any); len(held) != 1 || held[0].(map[string]any)["held"] != "Its source is already in journal "+reference+"." || journal["state"] != "exported" || journal["reference"] != reference {
		t.Fatalf("the exported journal: %v", exported)
	}
	call("user_a", partner, "POST", journalsPath, map[string]any{"action": "export", "confirmed": true}, 409)
	// Exported, it holds the month's sign-off until it is verified in the month's books.
	journalBlockers := func() []string {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		must(err)
		defer tx.Rollback()
		r, err := closeReadiness(t.Context(), tx, februaryClose)
		must(err)
		return slices.DeleteFunc(slices.Clone(r.Blockers), func(b string) bool { return !strings.Contains(b, "journal "+reference) })
	}
	if held := journalBlockers(); len(held) != 1 || !strings.Contains(held[0], "exported but not recorded as entered") {
		t.Fatalf("an exported journal holds the sign-off: %v", held)
	}
	// Recording a journal as entered takes posting:approve.
	entering := map[string]any{"action": "entered", "journal": journal["id"], "ledger_reference": "JE-77", "confirmed": true}
	call("user_a", partner, "POST", journalsPath, entering, 403)
	call("user_a", append([]string{"posting:approve"}, partner...), "POST", journalsPath, entering, 200)
	if entered := call("user_a", partner, "GET", journalsPath, nil, 200)["journals"].([]any)[0].(map[string]any); entered["state"] != "entered" || entered["ledger_reference"] != "JE-77" {
		t.Fatalf("an entered journal: %v", entered)
	}
	// Entered, it waits for a ledger that holds it: February's, exported before it was entered, neither verifies it nor
	// misses it, and the month's sign-off waits.
	if held := journalBlockers(); len(held) != 1 || !strings.Contains(held[0], "entered but not yet verified") {
		t.Fatalf("an entered journal holds the sign-off: %v", held)
	}
	// Its lines in the month's ledger, carrying its reference, verify it; found twice, it is uncertain.
	addLines := func(rows string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO books_lines(version_id,position,account_id,location,cells) SELECT $1::bigint,1000+n,k,'R'||n,jsonb_build_object('date','2027-02-28','account',a,'amount',m,'memo',$2::text||' accrual INV-55')
 FROM (VALUES `+rows+`) x(n,k,a,m)`, februaryMonth.Version, reference)
		must(err)
	}
	addLines(`(1,'n:6000','6000 Rent','150'),(2,'n:2100','2100 Accrued expenses','-150')`)
	if verified := call("user_a", partner, "GET", journalsPath, nil, 200)["journals"].([]any)[0].(map[string]any); verified["state"] != "verified" || !strings.Contains(verified["note"].(string), "Found in Acme 2027-02.csv") {
		t.Fatalf("a journal found in the month's ledger: %v", verified)
	}
	if held := journalBlockers(); len(held) != 0 {
		t.Fatalf("a verified journal holds no sign-off: %v", held)
	}
	addLines(`(3,'n:6000','Rent','150')`)
	if held := journalBlockers(); len(held) != 1 || !strings.Contains(held[0], "is uncertain. Found in Acme 2027-02.csv more than once: 3 lines carry its reference, for its 2.") {
		t.Fatalf("a journal found twice: %v", held)
	}
	// A journal the month's ledger holds any line of cannot be withdrawn; one it holds none of can, with why, and then
	// holds the sign-off no more.
	poster := append([]string{"posting:approve"}, partner...)
	withdrawing := map[string]any{"action": "withdraw", "journal": journal["id"], "reason": "Not imported: the client keyed it by hand", "confirmed": true}
	call("user_a", poster, "POST", journalsPath, withdrawing, 409)
	_, err = db.Exec(`DELETE FROM books_lines WHERE version_id=$1::bigint AND position>1000`, februaryMonth.Version)
	must(err)
	call("user_a", poster, "POST", journalsPath, withdrawing, 200)
	if held := journalBlockers(); len(held) != 0 {
		t.Fatalf("a withdrawn journal holds no sign-off: %v", held)
	}
	// A month whose books are a ledger's posts its entries instead.
	call("user_a", partner, "GET", "/v1/closes/"+octoberClose+"/journals", nil, 409)
	// March's ledger opens at February's derived balances, never at February's lines.
	march := send1("2027-03", "Date,Number,Account,Description,Debit,Credit\n03/15/2027,1000,Business Checking,Cash sale,50.00,\n03/15/2027,4000,Sales,Cash sale,,50.00\n")
	confirmColumns(march, "2027-03-31", map[string]string{"0": "date", "1": "account_number", "2": "account_name", "3": "memo", "4": "debit", "5": "credit"})
	attest(march)
	mapAll(march)
	marchState, _, err := readFileState(t.Context(), db, org, "Acme Co", march)
	must(err)
	marchBalances := map[string]string{}
	for _, a := range marchState.reading.Accounts {
		marchBalances[a.Key] = a.Balance
	}
	if !marchState.derived || marchBalances["n:1000"] != "1350.25" || marchBalances["n:4000"] != "-750.25" || marchBalances["n:6000"] != "400" {
		t.Fatalf("March rolls from February's derived balances: %v %s", marchBalances, marchState.underived)
	}
	// Published, its export names the file and the date its books are as of.
	frozen, _ := json.Marshal(pack)
	if rows, err := publicationRows(publication{Pack: frozen}); err != nil || !strings.Contains(fmt.Sprint(rows), "from the accepted file Acme 2027-01.csv, as of 2027-01-31") {
		t.Fatalf("a files month's pack is exported: %v %v", rows, err)
	}
	// Its files give no statement of cash flows: the firm's, imported, completes it, checked against the file's bank.
	if flow := pack["cash_flow"].(map[string]any); !strings.Contains(fmt.Sprint(flow["unavailable"]), "import the firm's statement") {
		t.Fatalf("a files month's pack asks for the firm's statement: %v", flow)
	}
	signer := append([]string{"close:signoff"}, partner...)
	imported := call("user_a", signer, "POST", "/v1/closes/"+januaryClose+"/cash-flow", map[string]any{"csv": "Section,Line,Amount\nBeginning cash,,1600.25\nOperating activities,Rent paid,-100.00\nEnding cash,,1500.25\n", "source": "January cash flow.csv", "confirmed": true}, 200)
	if flow := imported["cash_flow"].(map[string]any); flow["reconciled"] != true || !strings.Contains(imported["note"].(string), "imported from January cash flow.csv") {
		t.Fatalf("the firm's statement completes a files month's pack: %v %v", flow, imported["note"])
	}
	// The budget comparison reads them too: the file's profit and loss accounts are the ones a budget maps to.
	budget := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/budget", nil, 200)
	if accounts := budget["accounts"].([]any); len(accounts) != 2 || budget["reason"] == "Capture this month's QuickBooks period snapshot or Xero Trial Balance first." {
		t.Fatalf("January's budget from its file: %v", budget)
	}
	// The cash plan opens from the file's bank balances, as of the month's end, once its open items are sent as files.
	if plan := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/cash-plan", nil, 200); plan["state"] != "waiting" || !strings.Contains(plan["reason"].(string), "open receivables and payables") {
		t.Fatalf("a files month's plan waits for its open items: %v", plan)
	}
	openItemsAsOf := "2027-01-31"
	openItems := func(side, content string) {
		t.Helper()
		form := func(layout string) (string, []byte) {
			var b bytes.Buffer
			m := multipart.NewWriter(&b)
			must(m.WriteField("side", side))
			must(m.WriteField("as_of", openItemsAsOf))
			if layout != "" {
				must(m.WriteField("layout", layout))
				must(m.WriteField("confirmed", "true"))
			}
			f, e := m.CreateFormFile("file", side+".csv")
			must(e)
			_, e = f.Write([]byte(content))
			must(e)
			must(m.Close())
			return m.FormDataContentType(), b.Bytes()
		}
		kind, body := form("")
		layout, _ := json.Marshal(send("user_a", partner, "POST", "/v1/closes/"+januaryClose+"/open-item-files", kind, body, 200)["layout"])
		kind, body = form(string(layout))
		send("user_a", partner, "POST", "/v1/closes/"+januaryClose+"/open-item-files", kind, body, 201)
	}
	openItems("receivable", "Customer,Num,Due Date,Open Balance\nBeta LLC,1001,02/14/2027,250.00\n")
	openItems("payable", "Vendor,Num,Due Date,Open Balance\nLandlord,R-2,02/05/2027,100.00\n")
	// At the month's end, on accrual books, the open receivables reconcile to the receivables control accounts: January's
	// books hold none, so its 250.00 of open receivables are held, its payables likewise.
	if open := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/open-items", nil, 200); open["complete"] != false || !strings.Contains(open["completeness"].(string), "The open receivables total 250.00, but the books' accounts receivable accounts hold 0.00") {
		t.Fatalf("open items held by their control accounts: %v", open)
	}
	plan := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/cash-plan", nil, 200)
	if opening, _ := plan["opening"].(map[string]any); plan["state"] != "ready" || plan["start"] != "2027-02-01" || opening["amount"] != "1500.25" || plan["currency"] != "USD" ||
		plan["open_items"].(map[string]any)["missing_later"] != false || strings.Contains(fmt.Sprint(plan["readiness"]), "are not captured here") ||
		!strings.Contains(fmt.Sprint(plan["readiness"]), "The open receivables total 250.00") {
		t.Fatalf("a files month's plan opens from its file's bank balances: %v", plan)
	}
	// Open payables as of another date than the bank balances leave the plan a gap: a payment between them is in neither.
	openItemsAsOf = "2027-02-01"
	openItems("payable", "Vendor,Num,Due Date,Open Balance\nLandlord,R-2,02/05/2027,100.00\n")
	if gaps := fmt.Sprint(call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/cash-plan", nil, 200)["readiness"]); !strings.Contains(gaps, "the open payables are as of 2027-02-01: send them as of the same date") {
		t.Fatalf("a files month's cash and open items at different dates: %s", gaps)
	}
	// A reviewer records cash on 1 February as the bank's balance with an uncleared check bridging it to book cash; with
	// the open items as of that day too, the plan opens from it, the next day.
	observe := func(asOf string, want int) map[string]any {
		t.Helper()
		return call("user_a", partner, "POST", "/v1/closes/"+januaryClose+"/cash-observations", map[string]any{"kind": "bank", "as_of": asOf,
			"accounts": []map[string]string{{"name": "Business Checking", "balance": "1600.25"}},
			"bridge":   []map[string]string{{"account": "Business Checking", "description": "Check 1043 to Landlord", "amount": "-100.00", "evidence": "February bank statement, check register"}},
			"note":     "Bank balance read from online banking on 2 February", "confirmed": true}, want)
	}
	observe("2027-01-30", 422)
	if kept := observe("2027-02-01", 201); kept["book_cash"] != "1500.25" {
		t.Fatalf("the bank balance bridged to book cash: %v", kept)
	}
	openItems("receivable", "Customer,Num,Due Date,Open Balance\nBeta LLC,1001,02/14/2027,250.00\n")
	plan = call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/cash-plan", nil, 200)
	if opening, _ := plan["opening"].(map[string]any); plan["start"] != "2027-02-02" || opening["amount"] != "1500.25" || !strings.HasPrefix(opening["snapshot_id"].(string), "cash:") ||
		strings.Contains(fmt.Sprint(plan["readiness"]), "send them as of the same date") {
		t.Fatalf("the plan opens from the observed cash: %v", plan)
	}
	// The tie-out compares the file's balance-sheet accounts with their support: the bank, to be reconciled.
	tie := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/tie-out", nil, 200)
	if rows := tie["rows"].([]any); tie["snapshot"].(map[string]any)["source"] != "files" || len(rows) != 1 || rows[0].(map[string]any)["account_id"] != "n:1000" || rows[0].(map[string]any)["hint"] != "Reconcile it to the bank statement." {
		t.Fatalf("January's tie-out from its file: %v", tie)
	}
	// A reconciliation naming the account without its number supports it.
	_, err = db.Exec(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation','files-bank','{"input":{"account":"Business Checking","ledger_closing":"1500.25"}}')`, januaryClose)
	must(err)
	tie = call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/tie-out", nil, 200)
	if row := tie["rows"].([]any)[0].(map[string]any); row["status"] != "ties" || row["account"] != "1000 Business Checking" || len(tie["unmatched"].([]any)) != 0 {
		t.Fatalf("a reconciliation supports the file's bank: %v", tie)
	}
	// The client's page shows the month's books from files, and the plan reads them from the file.
	for _, raw := range call("user_a", partner, "GET", "/v1/closes/client?name=Acme%20Co", nil, 200)["months"].([]any) {
		if month := raw.(map[string]any); month["period"] == "2027-01" && fmt.Sprint(month["books"]) != "map[at:2026-10-05T00:00:00Z source:files]" {
			t.Fatalf("January's books on the client's page: %v", month["books"])
		}
	}
	planOf := func() planStep {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		must(err)
		defer tx.Rollback()
		b, err := readBooks(t.Context(), tx, januaryClose)
		must(err)
		return booksStep(b, januaryClose, "Acme Co", "2027-01")
	}
	if step := planOf(); step.State != "done" || step.Text != "Read from the accepted file Acme 2027-01.csv, exported Oct 5." {
		t.Fatalf("the plan reads January's books from its file: %v", step)
	}
	// While the accepted file's books are being written, the plan waits on them rather than asking for another acceptance.
	pendingTx, err := db.BeginTx(t.Context(), nil)
	must(err)
	_, err = pendingTx.Exec(`DELETE FROM books_versions WHERE close_id=$1::bigint AND source='files'`, januaryClose)
	must(err)
	pending, err := readBooks(t.Context(), pendingTx, januaryClose)
	pendingTx.Rollback()
	must(err)
	if step := booksStep(pending, januaryClose, "Acme Co", "2027-01"); step.State != "agent" || !strings.Contains(step.Text, "being written") {
		t.Fatalf("the plan waits on an accepted file's books: %v", step)
	}
	// The brief says the books are read from the file, with no ledger certification.
	ledgerLine := func() map[string]any {
		t.Helper()
		for _, raw := range call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/brief", nil, 200)["lines"].([]any) {
			if line := raw.(map[string]any); line["area"] == "ledger" {
				return line
			}
		}
		return nil
	}
	if line := ledgerLine(); line["state"] != "done" || line["text"] != "Books read from the accepted file Acme 2027-01.csv, exported Oct 5." {
		t.Fatalf("the brief reads January's books from its file: %v", line)
	}
	// Its exceptions check the file's balances, named as from the file.
	exceptions := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/quickbooks/exceptions", nil, 200)["balances"].(map[string]any)
	if snapshot := exceptions["snapshot"].(map[string]any); snapshot["source"] != "files" || snapshot["file"] != "Acme 2027-01.csv" || len(exceptions["items"].([]any)) != 0 {
		t.Fatalf("January's balances to review from its file: %v", exceptions)
	}
	// Its coverage is the file's accounts: the bank to reconcile, the rest for review, decided on the file's books.
	coverage := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/coverage", nil, 200)["coverage"].(map[string]any)
	kinds := map[string]string{}
	for _, raw := range coverage["items"].([]any) {
		item := raw.(map[string]any)
		kinds[item["account_id"].(string)] = item["kind"].(string)
	}
	if !strings.HasPrefix(coverage["snapshot_id"].(string), "file:") || len(kinds) != 4 || kinds["n:1000"] != "reconciliation" || kinds["n:3000"] != "accountant_review" {
		t.Fatalf("January's coverage from its file: %v", coverage)
	}
	call("user_a", partner, "POST", "/v1/closes/"+januaryClose+"/coverage", map[string]any{"action": "bind", "digest": coverage["digest"], "account_id": "n:3000", "excluded": true, "reason": "Equity is reviewed at year end", "confirmed": true}, 200)
	var bound int
	must(db.QueryRow(`SELECT count(*) FROM coverage_bindings WHERE close_id::text=$1 AND file_version_id IS NOT NULL AND account_id='n:3000' AND excluded`, januaryClose).Scan(&bound))
	if bound != 1 {
		t.Fatalf("a coverage decision on the file's books: %d", bound)
	}
	coverage = call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/coverage", nil, 200)["coverage"].(map[string]any)
	for _, raw := range coverage["items"].([]any) {
		if item := raw.(map[string]any); item["account_id"] == "n:3000" && item["state"] != "excluded" {
			t.Fatalf("the decision holds on the file's books: %v", item)
		}
	}
	// A request for the bank's statement, made on the file's books, stays current and waits on the client.
	call("user_a", partner, "POST", "/v1/closes/"+januaryClose+"/coverage", map[string]any{"action": "request", "digest": coverage["digest"], "accounts": []string{"n:1000"}, "recipient": "client@example.com", "body": "Please send January's bank statement.", "confirmed": true}, 200)
	var current, waiting bool
	must(db.QueryRow(`SELECT EXISTS(SELECT 1 FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id::text=$1 AND `+preparation.RequestCoverageIsCurrent+`)`, januaryClose).Scan(&current))
	must(db.QueryRow(`SELECT EXISTS(SELECT 1 FROM client_closes c WHERE c.id::text=$1 AND `+waitingStatements+`)`, januaryClose).Scan(&waiting))
	if !current || !waiting {
		t.Fatalf("a statement request on the file's books: current %v, waiting %v", current, waiting)
	}
	// Accepted again, the month's new books version keeps the exclusion, carried from the earlier one.
	januaryAccepted = call("user_a", partner, "POST", januaryPath, with(call("user_a", partner, "GET", januaryPath, nil, 200), map[string]any{"exported_on": "2026-10-05"}), 200)["id"].(string)
	again := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/coverage", nil, 200)["coverage"].(map[string]any)
	for _, raw := range again["items"].([]any) {
		if item := raw.(map[string]any); item["account_id"] == "n:3000" && (again["snapshot_id"] == coverage["snapshot_id"] || item["state"] != "excluded" || !strings.HasPrefix(item["reason"].(string), "Carried from an earlier capture")) {
			t.Fatalf("an exclusion carries to the file's new books: %v %v", again["snapshot_id"], item)
		}
	}
	// Without the month's activity, the profit and loss is unavailable, never zero.
	_, err = db.Exec(`UPDATE books_versions SET activity='{}',activity_unavailable='a column with the month''s movement' WHERE close_id::text=$1`, januaryClose)
	must(err)
	if pack = packOf(); pack["profit_and_loss_unavailable"] == nil || len(pack["profit_and_loss"].(statement).Sections) != 0 {
		t.Fatalf("a pack without activity: %v", pack["profit_and_loss_unavailable"])
	}
	if budget = call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/budget", nil, 200); budget["state"] != "waiting" || !strings.Contains(budget["reason"].(string), "activity is unavailable") {
		t.Fatalf("a budget comparison without activity waits: %v", budget)
	}
	// A check the file was accepted with that holds its statements holds the pack, named.
	_, err = db.Exec(`UPDATE books_import_acceptances SET checks=checks||'[{"key":"balanced","title":"Debits equal credits","state":"holds"}]'::jsonb WHERE id::text=$1`, januaryAccepted)
	must(err)
	if pack = packOf(); pack["state"] != "waiting" || !strings.Contains(pack["reason"].(string), "Debits equal credits") {
		t.Fatalf("a held file's pack waits: %v", pack)
	}
	if tie = call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/tie-out", nil, 200); !strings.Contains(fmt.Sprint(tie["error"]), "Debits equal credits") || len(tie["rows"].([]any)) != 0 {
		t.Fatalf("a held file's tie-out names its checks: %v", tie)
	}
	if exceptions = call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/quickbooks/exceptions", nil, 200)["balances"].(map[string]any); !strings.Contains(fmt.Sprint(exceptions["error"]), "Debits equal credits") {
		t.Fatalf("a held file's balances name its checks: %v", exceptions)
	}
	if step := planOf(); step.State != "you" || !strings.Contains(step.Text, "Debits equal credits") {
		t.Fatalf("the plan names a held file's checks: %v", step)
	}
	if line := ledgerLine(); line["state"] != "attention" || !strings.Contains(line["text"].(string), "Debits equal credits") {
		t.Fatalf("the brief names a held file's checks: %v", line)
	}
	// October was captured from a QuickBooks company: a reviewer confirms it is the files' company, its accounts mapped
	// across from a proposal by name.
	var qbo, captured string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) VALUES($1,'Acme Co','fixture','active',$2,now(),'123','Acme QBO') RETURNING id::text`, org, rand.Text()).Scan(&qbo))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, octoberClose, rand.Text()).Scan(&captured))
	ledgerAccounts := `[{"Id":"1","Name":"Business Checking","Classification":"Asset","AccountType":"Bank"},{"Id":"3","Name":"Owner's Equity","Classification":"Equity","AccountType":"Equity"},{"Id":"4","Name":"Sales","Classification":"Revenue","AccountType":"Income"},{"Id":"5","Name":"Rent","Classification":"Expense","AccountType":"Expense"}]`
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1::bigint,$2,'123',$3,$4,now(),'fixture')`, octoberClose, qbo, rand.Text(),
		`{"period":"2026-10","source_id":"`+captured+`","accounts":`+ledgerAccounts+`,"reports":{"TrialBalance":{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[]}},"ProfitAndLoss":{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Total"}]},"Rows":{"Row":[]}},"GeneralLedger":{}}}`)
	must(err)
	linksPath := "/v1/closes/client/company-links?name=Acme%20Co"
	links := call("user_a", partner, "GET", linksPath, nil, 200)
	companies := links["companies"].([]any)
	if len(companies) != 1 {
		t.Fatalf("the client's ledger companies: %v", links)
	}
	company := companies[0].(map[string]any)
	proposal := company["proposal"].(map[string]any)
	if company["ledger"] != "quickbooks" || company["company"] != "123" || company["name"] != "Acme QBO" || company["link"] != nil || proposal["n:1000"] != "1" || proposal["n:3000"] != "3" || proposal["n:4000"] != "4" || proposal["n:6000"] != "5" {
		t.Fatalf("a proposal by name: %v", company)
	}
	confirm := func(accounts map[string]any, basedOn string, want int) map[string]any {
		t.Helper()
		return call("user_a", partner, "POST", linksPath, map[string]any{"ledger": "quickbooks", "company": "123", "accounts": accounts, "reason": "The same company, moved to QuickBooks", "based_on": basedOn, "map": links["map"]}, want)
	}
	call("user_a", partner, "POST", linksPath, map[string]any{"ledger": "quickbooks", "company": "123", "accounts": proposal, "reason": "On an earlier map", "based_on": "", "map": "1:stale"}, 409)
	confirm(map[string]any{"n:1000": "1", "n:3000": "1"}, "", 422)
	linked := confirm(proposal, "", 200)["id"]
	confirm(proposal, "", 409)
	if link := call("user_a", partner, "GET", linksPath, nil, 200)["companies"].([]any)[0].(map[string]any)["link"].(map[string]any); link["id"] != linked || link["current"] != true || link["accounts"].(map[string]any)["n:4000"] != "4" {
		t.Fatalf("the confirmed link: %v", link)
	}
	// With the company confirmed, months compare across the sources through the accounts mapped across: November, on
	// files, with October's QuickBooks books; October's books with September's file, while an account September carries
	// is not mapped.
	packFor := func(closeID string) map[string]any {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		must(err)
		defer tx.Rollback()
		pack, err := closeManagementPack(t.Context(), tx, closeID)
		must(err)
		return pack
	}
	chooseFiles := func(period string) {
		t.Helper()
		var rev string
		must(db.QueryRow(`SELECT COALESCE(max(id)::text,'') FROM books_sources WHERE organization_id=$1`, org).Scan(&rev))
		call("user_a", []string{"integrations:manage", "clients:all"}, "POST", "/v1/closes/client/books-source", map[string]any{"client": "Acme Co", "period": period, "source": "files", "reason": "Kept on files", "rev": rev}, 200)
	}
	var novemberClose, septemberClose string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2026-11') RETURNING id::text`, org).Scan(&novemberClose))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Acme Co','2026-09') RETURNING id::text`, org).Scan(&septemberClose))
	novemberPath := "/v1/closes/client/import/acceptance?name=Acme%20Co&id=" + november
	call("user_a", partner, "POST", novemberPath, with(call("user_a", partner, "GET", novemberPath, nil, 200), map[string]any{"exported_on": "2026-10-05"}), 200)
	chooseFiles("2026-11")
	chooseFiles("2026-09")
	if pack := packFor(novemberClose); fmt.Sprint(pack["prior"].(map[string]any)["source"]) != "quickbooks" || !strings.Contains(pack["note"].(string), "confirmed as the files' company") {
		t.Fatalf("November on files compared with October's QuickBooks books: %v %v", pack["prior"], pack["comparison_unavailable"])
	}
	if pack := packFor(octoberClose); pack["prior"] != nil || !strings.Contains(fmt.Sprint(pack["comparison_unavailable"]), "Last month's activity is unavailable") {
		t.Fatalf("October's books with September's file, which gives no month's activity: %v %v", pack["prior"], pack["comparison_unavailable"])
	}
	// Parity compares captures with their versions; a file's version is its accepted reading, left alone.
	if _, mismatched, err := booksParity(t.Context(), db, org, 0); err != nil || len(mismatched) != 0 {
		t.Fatalf("parity leaves a file's books alone: %v %v", mismatched, err)
	}
	// A month whose books are its files is signed off over them, while no check it was accepted with holds them.
	readinessOf := func() readiness {
		t.Helper()
		tx, err := db.BeginTx(t.Context(), nil)
		must(err)
		defer tx.Rollback()
		r, err := closeReadiness(t.Context(), tx, januaryClose)
		must(err)
		return r
	}
	fromFiles := func(r readiness) []string {
		return slices.DeleteFunc(slices.Clone(r.Blockers), func(b string) bool { return !strings.HasPrefix(b, "Books from files") })
	}
	if held := fromFiles(readinessOf()); len(held) != 1 || !strings.Contains(held[0], "Debits equal credits") {
		t.Fatalf("a held file holds the sign-off, named: %v", held)
	}
	_, err = db.Exec(`UPDATE books_import_acceptances SET checks=(SELECT jsonb_agg(c) FROM jsonb_array_elements(checks) c WHERE c->>'key'<>'balanced' OR c->>'state'<>'holds') WHERE id::text=$1`, januaryAccepted)
	must(err)
	if r := readinessOf(); len(fromFiles(r)) != 0 {
		t.Fatalf("a files month with no check holding it can be signed off: %v", r.Blockers)
	}
	// Its scope defined and each account decided on the file's books, it is signed off over them, and so is its pack.
	_, err = db.Exec(`INSERT INTO close_scopes(close_id,scope,actor) VALUES($1::bigint,'{"description":"Bookkeeping close","required_papers":[],"external_steps":[]}','fixture')`, januaryClose)
	must(err)
	_, err = db.Exec(`INSERT INTO coverage_bindings(close_id,file_version_id,account_id,job_ids,excluded,reason,actor) SELECT $1::bigint,v.id,a,'[]',true,'Reviewed outside this close','fixture'
 FROM books_versions v CROSS JOIN unnest(ARRAY['n:1000','n:4000','n:6000']) a WHERE v.close_id=$1::bigint AND v.source='files' AND v.source_id=$2`, januaryClose, januaryAccepted)
	must(err)
	ready := readinessOf()
	if len(ready.Blockers) != 0 {
		t.Fatalf("January is ready to sign off: %v", ready.Blockers)
	}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1::bigint,'signed_off',$2,'{}','Signed off','fixture')`, januaryClose, ready.Digest)
	must(err)
	if pack = packOf(); pack["overall"] != "signed_off" || pack["open"] != false || strings.Contains(pack["note"].(string), "Provisional") {
		t.Fatalf("a signed-off files month's pack: %v %v %v", pack["overall"], pack["open"], pack["note"])
	}
	// A month signed off on the file's books holds the file, and another reading of it; reopened, deleting the file
	// takes its books with it.
	manager := []string{"workpapers:review", "clients:all", "data:manage"}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1::bigint,'signed_off',repeat('a',64),'{}','Signed off','fixture')`, januaryClose)
	must(err)
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+january, map[string]any{"reason": "Sent in error"}, 409)
	call("user_a", partner, "POST", januaryPath, with(call("user_a", partner, "GET", januaryPath, nil, 200), map[string]any{"exported_on": "2026-10-06"}), 409)
	// A re-export of January says how the month changed since its books were accepted, and what was built on them.
	_, err = db.Exec(`INSERT INTO management_publications(organization_id,close_id,version,pack,digest,commentary,published_by)
 SELECT $1,$2::bigint,1,jsonb_build_object('snapshot_id',v.id::text),'d','Published','fixture' FROM books_versions v WHERE v.close_id=$2::bigint AND v.source='files' ORDER BY v.id DESC LIMIT 1`, org, januaryClose)
	must(err)
	reexport := send1("2027-01", "Number,Account,Debit,Credit\n1000,Business Checking,1600.25,\n3000,Owner's Equity,,1000.00\n4000,Sales,,800.25\n6000,Rent,200.00,\n7000,Office,0.00,\n")
	confirmLayout(reexport, "2027-01-31")
	changed, _ := call("user_a", partner, "GET", "/v1/closes/client/import/acceptance?name=Acme%20Co&id="+reexport, nil, 200)["accepted_books"].(map[string]any)
	if changed["same"] != false || fmt.Sprint(changed["differences"]) != "[1000 Business Checking: was 1500.25, is now 1600.25. 4000 Sales: was -700.25, is now -800.25. 7000 Office: 0.00, not in the accepted books.]" ||
		fmt.Sprint(changed["effects"]) != "[The month is signed off on the accepted books: reopen it before accepting this file. 1 management pack publication for the month was built on the accepted books.]" {
		t.Fatalf("a re-export of an accepted month: %v", changed)
	}
	if call("user_a", partner, "GET", januaryPath, nil, 200)["accepted_books"] != nil {
		t.Fatalf("the accepted file itself is not a change to its month")
	}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1::bigint,'reopened',repeat('a',64),'{}','Reopened','fixture')`, januaryClose)
	must(err)
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+january, map[string]any{"reason": "Sent in error"}, 200)
	var left int
	must(db.QueryRow(`SELECT count(*) FROM books_versions WHERE organization_id=$1 AND source='files' AND close_id::text=$2`, org, januaryClose).Scan(&left))
	if left != 0 {
		t.Fatalf("a deleted file's books go with it: %d", left)
	}
	// A ledger file is read line by line; its month-end balances arrive with the next step, so it is not yet accepted.
	ledger := send1("2026-08", "Date,Account,Description,Debit,Credit\n08/25/2026,6000 Rent,August rent,200.00,\n08/25/2026,1000 Business Checking,August rent,,200.00\n")
	confirmColumns(ledger, "2026-08-31", map[string]string{"0": "date", "1": "account_name", "2": "memo", "3": "debit", "4": "credit"})
	attest(ledger)
	ledgerPath := "/v1/closes/client/import/acceptance?name=Acme%20Co&id=" + ledger
	ledgerRead := call("user_a", partner, "GET", ledgerPath, nil, 200)
	if ledgerRead["can_accept"] != false || !strings.Contains(fmt.Sprint(ledgerRead["ledger"]), "ledger detail") {
		t.Fatalf("a ledger file waits for its balances: %v", ledgerRead)
	}
	call("user_a", partner, "POST", ledgerPath, with(ledgerRead, map[string]any{"exported_on": "2026-10-05"}), 409)
	// With file imports switched off, the checks are read, and nothing is accepted.
	_, err = db.Exec(`UPDATE organizations SET file_imports=false WHERE id=$1`, org)
	must(err)
	if read = call("user_b", preparer, "GET", path, nil, 200); read["can_accept"] != false {
		t.Fatalf("no acceptance while file imports are off: %v", read)
	}
	call("user_a", partner, "POST", path, with(read, nil), 403)
}

// A month on files' balances are reviewed as a capture's are, each account known by its name without its number: an
// overdrawn bank and opening balance equity, but not accumulated depreciation or retained earnings, which sit opposite.
func TestAFilesMonthsBalancesAreReviewed(t *testing.T) {
	amount := func(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }
	b := &books{Currency: "USD", Accounts: map[string]packAccount{
		"n:1000": {Name: "1000 Business Checking", Class: "Asset", Type: "Bank"},
		"n:1590": {Name: "1590 Accumulated Depreciation", Class: "Asset", Type: "Fixed Asset"},
		"n:3200": {Name: "3200 Retained Earnings", Class: "Equity", Type: "Equity"},
		"n:3900": {Name: "3900 Opening Balance Equity", Class: "Equity", Type: "Equity"},
	}, Balances: map[string]*big.Rat{"n:1000": amount("-50"), "n:1590": amount("-200"), "n:3200": amount("300"), "n:3900": amount("-75")}}
	found := map[string]string{}
	for _, e := range fileBalanceExceptions(b) {
		found[e.AccountID] = e.Type
	}
	if len(found) != 2 || found["n:1000"] != "abnormal_balance" || found["n:3900"] != "opening_balance_equity" {
		t.Fatalf("a files month's balances to review: %v", found)
	}
}

// A file with openings rolls from the prior accepted month account by account: an opening column, or the month-end
// balance less the month's movement. A difference is unexplained and named; at the fiscal year's start the profit and
// loss opens at zero, the year's result carried into retained earnings. An amount not given exactly is never zero.
func TestAFileRollsForwardFromThePriorMonth(t *testing.T) {
	account := func(number, name, balance, movement, class string) fileAccount {
		return fileAccount{Key: "n:" + number, Number: number, Name: name, Balance: balance, Movement: movement, Mapped: &mappedAccount{Class: class}}
	}
	columns := func(meanings ...bookfiles.Meaning) *confirmedRead {
		read := &confirmedRead{Layout: bookfiles.Layout{Columns: map[int]bookfiles.Meaning{0: bookfiles.AccountNumber, 1: bookfiles.Balance}}}
		for i, m := range meanings {
			read.Layout.Columns[2+i] = m
		}
		return read
	}
	s := fileState{layout: columns(bookfiles.Movement), prior: "2026-11", priorIsBooks: true, priorBalances: map[string]string{"n:1000": "1000", "n:3000": "-1000", "n:4000": "-500", "n:6000": "500"},
		classes: map[string]string{"n:1000": "Asset", "n:3000": "Equity", "n:4000": "Revenue", "n:6000": "Expense"}}
	s.reading = fileReading{Period: "2026-12", Accounts: []fileAccount{
		account("1000", "Business Checking", "1200", "200", "Asset"),
		account("3000", "Retained Earnings", "-1000", "", "Equity"),
		account("4000", "Sales", "-700", "-200", "Revenue"),
		account("6000", "Rent", "500", "", "Expense"),
	}}
	if c := rollForward(s); c.State != "passes" || c.Detail != "Every account opens at November 2026's accepted closing balance." {
		t.Fatalf("a month that rolls: %+v", c)
	}
	// A prior month whose books are a ledger's: its accepted file supplements them, and nothing rolls from it.
	s.priorIsBooks = false
	if c := rollForward(s); c.State != "note" {
		t.Fatalf("a prior month whose file only supplements its books: %+v", c)
	}
	s.priorIsBooks = true
	s.reading.Accounts[0].Movement = "150"
	if c := rollForward(s); c.State != "holds" || c.Detail != "1 account does not roll from November 2026's accepted books, unexplained: 1000 Business Checking opens at 1050.00, closed at 1000.00 (50.00)." {
		t.Fatalf("a month that does not roll: %+v", c)
	}
	// A re-export of November closing where December opens is the evidence that November changed after it was accepted.
	s.reexport = &priorReexport{"Acme 2026-11 again.csv", map[string]string{"n:1000": "1050", "n:3000": "-1000", "n:4000": "-500", "n:6000": "500"}}
	if c := rollForward(s); c.State != "holds" || c.Detail != "1 account does not roll from November 2026's accepted books, November 2026 changed after it was accepted, as its re-export Acme 2026-11 again.csv shows: 1000 Business Checking opens at 1050.00, closed at 1000.00 (50.00)." || !strings.Contains(c.Holds, "re-export is accepted") {
		t.Fatalf("a difference the prior month's re-export explains: %+v", c)
	}
	s.reexport = nil
	// A movement the file does not give exactly is never taken as zero.
	s.reading.Accounts[0].Movement, s.reading.Accounts[0].MovementHeld = "", "unreadable"
	if c := rollForward(s); c.State != "holds" || !strings.Contains(c.Detail, "1000 Business Checking: unreadable") {
		t.Fatalf("an unreadable movement: %+v", c)
	}
	// January, the fiscal year's first month: the profit and loss opens at zero, last year's result in retained earnings.
	s.fiscalStart, s.prior, s.reading.Period = 1, "2026-12", "2027-01"
	s.priorBalances = map[string]string{"n:1000": "1000", "n:3000": "-1000", "n:4000": "-800", "n:6000": "500"}
	s.reading.Accounts = []fileAccount{
		account("1000", "Business Checking", "1200", "200", "Asset"),
		account("3000", "Retained Earnings", "-1300", "", "Equity"),
		account("4000", "Sales", "-300", "-300", "Revenue"),
		account("6000", "Rent", "100", "100", "Expense"),
	}
	if c := rollForward(s); c.State != "passes" || !strings.Contains(c.Detail, "the year's result carried into retained earnings") {
		t.Fatalf("the year's closing entries are modelled: %+v", c)
	}
	// Opening and movement columns: the opening and the movement make the balance, a blank movement being zero.
	s.layout = columns(bookfiles.Opening, bookfiles.Movement)
	for i, o := range []string{"1000", "-1300", "0", "0"} {
		s.reading.Accounts[i].Opening = o
	}
	if c := rollForward(s); c.State != "passes" {
		t.Fatalf("openings that roll and make the balances: %+v", c)
	}
	s.reading.Accounts[0].Balance = "1250"
	if c := rollForward(s); c.State != "holds" || !strings.Contains(c.Detail, "1000 Business Checking opens at 1000.00 and moves by 200.00, but closes at 1250.00") {
		t.Fatalf("an opening and movement that do not make the balance: %+v", c)
	}
	s.reading.Accounts[0].Balance = "1200"
	// A formula's opening holds until a recalculated copy is attested.
	s.reading.Accounts[0].OpeningHeld = "formula"
	if c := rollForward(s); c.State != "holds" || !strings.Contains(c.Detail, "1000 Business Checking: formula") {
		t.Fatalf("a formula's opening: %+v", c)
	}
	s.scope = &importScope{Recalculated: true}
	if c := rollForward(s); c.State != "passes" {
		t.Fatalf("a recalculated copy's opening: %+v", c)
	}
	// With no prior month the openings are not compared, but the file's own figures must agree: an amount that cannot be
	// read, or an opening and movement that do not make the balance, hold.
	s.prior = ""
	if c := rollForward(s); c.State != "note" {
		t.Fatalf("no prior month to roll from: %+v", c)
	}
	s.reading.Accounts[0].OpeningHeld = "rounded"
	if c := rollForward(s); c.State != "holds" {
		t.Fatalf("a first month whose figures cannot be checked: %+v", c)
	}
	s.reading.Accounts[0].OpeningHeld, s.reading.Accounts[0].Movement = "", ""
	if c := rollForward(s); c.State != "holds" || !strings.Contains(c.Detail, "1000 Business Checking opens at 1000.00 and moves by 0.00, but closes at 1200.00") {
		t.Fatalf("a blank movement is zero in a first month's arithmetic: %+v", c)
	}
	// With only an opening column, a first month's openings are a note, whatever they are.
	s.layout = columns(bookfiles.Opening)
	s.reading.Accounts[0].OpeningHeld = "rounded"
	if c := rollForward(s); c.State != "note" {
		t.Fatalf("a first month with openings alone: %+v", c)
	}
	s.layout = columns()
	if c := rollForward(s); c.Key != "" {
		t.Fatalf("a file without openings has no roll-forward: %+v", c)
	}
}

// An accepted file reconciles to a live month's books account by account, each matched by its name without its number,
// or a parent's last part; a difference, or a balance on one side only, is named.
func TestAFileReconcilesToTheMonthsBooks(t *testing.T) {
	amount := func(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }
	f := acceptedFile{accounts: map[string]booksAccount{"n:1000": {Name: "1000 Business Checking"}, "n:4000": {Name: "4000 Sales"}, "n:6000": {Name: "6000 Rent"}},
		balances: map[string]string{"n:1000": "1500.25", "n:4000": "-700.25", "n:6000": "0"}}
	captured := &books{Accounts: map[string]packAccount{"35": {Name: "Business Checking"}, "79": {Name: "Income:Sales"}, "12": {Name: "Undeposited Funds"}},
		Balances: map[string]*big.Rat{"35": amount("1500.25"), "79": amount("-700.25"), "12": amount("0")}}
	if matched, differences := reconcileFile(f, captured); matched != 2 || len(differences) != 0 {
		t.Fatalf("a file that agrees: %d %v", matched, differences)
	}
	captured.Balances["35"] = amount("1400")
	captured.Accounts["50"], captured.Balances["50"] = packAccount{Name: "Savings"}, amount("10")
	matched, differences := reconcileFile(f, captured)
	if matched != 2 || len(differences) != 2 || differences[0] != "1000 Business Checking: the file says 1500.25, the books 1400.00, a difference of 100.25." || differences[1] != "Savings: 10.00 in the books, not in the file." {
		t.Fatalf("a file that differs: %d %q", matched, differences)
	}
}

// Books re-keyed through a confirmed company link carry each mapped account across; an unmapped account with a balance
// or activity keeps the months apart, named.
func TestBooksAreRelinkedThroughTheMappedAccounts(t *testing.T) {
	amount := func(s string) *big.Rat { r, _ := new(big.Rat).SetString(s); return r }
	b := &books{Currency: "USD", Accounts: map[string]packAccount{"1": {Name: "Checking", Class: "Asset"}, "4": {Name: "Sales", Class: "Revenue"}, "9": {Name: "Old", Class: "Asset"}},
		Balances: map[string]*big.Rat{"1": amount("100"), "4": amount("-50"), "9": amount("0")}, Activity: map[string]*big.Rat{"4": amount("50")}}
	out, missing := relinked(b, map[string]string{"1": "n:1000", "4": "n:4000"}, nil, false)
	if missing != "" || out.Balances["n:1000"].Cmp(amount("100")) != 0 || out.Activity["n:4000"].Cmp(amount("50")) != 0 || out.Accounts["n:4000"].Name != "Sales" {
		t.Fatalf("books relinked: %v %v", out, missing)
	}
	if out.Typed || out.group("n:1000", nil) != "Assets" {
		t.Fatalf("relinked books group as an untyped pack does: %v", out.group("n:1000", nil))
	}
	// Into typed books, an account with no type takes the one the other side describes it with, else its class's general
	// type (revenue is Income); one with its own type keeps it, which last month's checks read.
	out, _ = relinked(b, map[string]string{"1": "n:1000", "4": "n:4000"}, map[string]packAccount{"n:1000": {Name: "1000 Business Checking", Class: "Asset", Type: "Bank"}}, true)
	if out.Accounts["n:1000"].Type != "Bank" || out.Accounts["n:4000"].Type != "Income" {
		t.Fatalf("relinked into typed books: %v", out.Accounts)
	}
	b.Accounts["1"] = packAccount{Name: "Checking", Class: "Asset", Type: "Other Current Asset"}
	if out, _ = relinked(b, map[string]string{"1": "n:1000", "4": "n:4000"}, map[string]packAccount{"n:1000": {Type: "Bank"}}, true); out.Accounts["n:1000"].Type != "Other Current Asset" {
		t.Fatalf("an account keeps its own type: %v", out.Accounts["n:1000"])
	}
	b.Balances["9"] = amount("10")
	if out, missing = relinked(b, map[string]string{"1": "n:1000", "4": "n:4000"}, nil, true); out != nil || missing != "Old" {
		t.Fatalf("an unmapped account with a balance: %v %q", out, missing)
	}
}
