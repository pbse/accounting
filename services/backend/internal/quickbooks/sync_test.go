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
		item := json.RawMessage(fmt.Sprintf(`{"Id":"bill-1","SyncToken":%q,"TxnDate":%q,"MetaData":{"LastUpdatedTime":"2026-09-01T12:00:00Z"},"TotalAmt":100}`, revision, period+"-01"))
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
}
