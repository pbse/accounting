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

	"close.local/backend/internal/loans"
	"close.local/backend/internal/preparation"
)

func TestSimpleLoanQueuesAndRechecksItsApprovedExample(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		name := "draft"
		if withdraw {
			name = "example withdrawn"
		}
		t.Run(name, func(t *testing.T) { simpleLoan(t, withdraw) })
	}
}

func simpleLoan(t *testing.T, withdraw bool) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	entity := "Borrower " + rand.Text()
	var closeID, exampleClose, connection, procedureID, draftID, exampleID, reviewID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-08') RETURNING id::text`, entity).Scan(&exampleClose))
	defer func() {
		db.Exec(`DELETE FROM procedure_example_approvals WHERE draft_id::text=$1`, draftID)
		db.Exec(`UPDATE procedure_drafts SET activated_version_id=NULL WHERE id::text=$1`, draftID)
		db.Exec(`UPDATE procedure_versions SET draft_id=NULL WHERE id::text=$1`, procedureID)
		db.Exec(`DELETE FROM procedure_drafts WHERE id::text=$1`, draftID)
		for _, id := range []string{closeID, exampleClose} {
			for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
				db.Exec(q, id)
			}
		}
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
	}()
	instructions := "QuickBooks company: 123\nLender ID for First Bank: 9\nLoan L777 account: Term Loan\nLoan L777 interest account: Interest Expense\nLoan L777 payment account: Checking\nLoan L777 day count: actual_360\nLoan L777 annual rate: 12\nLoan L777 agreement reference: Note L777\nLoan L777 accrue interest: false\nLoan L777 reclass current portion: false"
	must(db.QueryRow(`INSERT INTO procedure_drafts(organization_id,client_entity,kind,method,instructions,created_by) VALUES('local-development',$1,'loan',$2,$3,'fixture') RETURNING id::text`, entity, loans.Method, instructions).Scan(&draftID))
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,enabled,reason,actor,draft_id) VALUES('local-development',$1,'loan',$2,$3,true,'Synthetic loan policy','fixture',$4) RETURNING id::text`, entity, loans.Method, instructions, draftID).Scan(&procedureID))
	example := loans.Input{Entity: entity, ClosePeriod: "2026-08", Lender: "First Bank", SourceReference: "Synthetic approved lender statement", AgreementReference: "Note L777", Currency: "USD", AnnualRate: "12", DayCount: "actual_360", OpeningPrincipal: "10000.00", InterestPaidThrough: "2026-07-31", Payments: []loans.Payment{{Date: "2026-08-31", Principal: "0.00", Interest: "103.33"}}, ClosingPrincipal: "10000.00", BookBalance: "10000.00", LoanAccount: "Term Loan", InterestAccount: "Interest Expense", Method: loans.Method, MethodConfirmed: true, RateConfirmed: true, BookingConfirmed: true}
	input, _ := json.Marshal(example)
	paper, err := preparation.Calculate(preparation.Plan{Kind: "loan", Input: input}, entity, "2026-08")
	must(err)
	digest := preparation.Digest(paper)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'loan',$2,$3) RETURNING id::text`, exampleClose, digest, string(paper)).Scan(&exampleID))
	must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic example','fixture') RETURNING id::text`, exampleID, digest).Scan(&reviewID))
	_, err = db.Exec(`INSERT INTO procedure_example_approvals(draft_id,workpaper_id,workpaper_digest,review_id,draft_revision,reason,approved_by) VALUES($1,$2,$3,$4,1,'Synthetic approved loan example','fixture')`, draftID, exampleID, digest, reviewID)
	must(err)
	_, err = db.Exec(`UPDATE procedure_drafts SET activated_version_id=$2 WHERE id=$1`, draftID, procedureID)
	must(err)
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(context.Background(), tx, closeID, "loan")
	must(err)
	if len(procedure.LoanExamples) != 1 {
		t.Fatal("current activated policy lost its reviewed loan example")
	}
	ruleID, err := preparation.AttachProcedure(context.Background(), tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	var applied string
	must(db.QueryRow(`SELECT content FROM evidence WHERE id::text=$1`, ruleID).Scan(&applied))
	if !strings.Contains(applied, "current approved loan example") {
		t.Fatal("the applied policy does not explicitly state its approved loan scope")
	}
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	insert := func(name, body string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture','ready') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id))
		return id
	}
	capture := insert("QuickBooks transactions", "capture")
	var syncID string
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{"realm":"123"}') RETURNING id::text`, closeID, connection, capture).Scan(&syncID))
	chart := insert("QuickBooks accounts and Trial Balance", "period snapshot")
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+chart+`","accounts":[{"Id":"20","Name":"Term Loan","AccountType":"Long Term Liability"},{"Id":"30","Name":"Checking","AccountType":"Bank","CurrencyRef":{"value":"USD"}},{"Id":"40","Name":"Interest Expense","AccountType":"Expense"}],"reports":{"TrialBalance":{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"20","value":"Term Loan"},{"value":""},{"value":"9900.00"}]}]}},"GeneralLedger":{"Header":{"ReportName":"GeneralLedger","Currency":"USD","ReportBasis":"Accrual","StartPeriod":"2026-09-01","EndPeriod":"2026-09-30"},"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Num"},{"ColTitle":"Name"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[{"type":"Section","Header":{"ColData":[{"id":"20","value":"Term Loan"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":""},{"value":"Beginning Balance"},{"value":""},{"value":""},{"value":""},{"value":"10000.00"}]},{"type":"Data","ColData":[{"value":"2026-09-30"},{"id":"41","value":"Expense"},{"value":""},{"id":"9","value":"First Bank"},{"value":"-100.00"},{"value":"9900.00"}]}]}}]}}}}`)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Purchase','41','1','2026-09-30',now(),'USD',200,$2)`, syncID, `{"TotalAmt":200,"PaymentType":"Cash","AccountRef":{"value":"30","name":"Checking"},"EntityRef":{"value":"9","name":"First Bank","type":"Vendor"},"Line":[{"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"20","name":"Term Loan"}}},{"Amount":100,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"40","name":"Interest Expense"}}}]}`)
	must(err)
	// A model-read independent statement in the same close must not require another prompt.
	other := insert("bank-statement.txt", entity+"\nChecking statement September 2026\nClosing balance USD 900.00")
	var otherJob string
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','completed','fixture') RETURNING id::text`, closeID, "statement:"+other).Scan(&otherJob))
	reading, _ := json.Marshal(preparation.Proposal{Kind: "unsupported", Engine: "context_machine", Sources: []preparation.Source{{ID: other, Role: "statement"}}})
	_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,$3)`, otherJob, other, string(reading))
	must(err)
	statement := insert("loan-statement.txt", strings.ReplaceAll("First Bank\nBorrower: Example\nLoan reference: L777\nCurrency: USD\nStatement period: 2026-09-01 to 2026-09-30\nAnnual rate: 12%\nOpening principal: USD 10000.00\nOpening interest paid through: 2026-08-31\nNumber of payments: 1\nPayment received date: 2026-09-30\nTotal payments received: USD 200.00\nPrincipal paid: USD 100.00\nInterest paid: USD 100.00\nClosing principal: USD 9900.00", "Example", entity))
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		facts := []map[string]string{}
		for _, f := range []struct{ field, value, quote, source string }{

			{"currency", "USD", "Currency: USD", statement}, {"lender", "First Bank", "First Bank", statement}, {"borrower", entity, "Borrower: " + entity, statement}, {"loan_reference", "L777", "Loan reference: L777", statement},
			{"opening_principal", "10000.00", "Opening principal: USD 10000.00", statement}, {"closing_principal", "9900.00", "Closing principal: USD 9900.00", statement}, {"annual_rate", "12", "Annual rate: 12%", statement}, {"interest_paid_through", "2026-08-31", "Opening interest paid through: 2026-08-31", statement},
			{"payment_count", "1", "Number of payments: 1", statement}, {"payment_date", "2026-09-30", "Payment received date: 2026-09-30", statement}, {"payment_total", "200.00", "Total payments received: USD 200.00", statement}, {"principal_paid", "100.00", "Principal paid: USD 100.00", statement}, {"interest_paid", "100.00", "Interest paid: USD 100.00", statement},
			{"agreement_reference", "Note L777", "Loan L777 agreement reference: Note L777", ruleID}, {"loan_account", "Term Loan", "Loan L777 account: Term Loan", ruleID}, {"interest_account", "Interest Expense", "Loan L777 interest account: Interest Expense", ruleID},
		} {
			facts = append(facts, map[string]string{"field": f.field, "value": f.value, "source_id": f.source, "quote": f.quote})
		}
		output, _ := json.Marshal(map[string]any{"kind": "loan", "summary": "One paid monthly lender instalment", "questions": []string{}, "sources": []map[string]string{{"id": statement, "role": "statement"}, {"id": ruleID, "role": "support"}, {"id": chart, "role": "ledger"}, {"id": other, "role": "support"}}, "facts": facts})
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	tx, err = db.Begin()
	must(err)
	job, err := preparation.StartIntake(context.Background(), tx, closeID, statement, "fixture")
	must(err)
	must(tx.Commit())
	worked, err := preparation.ProcessIntake(modelContext(t, context.Background()), db)
	must(err)
	if !worked {
		t.Fatal("intake did not run")
	}
	var state, reason string
	must(db.QueryRow(`SELECT j.state,j.error||COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &reason))
	if state != "queued" {
		t.Fatalf("loan did not queue: %s %s", state, reason)
	}
	if withdraw {
		_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'changes_requested','Withdraw synthetic example','fixture')`, exampleID, digest)
		must(err)
	}
	worked, err = preparation.ProcessOne(context.Background(), db)
	must(err)
	if !worked {
		t.Fatal("worker did not run")
	}
	must(db.QueryRow(`SELECT state,error FROM preparation_jobs WHERE id::text=$1`, job).Scan(&state, &reason))
	if withdraw {
		if state == "completed" || !strings.Contains(reason, "approved fixed-loan example") {
			t.Fatalf("withdrawn example not held: %s %s", state, reason)
		}
		return
	}
	if state != "completed" {
		t.Fatalf("worker did not prepare: %s %s", state, reason)
	}
	var raw []byte
	must(db.QueryRow(`SELECT paper FROM workpapers WHERE close_id=$1`, closeID).Scan(&raw))
	var saved loans.Workpaper
	must(json.Unmarshal(raw, &saved))
	var meta struct {
		Straight bool `json:"straight_through"`
	}
	must(json.Unmarshal(raw, &meta))
	if len(saved.Schedule) != 1 || saved.Input.ClosingPrincipal != "9900.00" || saved.Schedule[0].Principal != "100.00" || saved.Schedule[0].Interest != "100.00" || saved.BookDifference != "0.00" || len(saved.Proposals) > 0 || !meta.Straight {
		t.Fatalf("wrong loan draft: %s", raw)
	}
	worked, err = preparation.ProcessOne(context.Background(), db)
	must(err)
	if worked {
		t.Fatal("completed loan was prepared twice")
	}
}
