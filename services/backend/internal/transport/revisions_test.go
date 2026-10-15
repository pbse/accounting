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
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/llm"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
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
	ledger := evidence("ledger.txt", "Prepaid expenses, September 2026: opening 1,200.00; balance 1,200.00")
	invoice.Role, invoice.Location = "invoice", "line 3"
	ledger.Role, ledger.Location = "support", "line 1"
	draft := func() string {
		input := prepaids.Input{Entity: "Alpha", SourceReference: "INV-7", Amount: "1200.00", ServiceStart: "2026-06-01", ServiceEnd: "2027-05-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software", PrepaidAccount: "Prepaid expenses", BookedBalance: "1200.00", BookingReference: "September ledger", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true}
		raw, _ := json.Marshal(input)
		body, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: raw, Sources: []preparation.Source{invoice, ledger}}, "Alpha", "2026-09")
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
	calls, sent := 0, ""
	// during runs while the model reads, as a person's answer might arrive.
	var during func()
	client := &http.Client{Transport: modelRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		sent = string(raw)
		if during != nil {
			during()
			during = nil
		}
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
	cites := map[string]string{}
	for _, s := range queued[0].Sources {
		cites[s.ID] = s.Role
	}
	if queued[0].Revises != first || input.ServiceStart != "2026-07-01" || input.ServiceEnd != "2027-05-31" || len(queued[0].Sources) != 3 || cites[contract.ID] != "support" || cites[ledger.ID] != "support" || cites[invoice.ID] != "invoice" {
		t.Fatalf("the revision changes the start from the contract, cited beside the ledger page, and nothing else: %s", plans)
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
	// The question is what the goal says of that document, where the person sees what needs them.
	tx, err := db.BeginTx(context.Background(), nil)
	must(err)
	defer tx.Rollback()
	job, err := preparation.StartIntake(context.Background(), tx, closeID, invoice.ID, "fixture")
	must(err)
	_, err = tx.Exec(`UPDATE preparation_jobs SET state='completed',workpaper_ids=jsonb_build_array($2::text),finished_at=now() WHERE id::text=$1`, job, second)
	must(err)
	progress, err := readGoalProgress(context.Background(), tx, closeID, "review")
	must(err)
	asked := false
	for _, item := range progress.Items {
		asked = asked || slices.Contains(item.Drafts, second) && strings.Contains(item.Text, "Before I revise it:")
	}
	if !asked {
		t.Fatalf("the returned document's line carries the agent's question: %+v", progress.Items)
	}
	tx.Rollback()
	queuedEnd := func() string {
		t.Helper()
		var plans string
		must(db.QueryRow(`SELECT plans::text FROM preparation_jobs WHERE close_id::text=$1 AND state='queued' ORDER BY id DESC LIMIT 1`, closeID).Scan(&plans))
		var queued []preparation.Plan
		must(json.Unmarshal([]byte(plans), &queued))
		var input prepaids.Input
		json.Unmarshal(queued[0].Input, &input)
		return input.ServiceEnd
	}

	// The question waits: another look asks the model nothing. A document the close reads afterwards resumes the return,
	// offered first, and the revision follows without the draft being returned again.
	before := calls
	must(reviseReturned(ctx, db, goal, roles))
	if calls != before {
		t.Fatalf("a question waits for its answer or a new document: %d model calls", calls-before)
	}
	renewal := evidence("renewal.txt", "Northwind renewal letter for Alpha\nThe subscription now ends June 30, 2027")
	reply = `{"changes":[{"field":"service_end","value":"2027-06-30","source_id":"` + renewal.ID + `","quote":"The subscription now ends June 30, 2027"}],"question":""}`
	must(reviseReturned(ctx, db, goal, roles))
	if calls != before+1 || !strings.Contains(sent, `\"asked\":[\"I read the note as changing service end`) || !strings.Contains(sent, "renewal.txt") || queuedEnd() != "2027-06-30" {
		t.Fatalf("the new document resumes the return with the question asked: %d calls, end %s", calls-before, queuedEnd())
	}
	_, err = db.Exec(`DELETE FROM preparation_jobs WHERE close_id::text=$1`, closeID)
	must(err)

	// Answering the question on the draft resumes it the same way.
	third := draft()
	returnDraft(third, "Fix the dates.")
	reply = `{"changes":[],"question":"Which end date should it use?"}`
	must(reviseReturned(ctx, db, goal, roles))
	var review string
	must(db.QueryRow(`SELECT review_id::text FROM workpaper_review_state WHERE id::text=$1`, third).Scan(&review))
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org, Permissions: []string{"workpapers:review", "clients:all"}}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var shown struct{ Clarification string }
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/workpapers/"+third+"/reviews", nil, 200), &shown))
	if shown.Clarification != "Which end date should it use?" {
		t.Fatalf("the draft's review shows the question waiting for an answer: %+v", shown)
	}
	call("POST", "/v1/closes/"+closeID+"/workpapers/"+third+"/clarification", map[string]string{"review_id": review, "answer": "Use June 30, 2027 as the end date."}, 200)
	call("POST", "/v1/closes/"+closeID+"/workpapers/"+third+"/clarification", map[string]string{"review_id": review, "answer": "Again"}, 409)
	reply = `{"changes":[{"field":"service_end","value":"2027-06-30","source_id":"note","quote":"June 30, 2027"}],"question":""}`
	// A further answer arriving while the model reads the first is not lost: that reading is set aside and the next look
	// reads both.
	during = func() {
		_, err := db.Exec(`UPDATE close_goals SET events=events||jsonb_build_array(jsonb_build_object('actor','user_a','text','Answered','answers',$2::text,'answer','It renews on July 1.','at',now())) WHERE id::text=$1`, goal, review)
		must(err)
	}
	must(reviseReturned(ctx, db, goal, roles))
	var queuedCount int
	must(db.QueryRow(`SELECT count(*) FROM preparation_jobs WHERE close_id::text=$1`, closeID).Scan(&queuedCount))
	if queuedCount != 0 {
		t.Fatal("a reading made before the latest answer settles nothing")
	}
	must(reviseReturned(ctx, db, goal, roles))
	if !strings.Contains(sent, "The reviewer answered: Use June 30, 2027 as the end date.") || !strings.Contains(sent, "It renews on July 1.") || queuedEnd() != "2027-06-30" {
		t.Fatalf("the answers resume the return: end %s", queuedEnd())
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
