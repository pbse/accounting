package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/llm"
	"close.local/backend/internal/preparation"
)

func TestAgentRetriesTemporaryReadingFailuresWithinItsBudget(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var closeID int64
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id`, "Retry fixture "+rand.Text()).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			if _, err := db.Exec(q, closeID); err != nil {
				t.Error(err)
			}
		}
	}()
	var source string
	body := "Insurance invoice total USD 1,200.00."
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(body)), body).Scan(&source))
	tx, err := db.Begin()
	must(err)
	var closeText string
	must(tx.QueryRow(`SELECT id::text FROM client_closes WHERE id=$1`, closeID).Scan(&closeText))
	job, err := preparation.StartIntake(context.Background(), tx, closeText, source, "fixture")
	must(err)
	must(tx.Commit())
	status, calls := 503, 0
	models, err := llm.New(llm.Config{APIKey: "synthetic-key", Model: "synthetic-model", HTTP: &http.Client{Transport: modelRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		if status != 200 {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"synthetic provider failure"}`))}, nil
		}
		proposal, _ := json.Marshal(map[string]any{"kind": "prepaid", "summary": "Insurance", "questions": []string{"Which service period does this invoice cover?"}, "facts": []any{}, "sources": []any{map[string]string{"id": source, "role": "invoice"}}})
		reply, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(proposal)}}}}})
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(reply)))}, nil
	})}})
	must(err)
	ctx := preparation.WithinCloses(llm.WithClient(context.Background(), models), []int64{closeID})
	process := func(want bool) {
		t.Helper()
		worked, err := preparation.ProcessAgent(ctx, db)
		must(err)
		if worked != want {
			t.Fatalf("worked=%v, want %v", worked, want)
		}
	}
	read := func(want string, attempt int, waiting bool) {
		t.Helper()
		var state string
		var attempts int
		var retry sql.NullTime
		must(db.QueryRow(`SELECT j.state,i.agent_attempts,i.agent_retry_at FROM preparation_jobs j JOIN preparation_intakes i ON i.job_id=j.id WHERE j.id=$1`, job).Scan(&state, &attempts, &retry))
		if state != want || attempts != attempt || retry.Valid != waiting || waiting && !retry.Time.After(time.Now()) {
			t.Fatalf("state=%s attempts=%d retry=%v", state, attempts, retry)
		}
		if waiting {
			remaining := time.Until(retry.Time)
			delay := time.Duration(attempt) * time.Minute
			if remaining < delay-10*time.Second || remaining > delay+time.Second {
				t.Fatalf("attempt %d waits %s, want about %s", attempt, remaining, delay)
			}
		}
	}
	process(true)
	read("waiting_evidence", 1, true)
	tx, err = db.Begin()
	must(err)
	progress, err := readGoalProgress(ctx, tx, closeText, "review")
	must(err)
	must(tx.Rollback())
	visible := false
	for _, item := range progress.Items {
		if item.JobID == job && item.Status == "working" && strings.Contains(item.Text, "Retrying automatically") {
			visible = true
		}
	}
	if !visible {
		t.Fatal("the goal hides automatic recovery")
	}
	process(false)
	if calls != 1 {
		t.Fatal("retried before the persisted delay")
	}
	_, err = db.Exec(`UPDATE preparation_intakes SET agent_retry_at=now()-interval '1 second' WHERE job_id=$1`, job)
	must(err)
	status = 200
	process(true)
	read("needs_confirmation", 2, false)
	if calls != 2 {
		t.Fatal("the scheduled retry did not recover the original job")
	}
	// A persistent rate limit stops at the existing three-attempt cap.
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE id=$1`, job)
	must(err)
	_, err = db.Exec(`UPDATE preparation_intakes SET agent_attempts=0,agent_retry_at=NULL WHERE job_id=$1`, job)
	must(err)
	status = 429
	for attempt := 1; attempt <= 3; attempt++ {
		process(true)
		if attempt < 3 {
			read("waiting_evidence", attempt, true)
			_, err = db.Exec(`UPDATE preparation_intakes SET agent_retry_at=now()-interval '1 second' WHERE job_id=$1`, job)
			must(err)
		} else {
			read("failed", attempt, false)
		}
	}
	process(false)
	if calls != 5 {
		t.Fatalf("unbounded calls: %d", calls)
	}
	// Reset the fixture so the attempt cap cannot mask a permanent failure.
	_, err = db.Exec(`UPDATE preparation_jobs SET state='waiting_evidence',error='' WHERE id=$1`, job)
	must(err)
	_, err = db.Exec(`UPDATE preparation_intakes SET agent_attempts=0 WHERE job_id=$1`, job)
	must(err)
	status = 401
	process(true)
	read("failed", 1, false)
	process(false)
	if calls != 6 {
		t.Fatalf("permanent failure was retried: calls=%d", calls)
	}
}
