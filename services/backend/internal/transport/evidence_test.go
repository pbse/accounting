package transport

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/documents"
	"close.local/backend/internal/preparation"
)

// Proves upload isolation, source binding and durable replay on the existing local database harness.
func TestEvidencePreparationRoundTrip(t *testing.T) {
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
	call := func(method, path string, body any, status int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	idOf := func(raw []byte) string {
		var v map[string]any
		if json.Unmarshal(raw, &v) != nil {
			t.Fatal("Invalid response")
		}
		return v["id"].(string)
	}
	closeID := idOf(call("POST", "/v1/closes", map[string]string{"entity": "Evidence job fixture", "period": "2026-09"}, 200))
	other := idOf(call("POST", "/v1/closes", map[string]string{"entity": "Other evidence fixture", "period": "2026-09"}, 200))
	defer func() {
		for _, id := range []string{closeID, other} {
			for _, table := range []string{"preparation_jobs", "preparation_plans", "workpapers", "evidence"} {
				db.Exec("DELETE FROM "+table+" WHERE close_id=$1", id)
			}
			db.Exec("DELETE FROM client_closes WHERE id=$1", id)
		}
	}()
	base := "/v1/closes/" + closeID
	upload := func(name, content string, authorized bool, status int) []byte {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		f, _ := m.CreateFormFile("file", name)
		io.WriteString(f, content)
		m.Close()
		r := httptest.NewRequest("POST", base+"/evidence", &b)
		r.Header.Set("Content-Type", m.FormDataContentType())
		if authorized {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("upload: %d %s", w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	upload("source.txt", "private", false, 401)
	upload("source.pdf", "%PDF-1.5", true, 422)
	upload("source.txt", "binary\x00text", true, 422)
	upload("large.txt", strings.Repeat("a", 524289), true, 422)
	statement := "id,date,amount\nS1,2026-09-02,200.00\nS2,2026-09-05,-50.00"
	ledger := "id,date,amount\nL1,2026-09-02,200.00\nL2,2026-09-05,-50.00\nL3,2026-09-28,-50.00"
	s := idOf(upload("statement.csv", statement, true, 200))
	l := idOf(upload("ledger.csv", ledger, true, 200))
	if idOf(upload("copy.csv", statement, true, 200)) != s {
		t.Fatal("Duplicate document was not deduplicated")
	}
	call("GET", "/v1/closes/"+other+"/evidence/"+s, nil, 404)
	raw := call("GET", base+"/evidence/"+s, nil, 200)
	if string(raw) != statement {
		t.Fatal("Original bytes lost")
	}
	input := json.RawMessage(`{"entity":"Evidence job fixture","account":"Bank","currency":"USD","close_period":"2026-09","statement_reference":"manual","ledger_reference":"manual","statement_opening":"1000.00","statement_closing":"1150.00","ledger_opening":"1000.00","ledger_closing":"1100.00","expected_pages":2,"pages":[1,2],"statement_csv":"wrong","ledger_csv":"wrong","policy_confirmed":true}`)
	plan := preparation.Plan{Kind: "reconciliation", Input: input, Sources: []preparation.Source{{ID: s, Role: "statement", Location: "rows 1–3"}, {ID: l, Role: "ledger", Location: "rows 1–4"}}}
	call("POST", "/v1/closes/"+other+"/preparations", plan, 422)
	planID := idOf(call("POST", base+"/preparations", plan, 200))
	selection := map[string]any{"plans": []string{planID}}
	call("POST", "/v1/closes/"+other+"/jobs", selection, 422)
	job := idOf(call("POST", base+"/jobs", selection, 202))
	if idOf(call("POST", base+"/jobs", selection, 202)) != job {
		t.Fatal("Duplicate delivery created another job")
	}
	call("POST", base+"/jobs/"+job+"/cancel", map[string]string{}, 200)
	call("POST", base+"/jobs", selection, 202)
	// Simulate an interrupted worker's uncommitted claim: concurrent workers skip it,
	// and releasing its transaction leaves durable work available for another process.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var locked string
	if err = tx.QueryRow(`SELECT id::text FROM preparation_jobs WHERE id=$1 FOR UPDATE`, job).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if worked, err := preparation.ProcessOne(context.Background(), db); err != nil || worked {
		tx.Rollback()
		t.Fatalf("Claim not isolated: %v %v", worked, err)
	}
	tx.Rollback()
	if worked, err := preparation.ProcessOne(context.Background(), db); err != nil || !worked {
		t.Fatalf("Resume failed: %v %v", worked, err)
	}
	if worked, err := preparation.ProcessOne(context.Background(), db); err != nil || worked {
		t.Fatalf("Repeated completed work: %v %v", worked, err)
	}
	var state string
	var count int
	db.QueryRow(`SELECT state FROM preparation_jobs WHERE id=$1`, job).Scan(&state)
	db.QueryRow(`SELECT count(*) FROM workpapers WHERE close_id=$1`, closeID).Scan(&count)
	if state != "completed" || count != 1 {
		t.Fatalf("Partial result: %s %d", state, count)
	}
	var paper []byte
	db.QueryRow(`SELECT paper FROM workpapers WHERE close_id=$1`, closeID).Scan(&paper)
	var parsed struct {
		ClosingDifference string               `json:"closing_difference"`
		Sources           []preparation.Source `json:"sources"`
	}
	if json.Unmarshal(paper, &parsed) != nil || parsed.ClosingDifference != "50.00" || len(parsed.Sources) != 2 || parsed.Sources[0].Digest == "" {
		t.Fatal("Source input or lineage lost")
	}
	call("POST", base+"/jobs/"+job+"/cancel", map[string]string{}, 409)
	var oldID string
	if err = db.QueryRow(`SELECT id::text FROM workpapers WHERE close_id=$1`, closeID).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	plan.Revises = oldID
	plan.Input = bytes.ReplaceAll(plan.Input, []byte(`"ledger_closing":"1100.00"`), []byte(`"ledger_closing":"1150.00"`))
	var revision map[string]string
	if err = json.Unmarshal(call("POST", base+"/preparations", plan, 200), &revision); err != nil || revision["job_id"] == "" {
		t.Fatal("Revision was not automatically queued", err)
	}
	if worked, err := preparation.ProcessOne(context.Background(), db); err != nil || !worked {
		t.Fatal("Recalculation failed", err)
	}
	var revisedID, reviewState string
	if err = db.QueryRow(`SELECT id::text,review_state FROM workpaper_review_state WHERE close_id=$1 AND paper->>'revises'=$2`, closeID, oldID).Scan(&revisedID, &reviewState); err != nil || reviewState != "pending" {
		t.Fatal("Revision did not require fresh review", err, reviewState)
	}
	db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id=$1`, oldID).Scan(&reviewState)
	if reviewState != "source_changed" {
		t.Fatal("Previous draft remained current", reviewState)
	}
	call("POST", base+"/preparations", plan, 200)
	if worked, err := preparation.ProcessOne(context.Background(), db); err != nil || worked {
		t.Fatal("Completed revision was duplicated", err)
	}
	// An uploaded malformed PDF is retained and fails visibly in the worker;
	// its raw bytes never become accounting input or silently empty text.
	pdf := idOf(upload("broken.pdf", "%PDF-1.4 invalid %%EOF", true, 200))
	call("GET", base+"/evidence/"+pdf+"/text", nil, 409)
	if worked, err := documents.ProcessOne(context.Background(), db); err != nil || !worked {
		t.Fatal("Extraction claim failed", err)
	}
	db.QueryRow(`SELECT extraction_state FROM evidence WHERE id=$1`, pdf).Scan(&state)
	if state != "failed" {
		t.Fatal("Malformed PDF did not fail visibly", state)
	}
	if string(call("GET", base+"/evidence/"+pdf, nil, 200)) != "%PDF-1.4 invalid %%EOF" {
		t.Fatal("PDF original lost")
	}
	call("POST", base+"/evidence/"+pdf+"/retry", map[string]string{}, 202)
	documents.ProcessOne(context.Background(), db)

	archive := call("GET", base+"/export", nil, 200)
	z, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range z.File {
		if f.Name == "evidence/"+s+".txt" {
			reader, _ := f.Open()
			b, _ := io.ReadAll(reader)
			reader.Close()
			found = string(b) == statement
		}
	}
	if !found {
		t.Fatal("Export lost original evidence")
	}
}
