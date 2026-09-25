package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestLoanDraftsNeedTheClientsApprovedLoanProcedure(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Loan client " + rand.Text()
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM procedure_versions WHERE kind='loan' AND organization_id='local-development' AND client_entity=$1`, entity)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("l", 32)
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
	input := map[string]any{"entity": entity, "close_period": "2026-09", "lender": "First Bank", "source_reference": "Statement Sep 2026", "agreement_reference": "Note §2", "currency": "USD",
		"annual_rate": "6.000", "day_count": "actual_360", "opening_principal": "120000.00", "interest_paid_through": "2026-08-15",
		"payments":          []map[string]string{{"date": "2026-09-15", "principal": "1500.00", "interest": "620.00"}, {"date": "2026-09-29", "principal": "500.00", "interest": "276.50"}},
		"closing_principal": "118000.00", "book_balance": "118000.00", "loan_account": "Loan payable", "interest_account": "Interest expense",
		"accrue_interest": true, "accrued_interest_account": "Accrued interest", "booked_accrued_interest": "0.00",
		"method": "lender_statement_v1", "method_confirmed": true, "rate_confirmed": true, "booking_confirmed": true}
	save := map[string]any{"kind": "loan", "input": input}
	path := "/v1/closes/" + closeID + "/workpapers"
	// Client overrides win over any firm default, so the test does not depend on firm settings.
	override := func(enabled bool, method ...string) {
		t.Helper()
		if _, e := db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES('local-development',$1,'loan','Reclassify the current portion at fiscal year-end.',$2,'Synthetic loan override','fixture',$3)`, entity, enabled, append(method, "lender_statement_v1")[0]); e != nil {
			t.Fatal(e)
		}
	}
	override(false)
	if body := call(path, save, 409); !strings.Contains(string(body), "approved loan procedure") || !strings.Contains(string(body), `"code":"POLICY_REQUIRED"`) {
		t.Fatalf("a loan draft needs an enabled loan procedure: %s", body)
	}
	override(true)
	var saved struct {
		ID string `json:"id"`
	}
	json.Unmarshal(call(path, save, 200), &saved)
	override(false)
	input["book_balance"] = "118000.01"
	if body := call(path, save, 409); !strings.Contains(string(body), "approved loan procedure") {
		t.Fatalf("disabling the client's loan procedure must stop new drafts: %s", body)
	}
	// Rate changes need the separately approved variable-rate procedure.
	override(true)
	input["book_balance"], input["method"] = "118000.00", "lender_statement_variable_v1"
	input["rate_changes"] = []map[string]string{{"effective": "2026-09-01", "annual_rate": "6.500", "reference": "Rate notice Aug 2026"}}
	if body := call(path, save, 409); !strings.Contains(string(body), "differs from the approved procedure") {
		t.Fatalf("a variable-rate draft needs the variable-rate procedure: %s", body)
	}
	override(true, "lender_statement_variable_v1")
	call(path, save, 200)
	input["method"] = "lender_statement_v1"
	delete(input, "rate_changes")
	var reason string
	if err = db.QueryRow(`SELECT `+bulkReason+` FROM workpaper_review_state w WHERE id::text=$1`, saved.ID).Scan(&reason); err != nil || reason != "Proposes a ledger adjustment" {
		t.Fatalf("a loan with proposed entries must be reviewed individually: %q %v", reason, err)
	}
	if body := call("/v1/closes/procedures", map[string]any{"client_entity": entity, "kind": "loan", "method": "", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true}, 422); !strings.Contains(string(body), "Confirm a supported procedure") {
		t.Fatalf("a loan procedure must name the reviewed method: %s", body)
	}
	call("/v1/closes/procedures", map[string]any{"client_entity": entity, "kind": "loan", "method": "lender_statement_v1", "instructions": "Accrue interest monthly.", "enabled": true, "reason": "Synthetic", "confirmed": true, "previous_id": "stale"}, 409)
}
