package transport

import (
	"archive/zip"
	"bytes"
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
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/reconciliation"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

// TestGovernedFinishPostsToTheSandboxAndSignsOffOnlyTheDeclaredScope is the governed-finish proof of docs/maturity.md
// (step 7): a reviewed prepaid draft is rehearsed against the production ledger without writing to it, approved and
// posted once to the QuickBooks sandbox, where the readback matches; a bank reconciliation missing a statement page is
// refused approval and blocks sign-off while it is in scope; the firm signs off a scope that leaves it out, and the
// binder carries the sign-off, the sandbox receipt, and the reconciliation with its blocker, marked as not covered.
func TestGovernedFinishPostsToTheSandboxAndSignsOffOnlyTheDeclaredScope(t *testing.T) {
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
	org, entity := "org_Finish"+rand.Text(), "Finish fixture "+rand.Text()
	for name, value := range map[string]string{"WORKOS_ORGANIZATION_ID": org, "QBO_SANDBOX_REALM_ID": "123", "QBO_SANDBOX_ACCESS_TOKEN": "synthetic-only", "QBO_SANDBOX_ENTITY": entity, "QBO_SANDBOX_PREPAID_ACCOUNT_ID": "10", "QBO_SANDBOX_EXPENSE_ACCOUNT_ID": "20", "SECRETS_KEY": strings.Repeat("ab", 32)} {
		t.Setenv(name, value)
	}
	for _, q := range []string{`INSERT INTO organizations(id,name) VALUES($1,'Governed finish fixture')`,
		`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic','fixture')`} {
		_, err = db.Exec(q, org)
		must(err)
	}
	var closeID string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,'2026-09') RETURNING id::text`, org, entity).Scan(&closeID))
	// Signing off carries the reviewed schedule into October, so the fixture is removed by firm.
	defer func() {
		closes := `close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`
		for _, q := range []string{`DELETE FROM close_signoffs WHERE ` + closes, `DELETE FROM close_scopes WHERE ` + closes,
			`DELETE FROM sandbox_postings WHERE ` + closes, `DELETE FROM production_postings WHERE ` + closes, `DELETE FROM preparation_jobs WHERE ` + closes,
			`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE ` + closes + `)`,
			`DELETE FROM workpapers WHERE ` + closes, `DELETE FROM evidence WHERE ` + closes,
			`DELETE FROM production_posting_grants WHERE organization_id=$1`, `DELETE FROM quickbooks_connections WHERE organization_id=$1`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`,
			`DELETE FROM workflow_events WHERE organization_id=$1`, `DELETE FROM workflow_runs WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			if _, e := db.Exec(q, org); e != nil {
				t.Error(e)
			}
		}
	}()
	store := savedCloses(db, strings.Repeat("f", 32))
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "close:signoff", "posting:approve", "posting:execute"}}
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	base := "/v1/closes/" + closeID
	// draft calculates a workpaper from its source through the preparation engine and saves it as a draft.
	draft := func(kind, name, role string, input any) (string, string) {
		t.Helper()
		var source, digest string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,'Synthetic source','fixture') RETURNING id::text,digest`, closeID, name, rand.Text()).Scan(&source, &digest))
		raw, _ := json.Marshal(input)
		body, err := preparation.Calculate(preparation.Plan{Kind: kind, Input: raw, Sources: []preparation.Source{{ID: source, Digest: digest, Name: name, Role: role, Location: "page 1"}}}, entity, "2026-09")
		must(err)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,$2,$3,$4) RETURNING id::text`, closeID, kind, preparation.Digest(body), string(body)).Scan(&id))
		return id, preparation.Digest(body)
	}
	prepaid, prepaidDigest := draft("prepaid", "software-invoice.txt", "invoice", prepaids.Input{Entity: entity, SourceReference: "Ledger software annual invoice", Amount: "1200.00", ServiceStart: "2026-09-01", ServiceEnd: "2027-08-31", ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software", PrepaidAccount: "Prepaid", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true})
	// The deliberate refusal: the statement's second page never arrived, so the reconciliation carries a blocker.
	bank, bankDigest := draft("reconciliation", "september-statement.txt", "statement", reconciliation.Input{Entity: entity, Account: "Operating bank", ClosePeriod: "2026-09", Currency: "USD", StatementReference: "September statement", LedgerReference: "Operating bank ledger", ExpectedPages: 2, Pages: []int{1}, StatementOpening: "1000", StatementClosing: "1150", LedgerOpening: "1000", LedgerClosing: "1150", StatementCSV: "id,date,amount\nS1,2026-09-02,200\nS2,2026-09-05,-50", LedgerCSV: "id,date,amount\nL1,2026-09-02,200\nL2,2026-09-05,-50", PolicyConfirmed: true})

	call("POST", base+"/workpapers/"+prepaid+"/reviews", map[string]any{"decision": "approved", "note": "Reviewed the invoice and the schedule", "digest": prepaidDigest}, 200)
	if refused := call("POST", base+"/workpapers/"+bank+"/reviews", map[string]any{"decision": "approved", "note": "Looks fine", "digest": bankDigest}, 409); !strings.Contains(string(refused), "no unresolved calculation blockers") {
		t.Fatalf("a reconciliation missing a page is not approved: %s", refused)
	}

	// One fake QuickBooks answers both hosts: the production company is only read, the sandbox takes the one write.
	var productionConnection string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment,access_expires_at,home_currency,fiscal_year_start) VALUES($1,$2,$3,'fixture','active',$4,now()+interval '1 day','987',$3,'production',now()+interval '1 hour','USD',1) RETURNING id::text`, closeID, org, entity, rand.Text()).Scan(&productionConnection))
	credentials, _ := json.Marshal(quickbooks.Credentials{Access: "synthetic-production-access", Refresh: "synthetic-refresh", Expires: 3600, Type: "bearer"})
	sealed, err := secrets.Seal(org+"/qbo/"+productionConnection, credentials)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_connections SET credential=$2 WHERE id::text=$1`, productionConnection, sealed)
	must(err)
	original := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = original })
	productionWrites, sandboxWrites, readbacks := 0, 0, 0
	var journal map[string]any
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		production := r.URL.Host == "quickbooks.api.intuit.com"
		if !production && r.URL.Host != "sandbox-quickbooks.api.intuit.com" {
			t.Fatalf("unexpected provider: %s", r.URL.Host)
		}
		var result any
		switch {
		case strings.Contains(r.URL.Path, "/companyinfo/"):
			result = map[string]any{"CompanyInfo": map[string]string{"CompanyName": entity}}
		case strings.HasSuffix(r.URL.Path, "/preferences"):
			result = map[string]any{"Preferences": map[string]any{"AccountingInfoPrefs": map[string]string{}}}
		case strings.HasSuffix(r.URL.Path, "/account/10"):
			result = map[string]any{"Account": map[string]any{"Id": "10", "Name": "Prepaid", "Active": true, "Classification": "Asset", "CurrencyRef": map[string]string{"value": "USD"}, "SyncToken": "1", "CurrentBalance": json.RawMessage("1200.00")}}
		case strings.HasSuffix(r.URL.Path, "/account/20"):
			result = map[string]any{"Account": map[string]any{"Id": "20", "Name": "Software", "Active": true, "Classification": "Expense", "CurrencyRef": map[string]string{"value": "USD"}, "SyncToken": "1", "CurrentBalance": json.RawMessage("0")}}
		case r.Method == "POST" && production:
			productionWrites++
			t.Fatal("the rehearsal wrote to the production ledger")
		case r.Method == "POST":
			sandboxWrites++
			var state string
			if err := db.QueryRow(`SELECT state FROM sandbox_postings WHERE workpaper_id::text=$1`, prepaid).Scan(&state); err != nil || state != "executing" {
				t.Fatalf("the sandbox write preceded its durable attempt: %s %v", state, err)
			}
			json.NewDecoder(r.Body).Decode(&journal)
			journal["Id"] = "456"
			result = map[string]any{"JournalEntry": journal}
		default:
			readbacks++
			result = map[string]any{"JournalEntry": journal}
		}
		body, _ := json.Marshal(result)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})

	// Rehearsal: under the client's grant, the approved draft's entry is previewed against the production company's
	// versioned balances and combined into the close's proposed state. Nothing is written.
	call("POST", base+"/production-grant", map[string]any{"action": "grant", "connection_id": productionConnection, "account_ids": []string{"10", "20"}, "kinds": []string{"prepaid"}, "max_amount": "5000", "expires_at": time.Now().Add(time.Hour), "reason": "Synthetic client authority for the rehearsal", "confirmation": "GRANT PRODUCTION POSTING"}, 200)
	call("POST", base+"/workpapers/"+prepaid+"/production-posting", map[string]string{"action": "preview"}, 200)
	var proposed struct {
		Rehearsal closeRehearsal `json:"rehearsal"`
	}
	must(json.Unmarshal(call("GET", base+"/proposed-state", nil, 200), &proposed))
	rehearsal := proposed.Rehearsal
	if rehearsal.IntentCount != 1 || len(rehearsal.Blockers) != 0 || len(rehearsal.ReviewRequired) != 0 || len(rehearsal.Accounts) != 2 || productionWrites != 0 {
		t.Fatalf("the rehearsal combines the approved entry without writing: %+v writes=%d", rehearsal, productionWrites)
	}
	for _, account := range rehearsal.Accounts {
		if account.Delta == "0.00" || account.Projected == account.Balance {
			t.Fatalf("each account shows its projected change: %+v", account)
		}
	}

	// A person approves the exact sandbox journal, then posts it once; the readback matches it.
	sandbox := base + "/workpapers/" + prepaid + "/posting"
	var preview posting
	must(json.Unmarshal(call("POST", sandbox, map[string]string{"action": "preview"}, 200), &preview))
	call("POST", sandbox, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO SANDBOX"}, 409)
	call("POST", sandbox, map[string]string{"action": "approve", "digest": preview.Digest, "confirmation": "APPROVE SANDBOX DRAFT"}, 200)
	var posted posting
	must(json.Unmarshal(call("POST", sandbox, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO SANDBOX"}, 200), &posted))
	if posted.State != "verified" || posted.ExternalID != "456" || sandboxWrites != 1 || readbacks == 0 {
		t.Fatalf("one sandbox write, confirmed by its readback: %+v writes=%d readbacks=%d", posted, sandboxWrites, readbacks)
	}
	call("POST", sandbox, map[string]string{"action": "execute", "digest": preview.Digest, "confirmation": "POST TO SANDBOX"}, 409)
	if sandboxWrites != 1 {
		t.Fatalf("a posted journal is never written again: %d", sandboxWrites)
	}

	// While the reconciliation is in the declared scope, sign-off is refused and says why.
	ready := func() readiness {
		t.Helper()
		var s readiness
		must(json.Unmarshal(call("GET", base+"/readiness", nil, 200), &s))
		return s
	}
	sign := func(s readiness, want int) {
		t.Helper()
		call("POST", base+"/signoff", map[string]any{"digest": s.Digest, "expected_decision_id": s.DecisionID, "action": "sign", "note": "September prepaid schedule signed off", "confirmed": true}, want)
	}
	current := ready()
	call("POST", base+"/scope", map[string]any{"digest": current.Digest, "scope": closeScope{Description: "Prepaid software and the operating bank reconciliation.", RequiredPapers: []string{prepaid, bank}, ExternalSteps: []externalStep{}}}, 200)
	current = ready()
	if len(current.Blockers) != 1 || current.Blockers[0] != "Draft #"+bank+" requires current approval and resolved calculation blockers." {
		t.Fatalf("the unsupported reconciliation blocks sign-off: %v", current.Blockers)
	}
	sign(current, 409)

	// The firm signs off a scope that says what it leaves out.
	call("POST", base+"/scope", map[string]any{"digest": current.Digest, "scope": closeScope{Description: "Prepaid software only. The operating bank reconciliation is not covered: statement page 2 is missing.", RequiredPapers: []string{prepaid}, ExternalSteps: []externalStep{}}}, 200)
	current = ready()
	if len(current.Blockers) != 0 {
		t.Fatalf("the declared scope is ready: %v", current.Blockers)
	}
	sign(current, 200)
	current = ready()
	if current.State != "signed_off" || current.DecisionID == "" {
		t.Fatalf("signed off: %+v", current)
	}

	// The binder carries the decision, the sandbox receipt and the uncovered reconciliation with its blocker.
	binder := call("GET", base+"/binder", nil, 200)
	archive, err := zip.NewReader(bytes.NewReader(binder), int64(len(binder)))
	must(err)
	files := map[string][]byte{}
	for _, f := range archive.File {
		r, err := f.Open()
		must(err)
		files[f.Name], err = io.ReadAll(r)
		must(err)
		r.Close()
	}
	var pack closePackage
	must(json.Unmarshal(files["manifest.json"], &pack))
	var receipts []struct {
		State      string `json:"state"`
		ExternalID string `json:"external_id"`
	}
	must(json.Unmarshal(pack.SandboxPostings, &receipts))
	if !pack.ReleaseReady || pack.Readiness == nil || pack.Readiness.DecisionID != current.DecisionID || len(receipts) != 1 || receipts[0].State != "verified" || receipts[0].ExternalID != "456" || len(files["checksums.sha256"]) == 0 {
		t.Fatalf("the binder carries the sign-off and the sandbox receipt: ready=%v receipts=%+v", pack.ReleaseReady, receipts)
	}
	review := string(files["review.html"])
	bankSection := review[strings.Index(review, "draft #"+bank):]
	if !strings.Contains(bankSection, "Statement page 2 is missing.") || !strings.Contains(bankSection, "Outside the declared sign-off scope: sign-off does not cover this draft.") || !strings.Contains(review[strings.Index(review, "draft #"+prepaid):], "In the declared sign-off scope.") {
		t.Fatalf("the binder shows the reconciliation as uncovered, with its blocker:\n%s", review)
	}
}
