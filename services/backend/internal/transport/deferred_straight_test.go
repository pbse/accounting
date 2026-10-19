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

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

func TestSimpleDeferredQueuesAndRechecksItsApprovedExample(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		name := "draft"
		if withdraw {
			name = "example withdrawn"
		}
		t.Run(name, func(t *testing.T) { simpleDeferred(t, withdraw, false) })
	}
}

func TestDeferredWaitsForALaterStatementReading(t *testing.T) { simpleDeferred(t, false, true) }

func simpleDeferred(t *testing.T, withdraw, statementLater bool) {
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
	// Keep the generated token inside an issuer identifier, never as a bare currency-shaped word.
	entity := "Revenue Fixture" + rand.Text()
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
	instructions := "Revenue account: Subscription Revenue\nDeferred revenue account: Deferred Revenue\nQuickBooks company: 123\nCustomer ID for Northwind: 7"
	must(db.QueryRow(`INSERT INTO procedure_drafts(organization_id,client_entity,kind,method,instructions,created_by) VALUES('local-development',$1,'prepaid',$2,$3,'fixture') RETURNING id::text`, entity, prepaids.Method, instructions).Scan(&draftID))
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor,draft_id) VALUES('local-development',$1,'prepaid',$2,$3,'Insurance','Prepaid Expenses',true,'Synthetic learned policy','fixture',$4) RETURNING id::text`, entity, prepaids.Method, instructions, draftID).Scan(&procedureID))
	// A reviewed deferred-revenue example is distinct from an approved prepaid example of the same calculator method.
	example := prepaids.Input{Schedule: prepaids.DeferredRevenue, Entity: entity, ClosePeriod: "2026-08", SourceReference: "Synthetic approved customer advance", Currency: "USD", Amount: "1200.00", ServiceStart: "2026-08-01", ServiceEnd: "2027-07-31", ExpenseAccount: "Subscription Revenue", PrepaidAccount: "Deferred Revenue", BookedBalance: "1200.00", BookingReference: "Synthetic books", Basis: "accrual", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	input, _ := json.Marshal(example)
	paper, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: input}, entity, "2026-08")
	must(err)
	digest := preparation.Digest(paper)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, exampleClose, digest, string(paper)).Scan(&exampleID))
	must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic example','fixture') RETURNING id::text`, exampleID, digest).Scan(&reviewID))
	_, err = db.Exec(`INSERT INTO procedure_example_approvals(draft_id,workpaper_id,workpaper_digest,review_id,draft_revision,reason,approved_by) VALUES($1,$2,$3,$4,1,'Synthetic approved deferred-revenue example','fixture')`, draftID, exampleID, digest, reviewID)
	must(err)
	_, err = db.Exec(`UPDATE procedure_drafts SET activated_version_id=$2 WHERE id=$1`, draftID, procedureID)
	must(err)
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(context.Background(), tx, closeID, "prepaid")
	must(err)
	if !procedure.DeferredExample {
		t.Fatal("current activated policy lost its reviewed deferred-revenue example")
	}
	ruleID, err := preparation.AttachProcedure(context.Background(), tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	var applied string
	must(db.QueryRow(`SELECT content FROM evidence WHERE id::text=$1`, ruleID).Scan(&applied))
	if !strings.Contains(applied, "Work kind: prepaid and deferred_revenue") {
		t.Fatal("the applied policy does not explicitly state its approved revenue scope")
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
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+chart+`","accounts":[{"Id":"12","Name":"Subscription Revenue","AccountType":"Income"},{"Id":"13","Name":"Deferred Revenue","AccountType":"Other Current Liability"}],"reports":{"TrialBalance":{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"13","value":"Deferred Revenue"},{"value":""},{"value":"1200.00"}]}]}},"GeneralLedger":{"Header":{"ReportName":"GeneralLedger","Currency":"USD","ReportBasis":"Accrual","StartPeriod":"2026-09-01","EndPeriod":"2026-09-30"},"Columns":{"Column":[{"ColTitle":"Date"},{"ColTitle":"Transaction Type"},{"ColTitle":"Num"},{"ColTitle":"Name"},{"ColTitle":"Amount"},{"ColTitle":"Balance"}]},"Rows":{"Row":[{"type":"Section","Header":{"ColData":[{"id":"13","value":"Deferred Revenue"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"value":""},{"value":"Beginning Balance"},{"value":""},{"value":""},{"value":""},{"value":"0.00"}]},{"type":"Data","ColData":[{"value":"2026-09-03"},{"id":"41","value":"Invoice"},{"value":"REV-22"},{"id":"7","value":"Northwind"},{"value":"1200.00"},{"value":"1200.00"}]}]}}]}}}}`)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Invoice','41','1','2026-09-03',now(),'USD',1200,'{"DocNumber":"REV-22","TotalAmt":1200,"CustomerRef":{"value":"7","name":"Northwind"},"Line":[{"Amount":1200,"SalesItemLineDetail":{"ItemRef":{"value":"1","name":"Annual subscription"}}},{"Amount":1200,"DetailType":"SubTotalLineDetail","SubTotalLineDetail":{}}]}')`, syncID)
	must(err)

	statement := insert("bank-statement.txt", entity+"\nChecking statement September 2026\nClosing balance USD 900.00")
	var otherJob string
	if !statementLater {
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','completed','fixture') RETURNING id::text`, closeID, "statement:"+statement).Scan(&otherJob))
		other, _ := json.Marshal(preparation.Proposal{Kind: "unsupported", Engine: "context_machine", Sources: []preparation.Source{{ID: statement, Role: "statement"}}})
		_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,$3)`, otherJob, statement, string(other))
		must(err)
	}

	invoice := insert("customer-invoice.txt", entity+"\nInvoice REV-22\nBill to: Northwind\nTotal USD 1,200.00\nService period: 2026-09-01 to 2027-08-31")
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var request struct{ Input []struct{ Content string } }
		json.Unmarshal(raw, &request)
		var given struct {
			Primary string `json:"primary_source_id"`
		}
		for _, item := range request.Input {
			json.Unmarshal([]byte(item.Content), &given)
		}
		facts := []map[string]string{}
		for _, f := range []struct{ field, value, quote string }{{"amount", "1200.00", "Total USD 1,200.00"}, {"currency", "USD", "Total USD 1,200.00"}, {"vendor", entity, entity}, {"billed_to", "Northwind", "Bill to: Northwind"}, {"invoice_number", "REV-22", "Invoice REV-22"}, {"service_start", "2026-09-01", "Service period: 2026-09-01 to 2027-08-31"}, {"service_end", "2027-08-31", "Service period: 2026-09-01 to 2027-08-31"}} {
			facts = append(facts, map[string]string{"field": f.field, "value": f.value, "source_id": invoice, "quote": f.quote})
		}
		output, _ := json.Marshal(map[string]any{"kind": "deferred_revenue", "summary": "Customer billed for a full year in advance", "questions": []string{}, "sources": []map[string]string{{"id": invoice, "role": "invoice"}, {"id": ruleID, "role": "support"}}, "facts": facts})
		if given.Primary == statement {
			output, _ = json.Marshal(map[string]any{"kind": "unsupported", "summary": "Bank statement, not a service invoice", "questions": []string{"Provide the matching ledger for a reconciliation."}, "sources": []map[string]string{{"id": statement, "role": "statement"}}, "facts": []any{}})
		}
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	tx, err = db.Begin()
	must(err)
	job, err := preparation.StartIntake(context.Background(), tx, closeID, invoice, "fixture")
	must(err)
	must(tx.Commit())
	if statementLater {
		tx, err = db.Begin()
		must(err)
		otherJob, err = preparation.StartIntake(context.Background(), tx, closeID, statement, "fixture")
		must(err)
		must(tx.Commit())
	}
	worked, err := preparation.ProcessIntake(modelContext(t, context.Background()), db)
	must(err)
	if !worked {
		t.Fatal("intake did not run")
	}
	var state, reason string
	must(db.QueryRow(`SELECT j.state,j.error||COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &reason))
	if statementLater {
		if state != "needs_confirmation" {
			t.Fatalf("unread statement did not hold the advance: %s", state)
		}
		worked, err = preparation.ProcessIntake(modelContext(t, context.Background()), db)
		must(err)
		if !worked {
			t.Fatal("the later statement was not read")
		}
		must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id::text=$1`, job).Scan(&state))
		if state != "waiting_evidence" {
			t.Fatalf("reading independent evidence did not resume advance: %s", state)
		}
		worked, err = preparation.ProcessIntake(modelContext(t, context.Background()), db)
		must(err)
		if !worked {
			t.Fatal("the advance was not re-read")
		}
		must(db.QueryRow(`SELECT j.state,j.error||COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &reason))
	}
	if state != "queued" {
		t.Fatalf("revenue did not queue: %s %s", state, reason)
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
		if state == "completed" || !strings.Contains(reason, "approved deferred-revenue example") {
			t.Fatalf("withdrawn example not held: %s %s", state, reason)
		}
		return
	}
	if state != "completed" {
		t.Fatalf("worker did not prepare: %s %s", state, reason)
	}
	var expense, balance string
	var straight bool
	must(db.QueryRow(`SELECT paper->>'period_expense',paper->>'prepaid_balance',COALESCE((paper->>'straight_through')::boolean,false) FROM workpapers WHERE close_id=$1`, closeID).Scan(&expense, &balance, &straight))
	if expense != "100.00" || balance != "1100.00" || !straight {
		t.Fatalf("wrong deferred draft: %s %s %t", expense, balance, straight)
	}
	worked, err = preparation.ProcessOne(context.Background(), db)
	must(err)
	if worked {
		t.Fatal("completed revenue was prepared twice")
	}
}
