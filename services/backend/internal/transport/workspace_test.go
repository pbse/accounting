package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestWorkCountsAndReviewQueueMatchTheirLists(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Workspace counts " + rand.Text()
	var closeID, otherID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, "Other "+entity).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, id := range []string{closeID, otherID} {
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
	}()
	for i, state := range []string{"needs_confirmation", "needs_confirmation", "queued", "completed", "failed"} {
		if _, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]',$3,'fixture')`, closeID, fmt.Sprintf("counts-%d", i), state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,'other','[]','needs_confirmation','fixture')`, otherID); err != nil {
		t.Fatal(err)
	}
	var evidenceID string
	content := "Invoice amount: 1200.00"
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(content)), content).Scan(&evidenceID); err != nil {
		t.Fatal(err)
	}
	sources := fmt.Sprintf(`[{"id":"%s","digest":"%s","role":"invoice","name":"invoice.txt","location":"line 1"}]`, evidenceID, preparation.Digest([]byte(content)))
	paper := func(label, extra string) string {
		t.Helper()
		var id string
		body := `{"label":"` + label + `","sources":` + sources + `,"blockers":[]` + extra + `}`
		if e := db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, label, body).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	superseded := paper("superseded", "")
	current := paper("current", `,"revises":"`+superseded+`"`)
	paper("blocked", `,"blockers":["Statement page 2 is missing."]`)
	if _, err = db.Exec(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','unlinked','{"blockers":[]}')`, closeID); err != nil {
		t.Fatal(err)
	}
	h := apiWithStore(db, strings.Repeat("q", 32))
	get := func(path string) []byte {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("q", 32))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	for _, view := range workViews {
		var page struct {
			Items  []json.RawMessage `json:"items"`
			Counts map[string]int    `json:"counts"`
		}
		json.Unmarshal(get("/v1/closes/"+closeID+"/work?view="+view), &page)
		if page.Counts[view] != len(page.Items) {
			t.Errorf("%s count %d but list has %d", view, page.Counts[view], len(page.Items))
		}
	}
	var scoped struct {
		Counts map[string]int `json:"counts"`
	}
	json.Unmarshal(get("/v1/closes/work?view=all&client="+strings.ReplaceAll(entity, " ", "%20")), &scoped)
	if scoped.Counts["needs_confirmation"] != 3 || scoped.Counts["all"] != 6 {
		t.Fatalf("client search should include both matching closes: %v", scoped.Counts)
	}
	var queue struct {
		Items []map[string]any `json:"items"`
		Count int              `json:"count"`
	}
	json.Unmarshal(get("/v1/closes/review-queue?client="+strings.ReplaceAll(entity, " ", "%20")), &queue)
	if queue.Count != 1 || len(queue.Items) != 1 || queue.Items[0]["workpaper_id"] != current {
		t.Fatalf("queue should hold only the current, linked, unblocked draft %s: %+v", current, queue)
	}
	var closes struct {
		Closes []savedClose `json:"closes"`
	}
	json.Unmarshal(get("/v1/closes?q="+strings.ReplaceAll("Other "+entity, " ", "%20")), &closes)
	if len(closes.Closes) != 1 || closes.Closes[0].ID != otherID {
		t.Fatalf("close search: %+v", closes.Closes)
	}
}
