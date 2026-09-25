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

func TestXeroTransactionsDriveTheCloseChecks(t *testing.T) {
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
	var closeID, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Xero books "+rand.Text()).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM xero_snapshots WHERE close_id=$1`, `DELETE FROM xero_connections WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	contact := map[string]string{"ContactID": "c-1", "Name": "Harbor Insurance"}
	payload, _ := json.Marshal(map[string]any{"date": "2026-09-30", "organisation": map[string]any{"Organisations": []any{map[string]any{"BaseCurrency": "USD"}}},
		"accounts": map[string]any{"Accounts": []any{
			map[string]string{"AccountID": "a-620", "Code": "620", "Name": "Prepayments", "Type": "PREPAYMENT"},
			map[string]string{"AccountID": "a-999", "Code": "999", "Name": "Suspense", "Type": "CURRLIAB"},
		}},
		"trial_balance": map[string]any{"Reports": []any{map[string]any{"Rows": []any{map[string]any{"RowType": "Header", "Cells": []any{map[string]string{"Value": "Account"}, map[string]string{"Value": "Debit"}, map[string]string{"Value": "Credit"}, map[string]string{"Value": "YTD Debit"}, map[string]string{"Value": "YTD Credit"}}}}}}},
		"transactions": map[string]any{
			"records": map[string]any{
				"Invoices":         []any{map[string]any{"Type": "ACCPAY", "InvoiceID": "bill-1", "InvoiceNumber": "INS-88", "Status": "AUTHORISED", "Date": "2026-09-10T00:00:00", "Contact": contact, "CurrencyCode": "USD", "Total": 1200, "LineItems": []any{map[string]any{"AccountCode": "620", "LineAmount": 1200}}}},
				"BankTransactions": []any{map[string]any{"Type": "SPEND", "BankTransactionID": "bt-1", "Status": "AUTHORISED", "Date": "2026-09-15T00:00:00", "Contact": map[string]string{"ContactID": "c-3", "Name": "Coffee Co"}, "CurrencyCode": "USD", "Total": 12, "LineItems": []any{map[string]any{"AccountCode": "999", "LineAmount": 12}}}},
			},
			"open": map[string]any{"Invoices": []any{
				map[string]any{"Type": "ACCPAY", "InvoiceID": "bill-9", "InvoiceNumber": "B-9", "Status": "AUTHORISED", "Date": "2026-07-01T00:00:00", "DueDate": "2026-07-31T00:00:00", "Contact": contact, "CurrencyCode": "USD", "AmountDue": 80},
				map[string]any{"Type": "ACCREC", "InvoiceID": "inv-9", "InvoiceNumber": "INV-9", "Status": "AUTHORISED", "Date": "2026-09-01T00:00:00", "DueDate": "2026-09-30T00:00:00", "Contact": map[string]string{"ContactID": "c-2", "Name": "Acme"}, "CurrencyCode": "USD", "AmountDue": 300},
			}},
		}})
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d',$3,'fixture',now()-interval '1 hour')`, closeID, connection, string(payload))
	must(err)
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	get := func(path string, out any) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), out) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
	var status struct {
		Latest struct {
			Transactions struct{ Counts map[string]int }
		}
	}
	if get("/xero", &status); status.Latest.Transactions.Counts["Invoices"] != 1 || status.Latest.Transactions.Counts["BankTransactions"] != 1 {
		t.Fatalf("the Xero panel says how many transactions the snapshot holds: %+v", status)
	}
	type scan struct {
		Capture *struct{ ID, Source, Unread string }
		Scanned int
		Items   []struct{ Type, Question string }
		Scans   struct{ State string } `json:"schedule_scan"`
	}
	var got scan
	get("/quickbooks/exceptions", &got)
	types := []string{}
	for _, item := range got.Items {
		types = append(types, item.Type)
		if strings.Contains(item.Question, "QuickBooks") {
			t.Fatalf("a Xero client's questions name Xero: %s", item.Question)
		}
	}
	if got.Capture == nil || got.Capture.Source != "xero" || got.Scanned != 2 || got.Scans.State != "checked" || strings.Join(types, ",") != "uncategorized,past_due_payable,unscheduled_prepaid" {
		t.Fatalf("the Xero snapshot's transactions drive the exceptions and schedule discovery: %+v", got)
	}
	var matching struct {
		State        string
		Transactions []struct{ Name string } `json:"transactions_without_document"`
	}
	get("/matching", &matching)
	if matching.State != "ready" || len(matching.Transactions) != 2 || matching.Transactions[0].Name != "Harbor Insurance" {
		t.Fatalf("the Xero bill and purchase are matched against documents: %+v", matching)
	}
	var aging struct {
		State       string
		Receivables []struct{ Name, Open string }
		Payables    []struct{ Name, Open string }
	}
	get("/aging", &aging)
	if aging.State != "ready" || len(aging.Receivables) != 1 || aging.Receivables[0].Open != "300.00" || len(aging.Payables) != 1 || aging.Payables[0].Open != "80.00" {
		t.Fatalf("the Xero open items are aged: %+v", aging)
	}
	// A snapshot for another month is not this close's books.
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-08','d','{"date":"2026-08-31"}','fixture',now()-interval '45 minutes')`, closeID, connection)
	must(err)
	got = scan{}
	if get("/quickbooks/exceptions", &got); got.Capture == nil || got.Capture.Unread != "" || got.Scanned != 2 {
		t.Fatalf("another month's snapshot is ignored: %+v", got)
	}
	// A newer snapshot without transactions says why; the older one's transactions are not shown in its place.
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d','{"date":"2026-09-30","transactions_error":"Reconnect Xero to read transactions."}','fixture',now()-interval '30 minutes')`, closeID, connection)
	must(err)
	got = scan{}
	get("/quickbooks/exceptions", &got)
	var waiting struct{ State, Reason string }
	get("/matching", &waiting)
	var agingWait struct{ State, Reason string }
	get("/aging", &agingWait)
	if got.Capture == nil || got.Capture.Unread != "Reconnect Xero to read transactions." || len(got.Items) != 0 || waiting.State != "waiting" || waiting.Reason != got.Capture.Unread || agingWait.Reason != got.Capture.Unread {
		t.Fatalf("the newest snapshot's missing transactions are explained everywhere: %+v %+v %+v", got, waiting, agingWait)
	}
	// A QuickBooks capture finished after the Xero snapshots is the client's books instead.
	var quickbooksConnection, evidence string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&quickbooksConnection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'capture.json',$2,'{}','fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(rand.Text()))).Scan(&evidence))
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at,exceptions) VALUES($1,$2,'2026-09','fixture','completed',$3,now(),'[]')`, closeID, quickbooksConnection, evidence)
	must(err)
	got = scan{}
	get("/quickbooks/exceptions", &got)
	if got.Capture == nil || got.Capture.Source != "quickbooks" {
		t.Fatalf("the newer QuickBooks capture is read: %+v", got)
	}
	// Once that capture's evidence is superseded it is history, and the Xero capture is read again.
	var newer string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'capture.json',$2,'{}','fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(rand.Text()))).Scan(&newer))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$1 WHERE id=$2`, newer, evidence)
	must(err)
	got = scan{}
	if get("/quickbooks/exceptions", &got); got.Capture == nil || got.Capture.Source != "xero" {
		t.Fatalf("a superseded QuickBooks capture does not hide the Xero books: %+v", got)
	}
}
