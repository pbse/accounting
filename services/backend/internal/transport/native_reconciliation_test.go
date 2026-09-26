package transport

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNativeReconciliationReadsXerosFlagsForTheBooksInUse(t *testing.T) {
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
	var closeID, xeroConnection, quickbooksConnection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Native "+rand.Text()).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM xero_snapshots WHERE close_id=$1`, `DELETE FROM xero_connections WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&xeroConnection))
	payload := `{"date":"2026-09-30","accounts":{"Accounts":[{"AccountID":"b-1","Name":"Operating","Type":"BANK","Class":"ASSET"}]},"transactions":{"records":{"BankTransactions":[{"Status":"AUTHORISED","IsReconciled":true,"BankAccount":{"AccountID":"b-1"}},{"Status":"AUTHORISED","IsReconciled":false,"BankAccount":{"AccountID":"b-1"}}],"Payments":[]}}}`
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor) VALUES($1,$2,'t-1','2026-09',encode(sha256(convert_to($3::jsonb::text,'UTF8')),'hex'),$3,'fixture')`, closeID, xeroConnection, payload)
	must(err)
	token := strings.Repeat("n", 32)
	handler := apiWithStore(db, token)
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	var native struct {
		Status   string `json:"status"`
		Reason   string `json:"reason"`
		Accounts []struct {
			Account                  string
			Reconciled, Unreconciled int
		} `json:"accounts"`
	}
	must(json.Unmarshal(get("/native-reconciliation").Body.Bytes(), &native))
	if native.Status != "read" || len(native.Accounts) != 1 || native.Accounts[0].Reconciled != 1 || native.Accounts[0].Unreconciled != 1 {
		t.Fatalf("Xero's flags are read for the close's bank account: %+v", native)
	}
	exported := get("/export")
	archive, err := zip.NewReader(bytes.NewReader(exported.Body.Bytes()), int64(exported.Body.Len()))
	must(err)
	for _, file := range archive.File {
		if file.Name == "review.html" {
			reader, e := file.Open()
			must(e)
			page, _ := io.ReadAll(reader)
			reader.Close()
			if !strings.Contains(string(page), "Xero flagged 1 of 2 captured records for Operating as reconciled") || !strings.Contains(string(page), "statement settlement were not verified") {
				t.Fatalf("the binder states Xero's flags without claiming settlement: %.400s", page)
			}
		}
	}
	// Books read from a newer QuickBooks snapshot have no provider flags to read.
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&quickbooksConnection))
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123','d','{"period":"2026-09"}',now(),now()+interval '1 minute','fixture')`, closeID, quickbooksConnection)
	must(err)
	native.Status, native.Accounts = "", nil
	must(json.Unmarshal(get("/native-reconciliation").Body.Bytes(), &native))
	if native.Status != "unavailable" || !strings.Contains(native.Reason, "QuickBooks") {
		t.Fatalf("a QuickBooks close keeps the reviewer's observation: %+v", native)
	}
}
