package transport

import (
	"bytes"
	"close.local/backend/internal/preparation"
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
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
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
	// A rate from an index must be the published value on record: at preview, at save and when a queued job runs.
	if _, err = db.Exec(`INSERT INTO index_rates(name,effective_date,percent) VALUES('SOFR','2026-08-31',4.0000) ON CONFLICT(name,effective_date) DO UPDATE SET percent=4.0000`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM index_rates WHERE name='SOFR' AND effective_date='2026-08-31'`)
	indexed := map[string]string{"effective": "2026-09-01", "annual_rate": "6.600", "reference": "Note §3: SOFR + 2.500%", "index": "SOFR", "index_date": "2026-08-31", "index_rate": "4.10", "margin": "2.500"}
	input["rate_changes"] = []map[string]string{indexed}
	if body := call(path, save, 409); !strings.Contains(string(body), "not the published value") {
		t.Fatalf("a saved index rate must be the published one: %s", body)
	}
	if body := call("/v1/loans:prepare", input, 409); !strings.Contains(string(body), "not the published value") {
		t.Fatalf("the preview checks it too: %s", body)
	}
	plans, _ := json.Marshal([]map[string]any{{"kind": "loan", "input": input, "sources": []any{}}})
	var job, state, failure string
	if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,'fixture') RETURNING id::text`, closeID, "index "+rand.Text(), string(plans)).Scan(&job); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state); state != "queued" {
			break
		}
		if worked, e := preparation.ProcessOne(t.Context(), db); e != nil || !worked {
			break
		}
	}
	if db.QueryRow(`SELECT state,error FROM preparation_jobs WHERE id=$1`, job).Scan(&state, &failure); state != "failed" || !strings.Contains(failure, "not the published value") {
		t.Fatalf("a queued job whose index rate is not the published one fails: %s %s", state, failure)
	}
	indexed["index_rate"], indexed["annual_rate"] = "4.00", "6.500"
	call(path, save, 200)
	// Debt issuance costs need the procedure that includes them.
	input["method"] = "lender_statement_costs_v1"
	input["issuance_costs"] = map[string]any{"amount": "1200.00", "start": "2026-01-01", "end": "2026-12-31", "account": "Debt issuance costs", "reference": "Closing statement", "method": "equal_full_months_v1", "materiality_reference": "Memo 2026-01", "booked_unamortized": "300.00", "booking_reference": "Trial balance Sep", "method_confirmed": true, "booking_confirmed": true}
	if body := call(path, save, 409); !strings.Contains(string(body), "differs from the approved procedure") {
		t.Fatalf("an issuance cost draft needs its own procedure: %s", body)
	}
	override(true, "lender_statement_costs_v1")
	call(path, save, 200)
	input["method"] = "lender_statement_v1"
	delete(input, "rate_changes")
	delete(input, "issuance_costs")
	var reason string
	if err = db.QueryRow(`SELECT `+bulkReason+` FROM workpaper_review_state w WHERE id::text=$1`, saved.ID).Scan(&reason); err != nil || reason != "Proposes a ledger adjustment" {
		t.Fatalf("a loan with proposed entries must be reviewed individually: %q %v", reason, err)
	}
	if body := call("/v1/closes/procedures", map[string]any{"client_entity": entity, "kind": "loan", "method": "", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true}, 422); !strings.Contains(string(body), "Confirm a supported procedure") {
		t.Fatalf("a loan procedure must name the reviewed method: %s", body)
	}
	call("/v1/closes/procedures", map[string]any{"client_entity": entity, "kind": "loan", "method": "lender_statement_v1", "instructions": "Accrue interest monthly.", "enabled": true, "reason": "Synthetic", "confirmed": true, "previous_id": "stale"}, 409)
	call("/v1/closes/procedures", map[string]any{"client_entity": entity, "kind": "loan", "method": "lender_statement_costs_v1", "instructions": "Amortize issuance costs straight-line.", "enabled": true, "reason": "Synthetic", "confirmed": true, "previous_id": "stale"}, 409)
	// A later lender amendment reopens the earlier schedule when it becomes readable.
	defer db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
	loanState := func() string {
		t.Helper()
		var state string
		if e := db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id::text=$1`, saved.ID).Scan(&state); e != nil {
			t.Fatal(e)
		}
		return state
	}
	var ordinary, amendment string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'other-lender.txt',$2,'First Banking revised loan terms','fixture','ready') RETURNING id::text`, closeID, rand.Text()).Scan(&ordinary); err != nil {
		t.Fatal(err)
	}
	if state := loanState(); state == "source_changed" {
		t.Fatal("an amendment from another lender reopened the loan")
	}
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'rate-notice.txt',$2,'First Bank rate change effective September 2026','fixture','queued') RETURNING id::text`, closeID, rand.Text()).Scan(&amendment); err != nil {
		t.Fatal(err)
	}
	if state := loanState(); state == "source_changed" {
		t.Fatal("an unread notice reopened the loan")
	}
	if _, err = db.Exec(`UPDATE evidence SET extraction_state='ready' WHERE id::text=$1`, amendment); err != nil {
		t.Fatal(err)
	}
	if state := loanState(); state != "source_changed" {
		t.Fatalf("the lender rate notice did not reopen the saved loan: %s", state)
	}

	var replacement string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'withdrawn-notice.txt',$2,'Other Bank ordinary notice','fixture','ready') RETURNING id::text`, closeID, rand.Text()).Scan(&replacement); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id::text=$1`, amendment, replacement); err != nil {
		t.Fatal(err)
	}
	if state := loanState(); state == "source_changed" {
		t.Fatal("a replaced lender notice still holds the loan")
	}

	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'payoff.txt',$2,'First Bank Payoff Statement','fixture','ready')`, closeID, rand.Text()); err != nil {
		t.Fatal(err)
	}
	if state := loanState(); state != "source_changed" {
		t.Fatalf("a lender payoff notice did not reopen the loan: %s", state)
	}

}
