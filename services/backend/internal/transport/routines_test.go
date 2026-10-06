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
	"github.com/golang-jwt/jwt/v5"
)

func TestEndedMonthCountsFromTheFirstOfTheMonth(t *testing.T) {
	for now, want := range map[string]string{"2026-03-31": "2026-02", "2026-03-01": "2026-02", "2027-01-15": "2026-12"} {
		day, _ := time.Parse("2006-01-02", now)
		if got := endedMonth(day); got != want {
			t.Errorf("%s: %s, want %s", now, got, want)
		}
	}
}

func TestMonthlyHandOverOpensAndHandsOverEachNewMonth(t *testing.T) {
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
	ctx := context.Background()
	// month(n) is the month n after the current one, and on(n, day) that day of it.
	now := time.Now().UTC()
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	month := func(n int) string { return first.AddDate(0, n, 0).Format("2006-01") }
	on := func(n, day int) time.Time { return first.AddDate(0, n, day-1) }
	org := "org_Month" + rand.Text()
	asFirm(t, map[string]string{"user_a": "reviewer", "user_admin": "partner"}, org)
	var setUp string
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Routine fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_admin',true,'Synthetic','fixture')`,
		`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture'),($1,'user_a','Beta',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha',$2) RETURNING id::text`, org, month(0)).Scan(&setUp))
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_routines WHERE organization_id=$1`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_deletion_receipts WHERE organization_id=$1`, `DELETE FROM client_roster WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`, `DELETE FROM close_templates WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	store := savedCloses(db, strings.Repeat("a", 32))
	call := func(subject string, permissions []string, method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	reviewer := []string{"workpapers:review"}
	type status struct {
		Goal *struct {
			Events []struct {
				Text string `json:"text"`
			} `json:"events"`
		} `json:"goal"`
		Routine *closeRoutine `json:"routine"`
	}
	read := func(closeID string) status {
		t.Helper()
		var s status
		must(json.Unmarshal(call("user_a", nil, "GET", "/v1/closes/"+closeID+"/goal", nil, 200), &s))
		return s
	}
	closeOf := func(period string) string {
		t.Helper()
		var id string
		err := db.QueryRow(`SELECT id::text FROM client_closes WHERE organization_id=$1 AND entity='Alpha' AND period=$2`, org, period).Scan(&id)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		return id
	}
	goals := func(closeID string) int {
		t.Helper()
		var n int
		must(db.QueryRow(`SELECT count(*) FROM close_goals WHERE close_id::text=$1`, closeID).Scan(&n))
		return n
	}

	// Handing this month over every month keeps its due date's distance after month end.
	due := monthEnd(month(0)).AddDate(0, 0, 70).Format("2006-01-02")
	if body := call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/goal", map[string]any{"confirmed": true, "finish": "signoff", "due_on": due, "every_month": true}, 422); !strings.Contains(string(body), "at most 60 days") {
		t.Fatalf("a monthly due date more than 60 days after month end is refused: %s", body)
	}
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/goal", map[string]any{"confirmed": true, "finish": "signoff", "every_month": true}, 200)
	s := read(setUp)
	if s.Routine == nil || s.Routine.Author != "user_a" || s.Routine.Finish != "signoff" || s.Routine.DueDays != nil || s.Routine.Next != month(1) || !strings.HasSuffix(s.Goal.Events[0].Text, "Every new month of Alpha will be handed over too") {
		t.Fatalf("the routine is set up with the hand-over, with no monthly due date: %+v %+v", s.Routine, s.Goal)
	}
	_, err = db.Exec(`UPDATE close_routines SET due_days=10 WHERE organization_id=$1`, org)
	must(err)
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/goal/stop", map[string]string{"reason": "Checking the routine"}, 200)
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/goal", map[string]any{"confirmed": true, "every_month": true}, 409)

	// Alpha is on the firm's roster, whose monthly close template dates each month 7 days after it ends and gives it to
	// the administrator.
	_, err = db.Exec(`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Alpha',true,$2,'Synthetic','fixture')`, org, month(0))
	must(err)
	_, err = db.Exec(`INSERT INTO close_templates(organization_id,due_days,owner,reason,actor) VALUES($1,7,'user_admin','Synthetic','fixture')`, org)
	must(err)

	// When the next month ends its close is opened and handed over once; a stopped goal is not restarted.
	sweepCloseRoutines(ctx, db, on(1, 15))
	if closeOf(month(1)) != "" {
		t.Fatal("a month is not handed over before it ends")
	}
	if n, err := handOverMonths(ctx, db, on(2, 1)); err != nil || n < 1 {
		t.Fatalf("the hourly upkeep hands the ended month over: %d %v", n, err)
	}
	next := closeOf(month(1))
	if next == "" || goals(next) != 1 {
		t.Fatalf("the ended month's close is opened and handed over: %q %d", next, goals(next))
	}
	var dueOn, owner string
	must(db.QueryRow(`SELECT due_on::text,owner FROM client_closes WHERE id::text=$1`, next).Scan(&dueOn, &owner))
	if dueOn != monthEnd(month(1)).AddDate(0, 0, 7).Format("2006-01-02") || owner != "user_admin" {
		t.Fatalf("the month the routine opens takes the firm's template: %s %s", dueOn, owner)
	}
	var by string
	must(db.QueryRow(`SELECT r.started_by FROM workflow_runs r JOIN close_goals g ON r.idempotency_key='goal:'||g.id AND r.workflow='close_goal' WHERE g.close_id::text=$1`, next).Scan(&by))
	if !strings.HasPrefix(by, "routine:") {
		t.Fatalf("the routine's goal gets its run: %q", by)
	}
	if s = read(next); s.Goal == nil || s.Goal.Events[0].Text != "Handed over by the monthly hand-over user_a set up, to get it signed off by "+monthEnd(month(1)).AddDate(0, 0, 10).Format("Mon 2 Jan 2006") || s.Routine.Next != month(2) {
		t.Fatalf("the goal follows the routine: %+v %+v", s.Goal, s.Routine)
	}
	call("user_a", reviewer, "POST", "/v1/closes/"+next+"/goal/stop", map[string]string{"reason": "Doing this month myself"}, 200)
	sweepCloseRoutines(ctx, db, on(2, 2))
	if goals(next) != 1 {
		t.Fatal("a month is handed over once")
	}
	_, err = db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, next)
	must(err)
	sweepCloseRoutines(ctx, db, on(2, 3))
	if closeOf(month(1)) != "" {
		t.Fatal("a deleted month is not opened again")
	}

	// A signed-off month is skipped. At the firm's close limit the routine waits and says why; with room again it
	// catches up one month at a time, skipping none.
	var signed string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha',$2) RETURNING id::text`, org, month(2)).Scan(&signed))
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','fixture','{}','Done','fixture')`, signed)
	must(err)
	sweepCloseRoutines(ctx, db, on(3, 1))
	if goals(signed) != 0 || read(setUp).Routine.Next != month(3) {
		t.Fatalf("a signed-off month is not handed over: %d %+v", goals(signed), read(setUp).Routine)
	}
	var limit string
	must(db.QueryRow(`UPDATE organizations SET max_closes=(SELECT count(*) FROM client_closes WHERE organization_id=$1) WHERE id=$1 RETURNING max_closes::text`, org).Scan(&limit))
	sweepCloseRoutines(ctx, db, on(5, 1))
	if s = read(setUp); closeOf(month(3)) != "" || !strings.HasPrefix(s.Routine.Waiting, "The firm is at its limit of "+limit+" closes, so Alpha "+month(3)+" is not open yet") {
		t.Fatalf("at the limit no close is opened and the routine says why: %+v", s.Routine)
	}
	_, err = db.Exec(`UPDATE organizations SET max_closes=500 WHERE id=$1`, org)
	must(err)
	sweepCloseRoutines(ctx, db, on(5, 1))
	sweepCloseRoutines(ctx, db, on(5, 1))
	for _, n := range []int{3, 4} {
		if id := closeOf(month(n)); id == "" || goals(id) != 1 {
			t.Fatalf("with room again %s is opened and handed over: %q", month(n), id)
		}
	}
	if s = read(setUp); s.Routine.Waiting != "" || s.Routine.Next != month(5) {
		t.Fatalf("the routine has caught up: %+v", s.Routine)
	}

	// Losing the client assignment stops the routine before it opens anything.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',false,'Moved to another client','fixture')`, org)
	must(err)
	sweepCloseRoutines(ctx, db, on(6, 1))
	var reason string
	must(db.QueryRow(`SELECT COALESCE(stop_reason,'') FROM close_routines WHERE organization_id=$1`, org).Scan(&reason))
	if reason != "Stopped because user_a can no longer act for Alpha." || closeOf(month(5)) != "" {
		t.Fatalf("a lost assignment stops the routine: %q %q", reason, closeOf(month(5)))
	}

	// A person stops a routine with a reason; deleting a close for contract termination stops the client's routine.
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Back on the client','fixture')`, org)
	must(err)
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/goal", map[string]any{"confirmed": true, "every_month": true}, 200)
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/routine/stop", map[string]string{"reason": " "}, 422)
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/routine/stop", map[string]string{"reason": "Quarterly from now on"}, 200)
	call("user_a", reviewer, "POST", "/v1/closes/"+setUp+"/routine/stop", map[string]string{"reason": "Again"}, 409)
	if read(setUp).Routine != nil {
		t.Fatal("a stopped routine is no longer shown")
	}
	// Set up on a month that ended earlier, a routine starts with the current month instead of catching up.
	var earlier string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha',$2) RETURNING id::text`, org, month(-2)).Scan(&earlier))
	call("user_a", reviewer, "POST", "/v1/closes/"+earlier+"/goal", map[string]any{"confirmed": true, "every_month": true}, 200)
	if s = read(earlier); s.Routine == nil || s.Routine.Next != month(0) {
		t.Fatalf("the routine starts with the current month: %+v", s.Routine)
	}
	admin := []string{"clients:all", "data:manage", "workpapers:review"}
	call("user_admin", admin, "POST", "/v1/closes/"+signed+"/retention", map[string]any{"retain_until": "2000-01-01", "legal_hold": false, "expected_version": 0, "reason": "Approved synthetic retention policy", "confirmed": true}, 200)
	var retention retentionStatus
	must(json.Unmarshal(call("user_admin", admin, "GET", "/v1/closes/"+signed+"/retention", nil, 200), &retention))
	// Alpha is also on the roster under a name since confirmed as its alias: leaving takes every name off, so the alias,
	// forgotten with the relationship, is not left behind as a client that still owes months.
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by,confirmed_at) VALUES($1,'Alpha','alpha group','fixture',now()-interval '1 day')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_roster(organization_id,client_entity,active,since,reason,actor) VALUES($1,'Alpha Group',true,$2,'Synthetic','fixture')`, org, month(0))
	must(err)
	call("user_admin", admin, "POST", "/v1/closes/"+signed+"/delete", map[string]any{"expected_version": retention.Version, "entity": "Alpha", "period": month(2), "reason_code": "contract_termination", "confirmed": true}, 200)
	var stillActive int
	must(db.QueryRow(`SELECT count(*) FROM current_client_roster WHERE organization_id=$1 AND active`, org).Scan(&stillActive))
	if stillActive != 0 {
		t.Fatalf("every name the client was on the roster under leaves with it: %d still active", stillActive)
	}
	if read(setUp).Routine != nil {
		t.Fatal("contract termination stops the client's monthly hand-over")
	}
	var active bool
	var left string
	must(db.QueryRow(`SELECT active,reason FROM current_client_roster WHERE organization_id=$1 AND client_entity='Alpha'`, org).Scan(&active, &left))
	if active || !strings.Contains(left, "contract termination") {
		t.Fatalf("contract termination takes the client off the roster: %v %s", active, left)
	}
}

func TestMonthlyHandOversTakeTurns(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Fifty routines of a firm at its close limit wait; another firm's waiting routine still gets its turn in the same pass,
	// and the pass ends once each has been tried.
	busy, free := "org_Busy"+rand.Text(), "org_Free"+rand.Text()
	asFirm(t, map[string]string{"user_a": "partner"}, busy, free)
	defer func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id IN ($1,$2))`,
			`DELETE FROM close_routines WHERE organization_id IN ($1,$2)`, `DELETE FROM client_closes WHERE organization_id IN ($1,$2)`,
			`DELETE FROM organization_member_access WHERE organization_id IN ($1,$2)`, `DELETE FROM workflow_runs WHERE organization_id IN ($1,$2)`, `DELETE FROM organizations WHERE id IN ($1,$2)`} {
			db.Exec(q, busy, free)
		}
	}()
	must := func(_ sql.Result, e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(db.Exec(`INSERT INTO organizations(id,name,max_closes) VALUES($1,'Busy fixture',1),($2,'Free fixture',500)`, busy, free))
	must(db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($2,'user_a',true,'Synthetic','fixture')`, busy, free))
	must(db.Exec(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Existing','2026-01')`, busy))
	must(db.Exec(`INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,last_period,waiting) SELECT $1,'Client '||n,'user_a',false,'review','2026-09','Earlier' FROM generate_series(1,50) n`, busy))
	must(db.Exec(`INSERT INTO close_routines(organization_id,client_entity,author,via_grant,finish,last_period,waiting) VALUES($1,'Alpha','user_a',false,'review','2026-09','Earlier')`, free))
	november := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	if handed, err := handOverMonths(t.Context(), db, november); err != nil || handed < 1 {
		t.Fatalf("one pass reaches past the waiting routines: %d %v", handed, err)
	}
	var opened bool
	if err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM client_closes WHERE organization_id=$1 AND entity='Alpha' AND period='2026-10')`, free).Scan(&opened); err != nil || !opened {
		t.Fatalf("the other firm's routine gets its turn: %v %v", opened, err)
	}
}
