package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
)

func TestEffortCapsCreditAndSeparatesWaiting(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	period := "2031-0" + string("123456789"[rand.Text()[0]%9])
	foreignOrg := "org_" + rand.Text()
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Other firm')`, foreignOrg); err != nil {
		t.Fatal(err)
	}
	var measured, quiet, foreign string
	for _, c := range []struct {
		entity, org string
		id          *string
	}{{"Effort measured " + rand.Text(), "local-development", &measured}, {"Effort quiet " + rand.Text(), "local-development", &quiet}, {"Effort foreign " + rand.Text(), foreignOrg, &foreign}} {
		if err = db.QueryRow(`INSERT INTO client_closes(entity,period,organization_id) VALUES($1,$2,$3) RETURNING id::text`, c.entity, period, c.org).Scan(c.id); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, id := range []string{measured, quiet, foreign} {
			db.Exec(`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM request_drafts WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM close_effort WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM close_effort_entries WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organizations WHERE id=$1`, foreignOrg)
	}()
	token := strings.Repeat("e", 32)
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
	for _, activity := range []string{"prepare", "review", "review"} {
		call("POST", "/v1/closes/"+measured+"/effort", map[string]any{"activity": activity, "seconds": 30}, 204)
	}
	call("POST", "/v1/closes/"+measured+"/effort", map[string]any{"activity": "napping", "seconds": 30}, 422)
	call("POST", "/v1/closes/"+measured+"/effort", map[string]any{"activity": "review", "seconds": 45}, 422)
	call("POST", "/v1/closes/"+foreign+"/effort", map[string]any{"activity": "review", "seconds": 30}, 404)
	call("POST", "/v1/closes/"+measured+"/effort/entries", map[string]any{"activity": "configuration", "minutes": 15, "note": "Mapped the client's chart of accounts offline"}, 200)

	var draft string
	if err = db.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send statements','[]','fixture','fixture') RETURNING id::text`, measured).Scan(&draft); err != nil {
		t.Fatal(err)
	}
	var answered, pending string
	for i, target := range []*string{&answered, &pending} {
		if err = db.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by,delivered_at) VALUES($1,$2,'client@example.test','fixture',now()-interval '3 hours') RETURNING id::text`, draft, i+1).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by) VALUES($1,3,'client@example.test','fixture')`, draft); err != nil {
		t.Fatal(err)
	}
	var reply string
	content := "Statement attached " + rand.Text()
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'reply.txt',$2,$3,'fixture') RETURNING id::text`, measured, preparation.Digest([]byte(content)), content).Scan(&reply); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO work_replies(request_id,source_id,recorded_by,received_from,recorded_at) VALUES($1,$2,'fixture','client@example.test',now()-interval '1 hour')`, answered, reply); err != nil {
		t.Fatal(err)
	}
	var summary struct {
		Tracked  int64            `json:"tracked_seconds"`
		By       map[string]int64 `json:"tracked_by_activity"`
		Recorded int64            `json:"recorded_minutes"`
		Answered int64            `json:"answered_wait_seconds"`
		Open     int              `json:"open_requests"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+measured+"/effort", nil, 200), &summary)
	var rows, largest int64
	if err = db.QueryRow(`SELECT count(*),max(seconds) FROM close_effort WHERE close_id=$1`, measured).Scan(&rows, &largest); err != nil {
		t.Fatal(err)
	}
	if largest > 60 || summary.Tracked != min(90, 60*rows) || summary.Recorded != 15 || summary.Open != 1 || summary.Answered < 7190 || summary.Answered > 7210 {
		t.Fatalf("credit must cap at 60s per person-minute and waiting must count only answered requests: %+v", summary)
	}
	var report struct {
		Items    []map[string]any `json:"items"`
		Measured int              `json:"measured"`
		Median   *float64         `json:"median_minutes"`
	}
	json.Unmarshal(call("GET", "/v1/closes/effort?period="+period, nil, 200), &report)
	if len(report.Items) != 2 || report.Measured != 1 || report.Median == nil || *report.Median < 15.9 || *report.Median > 16.6 {
		t.Fatalf("median must use measured closes only and never count the quiet close as zero: %+v", report)
	}
	for _, item := range report.Items {
		if item["close_id"] == foreign {
			t.Fatal("another firm's close leaked into the effort report")
		}
		if item["close_id"] == quiet && item["total_minutes"] != nil {
			t.Fatal("an unmeasured close must not report zero minutes")
		}
	}
}
