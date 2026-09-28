package transport

import (
	"bytes"
	"close.local/backend/internal/documents"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestStatementRowsNeedEveryPageRowAndBalanceChecked(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Statement" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Statement fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Harbor',true,'Synthetic','fixture')`, org)
	must(err)
	var closeID, pdf, partial string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Harbor','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM statement_extractions WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM model_calls WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	text := "--- Page 1 · PDF text ---\nHarbor Bank Statement\nAccount: Checking ****1234\nStatement period 2026-09-01 to 2026-09-30\nOpening balance 1,000.00\n2026-09-02 Deposit ACME 200.00\n2026-09-05 Coffee card 4111 1111 1111 1111 -25.00\nPage 1 of 2\n--- Page 2 · PDF text ---\n2026-09-12 Rent -500.00\nClosing balance 675.00\nPage 2 of 2\n"
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,original,extraction_state) VALUES($1,'statement.pdf','stmt-full',$2,'fixture','\x255044462d'::bytea,'ready') RETURNING id::text`, closeID, text).Scan(&pdf))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,original,extraction_state) VALUES($1,'partial.pdf','stmt-partial',$2,'fixture','\x255044462d'::bytea,'ready') RETURNING id::text`, closeID, strings.ReplaceAll(text, "of 2", "of 3")).Scan(&partial))
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	row := func(page int, date, amount, quote string, certain bool) map[string]any {
		return map[string]any{"page": page, "date": date, "description": "Row", "amount": amount, "quote": quote, "sign_certain": certain}
	}
	reading, _ := json.Marshal(map[string]any{"rows": []any{
		row(1, "2026-09-02", "200.00", "2026-09-02 Deposit ACME 200.00", true),
		row(1, "2026-09-05", "-25.00", "2026-09-05 Coffee card [card number ending 1111 removed] -25.00", false),
		row(1, "2026-09-12", "-500.00", "2026-09-12 Rent -500.00", true),
		row(2, "2026-09-12", "-50.00", "2026-09-12 Rent -500.00", true),
	}, "truncated": false, "account": "Checking ****1234", "account_quote": "Account: Checking ****1234", "period_start": "2026-09-01", "period_end": "2026-09-30", "period_quote": "Statement period 2026-09-01 to 2026-09-30", "opening_balance": "1000.00", "opening_quote": "Opening balance 1,000.00", "closing_balance": "675.00", "closing_quote": "Closing balance 675.00", "currency": "USD"})
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Host != "api.openai.com" || !bytes.Contains(raw, []byte(`"store":false`)) || !bytes.Contains(raw, []byte("untrusted data")) {
			t.Fatalf("unexpected model call %s", r.URL)
		}
		if bytes.Contains(raw, []byte("4111 1111 1111 1111")) || !bytes.Contains(raw, []byte("card number ending 1111 removed")) {
			t.Fatal("the statement reaches the model with its card number removed")
		}
		body, _ := json.Marshal(map[string]any{"id": "resp-statement", "status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": string(reading)}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(permissions []string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type review struct {
		Extraction struct {
			ID       string
			Proposal statementProposal
		}
		Checks statementChecks
	}
	base := "/v1/closes/" + closeID + "/evidence/" + pdf + "/statement"
	var got review
	must(json.Unmarshal(call(nil, "POST", base, nil, 200), &got))
	issues := []string{}
	for _, r := range got.Extraction.Proposal.Rows {
		issues = append(issues, r.Issue)
	}
	if issues[0] != "" || !strings.Contains(issues[1], "Direction unclear") || !strings.Contains(issues[2], "Not found as one line on page 1") || !strings.Contains(issues[3], "amount does not appear") || got.Checks.Pages != 2 || len(got.Checks.PageProblems) != 0 || got.Checks.Difference != "-50.00" {
		t.Fatalf("each row's citation is checked against its page, and the balance difference shown: %v %+v", issues, got.Checks)
	}
	keep := func(index, page int, date, amount string, checked bool) map[string]any {
		return map[string]any{"index": index, "page": page, "date": date, "description": "Row", "amount": amount, "checked": checked}
	}
	good := []any{keep(0, 1, "2026-09-02", "200.00", false), keep(1, 1, "2026-09-05", "-25.00", true), keep(3, 2, "2026-09-12", "-500.00", true)}
	confirm := func(rows []any, removed []int, pages []int, currency string) map[string]any {
		return map[string]any{"extraction_id": got.Extraction.ID, "rows": rows, "removed": removed, "pages_checked": pages, "account": "Checking ****1234", "period_start": "2026-09-01", "period_end": "2026-09-30", "opening_balance": "1000.00", "closing_balance": "675.00", "currency": currency, "fields_checked": currency != "USD", "confirmed": true}
	}
	with := func(payload map[string]any, key string, value any) map[string]any {
		payload[key] = value
		return payload
	}
	reviewer := []string{"workpapers:review"}
	call(nil, "POST", base+"/confirm", confirm(good, []int{2}, []int{1, 2}, "USD"), 403)
	call(reviewer, "POST", base+"/confirm", confirm([]any{keep(0, 1, "2026-09-02", "200.00", false), keep(1, 1, "2026-09-05", "-25.00", false), keep(3, 2, "2026-09-12", "-500.00", true)}, []int{2}, []int{1, 2}, "USD"), 422)
	call(reviewer, "POST", base+"/confirm", confirm(good, []int{2}, []int{1}, "USD"), 422)
	call(reviewer, "POST", base+"/confirm", confirm(good, nil, []int{1, 2}, "USD"), 422)
	call(reviewer, "POST", base+"/confirm", confirm([]any{keep(0, 1, "2026-09-02", "200.00", false), keep(1, 1, "2026-09-05", "-25.00", true), keep(3, 2, "2026-09-12", "-50.00", true)}, []int{2}, []int{1, 2}, "USD"), 422)
	call(reviewer, "POST", base+"/confirm", confirm(append(good, keep(-1, 2, "2026-09-20", "0.00", false)), []int{2}, []int{1, 2}, "USD"), 422)
	call(reviewer, "POST", base+"/confirm", confirm(good, []int{2}, []int{1, 2}, "EUR"), 422)
	call(reviewer, "POST", base+"/confirm", with(confirm(good, []int{2}, []int{1, 2}, "USD"), "account", "Savings"), 422)
	call(reviewer, "POST", base+"/confirm", with(with(confirm(good, []int{2}, []int{1, 2}, "USD"), "period_end", "2026-10-02"), "fields_checked", true), 422)
	call(reviewer, "POST", base+"/confirm", confirm([]any{map[string]any{"index": 0, "page": 1, "date": "2026-09-02", "description": "Changed", "amount": "200.00", "checked": false}, keep(1, 1, "2026-09-05", "-25.00", true), keep(3, 2, "2026-09-12", "-500.00", true)}, []int{2}, []int{1, 2}, "USD"), 422)
	var done struct {
		EvidenceID string `json:"evidence_id"`
		JobID      string `json:"job_id"`
	}
	must(json.Unmarshal(call(reviewer, "POST", base+"/confirm", confirm(good, []int{2}, []int{1, 2}, "USD"), 200), &done))
	var name, content string
	must(db.QueryRow(`SELECT name,content FROM evidence WHERE id::text=$1`, done.EvidenceID).Scan(&name, &content))
	want := "id,date,amount\ne" + pdf + "-p1-1,2026-09-02,200.00\ne" + pdf + "-p1-2,2026-09-05,-25.00\ne" + pdf + "-p2-1,2026-09-12,-500.00\n"
	if name != "statement rows.csv" || content != want || done.JobID == "" {
		t.Fatalf("the confirmed rows become statement CSV evidence with its own intake job: %s %q %s", name, content, done.JobID)
	}
	var saved string
	must(db.QueryRow(`SELECT confirmed->>'account' FROM statement_extractions WHERE id::text=$1 AND produced_id::text=$2 AND confirmed_by='user_a'`, got.Extraction.ID, done.EvidenceID).Scan(&saved))
	if saved != "Checking ****1234" {
		t.Fatalf("the reviewed record is kept with the evidence it produced: %q", saved)
	}
	call(reviewer, "POST", base+"/confirm", confirm(good, []int{2}, []int{1, 2}, "USD"), 409)
	partialBase := "/v1/closes/" + closeID + "/evidence/" + partial + "/statement"
	var incomplete review
	must(json.Unmarshal(call(nil, "POST", partialBase, nil, 200), &incomplete))
	if len(incomplete.Checks.PageProblems) == 0 {
		t.Fatalf("a statement whose printed pages disagree is flagged: %+v", incomplete.Checks)
	}
	got = incomplete
	call(reviewer, "POST", partialBase+"/confirm", confirm(good, []int{2}, []int{1, 2}, "USD"), 422)
}

func TestStatementCitationsReadWholeAmountsAndRealPages(t *testing.T) {
	if documents.AmountIn("2026-09-02 Deposit 120.00", "20.00") || !documents.AmountIn("2026-09-02 Deposit $1,120.00", "1120.00") || !documents.AmountIn("Fee -5.00", "-5.00") {
		t.Fatal("an amount must appear whole in its line")
	}
	if !dateIn("Sep 2, 2026 Deposit", "2026-09-02") || dateIn("2026-09-03 Deposit", "2026-09-02") {
		t.Fatal("a row's date must appear in its line")
	}
	if issue := rowIssue(statementRow{Page: 1, Date: "2026-09-03", Amount: "200.00", Quote: "2026-09-02 Deposit 200.00", SignCertain: true}, map[int]string{1: "2026-09-02 Deposit 200.00\n"}); !strings.Contains(issue, "date does not appear") {
		t.Fatalf("a row dated differently from its line is flagged: %q", issue)
	}
	if issue := rowIssue(statementRow{Page: 1, Date: "2026-09-02", Amount: "300.00", Quote: "2026-09-02 Deposit\n2026-09-03 Fee 300.00", SignCertain: true}, map[int]string{1: "2026-09-02 Deposit\n2026-09-03 Fee 300.00\n"}); !strings.Contains(issue, "one line") {
		t.Fatalf("a citation spanning two lines is flagged: %q", issue)
	}
	if statementAmount.MatchString("0000000001.00") || !statementAmount.MatchString("0.50") {
		t.Fatal("amounts are read as reconciliation reads them")
	}
	if _, ok := statementPages("--- Page 1 · PDF text ---\nA\n--- Page 3 · PDF text ---\nB\n"); ok {
		t.Fatal("pages out of sequence are refused")
	}
	if pages, ok := statementPages("--- Page 1 · PDF text ---\nA\n --- Page 2 · PDF text ---\n"); !ok || len(pages) != 1 {
		t.Fatal("an indented marker-shaped line is page text, not a page")
	}
}
