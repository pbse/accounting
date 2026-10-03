package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestPredictedReviewFlagsDraftsALaterTransactionMayAffect(t *testing.T) {
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
	entity := "Predicted " + rand.Text()
	var closeID, connection, evidenceID, syncID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM close_signoffs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'transactions.json',$2,'{}','quickbooks-sync') RETURNING id::text`, closeID, rand.Text()).Scan(&evidenceID))
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now()) RETURNING id::text`, closeID, connection, evidenceID).Scan(&syncID))
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,record) VALUES
 ($1,'Bill','41','2','2026-09-10',now(),'{"Line":[{"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"7","name":"Prepaid insurance"}}}]}'),
 ($1,'Purchase','52','1','2026-09-11',now()-interval '2 days','{"AccountRef":{"value":"9","name":"Accrued liabilities"}}'),
 ($1,'BillPayment','70','1','2026-09-13',now(),'{"CheckPayment":{"BankAccountRef":{"value":"3","name":"Operating bank"}}}'),
 ($1,'Purchase','60','1','2026-09-12',now(),'{"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"11","name":"Rent expense"}}}]}')`, syncID)
	must(err)
	approved := func(kind, input, at string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) RETURNING id::text`, closeID, kind, rand.Text(), `{"input":`+input+`}`).Scan(&id))
		_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor,created_at) SELECT id,digest,'approved','Reviewed fixture','fixture',now()+$2::interval FROM workpapers WHERE id=$1`, id, at)
		must(e)
		return id
	}
	flagged := approved("prepaid", `{"prepaid_account":"prepaid INSURANCE "}`, "-1 day")
	approved("accrual", `{"liability_account":"Accrued liabilities"}`, "-1 day")
	approved("prepaid", `{"prepaid_account":"Prepaid rent","expense_account":"Rent expense"}`, "-1 day")
	approved("prepaid", `{"prepaid_account":"Prepaid insurance"}`, "1 hour")
	bank := approved("reconciliation", `{"account":"Operating bank"}`, "-1 day")
	changed := approved("prepaid", `{"prepaid_account":"Prepaid insurance"},"sources":[{"id":"0","digest":"missing"}]`, "-1 day")

	token := strings.Repeat("p", 32)
	handler := apiWithStore(db, token)
	read := func() (out struct {
		Predicted []struct {
			WorkpaperID string `json:"workpaper_id"`
			Transaction string `json:"transaction"`
			Account     string `json:"account"`
			Count       int    `json:"count"`
		} `json:"predicted"`
	}) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/dependencies?client="+url.QueryEscape(entity), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("dependencies: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	if got := read().Predicted; len(got) != 2 || got[0].WorkpaperID != bank || got[0].Transaction != "BillPayment 70" || got[1].WorkpaperID != flagged || got[1].Transaction != "Bill 41" || got[1].Account != "Prepaid insurance" || got[1].Count != 1 {
		t.Fatalf("only the draft whose account a transaction updated after approval is flagged, not one already reopened (#%s): %+v", changed, got)
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='disconnected' WHERE id=$1`, connection)
	must(err)
	if got := read().Predicted; len(got) != 0 {
		t.Fatalf("a disconnected company's old capture predicts nothing: %+v", got)
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='active' WHERE id=$1`, connection)
	must(err)
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed fixture','fixture')`, closeID)
	must(err)
	if got := read().Predicted; len(got) != 0 {
		t.Fatalf("a signed-off close is not listed: %+v", got)
	}
}

func TestPredictedReviewSaysWhenTheAccountNoLongerTies(t *testing.T) {
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
	entity := "No longer ties " + rand.Text()
	var closeID, connection, evidenceID, syncID, paperID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'transactions.json',$2,'{}','quickbooks-sync') RETURNING id::text`, closeID, rand.Text()).Scan(&evidenceID))
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now()) RETURNING id::text`, closeID, connection, evidenceID).Scan(&syncID))
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,record) VALUES($1,'BillPayment','70','1','2026-09-13',now(),'{"CheckPayment":{"BankAccountRef":{"value":"3","name":"Operating bank"}}}')`, syncID)
	must(err)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation',$2,'{"input":{"account":"Operating bank","ledger_closing":"1000.00"}}') RETURNING id::text`, closeID, rand.Text()).Scan(&paperID))
	approve := func(id, at string) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor,created_at) SELECT id,digest,'approved','Reviewed fixture','fixture',now()+$2::interval FROM workpapers WHERE id=$1`, id, at)
		must(e)
	}
	approve(paperID, "-1 day")
	var savings string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation',$2,'{"input":{"account":"Savings bank","ledger_closing":"500.00"}}') RETURNING id::text`, closeID, rand.Text()).Scan(&savings))
	approve(savings, "-1 day")
	report := []map[string]any{{"type": "Data", "ColData": []map[string]string{{"id": "3", "value": "Operating bank"}, {"value": "900.00"}, {"value": ""}}}, {"type": "Data", "ColData": []map[string]string{{"id": "4", "value": "Savings bank"}, {"value": "500.00"}, {"value": ""}}}}
	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": evidenceID, "accounts": []map[string]string{{"Id": "3", "Name": "Operating bank", "AccountType": "Bank", "Classification": "Asset"}, {"Id": "4", "Name": "Savings bank", "AccountType": "Bank", "Classification": "Asset"}},
		"reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": ""}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}}, "Rows": map[string]any{"Row": report}}}})
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), string(payload))
	must(err)

	token := strings.Repeat("p", 32)
	handler := apiWithStore(db, token)
	read := func() (predicted int, ties []map[string]any) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/dependencies?client="+url.QueryEscape(entity), nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var out struct {
			Predicted    []any            `json:"predicted"`
			NoLongerTies []map[string]any `json:"no_longer_ties"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("dependencies: %d %s", w.Code, w.Body.String())
		}
		return len(out.Predicted), out.NoLongerTies
	}
	if predicted, ties := read(); predicted != 1 || len(ties) != 1 || ties[0]["workpaper_id"] != paperID || ties[0]["account"] != "Operating bank" || ties[0]["balance"] != "900.00" || ties[0]["expected"] != "1000.00" || ties[0]["difference"] != "-100.00" {
		t.Fatalf("a Trial Balance captured after approval that no longer ties lists the draft with both balances: %d %v", predicted, ties)
	}
	// The mismatch is listed whether or not a transaction was updated after the approval.
	_, err = db.Exec(`UPDATE quickbooks_transaction_rows SET provider_updated_at=now()-interval '2 days' WHERE sync_id::text=$1`, syncID)
	must(err)
	if predicted, ties := read(); predicted != 0 || len(ties) != 1 {
		t.Fatalf("a mismatch without a later transaction update is still listed: %d %v", predicted, ties)
	}
	// Approved again after that Trial Balance, the draft is no longer listed: the reviewer saw it.
	approve(paperID, "1 hour")
	if _, ties := read(); len(ties) != 0 {
		t.Fatalf("a Trial Balance captured before the approval does not list it: %v", ties)
	}
}
