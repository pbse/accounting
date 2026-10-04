package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// settleFixture is a collection email of close in state, on its own sender, claimed claimedAgo ago.
func settleFixture(t *testing.T, db *sql.DB, closeID, connection, state, claimedAgo, provider string) string {
	t.Helper()
	id := "settle-" + rand.Text()
	var message string
	if err := db.QueryRow(`INSERT INTO collection_messages(organization_id,close_id,client_entity,company,capture_id,item_id,label,customer,amount,currency,recipient,subject,body,created_by)
 SELECT organization_id,id,entity,'realm','1','inv-1','Invoice 1','Customer',100,'USD','pay@customer.example','Payment reminder: Invoice 1','Please pay invoice 1.','fixture' FROM client_closes WHERE id::text=$1 RETURNING id::text`, closeID).Scan(&message); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO email_outbox(id,collection_id,connection_id,payload,state,provider_id,approved_by,firm_authority,approved_until,attempts,claimed_at,error)
 VALUES($1,$2::bigint,$3::bigint,decode('00','hex'),$4,NULLIF($5,''),'fixture',true,now()+interval '1 day',1,now()-$6::interval,'Resend response was not received.')`, id, message, connection, state, provider, claimedAgo); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAnUnknownEmailIsSettledFromTheProvidersRecords(t *testing.T) {
	db := adminDB(t)
	token := strings.Repeat("e", 32)
	h := apiWithStore(db, token)
	entity := "Settle " + rand.Text()
	var mine, other struct{ ID string }
	for _, c := range []*struct{ ID string }{&mine, &other} {
		raw := adminJSON(t, h, token, "POST", "/v1/closes", `{"entity":"`+entity+`","period":"2026-0`+map[bool]string{true: "8", false: "9"}[c == &mine]+`"}`, 200)
		c.ID = raw["id"].(string)
	}
	var connection string
	if err := db.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,enabled,actor) SELECT organization_id,'firm@example.test','synthetic',decode('00','hex'),true,'fixture' FROM client_closes WHERE id::text=$1 RETURNING id::text`, mine.ID).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`WITH m AS (DELETE FROM email_outbox WHERE connection_id=$1::bigint) DELETE FROM email_delivery_events WHERE connection_id=$1::bigint`, connection)
		db.Exec(`DELETE FROM email_connections WHERE id=$1::bigint`, connection)
		db.Exec(`DELETE FROM client_closes WHERE entity=$1`, entity)
	})
	unknown := settleFixture(t, db, mine.ID, connection, "unknown", "1 hour", "")
	sending := settleFixture(t, db, mine.ID, connection, "sending", "10 seconds", "")
	settleFixture(t, db, mine.ID, connection, "accepted", "1 hour", "taken-id")
	if _, err := db.Exec(`INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at) VALUES($1::bigint,$2,'d','elsewhere-id','someone@else.example','delivered',now())`, connection, rand.Text()); err != nil {
		t.Fatal(err)
	}
	settle := func(closeID, outbox, body string, want int) {
		t.Helper()
		adminJSON(t, h, token, "POST", "/v1/closes/"+closeID+"/email/"+outbox+"/settle", body, want)
	}
	evidence := `"evidence":"Resend logs for firm@example.test to pay@customer.example on the attempt day show it"`
	settle(other.ID, unknown, `{"expected_state":"unknown","outcome":"accepted","provider_id":"re_1",`+evidence+`}`, 404)
	settle(mine.ID, unknown, `{"expected_state":"queued","outcome":"accepted","provider_id":"re_1",`+evidence+`}`, 409)
	settle(mine.ID, unknown, `{"expected_state":"unknown","outcome":"accepted","provider_id":"re_1","evidence":"seen"}`, 422)
	settle(mine.ID, unknown, `{"expected_state":"unknown","outcome":"failed","provider_id":"re_1",`+evidence+`}`, 422)
	settle(mine.ID, unknown, `{"expected_state":"unknown","outcome":"accepted","provider_id":"taken-id",`+evidence+`}`, 409)
	settle(mine.ID, unknown, `{"expected_state":"unknown","outcome":"accepted","provider_id":"elsewhere-id",`+evidence+`}`, 409)
	settle(mine.ID, sending, `{"expected_state":"sending","outcome":"accepted","provider_id":"re_2",`+evidence+`}`, 409)
	settle(mine.ID, unknown, `{"expected_state":"unknown","outcome":"accepted","provider_id":"re_1",`+evidence+`}`, 200)
	settle(mine.ID, unknown, `{"expected_state":"unknown","outcome":"accepted","provider_id":"re_1",`+evidence+`}`, 409)
	// Two settlements naming the same message ID at once: the second waits for the first and is refused.
	first, second := settleFixture(t, db, mine.ID, connection, "unknown", "1 hour", ""), settleFixture(t, db, mine.ID, connection, "unknown", "1 hour", "")
	same := emailSettlement{ExpectedState: "unknown", Outcome: "accepted", ProviderID: "re_same", Evidence: "Resend logs show one message"}
	tx1, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback()
	if _, err = settleEmail(t.Context(), tx1, first, []string{"unknown"}, same, "fixture"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		tx2, err := db.BeginTx(t.Context(), nil)
		if err == nil {
			_, err = settleEmail(t.Context(), tx2, second, []string{"unknown"}, same, "fixture")
			tx2.Rollback()
		}
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	if err = tx1.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !strings.Contains(fmt.Sprint(err), "belongs to another email") {
		t.Fatalf("a concurrent settlement of the same message ID is refused: %v", err)
	}
	var state, provider, line, by string
	if err := db.QueryRow(`SELECT m.state,m.provider_id,m.error,s.settled_by FROM email_outbox m JOIN email_settlements s ON s.outbox_id=m.id WHERE m.id=$1`, unknown).Scan(&state, &provider, &line, &by); err != nil {
		t.Fatal(err)
	}
	if state != "accepted" || provider != "re_1" || !strings.HasPrefix(line, "Resend response was not received. Settled as accepted by ") || by == "" {
		t.Fatalf("the settlement keeps the attempt's error and says who settled it: %s %s %q %s", state, provider, line, by)
	}
}

