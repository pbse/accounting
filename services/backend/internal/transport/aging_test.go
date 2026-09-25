package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAgingUsesTheLatestCaptureAndDownloadsAStatement(t *testing.T) {
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
	entity := "Aging " + rand.Text()
	var closeID, empty, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Empty "+entity).Scan(&empty))
	defer func() {
		for _, id := range []string{closeID, empty} {
			db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123') RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	capture := func(state, completeness, balance string, extra ...any) {
		t.Helper()
		invoices := []any{map[string]any{"Id": "1", "DocNumber": "INV-1", "TxnDate": "2026-08-01", "DueDate": "2026-08-31", "Balance": json.Number(balance), "CustomerRef": map[string]string{"value": "5", "name": "=Acme"}, "CurrencyRef": map[string]string{"value": "USD"}},
			map[string]any{"Id": "3", "TxnDate": "2026-08-01", "DueDate": "2026-08-31", "Balance": json.Number("7.00"), "CurrencyRef": map[string]string{"value": "USD"}}}
		payload, _ := json.Marshal(map[string]any{"finished_at": "2026-10-02T09:00:00Z", "completeness": completeness, "open_items": map[string]any{
			"Invoice": append(invoices, extra...),
			"Payment": []map[string]any{{"Id": "2", "TxnDate": "2026-09-20", "UnappliedAmt": json.Number("25.00"), "CustomerRef": map[string]string{"value": "5", "name": "=Acme"}, "CurrencyRef": map[string]string{"value": "USD"}}},
		}})
		_, e := db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) VALUES($1,$2,'2026-09','fixture',$3,now(),$4)`, closeID, connection, state, string(payload))
		must(e)
	}
	capture("completed", "All supported types were paged.", "100.00")
	capture("unchanged", "All supported types were paged. Open items were not fully read, so their overdue questions are missing: Open Bill count: refused.", "40.00",
		map[string]any{"Id": "4", "DocNumber": "INV-4", "TxnDate": "=cmd", "DueDate": "2026-09-10", "Balance": json.Number("1.00"), "CustomerRef": map[string]string{"value": "5", "name": "=Acme"}, "CurrencyRef": map[string]string{"value": "USD"}})
	token := strings.Repeat("g", 32)
	handler := apiWithStore(db, token)
	get := func(path string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	var waiting struct{ State string }
	json.Unmarshal(get("/v1/closes/"+empty+"/aging", 200).Body.Bytes(), &waiting)
	if waiting.State != "waiting" {
		t.Fatal("without a capture the aging waits")
	}
	var aging struct {
		State       string
		Complete    bool
		Note        string
		Receivables []struct {
			ID, Open, Credits string
			Buckets           [5]string
		}
	}
	json.Unmarshal(get("/v1/closes/"+closeID+"/aging", 200).Body.Bytes(), &aging)
	acme := aging.Receivables[len(aging.Receivables)-1]
	if aging.State != "ready" || aging.Complete || !strings.Contains(aging.Note, "capture time") || len(aging.Receivables) != 2 || acme.ID != "5" || acme.Buckets[1] != "41.00" || acme.Credits != "25.00" {
		t.Fatalf("the latest capture's open items, flagged incomplete, labeled as capture-time balances: %+v", aging)
	}
	statement := get("/v1/closes/"+closeID+"/aging/statement?customer=5&currency=USD", 200).Body.String()
	if !strings.Contains(statement, "'=Acme") || !strings.Contains(statement, "INV-4,'=cmd") || !strings.Contains(statement, "Incomplete") || !strings.Contains(statement, "INV-1,2026-08-01,2026-08-31,30,40.00") || !strings.Contains(statement, "not applied to any invoice here") {
		t.Fatalf("the statement is safe to open in a spreadsheet, states its basis and lists the documents: %s", statement)
	}
	get("/v1/closes/"+closeID+"/aging/statement?customer=6&currency=USD", 404)
	get("/v1/closes/"+closeID+"/aging/statement?customer=&currency=USD", 404)
	capture("unchanged", "All supported types were paged.", "40.00", "not a document")
	json.Unmarshal(get("/v1/closes/"+closeID+"/aging", 200).Body.Bytes(), &aging)
	if aging.Complete {
		t.Fatal("a record the aging cannot read makes it incomplete")
	}
	capture("unchanged", "All supported types were paged.", "40.00", map[string]any{"Id": "9", "TxnDate": "2026-09-01", "DueDate": "2026-09-10", "Balance": "unreadable", "CustomerRef": map[string]string{"value": "5", "name": "=Acme"}})
	json.Unmarshal(get("/v1/closes/"+closeID+"/aging", 200).Body.Bytes(), &aging)
	if aging.Complete {
		t.Fatal("a balance the aging cannot read makes it incomplete")
	}
	if !strings.Contains(statement, "Payment,2,2026-09-20") {
		t.Fatalf("a document without a number is named by its QuickBooks ID: %s", statement)
	}
}
