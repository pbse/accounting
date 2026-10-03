package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

// Worked by hand: opening 1,000.00 on Monday 2026-10-05; Wednesday payroll of 3,000.00, a Thursday receipt of
// 4,000.00, rent of 500.00 next Monday, a receipt before the plan starts and one after it ends.
func TestPlanWeeksShowsAMidweekShortfallAndCarriesBalances(t *testing.T) {
	item := func(on, amount, label string) cashItem {
		v, _ := new(big.Rat).SetString(amount)
		return cashItem{On: on, Amount: amount, Label: label, value: v}
	}
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	opening, _ := new(big.Rat).SetString("1000.00")
	weeks, outside := planWeeks(start, opening, []cashItem{
		item("2026-10-07", "-3000.00", "Payroll"), item("2026-10-08", "4000.00", "Invoice 12"), item("2026-10-12", "-500.00", "Rent"),
		item("2026-10-01", "250.00", "Overdue invoice"), item("2027-01-04", "900.00", "Invoice after the horizon"),
	})
	if len(weeks) != 13 || weeks[0].Start != "2026-10-05" || weeks[0].End != "2026-10-11" || weeks[12].End != "2027-01-03" {
		t.Fatalf("thirteen seven-day weeks from the start: %+v", weeks)
	}
	w := weeks[0]
	if w.Opening != "1000.00" || w.Receipts != "4000.00" || w.Disbursements != "3000.00" || w.Closing != "2000.00" || w.Lowest != "-2000.00" || w.LowestOn != "2026-10-07" {
		t.Fatalf("the week ends positive but the Wednesday payroll leaves the account 2,000.00 short: %+v", w)
	}
	if n := weeks[1]; n.Opening != "2000.00" || n.Closing != "1500.00" || n.Lowest != "1500.00" || n.LowestOn != "2026-10-12" || len(n.Items) != 1 {
		t.Fatalf("each week opens at the last one's close: %+v", n)
	}
	if last := weeks[12]; last.Opening != "1500.00" || last.Closing != "1500.00" || last.Lowest != "1500.00" {
		t.Fatalf("a quiet week holds its balance: %+v", last)
	}
	if len(outside) != 2 || outside[0].Label != "Overdue invoice" || outside[1].Label != "Invoice after the horizon" {
		t.Fatalf("items before the start or after the horizon are kept apart, never placed: %+v", outside)
	}
}

