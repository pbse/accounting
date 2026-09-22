package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

func TestCarryForwardRoundingLineageAndFreshReview(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Carry fixture " + rand.Text()
	var origin string
	if err = db.QueryRow("INSERT INTO client_closes(entity,period) VALUES($1,'2026-12') RETURNING id::text", entity).Scan(&origin); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_jobs", "workpapers", "evidence"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)", entity); e != nil {
				t.Error(e)
			}
		}
		if _, e := db.Exec("DELETE FROM client_closes WHERE entity=$1", entity); e != nil {
			t.Error(e)
		}
	}()
	sources := []preparation.Source{}
	for _, name := range []string{"invoice.txt", "mapping.txt"} {
		var id string
		digest := preparation.Digest([]byte(name))
		if err = db.QueryRow("INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$2,'fixture') RETURNING id::text", origin, name, digest).Scan(&id); err != nil {
			t.Fatal(err)
		}
		role := "support"
		if name == "invoice.txt" {
			role = "invoice"
		}
		sources = append(sources, preparation.Source{ID: id, Digest: digest, Name: name, Role: role, Location: "line 1"})
	}
	input := prepaids.Input{Entity: entity, SourceReference: "Synthetic annual terms", Amount: "100.01", ServiceStart: "2026-12-01", ServiceEnd: "2027-02-28", ClosePeriod: "2026-12", Currency: "USD", Basis: "accrual", ExpenseAccount: "Insurance", PrepaidAccount: "Prepaid insurance", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	raw, _ := json.Marshal(input)
	body, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: raw, Sources: sources}, entity, "2026-12")
	if err != nil {
		t.Fatal(err)
	}
	var first string
	if err = db.QueryRow("INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text", origin, preparation.Digest(body), string(body)).Scan(&first); err != nil {
		t.Fatal(err)
	}
	token := "carry-test-workspace-token-000000000"
	h := apiWithStore(db, token)
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	preview := func(id string) []carryItem {
		t.Helper()
		var result struct {
			Items []carryItem `json:"items"`
		}
		json.Unmarshal(call("GET", "/v1/closes/"+id+"/carry-forward", nil, 200), &result)
		return result.Items
	}
	approve := func(closeID, id, decision string) {
		var digest, version string
		if e := db.QueryRow("SELECT digest,review_id FROM workpaper_review_state WHERE id=$1", id).Scan(&digest, &version); e != nil {
			t.Fatal(e)
		}
		call("POST", "/v1/closes/"+closeID+"/workpapers/"+id+"/reviews", map[string]any{"decision": decision, "note": "Synthetic review; schedule only", "digest": digest, "expected_review_id": version}, 200)
	}
	if preview(origin)[0].Eligible {
		t.Fatal("Unreviewed schedule eligible")
	}
	approve(origin, first, "approved")
	selected := preview(origin)
	apply := func(closeID string, items []carryItem) (string, string) {
		t.Helper()
		var result struct {
			CloseID string   `json:"close_id"`
			Jobs    []string `json:"job_ids"`
		}
		json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/carry-forward", map[string]any{"confirmed": true, "items": items}, 200), &result)
		if len(result.Jobs) != 1 {
			t.Fatal("Missing durable job")
		}
		return result.CloseID, result.Jobs[0]
	}
	january, job := apply(origin, selected)
	retryClose, retryJob := apply(origin, selected)
	if january != retryClose || job != retryJob {
		t.Fatal("Retry duplicated work")
	}
	process := func(job string) (string, prepaids.Workpaper) {
		t.Helper()
		if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
			t.Fatal("Worker did not prepare", e)
		}
		var paperID, state string
		var raw []byte
		if e := db.QueryRow("SELECT state,COALESCE(workpaper_ids->>0,'') FROM preparation_jobs WHERE id=$1", job).Scan(&state, &paperID); e != nil || state != "completed" {
			t.Fatal(state, e)
		}
		if e := db.QueryRow("SELECT paper,review_state FROM workpaper_review_state WHERE id=$1", paperID).Scan(&raw, &state); e != nil || state != "pending" {
			t.Fatal("Approval inherited", state, e)
		}
		var paper prepaids.Workpaper
		json.Unmarshal(raw, &paper)
		if paper.Proposal != nil || paper.Input.BookingConfirmed || paper.Input.MethodConfirmed {
			t.Fatal("Carry-forward asserted posting authority")
		}
		var linked struct {
			Sources []preparation.Source      `json:"sources"`
			Carry   *preparation.CarryForward `json:"carry_forward"`
		}
		json.Unmarshal(raw, &linked)
		if len(linked.Sources) != 2 || len(linked.Carry.SourceIDs) != 2 {
			t.Fatal("Lost source lineage")
		}
		return paperID, paper
	}
	call("POST", "/v1/closes/"+january+"/jobs/"+job+"/cancel", map[string]any{}, 200)
	_, resumed := apply(origin, preview(origin))
	if resumed != job {
		t.Fatal("Cancelled carry-forward duplicated its job")
	}
	janPaper, jan := process(job)
	if jan.PeriodExpense != "33.33" || jan.PrepaidBalance != "33.35" || jan.Input.ClosePeriod != "2027-01" {
		t.Fatal("January rounding incorrect", jan)
	}
	if preview(january)[0].Eligible {
		t.Fatal("Next month inherited approval")
	}
	approve(january, janPaper, "approved")
	february, febJob := apply(january, preview(january))
	febPaper, feb := process(febJob)
	if feb.PeriodExpense != "33.35" || feb.PrepaidBalance != "0.00" {
		t.Fatal("Final residual lost", feb)
	}
	approve(february, febPaper, "approved")
	if preview(february)[0].Eligible {
		t.Fatal("Finished schedule carried beyond its term")
	}
	// Changing the root review invalidates both generations, without changing their history.
	approve(origin, first, "changes_requested")
	for _, id := range []string{janPaper, febPaper} {
		var state string
		db.QueryRow("SELECT review_state FROM workpaper_review_state WHERE id=$1", id).Scan(&state)
		if state != "source_changed" {
			t.Fatal("Ancestor change failed to invalidate carried work", id, state)
		}
	}
	if preview(january)[0].Eligible {
		t.Fatal("Stale lineage eligible")
	}
	// A fresh review permits a fresh run; the old result remains historical.
	approve(origin, first, "approved")
	_, newJob := apply(origin, preview(origin))
	if newJob == job {
		t.Fatal("Changed review reused old authority")
	}
	// The worker rechecks after enqueue, too.
	approve(origin, first, "changes_requested")
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal(e)
	}
	var state string
	db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", newJob).Scan(&state)
	if state != "failed" {
		t.Fatal("Worker accepted a stale predecessor", state)
	}
}
