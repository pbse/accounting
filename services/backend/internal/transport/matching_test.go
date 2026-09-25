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

func TestMatchPairsOnlyUniqueCurrencyAndAmount(t *testing.T) {
	item := func(id, currency, amount string) matchItem {
		i := matchItem{ID: id, Currency: currency}
		matchAmount(&i, amount)
		return i
	}
	docs := []matchItem{item("d1", "USD", "100"), item("d2", "USD", "50.00"), item("d3", "USD", "50"), item("d4", "USD", "70"), item("d5", "EUR", "10"), item("d6", "USD", "5")}
	txns := []matchItem{item("t1", "USD", "100.00"), item("t2", "USD", "50"), item("t3", "USD", "70"), item("t4", "USD", "70"), item("t5", "USD", "10"), item("t6", "USD", "900"), item("t7", "USD", "3")}
	if matchAmount(&matchItem{}, "1,2") {
		t.Fatal("grouped digits are unreadable, not 12")
	}
	pairs, ambiguous, docsLeft, txnsLeft := match(docs, txns)
	if len(pairs) != 1 || pairs[0].Documents[0].ID != "d1" || pairs[0].Transactions[0].ID != "t1" {
		t.Fatalf("one document and one transaction with the same currency and cents pair: %+v", pairs)
	}
	if len(ambiguous) != 2 || len(ambiguous[0].Documents) != 2 || len(ambiguous[1].Transactions) != 2 {
		t.Fatalf("two documents or two transactions on one amount are left to a person: %+v", ambiguous)
	}
	if len(docsLeft) != 2 || docsLeft[0].ID != "d5" || docsLeft[1].ID != "d6" {
		t.Fatalf("a currency never pairs with another: %+v", docsLeft)
	}
	if len(txnsLeft) != 3 || txnsLeft[0].ID != "t6" || txnsLeft[1].ID != "t5" || txnsLeft[2].ID != "t7" {
		t.Fatalf("unmatched transactions, largest first: %+v", txnsLeft)
	}
}

func TestMatchingReadsTheLatestCaptureAndQuotedInvoiceTotals(t *testing.T) {
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
	entity := "Matching " + rand.Text()
	var closeID, empty, connection, captureID, syncID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Empty "+entity).Scan(&empty))
	defer func() {
		db.Exec(`DELETE FROM preparation_intakes WHERE job_id IN (SELECT id FROM preparation_jobs WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id IN ($1,$2)`, closeID, empty)
	}()
	evidence := func(name string) (id string) {
		t.Helper()
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,'{}','fixture') RETURNING id::text`, closeID, name, preparation.Digest([]byte(rand.Text()))).Scan(&id))
		return id
	}
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123','USD' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	captureID = evidence("capture.json")
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now()) RETURNING id::text`, closeID, connection, captureID).Scan(&syncID))
	for _, row := range []struct{ kind, id, currency, amount, record string }{
		{"Bill", "1", "USD", "120", `{"DocNumber":"B-1","VendorRef":{"name":"Acme"}}`},
		{"Purchase", "2", "", "55.50", `{"EntityRef":{"name":"Shop"}}`},
		{"Invoice", "3", "USD", "120.00", `{}`},
		{"Purchase", "4", "USD", "", `{}`},
	} {
		_, err := db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,$2,$3,'0','2026-09-05',now(),NULLIF($4,''),NULLIF($5,''),$6)`, syncID, row.kind, row.id, row.currency, row.amount, row.record)
		must(err)
	}
	support := evidence("support.pdf")
	intake := func(name, kind, amount, quotedFrom string) (id string) {
		t.Helper()
		id = evidence(name)
		if quotedFrom == "" {
			quotedFrom = id
		}
		proposal, _ := json.Marshal(preparation.Proposal{Kind: kind, Facts: []preparation.Fact{{Field: "amount", Value: amount, Candidates: []preparation.Candidate{{Value: amount, SourceID: quotedFrom}}}}})
		var job string
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, closeID, "intake:"+id).Scan(&job))
		_, err := db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,$3)`, job, id, string(proposal))
		must(err)
		return id
	}
	intake("insurance.pdf", "prepaid", "120.00", "")
	intake("software.pdf", "prepaid", "55.50", support)
	intake("rent.pdf", "prepaid", " 1999", "")
	intake("statement.pdf", "reconciliation", "120.00", "")
	old := intake("old.pdf", "prepaid", "7.00", "")
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$1 WHERE id=$2`, evidence("new.pdf"), old)
	must(err)
	token := strings.Repeat("m", 32)
	handler := apiWithStore(db, token)
	get := func(id string) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+id+"/matching", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var waiting struct{ State string }
	json.Unmarshal(get(empty), &waiting)
	if waiting.State != "waiting" {
		t.Fatal("without a capture matching waits")
	}
	var got struct {
		State                  string
		Pairs                  []matchGroup
		Ambiguous              []matchGroup
		DocumentsNotInCapture  []matchItem `json:"documents_not_in_capture"`
		DocumentsWithoutTotal  []matchItem `json:"documents_without_total"`
		TransactionsWithoutDoc []matchItem `json:"transactions_without_document"`
		Unreadable             int         `json:"unreadable_transactions"`
	}
	must(json.Unmarshal(get(closeID), &got))
	if got.State != "ready" || len(got.Pairs) != 1 || got.Pairs[0].Documents[0].Name != "insurance.pdf" || got.Pairs[0].Transactions[0].Name != "Acme" || got.Pairs[0].Transactions[0].Number != "B-1" || len(got.Ambiguous) != 0 {
		t.Fatalf("the quoted invoice total pairs with the one bill of that amount; invoices and reconciliations are not spend: %s", get(closeID))
	}
	if len(got.DocumentsWithoutTotal) != 1 || got.DocumentsWithoutTotal[0].Name != "software.pdf" || len(got.DocumentsNotInCapture) != 1 || got.DocumentsNotInCapture[0].Amount != "1999.00" {
		t.Fatalf("a total quoted from another source is not the invoice's; an unmatched total is listed: %s", get(closeID))
	}
	if len(got.TransactionsWithoutDoc) != 1 || got.TransactionsWithoutDoc[0].Currency != "USD" || got.TransactionsWithoutDoc[0].Name != "Shop" || got.Unreadable != 1 {
		t.Fatalf("a purchase without a currency is in the home currency; an unreadable total is counted: %s", get(closeID))
	}
}
