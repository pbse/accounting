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
	"time"

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
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
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
	var early string
	content = "Earlier note " + rand.Text()
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'early.txt',$2,$3,'fixture') RETURNING id::text`, measured, preparation.Digest([]byte(content)), content).Scan(&early); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO work_replies(request_id,source_id,recorded_by,received_from,recorded_at) VALUES($1,$2,'fixture','client@example.test',now()-interval '4 hours')`, pending, early); err != nil {
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
	// Reviews after approval: #a approved, #b revises it unreviewed, #c revises #b and is reviewed; #d was only sent back
	// and #e revises it. Of four decisions, only #c's follows an approved earlier version. The quiet close's approved
	// draft and its reviewed revision count in the month's totals.
	draftIn := func(closeID, revises string) string {
		t.Helper()
		var id string
		paper := `{}`
		if revises != "" {
			paper = `{"revises":"` + revises + `"}`
		}
		if err := db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, rand.Text(), paper).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	review := func(id, decision string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'d',$2,'Reviewed','fixture')`, id, decision); err != nil {
			t.Fatal(err)
		}
	}
	a := draftIn(measured, "")
	review(a, "approved")
	c := draftIn(measured, draftIn(measured, a))
	review(c, "approved")
	d := draftIn(measured, "")
	review(d, "changes_requested")
	review(draftIn(measured, d), "approved")
	q := draftIn(quiet, "")
	review(q, "approved")
	review(draftIn(quiet, q), "changes_requested")
	// Half a minute of tracked preparation, earlier in the day, is not review time.
	if _, err = db.Exec(`INSERT INTO close_effort(close_id,actor,minute,activity,seconds) VALUES($1,'fixture',date_trunc('minute',now())-interval '3 hours','prepare',30)`, measured); err != nil {
		t.Fatal(err)
	}
	var trackedReview int64
	if err = db.QueryRow(`SELECT COALESCE(sum(seconds),0) FROM close_effort WHERE close_id=$1 AND activity='review'`, measured).Scan(&trackedReview); err != nil {
		t.Fatal(err)
	}
	type cohort struct {
		Eligible   int      `json:"eligible"`
		Measured   int      `json:"measured"`
		Median     *float64 `json:"median_minutes"`
		Recorded   int      `json:"recorded_minutes"`
		Assistance int      `json:"assistance_minutes"`
	}
	var report struct {
		Items         []map[string]any `json:"items"`
		Completed     cohort           `json:"completed"`
		InProgress    cohort           `json:"in_progress"`
		Reviews       int              `json:"reviews"`
		AfterApproval int              `json:"reviews_after_approval"`
		Trend         []struct {
			Period   string   `json:"period"`
			Measured int      `json:"measured"`
			Median   *float64 `json:"median_minutes"`
		} `json:"trend"`
	}
	json.Unmarshal(call("GET", "/v1/closes/effort?period="+period, nil, 200), &report)
	if in := report.InProgress; len(report.Items) != 2 || in.Eligible != 2 || in.Measured != 1 || in.Median == nil || *in.Median < 16.4 || *in.Median > 17.1 || in.Recorded != 15 || report.Completed.Eligible != 0 || report.Completed.Median != nil {
		t.Fatalf("effort so far uses measured open closes only, never the quiet close as zero, and nothing is completed yet: %+v", report)
	}
	if len(report.Trend) != 12 || report.Trend[11].Period != period || report.Trend[11].Measured != 0 {
		t.Fatalf("the trend counts signed-off closes only: %+v", report.Trend)
	}
	// A recorded sign-off that no longer stands (its digest is stale) leaves the close open.
	if _, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','stale','{}','Synthetic sign-off','fixture')`, measured); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, measured)
	defer db.Exec(`DELETE FROM close_scopes WHERE close_id=$1`, measured)
	report.Completed, report.InProgress = cohort{}, cohort{}
	json.Unmarshal(call("GET", "/v1/closes/effort?period="+period, nil, 200), &report)
	if report.Completed.Eligible != 0 || report.InProgress.Eligible != 2 {
		t.Fatalf("a sign-off changed since it was made is open work, not completed effort: %+v", report)
	}
	// A sign-off over the close's current readiness moves its effort to the completed cohort and the trend; our team's
	// help is part of the close's minutes and reported apart.
	call("POST", "/v1/closes/"+measured+"/effort/entries", map[string]any{"activity": "assistance", "minutes": 10, "note": "Set up the client's procedures"}, 200)
	var helped struct {
		Recorded   int64 `json:"recorded_minutes"`
		Assistance int64 `json:"assistance_minutes"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+measured+"/effort", nil, 200), &helped)
	if helped.Recorded != 25 || helped.Assistance != 10 {
		t.Fatalf("the close's recorded minutes include our team's help and name it: %+v", helped)
	}
	var current readiness
	json.Unmarshal(call("GET", "/v1/closes/"+measured+"/readiness", nil, 200), &current)
	call("POST", "/v1/closes/"+measured+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": "reopen", "note": "Recheck", "confirmed": true}, 200)
	json.Unmarshal(call("GET", "/v1/closes/"+measured+"/readiness", nil, 200), &current)
	call("POST", "/v1/closes/"+measured+"/scope", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "scope": closeScope{Description: "Effort fixture", RequiredPapers: []string{}, ExternalSteps: []externalStep{{Name: "Ledger review", Complete: true, Reference: "Reviewed"}}}}, 200)
	json.Unmarshal(call("GET", "/v1/closes/"+measured+"/readiness", nil, 200), &current)
	call("POST", "/v1/closes/"+measured+"/signoff", map[string]any{"digest": current.Digest, "expected_decision_id": current.DecisionID, "action": "sign", "note": "Synthetic sign-off", "confirmed": true}, 200)
	report.Completed, report.InProgress = cohort{}, cohort{}
	json.Unmarshal(call("GET", "/v1/closes/effort?period="+period, nil, 200), &report)
	if done := report.Completed; done.Eligible != 1 || done.Measured != 1 || done.Median == nil || *done.Median < 26.4 || *done.Median > 27.1 || done.Assistance != 10 || done.Recorded != 15 || report.InProgress.Eligible != 1 || report.InProgress.Measured != 0 || report.InProgress.Median != nil {
		t.Fatalf("a standing sign-off is completed effort, with our team's help apart; the quiet open close stays unmeasured: %+v", report)
	}
	if report.Trend[11].Measured != 1 || report.Trend[11].Median == nil || *report.Trend[11].Median != *report.Completed.Median {
		t.Fatalf("the trend ends with this month's signed-off closes and median: %+v", report.Trend)
	}
	if first, _ := time.Parse("2006-01", period); report.Trend[0].Period != first.AddDate(0, -11, 0).Format("2006-01") {
		t.Fatalf("the trend starts eleven months earlier, oldest first: %+v", report.Trend)
	}
	if report.Reviews != 6 || report.AfterApproval != 2 {
		t.Fatalf("the month counts every accessible close's reviews and those after an approved earlier version: %+v", report)
	}
	for _, item := range report.Items {
		if item["close_id"] == measured && (item["reviews"] != 4.0 || item["reviews_after_approval"] != 1.0 || item["review_minutes"] != float64(trackedReview)/60) {
			t.Fatalf("a close counts its reviews, those after approval through an unreviewed revision, and only review time: %+v", item)
		}
		if item["close_id"] == foreign {
			t.Fatal("another firm's close leaked into the effort report")
		}
		if item["close_id"] == quiet && item["total_minutes"] != nil {
			t.Fatal("an unmeasured close must not report zero minutes")
		}
	}
}
