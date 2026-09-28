package transport

import (
	"crypto/rand"
	"encoding/csv"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestStaffSearchAndExportTheAuditAcrossFirms(t *testing.T) {
	db := adminDB(t)
	t.Setenv("WORKOS_CLIENT_ID", "")
	token := strings.Repeat("u", 32)
	firmA, firmB := "org_AuditA"+rand.Text(), "org_AuditB"+rand.Text()
	staff := "staff_audit_" + strings.ToLower(rand.Text())
	if _, err := db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Audit A'),($2,'Audit B')`, firmA, firmB); err != nil {
		t.Fatal(err)
	}
	// Entries as staff actions leave them: one per firm, and one on no firm.
	if _, err := db.Exec(`INSERT INTO platform_audit(staff_user,action,organization_id,target_type,target_id,reason,request,result,error) VALUES
 ($1,'plan.approve',$2,'plan','7','Reviewed the Acme Secret plan','{"digest":"abc"}','ok',''),
 ($1,'firm.suspend',$3,'firm',$3,'=cmd|'' /C calc''!A0','{}','refused','=HYPERLINK("http://example.test")'),
 ($1,'sessions.revoke',NULL,'user','user_x','Laptop stolen from Acme Secret','{"app_sessions":1}','ok','')`, staff, firmA, firmB); err != nil {
		t.Fatal(err)
	}
	h := apiWithStore(db, token)
	search := func(query string) []any {
		t.Helper()
		return adminJSON(t, h, token, "GET", "/v1/admin/audit?staff="+staff+query, "", 200)["entries"].([]any)
	}

	// Filters: firm, entries on no firm, action start, result.
	if got := search(""); len(got) != 3 {
		t.Fatalf("all of the staff member's entries: %v", got)
	}
	if got := search("&firm=" + firmA); len(got) != 1 || !strings.Contains(mustString(got), `"action":"plan.approve"`) {
		t.Fatalf("one firm: %v", got)
	}
	if got := search("&firm=platform"); len(got) != 1 || !strings.Contains(mustString(got), `"organization":null`) {
		t.Fatalf("entries on no firm: %v", got)
	}
	if got := search("&action=plan."); len(got) != 1 {
		t.Fatalf("action start: %v", got)
	}
	if got := search("&result=refused"); len(got) != 1 || !strings.Contains(mustString(got), `"action":"firm.suspend"`) {
		t.Fatalf("refusals: %v", got)
	}
	// Paging by ID.
	newest := search("")
	oldestShown := int64(newest[1].(map[string]any)["id"].(float64))
	if older := search("&before=" + strconv.FormatInt(oldestShown, 10)); len(older) != 1 {
		t.Fatalf("before %d: %v", oldestShown, older)
	}

	// Free text is masked until revealed, and a reveal is in each firm's audit (and at platform level).
	if text := mustString(search("")); strings.Contains(text, "Acme") || strings.Contains(text, "abc") {
		t.Fatalf("masked audit: %s", text)
	}
	revealed := adminJSON(t, h, token, "GET", "/v1/admin/audit?staff="+staff+"&reveal=1", "", 200, "X-Access-Reason", "quarterly access review")
	if text := mustString(revealed); !strings.Contains(text, "Reviewed the Acme Secret plan") {
		t.Fatalf("revealed audit: %s", text)
	}
	for _, firm := range []string{firmA, firmB} {
		var reveals int
		if db.QueryRow(`SELECT count(*) FROM platform_audit WHERE organization_id=$1 AND action='content.reveal' AND target_type='audit' AND target_id='search'`, firm).Scan(&reveals); reveals != 1 {
			t.Fatalf("%s's audit must show the reveal: %d", firm, reveals)
		}
	}

	// The export: CSV of the same search, masked unless revealed, never runnable as a spreadsheet formula.
	export := func(query string, header ...string) (*httptest.ResponseRecorder, [][]string) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/admin/audit/export?staff="+staff+query, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		for i := 0; i+1 < len(header); i += 2 {
			r.Header.Set(header[i], header[i+1])
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("export: %d %s", w.Code, w.Body.String())
		}
		rows, err := csv.NewReader(strings.NewReader(w.Body.String())).ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		return w, rows
	}
	w, rows := export("")
	if w.Header().Get("Content-Type") != "text/csv; charset=utf-8" || w.Header().Get("X-Truncated") != "false" || len(rows) != 4 || rows[0][4] != "action" {
		t.Fatalf("masked export: %v %v", w.Header(), rows)
	}
	if strings.Contains(w.Body.String(), "Acme") {
		t.Fatalf("the masked export shows free text: %s", w.Body.String())
	}
	_, rows = export("&reveal=1", "X-Access-Reason", "exporting for the auditor")
	joined := mustString(rows)
	if !strings.Contains(joined, "Acme Secret") || !strings.Contains(joined, `'=cmd|`) || !strings.Contains(joined, `'=HYPERLINK`) {
		t.Fatalf("revealed export, formulas neutralized: %v", rows)
	}
	for value, want := range map[string]string{"\n=1+1": "'\n=1+1", " =1": "' =1", "plain": "plain", "-5 days": "'-5 days", "": ""} {
		if got := spreadsheetSafe(value); got != want {
			t.Errorf("spreadsheetSafe(%q) = %q, want %q", value, got, want)
		}
	}
	// A reveal says what it showed.
	var covered string
	if err := db.QueryRow(`SELECT request::text FROM platform_audit WHERE organization_id=$1 AND action='content.reveal' AND target_type='audit' AND target_id='export'`, firmA).Scan(&covered); err != nil || !strings.Contains(covered, `"staff": "`+staff+`"`) || !strings.Contains(covered, `"entries": 3`) {
		t.Fatalf("reveal scope %q %v", covered, err)
	}
	adminJSON(t, h, token, "GET", "/v1/admin/audit?before=zero", "", 422)
	// A reveal of one firm's search is that firm's, even when it shows nothing.
	adminJSON(t, h, token, "GET", "/v1/admin/audit?firm="+firmA+"&staff=nobody&reveal=1", "", 200, "X-Access-Reason", "checking an empty firm search")
	var firmEmpty int
	if db.QueryRow(`SELECT count(*) FROM platform_audit WHERE organization_id=$1 AND action='content.reveal' AND request->>'staff'='nobody'`, firmA).Scan(&firmEmpty); firmEmpty != 1 {
		t.Fatalf("an empty firm reveal belongs to the firm: %d", firmEmpty)
	}
	// A reveal that shows nothing is still recorded, at platform level.
	nobody := "staff_nobody_" + strings.ToLower(rand.Text())
	adminJSON(t, h, token, "GET", "/v1/admin/audit?staff="+nobody+"&reveal=1", "", 200, "X-Access-Reason", "checking an empty search")
	var empty string
	if err := db.QueryRow(`SELECT request::text FROM platform_audit WHERE organization_id IS NULL AND action='content.reveal' AND target_type='audit' AND request->>'staff'=$1`, nobody).Scan(&empty); err != nil || !strings.Contains(empty, `"entries": 0`) {
		t.Fatalf("empty reveal %q %v", empty, err)
	}
}
