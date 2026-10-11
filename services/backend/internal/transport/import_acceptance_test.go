package transport

import (
	"archive/zip"
	"bytes"
	"close.local/backend/internal/preparation"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
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
		for _, q := range []string{`DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
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
	// Its files give no statement of cash flows: the firm's, imported, completes it, checked against the file's bank.
	if flow := pack["cash_flow"].(map[string]any); !strings.Contains(fmt.Sprint(flow["unavailable"]), "import the firm's statement") {
		t.Fatalf("a files month's pack asks for the firm's statement: %v", flow)
	}
	signer := append([]string{"close:signoff"}, partner...)
	imported := call("user_a", signer, "POST", "/v1/closes/"+januaryClose+"/cash-flow", map[string]any{"csv": "Section,Line,Amount\nBeginning cash,,1000\nOperating activities,Receipts from customers,500.25\nEnding cash,,1500.25\n", "source": "January cash flow.csv", "confirmed": true}, 200)
	if flow := imported["cash_flow"].(map[string]any); flow["reconciled"] != true || !strings.Contains(imported["note"].(string), "imported from January cash flow.csv") {
		t.Fatalf("the firm's statement completes a files month's pack: %v %v", flow, imported["note"])
	}
	// The budget comparison reads them too: the file's profit and loss accounts are the ones a budget maps to.
	budget := call("user_a", partner, "GET", "/v1/closes/"+januaryClose+"/budget", nil, 200)
	if accounts := budget["accounts"].([]any); len(accounts) != 2 || budget["reason"] == "Capture this month's QuickBooks period snapshot or Xero Trial Balance first." {
		t.Fatalf("January's budget from its file: %v", budget)
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
	// Parity compares captures with their versions; a file's version is its accepted reading, left alone.
	if _, mismatched, err := booksParity(t.Context(), db, org, 0); err != nil || len(mismatched) != 0 {
		t.Fatalf("parity leaves a file's books alone: %v %v", mismatched, err)
	}
	// A month whose books are its files is not signed off yet.
	tx, err := db.BeginTx(t.Context(), nil)
	must(err)
	readiness, err := closeReadiness(t.Context(), tx, januaryClose)
	tx.Rollback()
	must(err)
	if !slices.ContainsFunc(readiness.Blockers, func(b string) bool { return strings.HasPrefix(b, "Books from files") }) {
		t.Fatalf("a files month waits to be signed off: %v", readiness.Blockers)
	}
	// A month signed off on the file's books holds the file, and another reading of it; reopened, deleting the file
	// takes its books with it.
	manager := []string{"workpapers:review", "clients:all", "data:manage"}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1::bigint,'signed_off',repeat('a',64),'{}','Signed off','fixture')`, januaryClose)
	must(err)
	call("user_a", manager, "POST", "/v1/closes/client/import/delete?name=Acme%20Co&id="+january, map[string]any{"reason": "Sent in error"}, 409)
	call("user_a", partner, "POST", januaryPath, with(call("user_a", partner, "GET", januaryPath, nil, 200), map[string]any{"exported_on": "2026-10-06"}), 409)
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
