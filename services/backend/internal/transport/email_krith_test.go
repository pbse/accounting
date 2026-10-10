package transport

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/delivery"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
)

// krithEvent posts one Resend event, signed with Krith's webhook secret, to Krith's webhook.
func krithEvent(t *testing.T, db *sql.DB, secret, eventID, body string) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/webhooks/resend/krith", krithEmailWebhook(db))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	key, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(eventID + "." + timestamp + "."))
	mac.Write([]byte(body))
	r := httptest.NewRequest("POST", "/v1/webhooks/resend/krith", strings.NewReader(body))
	r.Header.Set("svix-id", eventID)
	r.Header.Set("svix-timestamp", timestamp)
	r.Header.Set("svix-signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w.Code
}

func TestKrithSendsAFirmsEmailUntilItConnectsItsOwn(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
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
	org := "org_Krith" + rand.Text()
	var closeID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Smith & Co')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_sender',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Krith synthetic','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM email_delivery_events WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`, org)
		db.Exec(`DELETE FROM email_outbox WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`, org)
		db.Exec(`DELETE FROM work_requests WHERE draft_id IN (SELECT id FROM request_drafts WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM request_drafts WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM email_connections WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	secret := "whsec_" + base64.StdEncoding.EncodeToString([]byte("synthetic-krith-webhook-only"))
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("CLIENT_EMAIL_ADDRESS", "requests@mail.krith.test")
	t.Setenv("CLIENT_EMAIL_WEBHOOK_SECRET", secret)
	t.Setenv("PLATFORM_EMAIL_KEY", "re_platform_synthetic")
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_sender", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Organization: org, Permissions: []string{"clients:all", "integrations:manage", "requests:send", "workpapers:review"}}
	mux := http.NewServeMux()
	registerEmail(mux, db)
	registerWork(mux, db)
	h := organizationBoundary(db, mux)
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(r.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	type sender struct {
		ID, Provider, Sender string
		Tracking             bool `json:"tracking_configured"`
	}
	current := func() (sender, map[string]any) {
		t.Helper()
		var status struct {
			Connection *sender
			Krith      bool
			Providers  []map[string]string
		}
		raw := call("GET", "/v1/closes/email", nil, 200)
		must(json.Unmarshal(raw, &status))
		if status.Connection == nil {
			return sender{}, map[string]any{"krith": status.Krith, "providers": status.Providers}
		}
		return *status.Connection, map[string]any{"krith": status.Krith, "providers": status.Providers}
	}
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	var sent []delivery.Email
	var keys []string
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		response := `{"id":"domain-fixture","name":"firm.example","status":"verified","click_tracking":false,"capabilities":{"sending":"enabled"}}`
		if r.Method == "POST" {
			raw, _ := io.ReadAll(r.Body)
			var email delivery.Email
			must(json.Unmarshal(raw, &email))
			sent = append(sent, email)
			keys = append(keys, r.Header.Get("Authorization"))
			response = `{"id":"provider-` + strings.TrimPrefix(r.Header.Get("Idempotency-Key"), "close-request/") + `"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	})

	// A firm that connected nothing sends from Krith's sender, in its name, tracked by Krith's webhook.
	krith, extra := current()
	if krith.Provider != "krith" || krith.Sender != `"Smith & Co via Krith" <requests@mail.krith.test>` || !krith.Tracking || extra["krith"] != true {
		t.Fatalf("every firm starts with Krith's sender: %+v %v", krith, extra)
	}
	if providers, _ := json.Marshal(extra["providers"]); string(providers) != `[{"label":"Resend","name":"resend"}]` {
		t.Fatalf("the integrations a firm may connect instead: %s", providers)
	}
	if again, _ := current(); again.ID != krith.ID {
		t.Fatal("Krith's sender is recorded once")
	}
	if body := call("POST", "/v1/closes/email", map[string]any{"previous_id": krith.ID, "disable": true, "confirmed": true}, 409); !strings.Contains(string(body), "default") {
		t.Fatalf("Krith's sender is replaced by connecting another, never disconnected: %s", body)
	}
	call("POST", "/v1/closes/email", map[string]any{"previous_id": krith.ID, "webhook_secret": secret, "confirmed": true}, 409)

	makeRequest := func(name string) (string, string) {
		t.Helper()
		ctx := context.Background()
		tx, e := db.BeginTx(ctx, nil)
		must(e)
		defer tx.Rollback()
		var source string
		must(tx.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$2,'synthetic','fixture') RETURNING id::text`, closeID, name).Scan(&source))
		job, e := preparation.StartIntake(ctx, tx, closeID, source, "fixture")
		must(e)
		tx.Exec(`UPDATE preparation_jobs SET state='waiting_client' WHERE id=$1`, job)
		tx.Exec(`UPDATE preparation_intakes SET revision=1 WHERE job_id=$1`, job)
		digest, _, e := preparation.RequestContext(ctx, tx, closeID, nil)
		must(e)
		var draft, request string
		must(tx.QueryRow(`INSERT INTO request_drafts(close_id,body,workpaper_ids,context_digest,created_by) VALUES($1,'Synthetic approved request','[]',$2,'fixture') RETURNING id::text`, closeID, digest).Scan(&draft))
		must(tx.QueryRow(`INSERT INTO work_requests(job_id,draft_id,revision,recipient,approved_by) VALUES($1,$2,1,'client@example.test','user_sender') RETURNING id::text`, job, draft).Scan(&request))
		must(tx.Commit())
		return job, request
	}
	enqueue := func(connection string) string {
		t.Helper()
		job, request := makeRequest("invoice-" + rand.Text())
		var result struct{ ID string }
		must(json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/work/"+job+"/requests/"+request+"/email", map[string]any{"confirmed": true, "revision": 1, "connection_id": connection}, 200), &result))
		return result.ID
	}
	state := func(id string) (s, e string) {
		must(db.QueryRow(`SELECT state,error FROM email_outbox WHERE id=$1`, id).Scan(&s, &e))
		return
	}
	id := enqueue(krith.ID)
	// An event can arrive before the send's answer is saved: the outbox's tag finds the firm's connection.
	if code := krithEvent(t, db, secret, "early-sent", `{"type":"email.sent","created_at":"2026-09-01T12:00:00Z","data":{"email_id":"provider-`+id+`","to":["client@example.test"],"tags":{"outbox":"`+id+`"}}}`); code != 204 {
		t.Fatalf("early event: %d", code)
	}
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if s, e := state(id); s != "accepted" || len(sent) != 1 || keys[0] != "Bearer re_platform_synthetic" || sent[0].From != krith.Sender || len(sent[0].Tags) != 1 || sent[0].Tags[0] != (delivery.Tag{Name: delivery.OutboxTag, Value: id}) {
		t.Fatalf("Krith's sender sends with Krith's key and the outbox's tag: %s %s %+v %v", s, e, sent, keys)
	}
	var stored []byte
	must(db.QueryRow(`SELECT payload FROM email_outbox WHERE id=$1`, id).Scan(&stored))
	for _, event := range []struct {
		id, body string
		want     int
	}{
		{"delivered", `{"type":"email.delivered","created_at":"2026-09-01T12:01:00Z","data":{"email_id":"provider-` + id + `","to":["client@example.test"],"tags":[{"name":"outbox","value":"` + id + `"}]}}`, 204},
		// Krith's own welcome and invitation emails carry no outbox, and an outbox that is gone finds no firm.
		{"welcome", `{"type":"email.delivered","created_at":"2026-09-01T12:01:00Z","data":{"email_id":"platform-1","to":["new@firm.example"],"tags":{}}}`, 204},
		{"gone", `{"type":"email.bounced","created_at":"2026-09-01T12:01:00Z","data":{"email_id":"other","to":["client@example.test"],"tags":{"outbox":"NOSUCHOUTBOX"}}}`, 204},
	} {
		if code := krithEvent(t, db, secret, event.id, event.body); code != event.want {
			t.Fatalf("%s: %d", event.id, code)
		}
	}
	if code := krithEvent(t, db, "whsec_"+base64.StdEncoding.EncodeToString([]byte("another-secret-entirely")), "forged", `{"type":"email.bounced","created_at":"2026-09-01T12:01:00Z","data":{"email_id":"provider-`+id+`","to":["client@example.test"],"tags":{"outbox":"`+id+`"}}}`); code != 401 {
		t.Fatalf("an event not signed with Krith's secret is refused: %d", code)
	}
	var delivered string
	var events int
	must(db.QueryRow(`SELECT delivery FROM email_delivery WHERE id=$1`, id).Scan(&delivered))
	must(db.QueryRow(`SELECT count(*) FROM email_delivery_events WHERE connection_id=$1`, krith.ID).Scan(&events))
	if delivered != "delivered" || events != 2 {
		t.Fatalf("Krith's webhook tracks the firm's email, and only it: %s %d", delivered, events)
	}

	// Renaming the firm records Krith's sender again in its new name; the old one stops.
	_, err = db.Exec(`UPDATE organizations SET name='Smith & Partners' WHERE id=$1`, org)
	must(err)
	renamed, _ := current()
	if renamed.ID == krith.ID || renamed.Sender != `"Smith & Partners via Krith" <requests@mail.krith.test>` {
		t.Fatalf("a renamed firm's sender: %+v", renamed)
	}
	if job, request := makeRequest("renamed"); !strings.Contains(string(call("POST", "/v1/closes/"+closeID+"/work/"+job+"/requests/"+request+"/email", map[string]any{"confirmed": true, "revision": 1, "connection_id": krith.ID}, 409)), "current firm sender") {
		t.Fatal("an approval names the current sender")
	}

	// The firm may connect its own Resend account instead, and go back to Krith's.
	connect := map[string]any{"provider": "resend", "fields": map[string]string{"key": "re_synthetic_only_00000", "from": "Accountants <requests@firm.example>", "domain_id": "domain-fixture"}, "previous_id": renamed.ID, "confirmed": true}
	call("POST", "/v1/closes/email", map[string]any{"provider": "postmark", "fields": map[string]string{}, "previous_id": renamed.ID, "confirmed": true}, 422)
	call("POST", "/v1/closes/email", connect, 200)
	own, _ := current()
	if own.Provider != "resend" || own.Sender != "Accountants <requests@firm.example>" || own.Tracking {
		t.Fatalf("the firm's own sender: %+v", own)
	}
	ownID := enqueue(own.ID)
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if s, e := state(ownID); s != "accepted" || keys[len(keys)-1] != "Bearer re_synthetic_only_00000" || len(sent[len(sent)-1].Tags) != 0 {
		t.Fatalf("the firm's own sender sends with its own key: %s %s %v", s, e, keys)
	}
	call("POST", "/v1/closes/email", map[string]any{"previous_id": own.ID, "disable": true, "confirmed": true}, 200)
	if back, _ := current(); back.Provider != "krith" || back.Sender != renamed.Sender {
		t.Fatalf("disconnecting the firm's own sender brings back Krith's: %+v", back)
	}

	// Without Krith's address on this server a firm has only what it connected; a worker without Krith's key sends
	// nothing through Krith's sender.
	back, _ := current()
	queued := enqueue(back.ID)
	t.Setenv("PLATFORM_EMAIL_KEY", "")
	if _, err = delivery.ProcessOne(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if s, e := state(queued); s != "failed" || !strings.Contains(e, "not set up") {
		t.Fatalf("Krith's sender without its key: %s %s", s, e)
	}
	t.Setenv("CLIENT_EMAIL_ADDRESS", "")
	if still, extra := current(); still.ID != back.ID || extra["krith"] != false {
		t.Fatalf("a server without Krith's address keeps the firm's record and says so: %+v %v", still, extra)
	}
	_, err = db.Exec(`UPDATE email_connections SET enabled=false WHERE organization_id=$1`, org)
	must(err)
	if none, _ := current(); none.ID != "" {
		t.Fatalf("no sender is made without Krith's address: %+v", none)
	}
}

func TestCollectionMessagesThroughKrithNameWhereRepliesGo(t *testing.T) {
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
	t.Setenv("CLIENT_EMAIL_ADDRESS", "requests@mail.krith.test")
	t.Setenv("PLATFORM_EMAIL_KEY", "re_platform_synthetic")
	org, entity := "org_KrithCollect"+rand.Text(), "Collections client"
	var closeID, connection string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Smith & Co')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM email_outbox WHERE connection_id IN (SELECT id FROM email_connections WHERE organization_id=$1)`, `DELETE FROM email_connections WHERE organization_id=$1`,
			`DELETE FROM collection_messages WHERE organization_id=$1`, `DELETE FROM customer_contact_authority WHERE organization_id=$1`,
			`DELETE FROM quickbooks_entity_syncs WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`,
			`DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency) VALUES($1,$2,$3,'fixture','active',$4,now(),'123','USD') RETURNING id::text`, closeID, org, entity, rand.Text()).Scan(&connection))
	payload, _ := json.Marshal(map[string]any{"finished_at": "2026-10-05T09:00:00Z", "completeness": "All supported types were paged.", "open_items": map[string]any{
		"Invoice": []any{map[string]any{"Id": "2", "DocNumber": "INV-2", "TxnDate": "2026-08-01", "DueDate": "2026-09-20", "Balance": json.Number("800.00"), "CustomerRef": map[string]string{"value": "2", "name": "Beta"}, "CurrencyRef": map[string]string{"value": "USD"}}}, "Bill": []any{}}})
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',now(),$3)`, closeID, connection, string(payload))
	must(err)
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	var sent []delivery.Email
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		var email delivery.Email
		must(json.Unmarshal(raw, &email))
		sent = append(sent, email)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"provider-collection"}`))}, nil
	})
	store := savedCloses(db, strings.Repeat("c", 32))
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "requests:send"}}
	call := func(method, path string, body map[string]any, want int) string {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	collections := "/v1/closes/" + closeID + "/collections"
	draft := func(replyTo string, want int) (id, body string) {
		t.Helper()
		var out struct{ ID string }
		body = call("POST", collections, map[string]any{"action": "draft", "item_id": "2", "recipient": "ap@beta.example", "reply_to": replyTo}, want)
		json.Unmarshal([]byte(body), &out)
		return out.ID, body
	}
	sender := func() (id, from string) {
		t.Helper()
		var status struct{ Connection struct{ ID, Sender string } }
		must(json.Unmarshal([]byte(call("GET", "/v1/closes/email", nil, 200)), &status))
		return status.Connection.ID, status.Connection.Sender
	}
	if _, got := draft("", 422); !strings.Contains(got, "takes no replies") {
		t.Fatalf("a message through Krith's sender names where replies go: %s", got)
	}
	message, _ := draft("owner@client.example", 200)
	call("POST", collections, map[string]any{"action": "authority", "granted": true, "reference": "Engagement letter, section 4", "confirmed": true}, 200)
	krith, from := sender()
	var authorized string
	must(db.QueryRow(`SELECT sender FROM customer_contact_authority WHERE organization_id=$1 ORDER BY id DESC LIMIT 1`, org).Scan(&authorized))
	if authorized != from || from != `"Smith & Co via Krith" <requests@mail.krith.test>` {
		t.Fatalf("the client authorizes contact from Krith's sender in the firm's name: %s %s", authorized, from)
	}
	call("POST", collections, map[string]any{"action": "approve", "message_id": message, "connection_id": krith, "confirmed": true}, 200)
	if _, err = delivery.ProcessOne(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].From != from || sent[0].ReplyTo != "owner@client.example" {
		t.Fatalf("the message goes from Krith's sender with the reply address: %+v", sent)
	}

	// A message drafted without a reply address while the firm had its own sender is not sent through Krith's.
	_, err = db.Exec(`UPDATE email_connections SET enabled=false WHERE organization_id=$1`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO email_connections(organization_id,provider,sender,domain_id,credential,actor) VALUES($1,'resend',$2,'synthetic','dummy','fixture')`, org, from)
	must(err)
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',now(),$3)`, closeID, connection, string(payload))
	must(err)
	unanswerable, _ := draft("", 200)
	_, err = db.Exec(`UPDATE email_connections SET enabled=false WHERE organization_id=$1`, org)
	must(err)
	krith, _ = sender()
	if got := call("POST", collections, map[string]any{"action": "approve", "message_id": unanswerable, "connection_id": krith, "confirmed": true}, 409); !strings.Contains(got, "takes no replies") {
		t.Fatalf("Krith's sender needs the reply address at approval too: %s", got)
	}
}
