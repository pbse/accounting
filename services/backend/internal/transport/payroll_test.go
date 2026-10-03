package transport

import (
	"bytes"
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
	if paperKey("payroll", json.RawMessage(`{"input":{"provider":"Gusto","source_reference":"Sep summary"}}`)) != paperKey("payroll", json.RawMessage(`{"input":{"provider":" gusto","source_reference":"Sep summary, corrected"}}`)) {
		t.Fatal("a corrected summary replaces the provider's payroll for the month, whatever its title")
	}
}
