package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestTheGoalDraftsOneClientRequestAPersonApproves(t *testing.T) {
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
	entity := "Request client " + rand.Text()
	var closeID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, closeID)
		for _, table := range []string{"request_drafts", "preparation_jobs", "evidence"} {
			db.Exec(`DELETE FROM `+table+` WHERE close_id=$1`, closeID)
		}
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	// A document whose reading asks the client for something and the firm for a confirmation of its own; the close has
	// no ledger capture.
	var source, job string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'insurance invoice.pdf',$2,'Invoice','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&source))
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&job))
	_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,'{"kind":"prepaid","questions":["Please send the signed policy schedule showing the coverage dates.","Confirm the amount from the source evidence."]}')`, job, source)
	must(err)

	draft := func() bool {
		t.Helper()
		tx, err := db.BeginTx(context.Background(), nil)
		must(err)
		defer tx.Rollback()
		drafted, err := draftGoalRequest(context.Background(), tx, goalRow{id: "2147480000", close: closeID, author: "user_a"})
		must(err)
		must(tx.Commit())
		return drafted
	}
	token := strings.Repeat("r", 32)
	handler := apiWithStore(db, token)
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
	// Drafted on demand (chat, an agent or the Requests tab), the request is the one the goal would draft: the goal
	// finds it and drafts no other.
	var drafted struct {
		Proposal *struct {
			ID        string
			Questions []proposedQuestion
		}
		Kept []string
	}
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/proposed", map[string]any{}, 200), &drafted))
	if drafted.Proposal == nil || len(drafted.Proposal.Questions) != 1 || drafted.Kept == nil {
		t.Fatalf("drafting on demand drafts the client's question: %+v", drafted)
	}
	if draft() {
		t.Fatal("the goal drafts no second request for the same questions")
	}
	var proposed struct {
		Proposal *struct {
			ID, Entity, Period string
			Questions          []proposedQuestion
		}
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/requests/proposed", nil, 200), &proposed))
	if p := proposed.Proposal; p == nil || p.Entity != entity || p.Period != "2026-09" || len(p.Questions) != 1 || p.Questions[0].Document != "insurance invoice.pdf" || !strings.Contains(p.Questions[0].Question, "signed policy schedule") {
		t.Fatalf("the proposal shows the client, month and the client's question only: %+v", proposed.Proposal)
	}
	if proposed.Proposal.ID != drafted.Proposal.ID {
		t.Fatalf("every entry point shows the same draft: %s %s", proposed.Proposal.ID, drafted.Proposal.ID)
	}
	// A person adds a question of their own: it is asked after the drafted ones, and the goal keeps it.
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/proposed", map[string]any{"questions": []string{"Did the policy  change\n mid-year?"}}, 200), &drafted))
	if p := drafted.Proposal; p == nil || len(p.Questions) != 2 || p.Questions[1] != (proposedQuestion{Question: "Did the policy change mid-year?"}) {
		t.Fatalf("the firm's own question follows the drafted ones: %+v", drafted.Proposal)
	}
	if draft() {
		t.Fatal("the goal keeps the firm's own question on the draft")
	}
	// A retried addition asks the question once.
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/proposed", map[string]any{"questions": []string{"did the policy change mid-year?"}}, 200), &drafted))
	if len(drafted.Proposal.Questions) != 2 {
		t.Fatalf("a retried question is not asked twice: %+v", drafted.Proposal.Questions)
	}
	call("POST", "/v1/closes/"+closeID+"/requests/proposed", map[string]any{"questions": []string{" "}}, 422)
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/requests/proposed", nil, 200), &proposed))
	call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", map[string]any{"recipient": "owner@example.test"}, 422)
	// The person may reword a question before approving it, but not drop one or move it to another document.
	call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", map[string]any{"recipient": "owner@example.test", "confirmed": true, "questions": []proposedQuestion{}}, 422)
	call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", map[string]any{"recipient": "owner@example.test", "confirmed": true, "questions": []proposedQuestion{{Document: "another.pdf", Question: "Send it"}}}, 422)
	var opened struct{ ID, Link_token string }
	reworded := []proposedQuestion{{Document: "insurance invoice.pdf", Question: "Please send the signed\npolicy schedule  for this year."}, {Question: "Did the policy change mid-year?"}}
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", map[string]any{"recipient": "owner@example.test", "confirmed": true, "questions": reworded}, 200), &opened))
	var state, body string
	must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state))
	must(db.QueryRow(`SELECT d.body FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id::text=$1`, opened.ID).Scan(&body))
	if state != "waiting_client" || opened.Link_token == "" || !slices.Equal(requestQuestions(body), []string{"insurance invoice.pdf: Please send the signed policy schedule for this year.", "Did the policy change mid-year?"}) {
		t.Fatalf("approving opens the request as worded, with a private link, and the document waits on the client: %s %+v %q", state, opened, body)
	}
	// Without a ledger capture the request is still current: it binds no ledger account.
	tx, err := db.BeginTx(context.Background(), nil)
	must(err)
	if e := preparation.FreshClientRequest(context.Background(), tx, closeID, opened.ID); e != nil {
		t.Fatalf("a request about documents needs no ledger capture: %v", e)
	}
	tx.Rollback()
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/requests/proposed", nil, 200), &proposed))
	if proposed.Proposal != nil {
		t.Fatalf("an approved draft is no longer proposed: %+v", proposed.Proposal)
	}
	call("POST", "/v1/closes/"+closeID+"/requests/"+opened.ID+"/approve", map[string]any{"recipient": "owner@example.test", "confirmed": true}, 409)
	// The close's Requests lists it: approved, not yet sent, unanswered, handled in the document's work.
	var approved []struct {
		ID, Recipient, Email, Answer, Job string
		SentAt                            *string `json:"sent_at"`
		Held                              bool
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/requests/approved", nil, 200), &approved))
	if len(approved) != 1 || approved[0].ID != opened.ID || approved[0].Recipient != "owner@example.test" || approved[0].SentAt != nil || approved[0].Email != "" || approved[0].Answer != "" || approved[0].Held || approved[0].Job != job {
		t.Fatalf("an approved request is listed with where it stands: %+v", approved)
	}
}

// The goal asks the client for what waits on the client (a document's question, the records behind accounts with no
// evidence, a statement's missing page), each in the client's words with what it unblocks, and keeps what needs the
// firm's judgment; approval binds the accounts and drafts as coverage requests do, and a live request is not asked again.
func TestTheGoalRoutesWhatWaitsOnInformation(t *testing.T) {
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
	ctx := context.Background()
	org := "org_Route" + rand.Text()
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Routing fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT d.id FROM request_drafts d JOIN client_closes c ON c.id=d.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM request_drafts WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM coverage_bindings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM quickbooks_connections WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`UPDATE evidence SET superseded_by=NULL WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	// The capture lists a prepaid account and a bank account with no evidence, and one the firm must review.
	var closeID, ledger, conn, snapshot, source, paper, goal string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger.txt','route-ledger','Synthetic ledger','fixture') RETURNING id::text`, closeID).Scan(&ledger))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&conn))
	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": ledger, "coverage": []map[string]string{
		{"account_id": "a", "name": "Prepaid insurance", "kind": "prepaid", "question": "Provide the policy invoice."},
		{"account_id": "b", "name": "Operating bank", "kind": "reconciliation", "question": "Provide the statement."},
		{"account_id": "c", "name": "Office supplies", "kind": "accountant_review", "question": "Review activity."}}})
	must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123','route-snapshot',$3,now(),'fixture') RETURNING id::text`, closeID, conn, string(payload)).Scan(&snapshot))
	// A document waits on a client question; a reconciliation draft lacks a page and has a bridge for the firm.
	tx, err := db.BeginTx(ctx, nil)
	must(err)
	must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt','route-invoice','Invoice','fixture') RETURNING id::text`, closeID).Scan(&source))
	job, err := preparation.StartIntake(ctx, tx, closeID, source, "fixture")
	must(err)
	must(tx.Commit())
	for _, q := range []string{`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","questions":["Which months does the policy cover?"],"facts":[]}' WHERE job_id::text=$1`,
		`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`} {
		_, err = db.Exec(q, job)
		must(err)
	}
	// A document once matched to the prepaid account was replaced since: it no longer answers the account.
	var old, replacement string
	tx, err = db.BeginTx(ctx, nil)
	must(err)
	must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'old-policy.txt','route-old','Old policy','fixture') RETURNING id::text`, closeID).Scan(&old))
	oldJob, err := preparation.StartIntake(ctx, tx, closeID, old, "fixture")
	must(err)
	must(tx.Commit())
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'new-policy.txt','route-new','New policy','fixture') RETURNING id::text`, closeID).Scan(&replacement))
	for _, q := range []string{`UPDATE preparation_intakes SET proposal='{"kind":"prepaid","questions":[],"facts":[{"field":"prepaid_account","value":"Prepaid insurance"}]}' WHERE job_id::text=$1`,
		`UPDATE preparation_jobs SET state='needs_confirmation' WHERE id::text=$1`} {
		_, err = db.Exec(q, oldJob)
		must(err)
	}
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2::bigint WHERE id::text=$1`, old, replacement)
	must(err)
	var statement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'operating-september.pdf','route-statement','Statement page 1','fixture') RETURNING id::text`, closeID).Scan(&statement))
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation','route-recon',$2) RETURNING id::text`, closeID,
		`{"input":{"account":"Operating bank"},"sources":[{"id":"`+statement+`","digest":"route-statement","name":"operating-september.pdf","role":"statement","location":"page 1"}],"blockers":["Statement page 2 is missing.","Opening bridge does not tie; inspect cited prior outstanding items."]}`).Scan(&paper))
	must(db.QueryRow(`INSERT INTO close_goals(close_id,author) VALUES($1,'user_a') RETURNING id::text`, closeID).Scan(&goal))
	draft := func() bool {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		must(err)
		defer tx.Rollback()
		drafted, err := draftGoalRequest(ctx, tx, goalRow{id: goal, close: closeID, author: "user_a"})
		must(err)
		must(tx.Commit())
		return drafted
	}
	if !draft() {
		t.Fatal("the goal drafts a request")
	}
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
	var proposed struct {
		Proposal *struct {
			ID        string
			Questions []proposedQuestion
			Kept      []string
		}
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/requests/proposed", nil, 200), &proposed))
	p := proposed.Proposal
	about := map[string]proposedQuestion{}
	for _, q := range p.Questions {
		about[q.Document] = q
	}
	// The bank account's reconciliation already reads its statement, so only the missing page is asked for it.
	if len(p.Questions) != 3 || about["invoice.txt"].Unblocks != "preparing invoice.txt" ||
		about["operating-september.pdf"].Question != "Please send page 2 of this statement; it is missing from what we have." || about["operating-september.pdf"].Unblocks != "the Operating bank reconciliation" ||
		about["Operating bank"].Question != "" || !strings.Contains(about["Prepaid insurance"].Question, "invoices or agreements behind Prepaid insurance") || about["Prepaid insurance"].Unblocks != "the Prepaid insurance schedule" {
		t.Fatalf("the client is asked for the document's answer, the missing page and the records of the account no draft reads, each with what it unblocks: %+v", p.Questions)
	}
	for _, q := range p.Questions {
		if strings.Contains(q.Question, "bridge") || strings.Contains(q.Question, "Provide") {
			t.Fatalf("a blocker's or the firm's own wording never reaches the client: %+v", q)
		}
	}
	if len(p.Kept) != 2 || !strings.HasPrefix(p.Kept[0], "Draft #"+paper+": Opening bridge") || !strings.HasPrefix(p.Kept[1], "Office supplies: review") {
		t.Fatalf("the firm's judgment stays with the firm: %+v", p.Kept)
	}
	// Approval binds the accounts on their capture and the draft whose page it asks for, as coverage requests do.
	var opened struct{ ID string }
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/"+p.ID+"/approve", map[string]any{"recipient": "owner@example.test", "confirmed": true}, 200), &opened))
	var scope, papers string
	must(db.QueryRow(`SELECT coverage_scope::text,approved_papers::text FROM work_requests WHERE id::text=$1`, opened.ID).Scan(&scope, &papers))
	if !strings.Contains(scope, `"snapshot_id": "`+snapshot+`"`) || !strings.Contains(scope, `"account_id": "a"`) || strings.Contains(scope, `"account_id": "b"`) || strings.Contains(scope, `"account_id": "c"`) || !strings.Contains(papers, `"`+paper+`"`) {
		t.Fatalf("the request binds the accounts it asks about, on their capture, and the draft it asks a page for: %s %s", scope, papers)
	}
	tx, err = db.BeginTx(ctx, nil)
	must(err)
	if e := preparation.FreshClientRequest(ctx, tx, closeID, opened.ID); e != nil {
		t.Fatalf("the routed request is current: %v", e)
	}
	tx.Rollback()
	// With everything asked by a live request, the goal asks nothing again.
	if draft() {
		t.Fatal("what a live request asks is not asked again")
	}
}

