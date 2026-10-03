package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/delivery"
	"github.com/golang-jwt/jwt/v5"
)

func TestCollectionMessagesNeedTheClientsAuthorityAndAWillingAddress(t *testing.T) {
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
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	org, entity := "org_Collect"+rand.Text(), "Collections client"
	var closeID, connection, mailbox string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Collections fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM email_delivery_events WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`, `DELETE FROM email_outbox WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`,
			`DELETE FROM email_connections WHERE organization_id=$1`, `DELETE FROM collection_messages WHERE organization_id=$1`, `DELETE FROM collection_contact_preferences WHERE organization_id=$1`, `DELETE FROM customer_contact_authority WHERE organization_id=$1`,
			`DELETE FROM quickbooks_entity_syncs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`,
			`DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency) VALUES($1,$2,$3,'fixture','active',$4,now(),'123','USD') RETURNING id::text`, closeID, org, entity, rand.Text()).Scan(&connection))
	capture := func(balance string) {
		t.Helper()
		invoice := func(id, name, amount, due string) map[string]any {
			return map[string]any{"Id": id, "DocNumber": "INV-" + id, "TxnDate": "2026-08-01", "DueDate": due, "Balance": json.Number(amount), "CustomerRef": map[string]string{"value": id, "name": name}, "CurrencyRef": map[string]string{"value": "USD"}}
		}
		payload, _ := json.Marshal(map[string]any{"finished_at": "2026-10-05T09:00:00Z", "completeness": "All supported types were paged.", "open_items": map[string]any{
			"Invoice": []any{invoice("2", "Beta", balance, "2026-09-20"), invoice("3", "Gamma", "600.00", "2026-10-20")}, "Bill": []any{}}})
		_, e := db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',now(),$3)`, closeID, connection, string(payload))
		must(e)
	}
	capture("800.00")
	must(db.QueryRow(`INSERT INTO email_connections(organization_id,sender,domain_id,credential,actor) VALUES($1,'Firm <ar@firm.example>','synthetic','dummy','fixture') RETURNING id::text`, org).Scan(&mailbox))

	store := savedCloses(db, strings.Repeat("c", 32))
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "requests:send"}}
	call := func(body map[string]any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/v1/closes/"+closeID+"/collections", strings.NewReader(string(raw))).WithContext(auth.WithActor(t.Context(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	draft := func(item, recipient string) string {
		t.Helper()
		var out struct{ ID string }
		must(json.Unmarshal([]byte(call(map[string]any{"action": "draft", "item_id": item, "recipient": recipient}, 200)), &out))
		return out.ID
	}
	call(map[string]any{"action": "draft", "item_id": "99", "recipient": "ap@beta.example"}, 409)
	call(map[string]any{"action": "draft", "item_id": "2", "recipient": "not an address"}, 422)
	first := draft("2", "ap@beta.example")
	var body string
	must(db.QueryRow(`SELECT body FROM collection_messages WHERE id::text=$1`, first).Scan(&body))
	if !strings.Contains(body, "$800.00") || !strings.Contains(body, "was due on 2026-09-20") || !strings.Contains(body, entity) {
		t.Fatalf("the default message names the invoice, its balance and due date, and the client: %s", body)
	}
	approve := func(id string, want int) string {
		return call(map[string]any{"action": "approve", "message_id": id, "connection_id": mailbox, "confirmed": true}, want)
	}
	if got := approve(first, 409); !strings.Contains(got, "authority") {
		t.Fatalf("nothing is sent to a client's customer without the client's authority: %s", got)
	}
	call(map[string]any{"action": "authority", "granted": true, "reference": "Engagement letter, section 4", "confirmed": true}, 200)
	call(map[string]any{"action": "preference", "recipient": "AP@beta.example", "do_not_contact": true, "reason": "Client: Beta's owner asked not to be emailed"}, 200)
	if got := approve(first, 409); !strings.Contains(got, "marked do not contact") {
		t.Fatalf("a do-not-contact address is refused: %s", got)
	}
	call(map[string]any{"action": "preference", "recipient": "ap@beta.example", "do_not_contact": false, "reason": "Client: Beta agreed to email reminders"}, 200)
	call(map[string]any{"action": "approve", "message_id": first, "connection_id": "999999", "confirmed": true}, 409)
	var queued struct{ ID string }
	must(json.Unmarshal([]byte(approve(first, 200)), &queued))
	if again := approve(first, 200); !strings.Contains(again, queued.ID) {
		t.Fatalf("approving again returns the same message: %s %s", again, queued.ID)
	}

	// Withdrawn authority stops a message already approved: the worker cancels it rather than sending.
	call(map[string]any{"action": "authority", "granted": false, "reference": "Client withdrew permission by phone, 2026-10-06", "confirmed": true}, 200)
	state := "queued"
	for range 10 {
		if worked, e := delivery.ProcessOne(t.Context(), db); e != nil || !worked {
			break
		}
		must(db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, queued.ID).Scan(&state))
		if state != "queued" {
			break
		}
	}
	if state != "cancelled" {
		t.Fatalf("a withdrawn authority cancels the queued message: %s", state)
	}

	// An address that bounced is not emailed again for this client, even after a later failure; an event that arrived
	// before the provider id was stored is linked to the close.
	call(map[string]any{"action": "authority", "granted": true, "reference": "Client renewed permission in writing, 2026-10-07", "confirmed": true}, 200)
	_, err = db.Exec(`INSERT INTO email_delivery_events(connection_id,event_id,body_digest,provider_id,recipient,kind,occurred_at) VALUES($1,'bounce-1','x','provider-1','ap@beta.example','bounced',now()),($1,'fail-1','y','provider-1','ap@beta.example','failed',now()+interval '1 minute')`, mailbox)
	must(err)
	_, err = db.Exec(`UPDATE email_outbox SET provider_id='provider-1' WHERE id=$1`, queued.ID)
	must(err)
	var linked string
	must(db.QueryRow(`SELECT COALESCE(close_id::text,'') FROM email_delivery_events WHERE connection_id=$1 AND event_id='bounce-1'`, mailbox).Scan(&linked))
	if linked != closeID {
		t.Fatalf("an early delivery event is linked to its close: %q", linked)
	}
	second := draft("3", "ap@beta.example")
	if got := approve(second, 409); !strings.Contains(got, "bounced") {
		t.Fatalf("a bounced address is refused: %s", got)
	}

	// The client authorized contact from one sender; a different sender needs the client's authority again.
	third, fourth := draft("3", "billing@gamma.example"), draft("3", "owner@gamma.example")
	_, err = db.Exec(`UPDATE email_connections SET sender='Other <ar@other.example>' WHERE id::text=$1`, mailbox)
	must(err)
	if got := approve(third, 409); !strings.Contains(got, "authorized contact from Firm \\u003car@firm.example") {
		t.Fatalf("a different sender is refused: %s", got)
	}
	_, err = db.Exec(`UPDATE email_connections SET sender='Firm <ar@firm.example>' WHERE id::text=$1`, mailbox)
	must(err)

	// A message speaks for the capture it was drafted from: a new capture asks for a new draft, and stops one already
	// approved.
	must(json.Unmarshal([]byte(approve(third, 200)), &queued))
	capture("800.00")
	if got := approve(fourth, 409); !strings.Contains(got, "captured again") {
		t.Fatalf("a draft from earlier books is not sent: %s", got)
	}
	state = "queued"
	for range 10 {
		if worked, e := delivery.ProcessOne(t.Context(), db); e != nil || !worked {
			break
		}
		must(db.QueryRow(`SELECT state FROM email_outbox WHERE id=$1`, queued.ID).Scan(&state))
		if state != "queued" {
			break
		}
	}
	if state != "cancelled" {
		t.Fatalf("a new capture cancels the approved message: %s", state)
	}
	r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/collections", nil).WithContext(auth.WithActor(t.Context(), actor))
	w := httptest.NewRecorder()
	store.ServeHTTP(w, r)
	page := strings.ReplaceAll(w.Body.String(), " ", "")
	if w.Code != 200 || !strings.Contains(page, `"bounced":["ap@beta.example"]`) || !strings.Contains(page, `"state":"cancelled"`) {
		t.Fatalf("the page shows the bounced address and the cancelled message: %d %s", w.Code, w.Body.String())
	}
}
