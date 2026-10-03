package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/loans"
	"close.local/backend/internal/preparation"
)

func TestPriorScheduleRowsBecomeScheduleOnlyDrafts(t *testing.T) {
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
	entity := "Prior schedule " + rand.Text()
	defer func() {
		for _, q := range []string{`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM preparation_intakes WHERE job_id IN (SELECT j.id FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.entity=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `UPDATE evidence SET superseded_by=NULL WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM client_closes WHERE entity=$1`} {
			db.Exec(q, entity)
		}
	}()
	token := strings.Repeat("s", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var opening struct{ ID string }
	must(json.Unmarshal(call("POST", "/v1/closes", map[string]string{"entity": entity, "period": "2026-08"}, 200), &opening))

	type row struct{ label, amount, start, end, expense, prepaid, remaining string }
	insurance := row{"Insurance", "1200.00", "2026-01-01", "2026-12-31", "Insurance expense", "Prepaid insurance", "400.00"}
	software := row{"Software", "2400.00", "2026-03-01", "2027-02-28", "Software expense", "Prepaid software", "1200.00"}
	// intake stands in for the agent's reading of a schedule document: one line per row, every fact cited inside it.
	intake := func(supersedes string, rows ...row) (job, evidence string) {
		t.Helper()
		content := "Prior schedule of prepaid expenses at 2026-08-31 (USD)\n"
		p := preparation.Proposal{Kind: "prepaid", Summary: "Prior prepaid schedule", Facts: []preparation.Fact{}}
		for i, r := range rows {
			line := strings.Join([]string{r.label, r.amount, r.start, r.end, r.expense, r.prepaid, r.remaining}, ",")
			content += line + "\n"
			sl := preparation.ScheduleLine{Line: i + 1, Row: line}
			for field, value := range map[string]string{"label": r.label, "amount": r.amount, "service_start": r.start, "service_end": r.end, "currency": "USD", "expense_account": r.expense, "prepaid_account": r.prepaid, "remaining": r.remaining} {
				sl.Facts = append(sl.Facts, preparation.Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value})
			}
			p.Lines = append(p.Lines, sl)
		}
		var digest string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'schedule.csv',$2,$3,'fixture') RETURNING id::text,digest`, opening.ID, rand.Text(), content).Scan(&evidence, &digest))
		if supersedes != "" {
			_, e := db.Exec(`UPDATE evidence SET superseded_by=$1::bigint WHERE id::text=$2`, evidence, supersedes)
			must(e)
		}
		p.Sources = []preparation.Source{{ID: evidence, Digest: digest, Name: "schedule.csv", Role: "invoice", Location: "Full extracted text"}}
		proposal, _ := json.Marshal(p)
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1::bigint,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, opening.ID, "intake:"+evidence).Scan(&job))
		_, e := db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1::bigint,$2::bigint,$3)`, job, evidence, string(proposal))
		must(e)
		return job, evidence
	}
	importing := func(job string, lines []map[string]any, want int) string {
		t.Helper()
		var revision int
		must(db.QueryRow(`SELECT revision FROM preparation_intakes WHERE job_id::text=$1`, job).Scan(&revision))
		return string(call("POST", "/v1/closes/"+opening.ID+"/work/"+job+"/import_schedule", map[string]any{"revision": revision, "client_confirmed": true, "roster_confirmed": true, "lines": lines}, want))
	}
	both := func(second map[string]any) []map[string]any {
		return []map[string]any{{"line": 1, "decision": "import", "method": "equal_full_months_v1"}, second}
	}

	stated := software
	stated.remaining = "1400.00"
	job, first := intake("", insurance, stated)
	var revision int
	must(db.QueryRow(`SELECT revision FROM preparation_intakes WHERE job_id::text=$1`, job).Scan(&revision))
	call("POST", "/v1/closes/"+opening.ID+"/work/"+job+"/confirm", map[string]any{"revision": revision, "facts_confirmed": true}, 409)
	call("POST", "/v1/closes/"+opening.ID+"/work/"+job+"/import_schedule", map[string]any{"revision": revision, "client_confirmed": true, "lines": both(map[string]any{"line": 2, "decision": "import", "method": "equal_full_months_v1"})}, 422)
	importing(job, []map[string]any{{"line": 1, "decision": "import", "method": "equal_full_months_v1"}}, 422)
	if got := importing(job, both(map[string]any{"line": 2, "decision": "import", "method": "equal_full_months_v1"}), 422); !strings.Contains(got, "Row 2") || !strings.Contains(got, "states 1400.00") || !strings.Contains(got, "gives 1200.00") {
		t.Fatalf("a row whose stated balance the calculation does not give is refused: %s", got)
	}
	_, err = db.Exec(`UPDATE client_closes SET takeover=false WHERE id::text=$1`, opening.ID)
	must(err)
	importing(job, both(map[string]any{"line": 2, "decision": "set_aside", "reason": "Expired licence, written off by the prior accountant"}), 409)
	_, err = db.Exec(`UPDATE client_closes SET takeover=true WHERE id::text=$1`, opening.ID)
	must(err)
	importing(job, both(map[string]any{"line": 2, "decision": "set_aside", "reason": "Expired licence, written off by the prior accountant"}), 200)
	var plans []preparation.Plan
	var raw []byte
	must(db.QueryRow(`SELECT plans FROM preparation_jobs WHERE id::text=$1`, job).Scan(&raw))
	must(json.Unmarshal(raw, &plans))
	if len(plans) != 1 || plans[0].Imported == nil || plans[0].Imported.Label != "Insurance" || plans[0].Imported.RootSourceID != first {
		t.Fatalf("the imported row is queued as a schedule line: %s", raw)
	}
	for range 5 {
		if worked, e := preparation.ProcessOne(t.Context(), db); e != nil || !worked {
			break
		}
	}
	var paperID, reference, balance string
	var proposal, imported []byte
	must(db.QueryRow(`SELECT id::text,paper->'input'->>'source_reference',paper->>'prepaid_balance',COALESCE(paper->'proposal','null'::jsonb),paper->'imported' FROM workpapers WHERE close_id::text=$1`, opening.ID).Scan(&paperID, &reference, &balance, &proposal, &imported))
	if reference != "Prior schedule #"+first+" · Insurance" || balance != "400.00" || string(proposal) != "null" || !strings.Contains(string(imported), `"label": "Insurance"`) {
		t.Fatalf("the draft is schedule only, named by the schedule and its row: %s %s %s %s", reference, balance, proposal, imported)
	}
	// The ordinary revision form does not revise an imported row; a corrected schedule does.
	revise := map[string]any{"kind": "prepaid", "revises": paperID, "input": map[string]any{}, "sources": []map[string]string{{"id": first, "role": "invoice", "location": "Full extracted text"}}}
	if got := string(call("POST", "/v1/closes/"+opening.ID+"/preparations", revise, 422)); !strings.Contains(got, "uploading the corrected schedule") {
		t.Fatalf("the revision form refuses an imported row: %s", got)
	}

	corrected, _ := intake(first, software)
	importing(corrected, []map[string]any{{"line": 1, "decision": "import", "method": "equal_full_months_v1"}}, 409)
	corrected, replacement := intake(first, insurance, software)
	importing(corrected, both(map[string]any{"line": 2, "decision": "import", "method": "equal_full_months_v1"})[1:], 422)
	if got := importing(corrected, []map[string]any{{"line": 1, "decision": "set_aside", "reason": "No longer prepaid"}, {"line": 2, "decision": "import", "method": "equal_full_months_v1"}}, 409); !strings.Contains(got, "earlier version") {
		t.Fatalf("a row imported before cannot be set aside by a correction: %s", got)
	}
	importing(corrected, both(map[string]any{"line": 2, "decision": "import", "method": "equal_full_months_v1"}), 200)
	must(db.QueryRow(`SELECT plans FROM preparation_jobs WHERE id::text=$1`, corrected).Scan(&raw))
	must(json.Unmarshal(raw, &plans))
	if len(plans) != 2 || plans[0].Revises != paperID || plans[1].Revises != "" || plans[0].Imported.RootSourceID != first {
		t.Fatalf("the corrected schedule revises the row imported before and adds the new one: %s", raw)
	}
	// A schedule replacing two earlier documents is not guessed between them.
	var other string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'other.csv',$2,'Other schedule','fixture') RETURNING id::text`, opening.ID, rand.Text()).Scan(&other))
	combined, combo := intake(replacement, insurance, software)
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$1::bigint WHERE id::text=$2`, combo, other)
	must(err)
	if got := importing(combined, both(map[string]any{"line": 2, "decision": "import", "method": "equal_full_months_v1"}), 409); !strings.Contains(got, "one schedule at a time") {
		t.Fatalf("a schedule replacing two documents is refused: %s", got)
	}
}

func TestAPriorLoanScheduleBecomesOneScheduleOnlyLoanDraft(t *testing.T) {
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
	entity := "Prior loan " + rand.Text()
	defer func() {
		for _, q := range []string{`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM preparation_intakes WHERE job_id IN (SELECT j.id FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id WHERE c.entity=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, `DELETE FROM client_closes WHERE entity=$1`} {
			db.Exec(q, entity)
		}
	}()
	token := strings.Repeat("l", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var opening struct{ ID string }
	must(json.Unmarshal(call("POST", "/v1/closes", map[string]string{"entity": entity, "period": "2026-08"}, 200), &opening))
	fact := func(field, value string) preparation.Fact {
		return preparation.Fact{Field: field, Label: strings.ReplaceAll(field, "_", " "), Value: value}
	}
	p := preparation.Proposal{Kind: "loan", Summary: "Prior loan schedule", Facts: []preparation.Fact{fact("lender", "First Bank"), fact("agreement_reference", "Note 2024-1"), fact("annual_rate", "6.000"), fact("currency", "USD"), fact("loan_account", "Term loan"), fact("interest_account", "Interest expense")}}
	content := "First Bank schedule\n"
	for i, row := range [][4]string{{"2026-07-15", "50.00", "0.00", "10000.00"}, {"2026-08-15", "50.00", "500.00", "9500.00"}} {
		content += strings.Join(row[:], ",") + "\n"
		p.Lines = append(p.Lines, preparation.ScheduleLine{Line: i + 1, Row: strings.Join(row[:], ","), Facts: []preparation.Fact{fact("date", row[0]), fact("interest", row[1]), fact("principal", row[2]), fact("balance", row[3])}})
	}
	var evidence, digest, job string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1::bigint,'loan.csv',$2,$3,'fixture') RETURNING id::text,digest`, opening.ID, rand.Text(), content).Scan(&evidence, &digest))
	p.Sources = []preparation.Source{{ID: evidence, Digest: digest, Name: "loan.csv", Role: "invoice", Location: "Full extracted text"}}
	proposal, _ := json.Marshal(p)
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1::bigint,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, opening.ID, "intake:"+evidence).Scan(&job))
	_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1::bigint,$2::bigint,$3)`, job, evidence, string(proposal))
	must(err)
	importing := func(body map[string]any, want int) string {
		t.Helper()
		var revision int
		must(db.QueryRow(`SELECT revision FROM preparation_intakes WHERE job_id::text=$1`, job).Scan(&revision))
		body["revision"], body["client_confirmed"], body["roster_confirmed"] = revision, true, true
		return string(call("POST", "/v1/closes/"+opening.ID+"/work/"+job+"/import_schedule", body, want))
	}
	importing(map[string]any{"day_count": "monthly"}, 422)
	if got := importing(map[string]any{"day_count": "actual_365", "reclass_current_portion": true, "current_portion_account": "Loan, current portion"}, 422); !strings.Contains(got, "not the 12 months after") {
		t.Fatalf("a schedule without the next year's payments cannot state a current portion: %s", got)
	}
	// A loan draft needs the firm's approved loan procedure, as any loan draft does.
	if got := importing(map[string]any{"day_count": "actual_365"}, 409); !strings.Contains(got, "approved loan procedure") {
		t.Fatalf("a loan without the firm's loan procedure is refused: %s", got)
	}
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) SELECT organization_id,entity,'loan','Synthetic loan procedure',true,'Synthetic approval','fixture',$2 FROM client_closes WHERE id::text=$1`, opening.ID, loans.Method)
	must(err)
	defer db.Exec(`DELETE FROM procedure_versions WHERE client_entity=$1`, entity)
	importing(map[string]any{"day_count": "actual_365"}, 200)
	for range 5 {
		if worked, e := preparation.ProcessOne(t.Context(), db); e != nil || !worked {
			break
		}
	}
	var kind, closing, reference, imported string
	var proposals int
	must(db.QueryRow(`SELECT kind,paper->'input'->>'closing_principal',paper->'input'->>'source_reference',jsonb_array_length(COALESCE(paper->'proposals','[]')),paper->'imported'->>'label' FROM workpapers WHERE close_id::text=$1`, opening.ID).Scan(&kind, &closing, &reference, &proposals, &imported))
	if kind != "loan" || closing != "9500.00" || reference != "Prior schedule #"+evidence+" · First Bank" || proposals != 0 || imported != "First Bank" {
		t.Fatalf("the loan is one schedule-only draft named by its schedule: %s %s %s %d %s", kind, closing, reference, proposals, imported)
	}
}
