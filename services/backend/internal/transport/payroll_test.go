package transport

import (
	"bytes"
	"close.local/backend/internal/preparation"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	neturl "net/url"
	"os"
	"strings"
	"testing"
)

func TestPayrollDraftsNeedTheFirmsPayrollProcedure(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Payroll client " + rand.Text()
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM procedure_versions WHERE kind='payroll' AND organization_id='local-development' AND client_entity=$1`, entity)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("p", 32)
	handler := apiWithStore(db, token)
	call := func(path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	input := map[string]any{"entity": entity, "close_period": "2026-09", "currency": "USD", "provider": "Gusto", "source_reference": "Payroll summary Sep 2026", "first_pay_date": "2026-09-15", "last_pay_date": "2026-09-30",
		"gross_wages": "10000.00", "employee_taxes": "1500.00", "employee_deductions": "500.00", "net_pay": "8000.00", "employer_taxes": "765.00", "employer_contributions": "0.00",
		"provider_debits": "10265.00", "ledger_debits": "10265.00", "ledger_reference": "Payroll clearing GL", "wage_account": "Wages", "tax_expense_account": "Payroll taxes",
		"clearing_account": "Payroll clearing", "liability_account": "Payroll liabilities", "method": "payroll_summary_v1", "approved_confirmed": true, "not_booked_confirmed": true}
	path := "/v1/closes/" + closeID + "/workpapers"
	if body := call(path, map[string]any{"kind": "payroll", "input": input}, 409); !strings.Contains(string(body), "Payroll entries need the firm's approved payroll procedure") {
		t.Fatalf("a payroll draft needs the firm's payroll procedure: %s", body)
	}
	preview := httptest.NewRequest("GET", "/v1/closes/procedures?impact_kind=payroll&client_entity="+neturl.QueryEscape(entity), nil)
	preview.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, preview)
	var impact struct {
		ImpactDigest string `json:"impact_digest"`
	}
	if json.Unmarshal(w.Body.Bytes(), &impact); w.Code != 200 || impact.ImpactDigest == "" {
		t.Fatalf("the payroll procedure's impact is previewed: %d %s", w.Code, w.Body.String())
	}
	call("/v1/closes/procedures", map[string]any{"client_entity": entity, "kind": "payroll", "method": "payroll_summary_v1", "instructions": "Record payroll from the Gusto monthly summary.", "enabled": true, "reason": "Synthetic", "confirmed": true, "impact_digest": impact.ImpactDigest}, 200)
	var saved struct {
		Paper struct {
			Unpaid    string `json:"unpaid"`
			Proposals []struct {
				DebitAccount  string `json:"debit_account"`
				CreditAccount string `json:"credit_account"`
				Amount        string `json:"amount"`
			} `json:"proposals"`
		} `json:"paper"`
	}
	json.Unmarshal(call(path, map[string]any{"kind": "payroll", "input": input}, 200), &saved)
	if saved.Paper.Unpaid != "500.00" || len(saved.Paper.Proposals) != 3 || saved.Paper.Proposals[2].CreditAccount != "Payroll liabilities" || saved.Paper.Proposals[2].Amount != "500.00" {
		t.Fatalf("the saved payroll draft leaves the unremitted deductions owed: %+v", saved.Paper)
	}
	// A later document naming the provider and speaking of an amendment reopens the draft's review; next month's
	// ordinary summary from the same provider does not.
	var draft string
	if err = db.QueryRow(`SELECT id::text FROM workpapers WHERE close_id=$1 AND kind='payroll' ORDER BY id DESC LIMIT 1`, closeID).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	state := func() (s string) {
		t.Helper()
		if err := db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id::text=$1`, draft).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	defer db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
	evidence := func(content string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'payroll.txt',$2,$3,'fixture','ready')`, closeID, rand.Text(), content); err != nil {
			t.Fatal(err)
		}
	}
	evidence("Gusto payroll summary for October 2026")
	if s := state(); s == "source_changed" {
		t.Fatalf("an ordinary summary from the same provider does not reopen the draft: %s", s)
	}
	evidence("Gusto amended payroll register for September 2026: gross wages corrected")
	if s := state(); s != "source_changed" {
		t.Fatalf("an amended summary from the draft's provider reopens its review: %s", s)
	}
	// Through the preparation worker the draft carries its checks; it is approved only with both confirmations, given by
	// the reviewer and recorded with the exact summary and ledger evidence. A second draft for the same pay dates is a
	// duplicate, which blocks it.
	evidenceRow := func(name, content string) preparation.Source {
		t.Helper()
		var id string
		if err := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture','ready') RETURNING id::text`, closeID, name, preparation.Digest([]byte(content)), content).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return preparation.Source{ID: id, Digest: preparation.Digest([]byte(content)), Name: name, Location: "page 1"}
	}
	if _, err = db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID); err != nil {
		t.Fatal(err)
	}
	summary, page := evidenceRow("Gusto September summary.pdf", "Gusto payroll summary September 2026"), evidenceRow("Payroll clearing ledger.pdf", "Payroll clearing September 2026")
	summary.Role, page.Role = "support", "ledger"
	prepare := func() (string, string) {
		t.Helper()
		raw, _ := json.Marshal(input)
		plans, _ := json.Marshal([]preparation.Plan{{Kind: "payroll", Input: raw, Sources: []preparation.Source{summary, page}}})
		var job, paper string
		if err := db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest(append(plans, rand.Text()...)), string(plans)).Scan(&job); err != nil {
			t.Fatal(err)
		}
		if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
			t.Fatal("the payroll job was not processed", e)
		}
		if err := db.QueryRow(`SELECT COALESCE(workpaper_ids->>0,'') FROM preparation_jobs WHERE id::text=$1`, job).Scan(&paper); err != nil || paper == "" {
			t.Fatal("no payroll draft", err)
		}
		var checks string
		db.QueryRow(`SELECT paper->'checks'::text FROM workpapers WHERE id::text=$1`, paper).Scan(&checks)
		return paper, checks
	}
	first, checks := prepare()
	if !strings.Contains(checks, `"No other draft"`) || !strings.Contains(checks, `"unavailable"`) {
		t.Fatalf("the draft carries its checks, saying what the records cannot answer: %s", checks)
	}
	review := func(paper string, attestations []string, want int) []byte {
		t.Helper()
		var digest, reviewID string
		if err := db.QueryRow(`SELECT digest,review_id FROM workpaper_review_state WHERE id::text=$1`, paper).Scan(&digest, &reviewID); err != nil {
			t.Fatal(err)
		}
		return call("/v1/closes/"+closeID+"/workpapers/"+paper+"/reviews", map[string]any{"decision": "approved", "note": "Payroll checked", "digest": digest, "expected_review_id": reviewID, "attestations": attestations}, want)
	}
	if body := review(first, []string{"final_approved_summary"}, 409); !strings.Contains(string(body), "not already booked") {
		t.Fatalf("a payroll recording needs both confirmations: %s", body)
	}
	review(first, []string{"final_approved_summary", "not_already_booked"}, 200)
	var attested string
	if err = db.QueryRow(`SELECT attestations::text FROM workpaper_reviews WHERE workpaper_id::text=$1 ORDER BY id DESC LIMIT 1`, first).Scan(&attested); err != nil || !strings.Contains(attested, `"id": "`+summary.ID+`"`) || !strings.Contains(attested, `"id": "`+page.ID+`"`) {
		t.Fatalf("the confirmations are recorded with the exact summary and ledger page: %s %v", attested, err)
	}
	second, _ := prepare()
	var blockers string
	if err = db.QueryRow(`SELECT paper->'blockers'::text FROM workpapers WHERE id::text=$1`, second).Scan(&blockers); err != nil || !strings.Contains(blockers, "Draft #"+first+" already records Gusto payroll") {
		t.Fatalf("a second draft for the same pay dates is a duplicate: %s %v", blockers, err)
	}
	if paperKey("payroll", json.RawMessage(`{"input":{"provider":"Gusto","source_reference":"Sep summary"}}`)) != paperKey("payroll", json.RawMessage(`{"input":{"provider":" gusto","source_reference":"Sep summary, corrected"}}`)) {
		t.Fatal("a corrected summary replaces the provider's payroll for the month, whatever its title")
	}
}
