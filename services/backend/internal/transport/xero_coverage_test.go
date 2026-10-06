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

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// A close whose books are read from Xero asks the same coverage questions a QuickBooks close does: every account of the
// month's Xero capture needs a decision before sign-off, decisions name the Xero capture they were made on, the takeover
// proposes its scope from it, and a client request about its accounts stays current only on it. A newer QuickBooks
// capture takes over, as the ledger reads do; a Xero capture that cannot be read blocks sign-off until it is captured again.
func TestXeroCoverageAsksEveryAccountBeforeSignOff(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	var closeID, connection string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period,takeover) VALUES($1,'2026-09',true) RETURNING id::text`, "Xero coverage "+rand.Text()).Scan(&closeID))
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM client_request_links WHERE request_id IN (SELECT q.id FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE d.close_id=$1)`,
			`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, `DELETE FROM request_drafts WHERE close_id=$1`,
			`DELETE FROM coverage_bindings WHERE close_id=$1`, `DELETE FROM xero_snapshots WHERE close_id=$1`, `DELETE FROM xero_connections WHERE close_id=$1`,
			`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			if _, e := db.Exec(q, closeID); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection))
	month := `{"date":"2026-09-30","accounts":{"Accounts":[{"AccountID":"a-bank","Name":"Business Bank Account","Type":"BANK"},{"AccountID":"a-pre","Name":"Prepayments","Type":"PREPAYMENT"},{"AccountID":"a-rent","Name":"Rent","Type":"EXPENSE"}]},
	 "trial_balance":{"Reports":[{"Rows":[{"RowType":"Section","Rows":[{"RowType":"Row","Cells":[{"Value":"Business Bank Account","Attributes":[{"Value":"a-bank","Id":"account"}]}]}]}]}]}}`
	capture := func(period, payload, ago string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1',$3,$4,$5,'fixture',now()-$6::interval) RETURNING id::text`, closeID, connection, period, rand.Text(), payload, ago).Scan(&id))
		return id
	}
	first := capture("2026-09", month, "2 hours")

	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	view := func() coverageView {
		t.Helper()
		var got struct{ Coverage coverageView }
		must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/coverage", nil, 200), &got))
		return got.Coverage
	}
	ready := func() readiness {
		t.Helper()
		var r readiness
		must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/readiness", nil, 200), &r))
		return r
	}
	states := func(v coverageView) string {
		out := []string{}
		for _, item := range v.Items {
			out = append(out, item.AccountID+":"+item.Kind+":"+item.State)
		}
		return strings.Join(out, " ")
	}

	v := view()
	if v.SnapshotID != "xero:"+first || v.Stale || states(v) != "a-bank:reconciliation:missing_evidence a-pre:prepaid:missing_evidence a-rent:accountant_review:missing_evidence" {
		t.Fatalf("every account of the Xero capture needs a decision: %s %+v", v.SnapshotID, states(v))
	}
	if blockers := strings.Join(ready().Blockers, " | "); !strings.Contains(blockers, "Coverage for Business Bank Account: missing evidence.") || !strings.Contains(blockers, "Coverage for Rent: missing evidence.") {
		t.Fatalf("sign-off waits on each account: %s", blockers)
	}
	var takeover struct {
		Content struct {
			Scope struct {
				Items       []map[string]string `json:"items"`
				Unavailable string              `json:"unavailable"`
			} `json:"scope"`
		} `json:"content"`
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/takeover-setup", nil, 200), &takeover))
	if len(takeover.Content.Scope.Items) != 3 || takeover.Content.Scope.Unavailable != "" {
		t.Fatalf("the takeover proposes the account scope from the Xero capture: %+v", takeover.Content.Scope)
	}

	// An exclusion is recorded on the Xero capture it was made on.
	call("POST", "/v1/closes/"+closeID+"/coverage", map[string]any{"action": "bind", "digest": v.Digest, "account_id": "a-rent", "excluded": true, "reason": "Rent is reviewed with the lease outside this close.", "confirmed": true}, 200)
	var quickbooksSnapshot sql.NullString
	var xeroSnapshot string
	must(db.QueryRow(`SELECT snapshot_id::text,xero_snapshot_id::text FROM coverage_bindings WHERE close_id=$1`, closeID).Scan(&quickbooksSnapshot, &xeroSnapshot))
	if quickbooksSnapshot.Valid || xeroSnapshot != first || !strings.Contains(states(view()), "a-rent:accountant_review:excluded") {
		t.Fatalf("the exclusion names the Xero capture: %v %s %s", quickbooksSnapshot, xeroSnapshot, states(view()))
	}
	if signed := string(ready().Snapshot); !strings.Contains(signed, `"xero_snapshot_id":`+first) {
		t.Fatalf("readiness signs the Xero capture the exclusion was made on: %s", signed)
	}

	// A client request about the accounts stays current only on the capture it was approved on.
	var draft, request string
	must(db.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Please send the bank statement.','[]','synthetic','fixture') RETURNING id::text`, closeID).Scan(&draft))
	scope, _ := json.Marshal(map[string]any{"snapshot_id": "xero:" + first, "bindings": []map[string]string{{"account_id": "a-bank", "id": ""}}, "jobs": []string{}})
	must(db.QueryRow(`INSERT INTO work_requests(draft_id,revision,recipient,approved_by,coverage_scope) VALUES($1,0,'client@example.test','fixture',$2) RETURNING id::text`, draft, string(scope)).Scan(&request))
	current := func() bool {
		t.Helper()
		var ok bool
		must(db.QueryRow(`SELECT `+preparation.RequestCoverageIsCurrent+` FROM work_requests q JOIN request_drafts d ON d.id=q.draft_id WHERE q.id=$1`, request).Scan(&ok))
		return ok
	}
	if !current() {
		t.Fatal("a request on the Xero capture in use is current")
	}
	// With its link open, a request about the bank account is a close waiting on statements.
	tx, err := db.Begin()
	must(err)
	_, err = issueLink(context.Background(), tx, request, "fixture", rand.Text())
	must(err)
	must(tx.Commit())
	waiting := func() bool {
		t.Helper()
		var ok bool
		must(db.QueryRow(`SELECT `+waitingStatements+` FROM client_closes c WHERE c.id::text=$1`, closeID).Scan(&ok))
		return ok
	}
	if !waiting() {
		t.Fatal("a Xero close whose bank statement is requested waits on statements")
	}

	// A capture of another month does not propose this month's scope.
	second := capture("2026-08", month, "1 hour")
	takeover.Content.Scope.Items, takeover.Content.Scope.Unavailable = nil, ""
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/takeover-setup", nil, 200), &takeover))
	if v := view(); v.SnapshotID != "xero:"+second || current() || waiting() || len(takeover.Content.Scope.Items) != 0 || !strings.Contains(takeover.Content.Scope.Unavailable, "capture this month again") {
		t.Fatalf("a newer capture replaces the first; an August one is not September's: %s %+v", v.SnapshotID, takeover.Content.Scope)
	}

	// A newer QuickBooks capture is the books' capture, as the ledger reads use it.
	var evidence, quickbooksConnection, quickbooksID string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger.txt',$2,'Synthetic ledger','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&quickbooksConnection))
	must(db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,jsonb_build_object('period','2026-09','source_id',$4::text,'coverage',jsonb_build_array(jsonb_build_object('account_id','q-1','name','Checking','kind','reconciliation'))),now(),now()-interval '30 minutes','fixture') RETURNING id::text`, closeID, quickbooksConnection, rand.Text(), evidence).Scan(&quickbooksID))
	if v := view(); v.SnapshotID != quickbooksID || states(v) != "q-1:reconciliation:missing_evidence" {
		t.Fatalf("the newer QuickBooks capture is in use: %s %s", v.SnapshotID, states(v))
	}
	v = view()
	call("POST", "/v1/closes/"+closeID+"/coverage", map[string]any{"action": "bind", "digest": v.Digest, "account_id": "q-1", "excluded": true, "reason": "Reconciled by the client's bookkeeper.", "confirmed": true}, 200)
	// The QuickBooks decision is signed exactly as before Xero decisions existed: no Xero key on its row.
	var signed struct {
		Coverage []map[string]any `json:"coverage"`
	}
	must(json.Unmarshal(ready().Snapshot, &signed))
	for _, binding := range signed.Coverage {
		if _, has := binding["xero_snapshot_id"]; has != (binding["snapshot_id"] == nil) {
			t.Fatalf("only a Xero decision's signed row names a Xero capture: %+v", binding)
		}
	}

	// A Xero capture that cannot be read proposes nothing it could be taken to cover: it blocks sign-off until captured again.
	capture("2026-09", `{"date":"2026-09-30"}`, "1 minute")
	v = view()
	if !v.Stale || len(v.Items) != 1 || v.Items[0].Name != "the Xero capture" || v.Items[0].State != "source_changed" {
		t.Fatalf("an unreadable capture: %+v", v)
	}
	if blockers := strings.Join(ready().Blockers, " | "); !strings.Contains(blockers, "Coverage for the Xero capture: source changed.") {
		t.Fatalf("an unreadable capture blocks sign-off: %s", blockers)
	}
	call("POST", "/v1/closes/"+closeID+"/coverage", map[string]any{"action": "bind", "digest": v.Digest, "account_id": "", "excluded": true, "reason": "Nothing to see", "confirmed": true}, 409)
}

// An account of a Xero capture is handed to a teammate as a QuickBooks one is: the hand-off names the Xero capture, and
// ends once the account is decided or a new capture replaces the one it was made on.
func TestHandAXeroAccountToATeammate(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	ctx := context.Background()
	org := "org_XeroHand" + rand.Text()
	asFirm(t, map[string]string{"user_a": "partner", "user_b": "partner"}, org)
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Xero hand-off fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{`DELETE FROM close_goals WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM close_preparations WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM coverage_bindings WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM xero_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM xero_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`,
			`DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Errorf("cleanup %s: %v", q, e)
			}
		}
	})
	var alpha, connection string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&alpha))
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, alpha, rand.Text()).Scan(&connection))
	capture := func(ago string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09',$3,$4,'fixture',now()-$5::interval) RETURNING id::text`, alpha, connection, rand.Text(),
			`{"accounts":{"Accounts":[{"AccountID":"a-pre","Name":"Prepaid insurance","Type":"PREPAYMENT"},{"AccountID":"a-bank","Name":"Operating bank","Type":"BANK"}]},"trial_balance":{"Reports":[{"Rows":[]}]}}`, ago).Scan(&id))
		return id
	}
	first := capture("1 hour")
	store := savedCloses(db, strings.Repeat("a", 32))
	partner := []string{"workpapers:review", "clients:all"}
	call := func(subject, method, path string, body any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: partner}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, path, body, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	hand := func(account string, want int) {
		t.Helper()
		call("user_a", "POST", "/v1/closes/"+alpha+"/goal/handoffs", map[string]string{"account_id": account, "assignee": "user_b", "note": "Get the policy invoice"}, want)
	}
	call("user_a", "POST", "/v1/closes/"+alpha+"/goal", map[string]any{"confirmed": true}, 200)
	var goal string
	must(db.QueryRow(`SELECT id::text FROM close_goals WHERE close_id::text=$1`, alpha).Scan(&goal))
	last := func() string {
		t.Helper()
		var text string
		must(db.QueryRow(`SELECT events->-1->>'text' FROM close_goals WHERE id::text=$1`, goal).Scan(&text))
		return text
	}
	open := func() int {
		t.Helper()
		var n int
		must(db.QueryRow(`SELECT count(*) FROM goal_handoffs WHERE goal_id::text=$1 AND closed_at IS NULL`, goal).Scan(&n))
		return n
	}

	hand("a-pre", 200)
	var quickbooksSnapshot sql.NullString
	var xeroSnapshot string
	must(db.QueryRow(`SELECT snapshot_id::text,xero_snapshot_id::text FROM goal_handoffs WHERE goal_id::text=$1`, goal).Scan(&quickbooksSnapshot, &xeroSnapshot))
	if quickbooksSnapshot.Valid || xeroSnapshot != first {
		t.Fatalf("the hand-off names the Xero capture: %v %s", quickbooksSnapshot, xeroSnapshot)
	}
	// While its capture is the one in use, the hand-off stays open; deciding the account ends it.
	must(stepGoal(ctx, db, goal, nil))
	if open() != 1 {
		t.Fatalf("a hand-off on the capture in use stays open: %q", last())
	}
	_, err = db.Exec(`INSERT INTO coverage_bindings(close_id,xero_snapshot_id,account_id,job_ids,excluded,reason,actor) VALUES($1,$2,'a-pre','[]',true,'Policy expensed','user_b')`, alpha, first)
	must(err)
	must(stepGoal(ctx, db, goal, nil))
	if text := last(); text != "coverage for Prepaid insurance no longer needs anyone, so user_b's hand-off is closed." || open() != 0 {
		t.Fatalf("a decided account ends its hand-off: %q", text)
	}
	// A new Xero capture ends a hand-off made on the old one.
	hand("a-bank", 200)
	capture("1 minute")
	must(stepGoal(ctx, db, goal, nil))
	if text := last(); text != "The ledger capture changed, so user_b's hand-off of coverage for Operating bank is closed." || open() != 0 {
		t.Fatalf("a hand-off belongs to its capture: %q", text)
	}
}
