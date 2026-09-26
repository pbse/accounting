package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

func TestPortfolioGoalsReadDaysAndModifiers(t *testing.T) {
	monday := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	for text, want := range map[string]portfolioGoal{
		"Prepare my clients due Friday":    {action: "prepare", mine: true, due: "2026-10-02", compare: "by"},
		"prepare closes due on Monday":     {action: "prepare", due: "2026-09-28", compare: "on"},
		"stop work due before 2026-10-15":  {action: "stop", due: "2026-10-15", compare: "before"},
		"prepare everything due this week": {action: "prepare", due: "2026-10-04", compare: "by"},
		"prepare all due tomorrow":         {action: "prepare", due: "2026-09-29", compare: "by"},
		"recheck failed work":              {action: "recheck", state: "failed"},
		"stop work waiting on the client":  {action: "stop", state: "waiting_client"},
		"prepare all visible":              {action: "prepare", visible: true},
		"prepare my clients due sunday":    {action: "prepare", mine: true, due: "2026-10-04", compare: "by"},
	} {
		if got, err := parsePortfolioGoal(text, monday); err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", text, got, err, want)
		}
	}
	for _, text := range []string{"show my clients", "prepare Acme", "prepare due someday", "stop waiting on statements"} {
		if g, err := parsePortfolioGoal(text, monday); err == nil {
			t.Errorf("%q was read as %+v", text, g)
		}
	}
}

