package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPlanSaysWhoEachStepIsWith(t *testing.T) {
	states := func(steps []planStep) string {
		out := []string{}
		for _, s := range steps {
			out = append(out, s.Key+":"+s.State)
		}
		return strings.Join(out, " ")
	}
	connected := booksRead{source: "none", quickbooks: "active", daily: true, ended: true}
	for _, c := range []struct {
		books booksRead
		want  string
	}{
		{booksRead{source: "none", quickbooks: "active", daily: true}, "I read QuickBooks each day, and capture September 2026 once it ends."},
		{booksRead{source: "none", quickbooks: "active", daily: true, ended: true, sync: "queued"}, "Reading September 2026 from QuickBooks now."},
		{booksRead{source: "none", quickbooks: "active", daily: true, ended: true, sync: "failed", failure: "Transaction capture exceeds the 12 MiB per-close limit"}, "My last read of QuickBooks failed: Transaction capture exceeds the 12 MiB per-close limit I'll try again within a day."},
		{booksRead{source: "none", quickbooks: "active", ended: true}, "Capture September 2026 in the QuickBooks panel."},
		{booksRead{source: "none", xero: "active"}, "Capture September 2026 in the Xero panel."},
		{booksRead{source: "none", quickbooks: "reconnect_required", xero: "active", daily: true}, "I'll capture September 2026 from Xero within a day."},
		{booksRead{source: "none", quickbooks: "active", daily: true, ended: true, elsewhere: true}, "Capture September 2026 in the QuickBooks panel."},
	} {
		if got := booksStep(c.books, "7", "Acme", "2026-09").Text; got != c.want {
			t.Errorf("books %+v: %q", c.books, got)
		}
	}

	// A document that needs a person comes before one the agent is reading; review waits on both.
	p := goalProgress{Items: []goalItem{{Status: "needs_you"}, {Status: "working"}, {Status: "prepared"}, {Status: "stopped"}}, Needs: []string{"Review 1 held email reply on the Requests tab."}}
	plan := goalPlan(p, connected, "7", "Acme", "2026-09", "review")
	if got := states(plan); got != "books:agent documents:you accounts:waits verify:unknown review:waits" {
		t.Fatalf("plan states: %s", got)
	}
	if docs := plan[1]; docs.Text != "1 of 3 prepared, 1 need you, 1 in progress." || len(docs.Lines) != 1 || plan[4].Text != "Waits on 2 documents." {
		t.Fatalf("the documents say where they stand and review what it waits on: %+v %+v", docs, plan[4])
	}
	// Waiting on the client is the client's step once nothing needs a person.
	p = goalProgress{Items: []goalItem{{Status: "client"}, {Status: "prepared"}}, Needs: []string{}, Coverage: &goalCoverage{Total: 4, Open: 1}}
	if got := states(goalPlan(p, connected, "7", "Acme", "2026-09", "review")); got != "books:agent documents:client accounts:none verify:unknown review:waits" || !strings.Contains(states(goalPlan(p, connected, "7", "Acme", "2026-09", "signoff")), "accounts:you") {
		t.Fatalf("plan states: %s", got)
	}
	// A held reply needs a person even when every document is prepared.
	p = goalProgress{Items: []goalItem{{Status: "prepared"}}, Needs: []string{"Review 1 held email reply on the Requests tab."}}
	if docs := goalPlan(p, connected, "7", "Acme", "2026-09", "review")[1]; docs.State != "you" || docs.Lines[0] != p.Needs[0] {
		t.Fatalf("a close-wide need is the documents step's: %+v", docs)
	}
	// A sign-off goal approves every draft, then signs off: a draft awaiting approval is a prepared document, and coverage
	// past its limit is the sign-off step's.
	p = goalProgress{Items: []goalItem{{Status: "needs_you", Text: "Review and approve draft #3."}}, Needs: []string{coverageLimitNeed}, approve: 1, limited: true}
	plan = goalPlan(p, connected, "7", "Acme", "2026-09", "signoff")
	if got := states(plan); got != "books:agent documents:done accounts:unknown verify:unknown review:you signoff:you" || plan[1].Text != "1 of 1 prepared." || len(plan[1].Lines) != 0 || plan[5].Text != coverageLimitNeed || plan[4].Title != "Approve every draft" {
		t.Fatalf("sign-off plan: %s %+v", got, plan)
	}
	// Accounts read from an out-of-date capture wait on a new one rather than asking for decisions.
	p = goalProgress{Items: []goalItem{{Status: "prepared"}}, Needs: []string{}, Coverage: &goalCoverage{Total: 2, Open: 2}, accounts: &coverageView{Stale: true}}
	if accounts := goalPlan(p, connected, "7", "Acme", "2026-09", "signoff")[2]; accounts.State != "waits" {
		t.Fatalf("stale coverage waits on a capture: %+v", accounts)
	}
	p = goalProgress{Items: []goalItem{{Status: "prepared"}}, Needs: []string{}, Ready: true, Signoff: &goalSignoff{State: "open", Needs: []string{}}, Coverage: &goalCoverage{Total: 2}}
	if got := states(goalPlan(p, connected, "7", "Acme", "2026-09", "signoff")); got != "books:agent documents:done accounts:done verify:unknown review:done signoff:you" {
		t.Fatalf("ready to sign off: %s", got)
	}
}

