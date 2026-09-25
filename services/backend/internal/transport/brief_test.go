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

func TestCloseBriefNarratesWhatNeedsAPerson(t *testing.T) {
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
	var closeID, connection, evidenceID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Brief "+rand.Text()).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM close_scopes WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	type brief struct {
		Headline string
		Lines    []struct{ Area, State, Text string }
	}
	read := func() (b brief, areas string) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/brief", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil {
			t.Fatalf("brief: %d %s", w.Code, w.Body.String())
		}
		states := []string{}
		for _, line := range b.Lines {
			states = append(states, line.Area+"="+line.State)
		}
		return b, strings.Join(states, " ")
	}

	b, states := read()
	if states != "ledger=attention preparation=attention review=info balance sheet=waiting exceptions=waiting sign-off=waiting" || b.Headline != "2 things need you: capture a month-end snapshot, add client documents." {
		t.Fatalf("an empty close asks for a snapshot and documents: %s | %s", states, b.Headline)
	}
	_, err = db.Exec(`INSERT INTO close_scopes(close_id,scope,actor) VALUES($1,'{"description":"Bookkeeping close","required_papers":[],"external_steps":[]}','fixture')`, closeID)
	must(err)
	if b, states = read(); !strings.HasSuffix(states, "sign-off=attention") || !strings.HasSuffix(b.Headline, ", sign off the close.") {
		t.Fatalf("a close with nothing blocking sign-off asks a person to sign off: %s | %s", states, b.Headline)
	}

	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidenceID))
	payload := `{"period":"2026-09","source_id":"` + evidenceID + `","accounts":[{"Id":"1","Name":"Prepaid insurance","AccountType":"Other Current Asset","Classification":"Asset"},{"Id":"2","Name":"Loan payable","AccountType":"Long Term Liability","Classification":"Liability"},{"Id":"3","Name":"Checking","AccountType":"Bank","Classification":"Asset"},{"Id":"4","Name":"Accrued liabilities","AccountType":"Other Current Liability","Classification":"Liability"},{"Id":"5","Name":"Utilities","AccountType":"Expense","Classification":"Expense"}],
		"reports":{"TrialBalance":{"Header":{"Currency":"USD"},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Prepaid insurance"},{"value":"1000.00"},{"value":""}]},{"type":"Data","ColData":[{"id":"2","value":"Loan payable"},{"value":""},{"value":"5000.00"}]},{"type":"Data","ColData":[{"id":"3","value":"Checking"},{"value":""},{"value":"50.00"}]},{"type":"Data","ColData":[{"id":"4","value":"Accrued liabilities"},{"value":""},{"value":"400.00"}]},{"type":"Data","ColData":[{"id":"5","value":"Utilities"},{"value":""},{"value":""}]}]}}}}`
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), payload)
	must(err)
	for _, state := range []string{"completed", "needs_confirmation"} {
		_, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]',$3,'fixture')`, closeID, rand.Text(), state)
		must(err)
	}
	var documentID, documentDigest string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'policy.txt',$2,'Allianz policy','fixture') RETURNING id::text,digest`, closeID, rand.Text()).Scan(&documentID, &documentDigest))
	sources := `"sources":[{"id":"` + documentID + `","digest":"` + documentDigest + `"}]`
	save := func(kind, paper string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) RETURNING id::text`, closeID, kind, rand.Text(), paper).Scan(&id))
		return id
	}
	older := save("prepaid", `{"input":{"source_reference":"Allianz policy","prepaid_account":"Prepaid insurance"},"prepaid_balance":"1000.00",`+sources+`}`)
	save("prepaid", `{"revises":"`+older+`","input":{"source_reference":"Allianz policy","prepaid_account":"Prepaid insurance"},"prepaid_balance":"900.00",`+sources+`}`)
	save("accrual", `{"input":{"source_reference":"City utilities","liability_account":"Accrued liabilities","amount":"500.00"},"proposal":{"date":"2026-09-30","debit_account":"Utilities","credit_account":"Accrued liabilities","amount":"100.00"},`+sources+`}`)
	save("owner", `{"input":{"transaction_reference":"Card 4411"}}`)
	save("reconciliation", `{"input":{"account":"Checking","ledger_closing":"-50.00"},"blockers":["Statement page 2 is missing."],`+sources+`}`)

	b, states = read()
	if states != "ledger=done preparation=attention review=attention balance sheet=attention exceptions=attention sign-off=waiting" {
		t.Fatalf("each area reports its own state: %s", states)
	}
	if want := "8 things need you: confirm 1 document, approve 2 drafts, add evidence to 1 draft, fix 1 draft, explain 1 balance difference, support 1 balance, post the proposed entries for 1 balance, send 1 client question."; b.Headline != want {
		t.Errorf("headline %q, want %q", b.Headline, want)
	}
	texts := map[string]string{}
	for _, line := range b.Lines {
		texts[line.Area] = line.Text
	}
	for area, want := range map[string]string{
		"preparation":   "2 documents: 1 prepared, 1 need your confirmation.",
		"review":        "Of 4 current drafts, 0 approved, 2 ready for your approval and 1 need attention (blockers, requested changes or changed evidence). 1 draft cites no evidence yet and cannot be approved.",
		"balance sheet": "1 of 4 balance-sheet accounts tie. Tie once proposed entries post: Accrued liabilities. Differ: Prepaid insurance. No support yet: Loan payable. 2 matches rest on unapproved or blocked support.",
		"exceptions":    "The agent found 1 question for the client. Transactions are not scanned until a capture runs.",
	} {
		if !strings.HasPrefix(texts[area], want) {
			t.Errorf("%s says %q, want %q", area, texts[area], want)
		}
	}

	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id=$1`, connection)
	must(err)
	if b, states = read(); !strings.HasPrefix(states, "ledger=attention") || !strings.Contains(b.Headline, "capture a new snapshot") {
		t.Fatalf("a snapshot from an inactive connection is out of date: %s | %s", states, b.Headline)
	}
	save("prepaid", `{"input":{"source_reference":"Broken","prepaid_account":"Prepaid insurance"},"prepaid_balance":"not a number"}`)
	if b, states = read(); !strings.HasPrefix(states, "ledger=unknown") || !strings.Contains(states, "balance sheet=unknown") || !strings.HasSuffix(b.Headline, " Not checked: ledger, balance sheet.") {
		t.Fatalf("an unreadable source is reported as not checked, never as clear: %s | %s", states, b.Headline)
	}
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+evidenceID+`","accounts":[],"reports":{"TrialBalance":{"Columns":{"Column":[]},"Rows":{}}}}`)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at) VALUES($1,$2,'2026-09','fixture','completed',$3,now())`, closeID, connection, evidenceID)
	must(err)
	if b, states = read(); !strings.HasPrefix(states, "ledger=unknown") || !strings.Contains(states, "balance sheet=unknown exceptions=unknown") || !strings.HasSuffix(b.Headline, " Not checked: ledger, balance sheet, exceptions.") {
		t.Fatalf("a Trial Balance that cannot be read is never reported as read: %s | %s", states, b.Headline)
	}
}
