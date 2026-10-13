package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

// A month's payroll summary is recorded without a person confirming the agent's reading when every figure is read once
// from the summary or the ledger page, the summary ties, the capture shows the provider's debits and no booking, and an
// approved payroll from the same provider gives the accounts; a provider with no approved payroll yet waits for a person.
func TestAPayrollSummaryIsRecordedStraightThroughWithLastMonthsAccounts(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
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
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	entity := "Payroll " + strings.ToLower(rand.Text())
	var august, closeID, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-08') RETURNING id::text`, entity).Scan(&august))
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, id := range []string{closeID, august} {
			for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, `DELETE FROM workpapers WHERE close_id=$1`,
				`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
				db.Exec(q, id)
			}
		}
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
	}()
	must2 := func(_ any, e error) { t.Helper(); must(e) }
	must2(db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,enabled,reason,actor) VALUES('local-development',$1,'payroll','payroll_summary_v1','Record the provider summary',true,'Fixture','fixture')`, entity))
	// August's payroll from Gusto, approved: its accounts are September's.
	accounts := `"wage_account":"Wages","tax_expense_account":"Payroll Tax Expense","clearing_account":"Payroll Clearing","liability_account":"Payroll Liabilities"`
	var prior string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'payroll',$2,$3) RETURNING id::text`, august, "aug-"+entity, `{"status":"draft","input":{"provider":"Gusto",`+accounts+`}}`).Scan(&prior))
	must2(db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','','fixture')`, prior, "aug-"+entity))

	// September's capture: the clearing account shows Gusto's debit and no journal books the payroll.
	insert := func(name, body string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture','ready') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id))
		return id
	}
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'pay','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	captured := insert("QuickBooks transactions 2026-09", "capture")
	var syncID string
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{}') RETURNING id::text`, closeID, connection, captured).Scan(&syncID))
	must2(db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Purchase','91','1','2026-09-15',now(),'USD','10265.00',$2)`, syncID, `{"Line":[{"Amount":10265.00,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40","name":"Payroll Clearing"}}}]}`))
	snapshot := insert("QuickBooks 2026-09 accrual period evidence.txt", "period snapshot")
	must2(db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'pay',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(),
		`{"period":"2026-09","source_id":"`+snapshot+`","accounts":[{"Id":"40","Name":"Payroll Clearing"},{"Id":"41","Name":"Payroll Liabilities"},{"Id":"60","Name":"Wages"},{"Id":"61","Name":"Payroll Tax Expense"}],"reports":{}}`))

	summaryText := func(provider string) string {
		return provider + " payroll summary, September 2026\nPay dates 2026-09-15 to 2026-09-30\nGross wages 10,000.00\nEmployee taxes 1,500.00\nEmployee deductions 500.00\nNet pay 8,000.00\nEmployer taxes 765.00\nEmployer contributions 0.00\nDebited from your bank 10,265.00"
	}
	ledgerText := func(provider string) string {
		return "Payroll Clearing ledger, September 2026\n" + provider + " debits 10,265.00"
	}
	read := func(provider string) (string, string, string) {
		t.Helper()
		summary := insert(provider+" payroll September.txt", summaryText(provider))
		ledger := insert(provider+" clearing ledger September.txt", ledgerText(provider))
		tx, e := db.Begin()
		must(e)
		job, e := preparation.StartIntake(context.Background(), tx, closeID, summary, "fixture")
		must(e)
		must(tx.Commit())
		lines := strings.Split(summaryText(provider), "\n")
		quote := func(field, value, source, line string) map[string]string {
			return map[string]string{"field": field, "value": value, "source_id": source, "quote": line}
		}
		facts := []map[string]string{
			quote("provider", provider, summary, lines[0]), quote("source_reference", provider+" payroll summary, September 2026", summary, lines[0]),
			quote("first_pay_date", "2026-09-15", summary, lines[1]), quote("last_pay_date", "2026-09-30", summary, lines[1]),
			quote("gross_wages", "10000.00", summary, lines[2]), quote("employee_taxes", "1500.00", summary, lines[3]), quote("employee_deductions", "500.00", summary, lines[4]),
			quote("net_pay", "8000.00", summary, lines[5]), quote("employer_taxes", "765.00", summary, lines[6]), quote("employer_contributions", "0.00", summary, lines[7]),
			quote("provider_debits", "10265.00", summary, lines[8]),
			quote("ledger_debits", "10265.00", ledger, provider+" debits 10,265.00"), quote("ledger_reference", "Payroll Clearing ledger, September 2026", ledger, "Payroll Clearing ledger, September 2026"),
		}
		http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
			output, _ := json.Marshal(map[string]any{"kind": "payroll", "summary": provider + " payroll for September", "questions": []string{}, "sources": []any{map[string]string{"id": summary, "role": "support"}, map[string]string{"id": ledger, "role": "ledger"}}, "facts": facts})
			body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
		for {
			worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db)
			must(e)
			if !worked {
				break
			}
		}
		var state, checkpoint string
		must(db.QueryRow(`SELECT j.state,COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &checkpoint))
		return job, state, checkpoint
	}
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()

	// Gusto's September summary, with August's approved accounts, is queued by the agent and drafted with every check passed.
	job, state, checkpoint := read("Gusto")
	if state != "queued" || !strings.HasPrefix(checkpoint, "Prepared without confirmation") || !strings.Contains(checkpoint, "#"+prior) {
		t.Fatalf("Gusto's payroll is prepared without confirmation from August's accounts: %s %s", state, checkpoint)
	}
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal("the worker drafts the queued payroll", e)
	}
	var done, wages, checks, blockers, confirmed string
	must(db.QueryRow(`SELECT j.state,w.paper->'input'->>'wage_account',w.paper->'checks',w.paper->'blockers',i.confirmed_by FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id JOIN workpapers w ON j.workpaper_ids @> jsonb_build_array(w.id::text) WHERE j.id::text=$1`, job).Scan(&done, &wages, &checks, &blockers, &confirmed))
	if done != "completed" || wages != "Wages" || strings.Contains(checks, `"unavailable"`) || strings.Contains(checks, `"failed"`) || blockers != "[]" || confirmed != "agent" {
		t.Fatalf("the payroll draft uses August's accounts and passes every check: %s %s %s %s %s", done, wages, checks, blockers, confirmed)
	}
	// August's approval withdrawn, September's draft, whose accounts rest on it, needs review again.
	must2(db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'changes_requested','Wrong wage account','fixture')`, prior, "aug-"+entity))
	var review string
	must(db.QueryRow(`SELECT r.review_state FROM workpaper_review_state r JOIN preparation_jobs j ON j.workpaper_ids @> jsonb_build_array(r.id::text) WHERE j.id::text=$1`, job).Scan(&review))
	if review != "source_changed" {
		t.Fatal("a withdrawn precedent reopens the payroll draft's review:", review)
	}
	// A provider with no approved payroll yet: the accounts are a person's to choose once.
	if _, state, checkpoint := read("Paychex"); state != "needs_confirmation" || !strings.Contains(checkpoint, "no earlier approved payroll from Paychex") {
		t.Fatalf("a first payroll from a provider waits for a person: %s %s", state, checkpoint)
	}
}
