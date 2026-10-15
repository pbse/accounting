package transport

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

// The close's saved request drafts, newest first by number, each flagged when the evidence or reviews it was drafted
// from changed (scheduled ledger captures aside).
func TestRequestDraftHistory(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES('Request draft fixture','2026-09') RETURNING id::text`).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"request_drafts", "workpapers", "evidence", "client_closes"} {
			key := "close_id"
			if table == "client_closes" {
				key = "id"
			}
			db.Exec("DELETE FROM "+table+" WHERE "+key+"=$1", closeID)
		}
	}()
	token := strings.Repeat("r", 32)
	handler := apiWithStore(db, token)
	base := "/v1/closes/" + closeID
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", base+"/requests", nil))
	if response.Code != 401 {
		t.Fatal("Request history exposed without auth")
	}
	read := func() []requestDraft {
		t.Helper()
		r := httptest.NewRequest("GET", base+"/requests", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var drafts []requestDraft
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &drafts) != nil {
			t.Fatalf("Invalid request history: %d %s", w.Code, w.Body.String())
		}
		return drafts
	}
	var paper string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation','request-a','{"input":{"account":"Bank"},"sources":[]}') RETURNING id::text`, closeID).Scan(&paper); err != nil {
		t.Fatal(err)
	}
	tx, _ := db.Begin()
	digest, err := preparation.RequestContext(context.Background(), tx, closeID, []string{paper})
	tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send page 2.',jsonb_build_array($2::text),$3,'fixture')`, closeID, paper, digest); err != nil {
		t.Fatal(err)
	}
	if drafts := read(); len(drafts) != 1 || drafts[0].Stale {
		t.Fatal("A draft of the current context is current", drafts)
	}
	// The scheduled ledger captures never answer a client question, so they leave the draft current.
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'QuickBooks transactions 2026-09','sync-capture','{}','quickbooks-sync'),($1,'QuickBooks 2026-09 accrual period evidence.txt','period-refresh','{}','scheduled-quickbooks-refresh')`, closeID); err != nil {
		t.Fatal(err)
	}
	if drafts := read(); drafts[0].Stale {
		t.Fatal("A scheduled ledger capture flagged the request for reinspection")
	}
	// New evidence makes the old question potentially unnecessary; do not silently keep it current.
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'statement-page-2.txt','new-page','September statement page 2','fixture')`, closeID); err != nil {
		t.Fatal(err)
	}
	if drafts := read(); !drafts[0].Stale {
		t.Fatal("New evidence did not flag the request for reinspection")
	}
	// IDs are serialized as strings, but latest/history ordering must stay numeric.
	for _, id := range []string{"999999999999999998", "1000000000000000000"} {
		_, err = db.Exec(`INSERT INTO request_drafts(id,close_id,body,workpaper_ids,context_digest,created_by) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,$4,$5,'fixture')`, id, closeID, "Numeric order "+id, `[]`, "fixture")
		if err != nil {
			t.Fatal(err)
		}
	}
	if drafts := read(); drafts[0].ID != "1000000000000000000" {
		t.Fatal("History sorted numeric IDs as text", drafts[0].ID)
	}
}

func TestPaperChangesPreserveExactAmountsAndAllSections(t *testing.T) {
	before := []byte(`{"status":"draft","input":{"amount":"12000.00","currency":"USD"},"period_expense":"1000.00","schedule":[{"period":"2026-09","expense":"1000.00"},{"period":"2026-10","expense":"1000.00"}],"sources":[{"id":"1"}]}`)
	after := []byte(`{"status":"draft","revises":"5","input":{"amount":"6000.00","currency":"USD"},"period_expense":"500.00","schedule":[{"period":"2026-09","expense":"500.00"},{"period":"2026-10","expense":"500.00"}],"sources":[{"id":"2"}]}`)
	changes := paperChanges(before, after)
	if len(changes) != 5 {
		t.Fatalf("Expected amount, result, both schedule rows and evidence changes: %+v", changes)
	}
	if changes[0].Field != "input.amount" || changes[0].Before != "12000.00" || changes[0].After != "6000.00" {
		t.Fatal("Amounts were rounded or incorrectly compared")
	}
	if changes[2].Field != "schedule.2026-09.expense" || changes[3].Field != "schedule.2026-10.expense" || changes[3].After != "500.00" {
		t.Fatal("Schedule comparison dropped a changed month", changes)
	}
	if len(paperChanges(before, before)) != 0 {
		t.Fatal("Unchanged work reported as changed")
	}
}
