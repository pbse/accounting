package quickbooks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestTransactionChecksReuseEvidenceAndRefreshIndexedRows(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	org := "org_sync" + rand.Text()
	period := time.Now().UTC().Format("2006-01")
	var closeID, connectionID string
	if _, err = db.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,'Synthetic source check')`, org); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, org, "Source check "+org, period).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,credential,environment) VALUES($1,$2,$3,'fixture','active',$4,now()+interval '1 day','123',decode('0102','hex'),'sandbox') RETURNING id::text`, closeID, org, "Source check "+org, rand.Text()).Scan(&connectionID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE id=$1`, connectionID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	job := func() syncClaim {
		t.Helper()
		j := syncClaim{closeID: closeID, connectionID: connectionID, period: period, realm: "123", token: rand.Text()}
		if err := db.QueryRowContext(ctx, `INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,claim,attempts) VALUES($1,$2,$3,'fixture','running',$4,1) RETURNING id::text`, closeID, connectionID, period, j.token).Scan(&j.id); err != nil {
			t.Fatal(err)
		}
		return j
	}
	capture := func(revision string) EntityCapture {
		item := json.RawMessage(fmt.Sprintf(`{"Id":"bill-1","SyncToken":%q,"TxnDate":%q,"MetaData":{"LastUpdatedTime":"2026-09-01T12:00:00Z"},"TotalAmt":100,"Line":[{"AccountBasedExpenseLineDetail":{"AccountRef":{"value":"1","name":"Uncategorized Expense"}}}]}`, revision, period+"-01"))
		return EntityCapture{Realm: "123", Period: period, Types: []string{"Bill"}, Counts: map[string]int{"Bill": 1}, Records: map[string][]json.RawMessage{"Bill": {item}}, Settings: json.RawMessage(`{"Preferences":{"Id":"1","SyncToken":"1"}}`), StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(), Completeness: "synthetic checked read"}
	}
	first := job()
	if err := completeSync(ctx, db, first, capture("1")); err != nil {
		t.Fatal(err)
	}
	var indexed, sources int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM quickbooks_transaction_rows WHERE sync_id=$1`, first.id).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("first capture lost indexed row: %d %v", indexed, err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM quickbooks_transaction_rows WHERE sync_id=$1`, first.id); err != nil {
		t.Fatal(err)
	}
	second := job()
	if err := completeSync(ctx, db, second, capture("1")); err != nil {
		t.Fatal(err)
	}
	var state, priorSource, sameSource string
	if err := db.QueryRowContext(ctx, `SELECT state,evidence_id::text FROM quickbooks_entity_syncs WHERE id=$1`, second.id).Scan(&state, &sameSource); err != nil || state != "completed" {
		t.Fatalf("legacy capture was not reindexed: %s %v", state, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM quickbooks_transaction_rows WHERE sync_id=$1`, second.id).Scan(&indexed); err != nil || indexed != 1 {
		t.Fatalf("legacy capture did not gain indexed row: %d %v", indexed, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT evidence_id::text FROM quickbooks_entity_syncs WHERE id=$1`, first.id).Scan(&priorSource); err != nil || priorSource != sameSource {
		t.Fatal("unchanged check lost source identity", err)
	}
	third := job()
	if err := completeSync(ctx, db, third, capture("1")); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT state FROM quickbooks_entity_syncs WHERE id=$1`, third.id).Scan(&state); err != nil || state != "unchanged" {
		t.Fatalf("same indexed content created a revision: %s %v", state, err)
	}
	// An unchanged capture keeps its own open items; a bill now past due raises a question without new evidence.
	sixth := job()
	withOpen := capture("1")
	withOpen.OpenItems = map[string][]json.RawMessage{"Bill": {json.RawMessage(`{"Id":"b9","DocNumber":"B-9","DueDate":"` + time.Now().UTC().AddDate(0, -3, 0).Format("2006-01-02") + `","Balance":75,"VendorRef":{"value":"4","name":"Paper Co"}}`)}}
	if err := completeSync(ctx, db, sixth, withOpen); err != nil {
		t.Fatal(err)
	}
	var kinds string
	if err := db.QueryRowContext(ctx, `SELECT state||':'||(SELECT string_agg(e->>'type',',' ORDER BY e->>'type') FROM jsonb_array_elements(exceptions) e)||':'||jsonb_array_length(payload->'open_items'->'Bill') FROM quickbooks_entity_syncs WHERE id=$1`, sixth.id).Scan(&kinds); err != nil || kinds != "unchanged:past_due_payable,uncategorized:1" {
		t.Fatalf("open items change the questions, not the transaction evidence: %q %v", kinds, err)
	}
	// Each finished capture records its exceptions; an unchanged one carries the previous capture's.
	for _, j := range []syncClaim{second, third} {
		var kinds string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT string_agg(e->>'type',',') FROM jsonb_array_elements(exceptions) e),'none') FROM quickbooks_entity_syncs WHERE id=$1`, j.id).Scan(&kinds); err != nil || kinds != "uncategorized" {
			t.Fatalf("capture %s must record its exceptions: %q %v", j.id, kinds, err)
		}
	}
	fourth := job()
	if err := completeSync(ctx, db, fourth, capture("2")); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM evidence WHERE close_id=$1`, closeID).Scan(&sources); err != nil || sources != 2 {
		t.Fatalf("changed capture did not preserve both revisions: %d %v", sources, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM evidence WHERE id=$1 AND superseded_by IS NOT NULL`, priorSource).Scan(&sources); err != nil || sources != 1 {
		t.Fatal("prior evidence was not superseded", err)
	}
	var revision string
	if err := db.QueryRowContext(ctx, `SELECT revision FROM quickbooks_transaction_rows WHERE sync_id=$1 AND kind='Bill' AND provider_id='bill-1'`, fourth.id).Scan(&revision); err != nil || revision != "2" {
		t.Fatal("read model did not move to provider revision 2", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM quickbooks_transaction_rows WHERE sync_id=$1`, second.id).Scan(&indexed); err != nil || indexed != 0 {
		t.Fatal("historical indexed rows were retained after refresh", err)
	}
	// A capture finished before exceptions were recorded is re-indexed, not reused as unchanged.
	if _, err := db.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET exceptions=NULL WHERE id=$1`, fourth.id); err != nil {
		t.Fatal(err)
	}
	fifth := job()
	if err := completeSync(ctx, db, fifth, capture("2")); err != nil {
		t.Fatal(err)
	}
	var recorded bool
	if err := db.QueryRowContext(ctx, `SELECT state,exceptions IS NOT NULL FROM quickbooks_entity_syncs WHERE id=$1`, fifth.id).Scan(&state, &recorded); err != nil || state != "completed" || !recorded {
		t.Fatalf("a capture without recorded exceptions must not be reused: %s %v %v", state, recorded, err)
	}
}

func TestChangeHintsRefreshAfterTheCaptureThatMissedThem(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("QBO_CLIENT_ID", "synthetic")
	t.Setenv("QBO_CLIENT_SECRET", "synthetic")
	t.Setenv("QBO_REDIRECT_URI", "https://api.example.test/v1/quickbooks/callback")
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org := "org_hint" + rand.Text()
	var closeID, connectionID string
	_, err = db.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,'Synthetic change hints')`, org)
	must(err)
	must(db.QueryRowContext(ctx, `INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Hint client',$2) RETURNING id::text`, org, time.Now().UTC().Format("2006-01")).Scan(&closeID))
	must(db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,credential,environment) VALUES($1,$2,'Hint client','fixture','active',$3,now(),'123','\x00','sandbox') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connectionID))
	defer func() {
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE id=$1`, connectionID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	// A capture that started ten minutes ago and has completed.
	_, err = db.ExecContext(ctx, `INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,started_at,finished_at,created_at) SELECT id,$2,period,'fixture','completed',now()-interval '10 minutes',now()-interval '5 minutes',now()-interval '11 minutes' FROM client_closes WHERE id=$1`, closeID, connectionID)
	must(err)
	queued := func() []string {
		t.Helper()
		must(scheduleSync(ctx, db))
		rows, err := db.QueryContext(ctx, `SELECT requested_by FROM quickbooks_entity_syncs WHERE close_id=$1 AND state='queued'`, closeID)
		must(err)
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var by string
			rows.Scan(&by)
			out = append(out, by)
		}
		return out
	}
	hint := func(ago string) {
		t.Helper()
		_, err := db.ExecContext(ctx, `UPDATE quickbooks_connections SET changed_at=now()-$2::interval WHERE id=$1`, connectionID, ago)
		must(err)
	}
	hint("20 minutes")
	if got := queued(); len(got) != 0 {
		t.Fatalf("a change before the last capture started needs no refresh inside the daily window: %v", got)
	}
	hint("1 minute")
	if got := queued(); len(got) != 1 || got[0] != "quickbooks-change-hint" {
		t.Fatalf("a change after the last capture started queues one early refresh: %v", got)
	}
	if got := queued(); len(got) != 1 {
		t.Fatalf("a queued refresh is not duplicated: %v", got)
	}
	// A change arrives after that refresh was queued but before it started; the capture covers it.
	_, err = db.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='completed',created_at=now()-interval '5 minutes',started_at=now()-interval '1 minute',finished_at=now() WHERE close_id=$1 AND state='queued'`, closeID)
	must(err)
	hint("2 minutes")
	if got := queued(); len(got) != 0 {
		t.Fatalf("a capture that started after the hint is not repeated: %v", got)
	}
	// A change that arrives while a capture runs is picked up once it finishes.
	_, err = db.ExecContext(ctx, `INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,started_at) SELECT id,$2,period,'fixture','running',now()-interval '2 minutes' FROM client_closes WHERE id=$1`, closeID, connectionID)
	must(err)
	hint("1 minute")
	if got := queued(); len(got) != 0 {
		t.Fatalf("nothing is queued while a capture runs: %v", got)
	}
	_, err = db.ExecContext(ctx, `UPDATE quickbooks_entity_syncs SET state='completed',finished_at=now() WHERE close_id=$1 AND state='running'`, closeID)
	must(err)
	if got := queued(); len(got) != 1 || got[0] != "quickbooks-change-hint" {
		t.Fatalf("a change during a capture queues a refresh after it: %v", got)
	}
}

