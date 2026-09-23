package transport

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
)

func TestScopedSignoffInvalidationAndReopening(t *testing.T) {
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
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Signoff fixture "+rand.Text()).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"close_signoffs", "close_scopes", "preparation_jobs", "workpaper_reviews", "workpapers", "evidence", "client_closes"} {
			query := `DELETE FROM ` + table + ` WHERE close_id=$1`
			if table == "client_closes" {
				query = `DELETE FROM client_closes WHERE id=$1`
			}
			if table == "workpaper_reviews" {
				query = `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`
			}
			if _, e := db.Exec(query, closeID); e != nil {
				t.Error(e)
			}
		}
	}()
	papers := []string{}
	for _, name := range []string{"one", "two"} {
		var source, paper string
		if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$2,$2,'fixture') RETURNING id::text`, closeID, name).Scan(&source); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"sources": []any{map[string]string{"id": source, "digest": name, "role": "invoice"}}, "blockers": []string{}})
		if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, name, string(raw)).Scan(&paper); err != nil {
			t.Fatal(err)
		}
		papers = append(papers, paper)
	}
	token := strings.Repeat("s", 32)
	h := apiWithStore(db, token)
	base := "/v1/closes/" + closeID
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	get := func() readiness {
		t.Helper()
		var value readiness
		if e := json.Unmarshal(call("GET", base+"/readiness", nil, 200), &value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	decision := func(s readiness, action string) map[string]any {
		return map[string]any{"digest": s.Digest, "expected_decision_id": s.DecisionID, "action": action, "note": "Scoped synthetic check", "confirmed": true}
	}
	initial := get()
	if len(initial.Blockers) == 0 {
		t.Fatal("empty close looked ready")
	}
	if len(initial.SuggestedPapers) != 2 || initial.SuggestedPapers[0] != papers[0] || initial.SuggestedPapers[1] != papers[1] {
		t.Fatal("current drafts were not suggested", initial.SuggestedPapers)
	}
	if bytes.Contains(initial.Snapshot, []byte(`"revises"`)) {
		t.Fatal("UI suggestions changed the signed readiness snapshot")
	}
	scope := closeScope{Description: "Prepaids only <script>unsafe</script>; payroll excluded.", RequiredPapers: papers, ExternalSteps: []externalStep{{Name: "Ledger tie-out"}}}
	call("POST", base+"/scope", map[string]any{"digest": initial.Digest, "scope": scope}, 200)
	current := get()
	call("POST", base+"/signoff", decision(current, "sign"), 409)
	// Only one approved draft must not satisfy two required workpapers.
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'one','approved','fixture','reviewer')`, papers[0]); err != nil {
		t.Fatal(err)
	}
	current = get()
	if len(current.Blockers) != 2 {
		t.Fatalf("expected missing second review and external step: %v", current.Blockers)
	}
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'two','approved','fixture','reviewer')`, papers[1]); err != nil {
		t.Fatal(err)
	}
	current = get()
	scope.ExternalSteps[0].Complete = true
	scope.ExternalSteps[0].Reference = "Synthetic report p. 1 reviewed"
	call("POST", base+"/scope", map[string]any{"digest": current.Digest, "scope": scope}, 200)
	current = get()
	if len(current.Blockers) != 0 {
		t.Fatal(current.Blockers)
	}
	stale := current
	// New work invalidates an already-open browser snapshot and blocks sign-off.
	var job string
	if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,'signoff-job','[]','queued','fixture') RETURNING id::text`, closeID).Scan(&job); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/signoff", decision(stale, "sign"), 409)
	current = get()
	if len(current.Blockers) == 0 {
		t.Fatal("queued work did not block sign-off")
	}
	call("POST", base+"/signoff", decision(current, "sign"), 409)
	if _, err = db.Exec(`UPDATE preparation_jobs SET state='cancelled' WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	current = get()
	call("POST", base+"/signoff", decision(current, "sign"), 200)
	call("POST", base+"/signoff", decision(current, "sign"), 409)
	current = get()
	if current.State != "signed_off" || current.DecisionID == "" {
		t.Fatalf("sign-off missing: %+v", current)
	}
	// No inherited approval after a new source, even if selected papers are unchanged.
	if _, err = db.Exec(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'new.txt','new','new facts','fixture')`, closeID); err != nil {
		t.Fatal(err)
	}
	current = get()
	if current.State != "reopen_required" {
		t.Fatal("new evidence retained sign-off")
	}
	call("POST", base+"/signoff", decision(current, "sign"), 409)
	call("POST", base+"/scope", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "scope": scope}, 409)
	call("POST", base+"/signoff", decision(current, "reopen"), 200)
	reopened := get()
	if reopened.State != "open" {
		t.Fatal("reopening failed")
	}
	call("POST", base+"/signoff", decision(stale, "sign"), 409)
	call("POST", base+"/signoff", decision(reopened, "sign"), 200)
	current = get()
	var history []any
	json.Unmarshal(current.History, &history)
	if len(history) != 3 {
		t.Fatal("decision history lost")
	}
	exported := call("GET", base+"/export", nil, 200)
	archive, e := zip.NewReader(bytes.NewReader(exported), int64(len(exported)))
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range archive.File {
		if f.Name == "manifest.json" || f.Name == "review.html" {
			r, _ := f.Open()
			raw, _ := io.ReadAll(r)
			r.Close()
			if f.Name == "manifest.json" {
				var pack closePackage
				json.Unmarshal(raw, &pack)
				if pack.Readiness == nil || pack.Readiness.State != "signed_off" {
					t.Fatal("sign-off missing from export")
				}
			} else if strings.Contains(string(raw), "<script>") || !strings.Contains(string(raw), "payroll excluded") {
				t.Fatal("scope missing or unsafe in HTML")
			}
		}
	}
	// Returning a job to the same visible state must not resurrect old sign-off.
	if _, err = db.Exec(`UPDATE preparation_jobs SET state='queued',finished_at=NULL WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE preparation_jobs SET state='cancelled',finished_at=now() WHERE id=$1`, job); err != nil {
		t.Fatal(err)
	}
	if get().State != "reopen_required" {
		t.Fatal("job retry resurrected sign-off")
	}
	var revised string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','revision',jsonb_build_object('revises',$2::text,'blockers','[]'::jsonb)) RETURNING id::text`, closeID, papers[0]).Scan(&revised); err != nil {
		t.Fatal(err)
	}
	suggested := get().SuggestedPapers
	if len(suggested) != 2 || suggested[0] != papers[1] || suggested[1] != revised {
		t.Fatal("superseded draft remained in suggested scope", suggested)
	}
	// A hosted reviewer cannot sign without the additional close permission.
	mux := http.NewServeMux()
	registerReadiness(mux, db)
	r := httptest.NewRequest("POST", base+"/signoff", strings.NewReader(`{}`))
	r = r.WithContext(auth.WithActor(r.Context(), &auth.Claims{Organization: "org_fixture", Permissions: []string{"workpapers:review"}}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("sign-off role bypass: %d", w.Code)
	}
}
