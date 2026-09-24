package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestTheClientsBasisDecidesWhichWorkCanBeSavedAndApproved(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Cash client " + rand.Text()
	firm := "org_" + rand.Text()
	var closeID, otherClose string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Basis firm')`, firm); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES('Override client','2026-09',$1) RETURNING id::text`, firm).Scan(&otherClose); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, id := range []string{closeID, otherClose} {
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM procedure_versions WHERE (organization_id='local-development' AND client_entity=$1) OR organization_id=$2`, entity, firm)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, firm)
	}()
	version := func(org, client, kind, method string) {
		t.Helper()
		if _, e := db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,$2,$3,'Synthetic',true,'Synthetic basis','fixture',$4)`, org, client, kind, method); e != nil {
			t.Fatal(e)
		}
	}
	ctx := context.Background()
	version(firm, "", "basis", "cash")
	if issue, e := preparation.BasisIssue(ctx, db, otherClose, "accrual", nil); e != nil || !strings.Contains(issue, "cash basis") {
		t.Fatalf("a firm default cash basis must refuse accruals: %q %v", issue, e)
	}
	version(firm, "Override client", "basis", "accrual")
	if issue, e := preparation.BasisIssue(ctx, db, otherClose, "accrual", nil); e != nil || issue != "" {
		t.Fatalf("a client override must win over the firm default: %q %v", issue, e)
	}

	version("local-development", entity, "basis", "cash")
	version("local-development", entity, "loan", "lender_statement_v1")
	token := strings.Repeat("b", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + closeID + "/workpapers"
	accrual := map[string]any{"entity": entity, "source_reference": "Invoice", "close_period": "2026-09", "service_start": "2026-09-01", "service_end": "2026-09-30", "amount": "1200.00", "booked_balance": "0.00", "booking_reference": "Ledger", "estimation_basis": "Hours", "expense_account": "Consulting", "liability_account": "Accrued consulting", "currency": "USD", "basis": "accrual", "method": "fixed_incurred_amount_v1", "method_confirmed": true, "booking_confirmed": true, "examples_confirmed": true}
	if body := call("POST", path, map[string]any{"kind": "accrual", "input": accrual}, 409); !strings.Contains(string(body), "cash basis") {
		t.Fatalf("a cash client must not save accruals: %s", body)
	}
	prepaid := map[string]any{"entity": entity, "source_reference": "Policy", "close_period": "2026-09", "amount": "1200.00", "service_start": "2026-09-01", "service_end": "2027-08-31", "currency": "USD", "basis": "accrual", "expense_account": "Insurance", "prepaid_account": "Prepaid insurance", "method": "equal_full_months_v1", "examples_confirmed": true, "method_confirmed": true, "booking_confirmed": true}
	if body := call("POST", path, map[string]any{"kind": "prepaid", "input": prepaid}, 409); !strings.Contains(string(body), "cash basis") {
		t.Fatalf("a cash client must not save prepaid schedules: %s", body)
	}
	loan := map[string]any{"entity": entity, "close_period": "2026-09", "lender": "First Bank", "source_reference": "Statement", "agreement_reference": "Note", "currency": "USD", "annual_rate": "6.000", "day_count": "actual_360", "opening_principal": "1000.00", "interest_paid_through": "2026-08-31", "payments": []any{}, "closing_principal": "1000.00", "book_balance": "1000.00", "loan_account": "Loan payable", "interest_account": "Interest expense", "accrue_interest": true, "accrued_interest_account": "Accrued interest", "booked_accrued_interest": "0.00", "method": "lender_statement_v1", "method_confirmed": true, "rate_confirmed": true, "booking_confirmed": true}
	if body := call("POST", path, map[string]any{"kind": "loan", "input": loan}, 409); !strings.Contains(string(body), "Accrued loan interest") {
		t.Fatalf("a cash client must not accrue loan interest: %s", body)
	}
	delete(loan, "accrued_interest_account")
	delete(loan, "booked_accrued_interest")
	loan["accrue_interest"] = false
	call("POST", path, map[string]any{"kind": "loan", "input": loan}, 200)

	content := "Invoice " + rand.Text()
	var evidenceID, paperID string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(content)), content).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	earlier := `{"sources":[{"id":"` + evidenceID + `","digest":"` + preparation.Digest([]byte(content)) + `","role":"invoice","name":"invoice.txt","location":"line 1"}],"blockers":[]}`
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'accrual','earlier',$2,'fixture') RETURNING id::text`, closeID, earlier).Scan(&paperID); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	defer tx.Rollback()
	if _, status, message := decideReview(ctx, tx, closeID, paperID, "user_reviewer", "approved", "Synthetic approval", "earlier", "", false); status != 409 || !strings.Contains(message, "cash basis") {
		t.Fatalf("an accrual saved before the basis change must not be approved: %d %s", status, message)
	}
	tx.Rollback()

	var opened struct {
		Close struct {
			Basis      string `json:"basis"`
			BasisScope string `json:"basis_scope"`
		} `json:"close"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+closeID, nil, 200), &opened)
	if opened.Close.Basis != "cash" || opened.Close.BasisScope != "client" {
		t.Fatalf("the close must show its effective basis: %+v", opened.Close)
	}
	for _, bad := range []map[string]any{
		{"client_entity": entity, "kind": "basis", "method": "", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true},
		{"client_entity": entity, "kind": "basis", "method": "cash", "instructions": "x", "enabled": false, "reason": "Synthetic", "confirmed": true},
		{"client_entity": entity, "kind": "prepaid", "method": "cash", "instructions": "x", "enabled": true, "reason": "Synthetic", "confirmed": true},
	} {
		call("POST", "/v1/closes/procedures", bad, 422)
	}
}