func TestCashPlanFromTheLedgerOpenItemsAndApprovedObligations(t *testing.T) {
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
	entity := "Cash plan " + rand.Text()
	var closeID, connection, evidence string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		db.Exec(`DELETE FROM cash_obligations WHERE client_entity=$1`, entity)
		db.Exec(`DELETE FROM open_item_actions WHERE client_entity=$1`, entity)
		db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM evidence WHERE close_id=$1`, closeID)
		db.Exec(`DELETE FROM client_closes WHERE id=$1`, closeID)
	}()
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency) VALUES($1,'local-development',$2,'fixture','active',$3,now(),'123','USD') RETURNING id::text`, closeID, entity, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, closeID, rand.Text()).Scan(&evidence))
	accounts := `[{"Id":"1","Name":"Checking","AccountType":"Bank","CurrentBalance":5000},{"Id":"2","Name":"Euro account","AccountType":"Bank","CurrentBalance":900,"CurrencyRef":{"value":"EUR"}},{"Id":"3","Name":"Receivables","AccountType":"Accounts Receivable","CurrentBalance":3400}]`
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),'2026-10-05 10:00+00','fixture')`, closeID, connection, rand.Text(), `{"source_id":"`+evidence+`","accounts":`+accounts+`}`)
	must(err)
	invoice := func(id, name, amount, due string) map[string]any {
		return map[string]any{"Id": id, "DocNumber": "INV-" + id, "TxnDate": "2026-09-01", "DueDate": due, "Balance": json.Number(amount), "CustomerRef": map[string]string{"value": id, "name": name}, "CurrencyRef": map[string]string{"value": "USD"}}
	}
	bill := func(id, vendor, amount, due string) map[string]any {
		return map[string]any{"Id": id, "DocNumber": "B-" + id, "TxnDate": "2026-09-01", "DueDate": due, "Balance": json.Number(amount), "VendorRef": map[string]string{"value": id, "name": vendor}, "CurrencyRef": map[string]string{"value": "USD"}}
	}
	// A landlord bill in euros is left out of the dollar plan, so it cannot stand in for November's dollar rent either.
	euroBill := bill("11", "Landlord", "1400.00", "2026-11-06")
	euroBill["CurrencyRef"] = map[string]string{"value": "EUR"}
	payload, _ := json.Marshal(map[string]any{"finished_at": "2026-10-05T09:00:00Z", "completeness": "All supported types were paged.", "open_items": map[string]any{
		"Invoice": []any{invoice("1", "Acme", "2000.00", "2026-10-09"), invoice("2", "Beta", "800.00", "2026-09-20"), invoice("3", "Gamma", "600.00", "2026-10-20")},
		"Bill":    []any{bill("9", "Landlord", "1500.00", "2026-10-07"), bill("10", "Supplies", "300.00", "2026-10-10"), euroBill},
	}})
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',now(),$3)`, closeID, connection, string(payload))
	must(err)

	token := strings.Repeat("c", 32)
	handler := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/closes/"+closeID+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var items struct {
		CaptureID string `json:"capture_id"`
	}
	must(json.Unmarshal(call("GET", "/open-items", nil, 200), &items))
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Invoice", "item_id": "3", "status": "promised", "expected_on": "2026-10-14"}, 200)
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Bill", "item_id": "10", "status": "disputed", "note": "Short-shipped"}, 200)
	add := func(fields map[string]any, want int) {
		t.Helper()
		fields["action"] = "add"
		call("POST", "/cash-obligations", fields, want)
	}
	add(map[string]any{"name": "Payroll", "category": "payroll", "direction": "out", "amount": "4000.505", "first_on": "2026-10-07", "every": "two_weeks", "basis": "Gusto schedule"}, 422)
	add(map[string]any{"name": "Payroll", "category": "payroll", "direction": "out", "amount": "4000.00", "first_on": "2026-09-23", "every": "two_weeks", "basis": "Gusto pay schedule, approved by the CFO"}, 200)
	add(map[string]any{"name": "Rent", "category": "rent", "direction": "out", "amount": "1500.00", "first_on": "2026-10-07", "every": "month", "vendor": "Landlord", "basis": "Lease section 3"}, 200)
	add(map[string]any{"name": "Sales tax", "category": "tax", "direction": "out", "amount": "999.00", "first_on": "2026-10-20", "every": "once", "basis": "Q3 return estimate"}, 200)
	var list struct{ Obligations []cashObligation }
	must(json.Unmarshal(call("GET", "/cash-obligations", nil, 200), &list))
	for _, o := range list.Obligations {
		if o.Name != "Sales tax" {
			call("POST", "/cash-obligations", map[string]any{"action": "approve", "id": o.ID}, 200)
			call("POST", "/cash-obligations", map[string]any{"action": "approve", "id": o.ID}, 409)
		}
	}

	var plan struct {
		State, Start string
		Opening      struct {
			Amount  string
			LeftOut []string `json:"left_out"`
		}
		OpenItems struct {
			Through      string
			MissingLater bool `json:"missing_later"`
		} `json:"open_items"`
		Weeks       []cashWeek
		PastDue     []cashItem          `json:"past_due"`
		Unscheduled []map[string]string `json:"unscheduled"`
		Covered     []map[string]string
		Pending     []cashObligation `json:"pending_obligations"`
	}
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if plan.State != "ready" || plan.Start != "2026-10-05" || plan.Opening.Amount != "5000.00" || len(plan.Opening.LeftOut) != 1 || plan.OpenItems.Through != "2026-09-30" || !plan.OpenItems.MissingLater {
		t.Fatalf("the plan opens on the capture's day at the ledger's home-currency bank balances, and says the September capture has nothing dated in October: %+v", plan)
	}
	w1, w2, w3 := plan.Weeks[0], plan.Weeks[1], plan.Weeks[2]
	if w1.Receipts != "2000.00" || w1.Disbursements != "5500.00" || w1.Closing != "1500.00" || w1.Lowest != "-500.00" || w1.LowestOn != "2026-10-07" {
		t.Fatalf("Wednesday's rent bill and payroll take the account 500.00 short before Friday's receipt; the rent estimate is the bill, not counted twice: %+v", w1)
	}
	if w2.Receipts != "600.00" || w2.Closing != "2100.00" || w3.Disbursements != "4000.00" || w3.Closing != "-1900.00" {
		t.Fatalf("the promised invoice lands on its promised date and payroll recurs every two weeks: %+v %+v", w2, w3)
	}
	if len(plan.PastDue) != 1 || plan.PastDue[0].Label != "Invoice INV-2 · Beta" || len(plan.Unscheduled) != 1 || !strings.Contains(plan.Unscheduled[0]["reason"], "disputed") {
		t.Fatalf("a past-due invoice is not given a date and a disputed bill is not scheduled: %+v %+v", plan.PastDue, plan.Unscheduled)
	}
	if len(plan.Covered) != 1 || plan.Covered[0]["on"] != "2026-10-07" || len(plan.Pending) != 1 || plan.Pending[0].Name != "Sales tax" || plan.Weeks[4].Disbursements != "5500.00" {
		t.Fatalf("the week's landlord bill covers the rent estimate, and an unapproved obligation does not count: %+v %+v", plan.Covered, plan.Pending)
	}
	// An executive's plan leaves the draft schedule (its amount, vendor and notes) with the firm, saying only how many.
	executive := "user_ceo_" + rand.Text()
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT organization_id,$1,true,'Synthetic','fixture' FROM client_closes WHERE id::text=$2`, executive, closeID)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) SELECT organization_id,$1,entity,true,'Synthetic','fixture' FROM client_closes WHERE id::text=$2`, executive, closeID)
	must(err)
	defer db.Exec(`DELETE FROM client_access WHERE subject=$1`, executive)
	defer db.Exec(`DELETE FROM organization_member_access WHERE subject=$1`, executive)
	var org string
	must(db.QueryRow(`SELECT organization_id FROM client_closes WHERE id::text=$1`, closeID).Scan(&org))
	seen := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/cash-plan", nil).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: executive}, Organization: org, Permissions: []string{"executive:read"}}))
	w := httptest.NewRecorder()
	savedCloses(db, token).ServeHTTP(w, seen)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Sales tax") || !strings.Contains(w.Body.String(), `"unapproved_schedules":1`) || !strings.Contains(w.Body.String(), `"pending_obligations":[]`) {
		t.Fatalf("an executive does not see draft schedules: %d %s", w.Code, w.Body.String())
	}
	// Handing the landlord's bill to bill pay for next week moves the payment, and the estimate stays covered by it.
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Bill", "item_id": "9", "status": "handed_to_bill_pay", "expected_on": "2026-10-14"}, 200)
	plan.Covered = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if w1, w2 := plan.Weeks[0], plan.Weeks[1]; len(plan.Covered) != 1 || w1.Disbursements != "4000.00" || w1.Lowest != "1000.00" || w2.Disbursements != "1500.00" || w2.Closing != "2100.00" {
		t.Fatalf("the bill moves to its bill-pay date and the rent estimate is still not counted: %+v %+v %+v", plan.Covered, w1, w2)
	}
}
