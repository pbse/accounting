package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/reconciliation"
)

func TestTieOutComparesBalancesWithLatestSupport(t *testing.T) {
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
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Tie-out "+rand.Text()).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM xero_postings WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM xero_posting_grants WHERE connection_id IN (SELECT id FROM xero_connections WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM sandbox_postings WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	type row struct {
		Account, Status, Balance, Hint string
		Expected                       *string
		Provisional                    bool
		Entries                        []struct{ Posting string }
	}
	type board struct {
		Snapshot     *struct{ Source string }
		Rows         []row
		Unmatched    []struct{ Account, Reason string }
		Unclassified []struct{ Account string }
	}
	read := func() (b board, byName map[string]row) {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/tie-out", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil {
			t.Fatalf("tie-out: %d %s", w.Code, w.Body.String())
		}
		byName = map[string]row{}
		for _, r := range b.Rows {
			byName[r.Account] = r
		}
		return b, byName
	}
	if b, _ := read(); b.Snapshot != nil || len(b.Rows) != 0 {
		t.Fatalf("without a snapshot there is nothing to tie: %+v", b)
	}

	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidenceID))
	type account struct{ id, name, kind, class, debit, credit string }
	accounts := []account{
		{"1", "Prepaid insurance", "Other Current Asset", "Asset", "1000.00", ""},
		{"2", "Accrued liabilities", "Other Current Liability", "Liability", "", "400.00"},
		{"3", "Checking", "Bank", "Asset", "5000.00", ""},
		{"4", "Business card", "Credit Card", "Liability", "", "250.00"},
		{"5", "Loan payable", "Long Term Liability", "Liability", "", "10000.00"},
		{"6", "Office equipment", "Fixed Asset", "Asset", "3000.00", ""},
		{"7", "Accumulated depreciation", "Fixed Asset", "Asset", "", "600.00"},
		{"8", "Deferred revenue", "Other Current Liability", "Liability", "", "1200.00"},
		{"9", "Accrued payroll", "Other Current Liability", "Liability", "", "300.00"},
		{"10", "Accrued rent", "Other Current Liability", "Liability", "", "200.00"},
		{"11", "Term loan", "Long Term Liability", "Liability", "", ""},
		{"12", "Loan current portion", "Other Current Liability", "Liability", "", "1500.00"},
		{"13", "Utilities", "Expenses", "Expense", "900.00", ""},
		{"14", "Retained Earnings", "Equity", "Equity", "", "2000.00"},
		{"15", "Accrued audit fees", "Other Current Liability", "Liability", "", "50.00"},
		{"16", "Stripe clearing", "Other Current Asset", "Asset", "100.00", ""},
		{"17", "Server accumulated depreciation", "Fixed Asset", "Asset", "", "500.00"},
		{"18", "Accrued bank fees", "Other Current Liability", "Liability", "", "40.00"},
		{"19", "Accrued card fees", "Other Current Liability", "Liability", "", "40.00"},
	}
	directory, report := []map[string]string{}, []map[string]any{}
	for _, a := range accounts {
		directory = append(directory, map[string]string{"Id": a.id, "Name": a.name, "AccountType": a.kind, "Classification": a.class})
		report = append(report, map[string]any{"type": "Data", "ColData": []map[string]string{{"id": a.id, "value": a.name}, {"value": a.debit}, {"value": a.credit}}})
	}
	report = append(report, map[string]any{"type": "Data", "ColData": []map[string]string{{"id": "99", "value": "Old clearing"}, {"value": "12.00"}, {"value": ""}}})
	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": evidenceID, "accounts": directory,
		"reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": ""}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}}, "Rows": map[string]any{"Row": report}}}})
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), string(payload))
	must(err)

	save := func(kind string, paper any, approve bool) string {
		t.Helper()
		raw, _ := json.Marshal(paper)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) RETURNING id::text`, closeID, kind, rand.Text(), string(raw)).Scan(&id))
		if approve {
			_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,'approved','Reviewed tie-out fixture','user_reviewer' FROM workpapers WHERE id=$1`, id)
			must(e)
		}
		return id
	}
	post := func(paperID, state, verified string) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO sandbox_postings(close_id,workpaper_id,source_key,realm,digest,journal,state,verified_at) VALUES($1,$2,$3,'123','d','{}',$4,now()+$5::interval)`, closeID, paperID, "fixture-"+paperID, state, verified)
		must(e)
	}
	prepaid := func(source, account, balance string) prepaids.Workpaper {
		return prepaids.Workpaper{Input: prepaids.Input{SourceReference: source, PrepaidAccount: account, ExpenseAccount: "Insurance expense"}, PrepaidBalance: balance}
	}
	accrual := func(source, account, target string, proposal *prepaids.Adjustment) accruals.Workpaper {
		return accruals.Workpaper{Input: accruals.Input{SourceReference: source, LiabilityAccount: account, ExpenseAccount: "Utilities", Amount: target}, Proposal: proposal}
	}
	entry := func(credit, amount string) *prepaids.Adjustment {
		return &prepaids.Adjustment{Date: "2026-09-30", DebitAccount: "Utilities", CreditAccount: credit, Amount: amount}
	}
	// The older version of the Allianz policy is replaced by its latest one.
	save("prepaid", prepaid("Allianz policy", "Prepaid insurance", "700.00"), true)
	save("prepaid", prepaid("Allianz policy", "Prepaid insurance", "600.00"), true)
	save("prepaid", prepaid("Hiscox policy", "Prepaid insurance", "400.00"), true)
	save("prepaid", prepaid("Office lease", "Prepaid rent", "900.00"), true)
	save("prepaid", prepaid("Mislabelled", "Utilities", "50.00"), true)
	munich := prepaid("Munich policy", "Prepaid insurance", "300.00")
	munich.Input.Currency = "EUR"
	save("prepaid", munich, true)
	depreciation := prepaid("Laptops", "Accumulated depreciation", "1800.00")
	depreciation.Input.Schedule, depreciation.Input.Amount, depreciation.Input.SalvageValue = prepaids.Depreciation, "3000.00", "600.00"
	save("prepaid", depreciation, true)
	// A disposed server's accumulated depreciation is removed once its disposal entry is booked.
	server := prepaid("Server", "Server accumulated depreciation", "500.00")
	server.Input.Schedule, server.Input.Amount = prepaids.Depreciation, "1000.00"
	server.Disposal = []prepaids.Adjustment{{Date: "2026-09-30", DebitAccount: "Server accumulated depreciation", CreditAccount: "Office equipment", Amount: "500.00"}, {Date: "2026-09-30", DebitAccount: "Utilities", CreditAccount: "Office equipment", Amount: "500.00"}}
	save("prepaid", server, true)
	deferred := prepaid("Annual plan", "Deferred revenue", "1200.00")
	deferred.Input.Schedule = prepaids.DeferredRevenue
	save("prepaid", deferred, true)
	utilities := save("accrual", accrual("City utilities", "Accrued liabilities", "500.00", entry("Accrued liabilities", "100.00")), true)
	post(save("accrual", accrual("Payroll", "Accrued payroll", "300.00", entry("Accrued payroll", "300.00")), true), "verified", "-1 hour")
	save("accrual", accrual("Audit", "Accrued audit fees", "500.00", entry("Accrued audit fees", "100.00")), true)
	save("accrual", accrual("Bank fees", "Accrued bank fees", "45.00", nil), true)
	save("accrual", accrual("Card fees", "Accrued card fees", "45.00", nil), true)
	// Two opposing entries still to post net to nothing, but they are pending, so materiality does not explain the account.
	save("owner", owners.Workpaper{Input: owners.Input{TransactionReference: "Fee refund"}, Proposal: &prepaids.Adjustment{Date: "2026-09-30", DebitAccount: "Accrued card fees", CreditAccount: "Utilities", Amount: "3.00"}}, true)
	save("owner", owners.Workpaper{Input: owners.Input{TransactionReference: "Fee charge"}, Proposal: &prepaids.Adjustment{Date: "2026-09-30", DebitAccount: "Utilities", CreditAccount: "Accrued card fees", Amount: "3.00"}}, true)
	post(save("accrual", accrual("Landlord", "Accrued rent", "200.00", entry("Accrued rent", "200.00")), true), "unknown", "0")
	save("loan", loans.Workpaper{Input: loans.Input{Lender: "First Bank", LoanAccount: "Term loan", ClosingPrincipal: "1500.00", ReclassCurrentPortion: true, PrincipalDueNext12Months: "2000.00", CurrentPortionAccount: "Loan current portion"}}, true)
	save("reconciliation", reconciliation.Workpaper{Input: reconciliation.Input{Account: "Checking", LedgerClosing: "5000.00"}}, true)
	// The settlement's fee entry posted before the capture; its refund entry is a draft that never posts.
	post(save("settlement", reconciliation.SettlementWorkpaper{Input: reconciliation.SettlementInput{ReportReference: "stripe.csv"}, Proposal: &prepaids.Adjustment{Date: "2026-09-30", DebitAccount: "Stripe fees", CreditAccount: "Stripe clearing", Amount: "29.22"}, RefundProposal: &prepaids.Adjustment{Date: "2026-09-30", DebitAccount: "Utilities", CreditAccount: "Stripe clearing", Amount: "100.00"}}, true), "verified", "-1 hour")
	save("reconciliation", reconciliation.Workpaper{Input: reconciliation.Input{Account: "Business card", AccountType: "credit_card", LedgerClosing: "250.00"}, Blockers: []string{"Statement page 2 is missing."}}, false)

	b, rows := read()
	if b.Snapshot == nil || b.Snapshot.Source != "quickbooks" {
		t.Fatalf("the QuickBooks snapshot is read: %+v", b)
	}
	for name, want := range map[string]string{
		"Prepaid insurance": "ties", "Accumulated depreciation": "ties", "Deferred revenue": "ties", "Checking": "ties", "Business card": "ties",
		"Accrued liabilities": "ties_after_entries", "Accrued payroll": "ties", "Accrued rent": "posting_unknown", "Accrued audit fees": "differs",
		"Term loan": "ties", "Loan current portion": "ties", "Loan payable": "unsupported", "Office equipment": "unsupported",
		"Server accumulated depreciation": "ties_after_entries", "Accrued bank fees": "differs", "Accrued card fees": "differs",
	} {
		if rows[name].Status != want {
			t.Errorf("%s is %q, want %q: %+v", name, rows[name].Status, want, rows[name])
		}
	}
	// A balance that differs from its support blocks sign-off; one that ties does not.
	readTx, err := db.Begin()
	must(err)
	ready, err := closeReadiness(context.Background(), readTx, closeID)
	readTx.Rollback()
	must(err)
	if blocked := strings.Join(ready.Blockers, " | "); !strings.Contains(blocked, "Tie-out: Accrued audit fees is ") || strings.Contains(blocked, "Tie-out: Prepaid insurance") || strings.Contains(blocked, "Tie-out: Accrued rent") {
		t.Errorf("only differing balances block sign-off: %s", blocked)
	}
	if clearing := rows["Stripe clearing"].Entries; len(clearing) != 1 || clearing[0].Posting != "proposed" {
		t.Errorf("a posted fee entry is in the balance, but the settlement's refund draft is still proposed: %+v", rows["Stripe clearing"])
	}
	if _, found := rows["Retained Earnings"]; found || len(b.Rows) != 17 {
		t.Errorf("equity and supportless expense accounts are left off the board: %+v", b.Rows)
	}
	if rows["Prepaid insurance"].Provisional || !rows["Business card"].Provisional {
		t.Errorf("only a match against unapproved or blocked support is provisional: %+v %+v", rows["Prepaid insurance"], rows["Business card"])
	}
	if len(rows["Accrued payroll"].Entries) != 0 || len(rows["Accrued liabilities"].Entries) != 1 || rows["Accrued liabilities"].Entries[0].Posting != "proposed" {
		t.Errorf("an entry verified before the capture is already in the balance: %+v %+v", rows["Accrued payroll"], rows["Accrued liabilities"])
	}
	if rows["Loan payable"].Hint != "Add a loan workpaper from the lender statement." || rows["Loan payable"].Expected != nil {
		t.Errorf("unsupported balances say what support to add: %+v", rows["Loan payable"])
	}
	// The firm's materiality explains a small difference with no entry pending, never one a proposed entry only partly closes.
	_, err = db.Exec(`UPDATE organizations SET materiality_amount=1000 WHERE id='local-development'`)
	must(err)
	_, material := read()
	_, err = db.Exec(`UPDATE organizations SET materiality_amount=NULL WHERE id='local-development'`)
	must(err)
	if material["Accrued bank fees"].Status != "within_materiality" || material["Accrued audit fees"].Status != "differs" || material["Accrued card fees"].Status != "differs" {
		t.Errorf("a 5.00 difference is within a 1000.00 materiality; the audit fees' 450.00 difference, which a proposed entry only partly closes, still differs: %+v %+v", material["Accrued bank fees"], material["Accrued audit fees"])
	}
	unmatched := []string{}
	for _, u := range b.Unmatched {
		unmatched = append(unmatched, u.Account+": "+u.Reason)
	}
	listed := strings.Join(unmatched, " | ")
	if len(unmatched) != 3 || !strings.Contains(listed, "Prepaid rent: No Trial Balance account") || !strings.Contains(listed, "Utilities: This names an account that is not an asset") || !strings.Contains(listed, ": Prepared in EUR") || len(b.Unclassified) != 1 || b.Unclassified[0].Account != "Old clearing" {
		t.Errorf("support naming no balance-sheet account or another currency, and unclassified balances, are listed: %s %+v", listed, b.Unclassified)
	}

	var xeroConnection string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&xeroConnection))
	xeroPayload := `{"date":"2026-09-30","organisation":{"Organisations":[{"BaseCurrency":"USD"}]},"accounts":{"Accounts":[{"AccountID":"a-1","Code":"090","Name":"Checking","Class":"ASSET"},{"AccountID":"a-2","Code":"800","Name":"Business card","Class":"LIABILITY"},{"AccountID":"a-3","Code":"091","Name":"Savings","Class":"ASSET"},{"AccountID":"a-4","Code":"820","Name":"Accrued liabilities","Class":"LIABILITY"}]},
		"trial_balance":{"Reports":[{"Rows":[{"RowType":"Header","Cells":[{"Value":"Account"},{"Value":"Debit"},{"Value":"Credit"},{"Value":"YTD Debit"},{"Value":"YTD Credit"}]},{"RowType":"Row","Cells":[{"Value":"Checking","Attributes":[{"Id":"account","Value":"a-1"}]},{"Value":""},{"Value":""},{"Value":"5000.00"},{"Value":""}]},{"RowType":"Row","Cells":[{"Value":"Business card","Attributes":[{"Id":"account","Value":"a-2"}]},{"Value":""},{"Value":""},{"Value":""},{"Value":"300.00"}]},{"RowType":"Row","Cells":[{"Value":"Savings","Attributes":[{"Id":"account","Value":"a-3"}]},{"Value":""},{"Value":""},{"Value":"100.00"},{"Value":""}]},{"RowType":"Row","Cells":[{"Value":"Accrued liabilities","Attributes":[{"Id":"account","Value":"a-4"}]},{"Value":""},{"Value":""},{"Value":""},{"Value":"500.00"}]}]}]}}`
	// This Trial Balance was requested a minute ago; the utilities journal was verified in Xero before that.
	xeroPayload = strings.Replace(xeroPayload, `"trial_balance":`, `"trial_balance_requested_at":"`+time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)+`","trial_balance":`, 1)
	var xeroGrant string
	must(db.QueryRow(`INSERT INTO xero_posting_grants(organization_id,client_entity,connection_id,tenant_id,account_ids,kinds,max_amount,expires_at,reason,granted_by) SELECT organization_id,entity,$2,'t-1','{a-4,a-5}','{accrual}',1000,now()+interval '1 day','Tie-out fixture','fixture' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, xeroConnection).Scan(&xeroGrant))
	_, err = db.Exec(`INSERT INTO xero_postings(close_id,workpaper_id,source_key,grant_id,connection_id,tenant_id,digest,journal,ledger_before,state,verified_at) VALUES($1,$2,'utilities',$3,$4,'t-1','d','{}','{}','verified',now()-interval '2 minutes')`, closeID, utilities, xeroGrant, xeroConnection)
	must(err)
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d',$3,'fixture',now()+interval '1 minute')`, closeID, xeroConnection, xeroPayload)
	must(err)
	if b, rows = read(); b.Snapshot == nil || b.Snapshot.Source != "xero" || rows["Checking"].Status != "ties" || rows["Business card"].Status != "differs" || rows["Savings"].Status != "unsupported" {
		t.Fatalf("a newer Xero Trial Balance is tied the same way: %+v", b)
	}
	if entries := rows["Accrued liabilities"].Entries; rows["Accrued liabilities"].Status != "ties" || len(entries) != 0 {
		t.Fatalf("a Xero journal verified before the Trial Balance is already in it: %+v", rows["Accrued liabilities"])
	}
	_, err = db.Exec(`UPDATE xero_postings SET verified_at=now() WHERE close_id=$1`, closeID)
	must(err)
	if _, rows = read(); len(rows["Accrued liabilities"].Entries) != 1 || rows["Accrued liabilities"].Entries[0].Posting != "posted_after_capture" {
		t.Fatalf("a Xero journal verified after the Trial Balance is still pending in it: %+v", rows["Accrued liabilities"])
	}
}

