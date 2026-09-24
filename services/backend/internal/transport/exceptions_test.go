package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestExceptionsScanTheWholeLatestCapture(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID, connection, evidenceID, syncID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Exceptions "+rand.Text()).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	scan := func() (out struct {
		Capture  *struct{ ID string }    `json:"capture"`
		Scanned  int                     `json:"scanned"`
		Items    []struct{ Type string } `json:"items"`
		Balances struct {
			Snapshot *struct{ ID, Source string } `json:"snapshot"`
			Items    []struct{ Account string }   `json:"items"`
		} `json:"balances"`
	}) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/quickbooks/exceptions", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("exceptions: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	if got := scan(); got.Capture != nil || len(got.Items) != 0 || got.Balances.Snapshot != nil {
		t.Fatalf("without a capture or snapshot nothing is scanned: %+v", got)
	}
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'capture.json',$2,'{}','fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(rand.Text()))).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	snapshot := func(receivable, card string) (id string) {
		t.Helper()
		payload := `{"period":"2026-09","source_id":"` + evidenceID + `","accounts":[{"Id":"1","Name":"Accounts Receivable","AccountType":"Accounts Receivable"},{"Id":"2","Name":"Business card","AccountType":"Credit Card"}],
			"reports":{"TrialBalance":{"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Accounts Receivable"},` + receivable + `]},{"type":"Data","ColData":[{"id":"2","value":"Business card"},` + card + `]}]}}}}`
		if err := db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture') RETURNING id::text`, closeID, connection, rand.Text(), payload).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	snapshot(`{"value":""},{"value":"150.00"}`, `{"value":""},{"value":"10.00"}`)
	latest := snapshot(`{"value":"150.00"},{"value":""}`, `{"value":"20.00"},{"value":""}`)
	if got := scan(); got.Capture != nil || got.Balances.Snapshot == nil || got.Balances.Snapshot.ID != latest || len(got.Balances.Items) != 1 || got.Balances.Items[0].Account != "Business card" {
		t.Fatalf("balances come from the latest period snapshot even without a capture: %+v", got)
	}
	var xeroConnection, xeroSnapshot string
	if err = db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&xeroConnection); err != nil {
		t.Fatal(err)
	}
	xeroPayload := `{"date":"2026-09-30","organisation":{"Organisations":[{"BaseCurrency":"USD"}]},"accounts":{"Accounts":[{"AccountID":"a-1","Code":"090","Name":"Operating Account","Class":"ASSET"}]},
		"trial_balance":{"Reports":[{"Rows":[{"RowType":"Header","Cells":[{"Value":"Account"},{"Value":"Debit"},{"Value":"Credit"},{"Value":"YTD Debit"},{"Value":"YTD Credit"}]},{"RowType":"Row","Cells":[{"Value":"Operating Account","Attributes":[{"Id":"account","Value":"a-1"}]},{"Value":""},{"Value":""},{"Value":""},{"Value":"45.00"}]}]}]}}`
	if err = db.QueryRow(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d',$3,'fixture',now()+interval '1 minute') RETURNING id::text`, closeID, xeroConnection, xeroPayload).Scan(&xeroSnapshot); err != nil {
		t.Fatal(err)
	}
	if got := scan(); got.Balances.Snapshot == nil || got.Balances.Snapshot.Source != "xero" || got.Balances.Snapshot.ID != xeroSnapshot || len(got.Balances.Items) != 1 || got.Balances.Items[0].Account != "Operating Account" {
		t.Fatalf("a Xero snapshot captured after the QuickBooks one supplies the balances: %+v", got)
	}
	// A later row that was captured earlier does not replace the most recent capture.
	if _, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d',$3,'fixture',now()-interval '2 hours')`, closeID, xeroConnection, xeroPayload); err != nil {
		t.Fatal(err)
	}
	if got := scan(); got.Balances.Snapshot == nil || got.Balances.Snapshot.ID != xeroSnapshot {
		t.Fatalf("the most recently captured Xero snapshot is used, not the highest ID: %+v", got)
	}
	if _, err = db.Exec(`UPDATE xero_snapshots SET captured_at=now()-interval '1 hour' WHERE id=$1`, xeroSnapshot); err != nil {
		t.Fatal(err)
	}
	if got := scan(); got.Balances.Snapshot == nil || got.Balances.Snapshot.Source != "quickbooks" || got.Balances.Snapshot.ID != latest {
		t.Fatalf("a newer QuickBooks snapshot supplies the balances instead: %+v", got)
	}
	if err = db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now()) RETURNING id::text`, closeID, connection, evidenceID).Scan(&syncID); err != nil {
		t.Fatal(err)
	}
	record := `{"EntityRef":{"value":"7","name":"CloudSuite"},"Line":[]}`
	for i, kind := range []string{"Purchase", "Purchase", "Bill"} {
		if _, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,$2,$3,'0',$4,now(),'USD','49.00',$5)`, syncID, kind, string(rune('1'+i)), "2026-09-0"+string(rune('2'+i)), record); err != nil {
			t.Fatal(err)
		}
	}
	got := scan()
	if got.Capture == nil || got.Capture.ID != syncID || got.Scanned != 3 || len(got.Items) != 1 || got.Items[0].Type != "possible_duplicate" {
		t.Fatalf("every captured row must be scanned and the nearby pair raised: %+v", got)
	}
	// A later unchanged capture reuses those rows and records its own findings, including open items.
	var unchanged string
	if err = db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at,exceptions) VALUES($1,$2,'2026-09','fixture','unchanged',$3,now(),'[{"type":"possible_duplicate","kind":"Purchase","provider_ids":["1","2"]},{"type":"past_due_receivable","kind":"Invoice","provider_ids":["77"]}]') RETURNING id::text`, closeID, connection, evidenceID).Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if got = scan(); got.Capture == nil || got.Capture.ID != unchanged || got.Scanned != 3 || len(got.Items) != 2 || got.Items[1].Type != "past_due_receivable" {
		t.Fatalf("the close shows the latest finished capture's findings, as Daily books does: %+v", got)
	}
}