func TestVerifyTakesTheBriefsChecks(t *testing.T) {
	lines := []briefLine{{Area: "ledger", State: "attention", Text: "QuickBooks' two reads differ."}, {Area: "preparation", State: "attention", Text: "1 need your confirmation."}, {Area: "balance sheet", State: "done", Text: "3 of 3 balance-sheet accounts tie."}, {Area: "exceptions", State: "done", Text: "The agent found 0 questions for the client."}}
	if s := verifyStep(lines, "/c", true); s.State != "you" || len(s.Lines) != 3 || s.Lines[0] != "QuickBooks' two reads differ." {
		t.Fatalf("once the books are read, reads that differ need a person: %+v", s)
	}
	if s := verifyStep(lines, "/c", false); s.State != "done" || len(s.Lines) != 2 {
		t.Fatalf("until then the books step says what is missing: %+v", s)
	}
}

func TestPlanReadsTheBooksTheLedgerReadsUse(t *testing.T) {
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
	period, entity := time.Now().UTC().Format("2006-01"), "Books "+rand.Text()
	var closeID, evidence, qbo, xeroID string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,$2) RETURNING id::text`, entity, period).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM quickbooks_snapshots WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`,
			`DELETE FROM xero_snapshots WHERE close_id=$1`, `DELETE FROM xero_connections WHERE close_id=$1`, `UPDATE evidence SET superseded_by=NULL WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
	}()
	plan := func() map[string]planStep {
		t.Helper()
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		must(err)
		defer tx.Rollback()
		p, err := readGoalProgress(ctx, tx, closeID, "review")
		must(err)
		b, err := readBooks(ctx, tx, closeID)
		must(err)
		out := map[string]planStep{}
		for _, s := range goalPlan(p, b, closeID, entity, period, "review") {
			out[s.Key] = s
		}
		return out
	}
	// Days are the ones the captures were taken on, so the test holds across midnight.
	day := func(query string) string {
		t.Helper()
		var at time.Time
		must(db.QueryRow(query, closeID).Scan(&at))
		return at.UTC().Format("Jan 2")
	}
	if s := plan(); s["books"].State != "none" || s["accounts"].Text != "Account coverage needs a QuickBooks capture of the month." {
		t.Fatalf("without a ledger the plan covers the documents only: %+v", s)
	}
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&xeroID))
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1',$3,'d','{}','fixture',now()-interval '1 hour')`, closeID, xeroID, period)
	must(err)
	if s := plan(); s["books"].Text != "Read from Xero's Trial Balance captured "+day(`SELECT captured_at FROM xero_snapshots WHERE close_id=$1`)+". I read it again each day." || !strings.Contains(s["accounts"].Text, "for this Xero close") || s["review"].Text != "Waits on the documents." {
		t.Fatalf("a Xero capture is read and account coverage says why it is not checked: %+v", s)
	}
	// A QuickBooks capture older than Xero's is not the one the ledger reads use; a newer one is.
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger.txt',$2,'Synthetic ledger','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,credential,environment) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123','\x00','sandbox' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&qbo))
	capture := func(ago string) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,jsonb_build_object('period',$4::text,'source_id',$5::text,'coverage','[]'::jsonb),now(),now()-$6::interval,'fixture')`, closeID, qbo, rand.Text(), period, evidence, ago)
		must(err)
	}
	capture("2 hours")
	if s := plan(); !strings.HasPrefix(s["books"].Text, "Read from Xero") {
		t.Fatalf("the newer Xero capture stays the one in use: %+v", s["books"])
	}
	capture("1 minute")
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,started_at,finished_at) VALUES($1,$2,$3,'fixture','unchanged',now(),now())`, closeID, qbo, period)
	must(err)
	captured, read := day(`SELECT max(captured_at) FROM quickbooks_snapshots WHERE close_id=$1`), day(`SELECT finished_at FROM quickbooks_entity_syncs WHERE close_id=$1`)
	if s := plan(); s["books"].State != "done" || !strings.HasSuffix(s["books"].Text, "captured "+captured+". Transactions read "+read+". I read them again each day.") || s["accounts"].State != "done" {
		t.Fatalf("the newest capture is QuickBooks', and an unchanged read still counts as read: %+v", s)
	}
	var replacement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'ledger 2.txt',$2,'Synthetic ledger','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&replacement))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id::text=$1`, evidence, replacement)
	must(err)
	if s := plan(); s["books"].State != "you" || !strings.Contains(s["books"].Text, "is out of date") {
		t.Fatalf("a capture whose source was replaced is out of date: %+v", s["books"])
	}
	// An active connection can read the books before a person must reconnect one.
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id::text=$1`, qbo)
	must(err)
	if s := plan(); s["books"].Text != "I'll capture "+time.Now().UTC().Format("January 2006")+" from Xero within a day." {
		t.Fatalf("an active Xero connection reads the books: %+v", s["books"])
	}
	_, err = db.Exec(`UPDATE xero_connections SET state='reconnect_required' WHERE id::text=$1`, xeroID)
	must(err)
	if s := plan(); s["books"].Text != "Reconnect QuickBooks for "+entity+" so I can read the books." {
		t.Fatalf("with no active connection, reconnecting is a person's: %+v", s["books"])
	}
	// Once the books are read from production, the sandbox capture is not used, and it keeps the scheduled first capture
	// away, so a person captures the month.
	_, err = db.Exec(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,credential,environment) SELECT id,organization_id,entity,'fixture','active',$2,now(),'456','\x00','production' FROM client_closes WHERE id=$1`, closeID, rand.Text())
	must(err)
	if s := plan(); s["books"].Text != "Capture "+time.Now().UTC().Format("January 2006")+" in the QuickBooks panel." {
		t.Fatalf("a capture on another connection leaves the month to a person: %+v", s["books"])
	}
}

func TestXeroReadsAnAgentsCloseWhateverItsMonth(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,$2) RETURNING id::text`, "Older "+rand.Text(), time.Now().UTC().AddDate(0, -4, 0).Format("2006-01")).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM close_goals WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	due := func() (ok bool) {
		t.Helper()
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM client_closes c WHERE c.id::text=$1 AND `+xeroDue+`)`, closeID).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if due() {
		t.Fatal("an older month is not read by itself")
	}
	if _, err = db.Exec(`INSERT INTO close_goals(close_id,author) VALUES($1,'user_a')`, closeID); err != nil {
		t.Fatal(err)
	}
	if !due() {
		t.Fatal("a close the agent works on is read whatever its month")
	}
	if _, err = db.Exec(`UPDATE close_goals SET state='reached',reached_at=now() WHERE close_id::text=$1`, closeID); err != nil || due() {
		t.Fatalf("a reached goal is not worked on: %v", err)
	}
}
