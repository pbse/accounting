package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/preparation"
)

func TestClosePlanningRanksWorkAndBulkApprovalChecksEachDraft(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prefix := "Planning " + rand.Text()
	foreignOrg := "org_" + rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, foreignOrg); err != nil {
		t.Fatal(err)
	}
	var older, soon, foreign string
	create := func(entity, org string, target *string) {
		t.Helper()
		if e := db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,'2026-09',$2) RETURNING id::text`, entity, org).Scan(target); e != nil {
			t.Fatal(e)
		}
	}
	create(prefix+" Soon", "local-development", &soon)
	create(prefix+" Older", "local-development", &older)
	create(prefix+" Foreign", foreignOrg, &foreign)
	defer func() {
		for _, id := range []string{soon, older, foreign} {
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organizations WHERE id=$1`, foreignOrg)
	}()
	for _, id := range []string{soon, older} {
		if _, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,'plan','[]','needs_confirmation','fixture')`, id); err != nil {
			t.Fatal(err)
		}
	}
	token := strings.Repeat("p", 32)
	h := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	due := time.Now().AddDate(0, 0, 3).Format("2006-01-02")
	call("POST", "/v1/closes/"+soon+"/planning", map[string]string{"due_on": "30/09/2026", "priority": "high"}, 422)
	call("POST", "/v1/closes/"+soon+"/planning", map[string]string{"due_on": due, "priority": "high", "assignment": "take"}, 200)
	if _, err = db.Exec(`UPDATE client_closes SET owner='user_colleague' WHERE id=$1`, older); err != nil {
		t.Fatal(err)
	}
	call("POST", "/v1/closes/"+older+"/planning", map[string]string{"priority": "normal", "assignment": "take"}, 409)
	call("POST", "/v1/closes/"+older+"/planning", map[string]string{"priority": "normal", "assignment": "release"}, 409)
	var page struct {
		Items  []map[string]any `json:"items"`
		Counts map[string]int   `json:"counts"`
	}
	json.Unmarshal(call("GET", "/v1/closes/work?view=needs_confirmation&client="+strings.ReplaceAll(prefix, " ", "%20"), nil, 200), &page)
	if len(page.Items) != 2 || page.Items[0]["close_id"] != soon || page.Items[0]["owned_by_me"] != true || page.Items[0]["due_on"] != due {
		t.Fatalf("my due-soon close should rank first in its bucket: %+v", page.Items)
	}
	if page.Counts["mine"] != 1 {
		t.Fatalf("mine count: %v", page.Counts)
	}

	insertEvidence := func(closeID string) string {
		t.Helper()
		var id string
		content := "Statement for " + closeID
		if e := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'statement.csv',$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest([]byte(content)), content).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return fmt.Sprintf(`[{"id":"%s","digest":"%s","role":"statement","name":"statement.csv","location":"All rows"}]`, id, preparation.Digest([]byte(content)))
	}
	paper := func(closeID, digest, sources, extra string) string {
		t.Helper()
		var id string
		if e := db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'reconciliation',$2,$3) RETURNING id::text`, closeID, digest, `{"sources":`+sources+`,"blockers":[]`+extra+`}`).Scan(&id); e != nil {
			t.Fatal(e)
		}
		return id
	}
	sources := insertEvidence(soon)
	routine := paper(soon, "routine", sources, "")
	stale := paper(soon, "stale", sources, "")
	adjusting := paper(soon, "adjusting", sources, `,"proposal":{"amount":"10.00"}`)
	foreignPaper := paper(foreign, "foreign", insertEvidence(foreign), "")
	var queue struct {
		Items []map[string]any `json:"items"`
	}
	json.Unmarshal(call("GET", "/v1/closes/review-queue?client="+strings.ReplaceAll(prefix, " ", "%20"), nil, 200), &queue)
	reasons := map[string]any{}
	for _, item := range queue.Items {
		reasons[item["workpaper_id"].(string)] = item["bulk_reason"]
	}
	if reasons[routine] != "" || reasons[adjusting] != "Proposes a ledger adjustment" || len(queue.Items) != 3 {
		t.Fatalf("queue should flag drafts that need individual review: %v", reasons)
	}
	item := func(closeID, paperID, digest string) map[string]string {
		return map[string]string{"close_id": closeID, "workpaper_id": paperID, "digest": digest, "expected_review_id": ""}
	}
	var batch struct {
		Results []map[string]string `json:"results"`
	}
	json.Unmarshal(call("POST", "/v1/closes/review-batch", map[string]any{"note": "Synthetic bulk check", "items": []map[string]string{item(soon, routine, "routine"), item(soon, stale, "old digest"), item(soon, adjusting, "adjusting"), item(foreign, foreignPaper, "foreign")}}, 200), &batch)
	if len(batch.Results) != 4 || batch.Results[0]["review_id"] == "" || batch.Results[1]["error"] == "" || !strings.Contains(batch.Results[2]["error"], "individually") || !strings.Contains(batch.Results[3]["error"], "accessible") {
		t.Fatalf("each draft must be decided on its own checks: %+v", batch.Results)
	}
	var approvals int
	if err = db.QueryRow(`SELECT count(*) FROM workpaper_reviews WHERE workpaper_id IN ($1::bigint,$2::bigint,$3::bigint,$4::bigint) AND decision='approved'`, routine, stale, adjusting, foreignPaper).Scan(&approvals); err != nil || approvals != 1 {
		t.Fatalf("only the routine draft may be approved: %d %v", approvals, err)
	}
}
