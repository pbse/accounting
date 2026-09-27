package transport

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRequestDraftRevisions(t *testing.T) {
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
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	papers := []string{}
	for _, digest := range []string{"request-a", "request-b"} {
		var id string
		err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation',$2,'{"input":{"account":"Bank"},"blockers":["Statement page 2 is missing.","Statement page 2 is missing."],"sources":[]}') RETURNING id::text`, closeID, digest).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		papers = append(papers, id)
	}
	call("POST", "/v1/closes/0/requests", map[string]any{"workpaper_ids": papers}, 404)
	call("POST", base+"/requests", map[string]any{"workpaper_ids": []string{"0"}}, 422)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", base+"/requests", nil))
	if response.Code != 401 {
		t.Fatal("Request history exposed without auth")
	}
	call("POST", base+"/requests", map[string]any{"expected_id": "", "workpaper_ids": papers}, 200)
	read := func() []requestDraft {
		t.Helper()
		var drafts []requestDraft
		if json.Unmarshal(call("GET", base+"/requests", nil, 200), &drafts) != nil {
			t.Fatal("Invalid request history")
		}
		return drafts
	}
	drafts := read()
	if len(drafts) != 1 || drafts[0].Stale || strings.Count(drafts[0].Body, "Statement page 2 is missing.") != 1 || strings.TrimSpace(drafts[0].Body) != drafts[0].Body {
		t.Fatal("Consolidation or snapshot failed", drafts)
	}
	original := drafts[0]
	edited := map[string]any{"expected_id": original.ID, "workpaper_ids": papers, "context_digest": original.ContextDigest, "body": "Please provide page 2 of the September bank statement so we can check completeness."}
	call("POST", base+"/requests", edited, 200)
	call("POST", base+"/requests", edited, 200)
	drafts = read()
	if len(drafts) != 2 || drafts[1].Body != original.Body {
		t.Fatal("Retry duplicated a draft or history was overwritten")
	}
	edited["body"] = "Concurrent edit that must not replace newer wording"
	call("POST", base+"/requests", edited, 409)
	// The scheduled ledger captures never answer a client question, so they leave the draft current.
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'QuickBooks transactions 2026-09','sync-capture','{}','quickbooks-sync'),($1,'QuickBooks 2026-09 accrual period evidence.txt','period-refresh','{}','scheduled-quickbooks-refresh')`, closeID); err != nil {
		t.Fatal(err)
	}
	if drafts = read(); drafts[0].Stale {
		t.Fatal("A scheduled ledger capture flagged the request for reinspection")
	}
	// New evidence makes the old question potentially unnecessary; do not silently keep it current.
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'statement-page-2.txt','new-page','September statement page 2','fixture')`, closeID); err != nil {
		t.Fatal(err)
	}
	drafts = read()
	if !drafts[0].Stale {
		t.Fatal("New evidence did not flag the request for reinspection")
	}
	edited["expected_id"] = drafts[0].ID
	call("POST", base+"/requests", edited, 409)
	call("POST", base+"/requests", map[string]any{"expected_id": drafts[0].ID, "workpaper_ids": papers}, 200)
	drafts = read()
	if len(drafts) != 3 || drafts[0].Stale {
		t.Fatal("Fresh request revision not retained")
	}

	// IDs are serialized as strings, but latest/history ordering must stay numeric.
	for _, id := range []string{"999999999999999998", "1000000000000000000"} {
		_, err = db.Exec(`INSERT INTO request_drafts(id,close_id,body,workpaper_ids,context_digest,created_by) OVERRIDING SYSTEM VALUE VALUES($1,$2,$3,$4,$5,'fixture')`, id, closeID, "Numeric order "+id, `[]`, "fixture")
		if err != nil {
			t.Fatal(err)
		}
	}
	drafts = read()
	if drafts[0].ID != "1000000000000000000" {
		t.Fatal("History sorted numeric IDs as text", drafts[0].ID)
	}
	call("POST", base+"/requests", map[string]any{"expected_id": "999999999999999998", "workpaper_ids": papers}, 409)
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
