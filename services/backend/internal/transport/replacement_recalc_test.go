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

	"close.local/backend/internal/preparation"
)

// A replaced invoice is prepared again without a person: the agent reads the replacement, the straight-through check
// passes against the new capture, the new draft revises the old one, the old approval is gone, the change review shows
// only what moved, and an unrelated approved draft stands.
func TestReplacedInvoiceIsRecalculatedStraightThrough(t *testing.T) {
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
	entity := "Recalc " + rand.Text()
	var closeID, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, `DELETE FROM sandbox_postings WHERE close_id=$1`, `DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
	}()
	// The client's approved prepaid procedure, attached to the close as evidence, and its QuickBooks connection.
	var procedureID string
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor) VALUES('local-development',$1,'prepaid','equal_full_months_v1','Equal full months','Insurance','Prepaid Expenses',true,'Fixture','fixture') RETURNING id::text`, entity).Scan(&procedureID))
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(context.Background(), tx, closeID, "prepaid")
	must(err)
	_, err = preparation.AttachProcedure(context.Background(), tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	insert := func(name, body string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id))
		return id
	}
	// capture saves a completed transaction capture whose one bill books the amount, superseding the previous capture.
	var lastCapture string
	capture := func(amount string) {
		t.Helper()
		evidenceID := insert("QuickBooks transactions 2026-09 "+amount, "capture "+amount)
		if lastCapture != "" {
			_, e := db.Exec(`UPDATE evidence SET superseded_by=$2,replacement_reason='New QuickBooks transaction capture',replaced_by='quickbooks-sync',replaced_at=now() WHERE id::text=$1`, lastCapture, evidenceID)
			must(e)
		}
		lastCapture = evidenceID
		var syncID string
		must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{}') RETURNING id::text`, closeID, connection, evidenceID).Scan(&syncID))
		record, _ := json.Marshal(map[string]any{"DocNumber": "INV-778", "TotalAmt": json.Number(amount), "VendorRef": map[string]string{"value": "7", "name": "Acme Insurance Co"}, "Line": []map[string]any{{"Amount": json.Number(amount), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"value": "12", "name": "Insurance"}}}}})
		_, e := db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill','41','1','2026-09-03',now(),'USD',$2,$3)`, syncID, amount, string(record))
		must(e)
	}
	capture("1200.00")
	snapshotSource := insert("QuickBooks 2026-09 accrual period evidence.txt", "period snapshot")
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+snapshotSource+`","accounts":[{"Id":"12","Name":"Insurance"},{"Id":"13","Name":"Prepaid Expenses"}],"reports":{}}`)
	must(err)

	// The model reads whichever invoice is primary; its facts quote that invoice's own lines.
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	invoices := map[string]string{} // evidence id → amount
	calls := 0
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Input []struct {
				Content string `json:"content"`
			} `json:"input"`
		}
		json.Unmarshal(raw, &request)
		var given struct {
			Primary string `json:"primary_source_id"`
		}
		for _, item := range request.Input {
			json.Unmarshal([]byte(item.Content), &given)
		}
		amount := invoices[given.Primary]
		if amount == "" {
			t.Fatalf("the model was asked about an unknown primary source: %s", raw)
		}
		printed := amount[:len(amount)-6] + "," + amount[len(amount)-6:]
		quote := func(field, value, line string) map[string]string {
			return map[string]string{"field": field, "value": value, "source_id": given.Primary, "quote": line}
		}
		facts := []map[string]string{
			quote("amount", amount, "Annual premium USD "+printed),
			quote("currency", "USD", "Annual premium USD "+printed),
			quote("service_start", "2026-09-01", "Coverage period 2026-09-01 to 2027-08-31"),
			quote("service_end", "2027-08-31", "Coverage period 2026-09-01 to 2027-08-31"),
			quote("vendor", "Acme Insurance Co", "Acme Insurance Co"),
			quote("billed_to", entity, "Bill to: "+entity),
			quote("invoice_number", "INV-778", "Invoice INV-778"),
		}
		output, _ := json.Marshal(map[string]any{"kind": "prepaid", "summary": "Insurance prepaid schedule", "questions": []string{}, "sources": []map[string]string{{"id": given.Primary, "role": "invoice"}}, "facts": facts})
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	invoiceText := func(amount string) string {
		printed := amount[:len(amount)-6] + "," + amount[len(amount)-6:]
		return "Acme Insurance Co\nInvoice INV-778\nBill to: " + entity + "\nAnnual premium USD " + printed + "\nCoverage period 2026-09-01 to 2027-08-31\n"
	}
	// intake starts an invoice's intake and runs the agent once, returning the job and its state.
	intake := func(invoiceID string) (job, state, checkpoint string) {
		t.Helper()
		tx, err := db.Begin()
		must(err)
		job, err = preparation.StartIntake(context.Background(), tx, closeID, invoiceID, "fixture")
		must(err)
		must(tx.Commit())
		if worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db); e != nil || !worked {
			t.Fatalf("the agent did not run: %v %v", worked, e)
		}
		must(db.QueryRow(`SELECT j.state,COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &checkpoint))
		return job, state, checkpoint
	}
	// finish runs the preparation worker on a queued job and returns its draft.
	finish := func(job string) (draft string) {
		t.Helper()
		var state, checkpoint string
		must(db.QueryRow(`SELECT j.state,COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&state, &checkpoint))
		if state != "queued" {
			t.Fatalf("the invoice was not prepared straight through: state=%s %s", state, checkpoint)
		}
		if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
			t.Fatalf("the worker did not run: %v %v", worked, e)
		}
		var ids string
		must(db.QueryRow(`SELECT state||' '||error||' '||workpaper_ids::text FROM preparation_jobs WHERE id::text=$1`, job).Scan(&ids))
		var papers []string
		if _, rest, ok := strings.Cut(ids, "completed  "); !ok || json.Unmarshal([]byte(rest), &papers) != nil || len(papers) != 1 {
			t.Fatalf("the worker saved one draft: %s", ids)
		}
		return papers[0]
	}
	first := insert("insurance.txt", invoiceText("1200.00"))
	invoices[first] = "1200.00"
	job1, _, _ := intake(first)
	draft1 := finish(job1)
	var marked bool
	must(db.QueryRow(`SELECT COALESCE((paper->>'straight_through')::boolean,false) FROM workpapers WHERE id::text=$1`, draft1).Scan(&marked))
	if !marked {
		t.Fatal("a straight-through draft's paper says so")
	}

	token := strings.Repeat("r", 32)
	handler := modelHandler(t, apiWithStore(db, token))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	base := "/v1/closes/" + closeID
	review := func(paper string) (state, reviewID, digest string) {
		t.Helper()
		var out struct {
			State    string `json:"state"`
			ReviewID string `json:"review_id"`
			Digest   string `json:"digest"`
		}
		must(json.Unmarshal(call("GET", base+"/workpapers/"+paper+"/reviews", nil, 200), &out))
		return out.State, out.ReviewID, out.Digest
	}
	_, reviewID, digest := review(draft1)
	call("POST", base+"/workpapers/"+draft1+"/reviews", map[string]string{"decision": "approved", "note": "Ties to the invoice", "digest": digest, "expected_review_id": reviewID}, 200)
	if state, _, _ := review(draft1); state != "approved" {
		t.Fatalf("the first draft is approved: %s", state)
	}
	// An unrelated approved draft in the same close must stand through the replacement.
	var other string
	otherPaper, _ := json.Marshal(map[string]any{"input": map[string]string{"amount": "600.00"}, "sources": []map[string]string{{"id": snapshotSource, "digest": preparation.Digest([]byte("period snapshot")), "name": "QuickBooks 2026-09 accrual period evidence.txt", "role": "support"}}})
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','other-digest',$2) RETURNING id::text`, closeID, string(otherPaper)).Scan(&other))
	_, otherReview, otherDigest := review(other)
	call("POST", base+"/workpapers/"+other+"/reviews", map[string]string{"decision": "approved", "note": "Unrelated", "digest": otherDigest, "expected_review_id": otherReview}, 200)

	// A corrected invoice arrives with a higher premium. Its intake runs first, while the old invoice is still current, so
	// it waits for a person; recording the replacement resumes it, the books now carry the corrected bill, and the second
	// pass prepares it straight through.
	second := insert("insurance-corrected.txt", invoiceText("1300.00"))
	invoices[second] = "1300.00"
	job2, state, checkpoint := intake(second)
	if state != "needs_confirmation" || !strings.Contains(checkpoint, "was not used") {
		t.Fatalf("with the old invoice still current the corrected one waits for a person: %s %s", state, checkpoint)
	}
	call("POST", base+"/evidence/"+first+"/replace", map[string]string{"replacement_id": second, "reason": "Corrected premium from the carrier."}, 200)
	capture("1300.00")
	if state, _, _ := review(draft1); state != "source_changed" {
		t.Fatalf("the replaced source changes the first draft's review state: %s", state)
	}
	must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id::text=$1`, job2).Scan(&state))
	if state != "waiting_evidence" {
		t.Fatalf("recording the replacement resumes the corrected invoice's interpretation: %s", state)
	}
	if worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db); e != nil || !worked {
		t.Fatalf("the agent did not run again: %v %v", worked, e)
	}
	draft2 := finish(job2)
	var revises string
	must(db.QueryRow(`SELECT COALESCE(paper->>'revises','') FROM workpapers WHERE id::text=$1`, draft2).Scan(&revises))
	if revises != draft1 {
		t.Fatalf("the new draft revises the replaced one: revises=%q want %s", revises, draft1)
	}
	var changes struct {
		Previous string `json:"previous_id"`
		Changes  any    `json:"changes"`
	}
	must(json.Unmarshal(call("GET", base+"/workpapers/"+draft2+"/changes", nil, 200), &changes))
	raw, _ := json.Marshal(changes.Changes)
	if changes.Previous != draft1 || !strings.Contains(string(raw), "1200.00") || !strings.Contains(string(raw), "1300.00") || strings.Contains(string(raw), "2027-08-31") {
		t.Fatalf("the change review shows the moved amount and not the unchanged dates: %s", raw)
	}
	if state, _, _ := review(draft2); state != "pending" {
		t.Fatalf("the recalculated draft starts pending review: %s", state)
	}
	if state, _, _ := review(other); state != "approved" {
		t.Fatalf("an unrelated approval stands: %s", state)
	}
	if calls != 3 {
		t.Fatalf("one model call for the first invoice and two for the corrected one: %d", calls)
	}
	// Once the recalculated draft's entry has been posted, a further correction is a person's: it must be made against that entry.
	_, err = db.Exec(`INSERT INTO sandbox_postings(close_id,workpaper_id,source_key,realm,digest,journal,state) VALUES($1,$2,'recalc-source','test-realm','test-digest','{}','verified')`, closeID, draft2)
	must(err)
	third := insert("insurance-final.txt", invoiceText("1400.00"))
	invoices[third] = "1400.00"
	call("POST", base+"/evidence/"+second+"/replace", map[string]string{"replacement_id": third, "reason": "Final premium."}, 200)
	capture("1400.00")
	_, state, checkpoint = intake(third)
	if state != "needs_confirmation" || !strings.Contains(checkpoint, "was posted") {
		t.Fatalf("a posted predecessor leaves the correction to a person: state=%s %s", state, checkpoint)
	}
	// When two current drafts cite the replaced source, the agent cannot know which one a correction revises.
	shape := func(amount string, sources []map[string]string) string {
		paper, _ := json.Marshal(map[string]any{"input": map[string]string{"amount": amount}, "sources": sources, "straight_through": true})
		return string(paper)
	}
	cite := map[string]string{"id": third, "digest": preparation.Digest([]byte(invoiceText("1400.00"))), "name": "insurance-final.txt", "role": "invoice"}
	chart := map[string]string{"id": snapshotSource, "digest": preparation.Digest([]byte("period snapshot")), "name": "QuickBooks 2026-09 accrual period evidence.txt", "role": "chart"}
	for _, digest := range []string{"twin-a", "twin-b"} {
		_, err = db.Exec(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3)`, closeID, digest, shape("1400.00", []map[string]string{cite, chart}))
		must(err)
	}
	fourth := insert("insurance-fourth.txt", invoiceText("1500.00"))
	invoices[fourth] = "1500.00"
	call("POST", base+"/evidence/"+third+"/replace", map[string]string{"replacement_id": fourth, "reason": "Fourth premium."}, 200)
	capture("1500.00")
	_, state, checkpoint = intake(fourth)
	if state != "needs_confirmation" || !strings.Contains(checkpoint, "more than one earlier source or draft") {
		t.Fatalf("an ambiguous lineage waits for a person: state=%s %s", state, checkpoint)
	}
}
