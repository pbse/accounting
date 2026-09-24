package transport

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/loans"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/reconciliation"
)

func TestYearEndHandoffRollsForwardApprovedWork(t *testing.T) {
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
	entity, other := "Year fixture "+rand.Text(), "org_Year"+rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, other)
	must(err)
	closes := map[string]string{}
	open := func(org, name, period string) string {
		var id string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, org, name, period).Scan(&id))
		closes[org+name+period] = id
		return id
	}
	defer func() {
		for _, id := range closes {
			db.Exec(`DELETE FROM production_postings WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
		}
		db.Exec(`DELETE FROM production_posting_grants WHERE client_entity=$1`, entity)
		for _, id := range closes {
			db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organizations WHERE id=$1`, other)
	}()
	local := "local-development"
	jan, feb, mar := open(local, entity, "2026-01"), open(local, entity, "2026-02"), open(local, entity, "2026-03")
	anchor := open(local, entity, "2026-05")
	save := func(closeID, kind string, paper any, approve bool) string {
		t.Helper()
		raw, _ := json.Marshal(paper)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) RETURNING id::text`, closeID, kind, rand.Text(), string(raw)).Scan(&id))
		if approve {
			_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,'approved','Reviewed synthetic year','user_reviewer' FROM workpapers WHERE id=$1`, id)
			must(e)
		}
		return id
	}
	prepaid := func(source, expense, balance string, rows ...prepaids.Row) prepaids.Workpaper {
		return prepaids.Workpaper{Input: prepaids.Input{SourceReference: source, PrepaidAccount: "Prepaid " + source, ExpenseAccount: source + " expense", Currency: "USD"}, Schedule: rows, PeriodExpense: expense, PrepaidBalance: balance,
			Proposal: &prepaids.Adjustment{Date: "2026-01-31", DebitAccount: source + " expense", CreditAccount: "Prepaid " + source, Amount: expense}}
	}
	// Insurance takes a February credit, so its balance does not fall by the expense alone.
	save(jan, "prepaid", prepaid("Insurance", "100.00", "1100.00", prepaids.Row{Period: "2026-01", Expense: "100.00", Remaining: "1100.00"}), true)
	save(feb, "prepaid", prepaid("Insurance", "81.82", "818.18", prepaids.Row{Period: "2026-01", Expense: "100.00", Remaining: "1100.00"}, prepaids.Row{Period: "2026-02", Credit: "200.00", Expense: "81.82", Remaining: "818.18"}), true)
	save(mar, "prepaid", prepaid("Insurance", "81.82", "736.36", prepaids.Row{Period: "2026-02", Credit: "200.00", Expense: "81.82", Remaining: "818.18"}, prepaids.Row{Period: "2026-03", Expense: "81.82", Remaining: "736.36"}), true)
	save(jan, "prepaid", prepaid("Maintenance", "50.00", "500.00", prepaids.Row{Period: "2026-01", Expense: "50.00", Remaining: "500.00"}), true)
	save(feb, "prepaid", prepaid("Maintenance", "50.00", "400.00", prepaids.Row{Period: "2026-01", Expense: "50.00", Remaining: "450.00"}, prepaids.Row{Period: "2026-02", Expense: "50.00", Remaining: "400.00"}), true)
	olderSoftware := save(feb, "prepaid", prepaid("Software", "30.00", "330.00", prepaids.Row{Period: "2026-02", Expense: "30.00", Remaining: "330.00"}), true)
	save(feb, "prepaid", prepaid("Software", "35.00", "325.00", prepaids.Row{Period: "2026-02", Expense: "35.00", Remaining: "325.00"}), false)
	save(mar, "prepaid", prepaid("Hosting", "20.00", "280.00", prepaids.Row{Period: "2026-02", Expense: "20.00", Remaining: "300.00"}, prepaids.Row{Period: "2026-03", Expense: "20.00", Remaining: "280.00"}), true)
	save(jan, "prepaid", prepaid("Warranty", "10.00", "110.00", prepaids.Row{Period: "2026-01", Expense: "10.00", Remaining: "110.00"}), true)
	save(feb, "prepaid", prepaid("Warranty", "10.00", "100.00", prepaids.Row{Period: "2026-02", Expense: "10.00", Remaining: "100.00"}), true)
	loan := func(lender, opening, closing string) loans.Workpaper {
		return loans.Workpaper{Input: loans.Input{Lender: lender, LoanAccount: "Loan payable", OpeningPrincipal: opening, ClosingPrincipal: closing, Currency: "USD"}, Schedule: []loans.Row{{Principal: "1500.00", Interest: "620.00"}, {Principal: "500.00", Interest: "276.50"}}}
	}
	save(jan, "loan", loan("First Bank", "120000.00", "118000.00"), true)
	save(feb, "loan", loan("First Bank", "117500.00", "115500.00"), true)
	save(mar, "loan", loan("Second Bank", "5000.00", "3000.00"), true)
	save(mar, "loan", loan("New Bank", "0.00", "8000.00"), true)
	for _, kind := range []string{"bank", "credit_card"} {
		save(mar, "reconciliation", reconciliation.Workpaper{Status: "reconciled", Input: reconciliation.Input{Account: "Operating", AccountType: kind, StatementClosing: "900.00", LedgerClosing: "900.00"}, ClosingDifference: "0.00"}, true)
	}
	save(mar, "settlement", reconciliation.SettlementWorkpaper{Status: "draft", Input: reconciliation.SettlementInput{ReportReference: "stripe-mar.csv"}, Proposal: &prepaids.Adjustment{Date: "2026-03-31", DebitAccount: "Stripe fees", CreditAccount: "Sales", Amount: "29.22"}}, true)
	save(open(local, entity, "2025-12"), "prepaid", prepaid("Last year", "10.00", "0.00"), true)
	save(open(local, "Another client "+rand.Text(), "2026-02"), "prepaid", prepaid("Another client", "10.00", "0.00"), true)
	save(open(other, entity, "2026-02"), "prepaid", prepaid("Other firm", "10.00", "0.00"), true)

	var connection, grant string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,environment) VALUES($1,$2,$3,'fixture','active',$4,now(),'123','production') RETURNING id::text`, feb, local, entity, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,account_ids,kinds,max_amount,expires_at,reason,granted_by) VALUES($1,$2,$3,'123',ARRAY['20','21'],ARRAY['prepaid'],100,now()+interval '1 day','Synthetic','fixture') RETURNING id::text`, local, entity, connection).Scan(&grant))
	_, err = db.Exec(`INSERT INTO production_postings(close_id,workpaper_id,source_key,grant_id,connection_id,realm,digest,journal,ledger_before,state,external_id,verified_at) VALUES($1,$2,'software',$3,$4,'123','d','{"Line":[{"Amount":30,"JournalEntryLineDetail":{"PostingType":"Debit","AccountRef":{"value":"21"}}},{"Amount":30,"JournalEntryLineDetail":{"PostingType":"Credit","AccountRef":{"value":"20"}}}]}','{}','verified','901',now())`, feb, olderSoftware, grant, connection)
	must(err)

	token := strings.Repeat("y", 32)
	r := httptest.NewRequest("GET", "/v1/closes/"+anchor+"/year-end", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	apiWithStore(db, token).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("year-end: %d %s", w.Code, w.Body.String())
	}
	archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	must(err)
	files := map[string]string{}
	for _, f := range archive.File {
		rc, e := f.Open()
		must(e)
		body, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(body)
	}
	for _, line := range strings.Split(strings.TrimSpace(files["checksums.sha256"]), "\n") {
		sum, name, _ := strings.Cut(line, "  ")
		if fmt.Sprintf("%x", sha256.Sum256([]byte(files[name]))) != sum {
			t.Fatalf("checksum mismatch for %s", name)
		}
	}
	table := func(name string) [][]string {
		rows, e := csv.NewReader(strings.NewReader(files[name])).ReadAll()
		must(e)
		return rows[1:]
	}
	column := func(name string, index int) string {
		values := []string{}
		for _, row := range table(name) {
			values = append(values, row[0]+" "+row[index])
		}
		return strings.Join(values, ", ")
	}
	if got := column("months.csv", 2); !strings.Contains(got, "2026-04 no close") || len(table("months.csv")) != 5 {
		t.Fatalf("January through May, April has no close: %s", got)
	}
	if got := column("prepaids.csv", 1); got != "2026-01 Insurance, 2026-01 Maintenance, 2026-01 Warranty, 2026-02 Insurance, 2026-02 Maintenance, 2026-02 Warranty, 2026-03 Insurance, 2026-03 Hosting" {
		t.Fatalf("only this client's approved latest prepaids for the year: %s", got)
	}
	if got := column("prepaids.csv", 9); !strings.HasPrefix(got, "2026-01 prepaid, ") {
		t.Fatalf("each roll-forward row names its schedule: %s", got)
	}
	if got := column("loans.csv", 5); got != "2026-01 0.00, 2026-02 0.00, 2026-03 0.00, 2026-03 0.00" {
		t.Fatalf("fixed-rate loans report zero fees added: %s", got)
	}
	if got := column("reconciliations.csv", 2); got != "2026-03 bank, 2026-03 credit_card" {
		t.Fatalf("a bank and a card reconciliation with one name are separate: %s", got)
	}
	if got := table("postings.csv"); len(got) != 1 || got[0][2] != olderSoftware || got[0][3] != "verified" || got[0][6] != "Debit account 21 30; Credit account 20 30" {
		t.Fatalf("the verified posting on an older version stays in the history: %v", got)
	}
	if !strings.Contains(files["adjustments.csv"], "2026-03,settlement,") || !strings.Contains(files["adjustments.csv"], "Stripe fees,Sales,29.22") {
		t.Fatalf("an approved Stripe fee entry is part of the adjustment history:\n%s", files["adjustments.csv"])
	}
	questions := files["open-questions.csv"]
	for _, want := range []string{"Roll-forward break: Maintenance closed 2026-01 at 500.00, but this schedule shows 450.00", "Roll-forward break: First Bank closed 2026-01 at 118000.00 principal, but this month opens at 117500.00", "latest prepaid version for software is pending, not approved", "Hosting has a 2026-02 balance in this schedule but no 2026-02 workpaper", "No close exists for this month", "Warranty has an approved 2026-01 workpaper, but this schedule has no 2026-01 row", "Second Bank opens 2026-03 with 5000.00 principal, but 2026-02 has no loan workpaper"} {
		if !strings.Contains(questions, want) {
			t.Fatalf("missing open question %q in:\n%s", want, questions)
		}
	}
	if strings.Contains(questions, "Roll-forward break: Insurance") || strings.Contains(questions, "New Bank") {
		t.Fatalf("a credit in the saved schedule is not a break, and a loan opening at zero is new:\n%s", questions)
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET home_currency='USD',fiscal_year_start=7 WHERE id=$1`, connection)
	must(err)
	w = httptest.NewRecorder()
	apiWithStore(db, token).ServeHTTP(w, r)
	archive, err = zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	must(err)
	for _, f := range archive.File {
		if f.Name == "months.csv" {
			rc, e := f.Open()
			must(e)
			body, _ := io.ReadAll(rc)
			rc.Close()
			files[f.Name] = string(body)
		}
	}
	if got := table("months.csv"); len(got) != 11 || got[0][0] != "2025-07" || !strings.Contains(w.Header().Get("Content-Disposition"), "-FY2026.zip") {
		t.Fatalf("a July fiscal year runs from the prior July through the anchor month: %v %s", got, w.Header().Get("Content-Disposition"))
	}
}
