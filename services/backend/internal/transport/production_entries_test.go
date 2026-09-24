package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/loans"
	"close.local/backend/internal/owners"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/reconciliation"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestApprovedLoanAndOwnerEntriesPostUnderTheirGrant(t *testing.T) {
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
	org := "org_Entries" + rand.Text()
	var closeID, connectionID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Entries fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Entries fixture','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM production_postings WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM production_posting_grants WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, closeID)
		db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	saved := func(kind string, paper any) string {
		t.Helper()
		raw, _ := json.Marshal(paper)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) RETURNING id::text`, closeID, kind, kind+"-digest", string(raw)).Scan(&id))
		_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Reviewed synthetic entries','user_reviewer')`, id, kind+"-digest")
		must(e)
		return id
	}
	loan, err := loans.Prepare(loans.Input{Entity: "Entries fixture", ClosePeriod: "2026-09", Lender: "First Bank", SourceReference: "Loan statement Sep 2026", AgreementReference: "Note §2", Currency: "USD",
		AnnualRate: "6.000", DayCount: "actual_360", OpeningPrincipal: "120000.00", InterestPaidThrough: "2026-08-15",
		Payments:         []loans.Payment{{Date: "2026-09-15", Principal: "1500.00", Interest: "620.00"}, {Date: "2026-09-29", Principal: "500.00", Interest: "276.50"}},
		ClosingPrincipal: "118000.00", BookBalance: "100000.00", LoanAccount: "Loan payable", InterestAccount: "Interest expense",
		AccrueInterest: true, AccruedInterestAccount: "Accrued interest", BookedAccruedInterest: "0.00",
		ReclassCurrentPortion: true, PrincipalDueNext12Months: "20000.00", ScheduleReference: "Amortization schedule", CurrentPortionAccount: "Current portion of loan", BookedCurrentPortion: "18000.00",
		Method: loans.Method, MethodConfirmed: true, RateConfirmed: true, BookingConfirmed: true})
	must(err)
	if len(loan.Proposals) != 2 {
		t.Fatalf("fixture must propose two entries: %+v", loan.Proposals)
	}
	loanID := saved("loan", loan)
	owner, err := owners.Prepare(owners.Input{Entity: "Entries fixture", ClosePeriod: "2026-09", EntityType: "s_corp", TransactionReference: "Purchase 1072", TransactionDate: "2026-09-18", Payee: "Bistro", Amount: "86.40", LedgerReference: "GL p3",
		Direction: "personal_paid_by_business", PurposeStatus: "answered", StatedPurpose: "Family dinner", ClientConfirmationReference: "Client reply", Treatment: "distribution", BookedAccount: "Meals", OwnerAccount: "Shareholder distributions", Method: owners.Method, TreatmentConfirmed: true})
	must(err)
	ownerID := saved("owner", owner)
	var report string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'Stripe payouts Sep 2026','stripe-digest','Synthetic Stripe report','fixture') RETURNING id::text`, closeID).Scan(&report))
	feeID := saved("settlement", struct {
		reconciliation.SettlementWorkpaper
		Sources []preparation.Source `json:"sources"`
	}{reconciliation.SettlementWorkpaper{Input: reconciliation.SettlementInput{Entity: "Entries fixture", ClosePeriod: "2026-09", Currency: "USD", ReportReference: "Stripe payouts Sep 2026 · rows 1-13"},
		Proposal: &prepaids.Adjustment{Date: "2026-09-30", DebitAccount: "Stripe fees", CreditAccount: "Sales", Amount: "29.22"}}, []preparation.Source{{ID: report, Digest: "stripe-digest", Name: "Stripe payouts Sep 2026", Role: "processor_report", Location: "rows 1-13"}}})

	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	credentials, _ := json.Marshal(quickbooks.Credentials{Access: "synthetic-production-access", Refresh: "synthetic-refresh", Expires: 3600, Type: "bearer"})
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment,access_expires_at) VALUES($1,$2,'Entries fixture','fixture','active',$3,now()+interval '1 day','123','Entries fixture','production',now()+interval '1 hour') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connectionID))
	sealed, err := secrets.Seal(org+"/qbo/"+connectionID, credentials)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_connections SET credential=$2 WHERE id=$1`, connectionID, sealed)
	must(err)

	// Liabilities carry positive credit balances; the expense a positive debit balance.
	accounts := map[string][3]string{"30": {"Interest expense", "Expense", "100.00"}, "31": {"Accrued interest", "Liability", "0.00"}, "32": {"Loan payable", "Liability", "100000.00"}, "33": {"Current portion of loan", "Liability", "18000.00"}, "40": {"Meals", "Expense", "500.00"}, "42": {"Shareholder distributions", "Equity", "0.00"}, "43": {"Shareholder distributions", "Equity", "0.00"}, "50": {"Stripe fees", "Expense", "10.00"}, "51": {"Sales", "Revenue", "5000.00"}}
	after := map[string]string{"30": "119.67", "31": "19.67", "32": "98000.00", "33": "20000.00"}
	writes := 0
	var posted quickbooks.Journal
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		var result any
		switch {
		case strings.Contains(r.URL.Path, "/companyinfo/"):
			result = map[string]any{"CompanyInfo": map[string]string{"CompanyName": "Entries fixture"}}
		case strings.HasSuffix(r.URL.Path, "/preferences"):
			result = map[string]any{"Preferences": map[string]any{"AccountingInfoPrefs": map[string]string{"BookCloseDate": ""}}}
		case strings.Contains(r.URL.Path, "/account/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			a := accounts[id]
			balance := a[2]
			if writes > 0 && after[id] != "" {
				balance = after[id]
			}
			if writes > 1 && id >= "50" {
				balance = map[string]string{"50": "39.22", "51": "5029.22"}[id]
			}
			result = map[string]any{"Account": map[string]any{"Id": id, "Name": a[0], "Active": true, "Classification": a[1], "SyncToken": "1", "CurrentBalance": json.RawMessage(balance)}}
		case r.Method == "POST":
			writes++
			json.NewDecoder(r.Body).Decode(&posted)
			posted.ID = "901"
			result = map[string]any{"JournalEntry": posted}
		default:
			result = map[string]any{"JournalEntry": posted}
		}
		body, _ := json.Marshal(result)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: make(http.Header)}, nil
	})

	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "posting:approve", "posting:execute"}}
	mux := http.NewServeMux()
	registerProductionPosting(mux, db)
	call := func(path string, input any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", path, strings.NewReader(string(body))).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: HTTP %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	grantPath := "/v1/closes/" + closeID + "/production-grant"
	grant := func(limit string, kinds []string) string {
		var out struct{ ID string }
		json.Unmarshal(call(grantPath, map[string]any{"action": "grant", "connection_id": connectionID, "account_ids": []string{"30", "31", "32", "33", "40", "42", "43", "50", "51"}, "kinds": kinds, "max_amount": limit, "expires_at": time.Now().Add(time.Hour), "reason": "Reviewed synthetic loan and owner posting", "confirmation": "GRANT PRODUCTION POSTING"}, 200), &out)
		return out.ID
	}
	loanPath := "/v1/closes/" + closeID + "/workpapers/" + loanID + "/production-posting"
	first := grant("2000.00", []string{"loan"})
	if body := call(loanPath, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "not enabled") {
		t.Fatalf("new kinds stay off until the sandbox check enables them: %s", body)
	}
	t.Setenv("PRODUCTION_POSTING_KINDS", "prepaid,loan,owner")
	ownerPath := "/v1/closes/" + closeID + "/workpapers/" + ownerID + "/production-posting"
	if body := call(ownerPath, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "does not cover owner") {
		t.Fatalf("a grant covers only the kinds it names: %s", body)
	}
	if body := call(loanPath, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "grant limit") {
		t.Fatalf("the grant limit applies to the journal's total debits (2,019.67): %s", body)
	}
	call(grantPath, map[string]any{"action": "revoke", "grant_id": first, "confirmation": "REVOKE PRODUCTION GRANT"}, 200)
	second := grant("3000.00", []string{"loan", "owner"})
	var preview productionPosting
	json.Unmarshal(call(loanPath, map[string]string{"action": "preview"}, 200), &preview)
	var journal quickbooks.Journal
	json.Unmarshal(preview.Journal, &journal)
	if len(journal.Line) != 4 || preview.Projected["30"] != "119.67" || preview.Projected["31"] != "19.67" || preview.Projected["32"] != "98000.00" || preview.Projected["33"] != "20000.00" {
		t.Fatalf("both loan entries post as one journal with per-account effects: %+v %v", journal.Line, preview.Projected)
	}
	call(loanPath, map[string]string{"action": "approve", "digest": preview.Digest, "confirmation": "APPROVE PRODUCTION DRAFT"}, 200)
	var result productionPosting
	json.Unmarshal(call(loanPath, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 200), &result)
	if writes != 1 || result.State != "verified" || result.ExternalID != "901" {
		t.Fatalf("the loan journal must post once and verify against liability-aware balances: %+v writes=%d", result, writes)
	}
	if body := call(ownerPath, map[string]string{"action": "preview"}, 422); !strings.Contains(string(body), "exactly one account") {
		t.Fatalf("a name matching two granted accounts must be refused before any write: %s", body)
	}
	if writes != 1 {
		t.Fatal("no write may follow a refused preview")
	}
	feePath := "/v1/closes/" + closeID + "/workpapers/" + feeID + "/production-posting"
	call(grantPath, map[string]any{"action": "revoke", "grant_id": second, "confirmation": "REVOKE PRODUCTION GRANT"}, 200)
	grant("3000.00", []string{"settlement"})
	if body := call(feePath, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "not enabled") {
		t.Fatalf("Stripe fee entries stay off until enabled: %s", body)
	}
	t.Setenv("PRODUCTION_POSTING_KINDS", "prepaid,loan,owner,settlement")
	var newer string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) SELECT close_id,kind,'newer',jsonb_set(paper,'{input,report_reference}','"Stripe payouts Sep 2026 · page 1"') FROM workpapers WHERE id=$1 RETURNING id::text`, feeID).Scan(&newer))
	if body := call(feePath, map[string]string{"action": "preview"}, 409); !strings.Contains(string(body), "newer source draft") {
		t.Fatalf("a newer draft of the same Stripe report file supersedes this one, whatever its cited location: %s", body)
	}
	_, err = db.Exec(`DELETE FROM workpapers WHERE id=$1`, newer)
	must(err)
	json.Unmarshal(call(feePath, map[string]string{"action": "preview"}, 200), &preview)
	if preview.Projected["50"] != "39.22" || preview.Projected["51"] != "5029.22" {
		t.Fatalf("the fee entry grosses up revenue by the payout fees: %v", preview.Projected)
	}
	call(feePath, map[string]string{"action": "approve", "digest": preview.Digest, "confirmation": "APPROVE PRODUCTION DRAFT"}, 200)
	json.Unmarshal(call(feePath, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO PRODUCTION"}, 200), &result)
	if writes != 2 || result.State != "verified" {
		t.Fatalf("the approved Stripe fee entry posts once and verifies: %+v writes=%d", result, writes)
	}
}
