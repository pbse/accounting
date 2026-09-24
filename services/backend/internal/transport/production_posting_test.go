package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestProductionLedgerEffect(t *testing.T) {
	before := quickbooks.LedgerObservation{Company: "Client", Prepaid: quickbooks.LedgerAccount{ID: "10", Balance: "10.00"}, Expense: quickbooks.LedgerAccount{ID: "20", Balance: "5"}}
	after := quickbooks.LedgerObservation{Company: "Client", Prepaid: quickbooks.LedgerAccount{ID: "10", Balance: "1010"}, Expense: quickbooks.LedgerAccount{ID: "20", Balance: "-995.00"}}
	if !ledgerAfterMatches(before, after, "1000.00") {
		t.Fatal("exact ledger delta rejected")
	}
	projected, err := projectedBalances(before, "1000.00")
	if err != nil || projected["prepaid"] != "1010.00" || projected["expense"] != "-995.00" {
		t.Fatalf("wrong proposed balances: %v %v", projected, err)
	}
	after.Expense.Balance = "-994.99"
	if ledgerAfterMatches(before, after, "1000.00") {
		t.Fatal("incorrect ledger delta accepted")
	}
	raw, _ := json.Marshal(productionPosting{posting: posting{State: "verified"}})
	if !strings.Contains(string(raw), `"state":"verified"`) {
		t.Fatal("posting state missing from API response")
	}
}

func TestCloseRehearsalCombinesOnlyCompatibleSnapshots(t *testing.T) {
	before := quickbooks.LedgerObservation{Company: "Client", Prepaid: quickbooks.LedgerAccount{ID: "10", Name: "Prepaid", Balance: "100.00", SyncToken: "3"}, Expense: quickbooks.LedgerAccount{ID: "20", Name: "Expense", Balance: "50.00", SyncToken: "7"}}
	entry := func(id, amount string, observed quickbooks.LedgerObservation) rehearsalInput {
		journal, _ := json.Marshal(quickbooks.Journal{CurrencyRef: quickbooks.Reference{Value: "USD"}, Line: []quickbooks.Line{
			{Amount: json.Number(amount), DetailType: "JournalEntryLineDetail", JournalEntryLineDetail: quickbooks.Detail{PostingType: "Debit", AccountRef: quickbooks.Reference{Value: "10"}}},
			{Amount: json.Number(amount), DetailType: "JournalEntryLineDetail", JournalEntryLineDetail: quickbooks.Detail{PostingType: "Credit", AccountRef: quickbooks.Reference{Value: "20"}}},
		}})
		return rehearsalInput{PaperID: id, ReviewState: "approved", Posting: productionPosting{posting: posting{ID: id, State: "preview", Journal: journal, Realm: "123"}, ConnectionID: "42", LedgerBefore: observed}}
	}
	first, second := entry("1", "10.00", before), entry("2", "5.00", before)
	got := rehearseProduction([]rehearsalInput{first, second}, false)
	if len(got.Blockers) != 0 || len(got.Accounts) != 2 || got.Accounts[0].Projected != "115.00" || got.Accounts[1].Projected != "35.00" {
		t.Fatalf("wrong combined close: %+v", got)
	}
	first.Posting.Conflicts = []string{"2"}
	if got = rehearseProduction([]rehearsalInput{first, second}, false); len(got.Blockers) != 0 {
		t.Fatalf("included same-close proposal should be combined: %+v", got)
	}
	first.Posting.Conflicts = nil
	changed := before
	changed.Prepaid.SyncToken = "4"
	got = rehearseProduction([]rehearsalInput{first, entry("2", "5.00", changed)}, false)
	if len(got.Blockers) == 0 || len(got.Accounts) != 0 {
		t.Fatalf("mixed account versions should block combined state: %+v", got)
	}
	first.Posting.Conflicts = []string{"999"}
	got = rehearseProduction([]rehearsalInput{first, second}, false)
	if len(got.Blockers) == 0 || len(got.Accounts) != 0 {
		t.Fatalf("competing posting outside this close should block combined state: %+v", got)
	}
}