func TestStaffSettleAFirmsEmailDuringRecovery(t *testing.T) {
	db := adminDB(t)
	token := strings.Repeat("r", 32)
	h := apiWithStore(db, token)
	firm := "org_Settle" + rand.Text()
	if _, err := db.Exec(`INSERT INTO organizations(id,name,enabled) VALUES($1,'Restored firm',false)`, firm); err != nil {
		t.Fatal(err)
	}
	var closeID, connection string
	if err := db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Restored client','2026-08') RETURNING id::text`, firm).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,enabled,actor) VALUES($1,'firm@example.test','synthetic',decode('00','hex'),true,'fixture') RETURNING id::text`, firm).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM email_outbox WHERE connection_id=$1::bigint`, connection)
		db.Exec(`DELETE FROM email_connections WHERE id=$1::bigint`, connection)
		db.Exec(`DELETE FROM client_closes WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM platform_audit WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, firm)
	})
	queued := settleFixture(t, db, closeID, connection, "queued", "1 hour", "")
	stale := settleFixture(t, db, closeID, connection, "sending", "5 minutes", "")
	active := settleFixture(t, db, closeID, connection, "sending", "10 seconds", "")
	base := "/v1/admin/firms/" + firm + "/emails/"
	body := func(state, outcome, provider string) string {
		raw, _ := json.Marshal(map[string]string{"expected_state": state, "outcome": outcome, "provider_id": provider, "evidence": "Resend logs by idempotency key after the restore", "reason": "Reconciling the restored backup before workers resume"})
		return string(raw)
	}
	adminJSON(t, h, token, "POST", base+queued+"/settle", body("queued", "failed", ""), 422)
	adminJSON(t, h, token, "POST", "/v1/admin/firms/org_Other/emails/"+queued+"/settle", body("queued", "accepted", "re_9"), 404)
	adminJSON(t, h, token, "POST", base+queued+"/settle", body("queued", "accepted", "re_9"), 200)
	adminJSON(t, h, token, "POST", base+stale+"/settle", body("sending", "accepted", "re_10"), 200)
	adminJSON(t, h, token, "POST", base+active+"/settle", body("sending", "accepted", "re_11"), 409)
	var accepted int
	if err := db.QueryRow(`SELECT count(*) FROM email_outbox m JOIN email_settlements s ON s.outbox_id=m.id AND s.settled_by LIKE 'staff:%' WHERE m.connection_id=$1::bigint AND m.state='accepted'`, connection).Scan(&accepted); err != nil || accepted != 2 {
		t.Fatalf("staff settle a suspended firm's restored emails: %d %v", accepted, err)
	}
	if audit := mustString(adminJSON(t, h, token, "GET", "/v1/admin/firms/"+firm+"/audit", "", 200)); !strings.Contains(audit, `"action":"email.settle"`) {
		t.Fatalf("each settlement is in the staff audit: %s", audit)
	}
}
