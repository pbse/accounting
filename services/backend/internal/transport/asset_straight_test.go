package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

func TestSimpleAssetQueuesAndRechecksItsApprovedExample(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		name := "draft"
		if withdraw {
			name = "example withdrawn"
		}
		t.Run(name, func(t *testing.T) { simpleAsset(t, withdraw, false, false) })
	}
}

func TestAssetInServiceAnswerResumesPreparation(t *testing.T) { simpleAsset(t, false, true, false) }

func TestAssetClientAnswerResumesPreparation(t *testing.T) { simpleAsset(t, false, true, true) }

func simpleAsset(t *testing.T, withdraw, answered, client bool) {
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
	entity := "Asset " + rand.Text()
	var closeID, exampleClose, connection, procedureID, draftID, exampleID, reviewID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-08') RETURNING id::text`, entity).Scan(&exampleClose))
	defer func() {
		for _, q := range []string{`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`, `DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, `DELETE FROM request_drafts WHERE close_id=$1`} {
			db.Exec(q, closeID)
		}
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
	instructions := "Asset cost account: Equipment\nDepreciation expense account: Depreciation\nAccumulated depreciation account: Accumulated Depreciation\nUseful life months: 12\nSalvage value: 0.00\nDepreciation convention: full_first_month\nQuickBooks company: 123\nVendor ID for Acme Equipment: 8"
	must(db.QueryRow(`INSERT INTO procedure_drafts(organization_id,client_entity,kind,method,instructions,created_by) VALUES('local-development',$1,'prepaid',$2,$3,'fixture') RETURNING id::text`, entity, prepaids.Method, instructions).Scan(&draftID))
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor,draft_id) VALUES('local-development',$1,'prepaid',$2,$3,'Insurance','Prepaid Expenses',true,'Synthetic learned policy','fixture',$4) RETURNING id::text`, entity, prepaids.Method, instructions, draftID).Scan(&procedureID))
	// A reviewed depreciation example is distinct from an approved prepaid example of the same calculator method.
	example := prepaids.Input{Schedule: prepaids.Depreciation, Entity: entity, ClosePeriod: "2026-08", SourceReference: "Synthetic approved asset", Currency: "USD", Amount: "1200.00", ServiceStart: "2026-08-01", ServiceEnd: "2027-07-31", SalvageValue: "0.00", ExpenseAccount: "Depreciation", PrepaidAccount: "Accumulated Depreciation", BookedBalance: "0.00", BookingReference: "Synthetic books", Basis: "accrual", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
	input, _ := json.Marshal(example)
	paper, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: input}, entity, "2026-08")
	must(err)
	digest := preparation.Digest(paper)
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, exampleClose, digest, string(paper)).Scan(&exampleID))
	must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic example','fixture') RETURNING id::text`, exampleID, digest).Scan(&reviewID))
	_, err = db.Exec(`INSERT INTO procedure_example_approvals(draft_id,workpaper_id,workpaper_digest,review_id,draft_revision,reason,approved_by) VALUES($1,$2,$3,$4,1,'Synthetic approved asset example','fixture')`, draftID, exampleID, digest, reviewID)
	must(err)
	_, err = db.Exec(`UPDATE procedure_drafts SET activated_version_id=$2 WHERE id=$1`, draftID, procedureID)
	must(err)
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(context.Background(), tx, closeID, "prepaid")
	must(err)
	if !procedure.AssetExample {
		t.Fatal("current activated policy lost its reviewed asset example")
	}
	ruleID, err := preparation.AttachProcedure(context.Background(), tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	var applied string
	must(db.QueryRow(`SELECT content FROM evidence WHERE id::text=$1`, ruleID).Scan(&applied))
	if !strings.Contains(applied, "Work kind: prepaid and depreciation") {
		t.Fatal("the applied policy does not explicitly state its approved asset scope")
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
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+chart+`","accounts":[{"Id":"12","Name":"Equipment","AccountType":"Fixed Asset"},{"Id":"13","Name":"Accumulated Depreciation","AccountType":"Fixed Asset"},{"Id":"14","Name":"Depreciation","AccountType":"Expense"}],"reports":{"TrialBalance":{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"13","value":"Accumulated Depreciation"},{"value":"0.00"},{"value":"0.00"}]}]}}}}`)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill','41','1','2026-09-03',now(),'USD',1200,'{"DocNumber":"EQ-778","TotalAmt":1200,"VendorRef":{"value":"8","name":"Acme Equipment"},"Line":[{"Amount":1200,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"12","name":"Equipment"}}}]}')`, syncID)
	must(err)
	invoiceText := "Acme Equipment\nInvoice EQ-778\nBill to: " + entity + "\nTotal USD 1,200.00"
	if !answered {
		invoiceText += "\nPlaced in service: 2026-09-14"
	}
	name := "asset.txt"
	if client {
		name = "Delivery van accounting asset.txt"
	}
	invoice := insert(name, invoiceText)
	answerID := ""
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		facts := []map[string]string{}
		for _, f := range []struct{ field, value, quote string }{{"amount", "1200.00", "Total USD 1,200.00"}, {"currency", "USD", "Total USD 1,200.00"}, {"service_start", "2026-09-14", "Placed in service: 2026-09-14"}, {"vendor", "Acme Equipment", "Acme Equipment"}, {"invoice_number", "EQ-778", "Invoice EQ-778"}, {"billed_to", entity, "Bill to: " + entity}} {
			id, quote := invoice, f.quote
			if answered && f.field == "service_start" {
				if answerID == "" {
					continue
				}
				id, quote = answerID, "2026-09-14"
			}
			facts = append(facts, map[string]string{"field": f.field, "value": f.value, "source_id": id, "quote": quote})
		}
		questions := []string{}
		sources := []map[string]string{{"id": invoice, "role": "invoice"}, {"id": ruleID, "role": "support"}}
		if answered && answerID == "" {
			questions = append(questions, "When was this asset placed in service?")
		}
		if answerID != "" {
			sources = append(sources, map[string]string{"id": answerID, "role": "client_reply"})
		}
		output, _ := json.Marshal(map[string]any{"kind": "depreciation", "summary": "New equipment purchase", "questions": questions, "sources": sources, "facts": facts})
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	tx, err = db.Begin()
	must(err)
	job, err := preparation.StartIntake(context.Background(), tx, closeID, invoice, "fixture")
	must(err)
	must(tx.Commit())
	worked, err := preparation.ProcessIntake(modelContext(t, context.Background()), db)
	must(err)
	if !worked {
		t.Fatal("intake did not run")
	}
	var state, checkpoint string
	if answered {
		var revision int
		must(db.QueryRow(`SELECT j.state,i.revision FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &revision))
		if state != "needs_confirmation" {
			t.Fatalf("missing date did not ask a question: %s", state)
		}
		if client {
			token := strings.Repeat("a", 32)
			handler := apiWithStore(db, token)
			call := func(path, bearer string, body any) []byte {
				t.Helper()
				raw, _ := json.Marshal(body)
				req := httptest.NewRequest("POST", path, bytes.NewReader(raw))
				req.Header.Set("Authorization", "Bearer "+bearer)
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
				}
				return w.Body.Bytes()
			}
			var proposed struct{ Proposal struct{ ID string } }
			must(json.Unmarshal(call("/v1/closes/"+closeID+"/requests/proposed", token, map[string]any{"questions": []string{"When is the next board meeting?"}}), &proposed))
			var opened struct {
				ID        string
				LinkToken string `json:"link_token"`
			}
			must(json.Unmarshal(call("/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", token, map[string]any{"recipient": "owner@example.test", "confirmed": true}), &opened))
			answers, _ := json.Marshal(map[string]any{"answers": []clientAnswer{{1, "answered", "2026-09-14"}, {2, "answered", "2026-09-25"}}})
			call("/v1/client-request", opened.LinkToken, map[string]any{"body": string(answers), "confirmed": true})
			must(db.QueryRow(`SELECT source_id::text FROM request_submissions WHERE request_id::text=$1 ORDER BY id DESC LIMIT 1`, opened.ID).Scan(&answerID))
		} else {
			raw, _ := json.Marshal(map[string]any{"revision": revision, "body": "2026-09-14"})
			req := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/work/"+job+"/answer", bytes.NewReader(raw))
			token := strings.Repeat("a", 32)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			apiWithStore(db, token).ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatalf("answer: %d %s", response.Code, response.Body.String())
			}
			must(db.QueryRow(`SELECT source_id::text FROM preparation_answers WHERE job_id::text=$1`, job).Scan(&answerID))
		}
		must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id::text=$1`, job).Scan(&state))
		if state != "waiting_evidence" {
			t.Fatalf("answer did not resume its job: %s", state)
		}
		worked, err = preparation.ProcessIntake(modelContext(t, context.Background()), db)
		must(err)
		if !worked {
			t.Fatal("answered work did not resume")
		}
	}
	must(db.QueryRow(`SELECT j.state,j.error || COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &checkpoint))
	if state != "queued" {
		t.Fatalf("asset did not queue: %s %s", state, checkpoint)
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
	must(db.QueryRow(`SELECT state,error FROM preparation_jobs WHERE id::text=$1`, job).Scan(&state, &checkpoint))
	if withdraw {
		if state == "completed" || !strings.Contains(checkpoint, "approved depreciation example") {
			t.Fatalf("withdrawn example not held: %s %s", state, checkpoint)
		}
		return
	}
	if state != "completed" {
		t.Fatalf("worker did not prepare: %s %s", state, checkpoint)
	}
	var expense string
	var straight bool
	must(db.QueryRow(`SELECT paper->>'period_expense',COALESCE((paper->>'straight_through')::boolean,false) FROM workpapers WHERE close_id=$1`, closeID).Scan(&expense, &straight))
	if expense != "100.00" || !straight {
		t.Fatalf("wrong asset draft: %s %t", expense, straight)
	}
	if answered {
		var cited bool
		must(db.QueryRow(`SELECT paper->'sources' @> jsonb_build_array(jsonb_build_object('id',$2::text)) FROM workpapers WHERE close_id=$1`, closeID, answerID).Scan(&cited))
		if !cited {
			t.Fatal("the draft lost its answer source")
		}
	}
	// Replaying a completed queue does not save another draft.
	worked, err = preparation.ProcessOne(context.Background(), db)
	must(err)
	if worked {
		t.Fatal("completed asset was prepared twice")
	}
}
