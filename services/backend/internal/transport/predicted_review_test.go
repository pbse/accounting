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
	if got := read().Predicted; len(got) != 1 || got[0].WorkpaperID != flagged || got[0].Transaction != "Bill 41" || got[0].Account != "Prepaid insurance" || got[0].Count != 1 {
		t.Fatalf("only the draft whose account a transaction updated after approval is flagged, not one already reopened (#%s): %+v", changed, got)
	}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed fixture','fixture')`, closeID)
	must(err)
	if got := read().Predicted; len(got) != 0 {
		t.Fatalf("a signed-off close is not listed: %+v", got)
	}
}
