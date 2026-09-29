package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

// TestClientReplyCompletesTheDraftWithoutARestart is the client follow-through proof of docs/maturity.md: an
// incomplete invoice leaves the agent with a question; the firm approves one request and issues a link; the
// client answers part of it, then sends the full invoice; the file is scanned and released; the released
// document's own intake reads it and the draft is prepared straight through, with no one restarting the work.
// A withdrawn link no longer opens, and a reply on it is refused.
func TestClientReplyCompletesTheDraftWithoutARestart(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	entity := "Reply sequence " + rand.Text()
	var closeID, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id=$1`,
			`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`,
			`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`,
			`UPDATE evidence SET superseded_by=NULL WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`,
		} {
			if _, e := db.Exec(q, closeID); e != nil {
				t.Error(e)
			}
		}
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
	}()
	// The client's approved prepaid procedure, its QuickBooks connection, the booked bill and the period snapshot.
	var procedureID string
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor) VALUES('local-development',$1,'prepaid','equal_full_months_v1','Equal full months','Insurance','Prepaid Expenses',true,'Fixture','fixture') RETURNING id::text`, entity).Scan(&procedureID))
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(ctx, tx, closeID, "prepaid")
	must(err)
	_, err = preparation.AttachProcedure(ctx, tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now()+interval '1 day','123','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	insert := func(name, body string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id))
		return id
	}
	captureID := insert("QuickBooks transactions 2026-09", "capture")
	var syncID string
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{}') RETURNING id::text`, closeID, connection, captureID).Scan(&syncID))
	record, _ := json.Marshal(map[string]any{"DocNumber": "INV-778", "TotalAmt": json.Number("1200.00"), "VendorRef": map[string]string{"value": "7", "name": "Acme Insurance Co"}, "Line": []map[string]any{{"Amount": json.Number("1200.00"), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"value": "12", "name": "Insurance"}}}}})
	_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill','41','1','2026-09-03',now(),'USD','1200.00',$2)`, syncID, string(record))
	must(err)
	snapshotSource := insert("QuickBooks 2026-09 accrual period evidence.txt", "period snapshot")
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+snapshotSource+`","accounts":[{"Id":"12","Name":"Insurance"},{"Id":"13","Name":"Prepaid Expenses"}],"reports":{}}`)
	must(err)

	// The model quotes whatever the primary invoice prints; the first invoice omits its coverage period.
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	complete := map[string]bool{} // evidence id → whether the invoice prints its coverage period
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		if !strings.Contains(r.URL.Path, "/responses") { // the scanner below is a real local server
			return originalTransport.RoundTrip(r)
		}
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
		full, known := complete[given.Primary]
		if !known {
			t.Fatalf("the model was asked about an unknown primary source: %s", raw)
		}
		quote := func(field, value, line string) map[string]string {
			return map[string]string{"field": field, "value": value, "source_id": given.Primary, "quote": line}
		}
		facts := []map[string]string{
			quote("amount", "1200.00", "Annual premium USD 1,200.00"),
			quote("currency", "USD", "Annual premium USD 1,200.00"),
			quote("vendor", "Acme Insurance Co", "Acme Insurance Co"),
			quote("billed_to", entity, "Bill to: "+entity),
			quote("invoice_number", "INV-778", "Invoice INV-778"),
		}
		questions := []string{"Which period does invoice INV-778 cover? The invoice does not print its coverage dates."}
		if full {
			facts = append(facts, quote("service_start", "2026-09-01", "Coverage period 2026-09-01 to 2027-08-31"), quote("service_end", "2027-08-31", "Coverage period 2026-09-01 to 2027-08-31"))
			questions = []string{}
		}
		output, _ := json.Marshal(map[string]any{"kind": "prepaid", "summary": "Insurance prepaid schedule", "questions": questions, "sources": []map[string]string{{"id": given.Primary, "role": "invoice"}}, "facts": facts})
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	header := "Acme Insurance Co\nInvoice INV-778\nBill to: " + entity + "\nAnnual premium USD 1,200.00\n"
	fullInvoice := header + "Coverage period 2026-09-01 to 2027-08-31\n"
	first := insert("insurance-page-1.txt", header)
	complete[first] = false
	tx, err = db.Begin()
	must(err)
	job, err := preparation.StartIntake(ctx, tx, closeID, first, "fixture")
	must(err)
	must(tx.Commit())
	runIntake := func() {
		t.Helper()
		for worked := true; worked; {
			worked, err = preparation.ProcessIntake(ctx, db)
			must(err)
		}
	}
	state := func(id string) (s string) {
		t.Helper()
		must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id::text=$1`, id).Scan(&s))
		return
	}
	runIntake()
	if state(job) != "needs_confirmation" {
		t.Fatalf("an invoice without its coverage period leaves the agent with a question: %s", state(job))
	}

	auth := strings.Repeat("q", 32)
	h := apiWithStore(db, auth)
	call := func(method, path, token string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + closeID + "/work/" + job
	revision := func() (n int) {
		t.Helper()
		must(db.QueryRow(`SELECT revision FROM preparation_intakes WHERE job_id::text=$1`, job).Scan(&n))
		return
	}
	// The firm approves one request for the missing facts and issues the client's private link.
	call("POST", path+"/request", auth, map[string]any{"revision": revision(), "recipient": "client@example.test", "body": "Please help us finish September:\n\n1. Which period does invoice INV-778 cover?\n2. Send the full invoice, including its coverage page.\n\nThank you."}, 200)
	var requestID string
	must(db.QueryRow(`SELECT id::text FROM work_requests WHERE job_id::text=$1`, job).Scan(&requestID))
	var link map[string]string
	must(json.Unmarshal(call("POST", path+"/link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200), &link))
	token := link["link_token"]
	if state(job) != "waiting_client" {
		t.Fatalf("the work waits for the client: %s", state(job))
	}
	// A partial answer is kept and the work keeps waiting.
	partial, _ := json.Marshal(map[string]any{"answers": []clientAnswer{{1, "answered", "September 2026 through August 2027"}, {2, "awaiting_document", "Scanning the full invoice tomorrow"}}})
	call("POST", "/v1/client-request", token, map[string]any{"body": string(partial), "confirmed": true}, 200)
	if state(job) != "waiting_client" {
		t.Fatalf("a partial reply leaves the work waiting for the client: %s", state(job))
	}
	// The final document arrives with the completed answers.
	answers, _ := json.Marshal(map[string]any{"answers": []clientAnswer{{1, "answered", "September 2026 through August 2027"}, {2, "answered", "The full invoice is attached"}}})
	var b bytes.Buffer
	m := multipart.NewWriter(&b)
	must(m.WriteField("body", string(answers)))
	must(m.WriteField("confirmed", "true"))
	f, err := m.CreateFormFile("files", "insurance-full.txt")
	must(err)
	_, err = f.Write([]byte(fullInvoice))
	must(err)
	must(m.Close())
	r := httptest.NewRequest("POST", "/v1/client-request", &b)
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("final reply: %d %s", w.Code, w.Body.String())
	}
	var attachment string
	must(db.QueryRow(`SELECT id::text FROM client_attachments WHERE request_id::text=$1`, requestID).Scan(&attachment))
	// The scanner sees the exact bytes; a clean verdict lets a reviewer release the file into the close.
	scanKey := strings.Repeat("k", 32)
	t.Setenv("UPLOAD_SCAN_KEY", scanKey)
	scanner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Authorization") != "Bearer "+scanKey || string(body) != fullInvoice {
			t.Error("the scanner received something other than the authorized original bytes")
		}
		jsonReply(w, 200, map[string]string{"status": "clean", "version": "synthetic test engine"})
	}))
	defer scanner.Close()
	t.Setenv("TEXT_SCANNER_URL", scanner.URL)
	r = httptest.NewRequest("POST", "/v1/client-request/scan-text", strings.NewReader(fullInvoice))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("text scan: %d %s", w.Code, w.Body.String())
	}
	released := "/v1/closes/" + closeID + "/attachments/" + attachment
	call("POST", released, auth, map[string]any{"action": "release", "confirmed": true, "reason": "The client's full invoice for the approved request"}, 200)
	var source, newJob string
	must(db.QueryRow(`SELECT source_id::text FROM client_attachments WHERE id::text=$1`, attachment).Scan(&source))
	must(db.QueryRow(`SELECT job_id::text FROM preparation_intakes WHERE source_id::text=$1`, source).Scan(&newJob))
	complete[source] = true
	// Nobody restarts anything: the released invoice's own intake reads it, every fact is verified, and the proposal
	// waits only because the client's reply is in the close, which the straight-through rule leaves to a person.
	runIntake()
	var checkpoint, problem string
	must(db.QueryRow(`SELECT COALESCE(i.agent_checkpoint->>'straight_through',''),j.error FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, newJob).Scan(&checkpoint, &problem))
	if state(newJob) != "needs_confirmation" || problem != "" || !strings.Contains(checkpoint, "Client reply to request") {
		t.Fatalf("the released invoice is interpreted and waits for a person because of the client's reply: %s %q %s", state(newJob), problem, checkpoint)
	}
	var proposed struct {
		Questions []string
		Facts     []preparation.Fact
	}
	var raw []byte
	must(db.QueryRow(`SELECT proposal FROM preparation_intakes WHERE job_id::text=$1`, newJob).Scan(&raw))
	must(json.Unmarshal(raw, &proposed))
	values := map[string]string{}
	for _, f := range proposed.Facts {
		values[f.Field] = f.Value
	}
	if len(proposed.Questions) != 0 || values["service_end"] != "2027-08-31" || values["amount"] != "1200.00" {
		t.Fatalf("the released invoice answers the question with verified facts: %v %v", proposed.Questions, values)
	}
	// The person confirms the verified facts once; the worker prepares the draft.
	newPath := "/v1/closes/" + closeID + "/work/" + newJob
	var newRevision int
	must(db.QueryRow(`SELECT revision FROM preparation_intakes WHERE job_id::text=$1`, newJob).Scan(&newRevision))
	call("POST", newPath+"/confirm", auth, map[string]any{"revision": newRevision, "facts_confirmed": true, "inventory_confirmed": true, "input": map[string]any{"amount": "1200.00", "currency": "USD", "service_start": "2026-09-01", "service_end": "2027-08-31", "basis": "accrual", "expense_account": "Insurance", "prepaid_account": "Prepaid Expenses", "method": "equal_full_months_v1", "method_confirmed": true, "booking_confirmed": true}}, 200)
	if worked, e := preparation.ProcessOne(ctx, db); e != nil || !worked {
		t.Fatalf("the worker did not run: %v %v", worked, e)
	}
	var draft, cited string
	must(db.QueryRow(`SELECT w.id::text,w.paper->'input'->>'service_end' FROM preparation_jobs j JOIN workpapers w ON j.workpaper_ids @> jsonb_build_array(w.id::text) WHERE j.id::text=$1 AND j.state='completed'`, newJob).Scan(&draft, &cited))
	var citesRelease bool
	must(db.QueryRow(`SELECT paper->'sources' @> jsonb_build_array(jsonb_build_object('id',$2::text,'role','invoice')) FROM workpapers WHERE id::text=$1`, draft, source).Scan(&citesRelease))
	if cited != "2027-08-31" || !citesRelease {
		t.Fatalf("the draft cites the released invoice and carries its coverage: %s %v", cited, citesRelease)
	}
	if state(job) != "needs_confirmation" {
		t.Fatalf("the original incomplete page still needs a person's decision, not a draft of its own: %s", state(job))
	}
	// The link stays open for corrections until the firm withdraws it; a withdrawn link neither opens nor takes a reply.
	call("POST", path+"/revoke_link", auth, map[string]any{"revision": revision(), "request_id": requestID}, 200)
	call("GET", "/v1/client-request", token, nil, 404)
	call("POST", "/v1/client-request", token, map[string]any{"body": string(answers), "confirmed": true}, 404)
}
