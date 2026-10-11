package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
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
	if !draft() {
		t.Fatal("the goal drafts a request for the client's question")
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
	// The goal drafts the same documents once, whatever it is asked again.
	if draft() {
		t.Fatal("an unchanged set of questions is not drafted twice")
	}
	call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", map[string]any{"recipient": "owner@example.test"}, 422)
	var opened struct{ ID, Link_token string }
	must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/requests/"+proposed.Proposal.ID+"/approve", map[string]any{"recipient": "owner@example.test", "confirmed": true}, 200), &opened))
	var state string
	must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state))
	if state != "waiting_client" || opened.Link_token == "" {
		t.Fatalf("approving opens the request with a private link and the document waits on the client: %s %+v", state, opened)
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
}
