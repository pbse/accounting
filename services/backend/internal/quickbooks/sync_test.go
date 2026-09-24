package quickbooks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
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
