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

func TestCreditApplicationsAreApprovedAttemptedOnceAndReadBack(t *testing.T) {
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
	org := "org_Apply" + rand.Text()
	var closeID, connectionID, evidenceID string
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Apply fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_accountant',true,'Synthetic enrollment','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Apply fixture','2026-09') RETURNING id::text`, org).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM credit_applications WHERE close_id=$1`, `DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM quickbooks_connections WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			db.Exec(q, closeID)
		}
		db.Exec(`DELETE FROM production_posting_grants WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	t.Setenv("SECRETS_KEY", strings.Repeat("ab", 32))
	credentials, _ := json.Marshal(quickbooks.Credentials{Access: "synthetic-production-access", Refresh: "synthetic-refresh", Expires: 3600, Type: "bearer"})
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name,environment,access_expires_at,home_currency) VALUES($1,$2,'Apply fixture','fixture','active',$3,now()+interval '1 day','123','Apply fixture','production',now()+interval '1 hour','USD') RETURNING id::text`, closeID, org, rand.Text()).Scan(&connectionID))
	sealed, err := secrets.Seal(org+"/qbo/"+connectionID, credentials)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_connections SET credential=$2 WHERE id=$1`, connectionID, sealed)
	must(err)
	payment := `{"Id":"70","SyncToken":"1","TxnDate":"2026-09-05","CustomerRef":{"value":"4","name":"Acme"},"CurrencyRef":{"value":"USD"},"TotalAmt":200,"UnappliedAmt":200,"Line":[]}`
	invoice := `{"Id":"91","SyncToken":"3","TxnDate":"2026-08-20","CustomerRef":{"value":"4","name":"Acme"},"CurrencyRef":{"value":"USD"},"TotalAmt":250,"Balance":250}`
	otherInvoice := `{"Id":"92","SyncToken":"1","TxnDate":"2026-08-21","CustomerRef":{"value":"5","name":"Beta"},"CurrencyRef":{"value":"USD"},"TotalAmt":90,"Balance":90}`
	credit := `{"Id":"80","SyncToken":"2","TxnDate":"2026-09-01","VendorRef":{"value":"21","name":"Staples"},"CurrencyRef":{"value":"USD"},"Balance":60}`
	bill := `{"Id":"41","SyncToken":"5","TxnDate":"2026-08-25","VendorRef":{"value":"21","name":"Staples"},"CurrencyRef":{"value":"USD"},"Balance":100}`
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,'QuickBooks transactions 2026-09','capture-digest','{}','quickbooks-sync','ready') RETURNING id::text`, closeID).Scan(&evidenceID))
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now(),$4)`, closeID, connectionID, evidenceID,
		`{"realm":"123","records":{},"open_items":{"Payment":[`+payment+`],"Invoice":[`+invoice+`,`+otherInvoice+`],"VendorCredit":[`+credit+`],"Bill":[`+bill+`]}}`)
	must(err)

	paymentWrites, billPayments := 0, 0
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	http.DefaultTransport = sandboxTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "quickbooks.api.intuit.com" {
			t.Fatalf("a production application must use the production host: %s", r.URL.Host)
		}
		path := r.URL.Path[strings.Index(r.URL.Path, "/123/")+4:]
		body := map[string]string{"/invoice/92": `{"Invoice":` + otherInvoice + `}`, "/preferences": `{"Preferences":{"AccountingInfoPrefs":{"BookCloseDate":""}}}`,
			"/account/35": `{"Account":{"Id":"35","Name":"Checking","Active":true,"AccountType":"Bank","CurrencyRef":{"value":"USD"}}}`,
			"/account/36": `{"Account":{"Id":"36","Name":"Supplies","Active":true,"AccountType":"Expense","CurrencyRef":{"value":"USD"}}}`,
			"/account/37": `{"Account":{"Id":"37","Name":"Savings","Active":true,"AccountType":"Bank","CurrencyRef":{"value":"USD"}}}`}[path]
		switch {
		case r.Method == "POST" && path == "/payment":
			paymentWrites++
			body = `{"Payment":{"Id":"70"}}`
		case r.Method == "POST" && path == "/billpayment":
			billPayments++
			body = `{"BillPayment":{"Id":"95"}}`
		case path == "/billpayment/95":
			body = `{"BillPayment":{"Id":"95","TxnDate":"2026-09-30","PayType":"Check","CheckPayment":{"BankAccountRef":{"value":"35"}},"CurrencyRef":{"value":"USD"},"VendorRef":{"value":"21"},"TotalAmt":0,"Line":[{"Amount":60,"LinkedTxn":[{"TxnId":"41","TxnType":"Bill"}]},{"Amount":60,"LinkedTxn":[{"TxnId":"80","TxnType":"VendorCredit"}]}]}}`
		case path == "/payment/70":
			body = `{"Payment":` + payment + `}`
			if paymentWrites > 0 {
				body = `{"Payment":` + strings.NewReplacer(`"SyncToken":"1"`, `"SyncToken":"2"`, `"UnappliedAmt":200`, `"UnappliedAmt":50`, `"Line":[]`, `"Line":[{"Amount":150,"LinkedTxn":[{"TxnId":"91","TxnType":"Invoice"}]}]`).Replace(payment) + `}`
			}
		case path == "/invoice/91":
			body = `{"Invoice":` + invoice + `}`
			if paymentWrites > 0 {
				body = `{"Invoice":` + strings.Replace(invoice, `"Balance":250`, `"Balance":100`, 1) + `}`
			}
		case path == "/vendorcredit/80":
			body = `{"VendorCredit":` + credit + `}`
			if billPayments > 0 {
				body = `{"VendorCredit":` + strings.Replace(credit, `"Balance":60`, `"Balance":0`, 1) + `}`
			}
		case path == "/bill/41":
			body = `{"Bill":` + bill + `}`
			if billPayments > 0 {
				body = `{"Bill":` + strings.Replace(bill, `"Balance":100`, `"Balance":40`, 1) + `}`
			}
		}
		if body == "" {
			t.Fatalf("unexpected QuickBooks call %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_accountant"}, Organization: org, Permissions: []string{"clients:all", "posting:approve", "posting:execute"}}
	mux := http.NewServeMux()
	registerProductionPosting(mux, db)
	registerCreditApplications(mux, db)
	call := func(path string, input any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(input)
		r := httptest.NewRequest("POST", path, strings.NewReader(string(body))).WithContext(auth.WithActor(context.Background(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %v: HTTP %d %s", path, input, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + closeID + "/quickbooks/applications"
	apply := func(changes map[string]any) map[string]any {
		out := map[string]any{"action": "preview", "kind": "payment", "source_id": "70", "target_id": "91", "amount": "150.00", "reason": "Remittance names invoice 91", "rule_reference": "Cash application rule 1", "confirmed": true}
		for k, v := range changes {
			out[k] = v
		}
		return out
	}
	if body := call(path, apply(nil), 409); !strings.Contains(string(body), "not enabled") {
		t.Fatalf("applications stay off in production until enabled: %s", body)
	}
	t.Setenv("PRODUCTION_POSTING_KINDS", "prepaid,payment_application,credit_application")
	call("/v1/closes/"+closeID+"/production-grant", map[string]any{"action": "grant", "connection_id": connectionID, "account_ids": []string{"35", "36"}, "kinds": []string{"payment_application", "credit_application"}, "max_amount": "1000.00", "expires_at": time.Now().Add(time.Hour), "reason": "Reviewed synthetic application authority", "confirmation": "GRANT PRODUCTION POSTING"}, 200)
	if body := call(path, apply(map[string]any{"amount": "300.00"}), 409); !strings.Contains(string(body), "exceeds what is open") {
		t.Fatalf("an amount above the payment's unapplied 200.00 is refused: %s", body)
	}
	if body := call(path, apply(map[string]any{"target_id": "92", "amount": "50.00"}), 409); !strings.Contains(string(body), "same customer") {
		t.Fatalf("a payment is never applied to another customer's invoice: %s", body)
	}
	call(path, apply(map[string]any{"rule_reference": "x"}), 422)
	var item creditApplication
	must(json.Unmarshal(call(path, apply(nil), 200), &item))
	// Under the firm's independent-review rule the person who prepared an application cannot approve it.
	_, err = db.Exec(`UPDATE organizations SET require_independent_review=true WHERE id=$1`, org)
	must(err)
	if body := call(path, map[string]any{"action": "approve", "id": item.ID, "digest": item.Digest, "confirmed": true}, 409); !strings.Contains(string(body), "another member") {
		t.Fatalf("the preparer cannot approve under the firm's rule: %s", body)
	}
	actor.Subject = "user_reviewer"
	call(path, map[string]any{"action": "approve", "id": item.ID, "digest": item.Digest, "confirmed": true}, 200)
	actor.Subject = "user_accountant"
	_, err = db.Exec(`UPDATE organizations SET require_independent_review=false,independent_review_min_amount=1000 WHERE id=$1`, org)
	must(err)
	call(path, map[string]any{"action": "approve", "id": item.ID, "digest": item.Digest, "confirmed": true}, 200)
	_, err = db.Exec(`UPDATE organizations SET independent_review_min_amount=100 WHERE id=$1`, org)
	must(err)
	if body := call(path, map[string]any{"action": "execute", "id": item.ID, "digest": item.Digest, "confirmed": true}, 409); !strings.Contains(string(body), "at least 100") || paymentWrites != 0 {
		t.Fatalf("a minimum the amount now reaches stops the preparer's own approval before the write: %s writes=%d", body, paymentWrites)
	}
	_, err = db.Exec(`UPDATE organizations SET independent_review_min_amount=NULL WHERE id=$1`, org)
	must(err)
	must(json.Unmarshal(call(path, map[string]any{"action": "execute", "id": item.ID, "digest": item.Digest, "confirmed": true}, 200), &item))
	if paymentWrites != 1 || item.State != "verified" || item.Amount != "150.00" {
		t.Fatalf("the approved application is written once and read back: %+v writes=%d", item, paymentWrites)
	}

	// A newer capture whose transactions did not change reads open items afresh, and its candidates are the ones offered.
	fresh := `{"Id":"93","SyncToken":"1","TxnDate":"2026-09-20","CustomerRef":{"value":"4","name":"Acme"},"CurrencyRef":{"value":"USD"},"TotalAmt":200,"Balance":200}`
	matchingBill := `{"Id":"43","SyncToken":"1","TxnDate":"2026-08-28","VendorRef":{"value":"21","name":"Staples"},"CurrencyRef":{"value":"USD"},"Balance":60}`
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,finished_at,payload) VALUES($1,$2,'2026-09','fixture','unchanged',$3,now(),$4)`, closeID, connectionID, evidenceID,
		`{"counts":{},"open_items":{"Payment":[`+payment+`],"Invoice":[`+fresh+`],"VendorCredit":[`+credit+`],"Bill":[`+bill+`,`+matchingBill+`]}}`)
	must(err)
	listRequest := httptest.NewRequest("GET", path, nil).WithContext(auth.WithActor(context.Background(), actor))
	listed := httptest.NewRecorder()
	mux.ServeHTTP(listed, listRequest)
	if !strings.Contains(listed.Body.String(), `"id":"93"`) || strings.Contains(listed.Body.String(), `"id":"91"`) {
		t.Fatalf("candidates come from the newest capture, changed or not: %s", listed.Body.String())
	}
	// The agent suggests the vendor credit for the one bill it equals; payment 70 already has an application, so its
	// equal invoice is not suggested again.
	var offered struct {
		CaptureID   string                  `json:"capture_id"`
		Suggestions []quickbooks.Suggestion `json:"suggestions"`
	}
	must(json.Unmarshal(listed.Body.Bytes(), &offered))
	if len(offered.Suggestions) != 1 || offered.Suggestions[0] != (quickbooks.Suggestion{Kind: "vendor_credit", SourceID: "80", TargetID: "43", Amount: "60.00", Basis: offered.Suggestions[0].Basis}) || offered.CaptureID == "" {
		t.Fatalf("one suggestion, for the credit and the bill it equals: %+v", offered)
	}
	if body := call(path, apply(map[string]any{"kind": "vendor_credit", "source_id": "80", "target_id": "43", "amount": "60.00", "txn_date": "2026-09-30", "bank_account_id": "35", "capture_id": "1"}), 409); !strings.Contains(string(body), "captured again") {
		t.Fatalf("a suggestion from an earlier capture is reviewed again: %s", body)
	}
	credited := func(changes map[string]any) map[string]any {
		out := apply(map[string]any{"kind": "vendor_credit", "source_id": "80", "target_id": "41", "amount": "60.00", "txn_date": "2026-09-30", "bank_account_id": "35", "rule_reference": "Supplier credit rule 2"})
		for k, v := range changes {
			out[k] = v
		}
		return out
	}
	if body := call(path, credited(map[string]any{"txn_date": "2026-10-01"}), 422); !strings.Contains(string(body), "close month") {
		t.Fatalf("the bill payment is dated in the close month: %s", body)
	}
	if body := call(path, credited(map[string]any{"bank_account_id": "36"}), 422); !strings.Contains(string(body), "bank account") {
		t.Fatalf("the bill payment names a bank account: %s", body)
	}
	if body := call(path, credited(map[string]any{"bank_account_id": "37"}), 422); !strings.Contains(string(body), "grant") {
		t.Fatalf("the bank account must be in the production grant: %s", body)
	}
	must(json.Unmarshal(call(path, credited(nil), 200), &item))
	call(path, map[string]any{"action": "approve", "id": item.ID, "digest": item.Digest, "confirmed": true}, 200)
	must(json.Unmarshal(call(path, map[string]any{"action": "execute", "id": item.ID, "digest": item.Digest, "confirmed": true}, 200), &item))
	if billPayments != 1 || item.State != "verified" || item.ExternalID != "95" {
		t.Fatalf("the vendor credit is applied once by a bill payment that is read back: %+v", item)
	}

	// A bill payment whose create response was lost is bound by its ID only when it verifies.
	_, err = db.Exec(`UPDATE credit_applications SET state='unknown',external_id=NULL WHERE id=$1`, item.ID)
	must(err)
	call(path, map[string]any{"action": "verify", "id": item.ID, "digest": item.Digest}, 409)
	must(json.Unmarshal(call(path, map[string]any{"action": "verify", "id": item.ID, "digest": item.Digest, "bill_payment_id": "95"}, 200), &item))
	if billPayments != 1 || item.State != "verified" || item.ExternalID != "95" {
		t.Fatalf("a found bill payment is bound after readback, without a second write: %+v", item)
	}

	// An uncertain attempt blocks sign-off.
	_, err = db.Exec(`UPDATE credit_applications SET state='unknown' WHERE id=$1`, item.ID)
	must(err)
	tx, err := db.Begin()
	must(err)
	defer tx.Rollback()
	ready, err := closeReadiness(context.Background(), tx, closeID)
	must(err)
	if !strings.Contains(strings.Join(ready.Blockers, " "), "Credit application attempt #"+item.ID+" must be resolved") {
		t.Fatalf("an uncertain application blocks sign-off: %v", ready.Blockers)
	}
}
