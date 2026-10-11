package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/secrets"
	"close.local/backend/internal/xero"
	"github.com/golang-jwt/jwt/v5"
)

func TestXeroConnectsOneOrganisationAndCapturesTheTrialBalance(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", database)
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
	org := "org_Xero" + rand.Text()
	var september, october, due string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Synthetic Xero firm')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture'),($1,'user_other',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Xero client','2026-09') RETURNING id::text`, org).Scan(&september))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Xero client','2026-10') RETURNING id::text`, org).Scan(&october))
	defer func() {
		for _, id := range []string{september, october, due} {
			db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, id)
		}
		db.Exec(`DELETE FROM xero_posting_grants WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM xero_connections WHERE organization_id=$1`, org)
		for _, id := range []string{due, september, october} {
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	t.Setenv("XERO_CLIENT_ID", "synthetic-client")
	t.Setenv("XERO_CLIENT_SECRET", "synthetic-secret")
	t.Setenv("XERO_REDIRECT_URI", "https://api.example.test/v1/xero/callback")
	t.Setenv("PUBLIC_APP_URL", "https://app.example.test")
	t.Setenv("SECRETS_KEY", strings.Repeat("cd", 32))

	access := func(n string) string {
		fields := map[string]string{"authentication_event_id": "event-now", "n": n}
		// The last rotated token carries the transaction read scopes.
		if n == "refresh-3" {
			fields["scope"] = "accounting.invoices.read accounting.payments.read accounting.banktransactions.read accounting.manualjournals"
		}
		claims, _ := json.Marshal(fields)
		return "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
	}
	var tenantHeaders, dates, refreshes []string
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		reply := func(status int, body any) (*http.Response, error) {
			raw, _ := json.Marshal(body)
			return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
		}
		switch {
		case r.URL.Host == "identity.xero.com":
			r.ParseForm()
			switch r.PostForm.Get("grant_type") {
			case "authorization_code":
				return reply(200, map[string]any{"access_token": access("1"), "refresh_token": "refresh-1", "expires_in": 1800, "token_type": "Bearer"})
			case "refresh_token":
				refreshes = append(refreshes, r.PostForm.Get("refresh_token"))
				// Xero rotates refresh tokens: each one works once.
				next := map[string]string{"refresh-1": "refresh-2", "refresh-2": "refresh-3"}[r.PostForm.Get("refresh_token")]
				if next == "" {
					return reply(400, map[string]string{"error": "invalid_grant"})
				}
				return reply(200, map[string]any{"access_token": access(next), "refresh_token": next, "expires_in": 1800, "token_type": "Bearer"})
			}
		case r.URL.Path == "/connections":
			return reply(200, []map[string]string{
				{"tenantId": "t-a", "tenantName": "Harbor Dental Ltd", "tenantType": "ORGANISATION", "authEventId": "event-now"},
				{"tenantId": "t-b", "tenantName": "Harbor Holdings Ltd", "tenantType": "ORGANISATION", "authEventId": "event-now"},
				{"tenantId": "t-old", "tenantName": "Earlier consent", "tenantType": "ORGANISATION", "authEventId": "event-before"},
				{"tenantId": "t-practice", "tenantName": "A practice", "tenantType": "PRACTICE", "authEventId": "event-now"},
			})
		case strings.HasSuffix(r.URL.Path, "/Organisation"):
			return reply(200, map[string]any{"Organisations": []any{map[string]any{"Name": "Harbor Holdings Ltd", "BaseCurrency": "NZD", "FinancialYearEndMonth": 3}}})
		case strings.HasSuffix(r.URL.Path, "/Accounts"):
			return reply(200, map[string]any{"Accounts": []any{map[string]string{"AccountID": "a-090", "Code": "090", "Name": "Business Bank Account", "Class": "ASSET"}}})
		case strings.HasSuffix(r.URL.Path, "/Reports/TrialBalance"):
			tenantHeaders = append(tenantHeaders, r.Header.Get("xero-tenant-id"))
			dates = append(dates, r.URL.Query().Get("date"))
			when, _ := time.Parse("2006-01-02", r.URL.Query().Get("date"))
			label := when.Format("2 January 2006")
			return reply(200, map[string]any{"Reports": []any{map[string]any{"ReportType": "TrialBalance", "ReportDate": label, "Rows": []any{
				map[string]any{"RowType": "Header", "Cells": []any{map[string]string{"Value": "Account"}, map[string]string{"Value": "Debit"}, map[string]string{"Value": "Credit"}, map[string]string{"Value": "YTD Debit"}, map[string]string{"Value": "YTD Credit"}}},
				map[string]any{"RowType": "Section", "Rows": []any{map[string]any{"RowType": "Row", "Cells": []any{map[string]any{"Value": "Business Bank Account (090)", "Attributes": []any{map[string]string{"Id": "account", "Value": "a-090"}}}, map[string]string{"Value": "10.00"}, map[string]string{"Value": ""}, map[string]string{"Value": "1250.00"}, map[string]string{"Value": ""}}}}},
			}}}})
		}
		if endpoint := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]; map[string]bool{"Invoices": true, "CreditNotes": true, "BankTransactions": true, "ManualJournals": true, "Payments": true, "Overpayments": true, "Prepayments": true}[endpoint] {
			records := []any{}
			if endpoint == "Invoices" && r.URL.Query().Get("Statuses") == "AUTHORISED" {
				records = append(records, map[string]any{"Type": "ACCPAY", "InvoiceID": "b-1", "InvoiceNumber": "B-1", "Status": "AUTHORISED", "Date": "2026-08-01T00:00:00", "DueDate": "2026-09-01T00:00:00", "Contact": map[string]string{"ContactID": "c-1", "Name": "Harbor Insurance"}, "CurrencyCode": "NZD", "AmountDue": 80})
			}
			if endpoint == "Invoices" && r.URL.Query().Get("Statuses") == "AUTHORISED,PAID" {
				records = append(records, map[string]any{"Type": "ACCPAY", "InvoiceID": "b-9", "InvoiceNumber": "B-9", "Status": "PAID", "Date": "2026-09-12T00:00:00", "Contact": map[string]string{"ContactID": "c-9", "Name": "Harbor Printing"}, "CurrencyCode": "NZD", "Total": 40})
			}
			return reply(200, map[string]any{endpoint: records, "pagination": map[string]int{"page": 1, "pageSize": 1000, "itemCount": len(records)}})
		}
		return reply(404, map[string]string{})
	})

	mux := http.NewServeMux()
	registerXero(mux, db)
	registerClientConnections(mux, db)
	h := organizationBoundary(db, mux)
	// A close's route; client names the client's page's route instead, which connects the organisation.
	call := func(subject, closeID string, body any, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(body)
		target := "/v1/closes/" + closeID + "/xero"
		if closeID == "client" {
			target = "/v1/closes/client/xero?name=" + url.QueryEscape("Xero client")
		}
		r := httptest.NewRequest("POST", target, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(r.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%v: %d %s", body, w.Code, w.Body.String())
		}
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	started := call("user_accountant", "client", map[string]any{"action": "connect", "confirmed": true}, 200)
	link, _ := url.Parse(started["url"].(string))
	state, id := link.Query().Get("state"), started["id"].(string)
	var month sql.NullString
	if err = db.QueryRow(`SELECT close_id::text FROM xero_connections WHERE id=$1`, id).Scan(&month); err != nil || month.Valid {
		t.Fatalf("a connection made from the client's page names no month: %v %v", month, err)
	}
	// The client's page reads no month's Trial Balance.
	call("user_accountant", "client", map[string]any{"action": "snapshot", "connection_id": id, "confirmed": true}, 422)
	if link.Query().Get("scope") != "offline_access accounting.settings.read accounting.reports.trialbalance.read accounting.manualjournals accounting.invoices accounting.payments.read accounting.banktransactions.read" {
		t.Fatalf("granular scopes: reads, transaction reads, manual journals and bill updates for approved writes: %s", link.Query().Get("scope"))
	}
	back := httptest.NewRecorder()
	xeroReturn(back, httptest.NewRequest("GET", "/v1/xero/callback?code=c1&state="+url.QueryEscape(state), nil), db)
	if back.Code != 303 || !strings.HasPrefix(back.Header().Get("Location"), "https://app.example.test/dashboard/clients/Xero%20client#") {
		t.Fatalf("the sign-in returns to the client's page: %d %s", back.Code, back.Header().Get("Location"))
	}
	// The sign-in is finished on the client's page, by whoever started it; a month cannot finish or start one.
	monthStatus := func() map[string]any {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+september+"/xero", nil)
		r = r.WithContext(auth.WithActor(r.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	call("user_accountant", september, map[string]any{"action": "callback", "connection_id": id, "code": "c1", "state": state, "confirmed": true}, 409)
	call("user_accountant", september, map[string]any{"action": "connect", "confirmed": true}, 409)
	call("user_other", "client", map[string]any{"action": "callback", "connection_id": id, "code": "c1", "state": state, "confirmed": true}, 409)
	call("user_accountant", "client", map[string]any{"action": "callback", "connection_id": id, "code": "c1", "state": state, "confirmed": true}, 200)
	status := monthStatus()
	connection := status["connection"].(map[string]any)
	tenants, _ := json.Marshal(connection["tenants"])
	if status["hosted"] != true || connection["state"] != "choose_tenant" || strings.Contains(string(tenants), "t-old") || strings.Contains(string(tenants), "t-practice") || !strings.Contains(string(tenants), "t-a") || !strings.Contains(string(tenants), "t-b") {
		t.Fatalf("only organisations from this sign-in are offered: %s", tenants)
	}
	call("user_accountant", "client", map[string]any{"action": "callback", "connection_id": id, "code": "c1", "state": state, "confirmed": true}, 409)
	call("user_accountant", "client", map[string]any{"action": "confirm", "connection_id": id, "tenant_id": "t-old", "confirmed": true}, 409)
	// A scheduled attempt an hour ago (as when a lapsed token stopped it) does not hold the next capture back
	// once the organisation is confirmed again.
	if err = db.QueryRow(`SELECT id::text FROM client_closes WHERE organization_id=$1 AND entity='Xero client' AND period=$2`, org, time.Now().UTC().Format("2006-01")).Scan(&due); err == sql.ErrNoRows {
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Xero client',$2) RETURNING id::text`, org, time.Now().UTC().Format("2006-01")).Scan(&due))
	}
	_, err = db.Exec(`UPDATE client_closes SET xero_refresh_at=now()-interval '1 hour' WHERE id::text=$1`, due)
	must(err)
	isDue := func() (ok bool) {
		t.Helper()
		must(db.QueryRow(`SELECT EXISTS(SELECT 1 FROM client_closes c WHERE c.id::text=$1 AND `+xeroDue+`)`, due).Scan(&ok))
		return ok
	}
	if isDue() {
		t.Fatal("nothing is due before the organisation is confirmed")
	}
	call("user_accountant", "client", map[string]any{"action": "confirm", "connection_id": id, "tenant_id": "t-b", "confirmed": true}, 200)
	status = monthStatus()
	if connection = status["connection"].(map[string]any); connection["state"] != "active" || connection["tenant_name"] != "Harbor Holdings Ltd" {
		t.Fatalf("the chosen organisation is connected: %v", connection)
	}
	if !isDue() {
		t.Fatal("confirming the organisation makes the client's open close due for a fresh capture")
	}
	_, err = db.Exec(`UPDATE client_closes SET xero_refresh_at=NULL WHERE id::text=$1`, due)
	must(err)
	status = call("user_accountant", september, map[string]any{"action": "snapshot", "connection_id": id, "confirmed": true}, 200)
	latest, _ := json.Marshal(status["latest"])
	if len(dates) != 1 || dates[0] != "2026-09-30" || tenantHeaders[0] != "t-b" || !strings.Contains(string(latest), `"currency":"NZD"`) || !strings.Contains(string(latest), `"ytd_debit":"1250.00"`) {
		t.Fatalf("the month-end Trial Balance of the chosen organisation is captured: %v %v %s", dates, tenantHeaders, latest)
	}
	if !strings.Contains(string(latest), "connect Xero again to read them") {
		t.Fatalf("a connection without the transaction scopes still captures the Trial Balance and says to reconnect: %s", latest)
	}
	if p, err := preparation.ClientProfile(context.Background(), db, september); err != nil || p != (preparation.Profile{Currency: "NZD", FiscalStart: 4, State: "detected"}) {
		t.Fatalf("a snapshot records the organisation's base currency and financial year: %+v %v", p, err)
	}
	var intact bool
	must(db.QueryRow(`SELECT bool_and(digest=encode(sha256(convert_to(payload::text,'UTF8')),'hex')) FROM xero_snapshots WHERE close_id=$1`, september).Scan(&intact))
	if !intact {
		t.Fatal("each snapshot digest must match its stored payload")
	}
	var requested []byte
	must(db.QueryRow(`SELECT payload FROM xero_snapshots WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, september).Scan(&requested))
	if at, ok := xero.RequestedAt(requested); !ok || at.After(time.Now()) {
		t.Fatalf("a snapshot records when its Trial Balance was requested: %s", requested)
	}
	// Two expiries in a row: the second refresh only works if the first rotated token was saved.
	for range 2 {
		_, err = db.Exec(`UPDATE xero_connections SET access_expires_at=now()-interval '1 minute' WHERE id=$1`, id)
		must(err)
		call("user_accountant", september, map[string]any{"action": "snapshot", "connection_id": id, "confirmed": true}, 200)
	}
	// Each capture with transactions keeps its supported ledger rows for search, and only the newest capture's.
	ledgerRows := func() (newest, older int) {
		t.Helper()
		must(db.QueryRow(`SELECT count(*) FILTER (WHERE t.snapshot_id=(SELECT max(id) FROM xero_snapshots WHERE close_id=$1)),count(*) FILTER (WHERE t.snapshot_id<>(SELECT max(id) FROM xero_snapshots WHERE close_id=$1)) FROM xero_transaction_rows t JOIN xero_snapshots s ON s.id=t.snapshot_id WHERE s.close_id=$1 AND t.record->'VendorRef'->>'name'='Harbor Printing' AND t.record->>'DocNumber'='B-9'`, september).Scan(&newest, &older))
		return
	}
	if newest, older := ledgerRows(); newest != 1 || older != 0 {
		t.Fatalf("the capture with transactions keeps its ledger rows: %d %d", newest, older)
	}
	call("user_accountant", september, map[string]any{"action": "snapshot", "connection_id": id, "confirmed": true}, 200)
	if newest, older := ledgerRows(); newest != 1 || older != 0 {
		t.Fatalf("a newer capture replaces the older capture's ledger rows: %d %d", newest, older)
	}
	var sealed []byte
	must(db.QueryRow(`SELECT credential FROM xero_connections WHERE id=$1`, id).Scan(&sealed))
	plain, err := secrets.Open(org+"/xero/"+id, sealed)
	must(err)
	var stored xero.Credentials
	json.Unmarshal(plain, &stored)
	if strings.Join(refreshes, ",") != "refresh-1,refresh-2" || stored.Refresh != "refresh-3" {
		t.Fatalf("each rotated refresh token is saved before use: %v %s", refreshes, stored.Refresh)
	}
	// Saving a snapshot waits on the close lock Daily books' Mark reviewed takes (through its foreign key).
	holder, err := db.Begin()
	must(err)
	defer holder.Rollback()
	var holderPID string
	must(holder.QueryRow(`SELECT pg_backend_pid()::text FROM client_closes WHERE id=$1 FOR UPDATE`, october).Scan(&holderPID))
	saved := make(chan int)
	go func() {
		raw, _ := json.Marshal(map[string]any{"action": "snapshot", "connection_id": id, "confirmed": true})
		r := httptest.NewRequest("POST", "/v1/closes/"+october+"/xero", bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(r.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "integrations:manage"}}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		saved <- w.Code
	}()
	for deadline, waiting := time.Now().Add(10*time.Second), 0; waiting == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the snapshot never waited on the held close lock")
		}
		must(db.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid))`, holderPID).Scan(&waiting))
	}
	must(holder.Commit())
	if code := <-saved; code != 200 {
		t.Fatalf("the snapshot is saved once the lock is released: %d", code)
	}
	// Each snapshot records the findings of its transactions when it has them.
	var unread int
	var septemberFindings, octoberFindings string
	must(db.QueryRow(`SELECT (SELECT count(*) FROM xero_snapshots WHERE close_id=$1 AND exceptions IS NULL),(SELECT exceptions::text FROM xero_snapshots WHERE close_id=$1 ORDER BY id DESC LIMIT 1),(SELECT exceptions::text FROM xero_snapshots WHERE close_id=$2 ORDER BY id DESC LIMIT 1)`, september, october).Scan(&unread, &septemberFindings, &octoberFindings))
	if unread != 2 || septemberFindings != "[]" || !strings.Contains(octoberFindings, "past_due_payable") || !strings.Contains(octoberFindings, "outside Xero") {
		t.Fatalf("snapshots without transactions record no findings; with them, the overdue bill is recorded in Xero's words: %d %s %s", unread, septemberFindings, octoberFindings)
	}

	// The worker captures an open current-month close once a day under its own name, one attempt per close a day.
	scheduled := func() (n int) {
		t.Helper()
		must(db.QueryRow(`SELECT count(*) FROM xero_snapshots WHERE close_id=$1 AND actor='scheduled-xero-refresh'`, due).Scan(&n))
		return n
	}
	if due == september {
		err = sweepXeroRefresh(t.Context(), db)
		must(err)
		if scheduled() != 0 {
			t.Fatal("a close captured by hand in the past day is not captured again")
		}
	}
	_, err = db.Exec(`UPDATE xero_snapshots SET captured_at=now()-interval '25 hours' WHERE close_id=$1`, due)
	must(err)
	_, err = db.Exec(`UPDATE organizations SET enabled=false WHERE id=$1`, org)
	must(err)
	err = sweepXeroRefresh(t.Context(), db)
	must(err)
	_, err = db.Exec(`UPDATE organizations SET enabled=true WHERE id=$1`, org)
	must(err)
	if scheduled() != 0 {
		t.Fatal("a disabled firm's closes are not captured or claimed")
	}
	for range 2 {
		err = sweepXeroRefresh(t.Context(), db)
		must(err)
	}
	if scheduled() != 1 {
		t.Fatalf("one scheduled capture for the due close: %d", scheduled())
	}
	_, err = db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1 AND actor='scheduled-xero-refresh'`, due)
	must(err)
	err = sweepXeroRefresh(t.Context(), db)
	must(err)
	if scheduled() != 0 {
		t.Fatal("a close already attempted today is not attempted again, whatever the outcome")
	}
	blockers := func() string {
		t.Helper()
		tx, err := db.Begin()
		must(err)
		defer tx.Rollback()
		retention, err := readRetention(t.Context(), tx, september, org)
		must(err)
		return strings.Join(retention.Blockers, " | ")
	}
	// A connection made on the client's page belongs to no month, so deleting a month never waits on it.
	if b := blockers(); strings.Contains(b, "Xero connection") {
		t.Fatalf("a client's connection holds no month back: %s", b)
	}
	// One opened from a month before connections moved to the client's page still holds that month back.
	_, err = db.Exec(`UPDATE xero_connections SET close_id=$2 WHERE id=$1`, id, september)
	must(err)
	if b := blockers(); !strings.Contains(b, "Disconnect the Xero connection opened from this close.") || !strings.Contains(b, "A Xero connection opened here is used by another close.") {
		t.Fatalf("deleting the opening close must wait for disconnection and the other close: %s", b)
	}
	status = call("user_accountant", september, map[string]any{"action": "disconnect", "connection_id": id, "confirmed": true}, 200)
	var cleared bool
	must(db.QueryRow(`SELECT credential IS NULL AND state='disconnected' FROM xero_connections WHERE id=$1`, id).Scan(&cleared))
	if !cleared || status["connection"] != nil {
		t.Fatal("disconnecting clears the stored credential")
	}
}
