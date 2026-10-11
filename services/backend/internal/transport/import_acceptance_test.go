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
		for _, q := range []string{`DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
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
	pack = packOf()
	if prior, _ := pack["prior"].(map[string]any); prior["status"] != "comparative" || prior["file"] != "Acme 2026-12.csv" || prior["as_of"] != "2026-12-31" || !strings.Contains(pack["note"].(string), "2026-12 is a reviewed comparative") || pack["overall"] != "provisional" {
		t.Fatalf("December as a reviewed comparative: %v %v", pack["prior"], pack["note"])
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
	januaryAccepted = call("user_a", partner, "POST", januaryPath, with(januaryRead, map[string]any{"exported_on": "2026-10-05"}), 200)["id"].(string)
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
	must(db.QueryRow(`SELECT count(*) FROM books_versions WHERE organization_id=$1 AND source='files'`, org).Scan(&left))
	if left != 0 {
		t.Fatalf("a deleted file's books go with it: %d", left)
	}
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
	s := fileState{layout: columns(bookfiles.Movement), prior: "2026-11", priorBalances: map[string]string{"n:1000": "1000", "n:3000": "-1000", "n:4000": "-500", "n:6000": "500"},
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
	s.reading.Accounts[0].Movement = "150"
	if c := rollForward(s); c.State != "holds" || c.Detail != "1 account does not roll from November 2026's accepted books, unexplained: 1000 Business Checking opens at 1050.00, closed at 1000.00 (50.00)." {
		t.Fatalf("a month that does not roll: %+v", c)
	}
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
