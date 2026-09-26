package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/secrets"
	"close.local/backend/internal/xero"
	"github.com/golang-jwt/jwt/v5"
)

func TestXeroBillLineIsRecodedOnceAfterIndependentApproval(t *testing.T) {
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
	org, entity, tenant := "org_XeroFix"+rand.Text(), "Xero fix client", "t-"+rand.Text()
	var closeID, connection string
	_, err = db.Exec(`INSERT INTO organizations(id,name,require_independent_review) VALUES($1,'Xero correction fixture',true)`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture'),($1,'user_reviewer',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM close_signoffs WHERE close_id=$1`, `DELETE FROM xero_corrections WHERE close_id=$1`, `DELETE FROM xero_snapshots WHERE close_id=$1`} {
			db.Exec(q, closeID)
		}
		for _, q := range []string{`DELETE FROM xero_posting_grants WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id,tenant_name,access_expires_at) VALUES($1,$2,$3,'fixture','active',$4,now(),$5,'Xero fix client',now()+interval '1 hour') RETURNING id::text`, closeID, org, entity, rand.Text(), tenant).Scan(&connection))
	seal := func(scope ...string) {
		t.Helper()
		claims, _ := json.Marshal(map[string]any{"scope": scope})
		raw, _ := json.Marshal(xero.Credentials{Access: "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig", Refresh: "refresh", Expires: 1800, Type: "Bearer"})
		sealed, e := secrets.Seal(org+"/xero/"+connection, raw)
		must(e)
		_, e = db.Exec(`UPDATE xero_connections SET credential=$2 WHERE id=$1`, connection, sealed)
		must(e)
	}
	seal("accounting.invoices.read")

	const general, equipment, travel, other, liability, uncoded = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444", "55555555-5555-5555-5555-555555555555", "66666666-6666-6666-6666-666666666666"
	line := func(id, account, code, amount string) map[string]any {
		return map[string]any{"LineItemID": id, "Description": "Line " + id, "Quantity": 1, "UnitAmount": json.Number(amount), "AccountID": account, "AccountCode": code, "TaxType": "NONE", "TaxAmount": 0, "LineAmount": json.Number(amount)}
	}
	bill := map[string]any{"InvoiceID": "i-1", "Type": "ACCPAY", "Status": "AUTHORISED", "Contact": map[string]string{"ContactID": "c-1"}, "Date": "/Date(1790208000000+0000)/", "DueDate": "/Date(1790812800000+0000)/", "InvoiceNumber": "B-7", "CurrencyCode": "USD", "LineAmountTypes": "NoTax", "SubTotal": 130, "TotalTax": 0, "Total": 130, "AmountDue": 130, "AmountPaid": 0, "AmountCredited": 0,
		"LineItems": []any{line("l1", general, "429", "100.00"), line("l2", travel, "493", "50.00"), line("l3", general, "429", "-20.00")}}
	sale := map[string]any{"InvoiceID": "i-3", "Type": "ACCREC", "Status": "AUTHORISED", "Contact": map[string]string{"ContactID": "c-2"}, "Date": "/Date(1790208000000+0000)/", "CurrencyCode": "USD", "LineAmountTypes": "NoTax", "Total": 9, "AmountPaid": 0, "AmountCredited": 0, "TotalTax": 0, "LineItems": []any{line("s1", general, "429", "9.00")}}
	paid := map[string]any{"InvoiceID": "i-2", "Type": "ACCPAY", "Status": "PAID", "Contact": map[string]string{"ContactID": "c-1"}, "Date": "/Date(1790208000000+0000)/", "CurrencyCode": "USD", "LineAmountTypes": "NoTax", "Total": 5, "AmountPaid": 5, "AmountCredited": 0, "TotalTax": 0, "LineItems": []any{line("p1", general, "429", "5.00")}}
	captureOn := func(on, requested, saved string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"transactions": map[string]any{"records": map[string]any{"Invoices": []any{bill, paid, sale}}}})
		_, e := db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,$3,'2026-09','d',$4::jsonb||jsonb_build_object('trial_balance_requested_at',now()+$5::interval),'fixture',now()+$6::interval)`, closeID, on, tenant, string(payload), requested, saved)
		must(e)
	}
	capture := func(at string) { t.Helper(); captureOn(connection, at, at) }
	capture("-1 hour")
	live, _ := json.Marshal(bill)
	tamper, posts, lock, equipmentName := false, 0, "/Date(1719705600000+0000)/", "Equipment"
	var sent struct {
		key   string
		lines []map[string]any
	}
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		reply := func(body any) (*http.Response, error) {
			raw, _ := json.Marshal(body)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/Accounts"):
			return reply(map[string]any{"Accounts": []map[string]string{{"AccountID": general, "Code": "429", "Name": "General", "Status": "ACTIVE", "Class": "EXPENSE"}, {"AccountID": equipment, "Code": "720", "Name": equipmentName, "Status": "ACTIVE", "Class": "EXPENSE"}, {"AccountID": travel, "Code": "493", "Name": "Travel", "Status": "ACTIVE", "Class": "EXPENSE"}, {"AccountID": other, "Code": "800", "Name": "Other", "Status": "ACTIVE", "Class": "EXPENSE"}, {"AccountID": liability, "Code": "820", "Name": "Accrued", "Status": "ACTIVE", "Class": "LIABILITY"}, {"AccountID": uncoded, "Name": "Uncoded", "Status": "ACTIVE", "Class": "EXPENSE"}}})
		case strings.HasSuffix(r.URL.Path, "/Organisation"):
			return reply(map[string]any{"Organisations": []map[string]string{{"PeriodLockDate": lock}}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/Invoices/i-1"):
			posts++
			var body struct{ Invoices []struct{ LineItems []map[string]any } }
			json.NewDecoder(r.Body).Decode(&body)
			sent.key, sent.lines = r.Header.Get("Idempotency-Key"), body.Invoices[0].LineItems
			var current map[string]any
			json.Unmarshal(live, &current)
			for i, l := range current["LineItems"].([]any) {
				l.(map[string]any)["AccountID"], l.(map[string]any)["AccountCode"] = sent.lines[i]["AccountID"], sent.lines[i]["AccountCode"]
			}
			live, _ = json.Marshal(current)
			return reply(map[string]any{"Invoices": []any{current}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/Invoices/i-1"):
			var current map[string]any
			json.Unmarshal(live, &current)
			if tamper {
				current["LineItems"].([]any)[1].(map[string]any)["AccountCode"] = "800"
			}
			return reply(map[string]any{"Invoices": []any{current}})
		}
		return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	})

	accountant := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "posting:approve", "posting:execute"}}
	reviewer := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_reviewer"}, Organization: org, Permissions: []string{"clients:all", "posting:approve"}}
	mux := http.NewServeMux()
	registerXeroPosting(mux, db)
	registerXeroCorrections(mux, db)
	call := func(actor *auth.Claims, method, path string, input any, want int) map[string]any {
		t.Helper()
		body, _ := json.Marshal(input)
		r := httptest.NewRequest(method, path, bytes.NewReader(body)).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: HTTP %d %s", method, path, input, w.Code, w.Body.String())
		}
		out := map[string]any{}
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	base := "/v1/closes/" + closeID + "/xero/bills/"
	path := base + "i-1/correction"
	if got := call(accountant, "GET", base+"candidates", nil, 200)["items"].([]any); len(got) != 1 || got[0].(map[string]any)["id"] != "i-1" {
		t.Fatalf("only the unpaid bill of the close month is a candidate: %v", got)
	}
	if got := call(accountant, "GET", path, nil, 200); got["state"] != "not_prepared" || got["before_bill"] == nil {
		t.Fatalf("a captured bill shows its lines before any preview: %v", got)
	}
	preview := func(lineID, account string) map[string]any {
		return map[string]any{"action": "preview", "reason": "Laptop is equipment per invoice B-7", "rule_reference": "Policy 4.2: assets over 50", "target_line_id": lineID, "target_account_id": account, "confirmed": true}
	}
	message := func(out map[string]any) string { s, _ := out["message"].(string); return s }
	set := func(q string, args ...any) {
		t.Helper()
		_, e := db.Exec(q, args...)
		must(e)
	}
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "not enabled") {
		t.Fatalf("bill corrections stay off until the operator enables them: %v", got)
	}
	t.Setenv("XERO_POSTING_KINDS", "bill_correction")
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "covers bill corrections") {
		t.Fatalf("a grant covering bill corrections is required: %v", got)
	}
	call(accountant, "POST", "/v1/closes/"+closeID+"/xero-grant", map[string]any{"action": "grant", "connection_id": connection, "account_ids": []string{general, equipment, travel, liability, uncoded}, "kinds": []string{"bill_correction"}, "max_amount": "500.00", "expires_at": time.Now().Add(time.Hour), "reason": "Reviewed synthetic bill recodes", "confirmation": "GRANT XERO POSTING"}, 200)
	set(`UPDATE xero_posting_grants SET kinds=ARRAY['accrual'] WHERE organization_id=$1`, org)
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "covers bill corrections") {
		t.Fatalf("a grant for other kinds does not cover bill corrections: %v", got)
	}
	set(`UPDATE xero_posting_grants SET kinds=ARRAY['bill_correction'] WHERE organization_id=$1`, org)
	set(`UPDATE xero_connections SET tenant_id='t-other' WHERE id=$1`, connection)
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "different organisation") {
		t.Fatalf("a connection moved to another organisation is refused: %v", got)
	}
	set(`UPDATE xero_connections SET tenant_id=$2 WHERE id=$1`, connection, tenant)
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "Reconnect Xero") {
		t.Fatalf("a read-only invoice scope must reconnect: %v", got)
	}
	seal("accounting.invoices")
	var former string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,$3,'fixture','disconnected',$4,now(),$5) RETURNING id::text`, closeID, org, entity, rand.Text(), tenant).Scan(&former))
	captureOn(former, "-1 hour", "-1 hour")
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "granted connection") {
		t.Fatalf("a capture from another connection is not the reviewed source: %v", got)
	}
	capture("-1 hour")
	for _, account := range []string{other, general, liability, uncoded} {
		call(accountant, "POST", path, preview("l1", account), 422)
	}
	unconfirmed := preview("l1", equipment)
	unconfirmed["confirmed"] = false
	call(accountant, "POST", path, unconfirmed, 422)
	if got := call(accountant, "POST", path, preview("l3", equipment), 409); !strings.Contains(message(got), "positive amount") {
		t.Fatalf("a negative line is refused: %v", got)
	}
	saved, plain := live, bill
	edited := func(edit func(map[string]any)) {
		var b map[string]any
		json.Unmarshal(saved, &b)
		edit(b)
		live, _ = json.Marshal(b)
	}
	edited(func(b map[string]any) { b["Reference"] = "edited" })
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "changed in Xero") {
		t.Fatalf("a bill edited since the capture is refused: %v", got)
	}
	first := func(b map[string]any) map[string]any { return b["LineItems"].([]any)[0].(map[string]any) }
	for name, edit := range map[string]func(map[string]any){
		"paid":          func(b map[string]any) { b["AmountPaid"] = 10 },
		"credited":      func(b map[string]any) { b["AmountCredited"] = 10 },
		"taxed":         func(b map[string]any) { b["TotalTax"] = 1 },
		"another month": func(b map[string]any) { b["Date"] = "2026-08-31T00:00:00" },
		"draft":         func(b map[string]any) { b["Status"] = "DRAFT" },
		"item line":     func(b map[string]any) { first(b)["ItemCode"] = "LAPTOP" },
		"stale code":    func(b map[string]any) { first(b)["AccountCode"] = "999" },
		"over limit":    func(b map[string]any) { first(b)["LineAmount"] = 600 },
		"zero line":     func(b map[string]any) { first(b)["LineAmount"] = 0 },
		"eleven lines": func(b map[string]any) {
			for i := 4; i <= 11; i++ {
				b["LineItems"] = append(b["LineItems"].([]any), line(fmt.Sprint("l", i), travel, "493", "1.00"))
			}
		},
	} {
		edited(edit)
		bill = nil
		json.Unmarshal(live, &bill)
		capture("-1 hour")
		if got := call(accountant, "POST", path, preview("l1", equipment), 409); message(got) == "" {
			t.Fatalf("a %s bill is refused with a reason: %v", name, got)
		}
	}
	live, bill = saved, plain
	capture("-1 hour")
	lock = "/Date(1790208000000+0000)/"
	if got := call(accountant, "POST", path, preview("l1", equipment), 409); !strings.Contains(message(got), "lock date") {
		t.Fatalf("a bill inside Xero's lock date is refused: %v", got)
	}
	lock = "/Date(1719705600000+0000)/"
	item := call(accountant, "POST", path, preview("l1", equipment), 200)
	if item["state"] != "preview" || item["target_account_code"] != "720" {
		t.Fatalf("preview binds the new account's code: %v", item)
	}
	if got := call(accountant, "POST", path, map[string]any{"action": "verify"}, 409); !strings.Contains(message(got), "Only an attempted") {
		t.Fatalf("a preview has nothing to verify: %v", got)
	}
	call(reviewer, "POST", path, map[string]any{"action": "approve", "digest": "other", "confirmed": true}, 409)
	approve := map[string]any{"action": "approve", "digest": item["digest"], "confirmed": true}
	equipmentName = "Equipment and tools"
	if got := call(reviewer, "POST", path, approve, 409); !strings.Contains(message(got), "changed") {
		t.Fatalf("an account renamed since the preview asks for a new one: %v", got)
	}
	equipmentName = "Equipment"
	if got := call(accountant, "POST", path, approve, 409); !strings.Contains(message(got), "independent") {
		t.Fatalf("whoever previewed the correction cannot approve it: %v", got)
	}
	call(reviewer, "POST", path, approve, 200)
	execute := map[string]any{"action": "execute", "digest": item["digest"], "confirmed": true}
	call(reviewer, "POST", path, execute, 403)
	set(`UPDATE xero_corrections SET state='executing' WHERE close_id=$1`, closeID)
	call(accountant, "POST", path, execute, 409)
	set(`UPDATE xero_corrections SET state='approved' WHERE close_id=$1`, closeID)
	set(`UPDATE xero_posting_grants SET granted_by='user_gone' WHERE organization_id=$1`, org)
	call(accountant, "POST", path, execute, 409)
	set(`UPDATE xero_posting_grants SET granted_by='user_accountant' WHERE organization_id=$1`, org)
	if posts != 0 {
		t.Fatalf("a refused execution sends nothing: %d", posts)
	}
	done := call(accountant, "POST", path, execute, 200)
	if done["state"] != "verified" || posts != 1 || sent.key != done["request_id"] || len(sent.lines) != 3 || sent.lines[0]["AccountID"] != equipment || sent.lines[0]["AccountCode"] != "720" || sent.lines[1]["AccountCode"] != "493" || sent.lines[0]["TaxAmount"] != nil {
		t.Fatalf("one update resends every line with only the approved line's account changed, and reads back: %v %d %+v", done, posts, sent)
	}
	blockers := func() string {
		t.Helper()
		tx, e := db.Begin()
		must(e)
		defer tx.Rollback()
		ready, e := closeReadiness(context.Background(), tx, closeID)
		must(e)
		return strings.Join(ready.Blockers, " | ")
	}
	if got := blockers(); !strings.Contains(got, "Capture Xero again") {
		t.Fatalf("sign-off waits for a capture after the correction: %s", got)
	}
	bill = nil
	json.Unmarshal(live, &bill)
	captureOn(connection, "-20 minutes", "1 second")
	if got := blockers(); !strings.Contains(got, "Capture Xero again") {
		t.Fatalf("a capture that read Xero before the correction does not count, however late it was saved: %s", got)
	}
	set(`UPDATE xero_corrections SET verified_at=now()-interval '10 minutes' WHERE close_id=$1`, closeID)
	capture("-5 minutes")
	if got := blockers(); strings.Contains(got, "Xero") {
		t.Fatalf("a newer capture clears the correction's blocker: %s", got)
	}
	call(accountant, "POST", path, execute, 422)
	if got := call(accountant, "POST", path, preview("l2", equipment), 409); !strings.Contains(message(got), "attempted") {
		t.Fatalf("an attempted correction is not replaced by a new preview: %v", got)
	}
	if posts != 1 {
		t.Fatalf("an attempted correction is never sent again: %d", posts)
	}
	verify := map[string]any{"action": "verify"}
	tamper = true
	call(accountant, "POST", path, verify, 409)
	if got := blockers(); !strings.Contains(got, "Xero bill correction attempt") {
		t.Fatalf("a readback that differs leaves the attempt unknown and blocks sign-off: %s", got)
	}
	tamper = false
	_, err = db.Exec(`UPDATE xero_posting_grants SET revoked_at=now(),revoked_by='fixture' WHERE organization_id=$1`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed fixture','fixture')`, closeID)
	must(err)
	if got := call(accountant, "POST", path, verify, 200); got["state"] != "verified" {
		t.Fatalf("an attempt is settled after its grant ends and after sign-off: %v", got)
	}
	if got := blockers(); strings.Contains(got, "Capture Xero again") {
		t.Fatalf("reading back again keeps the first verification time, so a later capture still counts: %s", got)
	}
	if got := call(accountant, "POST", path, preview("l2", equipment), 409); !strings.Contains(message(got), "open close") {
		t.Fatalf("a signed-off close takes no new correction: %v", got)
	}
}
