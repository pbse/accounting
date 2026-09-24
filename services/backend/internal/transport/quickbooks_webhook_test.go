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
)

func TestQuickBooksWebhookMarksOnlyActiveSandboxCompanies(t *testing.T) {
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
	org := "org_Hook" + rand.Text()
	var closeID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Webhook fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Webhook client','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	realm1, realm2 := "9"+strings.Repeat("1", 12), "9"+strings.Repeat("2", 12)
	connection := func(client, realm, state, environment string) string {
		var id string
		must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,environment) VALUES($1,$2,$3,'fixture',$4,$5,now(),$6,$7) RETURNING id::text`, closeID, org, client, state, rand.Text(), realm, environment).Scan(&id))
		return id
	}
	first, second := connection("A", realm1, "active", "sandbox"), connection("B", realm2, "active", "sandbox")
	production, gone := connection("C", realm1, "active", "production"), connection("D", realm1, "disconnected", "sandbox")
	marked := func() string {
		t.Helper()
		var out string
		must(db.QueryRow(`SELECT COALESCE(string_agg(client_entity,'' ORDER BY client_entity),'') FROM quickbooks_connections WHERE id::text IN ($1,$2,$3,$4) AND changed_at IS NOT NULL`, first, second, production, gone).Scan(&out))
		return out
	}
	post := func(body, signature string) int {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/webhooks/quickbooks", strings.NewReader(body))
		r.Header.Set("intuit-signature", signature)
		w := httptest.NewRecorder()
		quickbooksWebhook(db)(w, r)
		return w.Code
	}
	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte("verifier-token"))
		mac.Write([]byte(body))
		return base64.StdEncoding.EncodeToString(mac.Sum(nil))
	}
	event := func(realm, kind string) string {
		return `{"specversion":"1.0","id":"` + rand.Text() + `","source":"intuit.dsnBgbseACLLRZNxo2dfc4evmEJdxde58xeeYcZliOU=","type":"` + kind + `","datacontenttype":"application/json","time":"2026-09-24T12:00:00Z","intuitentityid":"42","intuitaccountid":"` + realm + `","data":{}}`
	}
	valid := "[" + event(realm1, "qbo.bill.updated.v1") + "," + event(realm2, "billing.updated") + "]"
	t.Setenv("QBO_WEBHOOK_VERIFIER", "")
	if code := post(valid, sign(valid)); code != 503 {
		t.Fatalf("an unconfigured verifier refuses notifications: %d", code)
	}
	t.Setenv("QBO_WEBHOOK_VERIFIER", "verifier-token")
	if code := post(valid, sign(valid+" ")); code != 401 || marked() != "" {
		t.Fatalf("a wrong signature is refused and marks nothing: %d %q", code, marked())
	}
	for _, bad := range []string{`{"not":"an array"}`, "[" + event(realm2, "qbo.bill.updated") + "," + `{"specversion":"1.0","type":"qbo.bill.updated.v1","intuitaccountid":"` + realm2 + `"}` + "]"} {
		if code := post(bad, sign(bad)); code != 400 || marked() != "" {
			t.Fatalf("a notification without a valid CloudEvent is refused: %d %q", code, marked())
		}
	}
	if code := post(valid, sign(valid)); code != 200 || marked() != "A" {
		t.Fatalf("only the active sandbox connection for a valid event's company is marked: %d %q", code, marked())
	}
	both := "[" + event(realm1, "qbo.invoice.created.v1") + "," + event(realm2, "qbo.account.updated.v1") + "]"
	if code := post(both, sign(both)); code != 200 || marked() != "AB" {
		t.Fatalf("one notification can mark several companies: %d %q", code, marked())
	}
}
