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
	"close.local/backend/internal/quickbooks"
	"close.local/backend/internal/secrets"
	"github.com/golang-jwt/jwt/v5"
)

func TestProductionBillCorrectionStaysInsideItsGrant(t *testing.T) {
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
	org := "org_Corrections" + rand.Text()
	var closeID, connectionID, evidenceID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Corrections fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Corrections fixture','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM bill_corrections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM production_posting_grants WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	credentials, _ := json.Marshal(quickbooks.Credentials{Access: "synthetic-production-access", Refresh: "synthetic-refresh", Expires: 3600, Type: "bearer"})
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment,access_expires_at,home_currency) VALUES($1,$2,'Corrections fixture','fixture','active',$3,now()+interval '1 day','123','Corrections fixture','production',now()+interval '1 hour','USD') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connectionID))
	sealed, err := secrets.Seal(org+"/qbo/"+connectionID, credentials)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_connections SET credential=$2 WHERE id=$1`, connectionID, sealed)
	must(err)
	bill := `{"Id":"41","SyncToken":"3","TxnDate":"2026-09-15","VendorRef":{"value":"21"},"CurrencyRef":{"value":"USD"},"TotalAmt":100,"Balance":100,"Line":[{"Id":"1","Amount":60,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"7"}}},{"Id":"2","Amount":40,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"8"}}}]}`
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'QuickBooks transactions 2026-09','capture-digest','{}','quickbooks-sync','ready') RETURNING id::text`, closeID).Scan(&evidenceID))
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now(),$4)`, closeID, connectionID, evidenceID, `{"realm":"123","records":{"Bill":[`+bill+`]}}`)
	must(err)

	writes := 0
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "quickbooks.api.intuit.com" {
			t.Fatalf("a production correction must use the production host: %s", r.URL.Host)
		}
		body := ""
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/bill"):
			writes++
			body = `{"Bill":{"Id":"41"}}`
		case strings.HasSuffix(r.URL.Path, "/bill/41"):
			body = `{"Bill":` + bill + `}`
			if writes > 0 {
				body = `{"Bill":` + strings.Replace(strings.Replace(bill, `"SyncToken":"3"`, `"SyncToken":"4"`, 1), `{"value":"8"}`, `{"value":"9"}`, 1) + `}`
			}
		case strings.HasSuffix(r.URL.Path, "/account/9"):
			body = `{"Account":{"Id":"9","Name":"Office supplies","Active":true,"Classification":"Expense","CurrencyRef":{"value":"USD"}}}`
		case strings.HasSuffix(r.URL.Path, "/preferences"):
			body = `{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":""}}}`
		default:
			t.Fatalf("unexpected QuickBooks call %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})

	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "posting:approve", "posting:execute"}}
	mux := http.NewServeMux()
	registerProductionPosting(mux, db)
	registerBillCorrection(mux, db)
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
	grant := func(limit string, accounts ...string) string {
		var out struct{ ID string }
		json.Unmarshal(call(grantPath, map[string]any{"action": "grant", "connection_id": connectionID, "account_ids": accounts, "kinds": []string{"bill_correction"}, "max_amount": limit, "expires_at": time.Now().Add(time.Hour), "reason": "Reviewed synthetic correction authority", "confirmation": "GRANT PRODUCTION POSTING"}, 200), &out)
		return out.ID
	}
	revoke := func(id string) {
		call(grantPath, map[string]any{"action": "revoke", "grant_id": id, "confirmation": "REVOKE PRODUCTION GRANT"}, 200)
	}
	path := "/v1/closes/" + closeID + "/quickbooks/bills/41/correction"
	preview := map[string]any{"action": "preview", "treatment": "inconsistent_coding", "reason": "Supplies coded to travel", "target_account_id": "9", "target_line_id": "2", "confirmed": true}

	first := grant("1000.00", "7", "9")
	if body := call(path, preview, 409); !strings.Contains(string(body), "not enabled") {
		t.Fatalf("production Bill corrections stay off until enabled: %s", body)
	}
	t.Setenv("PRODUCTION_POSTING_KINDS", "prepaid,bill_correction")
	if body := call(path, preview, 409); !strings.Contains(string(body), "current account is not allowed") {
		t.Fatalf("a recode out of an account the grant does not allow is refused before anything is saved: %s", body)
	}
	revoke(first)
	second := grant("30.00", "8", "9")
	if body := call(path, preview, 409); !strings.Contains(string(body), "grant limit") {
		t.Fatalf("the grant limit bounds the recoded line (40.00): %s", body)
	}
	revoke(second)
	var item billCorrection
	call(path, preview, 409)
	third := grant("1000.00", "8", "9")
	json.Unmarshal(call(path, preview, 200), &item)
	revoke(third)
	grant("1000.00", "8", "9")
	if body := call(path, map[string]any{"action": "approve", "digest": item.Digest, "confirmed": true}, 409); !strings.Contains(string(body), "grant changed") {
		t.Fatalf("an approval is bound to the grant the correction was previewed under: %s", body)
	}
	json.Unmarshal(call(path, preview, 200), &item)
	if item.State != "preview" || item.GrantID == "" {
		t.Fatalf("the preview is bound to the current grant: %+v", item)
	}
	call(path, map[string]any{"action": "approve", "digest": item.Digest, "confirmed": true}, 200)
	json.Unmarshal(call(path, map[string]any{"action": "execute", "digest": item.Digest, "confirmed": true}, 200), &item)
	if writes != 1 || item.State != "verified" {
		t.Fatalf("the approved correction posts once and verifies: %+v writes=%d", item, writes)
	}
}