func TestPortfolioGoalsRunOnlyTheServerSelectedCloses(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_Goal" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Goal fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	defer func() {
		for _, q := range []string{`DELETE FROM portfolio_operations WHERE organization_id=$1`, `DELETE FROM recovery_actions WHERE organization_id=$1`, `DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	close := func(entity, period, due, owner string) string {
		t.Helper()
		var id, source string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period,due_on,owner) VALUES($1,$2,$3,$4,$5) RETURNING id::text`, org, entity, period, due, owner).Scan(&id))
		tx, e := db.BeginTx(context.Background(), nil)
		must(e)
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Invoice amount: 1200.00','fixture') RETURNING id::text`, id, "goal-"+id).Scan(&source))
		_, e = preparation.StartIntake(context.Background(), tx, id, source, "fixture")
		must(e)
		must(tx.Commit())
		return id
	}
	due := close("Alpha", "2026-09", "2026-10-02", "user_a")
	close("Alpha", "2026-08", "2026-10-09", "user_a")
	close("Alpha", "2026-07", "2026-10-01", "user_b")
	close("Beta", "2026-09", "2026-10-02", "user_a")
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) portfolioOperation {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		var op portfolioOperation
		json.Unmarshal(w.Body.Bytes(), &op)
		return op
	}
	reviewer := []string{"workpapers:review"}
	goal := map[string]string{"text": "Prepare my clients due Friday", "today": "2026-09-28"}
	op := call("user_a", nil, "POST", "/v1/closes/portfolio", goal, 200)
	if len(op.Items) != 1 || op.Items[0].CloseID != due || op.Items[0].Count != 1 || op.Items[0].State != "pending" || !strings.Contains(op.Scope, "that you own, due on or before Fri 2 Oct 2026") || op.HasMore {
		t.Fatalf("the preview selects only the member's accessible close due by Friday: %+v", op)
	}
	if visible := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "prepare all visible", "view": "running", "client": "2026-08"}, 200); len(visible.Items) != 1 || visible.Items[0].Label != "Alpha · 2026-08" || !strings.Contains(visible.Scope, "running view matching “2026-08”") {
		t.Fatalf("all visible uses the inbox's view and client search, not the visible list: %+v", visible)
	}
	call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "prepare all visible", "view": "everything"}, 422)
	path := "/v1/closes/portfolio/run"
	call("user_b", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 404)
	call("user_a", nil, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 403)
	call("user_a", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": false}, 422)
	pending := func() int {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/portfolio", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_a"}, Organization: org}))
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		var out struct{ Operations []portfolioOperation }
		must(json.Unmarshal(w.Body.Bytes(), &out))
		return len(out.Operations)
	}
	if pending() != 2 {
		t.Fatal("each unfinished goal is listed to resume")
	}
	ran := call("user_a", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 200)
	var run, actor string
	must(db.QueryRow(`SELECT id::text,actor FROM close_preparations WHERE close_id::text=$1`, due).Scan(&run, &actor))
	if ran.Items[0].State != "done" || ran.Items[0].Result != "Coordinated 1 source document as run #"+run+"." || actor != "user_a" {
		t.Fatalf("the run prepares the close through its own action, as the member: %+v %s", ran.Items, actor)
	}
	if again := call("user_a", reviewer, "POST", path, map[string]any{"id": op.ID, "confirmed": true}, 200); again.Items[0].Result != ran.Items[0].Result || pending() != 1 {
		t.Fatal("a finished goal runs nothing again and is no longer listed")
	}
	repeat := call("user_a", nil, "POST", "/v1/closes/portfolio", goal, 200)
	if repeat = call("user_a", reviewer, "POST", path, map[string]any{"id": repeat.ID, "confirmed": true}, 200); repeat.Items[0].Result != "Already being prepared as run #"+run+"." {
		t.Fatalf("a goal repeated over the same scope claims no new run: %+v", repeat.Items)
	}
	stop := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "stop my clients due Friday", "today": "2026-09-28"}, 200)
	if len(stop.Items) != 1 || stop.Items[0].Count != 1 {
		t.Fatalf("the stop preview counts the unresolved job: %+v", stop)
	}
	_, err = db.Exec(`UPDATE preparation_jobs SET state='failed' WHERE close_id::text=$1`, due)
	must(err)
	call("user_a", reviewer, "POST", path, map[string]any{"id": stop.ID, "confirmed": true}, 422)
	stale := call("user_a", reviewer, "POST", path, map[string]any{"id": stop.ID, "confirmed": true, "reason": "Client paused the engagement"}, 200)
	var state string
	must(db.QueryRow(`SELECT state FROM preparation_jobs WHERE close_id::text=$1`, due).Scan(&state))
	if stale.Items[0].State != "failed" || !strings.Contains(stale.Items[0].Result, "Work changed") || state != "failed" {
		t.Fatalf("work that changed since the preview is not stopped: %+v %s", stale.Items, state)
	}
	recheck := call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "recheck failed work", "today": "2026-09-28"}, 200)
	if len(recheck.Items) != 1 || recheck.Items[0].CloseID != due {
		t.Fatalf("failed work selects the close with a failed job: %+v", recheck)
	}
	// A refusal after the action has written leaves none of its writes behind.
	var revision, actions int
	must(db.QueryRow(`SELECT i.revision FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id::text=$1`, due).Scan(&revision))
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=1 WHERE id=$1`, org)
	must(err)
	limited := call("user_a", reviewer, "POST", path, map[string]any{"id": recheck.ID, "confirmed": true}, 200)
	var after int
	must(db.QueryRow(`SELECT i.revision,(SELECT count(*) FROM recovery_actions WHERE close_id=j.close_id) FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id WHERE j.close_id::text=$1`, due).Scan(&after, &actions))
	if limited.Items[0].State != "failed" || !strings.Contains(limited.Items[0].Result, "active preparation limit") || after != revision || actions != 0 {
		t.Fatalf("a recheck refused mid-way records the refusal and none of its writes: %+v revision %d→%d actions %d", limited.Items, revision, after, actions)
	}
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=DEFAULT WHERE id=$1`, org)
	must(err)
	recheck = call("user_a", nil, "POST", "/v1/closes/portfolio", map[string]string{"text": "recheck failed work", "today": "2026-09-28"}, 200)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',false,'Synthetic revocation','fixture')`, org)
	must(err)
	revoked := call("user_a", reviewer, "POST", path, map[string]any{"id": recheck.ID, "confirmed": true}, 200)
	if revoked.Items[0].State != "failed" || revoked.Items[0].Result != "" || revoked.Items[0].CloseID != "" || revoked.Items[0].Label != "A close you can no longer access" {
		t.Fatalf("a close the member lost access to is not rechecked, and its name is hidden: %+v", revoked.Items)
	}
	var result string
	must(db.QueryRow(`SELECT result FROM portfolio_items WHERE operation_id::text=$1`, recheck.ID).Scan(&result))
	if result != "You can no longer access this close." {
		t.Fatalf("the refusal is recorded: %q", result)
	}
}