// A partial reply to a drafted request resumes the work it answers: answering one of two documents' questions resumes
// that document's work, the other keeps waiting on the client, and the request takes the rest even once the first
// document's work is done.
func TestAPartialReplyResumesTheWorkItAnswers(t *testing.T) {
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
	var closeID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Partial client "+rand.Text()).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, `DELETE FROM request_drafts WHERE close_id=$1`,
			`DELETE FROM preparation_jobs WHERE close_id=$1`, `UPDATE evidence SET superseded_by=NULL WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	jobs := []string{}
	for _, name := range []string{"insurance invoice.pdf", "software order.pdf"} {
		var source, job string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,'Document','fixture') RETURNING id::text`, closeID, name, rand.Text()).Scan(&source))
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&job))
		_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,'{"kind":"prepaid","questions":["Which months does it cover?"]}')`, job, source)
		must(err)
		jobs = append(jobs, job)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	must(err)
	_, err = draftGoalRequest(context.Background(), tx, goalRow{id: "2147480001", close: closeID, author: "user_a"})
	must(err)
	must(tx.Commit())
	token := strings.Repeat("r", 32)
	handler := apiWithStore(db, token)
	call := func(method, path, bearer string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var proposed struct{ Proposal struct{ ID string } }
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/requests/proposed", token, nil, 200), &proposed))
	var opened struct {
		ID        string `json:"id"`
		LinkToken string `json:"link_token"`
	}
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", token, map[string]any{"recipient": "owner@example.test", "confirmed": true}, 200), &opened))
	state := func(job string) (s string) {
		t.Helper()
		must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&s))
		return
	}
	reply := func(answers []clientAnswer) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"answers": answers})
		call("POST", "/v1/client-request", opened.LinkToken, map[string]any{"body": string(raw), "confirmed": true}, 200)
	}
	reply([]clientAnswer{{1, "answered", "September 2026 to August 2027"}})
	if state(jobs[0]) != "waiting_evidence" || state(jobs[1]) != "waiting_client" {
		t.Fatalf("the answered document's work resumes and the other waits: %s %s", state(jobs[0]), state(jobs[1]))
	}
	// The first document's work finishes; the request still takes the second answer.
	_, err = db.Exec(`UPDATE preparation_jobs SET state='completed' WHERE id=$1`, jobs[0])
	must(err)
	var page struct{ Open bool }
	must(json.Unmarshal(call("GET", "/v1/client-request", opened.LinkToken, nil, 200), &page))
	if !page.Open {
		t.Fatal("the request stays open for the question still to answer")
	}
	reply([]clientAnswer{{2, "answered", "October 2026 to September 2027"}})
	if state(jobs[0]) != "completed" || state(jobs[1]) != "waiting_evidence" {
		t.Fatalf("the second answer resumes only the second document's work: %s %s", state(jobs[0]), state(jobs[1]))
	}
	// With all its work finished, the request takes no more answers.
	_, err = db.Exec(`UPDATE preparation_jobs SET state='completed' WHERE id=$1`, jobs[1])
	must(err)
	must(json.Unmarshal(call("GET", "/v1/client-request", opened.LinkToken, nil, 200), &page))
	if page.Open {
		t.Fatal("a request whose work is all finished is closed")
	}
}
