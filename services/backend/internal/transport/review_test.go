package transport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
)

func TestReviewAndEvidenceReplacement(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES('Review fixture','2026-09') RETURNING id::text`).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"sandbox_postings", "preparation_jobs", "preparation_plans", "workpapers"} {
			db.Exec("DELETE FROM "+table+" WHERE close_id=$1", closeID)
		}
		db.Exec(`UPDATE evidence SET superseded_by=NULL WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	base := "/v1/closes/" + closeID
	sources := []preparation.Source{}
	for _, text := range []string{"Invoice A 12000", "Invoice B 6000", "Invoice A corrected 10000"} {
		var id string
		digest := preparation.Digest([]byte(text))
		if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, digest, text).Scan(&id); err != nil {
			t.Fatal(err)
		}
		sources = append(sources, preparation.Source{ID: id, Digest: digest, Name: "invoice.txt", Role: "invoice", Location: "line 1"})
	}
	input := json.RawMessage(`{"entity":"Review fixture","source_reference":"Invoice","amount":"12000.00","service_start":"2026-09-01","service_end":"2027-08-31","close_period":"2026-09","currency":"USD","basis":"accrual","expense_account":"Expense","prepaid_account":"Prepaid","method":"equal_full_months_v1","method_confirmed":true,"booking_confirmed":true}`)
	plans := []preparation.Plan{}
	papers := []string{}
	digests := []string{}
	for _, source := range sources[:2] {
		p := preparation.Plan{Kind: "prepaid", Input: input, Sources: []preparation.Source{source}}
		plans = append(plans, p)
		raw, err := preparation.Calculate(p, "Review fixture", "2026-09")
		if err != nil {
			t.Fatal(err)
		}
		digest := preparation.Digest(raw)
		var id string
		if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, digest, string(raw)).Scan(&id); err != nil {
			t.Fatal(err)
		}
		papers = append(papers, id)
		digests = append(digests, digest)
	}
	for i, paper := range papers {
		body := map[string]string{"decision": "approved", "note": "Checked invoice and full-month method.", "digest": digests[i], "expected_review_id": ""}
		call("POST", base+"/workpapers/"+paper+"/reviews", body, 200)
		call("POST", base+"/workpapers/"+paper+"/reviews", body, 409)
	}
	// Hosted members without review authority cannot submit decisions.
	mux := http.NewServeMux()
	registerReview(mux, db)
	r := httptest.NewRequest("POST", base+"/workpapers/"+papers[0]+"/reviews", nil)
	r = r.WithContext(auth.WithActor(r.Context(), &auth.Claims{Organization: "org_test"}))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, r)
	if response.Code != 403 {
		t.Fatal("Hosted review permission was bypassed")
	}
	r = httptest.NewRequest("GET", base+"/workpapers/"+papers[0]+"/reviews", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != 401 {
		t.Fatal("Review history leaked without authentication")
	}
	call("GET", "/v1/closes/0/workpapers/"+papers[0]+"/reviews", nil, 404)
	call("POST", base+"/evidence/"+sources[0].ID+"/replace", map[string]string{"replacement_id": sources[0].ID, "reason": "Self replacement"}, 409)
	// A pending preparation must not use the old source after it is explicitly replaced.
	raw, _ := json.Marshal(plans[:1])
	var job string
	if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,'review-test',$2,'fixture') RETURNING id::text`, closeID, string(raw)).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO sandbox_postings(close_id,workpaper_id,source_key,realm,digest,journal,state) VALUES($1,$2,'review-source','test-realm','test-digest','{}','executing')`, closeID, papers[0]); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/evidence/"+sources[0].ID+"/replace", map[string]string{"replacement_id": sources[2].ID, "reason": "Cannot race an outstanding posting."}, 409)
	if _, err = db.Exec(`UPDATE sandbox_postings SET state='approved',approved_by='fixture',approval_expires_at=now()+interval '10 minutes' WHERE workpaper_id=$1`, papers[0]); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/evidence/"+sources[0].ID+"/replace", map[string]string{"replacement_id": sources[2].ID, "reason": "Invoice credit changes amount."}, 200)
	var postingState string
	var expired bool
	if err = db.QueryRow(`SELECT state,approval_expires_at IS NULL FROM sandbox_postings WHERE workpaper_id=$1`, papers[0]).Scan(&postingState, &expired); err != nil || postingState != "preview" || !expired {
		t.Fatal("Replacement retained an affected posting approval")
	}

	for i, paper := range papers {
		var result struct {
			State    string            `json:"state"`
			ReviewID string            `json:"review_id"`
			History  []json.RawMessage `json:"history"`
		}
		if json.Unmarshal(call("GET", base+"/workpapers/"+paper+"/reviews", nil, 200), &result) != nil {
			t.Fatal("Invalid review response")
		}
		expected := "source_changed"
		if i == 1 {
			expected = "approved"
		}
		if result.State != expected || len(result.History) != 1 {
			t.Fatalf("Affected scope or history lost: %+v", result)
		}
		if i == 0 {
			call("POST", base+"/workpapers/"+paper+"/reviews", map[string]string{"decision": "approved", "note": "Should be rejected", "digest": digests[i], "expected_review_id": result.ReviewID}, 409)
		}
	}
	if worked, err := preparation.ProcessOne(context.Background(), db); err != nil || !worked {
		t.Fatalf("Worker failed: %v %v", worked, err)
	}
	var state string
	db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state)
	if state != "failed" {
		t.Fatal("Worker used superseded evidence")
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM workpapers WHERE close_id=$1`, closeID).Scan(&count)
	if count != 2 {
		t.Fatal("Stale job created a new draft")
	}
}
