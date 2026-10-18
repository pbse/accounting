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

func TestFixedIncurredAccrualQueuesAndRechecksItsPolicy(t *testing.T) {
	t.Run("draft", func(t *testing.T) { fixedIncurredAccrual(t, false) })
	t.Run("policy changed", func(t *testing.T) { fixedIncurredAccrual(t, true) })
}
func fixedIncurredAccrual(t *testing.T, changed bool) {

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
	entity := "Incurred " + rand.Text()
	var closeID, connection, procedureID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`,
			`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
		db.Exec(`DELETE FROM procedure_versions WHERE organization_id='local-development' AND client_entity=$1`, entity)
	}()
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,expense_account,prepaid_account,enabled,reason,actor) VALUES('local-development',$1,'accrual','fixed_incurred_amount_v1',E'Expense account: Professional Fees\nLiability account: Accrued Expenses\nReverse next month: true\nQuickBooks company: 123\nVendor ID for Acme Services: 8\nVendor ID for Other Services: 9\nVendor ID for Third Services: 10','','',true,'Fixture','fixture') RETURNING id::text`, entity).Scan(&procedureID))
	tx, err := db.Begin()
	must(err)
	procedure, err := preparation.CurrentProcedure(context.Background(), tx, closeID, "accrual")
	must(err)
	ruleID, err := preparation.AttachProcedure(context.Background(), tx, closeID, procedure, false, "fixture")
	must(err)
	must(tx.Commit())
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123','USD',1) RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	insert := func(name, body, state string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture',$5) RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body, state).Scan(&id))
		return id
	}
	// One vendor-stated unbilled service against an explicit zero liability.
	type invoice struct{ vendor, number, amount, printed, extra string }
	acme := invoice{"Acme Services", "INV-778", "1200.00", "1,200.00", ""}
	captured := insert("QuickBooks transactions 2026-09", "capture", "ready")
	var syncID string
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{"realm":"123"}') RETURNING id::text`, closeID, connection, captured).Scan(&syncID))
	snapshot := insert("QuickBooks 2026-09 accrual period evidence.txt", "period snapshot", "ready")
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, connection, rand.Text(), `{"period":"2026-09","source_id":"`+snapshot+`","accounts":[{"Id":"12","Name":"Professional Fees","AccountType":"Expense"},{"Id":"13","Name":"Accrued Expenses","AccountType":"Other Current Liability"}],"reports":{"TrialBalance":{"Header":{"Currency":"USD","ReportBasis":"Accrual"},"Columns":{"Column":[{"ColTitle":"Account"},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"13","value":"Accrued Expenses"},{"value":"0.00"},{"value":"0.00"}]}]}}}}`)
	must(err)
	text := func(inv invoice) string {
		return inv.vendor + "\nInvoice " + inv.number + "\nBill to: " + entity + "\nUnbilled service total: USD " + inv.printed + "\nService period 2026-09-01 to 2026-09-30\nEstimation basis: Fixed incurred September service\n" + inv.extra
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
			quote("amount", inv.amount, "Unbilled service total: USD "+inv.printed), quote("currency", "USD", "Unbilled service total: USD "+inv.printed),
			quote("service_start", "2026-09-01", "Service period 2026-09-01 to 2026-09-30"), quote("service_end", "2026-09-30", "Service period 2026-09-01 to 2026-09-30"),
			quote("estimation_basis", "Fixed incurred September service", "Estimation basis: Fixed incurred September service"), quote("vendor", inv.vendor, inv.vendor), quote("billed_to", entity, "Bill to: "+entity), quote("invoice_number", inv.number, "Invoice "+inv.number),
		}
		for _, f := range []struct{ field, value, quote string }{{"expense_account", "Professional Fees", "Expense account: Professional Fees"}, {"liability_account", "Accrued Expenses", "Liability account: Accrued Expenses"}, {"reverse_next_month", "true", "Reverse next month: true"}} {
			facts = append(facts, map[string]string{"field": f.field, "value": f.value, "quote": f.quote, "source_id": ruleID})
		}
		output, _ := json.Marshal(map[string]any{"kind": "accrual", "summary": "Fixed incurred service", "questions": []string{}, "sources": []map[string]string{{"id": given.Primary, "role": "invoice"}}, "facts": facts})
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
		must(db.QueryRow(`SELECT j.state,j.error || COALESCE(i.agent_checkpoint->>'straight_through','') FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id::text=$1`, job).Scan(&s, &checkpoint))
		return s, checkpoint
	}

	source := insert("service.txt", text(acme), "ready")
	invoices[source] = acme
	job := start(source)
	var anotherJob string
	if !changed {
		another := acme
		another.vendor = "Other Services"
		another.number = "OS-18"
		id := insert("other-service.txt", text(another), "ready")
		invoices[id] = another
		anotherJob = start(id)
	}
	for step := 0; step < 6; step++ {
		if !read() {
			break
		}
	}
	if state, why := state(job); state != "queued" {
		t.Fatalf("accrual did not queue: %s %s", state, why)
	}
	if !changed {
		if state, why := state(anotherJob); state != "queued" {
			t.Fatalf("both jobs must queue before either saves: %s %s", state, why)
		}
	}

	// A changed policy between interpretation and preparation must hold the queued plan.
	if changed {
		must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,method,instructions,enabled,reason,actor) VALUES('local-development',$1,'accrual','fixed_incurred_amount_v1',E'Expense account: Professional Fees\nLiability account: Accrued Expenses\nReverse next month: false',true,'Changed synthetic reversal','fixture') RETURNING id::text`, entity).Scan(&procedureID))
	}
	worked, e := preparation.ProcessOne(context.Background(), db)
	must(e)
	if !worked {
		t.Fatal("preparation worker did not claim the queued accrual")
	}
	var count int
	must(db.QueryRow(`SELECT count(*) FROM workpapers WHERE close_id=$1`, closeID).Scan(&count))
	if changed {
		var reason string
		must(db.QueryRow(`SELECT error FROM preparation_jobs WHERE id::text=$1`, job).Scan(&reason))
		if !strings.Contains(reason, "procedure") {
			t.Fatalf("wrong hold reason: %s", reason)
		}
	}
	if changed && count != 0 {
		t.Fatal("worker saved a draft under a changed procedure")
	}
	if !changed && count != 1 {
		t.Fatalf("worker did not save one draft: %d", count)
	}
	if !changed {
		worked, e := preparation.ProcessOne(context.Background(), db)
		must(e)
		if !worked {
			t.Fatal("second queued job was not claimed")
		}
		if state, why := state(anotherJob); state == "completed" || !strings.Contains(why, "another current accrual") {
			t.Fatalf("worker did not hold a competing queued draft: %s %s", state, why)
		}
		var original string
		must(db.QueryRow(`SELECT id::text FROM workpapers WHERE close_id=$1`, closeID).Scan(&original))
		// Replace through the ordinary evidence route; the replacement may revise its own unposted predecessor.
		revised := acme
		revised.amount = "1500.00"
		revised.printed = "1,500.00"
		replacement := insert("corrected-service.txt", text(revised), "ready")
		invoices[replacement] = revised
		token := strings.Repeat("x", 32)
		raw, _ := json.Marshal(map[string]string{"replacement_id": replacement, "reason": "Corrected vendor-stated unbilled amount"})
		request := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/evidence/"+source+"/replace", bytes.NewReader(raw))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		apiWithStore(db, token).ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("replacement: %d %s", response.Code, response.Body.String())
		}
		replacementJob := start(replacement)
		if !read() {
			t.Fatal("replacement was not read")
		}
		if state, why := state(replacementJob); state != "queued" {
			t.Fatalf("own predecessor was blocked: %s %s", state, why)
		}
		worked, e = preparation.ProcessOne(context.Background(), db)
		must(e)
		if !worked {
			t.Fatal("replacement was not prepared")
		}
		var revises string
		must(db.QueryRow(`SELECT COALESCE(paper->>'revises','') FROM workpapers WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID).Scan(&revises))
		if revises != original {
			t.Fatalf("replacement lost lineage: %s want %s", revises, original)
		}
		// Queue-time collision protection remains in effect after the predecessor was superseded.
		third := acme
		third.vendor = "Third Services"
		third.number = "TS-19"
		thirdSource := insert("third-service.txt", text(third), "ready")
		invoices[thirdSource] = third
		thirdJob := start(thirdSource)
		if !read() {
			t.Fatal("third service was not read")
		}
		if state, why := state(thirdJob); state != "needs_confirmation" || !strings.Contains(why, "another current accrual") {
			t.Fatalf("queue did not hold an existing current draft: %s %s", state, why)
		}
	}
}
