package transport

import (
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
)

func TestTheReconciliationLedgerComesFromTheCurrentCapture(t *testing.T) {
	db := adminDB(t)
	token := strings.Repeat("g", 32)
	h := apiWithStore(db, token)
	entity := "Ledger " + rand.Text()
	closeID := adminJSON(t, h, token, "POST", "/v1/closes", `{"entity":"`+entity+`","period":"2026-09"}`, 200)["id"].(string)
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM quickbooks_snapshots WHERE close_id::text=$1`, `DELETE FROM evidence WHERE close_id::text=$1`, `DELETE FROM quickbooks_connections WHERE close_id::text=$1`, `DELETE FROM client_closes WHERE id::text=$1`} {
			db.Exec(q, closeID)
		}
	})
	var connection, source string
	if err := db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id::text=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&source); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": source, "accounts": []map[string]any{{"Id": "3", "Name": "Operating Checking", "AccountType": "Bank", "Classification": "Asset", "CurrencyRef": map[string]string{"value": "USD"}}},
		"reports": map[string]any{"ProfitAndLoss": map[string]any{},
			"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": ""}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}},
				"Rows": map[string]any{"Row": []map[string]any{{"type": "Data", "ColData": []map[string]string{{"id": "3", "value": "Operating Checking"}, {"value": "1300.00"}, {"value": ""}}}}}},
			"GeneralLedger": map[string]any{"Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "Date"}, {"ColTitle": "Amount"}, {"ColTitle": "Balance"}}},
				"Rows": map[string]any{"Row": []map[string]any{{"type": "Section", "Header": map[string]any{"ColData": []map[string]string{{"id": "3", "value": "Operating Checking"}}}, "Rows": map[string]any{"Row": []map[string]any{
					{"type": "Data", "ColData": []map[string]string{{"value": "Beginning Balance"}, {"value": ""}, {"value": "1000.00"}}},
					{"type": "Data", "ColData": []map[string]string{{"value": "2026-09-03"}, {"value": "300.00"}, {"value": "1300.00"}}}}}}}}}}})
	if _, err := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1::bigint,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), string(payload)); err != nil {
		t.Fatal(err)
	}
	path := "/v1/closes/" + closeID + "/reconciliation-ledger?account="
	got := adminJSON(t, h, token, "GET", path+"operating%20checking", "", 200)
	if got["ledger_csv"] != "id,date,amount\nGL1,2026-09-03,300.00\n" || got["ledger_opening"] != "1000.00" || got["ledger_closing"] != "1300.00" || !strings.Contains(got["ledger_reference"].(string), "QuickBooks General Ledger for operating checking") {
		t.Fatalf("the bank account's month comes from the capture: %v", got)
	}
	adminJSON(t, h, token, "GET", path+"Savings", "", 409)
	if got["source_id"] != source {
		t.Fatalf("the capture's own evidence entry is the ledger source: %v", got)
	}

	// Saved with the capture as its ledger source, the draft's ledger is read from the capture, whatever the form held.
	var statement string
	if err := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'statement.csv',$2,'id,date,amount
S1,2026-09-03,300.00
','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&statement); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(reconciliation.Input{Entity: entity, Account: "Operating Checking", AccountType: "bank", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "manual", LedgerReference: "manual", ExpectedPages: 1, Pages: []int{1},
		StatementOpening: "1000", StatementClosing: "1300", LedgerOpening: "0", LedgerClosing: "0", PolicyConfirmed: true})
	plan, _ := json.Marshal(preparation.Plan{Kind: "reconciliation", Input: input, Sources: []preparation.Source{{ID: statement, Role: "statement", Location: "rows 1"}, {ID: source, Role: "ledger", Location: "General Ledger · Operating Checking"}}})
	saved := adminJSON(t, h, token, "POST", "/v1/closes/"+closeID+"/preparations", string(plan), 200)
	var stored []byte
	if err := db.QueryRow(`SELECT plan FROM preparation_plans WHERE id::text=$1`, saved["id"]).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var pinned preparation.Plan
	var checked reconciliation.Input
	if json.Unmarshal(stored, &pinned) != nil || json.Unmarshal(pinned.Input, &checked) != nil || checked.LedgerCSV != "id,date,amount\nGL1,2026-09-03,300.00\n" || checked.LedgerOpening != "1000.00" || checked.LedgerClosing != "1300.00" {
		t.Fatalf("the saved draft's ledger is the capture's: %+v", checked)
	}
}
