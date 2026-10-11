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
		db.Exec(`DELETE FROM cash_obligation_matches WHERE client_entity=$1`, entity)
		db.Exec(`DELETE FROM cash_obligations WHERE client_entity=$1`, entity)
		db.Exec(`DELETE FROM cash_plan_reviews WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`, entity)
		db.Exec(`DELETE FROM cash_scenarios WHERE client_entity=$1`, entity)
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
		"Bill":    []any{bill("9", "Landlord", "1500.00", "2026-10-07"), bill("10", "Supplies", "300.00", "2026-10-10"), euroBill, bill("12", "Cleaner", "250.00", "2026-10-3x")},
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
		Matches     []cashMatched
		Questions   []cashQuestion
		Pending     []cashObligation `json:"pending_obligations"`
		Horizon     map[string]any
		Readiness   struct {
			State      string
			Unresolved []cashGap
		}
		Review *struct{ State string }
	}
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if plan.State != "ready" || plan.Start != "2026-10-05" || plan.Opening.Amount != "5000.00" || len(plan.Opening.LeftOut) != 1 || plan.OpenItems.Through != "2026-09-30" || !plan.OpenItems.MissingLater {
		t.Fatalf("the plan opens on the capture's day at the ledger's home-currency bank balances, and says the September capture has nothing dated in October: %+v", plan)
	}
	// The landlord's bill due the week rent is scheduled is a question, not a silent replacement: until someone decides,
	// both count, the plan is not reviewable, and its readiness names the decision.
	if w1 := plan.Weeks[0]; len(plan.Questions) != 1 || plan.Questions[0].Name != "Rent" || plan.Questions[0].On != "2026-10-07" || len(plan.Questions[0].Bills) != 1 || plan.Questions[0].Bills[0].Document != "bill:9" ||
		w1.Disbursements != "7000.00" || len(plan.Matches) != 0 || plan.Horizon["end"] != "2027-01-03" || plan.Review != nil {
		t.Fatalf("the rent and its landlord's bill both count until decided: %+v %+v %+v", plan.Questions, plan.Weeks[0], plan.Horizon)
	}
	asked := false
	for _, g := range plan.Readiness.Unresolved {
		asked = asked || g.Kind == "decision" && strings.HasPrefix(g.Text, "Rent on 2026-10-07: Landlord has an undecided bill that week")
	}
	if plan.Readiness.State != "incomplete" || !asked {
		t.Fatalf("readiness names the open decision and the missing sources: %+v", plan.Readiness)
	}
	var early struct{ Digest string }
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &early))
	if body := string(call("POST", "/cash-plan/reviews", map[string]any{"digest": early.Digest, "assumptions": "Rent per the lease", "confirmed": true}, 409)); !strings.Contains(body, "may count a payment twice") {
		t.Fatalf("a plan with an open question cannot be reviewed: %s", body)
	}
	// Deciding: only a standing approved schedule's real date, a part within the schedule, and the decision in view.
	var rentID string
	for _, o := range list.Obligations {
		if o.Name == "Rent" {
			rentID = o.ID
		}
	}
	// A decision names the capture the person saw: the latest, unless a test says otherwise.
	decide := func(fields map[string]any, want int) []byte {
		t.Helper()
		if _, ok := fields["action"]; !ok {
			fields["action"] = "decide"
		}
		if _, ok := fields["capture_id"]; !ok {
			var seen struct {
				CaptureID string `json:"capture_id"`
			}
			must(json.Unmarshal(call("GET", "/open-items", nil, 200), &seen))
			fields["capture_id"] = seen.CaptureID
		}
		return call("POST", "/cash-matches", fields, want)
	}
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:9", "relation": "replaces", "capture_id": "0"}, 409)
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-08", "document": "bill:9", "relation": "replaces"}, 409)
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:404", "relation": "replaces"}, 409)
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:9", "relation": "part", "amount": "1600.00"}, 422)
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:9", "relation": "guess"}, 422)
	var first struct{ ID string }
	must(json.Unmarshal(decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:9", "relation": "separate"}, 200), &first))
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:9", "relation": "replaces"}, 409)
	decide(map[string]any{"obligation_id": rentID, "on": "2026-10-07", "document": "bill:9", "relation": "replaces", "previous_id": first.ID}, 200)
	// The same bill cannot also pay November's rent.
	decide(map[string]any{"obligation_id": rentID, "on": "2026-11-07", "document": "bill:9", "relation": "replaces"}, 409)
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	w1, w2, w3 := plan.Weeks[0], plan.Weeks[1], plan.Weeks[2]
	if w1.Receipts != "2000.00" || w1.Disbursements != "5500.00" || w1.Closing != "1500.00" || w1.Lowest != "-500.00" || w1.LowestOn != "2026-10-07" || len(plan.Questions) != 0 ||
		len(plan.Matches) != 1 || plan.Matches[0].Counted != "0.00" || plan.Matches[0].Bills[0].Relation != "replaces" || plan.Matches[0].Bills[0].State != "counted" {
		t.Fatalf("decided, Wednesday's rent bill is the rent, counted once, and payroll takes the account 500.00 short before Friday's receipt: %+v %+v", w1, plan.Matches)
	}
	if w2.Receipts != "600.00" || w2.Closing != "2100.00" || w3.Disbursements != "4000.00" || w3.Closing != "-1900.00" {
		t.Fatalf("the promised invoice lands on its promised date and payroll recurs every two weeks: %+v %+v", w2, w3)
	}
	reasons := map[string]string{}
	for _, u := range plan.Unscheduled {
		reasons[u["label"]] = u["reason"]
	}
	if len(plan.PastDue) != 1 || plan.PastDue[0].Label != "Invoice INV-2 · Beta" || len(plan.Unscheduled) != 2 || !strings.Contains(reasons["Bill B-10 · Supplies"], "disputed") ||
		reasons["Bill B-12 · Cleaner"] != "Its date (2026-10-3x) could not be read, so it is not placed." {
		t.Fatalf("a past-due invoice is not given a date, and a disputed bill and one with an unreadable date are not scheduled: %+v %+v", plan.PastDue, plan.Unscheduled)
	}
	proposed := false
	for _, g := range plan.Readiness.Unresolved {
		proposed = proposed || g.Kind == "decision" && strings.HasPrefix(g.Text, "Sales tax is proposed but not approved") && g.Amount == "999.00"
	}
	if len(plan.Pending) != 1 || plan.Pending[0].Name != "Sales tax" || plan.Weeks[4].Disbursements != "5500.00" || !proposed {
		t.Fatalf("an unapproved obligation does not count and readiness names it; November's rent is not paid by a euro bill: %+v %+v", plan.Pending, plan.Readiness)
	}
	// An executive's plan leaves the draft schedule (its amount, vendor and notes) with the firm, saying only how many.
	executive := "user_ceo_" + rand.Text()
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) SELECT organization_id,$1,true,'Synthetic','fixture' FROM client_closes WHERE id::text=$2`, executive, closeID)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) SELECT organization_id,$1,entity,true,'Synthetic','fixture' FROM client_closes WHERE id::text=$2`, executive, closeID)
	must(err)
	defer db.Exec(`DELETE FROM cash_scenarios WHERE created_by=$1`, executive)
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
	// A what-if delays the promised invoice and adds a hire; only its own view changes.
	var none struct{ Scenarios []map[string]any }
	call("POST", "/cash-scenarios", map[string]any{"action": "add", "name": "Late customer", "changes": []map[string]any{{"kind": "move", "item": "invoice:3", "on": "not a date"}}}, 422)
	call("POST", "/cash-scenarios", map[string]any{"action": "add", "name": "Late customer and a hire", "changes": []map[string]any{
		{"kind": "move", "item": "invoice:3", "on": "2026-11-04"},
		{"kind": "add", "label": "New engineer payroll", "direction": "out", "amount": "3000.00", "first_on": "2026-10-16", "every": "two_weeks"},
		{"kind": "move", "item": "invoice:404", "on": "2026-10-20"}}}, 200)
	must(json.Unmarshal(call("GET", "/cash-scenarios", nil, 200), &none))
	if len(none.Scenarios) != 1 {
		t.Fatalf("the scenario is listed: %+v", none)
	}
	id := none.Scenarios[0]["id"].(string)
	var what struct {
		Weeks    []cashWeek
		Lowest   map[string]string
		Scenario struct {
			Name       string
			Missing    []string
			BaseLowest map[string]string `json:"base_lowest"`
		}
	}
	var again struct {
		Weeks  []cashWeek
		Lowest map[string]string
	}
	must(json.Unmarshal(call("GET", "/cash-plan?scenario="+id, nil, 200), &what))
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &again))
	if what.Weeks[1].Receipts != "0.00" || what.Weeks[4].Receipts != "600.00" || what.Weeks[1].Disbursements != "3000.00" || what.Scenario.BaseLowest["amount"] != again.Lowest["amount"] ||
		len(what.Scenario.Missing) != 1 || what.Scenario.Missing[0] != "invoice:404" || what.Lowest["amount"] == again.Lowest["amount"] {
		t.Fatalf("the scenario moves the receipt to its week, adds the hire's payroll and reports the base plan's low point: %+v %+v %+v", what.Weeks[:5], what.Scenario, again.Lowest)
	}
	if again.Weeks[1].Receipts != "600.00" {
		t.Fatalf("the base plan is unchanged by a scenario: %+v", again.Weeks[1])
	}
	// An executive sees and opens only their own scenarios; the firm's stay with the firm.
	asExecutive := func(method, path string, body any) (int, string) {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/v1/closes/"+closeID+path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: executive}, Organization: org, Permissions: []string{"executive:read"}}))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		savedCloses(db, token).ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	if code, body := asExecutive("GET", "/cash-scenarios", nil); code != 200 || strings.Contains(body, "Late customer") {
		t.Fatalf("an executive does not see the firm's scenarios: %d %s", code, body)
	}
	if code, _ := asExecutive("GET", "/cash-plan?scenario="+id, nil); code != 404 {
		t.Fatalf("an executive cannot open the firm's scenario: %d", code)
	}
	if code, body := asExecutive("POST", "/cash-scenarios", map[string]any{"action": "add", "name": "Board's hire", "changes": []map[string]any{{"kind": "add", "label": "Sales lead", "direction": "out", "amount": "2500.00", "first_on": "2026-10-23", "every": "once"}}}); code != 200 {
		t.Fatalf("an executive tries their own what-if: %d %s", code, body)
	}
	if _, body := asExecutive("GET", "/cash-scenarios", nil); !strings.Contains(body, "Board's hire") {
		t.Fatalf("an executive sees their own scenario: %s", body)
	}
	if body := string(call("GET", "/cash-scenarios", nil, 200)); !strings.Contains(body, "Board's hire") || !strings.Contains(body, "Late customer") {
		t.Fatalf("the firm sees every scenario of the client: %s", body)
	}
	// A scenario made for other books is not applied to these.
	_, err = db.Exec(`UPDATE cash_scenarios SET company='quickbooks:sandbox:other' WHERE id::text=$1`, id)
	must(err)
	call("GET", "/cash-plan?scenario="+id, nil, 409)
	call("POST", "/cash-scenarios", map[string]any{"action": "retire", "id": id}, 200)
	call("GET", "/cash-plan?scenario="+id, nil, 404)

	// The October close's capture covers the month and reads every bill's date: the sources are complete. What is left
	// (a past-due invoice, a disputed bill, a euro bill, a proposed tax) are assumptions, so the plan is a draft.
	var digest struct{ Digest string }
	_, err = db.Exec(`UPDATE client_closes SET period='2026-10' WHERE id::text=$1`, closeID)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_entity_syncs SET period='2026-10',payload=jsonb_set(payload,'{open_items,Bill}',(SELECT jsonb_agg(b) FROM jsonb_array_elements(payload->'open_items'->'Bill') b WHERE b->>'Id'<>'12')) WHERE close_id::text=$1`, closeID)
	must(err)
	// A person reviews the plan they see; the review holds until a figure or an assumption changes, and a draft is
	// reviewed as a draft, never current.
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &digest))
	call("POST", "/cash-plan/reviews", map[string]any{"digest": digest.Digest, "assumptions": "ok", "confirmed": true}, 422)
	call("POST", "/cash-plan/reviews", map[string]any{"digest": "stale", "assumptions": "Promised dates as recorded", "confirmed": true}, 409)
	call("POST", "/cash-plan/reviews", map[string]any{"digest": digest.Digest, "assumptions": "Promised dates as recorded; payroll per Gusto", "confirmed": true}, 200)
	call("POST", "/cash-plan/reviews", map[string]any{"digest": digest.Digest, "assumptions": "Promised dates as recorded; payroll per Gusto", "confirmed": true}, 409)
	type reviewed struct {
		Reviews []struct {
			State, Assumptions string
			Actuals            []map[string]string
		}
	}
	var reviews reviewed
	must(json.Unmarshal(call("GET", "/cash-plan/reviews", nil, 200), &reviews))
	if len(reviews.Reviews) != 1 || reviews.Reviews[0].State != "draft_reviewed" || len(reviews.Reviews[0].Actuals) != 0 {
		t.Fatalf("the reviewed plan is current, with nothing to compare yet: %+v", reviews)
	}

	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	// Every week reconciles: opening plus receipts less payments is its closing, and each total is the sum of the items
	// listed under it; the next week opens at this one's close.
	for i, week := range plan.Weeks {
		rat := func(v string) *big.Rat { r, _ := new(big.Rat).SetString(v); return r }
		in, out := new(big.Rat), new(big.Rat)
		for _, item := range week.Items {
			if v := rat(item.Amount); v.Sign() > 0 {
				in.Add(in, v)
			} else {
				out.Sub(out, v)
			}
		}
		closing := new(big.Rat).Sub(new(big.Rat).Add(rat(week.Opening), rat(week.Receipts)), rat(week.Disbursements))
		if in.Cmp(rat(week.Receipts)) != 0 || out.Cmp(rat(week.Disbursements)) != 0 || closing.Cmp(rat(week.Closing)) != 0 || i > 0 && plan.Weeks[i-1].Closing != week.Opening {
			t.Fatalf("week %d does not reconcile to its items: %+v", i, week)
		}
	}
	// The past-due invoice is named as unplaced, with its amount.
	unplaced := false
	for _, g := range plan.Readiness.Unresolved {
		unplaced = unplaced || g.Kind == "decision" && g.Text == "Invoice INV-2 · Beta is past due with no agreed date." && g.Amount == "800.00"
	}
	if !unplaced {
		t.Fatalf("a past-due invoice is named in readiness: %+v", plan.Readiness)
	}
	if plan.Review == nil || plan.Review.State != "draft_reviewed" || plan.Readiness.State != "draft" {
		t.Fatalf("the plan says its newest review holds, as a draft: %+v %+v", plan.Review, plan.Readiness)
	}
	// A new proposal changes what the reviewer accepted: the review no longer holds, though no figure moved.
	add(map[string]any{"name": "Year-end bonus", "category": "payroll", "direction": "out", "amount": "18000.00", "first_on": "2026-12-15", "every": "once", "basis": "Suggested from last December's bonus"}, 200)
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if plan.Review == nil || plan.Review.State != "inputs_changed" || plan.Readiness.State == "complete" {
		t.Fatalf("a newly proposed schedule leaves the review changed: %+v %+v", plan.Review, plan.Readiness)
	}
	// A promise whose date has passed without payment is broken: the invoice stays open, unplaced, and named so.
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Invoice", "item_id": "2", "status": "promised", "expected_on": "2026-10-01"}, 200)
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	broken := false
	for _, g := range plan.Readiness.Unresolved {
		broken = broken || g.Text == "Invoice INV-2 · Beta: the promised date 2026-10-01 passed without payment, so it is not placed." && g.Amount == "800.00"
	}
	if !broken || len(plan.PastDue) != 1 || plan.Weeks[0].Receipts != "2000.00" {
		t.Fatalf("a broken promise adds no receipt and is named: %+v %+v", plan.Readiness, plan.Weeks[0])
	}
	// Handing the landlord's bill to bill pay for next week moves the payment; the decision keeps the bill as the rent.
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Bill", "item_id": "9", "status": "handed_to_bill_pay", "expected_on": "2026-10-14"}, 200)
	plan.Matches = nil
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if w1, w2 := plan.Weeks[0], plan.Weeks[1]; len(plan.Matches) != 1 || plan.Matches[0].Bills[0].On != "2026-10-14" || len(plan.Questions) != 0 || w1.Disbursements != "4000.00" || w1.Lowest != "1000.00" || w2.Disbursements != "1500.00" || w2.Closing != "2100.00" ||
		plan.Review == nil || plan.Review.State != "inputs_changed" {
		t.Fatalf("the bill moves to its bill-pay date, still pays the rent, and the review shows its inputs changed: %+v %+v %+v %+v", plan.Matches, w1, w2, plan.Review)
	}
	// A disputed bill is left out of the payments, so it no longer stands in for the week's rent: the estimate counts.
	decided := func() string {
		var id string
		must(db.QueryRow(`SELECT id::text FROM open_item_actions WHERE lower(client_entity)=lower($1) AND kind='Bill' AND item_id='9' ORDER BY id DESC LIMIT 1`, entity).Scan(&id))
		return id
	}
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Bill", "item_id": "9", "status": "disputed", "note": "Overcharged", "previous_id": decided()}, 200)
	plan.Matches = nil
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if w1 := plan.Weeks[0]; len(plan.Matches) != 1 || plan.Matches[0].Counted != "1500.00" || !strings.Contains(plan.Matches[0].Bills[0].State, "disputed") || w1.Disbursements != "5500.00" {
		t.Fatalf("a disputed bill does not stand in for the rent: the estimate counts: %+v %+v", plan.Matches, w1)
	}
	call("POST", "/open-items", map[string]any{"capture_id": items.CaptureID, "kind": "Bill", "item_id": "9", "status": "handed_to_bill_pay", "expected_on": "2026-10-14", "previous_id": decided()}, 200)
	// Retiring the rent schedule retires the decisions made against it: its bill is a plain bill again.
	call("POST", "/cash-obligations", map[string]any{"action": "retire", "id": rentID, "reason": "Lease renegotiated"}, 200)
	plan.Matches = nil
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	var standingDecisions int
	must(db.QueryRow(`SELECT count(*) FROM cash_obligation_matches WHERE client_entity=$1 AND retired_at IS NULL`, entity).Scan(&standingDecisions))
	if standingDecisions != 0 || len(plan.Matches) != 0 || plan.Weeks[1].Disbursements != "1500.00" {
		t.Fatalf("a retired schedule takes its decisions with it, and its bill still counts: %d %+v", standingDecisions, plan.Weeks[1])
	}
	// A decision whose schedule stops counting some other way lapses visibly and can be cleared.
	add(map[string]any{"name": "Rent", "category": "rent", "direction": "out", "amount": "1500.00", "first_on": "2026-10-07", "every": "month", "vendor": "Landlord", "basis": "Renegotiated lease"}, 200)
	must(json.Unmarshal(call("GET", "/cash-obligations", nil, 200), &list))
	newRent := ""
	for _, o := range list.Obligations {
		if o.Name == "Rent" && o.RetiredAt == nil {
			newRent = o.ID
		}
	}
	call("POST", "/cash-obligations", map[string]any{"action": "approve", "id": newRent}, 200)
	var lapse struct{ ID string }
	must(json.Unmarshal(decide(map[string]any{"obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "relation": "replaces"}, 200), &lapse))
	// A bill still open cannot be confirmed paid. Once a later capture no longer lists it (paid, voided or credited),
	// the rent counts again until someone confirms it was paid; then the date is settled and its decision stays.
	decide(map[string]any{"action": "paid", "obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "previous_id": lapse.ID}, 409)
	// A capture taken before the decision cannot say the bill left the open items: nothing is asked or confirmed.
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) SELECT close_id,connection_id,period,requested_by,state,now(),
 jsonb_set(payload,'{open_items,Bill}',(SELECT jsonb_agg(b) FROM jsonb_array_elements(payload->'open_items'->'Bill') b WHERE b->>'Id'<>'9')) FROM quickbooks_entity_syncs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID)
	must(err)
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	unknown := false
	for _, g := range plan.Readiness.Unresolved {
		unknown = unknown || g.Kind == "source" && strings.Contains(g.Text, "cannot say whether it is still open")
	}
	if len(plan.Matches) != 1 || plan.Matches[0].Bills[0].Confirm || plan.Matches[0].Counted != "1500.00" || !unknown || plan.Readiness.State != "incomplete" {
		t.Fatalf("an earlier capture says nothing about the bill, and the plan says it is incomplete: %+v %+v", plan.Matches, plan.Readiness)
	}
	decide(map[string]any{"action": "paid", "obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "previous_id": lapse.ID}, 409)
	// A capture taken after the decision, reading every open item of the bill's month, shows it left them.
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,payload) SELECT close_id,connection_id,period,requested_by,state,now(),
 jsonb_set(payload,'{finished_at}',to_jsonb(to_char((now()+interval '1 minute') AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS"Z"'))) FROM quickbooks_entity_syncs WHERE close_id=$1 ORDER BY id DESC LIMIT 1`, closeID)
	must(err)
	plan.Matches = nil
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	asksPaid := false
	for _, g := range plan.Readiness.Unresolved {
		asksPaid = asksPaid || g.Kind == "decision" && strings.Contains(g.Text, "confirm whether it was paid")
	}
	if len(plan.Matches) != 1 || !plan.Matches[0].Bills[0].Confirm || plan.Matches[0].Counted != "1500.00" || !asksPaid {
		t.Fatalf("a bill that left the open items settles nothing until confirmed paid: %+v %+v", plan.Matches, plan.Readiness)
	}
	must(json.Unmarshal(decide(map[string]any{"action": "paid", "obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "previous_id": lapse.ID}, 200), &lapse))
	plan.Matches = nil
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if len(plan.Matches) != 1 || plan.Matches[0].Bills[0].State != "paid" || plan.Matches[0].Counted != "0.00" {
		t.Fatalf("confirmed paid, the bill settles the rent: %+v", plan.Matches)
	}
	decide(map[string]any{"action": "clear", "obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "previous_id": lapse.ID}, 409)
	// Correcting a paid bill's relation keeps its payment: as a part of 1,000.00 it leaves a 500.00 residual, not 1,500.00.
	must(json.Unmarshal(decide(map[string]any{"obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "relation": "part", "amount": "1000.00", "previous_id": lapse.ID}, 200), &lapse))
	plan.Matches = nil
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	if len(plan.Matches) != 1 || plan.Matches[0].Bills[0].State != "paid" || plan.Matches[0].Counted != "500.00" {
		t.Fatalf("a corrected relation keeps the confirmed payment: %+v", plan.Matches)
	}
	_, err = db.Exec(`UPDATE cash_obligations SET currency='EUR' WHERE id::text=$1`, newRent)
	must(err)
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	lapsed := false
	for _, g := range plan.Readiness.Unresolved {
		lapsed = lapsed || g.Kind == "decision" && strings.Contains(g.Text, "no longer applies (2026-10-07)")
	}
	if !lapsed || len(plan.Matches) != 0 {
		t.Fatalf("a decision for a date that no longer counts is named, not applied: %+v", plan.Readiness)
	}
	decide(map[string]any{"action": "clear", "obligation_id": newRent, "on": "2026-10-07", "document": "bill:9", "previous_id": lapse.ID}, 200)
	plan.Readiness.Unresolved = nil
	must(json.Unmarshal(call("GET", "/cash-plan", nil, 200), &plan))
	for _, g := range plan.Readiness.Unresolved {
		if strings.Contains(g.Text, "no longer applies") {
			t.Fatalf("a cleared lapsed decision is no longer named: %+v", plan.Readiness)
		}
	}
	// The handoff changed the plan: the review is kept and says so. A capture three days later is compared with what
	// the reviewed plan expected that day: 5,000 opening less the rent bill and payroll on the 7th is -500.00.
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) SELECT close_id,connection_id,realm,'later',payload,now(),captured_at+interval '3 days','fixture' FROM quickbooks_snapshots WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID)
	must(err)
	must(json.Unmarshal(call("GET", "/cash-plan/reviews", nil, 200), &reviews))
	if r := reviews.Reviews[0]; r.State != "inputs_changed" || len(r.Actuals) != 1 || r.Actuals[0]["on"] != "2026-10-08" || r.Actuals[0]["forecast"] != "-500.00" || r.Actuals[0]["actual"] != "5000.00" || r.Actuals[0]["difference"] != "5500.00" {
		t.Fatalf("a changed plan leaves its review flagged, and later book cash is compared with the reviewed forecast: %+v", reviews)
	}
	// A capture whose home-currency bank balance cannot be read states no actual, rather than a zero.
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) SELECT close_id,connection_id,realm,'unreadable',jsonb_set(payload,'{accounts}','[{"Id":"1","Name":"Checking","AccountType":"Bank","CurrentBalance":null}]'),now(),captured_at+interval '1 day','fixture' FROM quickbooks_snapshots WHERE close_id::text=$1 ORDER BY id DESC LIMIT 1`, closeID)
	must(err)
	var unreadable struct {
		Reviews []struct {
			Actuals []struct {
				Actual     string
				Unreadable []string
			}
		}
	}
	must(json.Unmarshal(call("GET", "/cash-plan/reviews", nil, 200), &unreadable))
	if a := unreadable.Reviews[0].Actuals; len(a) != 2 || a[1].Actual != "" || len(a[1].Unreadable) != 1 || a[1].Unreadable[0] != "Checking" {
		t.Fatalf("an unreadable bank balance is named, not counted as zero: %+v", a)
	}
}

func TestScenariosMoveOpenItemsAndAddPaymentsWithoutTouchingTheBase(t *testing.T) {
	start := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	item := func(key, on, amount string) cashItem {
		v, _ := new(big.Rat).SetString(amount)
		return cashItem{Key: key, On: on, Amount: amount, value: v}
	}
	base := []cashItem{item("invoice:3", "2026-10-14", "600.00"), item("bill:9", "2026-10-07", "-1500.00")}
	out, missing := applyScenario(base, "Hire", []cashChange{{Kind: "move", Item: "invoice:3", On: "2026-11-04"}, {Kind: "add", Label: "Engineer", Direction: "out", Amount: "3000.00", FirstOn: "2026-10-16", Every: "two_weeks", UntilOn: "2026-10-31"}, {Kind: "move", Item: "bill:1", On: "2026-10-20"}}, start, start.AddDate(0, 0, 91))
	if base[0].On != "2026-10-14" || out[0].On != "2026-11-04" || out[0].Basis != "scenario: Hire" || len(out) != 4 || out[2].On != "2026-10-16" || out[2].Amount != "-3000.00" || out[3].On != "2026-10-30" || len(missing) != 1 || missing[0] != "bill:1" {
		t.Fatalf("a scenario moves a copy of the item, adds the payroll on each date up to its end and names a move it cannot make: %+v %v", out, missing)
	}
	for _, c := range []cashChange{{Kind: "move", Item: "obligation:2", On: "2026-10-20"}, {Kind: "add", Label: "Hire", Direction: "out", Amount: "0.00", FirstOn: "2026-10-16", Every: "once"}, {Kind: "add", Label: "Hire", Direction: "sideways", Amount: "10.00", FirstOn: "2026-10-16", Every: "once"}, {Kind: "delete"}} {
		if c.valid() {
			t.Fatalf("an invalid change is refused: %+v", c)
		}
	}
}
