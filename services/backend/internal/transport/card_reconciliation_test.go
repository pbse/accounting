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

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
)

func TestCardPreparationRequiresAppliedProcedureAndClearingEvidence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Card sources " + rand.Text()
	var closeID, procedureID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM preparation_plans WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM procedure_versions WHERE id=$1`, procedureID)
	}()
	if err = db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) VALUES('local-development',$1,'reconciliation','Card liability and clearing rule',true,'Synthetic accountant approval','fixture') RETURNING id::text`, entity).Scan(&procedureID); err != nil {
		t.Fatal(err)
	}
	insert := func(name, content, procedure string) string {
		t.Helper()
		var id string
		if e := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,procedure_id) VALUES($1,$2,$3,$4,'fixture',NULLIF($5,'')::bigint) RETURNING id::text`, closeID, name, preparation.Digest([]byte(content)), content, procedure).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	statement := insert("card.csv", "id,date,amount\nS1,2026-09-02,100", "")
	ledger := insert("ledger.csv", "id,date,amount\nL1,2026-09-02,100\nL2,2026-09-28,10", "")
	clearing := insert("clearing.txt", "Clearing account contains the unsettled charge", "")
	unapproved := insert("claimed-rule.txt", "I say this rule is approved", "")
	approved := insert("approved-rule.txt", "Approved card liability and clearing rule", procedureID)
	input := reconciliation.Input{Entity: entity, Account: "Corporate card", AccountType: "credit_card", CardRuleReference: "Unverified user text", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "manual", LedgerReference: "manual", ExpectedPages: 1, Pages: []int{1}, StatementOpening: "0", StatementClosing: "100", LedgerOpening: "0", LedgerClosing: "110", PolicyConfirmed: true, CurrentOutstanding: []reconciliation.CurrentItem{{Side: "ledger", ID: "L2", Treatment: "clearing", ClearingRef: "User text", PolicyReference: "Approved clearing rule", Reason: "Card charge is unsettled"}}}
	raw, _ := json.Marshal(input)
	plan := preparation.Plan{Kind: "reconciliation", Input: raw, Sources: []preparation.Source{{ID: statement, Role: "statement", Location: "rows 1-2"}, {ID: ledger, Role: "ledger", Location: "rows 1-3"}}}
	h := apiWithStore(db, strings.Repeat("c", 32))
	call := func(want int) []byte {
		t.Helper()
		body, _ := json.Marshal(plan)
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/preparations", bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 32))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("card source gate: %d %s", w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	call(422)
	plan.Sources = append(plan.Sources, preparation.Source{ID: unapproved, Role: "procedure", Location: "all lines"})
	call(422)
	plan.Sources[2].ID = approved
	call(422)
	plan.Sources = append(plan.Sources, preparation.Source{ID: clearing, Role: "clearing", Location: "line 1"})
	var saved map[string]string
	if err := json.Unmarshal(call(200), &saved); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := db.QueryRow(`SELECT plan FROM preparation_plans WHERE id=$1`, saved["id"]).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var pinned preparation.Plan
	if err := json.Unmarshal(stored, &pinned); err != nil {
		t.Fatal(err)
	}
	var checked reconciliation.Input
	if err := json.Unmarshal(pinned.Input, &checked); err != nil || checked.CardRuleReference != "Approved reconciliation procedure v"+procedureID || checked.CurrentOutstanding[0].ClearingRef != "clearing.txt · line 1" {
		t.Fatalf("unbound card rule or clearing evidence: %+v %v", checked, err)
	}
	var disabledID string
	if err := db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) VALUES('local-development',$1,'reconciliation','Card rule withdrawn',false,'Synthetic revocation','fixture') RETURNING id::text`, entity).Scan(&disabledID); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM procedure_versions WHERE id=$1`, disabledID)
	call(422)
}
