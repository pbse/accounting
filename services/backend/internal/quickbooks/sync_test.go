package quickbooks

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPeriodContentChangesOnlyWithTheMonth(t *testing.T) {
	payload := func(checking, rows, captured string, balance int) []byte {
		return []byte(fmt.Sprintf(`{"completed_at":%q,"accounts":[{"Id":"3","Name":"Checking","AccountType":"Bank","CurrentBalance":%d,"SyncToken":"%d"}],
			"reports":{"TrialBalance":{"Header":{"Time":%q,"EndPeriod":"2026-08-31"},"Rows":{"Row":[{"ColData":[{"id":"3","value":"Checking"},{"value":%q}]}]}},"GeneralLedger":{"Header":{"Time":%q},"Rows":%s}}}`, captured, balance, balance, captured, checking, captured, rows))
	}
	before := periodContent(payload("100.00", `[1]`, "2026-10-01T00:00:00Z", 7))
	if before == "" || before != periodContent(payload("100.00", `[1]`, "2026-10-02T00:00:00Z", 9)) {
		t.Fatalf("a re-read with only its time and current balances changed is a change: %q", before)
	}
	if before == periodContent(payload("125.00", `[1]`, "2026-10-02T00:00:00Z", 7)) {
		t.Fatal("a later entry did not change the snapshot")
	}
	if before == periodContent(payload("100.00", `[1,2]`, "2026-10-02T00:00:00Z", 7)) {
		t.Fatal("offsetting ledger rows with the same balance did not change the snapshot")
	}
	if periodContent([]byte(`{"reports":{}}`)) != "" {
		t.Fatal("an unreadable snapshot compared equal")
	}
}

func TestScheduledPeriodSnapshotRefreshesUntilAPersonCaptures(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var closeID, connection string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-08') RETURNING id::text`, "Fresh snapshot "+rand.Text()).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123','Fixture Co' FROM client_closes WHERE id=$1 RETURNING id::text`, closeID, rand.Text()).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	due := func() bool {
		t.Helper()
		var org, entity, company string
		err := db.QueryRow(firstPeriod, closeID, connection, "2026-08").Scan(&org, &entity, &company)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		return err == nil
	}
	snapshot := func(actor, age string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor,captured_at) VALUES($1,$2,'123',$3,'{"period":"2026-08"}',now(),$4,now()-$5::interval)`, closeID, connection, rand.Text(), actor, age); err != nil {
			t.Fatal(err)
		}
	}
	if !due() {
		t.Fatal("an ended month with no period snapshot is not due")
	}
	snapshot("scheduled-quickbooks-refresh", "1 hour")
	if due() {
		t.Fatal("a scheduled snapshot an hour old is read again")
	}
	snapshot("scheduled-quickbooks-refresh", "0 seconds")
	db.Exec(`UPDATE quickbooks_snapshots SET captured_at=now()-interval '21 hours' WHERE close_id=$1`, closeID)
	if !due() {
		t.Fatal("a day-old scheduled snapshot is not refreshed")
	}
	for i := 0; i < 4; i++ {
		snapshot("scheduled-quickbooks-refresh", "2 days")
	}
	if due() {
		t.Fatal("a seventh scheduled snapshot was due")
	}
	db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1 AND captured_at<now()-interval '1 day'`, closeID)
	snapshot("scheduled-quickbooks-refresh", "2 days")
	if !due() {
		t.Fatal("the cap outlived its snapshots")
	}
	snapshot("person@example.test", "2 days")
	if due() {
		t.Fatal("a person's capture was refreshed over")
	}
}