func TestTheProductionConnectionIsReadFirst(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("QBO_PRODUCTION_CLIENT_ID", "synthetic")
	t.Setenv("QBO_PRODUCTION_CLIENT_SECRET", "synthetic")
	t.Setenv("QBO_PRODUCTION_REDIRECT_URI", "https://api.example.test/v1/quickbooks/callback")
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	org := "org_read" + rand.Text()
	period := time.Now().UTC().Format("2006-01")
	var closeID string
	if _, err = db.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,'Synthetic reading connection')`, org); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, org, "Reading "+org, period).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	connection := map[string]string{}
	for _, environment := range []string{"sandbox", "production"} {
		var id string
		if err = db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,credential,environment) VALUES($1,$2,$3,'fixture','active',$4,now()+interval '1 day','123',decode('0102','hex'),$5) RETURNING id::text`, closeID, org, "Reading "+org, rand.Text(), environment).Scan(&id); err != nil {
			t.Fatal(err)
		}
		connection[environment] = id
	}
	defer func() {
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	var scheduled []string
	for i := 0; i < 10 && len(scheduled) == 0; i++ {
		if err = scheduleSync(ctx, db); err != nil {
			t.Fatal(err)
		}
		rows, err := db.QueryContext(ctx, `SELECT connection_id::text FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			scheduled = append(scheduled, id)
		}
		rows.Close()
	}
	if len(scheduled) != 1 || scheduled[0] != connection["production"] {
		t.Fatalf("only the production connection is scheduled: %v of %v", scheduled, connection)
	}
	if _, _, _, err = syncCredentials(ctx, db, syncClaim{closeID: closeID, connectionID: connection["sandbox"], period: period}); err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("a sandbox capture queued before production was connected must not run: %v", err)
	}
}

func TestTheFirstEndedMonthSnapshotIsCapturedOnce(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	org, other := "org_first"+rand.Text(), "org_other"+rand.Text()
	entity := "First snapshot " + org
	for _, id := range []string{org, other} {
		_, err = db.ExecContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,'Synthetic first snapshot')`, id)
		must(err)
	}
	month := func(back int) string {
		return time.Now().UTC().AddDate(0, -back, -time.Now().UTC().Day()+1).Format("2006-01")
	}
	closes := map[string]string{}
	open := func(owner, period string) string {
		t.Helper()
		var id string
		must(db.QueryRowContext(ctx, `INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, owner, entity, period).Scan(&id))
		closes[id] = owner
		return id
	}
	ended, current, signed, renamed, raced := open(org, month(1)), open(org, month(0)), open(org, month(2)), open(org, month(3)), open(org, month(4))
	foreign := open(other, month(1))
	var connection string
	must(db.QueryRowContext(ctx, `INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment) VALUES($1,$2,$3,'fixture','active',$4,now()+interval '1 day','123','Synthetic company','sandbox') RETURNING id::text`, ended, org, entity, rand.Text()).Scan(&connection))
	defer func() {
		for id := range closes {
			db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
		}
		db.Exec(`DELETE FROM quickbooks_connections WHERE id=$1`, connection)
		for id := range closes {
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, org, other)
	}()
	_, err = db.ExecContext(ctx, `INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed','fixture')`, signed)
	must(err)

	prior := http.DefaultTransport
	defer func() { http.DefaultTransport = prior }()
	reads, company, race := 0, "Synthetic company", ""
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		reads++
		body := `{"QueryResponse":{"startPosition":1,"maxResults":1,"Account":[{"Id":"1","Name":"Checking","AccountType":"Bank"}]}}`
		switch {
		case strings.Contains(r.URL.Path, "/companyinfo/"):
			body = `{"CompanyInfo":{"Id":"1","CompanyName":"` + company + `"}}`
			if race != "" {
				_, e := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123','manual',$3,now(),'user_accountant')`, race, connection, `{"period":"manual"}`)
				must(e)
			}
		case strings.Contains(r.URL.Path, "/reports/"):
			q := r.URL.Query()
			body = fmt.Sprintf(`{"Header":{"ReportName":%q,"StartPeriod":%q,"EndPeriod":%q,"ReportBasis":"Accrual","Currency":"USD","Time":"2026-09-22T10:00:00Z","Option":[{"Name":"NoReportData","Value":"false"}]},"Columns":{"Column":[{"ColTitle":""},{"ColTitle":"Debit"},{"ColTitle":"Credit"}]},"Rows":{"Row":[{"type":"Data","ColData":[{"id":"1","value":"Checking"},{"value":"100.00"},{"value":""}]}]}}`, strings.TrimPrefix(r.URL.Path, "/v3/company/123/reports/"), q.Get("start_date"), q.Get("end_date"))
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{"Content-Type": {"application/json"}}}, nil
	})
	capture := func(closeID string) (snapshots, calls int) {
		t.Helper()
		before := reads
		period := ""
		must(db.QueryRowContext(ctx, `SELECT period FROM client_closes WHERE id=$1`, closeID).Scan(&period))
		err := captureFirstPeriod(ctx, db, syncClaim{closeID: closeID, connectionID: connection, period: period}, "sandbox", "123", "access-synthetic")
		if err != nil && closeID != renamed {
			t.Fatalf("capture %s: %v", closeID, err)
		}
		must(db.QueryRowContext(ctx, `SELECT count(*) FROM quickbooks_snapshots WHERE close_id=$1`, closeID).Scan(&snapshots))
		return snapshots, reads - before
	}

	if snapshots, calls := capture(ended); snapshots != 1 || calls == 0 {
		t.Fatalf("an ended month without a period snapshot is captured: %d snapshots, %d reads", snapshots, calls)
	}
	var actor, period, createdBy string
	must(db.QueryRowContext(ctx, `SELECT s.actor,s.payload->>'period',e.created_by FROM quickbooks_snapshots s JOIN evidence e ON e.id::text=s.payload->>'source_id' AND e.close_id=s.close_id WHERE s.close_id=$1 AND s.payload ? 'reports' AND s.payload ? 'coverage'`, ended).Scan(&actor, &period, &createdBy))
	if actor != "scheduled-quickbooks-refresh" || createdBy != "scheduled-quickbooks-refresh" || period != month(1) {
		t.Fatalf("the capture names the schedule and cites its evidence: %s %s %s", actor, createdBy, period)
	}
	if snapshots, calls := capture(ended); snapshots != 1 || calls != 0 {
		t.Fatalf("an existing period snapshot is never read again or replaced: %d snapshots, %d reads", snapshots, calls)
	}
	for name, id := range map[string]string{"the current month": current, "a signed-off month": signed, "another firm's close": foreign} {
		if snapshots, calls := capture(id); snapshots != 0 || calls != 0 {
			t.Fatalf("%s is not captured: %d snapshots, %d reads", name, snapshots, calls)
		}
	}
	company = "Renamed company"
	if snapshots, _ := capture(renamed); snapshots != 0 {
		t.Fatal("a company whose identity changed is not captured")
	}
	company, race = "Synthetic company", raced
	if snapshots, _ := capture(raced); snapshots != 1 {
		t.Fatal("a person's capture saved during the read wins; the schedule saves nothing")
	}
}
