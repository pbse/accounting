package transport

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestXeroWebhookHintsTheTenantAndTheSweepCapturesHintedCloses(t *testing.T) {
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
	org, tenant := "org_XHook"+rand.Text(), rand.Text()
	var closeID, connection string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Xero webhook fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Hinted client',to_char(now() AT TIME ZONE 'UTC','YYYY-MM')) RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,'Hinted client','fixture','active',$3,now(),$4) RETURNING id::text`, closeID, org, rand.Text(), tenant).Scan(&connection))
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) SELECT $1,$2,$3,period,'d','{}','fixture',now()-interval '2 hours' FROM client_closes WHERE id=$1`, closeID, connection, tenant)
	must(err)

	post := func(body, signature string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/webhooks/xero", strings.NewReader(body))
		r.Header.Set("x-xero-signature", signature)
		w := httptest.NewRecorder()
		xeroWebhook(db)(w, r)
		return w.Code
	}
	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte("webhook-key"))
		mac.Write([]byte(body))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	changed := func() string {
		t.Helper()
		var out sql.NullString
		must(db.QueryRow(`SELECT to_char(changed_at AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS') FROM xero_connections WHERE id=$1`, connection).Scan(&out))
		return out.String
	}
	due := func() bool {
		t.Helper()
		var ok bool
		must(db.QueryRow(`SELECT EXISTS(SELECT 1 FROM client_closes c WHERE c.id::text=$1 AND `+xeroDue+`)`, closeID).Scan(&ok))
		return ok
	}
	events := func(category, at string) string {
		return `{"events":[{"resourceUrl":"https://api.xero.com/api.xro/2.0/Invoices/1","resourceId":"1","eventDateUtc":"` + at + `","eventType":"UPDATE","eventCategory":"` + category + `","tenantId":"` + tenant + `","tenantType":"ORGANISATION"}],"firstEventSequence":1,"lastEventSequence":1,"entropy":"x"}`
	}
	intent := `{"events":[],"firstEventSequence":0,"lastEventSequence":0,"entropy":"YSXKUGKBIMLVJIERNVNZ"}`

	t.Setenv("XERO_WEBHOOK_KEY", "")
	if code := post(intent, sign(intent)); code != 503 {
		t.Fatalf("an unconfigured key refuses notifications: %d", code)
	}
	t.Setenv("XERO_WEBHOOK_KEY", "webhook-key")
	if post(intent, sign(intent)) != 200 || post(intent, sign(intent+" ")) != 401 {
		t.Fatal("intent to receive: a signed empty notification is accepted and a wrongly signed one refused")
	}
	if due() {
		t.Fatal("a close captured two hours ago is not due without a hint")
	}
	contact := events("CONTACT", "2026-09-24T12:00:00.902")
	if code := post(contact, sign(contact)); code != 200 || changed() != "" {
		t.Fatalf("a contact change is acknowledged and ignored: %d %q", code, changed())
	}
	hint := events("INVOICE", time.Now().UTC().Add(-5*time.Minute).Format("2006-01-02T15:04:05.000"))
	if code := post(hint, sign(hint+" ")); code != 401 || changed() != "" {
		t.Fatalf("a wrongly signed invoice event records nothing: %d %q", code, changed())
	}
	if code := post(hint, sign(hint)); code != 200 || changed() == "" {
		t.Fatalf("an invoice change hints the tenant's active connection: %d %q", code, changed())
	}
	recorded := changed()
	older := events("CREDITNOTE", "2026-01-01T00:00:00Z")
	future := events("INVOICE", "2099-01-01T00:00:00")
	if post(older, sign(older)) != 200 || post(future, sign(future)) != 200 || changed() != recorded {
		t.Fatalf("an older or future change never moves the change time: %q %q", changed(), recorded)
	}
	set := func(q string) {
		t.Helper()
		_, err := db.Exec(q, closeID)
		must(err)
	}
	if !due() {
		t.Fatal("a change after the last capture makes the close due, although it has never been claimed")
	}
	set(`UPDATE xero_connections SET state='reconnect_required' WHERE close_id=$1`)
	if due() {
		t.Fatal("a hint on a connection that is not active makes nothing due")
	}
	set(`UPDATE xero_connections SET state='active' WHERE close_id=$1`)
	set(`UPDATE client_closes SET xero_refresh_at=now()-interval '10 minutes' WHERE id=$1`)
	if due() {
		t.Fatal("a close claimed in the past 30 minutes waits, however many hints arrive")
	}
	set(`UPDATE client_closes SET xero_refresh_at=now()-interval '40 minutes' WHERE id=$1`)
	if !due() {
		t.Fatal("a change after an older claim makes the close due")
	}
	set(`UPDATE xero_connections SET changed_at=now()-interval '1 hour' WHERE close_id=$1`)
	if due() {
		t.Fatal("a claim after the change settles the hint")
	}
	set(`UPDATE xero_connections SET changed_at=now()-interval '5 minutes' WHERE close_id=$1`)
	set(`UPDATE client_closes SET xero_refresh_at=NULL WHERE id=$1`)
	set(`UPDATE xero_snapshots SET captured_at=now()-interval '1 minute' WHERE close_id=$1`)
	if due() {
		t.Fatal("a capture after the change settles the hint")
	}
}