func TestProductionPostingSingleDurableAttempt(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	org := "org_Production" + rand.Text()
	var closeID, paperID, connectionID string
	if _, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Production fixture')`, org); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Production fixture','2026-09') RETURNING id::text`, org).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, q := range []string{`DELETE FROM production_postings WHERE close_id=$1`, `DELETE FROM production_posting_grants WHERE organization_id=$1`, `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`, `DELETE FROM client_closes WHERE id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			arg := closeID
			if strings.Contains(q, "organization_id") || strings.Contains(q, "organizations WHERE") {
				arg = org
			}
			if _, e := db.Exec(q, arg); e != nil {
				t.Error(e)
			}
		}
	}()
	paper, err := prepaids.Prepare(prepaids.Input{Entity: "Production fixture", SourceReference: "Synthetic annual invoice", Amount: "12000", ServiceStart: "2026-09-01", ServiceEnd: "2027-08-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software", PrepaidAccount: "Prepaid", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(paper)
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','synthetic-digest',$2) RETURNING id::text`, closeID, string(raw)).Scan(&paperID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	credentials, _ := json.Marshal(quickbooks.Credentials{Access: "synthetic-production-access", Refresh: "synthetic-refresh", Expires: 3600, Type: "bearer"})
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment,access_expires_at) VALUES($1,$2,'Production fixture','fixture','active',$3,now()+interval '1 day','123','Production fixture','production',now()+interval '1 hour') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connectionID); err != nil {
		t.Fatal(err)
	}
	encrypted, err := secrets.Seal(org+"/qbo/"+connectionID, credentials)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE quickbooks_connections SET credential=$2 WHERE id=$1`, connectionID, encrypted); err != nil {
		t.Fatal(err)
	}
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "posting:approve", "posting:execute"}}
	mux := http.NewServeMux()
	registerProductionPosting(mux, db)
	call := func(path string, input any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(auth.WithActor(context.Background(), actor))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: HTTP %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	grantPath := "/v1/closes/" + closeID + "/production-grant"
	call(grantPath, map[string]any{"action": "grant", "connection_id": connectionID, "prepaid_account_id": "10", "expense_account_id": "20", "max_amount": "20000", "expires_at": time.Now().Add(time.Hour), "reason": "Reviewed synthetic client authority", "confirmation": "GRANT PRODUCTION POSTING"}, 200)
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	writes := 0
	amount := ""
	accountVersion := "1"
	laterBalance := false
	failReadback := false
	var journal quickbooks.Journal
	uncertain := false
	closedThrough := ""
	closeOnWrite := false
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "quickbooks.api.intuit.com" {
			t.Fatalf("wrong production host: %s", r.URL.Host)
		}
		var result any
		switch {
		case strings.Contains(r.URL.Path, "/companyinfo/"):
			result = map[string]any{"CompanyInfo": map[string]string{"CompanyName": "Production fixture"}}
		case strings.HasSuffix(r.URL.Path, "/preferences"):
			through := closedThrough
			if closeOnWrite && writes > 0 {
				through = "2099-12-31"
			}
			result = map[string]any{"Preferences": map[string]any{"AccountingInfoPrefs": map[string]string{"BookCloseDate": through}}}
		case strings.HasSuffix(r.URL.Path, "/account/10"), strings.HasSuffix(r.URL.Path, "/account/20"):
			prepaid := strings.HasSuffix(r.URL.Path, "/account/10")
			id, name, class, balance := "20", "Software", "Expense", "0"
			if prepaid {
				id, name, class = "10", "Prepaid", "Asset"
			}
			if writes > 0 {
				if prepaid {
					balance = amount
				} else {
					balance = "-" + amount
				}
			}
			if writes == 2 {
				cents, e := prepaids.Amount(amount)
				if e != nil {
					t.Fatal(e)
				}
				twice := fmt.Sprintf("%d.%02d", cents*2/100, cents*2%100)
				if prepaid {
					balance = twice
				} else {
					balance = "-" + twice
				}
			}
			if laterBalance {
				balance = "9999"
			}
			result = map[string]any{"Account": map[string]any{"Id": id, "Name": name, "Active": true, "Classification": class, "SyncToken": accountVersion, "CurrentBalance": json.RawMessage(balance)}}
		case r.Method == "POST":
			writes++
			var state string
			if e := db.QueryRow(`SELECT state FROM production_postings WHERE workpaper_id=$1`, paperID).Scan(&state); e != nil || state != "executing" {
				t.Fatalf("provider write before durable reservation: %s %v", state, e)
			}
			if !strings.Contains(r.URL.RawQuery, "requestid=") {
				t.Fatal("missing stable request ID")
			}
			if uncertain {
				json.NewDecoder(r.Body).Decode(&journal)
				amount = string(journal.Line[0].Amount)
				journal.ID = "789"
				return nil, errors.New("synthetic response lost")
			}
			json.NewDecoder(r.Body).Decode(&journal)
			amount = string(journal.Line[0].Amount)
			journal.ID = "456"
			result = map[string]any{"JournalEntry": journal}
		default:
			if failReadback {
				return nil, errors.New("synthetic temporary readback failure")
			}
			result = map[string]any{"JournalEntry": journal}
		}
		body, _ := json.Marshal(result)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})
	path := "/v1/closes/" + closeID + "/workpapers/" + paperID + "/production-posting"
	var preview productionPosting
	json.Unmarshal(call(path, map[string]string{"action": "preview"}, 200), &preview)
	call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 409)
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'synthetic-digest','approved','Reviewed exact synthetic proposal','user_accountant')`, paperID); err != nil {
		t.Fatal(err)
	}
	accountVersion = "2"
	call(path, map[string]string{"action": "approve", "digest": preview.Digest, "confirmation": "APPROVE PRODUCTION DRAFT"}, 409)
	json.Unmarshal(call(path, map[string]string{"action": "preview"}, 200), &preview)
	rehearsalRequest := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/proposed-state", nil).WithContext(auth.WithActor(context.Background(), actor))
	rehearsalResponse := httptest.NewRecorder()
	mux.ServeHTTP(rehearsalResponse, rehearsalRequest)
	var rehearsal struct {
		Rehearsal closeRehearsal `json:"rehearsal"`
	}
	if rehearsalResponse.Code != 200 || json.Unmarshal(rehearsalResponse.Body.Bytes(), &rehearsal) != nil || rehearsal.Rehearsal.IntentCount != 1 || len(rehearsal.Rehearsal.Accounts) != 2 {
		t.Fatalf("saved close rehearsal unavailable: %d %s", rehearsalResponse.Code, rehearsalResponse.Body.String())
	}
	call(path, map[string]string{"action": "approve", "digest": preview.Digest, "confirmation": "APPROVE PRODUCTION DRAFT"}, 200)
	actor.Permissions = []string{"clients:all", "posting:execute"}
	closedThrough = "2099-12-31"
	call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 422)
	closedThrough = "2000-01-31"
	call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 409)
	if writes != 0 {
		t.Fatalf("a closed or changed closing date must stop the write: %d", writes)
	}
	closedThrough = ""
	closeOnWrite = true
	var posted productionPosting
	json.Unmarshal(call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 200), &posted)
	if writes != 1 || posted.State != "verified" || posted.ExternalID != "456" || len(posted.LedgerAfter) == 0 {
		t.Fatalf("expected one verified durable attempt: %+v writes=%d", posted, writes)
	}
	closeOnWrite = false
	call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 409)
	failReadback = true
	call(path, map[string]string{"action": "verify"}, 422)
	failReadback = false
	laterBalance = true
	json.Unmarshal(call(path, map[string]string{"action": "verify"}, 200), &posted)
	if posted.State != "verified" {
		t.Fatal("verified receipt was not restored after a temporary readback failure")
	}
	laterBalance = false
	actor.Permissions = []string{"clients:all", "posting:approve"}
	paper.Input.SourceReference = "Synthetic second invoice"
	raw, _ = json.Marshal(paper)
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','synthetic-second',$2) RETURNING id::text`, closeID, string(raw)).Scan(&paperID); err != nil {
		t.Fatal(err)
	}
	path = "/v1/closes/" + closeID + "/workpapers/" + paperID + "/production-posting"
	json.Unmarshal(call(path, map[string]string{"action": "preview"}, 200), &preview)
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'synthetic-second','approved','Reviewed second synthetic proposal','user_accountant')`, paperID); err != nil {
		t.Fatal(err)
	}
	call(path, map[string]string{"action": "approve", "digest": preview.Digest, "confirmation": "APPROVE PRODUCTION DRAFT"}, 200)
	actor.Permissions = []string{"clients:all", "posting:execute"}
	uncertain = true
	if body := call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 502); !strings.Contains(string(body), `"code":"EXECUTION_UNKNOWN"`) {
		t.Fatalf("an uncertain write must say its outcome is unknown: %s", body)
	}
	call(path, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 409)
	var state string
	if err = db.QueryRow(`SELECT state FROM production_postings WHERE workpaper_id=$1`, paperID).Scan(&state); err != nil || state != "unknown" || writes != 2 {
		t.Fatalf("uncertain attempt was not preserved once: state=%s writes=%d err=%v", state, writes, err)
	}
	call(path, map[string]string{"action": "verify", "external_id": "999"}, 422)
	json.Unmarshal(call(path, map[string]string{"action": "verify", "external_id": "789"}, 200), &posted)
	if posted.State != "verified" || posted.ExternalID != "789" || writes != 2 {
		t.Fatalf("manual readback did not resolve saved attempt: %+v writes=%d", posted, writes)
	}
}
