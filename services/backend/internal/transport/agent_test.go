package transport

import (
	"bytes"
	"context"
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

type modelRoundTrip func(*http.Request) (*http.Response, error)

func (f modelRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAgentAnswersResumeAndInventoryFence(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	var closeID, other string
	for _, v := range []struct {
		name string
		id   *string
	}{{"Agent fixture", &closeID}, {"Foreign agent fixture", &other}} {
		if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, v.name).Scan(v.id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, id := range []string{closeID, other} {
			for _, table := range []string{"preparation_jobs", "request_drafts", "workpapers", "evidence", "client_closes"} {
				column := "close_id"
				if table == "client_closes" {
					column = "id"
				}
				if _, e := db.Exec("DELETE FROM "+table+" WHERE "+column+"=$1", id); e != nil {
					t.Error(e)
				}
			}
		}
	}()
	insert := func(id, name, body string) string {
		t.Helper()
		var source string
		if e := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, id, name, preparation.Digest([]byte(body)), body).Scan(&source); e != nil {
			t.Fatal(e)
		}
		return source
	}
	invoice := insert(closeID, "invoice.pdf", "--- Page 1 · PDF text ---\nExample insurance costs USD 1200. Coverage runs 2026-09-01 through 2027-08-31.\n")
	insert(other, "private.txt", "FOREIGN_CLIENT_CONTENT_MUST_NOT_LEAVE")
	tx, _ := db.Begin()
	job, err := preparation.StartIntake(context.Background(), tx, closeID, invoice, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if tx.Commit() != nil {
		t.Fatal("Cannot start fixture")
	}
	token := strings.Repeat("a", 32)
	h := apiWithStore(db, token)
	base := "/v1/closes/" + closeID
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	read := func() (int, string) {
		t.Helper()
		var revision int
		var state string
		if e := db.QueryRow(`SELECT i.revision,j.state FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.id=$1`, job).Scan(&revision, &state); e != nil {
			t.Fatal(e)
		}
		return revision, state
	}
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	calls := 0
	answerID := ""
	drift := false
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Host != "api.openai.com" || strings.Contains(string(raw), "FOREIGN_CLIENT_CONTENT_MUST_NOT_LEAVE") {
			t.Fatal("Provider boundary violated")
		}
		_, state := read()
		if state != "interpreting" {
			t.Fatal("Agent has no durable running claim")
		}
		sources := []any{map[string]string{"id": invoice, "role": "invoice"}}
		facts := []any{}
		for field, value := range map[string]string{"amount": "1200.00", "service_start": "2026-09-01", "service_end": "2027-08-31", "currency": "USD"} {
			facts = append(facts, map[string]string{"field": field, "value": value, "source_id": invoice, "quote": "Example insurance costs USD 1200. Coverage runs 2026-09-01 through 2027-08-31."})
		}
		questions := []string{"Which expense and prepaid accounts apply to this invoice?"}
		if answerID != "" {
			if !strings.Contains(string(raw), "Use Insurance expense and Prepaid insurance.") {
				t.Fatal("Answer was not supplied on resume")
			}
			sources = append(sources, map[string]string{"id": answerID, "role": "support"})
			questions = []string{}
			for field, value := range map[string]string{"expense_account": "Insurance expense", "prepaid_account": "Prepaid insurance"} {
				facts = append(facts, map[string]string{"field": field, "value": value, "source_id": answerID, "quote": "Use Insurance expense and Prepaid insurance."})
			}
		}
		output, _ := json.Marshal(map[string]any{"kind": "prepaid", "summary": "Insurance prepaid schedule", "questions": questions, "sources": sources, "facts": facts})
		if drift {
			insert(closeID, "new-evidence.txt", "New context arrived while the model was interpreting.")
			drift = false
		}
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	process := func() {
		t.Helper()
		if worked, e := preparation.ProcessIntake(context.Background(), db); e != nil || !worked {
			t.Fatal("Intake failed", e)
		}
	}
	usage := func() (n int) {
		t.Helper()
		if e := db.QueryRow(`SELECT COALESCE((SELECT calls FROM model_calls WHERE organization_id='local-development' AND day=(now() AT TIME ZONE 'UTC')::date),0)`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	// At the firm's daily limit the job waits, uncounted and unclaimed, instead of calling the model.
	if _, err = db.Exec(`INSERT INTO model_calls(organization_id,day,calls) VALUES('local-development',(now() AT TIME ZONE 'UTC')::date,1) ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`UPDATE organizations SET max_model_calls_per_day=2000 WHERE id='local-development'`)
	if _, err = db.Exec(`UPDATE organizations SET max_model_calls_per_day=$1 WHERE id='local-development'`, usage()); err != nil {
		t.Fatal(err)
	}
	spent := usage()
	process()
	var attempts int
	var message string
	var retry bool
	if err = db.QueryRow(`SELECT i.agent_attempts,j.error,i.agent_retry_at>now() FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.id=$1`, job).Scan(&attempts, &message, &retry); err != nil {
		t.Fatal(err)
	}
	if _, state := read(); state != "waiting_evidence" || attempts != 0 || !retry || !strings.Contains(message, "daily model call limit") || calls != 0 || usage() != spent {
		t.Fatalf("a job past the limit waits without a call or an attempt: %s %d %v %q calls=%d", state, attempts, retry, message, calls)
	}
	if worked, e := preparation.ProcessIntake(context.Background(), db); e != nil || worked {
		t.Fatal("a job waiting for the limit is not claimed again before its retry time", e)
	}
	if _, err = db.Exec(`UPDATE organizations SET max_model_calls_per_day=2000 WHERE id='local-development'`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE preparation_intakes SET agent_retry_at=NULL WHERE job_id=$1`, job); err != nil {
		t.Fatal(err)
	}
	process()
	revision, state := read()
	if state != "needs_confirmation" {
		t.Fatal(state)
	}
	if calls != 1 || usage() != spent+1 {
		t.Fatalf("a small inventory costs one counted call: calls=%d usage %d → %d", calls, spent, usage())
	}
	input := map[string]any{"amount": "1200.00", "service_start": "2026-09-01", "service_end": "2027-08-31", "currency": "USD", "basis": "accrual", "expense_account": "Insurance expense", "prepaid_account": "Prepaid insurance", "method": "equal_full_months_v1", "method_confirmed": true, "booking_confirmed": true}
	action := base + "/work/" + job
	call("POST", action+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true, "input": input}, 409)
	call("POST", action+"/answer", map[string]any{"revision": revision, "body": "Use Insurance expense and Prepaid insurance."}, 200)
	if err = db.QueryRow(`SELECT source_id::text FROM preparation_answers WHERE job_id=$1`, job).Scan(&answerID); err != nil {
		t.Fatal(err)
	}
	// Evidence arrives during a network call: the result must not become current.
	drift = true
	process()
	revision, state = read()
	if state != "waiting_evidence" {
		t.Fatal("Stale model output published", state)
	}
	process()
	revision, state = read()
	if state != "needs_confirmation" || calls != 3 {
		t.Fatal("Same job did not resume", state, calls)
	}
	call("POST", action+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true, "input": input}, 409)
	call("POST", action+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true, "inventory_confirmed": true, "input": input}, 200)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal(e)
	}
	_, state = read()
	if state != "completed" {
		t.Fatal("Agent preparation did not finish", state)
	}
	var amount string
	if err = db.QueryRow(`SELECT paper->>'period_expense' FROM workpapers WHERE close_id=$1`, closeID).Scan(&amount); err != nil || amount != "100.00" {
		t.Fatal(amount, err)
	}
	// Server-side filters apply before the page cap and include current blockers.
	blocked := call("GET", base+"/work?view=blocked", nil, 200)
	if strings.Contains(string(blocked), `"id":"`+job+`"`) {
		t.Fatal("Clean prepared work marked blocked")
	}
	if !strings.Contains(string(call("GET", base+"/work?view=completed&client=Agent", nil, 200)), `"id":"`+job+`"`) {
		t.Fatal("Completed work missing from filtered inbox")
	}
	// The agent selects both CSVs; confirmation loads transactions from evidence,
	// never from model-generated transaction data.
	statement := insert(closeID, "statement.csv", "id,date,amount\ns1,2026-09-02,10.00\ns2,2026-09-03,-5.00\n")
	ledger := insert(closeID, "ledger.csv", "id,date,amount\nl1,2026-09-02,10.00\nl2,2026-09-03,-5.00\n")
	metadata := insert(closeID, "balances.txt", "Cash USD: both opening balances 100.00, both closing balances 105.00; expected pages 1, observed pages 1.")
	tx, _ = db.Begin()
	job, err = preparation.StartIntake(context.Background(), tx, closeID, statement, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		facts := []any{}
		for field, value := range map[string]string{"account": "Cash", "currency": "USD", "statement_opening": "100.00", "statement_closing": "105.00", "ledger_opening": "100.00", "ledger_closing": "105.00", "expected_pages": "1", "pages": "1"} {
			facts = append(facts, map[string]string{"field": field, "value": value, "source_id": metadata, "quote": "Cash USD: both opening balances 100.00, both closing balances 105.00; expected pages 1, observed pages 1."})
		}
		output, _ := json.Marshal(map[string]any{"kind": "reconciliation", "summary": "Reconcile Cash", "questions": []string{}, "sources": []any{map[string]string{"id": statement, "role": "statement"}, map[string]string{"id": ledger, "role": "ledger"}, map[string]string{"id": metadata, "role": "support"}}, "facts": facts})
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	process()
	revision, state = read()
	if state != "needs_confirmation" {
		t.Fatal(state)
	}
	call("POST", base+"/work/"+job+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true, "inventory_confirmed": true, "input": map[string]any{"account": "Cash", "currency": "USD", "statement_opening": "100.00", "statement_closing": "105.00", "ledger_opening": "100.00", "ledger_closing": "105.00", "expected_pages": 1, "pages": []int{1}, "policy_confirmed": true}}, 200)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal(e)
	}
	_, state = read()
	var matches int
	if err = db.QueryRow("SELECT jsonb_array_length(paper->'matches') FROM workpapers WHERE close_id=$1 AND kind='reconciliation'", closeID).Scan(&matches); err != nil || matches != 2 || state != "completed" {
		t.Fatal("Selected CSV reconciliation did not prepare", state, matches, err)
	}

}

