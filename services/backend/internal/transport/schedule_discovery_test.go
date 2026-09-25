package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLedgerAdditionsAskForASchedule(t *testing.T) {
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
	var closeID, connection, evidenceID, syncID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Schedules "+rand.Text()).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_transaction_rows WHERE sync_id IN (SELECT id FROM quickbooks_entity_syncs WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	type scan struct {
		Items []struct {
			Type, Account, Question string
			ProviderIDs             []string `json:"provider_ids"`
		}
		Schedules struct {
			State   string
			Unknown []string `json:"unknown_accounts"`
			Items   int      `json:"item_lines"`
		} `json:"schedule_scan"`
	}
	read := func() (s scan, asked string) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/quickbooks/exceptions", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
			t.Fatalf("exceptions: %d %s", w.Code, w.Body.String())
		}
		ids := []string{}
		for _, item := range s.Items {
			if strings.HasPrefix(item.Type, "unscheduled_") {
				ids = append(ids, item.Type+" "+item.ProviderIDs[0])
			}
		}
		slices.Sort(ids)
		return s, strings.Join(ids, ", ")
	}
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'capture.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidenceID))
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now()) RETURNING id::text`, closeID, connection, evidenceID).Scan(&syncID))
	bill := func(id, doc, account, amount string) {
		t.Helper()
		record := `{"DocNumber":"` + doc + `","VendorRef":{"value":"9","name":"Allianz"},"Line":[{"Amount":` + amount + `,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"` + account + `","name":"Account ` + account + `"}}}]}`
		_, e := db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill',$2,'0','2026-09-03',now(),'USD',$3,$4)`, syncID, id, amount, record)
		must(e)
	}
	brief := func() (exceptions, headline string) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/brief", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var b struct {
			Headline string
			Lines    []struct{ Area, Text string }
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil {
			t.Fatalf("brief: %d %s", w.Code, w.Body.String())
		}
		for _, line := range b.Lines {
			if line.Area == "exceptions" {
				exceptions = line.Text
			}
		}
		return exceptions, b.Headline
	}
	bill("1", "INV-88", "1", "12000")
	bill("2", "INV-90", "1", "12000")
	bill("3", "", "3", "2400")

	if s, asked := read(); asked != "" || s.Schedules.State != "no_directory" {
		t.Fatalf("without a snapshot's account list nothing is concluded: %q %+v", asked, s.Schedules)
	}
	if text, _ := brief(); !strings.HasSuffix(text, " Schedule discovery waits on a month-end snapshot.") {
		t.Fatalf("the brief says schedule discovery is waiting: %s", text)
	}
	payload := `{"period":"2026-09","source_id":"` + evidenceID + `","accounts":[{"Id":"1","Name":"Prepaid insurance","AccountType":"Other Current Asset","AccountSubType":"PrepaidExpenses"},{"Id":"3","Name":"Office equipment","AccountType":"Fixed Asset","AccountSubType":"MachineryAndEquipment"}],"reports":{"TrialBalance":{"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[]}}}}`
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), payload)
	must(err)
	revises := ""
	paper := func(reference, account, amount, currency string, schedule ...string) (id string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"revises": revises, "input": map[string]string{"source_reference": reference, "prepaid_account": account, "amount": amount, "currency": currency, "schedule": strings.Join(schedule, "")}})
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, rand.Text(), string(raw)).Scan(&id))
		return id
	}
	paper("Allianz INV-880 and INV-88-1", "Prepaid insurance", "12000.00", "USD")
	paper("Allianz INV-88 renewal", "Prepaid insurance", "12000.00", "EUR")
	paper("Allianz INV-90", "Prepaid software", "12000.00", "USD")
	paper("Dell laptops", "Accumulated depreciation", "2400.00", "USD", "depreciation")
	if _, asked := read(); asked != "unscheduled_fixed_asset 3, unscheduled_prepaid 1, unscheduled_prepaid 2" {
		t.Fatalf("a schedule citing another document, currency or account, or no document number, covers nothing: %q", asked)
	}
	paper("Allianz INV-88 renewal", "Prepaid Expenses:Prepaid insurance", "12000.00", "USD")
	s, asked := read()
	if asked != "unscheduled_fixed_asset 3, unscheduled_prepaid 2" || s.Schedules.State != "checked" {
		t.Fatalf("only the bill the schedule cites is covered; the same-amount bill still asks: %q %+v", asked, s.Schedules)
	}
	for _, item := range s.Items {
		if item.Type == "unscheduled_fixed_asset" && !strings.Contains(item.Question, "when was it placed in service") {
			t.Fatalf("the fixed asset question asks for the in-service date: %s", item.Question)
		}
	}
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,amount,record) VALUES($1,'Bill','6','0','2026-09-05',now(),'700','{"DocNumber":"INV-95","Line":[{"Amount":700,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"1","name":"Prepaid insurance"}}}]}')`, syncID)
	must(err)
	paper("Allianz INV-95", "Prepaid insurance", "700.00", "")
	if _, asked = read(); asked != "unscheduled_fixed_asset 3, unscheduled_prepaid 2, unscheduled_prepaid 6" {
		t.Fatalf("a transaction whose currency was not captured is never covered: %q", asked)
	}
	revises = paper("Allianz INV-90 renewal", "Prepaid insurance", "12000.00", "USD")
	if _, asked = read(); asked != "unscheduled_fixed_asset 3, unscheduled_prepaid 6" {
		t.Fatalf("a schedule citing INV-90 covers it: %q", asked)
	}
	paper("Allianz policy", "Prepaid insurance", "12000.00", "USD")
	if _, asked = read(); asked != "unscheduled_fixed_asset 3, unscheduled_prepaid 2, unscheduled_prepaid 6" {
		t.Fatalf("a revised schedule that no longer cites INV-90 stops covering it: %q", asked)
	}
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill','5','0','2026-09-04',now(),'USD','80','{"Line":[{"Amount":80,"ItemBasedExpenseLineDetail":{"ItemRef":{"value":"5","name":"Toner"}}}]}')`, syncID)
	must(err)
	if s, _ = read(); s.Schedules.State != "incomplete" || len(s.Schedules.Unknown) != 0 || s.Schedules.Items != 1 {
		t.Fatalf("an item line, whose account is not captured, makes the scan incomplete: %+v", s.Schedules)
	}
	bill("4", "INV-91", "77", "300")
	if s, _ = read(); s.Schedules.State != "incomplete" || len(s.Schedules.Unknown) != 1 || s.Schedules.Unknown[0] != "Account 77" || s.Schedules.Items != 1 {
		t.Fatalf("an account missing from the list makes the scan incomplete: %+v", s.Schedules)
	}
	if text, headline := brief(); !strings.HasSuffix(text, "not in the snapshot's account list: Account 77; 1 line posts through items whose accounts are not captured.") || !strings.HasSuffix(headline, ", schedule check.") {
		t.Fatalf("the brief names the incomplete schedule check: %s | %s", text, headline)
	}
}