func TestTieOutLeavesARevisedDraftOut(t *testing.T) {
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
	entity := "Revised tie-out " + rand.Text()
	var closeID, connection, evidenceID, old string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidenceID))
	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": evidenceID, "accounts": []map[string]string{{"Id": "3", "Name": "Checking", "AccountType": "Bank", "Classification": "Asset"}},
		"reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": ""}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}},
			"Rows": map[string]any{"Row": []map[string]any{{"type": "Data", "ColData": []map[string]string{{"id": "3", "value": "Checking"}, {"value": "900.00"}, {"value": ""}}}}}}}})
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, closeID, connection, rand.Text(), string(payload))
	must(err)
	// A draft first saved against the wrong account and then revised to the right one supports only the right one.
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation',$2,'{"input":{"account":"Old checking","ledger_closing":"900.00"}}') RETURNING id::text`, closeID, rand.Text()).Scan(&old))
	_, err = db.Exec(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation',$2,$3)`, closeID, rand.Text(), `{"input":{"account":"Checking","ledger_closing":"900.00"},"revises":"`+old+`"}`)
	must(err)
	tx, err := db.Begin()
	must(err)
	defer tx.Rollback()
	tie, err := closeTieOut(t.Context(), tx, closeID)
	if err != nil || len(tie.Unmatched) != 0 || len(tie.Rows) != 1 || tie.Rows[0].Status != "ties" {
		t.Fatalf("the revised draft is left out: %+v %v", tie, err)
	}
}
