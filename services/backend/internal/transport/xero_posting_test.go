package transport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

func TestApprovedDraftPostsOnceToXero(t *testing.T) {
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
	org, entity := "org_XeroPost"+rand.Text(), "Xero post client"
	var closeID, connection string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Xero posting fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM close_signoffs WHERE close_id=$1`, `DELETE FROM xero_postings WHERE close_id=$1`, `DELETE FROM production_postings WHERE close_id=$1`, `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`} {
			db.Exec(q, closeID)
		}
		for _, q := range []string{`DELETE FROM procedure_versions WHERE organization_id=$1`, `DELETE FROM xero_posting_grants WHERE organization_id=$1`, `DELETE FROM production_posting_grants WHERE organization_id=$1`, `DELETE FROM xero_connections WHERE organization_id=$1`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id,tenant_name,access_expires_at,home_currency,fiscal_year_start) VALUES($1,$2,$3,'fixture','active',$4,now(),'t-1','Xero post client',now()+interval '1 hour','USD',1) RETURNING id::text`, closeID, org, entity, rand.Text()).Scan(&connection))
	seal := func(scope ...string) {
		t.Helper()
		claims, _ := json.Marshal(map[string]any{"scope": scope})
		raw, _ := json.Marshal(xero.Credentials{Access: "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig", Refresh: "refresh", Expires: 1800, Type: "Bearer"})
		sealed, e := secrets.Seal(org+"/xero/"+connection, raw)
		must(e)
		_, e = db.Exec(`UPDATE xero_connections SET credential=$2 WHERE id=$1`, connection, sealed)
		must(e)
	}
	seal("accounting.reports.trialbalance.read")
	paper := func(source, amount string, debit ...string) (string, string) {
		t.Helper()
		var evidence, digest, id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'estimate.txt',$2,'Estimate','fixture') RETURNING id::text,digest`, closeID, rand.Text()).Scan(&evidence, &digest))
		account := append(debit, "Utilities")[0]
		body, _ := json.Marshal(map[string]any{"input": map[string]string{"entity": entity, "close_period": "2026-09", "currency": "USD", "source_reference": source}, "proposal": map[string]string{"date": "2026-09-30", "debit_account": account, "credit_account": "Accrued utilities", "amount": amount}, "sources": []map[string]string{{"id": evidence, "digest": digest, "role": "invoice"}}, "blockers": []string{}})
		paperDigest := rand.Text()
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'accrual',$2,$3) RETURNING id::text`, closeID, paperDigest, string(body)).Scan(&id))
		if len(debit) < 2 {
			_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Reviewed','user_reviewer')`, id, paperDigest)
			must(e)
		}
		return id, "/v1/closes/" + closeID + "/workpapers/" + id + "/xero-posting"
	}

	const utilities, accrued = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	lock, balance, tamper, renamed, puts := "/Date(1719705600000+0000)/", "1000.00", false, false, 0
	keys := []string{}
	posted := map[string]xero.ManualJournal{}
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		reply := func(status int, body any) (*http.Response, error) {
			raw, _ := json.Marshal(body)
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/Accounts"):
			return reply(200, map[string]any{"Accounts": []map[string]string{{"AccountID": utilities, "Name": "Utilities", "Status": "ACTIVE"}, {"AccountID": accrued, "Name": "Accrued utilities", "Status": "ACTIVE"}, {"AccountID": "33333333-3333-3333-3333-333333333333", "Name": "Utilities", "Status": "ARCHIVED"}, {"AccountID": "55555555-5555-5555-5555-555555555555", "Name": "Suspense", "Status": "ACTIVE"}}})
		case strings.HasSuffix(r.URL.Path, "/Organisation"):
			return reply(200, map[string]any{"Organisations": []map[string]string{{"PeriodLockDate": lock}}})
		case strings.HasSuffix(r.URL.Path, "/Reports/TrialBalance"):
			row := func(id, label, debit, credit string) map[string]any {
				return map[string]any{"RowType": "Row", "Cells": []any{map[string]any{"Value": label, "Attributes": []any{map[string]string{"Id": "account", "Value": id}}}, map[string]string{"Value": ""}, map[string]string{"Value": ""}, map[string]string{"Value": debit}, map[string]string{"Value": credit}}}
			}
			return reply(200, map[string]any{"Reports": []any{map[string]any{"ReportType": "TrialBalance", "ReportDate": "30 September 2026", "Rows": []any{
				map[string]any{"RowType": "Header", "Cells": []any{map[string]string{"Value": "Account"}, map[string]string{"Value": "Debit"}, map[string]string{"Value": "Credit"}, map[string]string{"Value": "YTD Debit"}, map[string]string{"Value": "YTD Credit"}}},
				map[string]any{"RowType": "Section", "Rows": []any{row(utilities, "Utilities", balance, "")}},
			}}}})
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/ManualJournals"):
			puts++
			keys = append(keys, r.Header.Get("Idempotency-Key"))
			var body struct{ ManualJournals []xero.ManualJournal }
			json.NewDecoder(r.Body).Decode(&body)
			journal := body.ManualJournals[0]
			journal.ManualJournalID = "44444444-4444-4444-4444-44444444444" + string(rune('0'+puts))
			journal.Date = "/Date(1790726400000+0000)/"
			posted[journal.ManualJournalID] = journal
			return reply(200, map[string]any{"ManualJournals": []xero.ManualJournal{journal}})
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/ManualJournals/"):
			journal, ok := posted[r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]]
			if !ok {
				return reply(404, map[string]string{})
			}
			if tamper {
				journal.JournalLines = []xero.JournalLine{{AccountID: utilities, LineAmount: "999.00"}, {AccountID: accrued, LineAmount: "-999.00"}}
			}
			if renamed {
				journal.Narration = "Someone else's journal"
			}
			return reply(200, map[string]any{"ManualJournals": []xero.ManualJournal{journal}})
		}
		return reply(404, map[string]string{})
	})

	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "posting:approve", "posting:execute"}}
	mux := http.NewServeMux()
	registerXeroPosting(mux, db)
	mux.HandleFunc("GET /v1/closes/{id}/export", exportClose(db, false))
	// A close with no Xero journal keeps the signed snapshot it had, so earlier sign-offs stand.
	plainTx, err := db.Begin()
	must(err)
	plain, err := closeReadiness(context.Background(), plainTx, closeID)
	plainTx.Rollback()
	must(err)
	if strings.Contains(string(plain.Snapshot), "xero_postings") {
		t.Fatalf("a close without Xero journals has no Xero key in its signed snapshot: %s", plain.Snapshot)
	}
	call := func(path string, input any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", path, bytes.NewReader(body)).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %v: HTTP %d %s", path, input, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	first, path := paper("Utility estimate September", "300.00")
	call(path, map[string]string{"action": "preview"}, 409)
	grant := map[string]any{"action": "grant", "connection_id": connection, "account_ids": []string{utilities, accrued, "33333333-3333-3333-3333-333333333333"}, "kinds": []string{"accrual"}, "max_amount": "1000.00", "expires_at": time.Now().Add(time.Hour), "reason": "Reviewed synthetic Xero posting", "confirmation": "GRANT XERO POSTING"}
	call("/v1/closes/"+closeID+"/xero-grant", grant, 200)
	if body := call(path, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "not enabled") {
		t.Fatalf("every kind stays off until the operator enables it: %s", body)
	}
	t.Setenv("XERO_POSTING_KINDS", "accrual")
	if body := call(path, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "Reconnect Xero") {
		t.Fatalf("a connection without the manual journal scope must reconnect: %s", body)
	}
	seal("accounting.reports.trialbalance.read", "accounting.manualjournals")
	lock = "/Date(1790726400000+0000)/"
	if body := call(path, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "lock date") {
		t.Fatalf("a journal inside Xero's lock date is refused: %s", body)
	}
	lock = "/Date(1719705600000+0000)/"
	_, suspense := paper("Suspense estimate", "10.00", "Suspense")
	if body := call(suspense, map[string]string{"action": "preview"}, 422); !strings.Contains(string(body), "No granted, active Xero account") {
		t.Fatalf("an account the grant does not list is refused: %s", body)
	}
	_, large := paper("Large estimate", "1500.00")
	if body := call(large, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "limit") {
		t.Fatalf("a journal above the grant limit is refused: %s", body)
	}
	_, err = db.Exec(`UPDATE xero_connections SET tenant_id='t-2' WHERE id=$1`, connection)
	must(err)
	if body := call(path, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "different organisation") {
		t.Fatalf("a connection now pointing at another organisation is refused: %s", body)
	}
	_, err = db.Exec(`UPDATE xero_connections SET tenant_id='t-1' WHERE id=$1`, connection)
	must(err)
	var intent xeroPosting
	must(json.Unmarshal(call(path, map[string]string{"action": "preview"}, 200), &intent))
	var journal xero.ManualJournal
	json.Unmarshal(intent.Journal, &journal)
	if intent.State != "preview" || len(journal.JournalLines) != 2 || journal.JournalLines[0].AccountID != utilities || journal.JournalLines[0].LineAmount != "300.00" || journal.JournalLines[1].AccountID != accrued || journal.JournalLines[1].LineAmount != "-300.00" || journal.Status != "POSTED" {
		t.Fatalf("one POSTED manual journal on the granted active accounts: %+v %+v", intent, journal)
	}
	call(path, map[string]string{"action": "approve", "digest": "stale", "confirmation": "APPROVE XERO DRAFT"}, 409)
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor,method) VALUES($1,$2,'basis','Cash basis',true,'Synthetic basis','fixture','cash')`, org, entity)
	must(err)
	if body := call(path, map[string]string{"action": "approve", "digest": intent.Digest, "confirmation": "APPROVE XERO DRAFT"}, 409); !strings.Contains(string(body), "basis") {
		t.Fatalf("a cash-basis client's accrual cannot be approved for posting: %s", body)
	}
	_, err = db.Exec(`DELETE FROM procedure_versions WHERE organization_id=$1 AND kind='basis'`, org)
	must(err)
	must(json.Unmarshal(call(path, map[string]string{"action": "approve", "digest": intent.Digest, "confirmation": "APPROVE XERO DRAFT"}, 200), &intent))
	balance = "1100.00"
	if body := call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 409); !strings.Contains(string(body), "changed") || puts != 0 {
		t.Fatalf("a changed balance stops the write: %s", body)
	}
	balance = "1000.00"
	_, err = db.Exec(`UPDATE xero_postings SET approval_expires_at=now()-interval '1 second' WHERE workpaper_id=$1`, first)
	must(err)
	if body := call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 409); !strings.Contains(string(body), "APPROVAL_EXPIRED") || puts != 0 {
		t.Fatalf("a lapsed approval says so and writes nothing: %s", body)
	}
	call(path, map[string]string{"action": "approve", "digest": intent.Digest, "confirmation": "APPROVE XERO DRAFT"}, 200)

	// A draft already posted to QuickBooks cannot also reach Xero.
	var qbo, qboGrant string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment) VALUES($1,$2,$3,'fixture','active',$4,now(),'123',$3,'production') RETURNING id::text`, closeID, org, entity, rand.Text()).Scan(&qbo))
	must(db.QueryRow(`INSERT INTO production_posting_grants(organization_id,client_entity,connection_id,realm,account_ids,kinds,max_amount,expires_at,reason,granted_by) VALUES($1,$2,$3,'123','{1,2}','{accrual}',100,now()+interval '1 hour','Synthetic','fixture') RETURNING id::text`, org, entity, qbo).Scan(&qboGrant))
	_, err = db.Exec(`INSERT INTO production_postings(close_id,workpaper_id,source_key,grant_id,connection_id,realm,digest,journal,ledger_before,state,attempted_at) VALUES($1,$2,'other',$3,$4,'123','d','{}','{}','unknown',now())`, closeID, first, qboGrant, qbo)
	must(err)
	if body := call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 409); !strings.Contains(string(body), "QuickBooks") || puts != 0 {
		t.Fatalf("a draft attempted in QuickBooks is refused: %s", body)
	}
	_, err = db.Exec(`DELETE FROM production_postings WHERE close_id=$1`, closeID)
	must(err)

	actor.Permissions = []string{"clients:all", "posting:approve"}
	call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 403)
	actor.Permissions = []string{"clients:all", "posting:execute"}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed fixture','fixture')`, closeID)
	must(err)
	if body := call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 409); !strings.Contains(string(body), "signed off") || puts != 0 {
		t.Fatalf("a signed-off close takes no Xero write: %s puts=%d", body, puts)
	}
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'reopened','d','{}','Reopened fixture','fixture')`, closeID)
	must(err)
	must(json.Unmarshal(call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 200), &intent))
	if puts != 1 || keys[0] != intent.RequestID || intent.State != "verified" || intent.ExternalID == "" {
		t.Fatalf("one write with the saved request as its idempotency key, read back and verified: puts=%d %v %+v", puts, keys, intent)
	}
	call(path, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 409)
	if puts != 1 {
		t.Fatal("a verified journal is never posted again")
	}
	exported := httptest.NewRecorder()
	mux.ServeHTTP(exported, httptest.NewRequest("GET", "/v1/closes/"+closeID+"/export", nil).WithContext(auth.WithActor(context.Background(), actor)))
	archive, err := zip.NewReader(bytes.NewReader(exported.Body.Bytes()), int64(exported.Body.Len()))
	if err != nil {
		t.Fatalf("export: %d %s", exported.Code, exported.Body.String())
	}
	var manifest closePackage
	for _, file := range archive.File {
		if file.Name == "manifest.json" {
			reader, e := file.Open()
			must(e)
			must(json.NewDecoder(reader).Decode(&manifest))
			reader.Close()
		}
	}
	if !strings.Contains(string(manifest.XeroPostings), intent.ExternalID) {
		t.Fatalf("the close package carries the verified Xero journal's receipt: %s", manifest.XeroPostings)
	}
	var sourceKey string
	must(db.QueryRow(`SELECT source_key FROM xero_postings WHERE workpaper_id=$1`, first).Scan(&sourceKey))
	for _, probe := range [][2]string{{first, ""}, {"0", sourceKey}} {
		if posted, e := postedElsewhere(context.Background(), db, "xero_postings", closeID, probe[0], probe[1]); e != nil || !posted {
			t.Fatalf("QuickBooks refuses a draft, or another draft of its source, already posted to Xero: %v %v", probe, e)
		}
	}

	// A pending draft needs a person who can also review drafts to approve it with its posting.
	actor.Permissions = []string{"clients:all", "posting:approve"}
	_, pending := paper("Pending estimate", "50.00", "Utilities", "unreviewed")
	must(json.Unmarshal(call(pending, map[string]string{"action": "preview"}, 200), &intent))
	if body := call(pending, map[string]string{"action": "approve", "digest": intent.Digest, "confirmation": "APPROVE XERO DRAFT"}, 409); !strings.Contains(string(body), "review drafts") {
		t.Fatalf("a pending draft is not approved by someone who cannot review drafts: %s", body)
	}

	// A readback that differs leaves the attempt unknown; a person verifies it by its ID without another write.
	_, second := paper("Utility estimate September water", "200.00")
	actor.Permissions = []string{"clients:all", "posting:approve", "posting:execute"}
	must(json.Unmarshal(call(second, map[string]string{"action": "preview"}, 200), &intent))
	call(second, map[string]string{"action": "approve", "digest": intent.Digest, "confirmation": "APPROVE XERO DRAFT"}, 200)
	_, third := paper("Utility estimate September gas", "20.00")
	var competing xeroPosting
	must(json.Unmarshal(call(third, map[string]string{"action": "preview"}, 200), &competing))
	if body := call(third, map[string]string{"action": "approve", "digest": competing.Digest, "confirmation": "APPROVE XERO DRAFT"}, 409); !strings.Contains(string(body), "uses these accounts") {
		t.Fatalf("a second intent on the same accounts waits until the first settles: %s", body)
	}
	tamper = true
	if body := call(second, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 502); !strings.Contains(string(body), "EXECUTION_UNKNOWN") {
		t.Fatalf("an unconfirmed journal is unknown, not verified: %s", body)
	}
	call(second, map[string]string{"action": "execute", "digest": intent.Digest, "confirmation": "POST TO XERO"}, 409)
	// Xero receipts are part of the signed snapshot, so a later change to one reopens the close.
	digest := func() string {
		t.Helper()
		readTx, e := db.Begin()
		must(e)
		defer readTx.Rollback()
		ready, e := closeReadiness(context.Background(), readTx, closeID)
		must(e)
		return ready.Digest
	}
	before := digest()
	_, err = db.Exec(`UPDATE xero_postings SET last_error='Readback differs' WHERE id=(SELECT min(id) FROM xero_postings WHERE close_id=$1)`, closeID)
	must(err)
	if digest() == before {
		t.Fatal("a changed Xero receipt must change the close's signed snapshot")
	}
	// An unknown Xero journal blocks sign-off until it is verified.
	readTx, err := db.Begin()
	must(err)
	ready, err := closeReadiness(context.Background(), readTx, closeID)
	readTx.Rollback()
	must(err)
	if !strings.Contains(strings.Join(ready.Blockers, " "), "Xero journal attempt #") {
		t.Fatalf("an unknown Xero journal must block sign-off: %v", ready.Blockers)
	}
	call(second, map[string]string{"action": "verify"}, 502)
	tamper, renamed = false, true
	call(second, map[string]string{"action": "verify"}, 502)
	renamed = false
	// A reconnection to the same organisation can still verify the saved attempt.
	_, err = db.Exec(`UPDATE xero_connections SET state='disconnected',credential=NULL WHERE id=$1`, connection)
	must(err)
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id,tenant_name,access_expires_at,home_currency,fiscal_year_start) VALUES($1,$2,$3,'fixture','active',$4,now(),'t-1','Xero post client',now()+interval '1 hour','USD',1) RETURNING id::text`, closeID, org, entity, rand.Text()).Scan(&connection))
	seal("accounting.manualjournals")
	must(json.Unmarshal(call(second, map[string]string{"action": "verify"}, 200), &intent))
	if intent.State != "verified" || puts != 2 {
		t.Fatalf("verification reads back the saved journal without posting again: %+v puts=%d", intent, puts)
	}
}
