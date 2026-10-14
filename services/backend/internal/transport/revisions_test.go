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

	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

func TestAReturnedDraftIsRevisedFromTheNote(t *testing.T) {
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
	org := "org_Revise" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Revision fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_plans WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	var closeID, goal string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&closeID))
	evidence := func(name, content string) preparation.Source {
		var s preparation.Source
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text,name,digest`, closeID, name, preparation.Digest([]byte(content)), content).Scan(&s.ID, &s.Name, &s.Digest))
		return s
	}
	invoice := evidence("invoice.txt", "Alpha\nInvoice INV-7 from Northwind Software\nAnnual subscription 1,200.00\nService period June 1, 2026 to May 31, 2027")
	contract := evidence("contract.txt", "Northwind Software order form for Alpha\nService begins July 1, 2026 and runs twelve months")
	invoice.Role, invoice.Location = "invoice", "line 3"
	draft := func() string {
		input := prepaids.Input{Entity: "Alpha", SourceReference: "INV-7", Amount: "1200.00", ServiceStart: "2026-06-01", ServiceEnd: "2027-05-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software", PrepaidAccount: "Prepaid expenses", BookedBalance: "1200.00", BookingReference: "September ledger", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
		raw, _ := json.Marshal(input)
		body, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: raw, Sources: []preparation.Source{invoice}}, "Alpha", "2026-09")
		must(err)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, preparation.Digest(append(body, rand.Text()...)), body).Scan(&id))
		return id
	}
	returnDraft := func(id, note string) {
		_, err := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,decision,note,actor,paper_digest) SELECT id,'changes_requested',$2,'Reviewer A',digest FROM workpapers WHERE id::text=$1`, id, note)
		must(err)
	}
	must(db.QueryRow(`INSERT INTO close_goals(close_id,author) VALUES($1,'user_a') RETURNING id::text`, closeID).Scan(&goal))
	reply := `{"changes":[{"field":"service_start","value":"2026-07-01","source_id":"` + contract.ID + `","quote":"Service begins July 1, 2026"}],"question":""}`
	calls := 0
	client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(raw), "Use the contract's start date") && !strings.Contains(string(raw), "Fix the dates") {
			t.Fatal("the reviewer's note reaches the model")
		}
		body, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"content": []any{map[string]string{"type": "output_text", "text": reply}}}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{}}, nil
	})}
	models, err := llm.New(llm.Config{HTTP: client, APIKey: "synthetic-key", Model: "synthetic-model"})
	must(err)
	ctx := llm.WithClient(context.Background(), models)
	roles := map[string]reviewer{"user_a": {review: true}}
	events := func() string {
		var raw string
		must(db.QueryRow(`SELECT events::text FROM close_goals WHERE id::text=$1`, goal).Scan(&raw))
		return raw
	}

	// The note names the contract's date: the revision cites the contract, changes the start and is queued for the worker.
	first := draft()
	returnDraft(first, "Use the contract's start date.")
	must(reviseReturned(ctx, db, goal, roles))
	var plans string
	must(db.QueryRow(`SELECT plans::text FROM preparation_jobs WHERE close_id::text=$1 AND state='queued'`, closeID).Scan(&plans))
	var queued []preparation.Plan
	must(json.Unmarshal([]byte(plans), &queued))
	var input prepaids.Input
	json.Unmarshal(queued[0].Input, &input)
	if queued[0].Revises != first || input.ServiceStart != "2026-07-01" || input.ServiceEnd != "2027-05-31" || len(queued[0].Sources) != 2 || queued[0].Sources[1].ID != contract.ID || queued[0].Sources[1].Role != "support" {
		t.Fatalf("the revision changes the start from the contract and nothing else: %s", plans)
	}
	if !strings.Contains(events(), "service start from 2026-06-01 to 2026-07-01 (from contract.txt)") {
		t.Fatalf("the goal says what changed and why: %s", events())
	}
	// The return is taken up once: another look neither calls the model nor queues again.
	must(reviseReturned(ctx, db, goal, roles))
	var jobs int
	must(db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id::text=$1`, closeID).Scan(&jobs))
	if jobs != 1 || calls != 1 {
		t.Fatalf("one return, one revision: %d jobs, %d model calls", jobs, calls)
	}
	// The worker recalculates it as a revision that comes back for review.
	must(preparationStep(ctx, db, closeID))
	var revised, state string
	must(db.QueryRow(`SELECT w.id::text,r.review_state FROM workpapers w JOIN workpaper_review_state r ON r.id=w.id WHERE w.close_id::text=$1 AND w.paper->>'revises'=$2`, closeID, first).Scan(&revised, &state))
	if state != "pending" {
		t.Fatalf("the revised draft waits for review: %s", state)
	}

	// A value no source or note shows becomes one question, and nothing is queued.
	_, err = db.Exec(`DELETE FROM preparation_jobs WHERE close_id::text=$1`, closeID)
	must(err)
	second := draft()
	returnDraft(second, "Fix the dates.")
	reply = `{"changes":[{"field":"service_end","value":"2027-06-30","source_id":"` + invoice.ID + `","quote":"Service period June 1, 2026 to May 31, 2027"}],"question":""}`
	must(reviseReturned(ctx, db, goal, roles))
	must(db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id::text=$1`, closeID).Scan(&jobs))
	if jobs != 0 || !strings.Contains(events(), "Before I revise it: I read the note as changing service end to 2027-06-30") {
		t.Fatalf("an unsupported value is a question, not a revision: %d jobs, %s", jobs, events())
	}
}

// preparationStep runs the worker's preparation queue for one close until it is empty.
func preparationStep(ctx context.Context, db *sql.DB, closeID string) error {
	var id int64
	if err := db.QueryRow(`SELECT id FROM client_closes WHERE id::text=$1`, closeID).Scan(&id); err != nil {
		return err
	}
	ctx = preparation.WithinCloses(ctx, []int64{id})
	for {
		worked, err := preparation.ProcessOne(ctx, db)
		if err != nil || !worked {
			return err
		}
	}
}
