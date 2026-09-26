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

	"close.local/backend/internal/preparation"
)

func TestLedgerCertificationComparesStoredReports(t *testing.T) {
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
	var closeID, connection, captureEvidence, reportEvidence, syncID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Certified "+rand.Text()).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'transactions.json',$2,'{}','quickbooks-sync') RETURNING id::text`, closeID, rand.Text()).Scan(&captureEvidence))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'reports.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&reportEvidence))
	counts := `{"Bill":1,"BillPayment":0,"CreditMemo":0,"Deposit":0,"Invoice":0,"JournalEntry":0,"Payment":0,"Purchase":1,"RefundReceipt":0,"SalesReceipt":0,"Transfer":0,"VendorCredit":0}`
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now(),$4) RETURNING id::text`, closeID, connection, captureEvidence, `{"counts":`+counts+`}`).Scan(&syncID))
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,record) VALUES ($1,'Bill','41','1','2026-09-10',now(),'{}'),($1,'Purchase','52','1','2026-09-11',now(),'{}')`, syncID)
	must(err)
	listed := func(rows ...string) string {
		return `{"Columns":{"Column":[{"ColType":"Date","MetaData":[{"Name":"ColKey","Value":"tx_date"}]},{"ColType":"String","MetaData":[{"Name":"ColKey","Value":"txn_type"}]}]},"Rows":{"Row":[` + strings.Join(rows, ",") + `]}}`
	}
	row := func(kind, id string) string {
		return `{"type":"Data","ColData":[{"value":"2026-09-10"},{"value":"` + kind + `","id":"` + id + `"}]}`
	}
	snapshot := func(transactions string) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(),
			`{"period":"2026-09","source_id":"`+reportEvidence+`","accounts":[{"Id":"1","Name":"Checking","AccountType":"Bank","Classification":"Asset"}],"reports":{"TransactionList":`+transactions+`,"TrialBalance":{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Checking"},{"value":"100.00"},{"value":""}]}]}}}}`)
		must(e)
	}
	token := strings.Repeat("c", 32)
	handler := apiWithStore(db, token)
	get := func(path string, into any) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), into) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	type result struct {
		Status        string `json:"status"`
		Provisional   bool   `json:"provisional"`
		Reason        string `json:"reason"`
		Certification struct {
			Reported int      `json:"reported"`
			Missing  []string `json:"missing"`
		} `json:"certification"`
	}
	check := func() (got result) {
		t.Helper()
		get("/ledger-certification", &got)
		return got
	}
	if got := check(); got.Status != "unavailable" || !strings.Contains(got.Reason, "period reports") {
		t.Fatalf("without period reports there is nothing to certify: %+v", got)
	}
	snapshot(listed(row("Bill", "41"), row("Expense", "52")))
	if got := check(); got.Status != "agrees" || got.Certification.Reported != 2 || got.Provisional {
		t.Fatalf("the report and the capture agree: %+v", got)
	}
	snapshot(listed(row("Bill", "41"), row("Expense", "52"), row("Invoice", "130")))
	if got := check(); got.Status != "differs" || strings.Join(got.Certification.Missing, ",") != "Invoice 130" {
		t.Fatalf("the newest report lists a transaction the capture lacks: %+v", got)
	}
	var brief struct {
		Lines []struct{ Area, State, Text string } `json:"lines"`
	}
	get("/brief", &brief)
	for _, line := range brief.Lines {
		if line.Area == "ledger" && (line.State != "attention" || !strings.Contains(line.Text, "stored reads differ")) {
			t.Fatalf("the brief says the reads differ: %+v", line)
		}
	}
	// The close package says what the certification found, in its manifest and on the binder page.
	rows, err := db.Query(`SELECT id,payload FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
	must(err)
	for rows.Next() {
		var id int64
		var payload []byte
		must(rows.Scan(&id, &payload))
		_, e := db.Exec(`UPDATE quickbooks_snapshots SET digest=$2 WHERE id=$1`, id, preparation.Digest(payload))
		must(e)
	}
	rows.Close()
	r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/export", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("export: %d %s", w.Code, w.Body.String())
	}
	files := map[string]string{}
	for _, file := range archive.File {
		reader, e := file.Open()
		must(e)
		body, e := io.ReadAll(reader)
		must(e)
		reader.Close()
		files[file.Name] = string(body)
	}
	if !strings.Contains(files["manifest.json"], `"status": "differs"`) || !strings.Contains(files["review.html"], "stored reads differ") {
		t.Fatalf("the package records the certification: %.300s", files["manifest.json"])
	}
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state) VALUES($1,$2,'2026-09','fixture','queued')`, closeID, connection)
	must(err)
	if got := check(); !got.Provisional {
		t.Fatalf("a capture still running makes the comparison provisional: %+v", got)
	}
	_, err = db.Exec(`UPDATE quickbooks_entity_syncs SET payload='{"counts":{"Bill":1}}' WHERE id=$1`, syncID)
	must(err)
	if got := check(); got.Status != "unavailable" || !strings.Contains(got.Reason, "count") {
		t.Fatalf("a capture without every type's count is not certified: %+v", got)
	}
}
