package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/preparation"
)

// Two unrelated invoices in one close are each prepared without confirmation, whatever order they are read in; a
// document still being read makes the agent wait, and one naming an invoice, or speaking of a credit, still holds it.
func TestUnrelatedInvoicesInOneCloseArePreparedStraightThrough(t *testing.T) {
	mixedClose(t, false, false)
}

// With the preparation worker saving drafts between reads, as it runs, all three invoices end as drafts within budget.
func TestUnrelatedInvoicesAreDraftedWithTheWorkerRunningBetweenReads(t *testing.T) {
	mixedClose(t, true, false)
}

func TestAnIdentifiedOtherSupplierCreditLeavesIndependentInvoicesRunning(t *testing.T) {
	mixedClose(t, true, true)
}

func mixedClose(t *testing.T, serial, supplierCredit bool) {
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
	entity := "Mixed " + rand.Text()
	var closeID, connection, procedureID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`,
			`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
	}()
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor) VALUES('local-development',$1,'prepaid','equal_full_months_v1','Equal full months','Insurance','Prepaid Expenses',true,'Fixture','fixture') RETURNING id::text`, entity).Scan(&procedureID))
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(context.Background(), tx, closeID, "prepaid")
	must(err)
	_, err = preparation.AttachProcedure(context.Background(), tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	insert := func(name, body, state string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture',$5) RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body, state).Scan(&id))
		return id
	}
	// Two carriers' bills, each booking its premium to Insurance in September.
	type invoice struct{ vendor, number, amount, printed, extra string }
	acme, beacon, carter := invoice{"Acme Insurance Co", "INV-778", "1200.00", "1,200.00", ""}, invoice{"Beacon Mutual", "BM-4410", "600.00", "600.00", ""}, invoice{"Carter Assurance", "CA-12", "300.00", "300.00", ""}
	captured := insert("QuickBooks transactions 2026-09", "capture", "ready")
	var syncID string
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{"realm":"123"}') RETURNING id::text`, closeID, connection, captured).Scan(&syncID))
	for i, inv := range []invoice{acme, beacon, carter} {
		record, _ := json.Marshal(map[string]any{"DocNumber": inv.number, "TotalAmt": json.Number(inv.amount), "VendorRef": map[string]string{"value": string(rune('7' + i)), "name": inv.vendor}, "Line": []map[string]any{{"Amount": json.Number(inv.amount), "AccountBasedExpenseLineDetail": map[string]any{"AccountRef": map[string]string{"value": "12", "name": "Insurance"}}}}})
		_, e := db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'Bill',$2,'1','2026-09-03',now(),'USD',$3,$4)`, syncID, string(rune('4'+i)), inv.amount, string(record))
		must(e)
	}
	snapshot := insert("QuickBooks 2026-09 accrual period evidence.txt", "period snapshot", "ready")
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+snapshot+`","accounts":[{"Id":"12","Name":"Insurance"},{"Id":"13","Name":"Prepaid Expenses"}],"reports":{}}`)
	must(err)
	tx, err = db.Begin()
	must(err)
	ledger, err := preparation.CloseLedger(context.Background(), tx, closeID)
	must(err)
	must(tx.Rollback())
	text := func(inv invoice) string {
		return inv.vendor + "\nInvoice " + inv.number + "\nBill to: " + entity + "\nAnnual premium USD " + inv.printed + "\nCoverage period 2026-09-01 to 2027-08-31\n" + inv.extra
	}

	// The model reads whichever invoice is primary and quotes its own lines.
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	invoices := map[string]invoice{}
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
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
		inv, ok := invoices[given.Primary]
		if !ok {
			t.Fatalf("the model was asked about an unknown primary source: %s", raw)
		}
		quote := func(field, value, line string) map[string]string {
			return map[string]string{"field": field, "value": value, "source_id": given.Primary, "quote": line}
		}
		facts := []map[string]string{
			quote("amount", inv.amount, "Annual premium USD "+inv.printed), quote("currency", "USD", "Annual premium USD "+inv.printed),
			quote("service_start", "2026-09-01", "Coverage period 2026-09-01 to 2027-08-31"), quote("service_end", "2027-08-31", "Coverage period 2026-09-01 to 2027-08-31"),
			quote("vendor", inv.vendor, inv.vendor), quote("billed_to", entity, "Bill to: "+entity), quote("invoice_number", inv.number, "Invoice "+inv.number),
		}
		output, _ := json.Marshal(map[string]any{"kind": "prepaid", "summary": "Insurance prepaid schedule", "questions": []string{}, "sources": []map[string]string{{"id": given.Primary, "role": "invoice"}}, "facts": facts})
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	start := func(source string) string {
		t.Helper()
		tx, err := db.Begin()
		must(err)
		job, err := preparation.StartIntake(context.Background(), tx, closeID, source, "fixture")
		must(err)
		must(tx.Commit())
		return job
	}
	read := func() bool {
		t.Helper()
		worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db)
		must(e)
		return worked
	}
	state := func(job string) (string, string) {
		t.Helper()
		var s, checkpoint string
		must(db.QueryRow(`SELECT j.state,COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&s, &checkpoint))
		return s, checkpoint
	}

	// Beacon's invoice is still being read when Acme's is ready: the agent waits, with no model call.
	first := insert("acme.txt", text(acme), "ready")
	second := insert("beacon.txt", text(beacon), "processing")
	third := insert("carter.txt", text(carter), "ready")
	invoices[first], invoices[second], invoices[third] = acme, beacon, carter
	if supplierCredit {
		_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,'VendorCredit','51','1','2026-09-04',now(),'USD',30,$2)`, syncID, `{"Id":"51","DocNumber":"CA-12-C","VendorRef":{"value":"9","name":"Carter Assurance"},"TotalAmt":30,"Line":[{"Amount":30,"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"12","name":"Insurance"}}}]}`)
		must(err)
		credit := insert("carter-credit.txt", "Carter Assurance\nCredit note CA-12-C\nBill to: "+entity+"\nOriginal invoice: CA-12\nCredit total USD 30.00", "ready")
		creditJob := start(credit)
		_, err = db.Exec(`UPDATE preparation_intakes SET proposal=jsonb_build_object('kind','unsupported','engine','context_machine','summary','Supplier credit','facts','[]'::jsonb,'questions',jsonb_build_array('Confirm credit treatment'),'sources',jsonb_build_array(jsonb_build_object('id',$2::text,'role','support'))) WHERE job_id::text=$1`, creditJob, credit)
		must(err)
		_, err = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, creditJob)
		must(err)
	}
	jobA, jobB, jobC := start(first), start(second), start(third)
	if read() {
		t.Fatal("the agent waits while other evidence in the close is still being read")
	}
	_, err = db.Exec(`UPDATE evidence SET extraction_state='ready' WHERE id::text=$1`, second)
	must(err)
	if serial {
		for steps := 0; ; steps++ {
			read, saved := read(), false
			if worked, e := preparation.ProcessOne(context.Background(), db); e != nil {
				t.Fatal(e)
			} else {
				saved = worked
			}
			if !read && !saved {
				break
			}
			if steps > 10 {
				t.Fatal("reading and saving do not settle")
			}
		}
		for _, job := range []string{jobA, jobB, jobC} {
			var done string
			must(db.QueryRow(`SELECT state||' '||jsonb_array_length(workpaper_ids) FROM preparation_jobs WHERE id::text=$1`, job).Scan(&done))
			if supplierCredit && job == jobC {
				if s, checkpoint := state(job); done != "needs_confirmation 0" || !strings.Contains(checkpoint, "evidence in the close (carter-credit.txt) was not used") {
					t.Fatalf("Carter still needs its credit decision: %s %s %s", done, s, checkpoint)
				}
				continue
			}
			if done != "completed 1" {
				_, checkpoint := state(job)
				t.Fatalf("each invoice ends as one draft: %s %s", done, checkpoint)
			}
		}
		if supplierCredit {
			return
		}
		// A credit note arriving after the drafts reopens only the draft whose invoice it names; a document that names no
		// draft's invoice, credit or not, changes nothing.
		review := func(job string) string {
			t.Helper()
			var state string
			must(db.QueryRow(`SELECT w.review_state FROM workpaper_review_state w JOIN preparation_jobs j ON j.workpaper_ids @> jsonb_build_array(w.id::text) WHERE j.id::text=$1`, job).Scan(&state))
			return state
		}
		insert("other-credit.txt", "Delta Insurers\nCredit note CN-9 against invoice DL-1", "ready")
		if review(jobA) != "pending" || review(jobB) != "pending" {
			t.Fatalf("a credit naming no draft's invoice changes nothing: %s %s", review(jobA), review(jobB))
		}
		insert("acme-credit.txt", "Acme Insurance Co\nCredit note CN-1 against invoice INV-778\nCredited USD 200.00", "ready")
		if review(jobA) != "source_changed" || review(jobB) != "pending" || review(jobC) != "pending" {
			t.Fatalf("a later credit note naming Acme's invoice reopens Acme's draft only: %s %s %s", review(jobA), review(jobB), review(jobC))
		}
		return
	}
	// An invoice read before the others waits on them; once every other is read as an invoice of its own it is read
	// again, so three invoices take five reads, within each job's attempts.
	reads := 0
	for read() {
		if reads++; reads > 5 {
			t.Fatal("the agent keeps reading")
		}
	}
	for _, job := range []string{jobA, jobB, jobC} {
		if s, checkpoint := state(job); s != "queued" {
			t.Fatalf("each unrelated invoice is prepared without confirmation: %s %s", s, checkpoint)
		}
	}
	// If Beacon's document stops being read as an invoice before Acme's draft is saved, Acme's is read again.
	retype := func(kind string) {
		t.Helper()
		_, e := db.Exec(`UPDATE preparation_intakes SET proposal=jsonb_set(proposal,'{kind}',to_jsonb($2::text)) WHERE job_id::text=$1`, jobB, kind)
		must(e)
	}
	retype("unsupported")
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatalf("the worker ran: %v %v", worked, e)
	}
	if s, _ := state(jobA); s != "waiting_evidence" {
		t.Fatalf("an exemption that no longer holds is proven again by reading the close: %s", s)
	}
	retype("prepaid")
	for read() {
	}
	if s, checkpoint := state(jobA); s != "queued" {
		t.Fatalf("read again, Acme's invoice is prepared: %s %s", s, checkpoint)
	}
	// A document arriving after Acme's was read but before its draft is saved sends it back to be read again.
	note := insert("note.txt", "Board minutes for September", "ready")
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatalf("the worker ran: %v %v", worked, e)
	}
	if s, _ := state(jobA); s != "waiting_evidence" {
		t.Fatalf("a changed close is read again, not handed to a person: %s", s)
	}

	// An invoice that also names Acme's invoice, speaks of a credit, or has a page the extractor could not read is not
	// unrelated; nor is a document no invoice job reads. Beacon's own invoice is.
	for i, extra := range []string{"Reference: INV-778", "Credit applied for overbilling", documents.UnreadPage} {
		other := beacon
		other.number, other.extra = fmt.Sprintf("BM-50%d", i), extra
		id := insert(other.number+".txt", text(other), "ready")
		invoices[id] = other
		start(id)
		for read() {
		}
		var raw []byte
		must(db.QueryRow(`SELECT proposal FROM preparation_intakes WHERE job_id::text=$1`, jobA).Scan(&raw))
		var proposal preparation.Proposal
		must(json.Unmarshal(raw, &proposal))
		tx, err := db.Begin()
		must(err)
		unrelated, err := preparation.UnrelatedEvidence(context.Background(), tx, closeID, jobA, proposal, ledger)
		tx.Rollback()
		must(err)
		if unrelated[id] || unrelated[note] || !unrelated[second] {
			t.Fatalf("%q is weighed by a person, while Beacon's invoice is unrelated to Acme's: %v", extra, unrelated)
		}
	}
	// A bank statement, read (as one standing alone is) as unsupported work citing itself as a statement, is another
	// matter, its "credits" header included, unless it prints Acme's amount anywhere or names its invoice; a document the
	// agent did not call a statement is not.
	statement := func(name, body, role string) string {
		t.Helper()
		id := insert(name, body, "ready")
		job := start(id)
		_, e := db.Exec(`UPDATE preparation_intakes SET proposal=jsonb_build_object('kind','unsupported','engine','openai','summary','Bank statement','facts','[]'::jsonb,'questions',jsonb_build_array('Which work does this statement support?'),'sources',jsonb_build_array(jsonb_build_object('id',$2::text,'role',$3::text))) WHERE job_id::text=$1`, job, id, role)
		must(e)
		_, e = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, job)
		must(e)
		return id
	}
	plain := statement("bank.txt", "First National Bank\nStatement for September 2026\nDeposits and other credits 4,500.00\nCheck 1042 250.00", "statement")
	refunded := statement("bank-refund.txt", "First National Bank\nStatement for September 2026\nDeposits and other credits\n09/14 1,200.00", "statement")
	named := statement("bank-acme.txt", "First National Bank\nStatement for September 2026\nPayment INV-778 250.00", "statement")
	whole := statement("bank-whole.txt", "First National Bank\nStatement for September 2026\nPayment 1200", "statement")
	unnamed := statement("letter.txt", "First National Bank\nNotice of new fees", "support")
	var raw []byte
	must(db.QueryRow(`SELECT proposal FROM preparation_intakes WHERE job_id::text=$1`, jobA).Scan(&raw))
	var proposal preparation.Proposal
	must(json.Unmarshal(raw, &proposal))
	unrelated, err := preparation.UnrelatedEvidence(context.Background(), db, closeID, jobA, proposal, ledger)
	must(err)
	if !unrelated[plain] || unrelated[refunded] || unrelated[named] || unrelated[unnamed] || unrelated[whole] {
		t.Fatalf("a plain bank statement is another matter; one printing Acme's amount or naming its invoice, or a document not read as a statement, is not: %v", unrelated)
	}
	t.Run("independent customer invoices", func(t *testing.T) {
		scope, err := db.Begin()
		must(err)
		defer scope.Rollback()
		advance := func(customer, number, extra string) (string, string, preparation.Proposal) {
			t.Helper()
			body := entity + "\nInvoice " + number + "\nBill to: " + customer + "\nTotal USD 1,200.00\nService period 2026-09-01 to 2027-08-31\n" + extra
			var source, job string
			must(scope.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture','ready') RETURNING id::text`, closeID, number+".txt", preparation.Digest([]byte(body)), body).Scan(&source))
			must(scope.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, closeID, "intake:"+source).Scan(&job))
			p := preparation.Proposal{Kind: "deferred_revenue", Engine: "context_machine", Sources: []preparation.Source{{ID: source, Role: "invoice"}}}
			for _, f := range []struct{ field, value, quote string }{{"vendor", entity, entity}, {"billed_to", customer, "Bill to: " + customer}, {"invoice_number", number, "Invoice " + number}, {"amount", "1200.00", "Total USD 1,200.00"}, {"currency", "USD", "Total USD 1,200.00"}, {"service_start", "2026-09-01", "Service period 2026-09-01 to 2027-08-31"}, {"service_end", "2027-08-31", "Service period 2026-09-01 to 2027-08-31"}} {
				p.Facts = append(p.Facts, preparation.Fact{Field: f.field, Value: f.value, Candidates: []preparation.Candidate{{Value: f.value, SourceID: source, Quote: f.quote}}})
			}
			raw, _ := json.Marshal(p)
			_, err = scope.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,$3)`, job, source, string(raw))
			must(err)
			return source, job, p
		}
		firstAdvance, advanceJob, advanceProposal := advance("Northwind", "REV-22", "")
		secondAdvance, _, _ := advance("Contoso", "REV-23", "")
		sameCustomer, _, _ := advance("Northwind", "REV-24", "")
		credit, _, _ := advance("Another customer", "REV-25", "Credit applied for overbilling")
		disputed, _, _ := advance("Different customer", "REV-26", "--- Page 1 · PDF text; Gemini fixture read it differently, so check the original ---")
		unused, err := preparation.UnrelatedEvidence(context.Background(), scope, closeID, jobA, proposal, ledger)
		must(err)
		if !unused[firstAdvance] || !unused[secondAdvance] || unused[credit] || unused[disputed] {
			t.Fatalf("independent advances release supplier work; credits and image conflicts remain held: %v", unused)
		}
		unused, err = preparation.UnrelatedEvidence(context.Background(), scope, closeID, advanceJob, advanceProposal, ledger)
		must(err)
		if !unused[first] || !unused[secondAdvance] || unused[sameCustomer] || unused[credit] || unused[disputed] {
			t.Fatalf("different counterparties progress, same customer and credits remain held: %v", unused)
		}
	})
	// A rescan of Acme's invoice (other bytes, the same vendor, number, amount, currency and service dates) does not hold
	// Acme's invoice, and its own job waits for a person, naming the document it copies, rather than preparing it twice.
	rescan := insert("acme rescan.txt", text(acme)+"Scanned 2026-09-30", "ready")
	invoices[rescan] = acme
	rescanJob := start(rescan)
	for read() {
	}
	if unrelated, err = preparation.UnrelatedEvidence(context.Background(), db, closeID, jobA, proposal, ledger); err != nil || !unrelated[rescan] {
		t.Fatalf("a later copy does not hold the original: %v %v", unrelated, err)
	}
	// Acme's own work failed in this fixture, so the rescan is not deferred to it: Acme's copy does not hold the rescan.
	var rescanProposal preparation.Proposal
	must(db.QueryRow(`SELECT proposal FROM preparation_intakes WHERE job_id::text=$1`, rescanJob).Scan(&raw))
	must(json.Unmarshal(raw, &rescanProposal))
	if s, checkpoint := state(jobA); s != "failed" || strings.Contains(checkpoint, "another copy") {
		t.Fatalf("the fixture's Acme job failed: %s %s", s, checkpoint)
	}
	if unrelated, err = preparation.UnrelatedEvidence(context.Background(), db, closeID, rescanJob, rescanProposal, ledger); err != nil || !unrelated[first] {
		t.Fatalf("an earlier copy whose work stopped does not hold the rescan: %v %v", unrelated, err)
	}
	if _, checkpoint := state(rescanJob); strings.Contains(checkpoint, "another copy") {
		t.Fatalf("a rescan is not deferred to stopped work: %s", checkpoint)
	}
	// While Acme's work is live, the rescan read again waits for a person, naming the document it copies.
	_, err = db.Exec(`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`, jobA)
	must(err)
	_, err = db.Exec(`UPDATE preparation_intakes SET revision=revision+1,agent_claim=NULL WHERE job_id::text=$1`, rescanJob)
	must(err)
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_evidence',error='',finished_at=NULL WHERE id::text=$1`, rescanJob)
	must(err)
	for read() {
	}
	if s, checkpoint := state(rescanJob); s != "needs_confirmation" || !strings.Contains(checkpoint, "another copy of acme.txt") {
		t.Fatalf("a rescan is not prepared twice: %s %s", s, checkpoint)
	}
}