func TestAgentProposedDepreciationContinuesThroughItsForm(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	var closeID, invoice, ledger string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES('Depreciation agent fixture','2026-09') RETURNING id::text`).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_plans", "preparation_jobs", "evidence", "client_closes"} {
			column := "close_id"
			if table == "client_closes" {
				column = "id"
			}
			db.Exec("DELETE FROM "+table+" WHERE "+column+"=$1", closeID)
		}
	}()
	quote := "Laptop, USD 3600.00, placed in service 2026-09-01."
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'laptop.txt','laptop-invoice',$2,'fixture') RETURNING id::text`, closeID, quote).Scan(&invoice); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger.txt','laptop-ledger','Accumulated depreciation 0.00','fixture') RETURNING id::text`, closeID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	job, err := preparation.StartIntake(context.Background(), tx, closeID, invoice, "fixture")
	if err != nil || tx.Commit() != nil {
		t.Fatal("Cannot start fixture", err)
	}
	originalTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = originalTransport }()
	http.DefaultTransport = modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		output, _ := json.Marshal(map[string]any{"kind": "depreciation", "summary": "Laptop depreciation", "questions": []string{}, "sources": []any{map[string]string{"id": invoice, "role": "invoice"}, map[string]string{"id": ledger, "role": "support"}}, "facts": []any{map[string]string{"field": "amount", "value": "3600.00", "source_id": invoice, "quote": quote}, map[string]string{"field": "service_start", "value": "2026-09-01", "source_id": invoice, "quote": quote}}})
		body, _ := json.Marshal(map[string]any{"id": "mock-response", "status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(output)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	if worked, e := preparation.ProcessIntake(context.Background(), db); e != nil || !worked {
		t.Fatal("Intake failed", e)
	}
	var revision int
	var state, kind string
	if err = db.QueryRow(`SELECT i.revision,j.state,i.proposal->>'kind' FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.id::text=$1`, job).Scan(&revision, &state, &kind); err != nil || state != "needs_confirmation" || kind != "depreciation" {
		t.Fatalf("the agent proposes depreciation for review: %s %s %v", state, kind, err)
	}
	token := strings.Repeat("a", 32)
	h := apiWithStore(db, token)
	call := func(schedule string, want int) {
		t.Helper()
		input := map[string]any{"entity": "Depreciation agent fixture", "source_reference": "laptop.txt", "amount": "3600.00", "service_start": "2026-09-01", "service_end": "2029-08-31", "close_period": "2026-09", "currency": "USD", "basis": "accrual", "expense_account": "Depreciation expense", "prepaid_account": "Accumulated depreciation", "schedule": schedule, "booked_balance": "0.00", "booking_reference": "September ledger", "method": "equal_full_months_v1", "method_confirmed": true, "booking_confirmed": true, "examples_confirmed": true}
		raw, _ := json.Marshal(map[string]any{"kind": "prepaid", "input": input, "work_id": job, "work_revision": revision, "sources": []preparation.Source{{ID: invoice, Role: "invoice", Location: "line 1"}, {ID: ledger, Role: "support", Location: "line 1"}}})
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/preparations", bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", schedule, w.Code, w.Body.String())
		}
	}
	call("deferred_revenue", 409)
	call("depreciation", 200)
	if err = db.QueryRow(`SELECT state FROM preparation_jobs WHERE id::text=$1`, job).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("the confirmed depreciation continues the agent's job: %s %v", state, err)
	}
}
