package transport

import (
	"bytes"
	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"github.com/golang-jwt/jwt/v5"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func nativeCodingFixture() (preparation.Procedure, *preparation.Ledger) {
	p := preparation.Procedure{ID: "7", Kind: "coding", ClientEntity: "Example", Enabled: true, Instructions: "QuickBooks company: 123\nAutomatic native correction candidates: true\nHome currency: USD\nBill vendor 21 source account: 8\nBill vendor 21 target account: 7\nBill vendor 21 maximum line amount: 150.00\nBill vendor 21 rule reference: Accountant memo 4\nPurchase vendor 12 source account: 8\nPurchase vendor 12 target account: 7\nPurchase vendor 12 maximum line amount: 50.00\nPurchase vendor 12 rule reference: Accountant memo 5\nDeposit customer 17 source account: 11\nDeposit customer 17 target account: 12\nDeposit customer 17 maximum line amount: 500.00\nDeposit customer 17 rule reference: Accountant memo 6"}
	l := &preparation.Ledger{Capture: "5", Source: preparation.Source{ID: "1", Digest: "capture"}, Chart: "2", ChartSource: preparation.Source{ID: "3", Digest: "chart"}, Realm: "123", CaptureRealm: "123", ChartRealm: "123", ReportPeriod: "2026-09", Accounts: map[string]string{"7": "Office Supplies", "8": "Uncategorized Expense", "11": "Uncategorized Income", "12": "Subscription Revenue"}, Types: map[string]string{"7": "Expense", "8": "Expense", "11": "Income", "12": "Income"}}
	l.AccountCatalog = json.RawMessage(`[{"Id":"7","Name":"Office Supplies","AccountType":"Expense","Active":true},{"Id":"8","Name":"Uncategorized Expense","AccountType":"Expense","Active":true},{"Id":"11","Name":"Uncategorized Income","AccountType":"Income","Active":true},{"Id":"12","Name":"Subscription Revenue","AccountType":"Income","Active":true}]`)
	for _, test := range []struct{ kind, id, target, record string }{
		{"Bill", "41", "7", `{"Id":"41","SyncToken":"3","TxnDate":"2026-09-15","VendorRef":{"value":"21","name":"Acme"},"CurrencyRef":{"value":"USD"},"TotalAmt":100,"Balance":100,"Line":[{"Id":"1","Amount":100,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"8"}}}]}`},
		{"Purchase", "42", "7", `{"Id":"42","SyncToken":"6","TxnDate":"2026-09-15","PaymentType":"Cash","AccountRef":{"value":"35"},"EntityRef":{"value":"12","name":"Staples","type":"Vendor"},"CurrencyRef":{"value":"USD"},"TotalAmt":25,"Line":[{"Id":"1","Amount":25,"DetailType":"AccountBasedExpenseLineDetail","AccountBasedExpenseLineDetail":{"AccountRef":{"value":"8"}}}]}`},
		{"Deposit", "43", "12", `{"Id":"43","SyncToken":"2","TxnDate":"2026-09-15","DepositToAccountRef":{"value":"35"},"CurrencyRef":{"value":"USD"},"TotalAmt":300,"Line":[{"Id":"1","Amount":300,"DetailType":"DepositLineDetail","DepositLineDetail":{"AccountRef":{"value":"11"},"Entity":{"value":"17","type":"Customer","name":"Northwind"}}}]}`},
	} {
		l.Rows = append(l.Rows, preparation.LedgerRow{Kind: test.kind, ProviderID: test.id, Date: "2026-09-15", Currency: "USD", Record: json.RawMessage(test.record)})
	}
	return p, l
}

func TestNativeCodingCandidatesFollowTheApprovedCapturedRule(t *testing.T) {
	p, l := nativeCodingFixture()
	source := preparation.Source{ID: "8", Digest: "approved-policy"}
	for _, row := range l.Rows {
		t.Run(row.Kind, func(t *testing.T) {
			candidate, reason := nativeCodingCandidate(row, l, p, source, "Example", "2026-09", "USD")
			target := "7"
			if row.Kind == "Deposit" {
				target = "12"
			}
			if reason != "" || candidate.State != "candidate" || candidate.BillID != row.ProviderID || candidate.TargetAccountID != target || candidate.TargetLineID != "1" || candidate.GrantID != "" || candidate.RuleReference == "" {
				t.Fatalf("native candidate: %+v %s", candidate, reason)
			}
			other := p
			other.Instructions = strings.Replace(p.Instructions, "QuickBooks company: 123", "QuickBooks company: 999", 1)
			if _, why := nativeCodingCandidate(row, l, other, source, "Example", "2026-09", "USD"); !strings.Contains(why, "company") {
				t.Fatalf("wrong company not held: %s", why)
			}
		})
	}
}

func TestNativeCodingGoalPreparesAndPreservesHumanDisposition(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	org := "org_Native" + rand.Text()
	asFirm(t, map[string]string{"user_native": "partner"}, org)
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Native fixture')`, org)
	must(err)
	var closeID, conn, syncID, policyID string
	defer func() {
		for _, table := range []string{"bill_corrections", "close_goals", "close_preparations", "quickbooks_snapshots", "quickbooks_entity_syncs", "quickbooks_connections", "evidence"} {
			db.Exec(`DELETE FROM `+table+` WHERE close_id::text=$1`, closeID)
		}
		db.Exec(`DELETE FROM client_closes WHERE id::text=$1`, closeID)
		for _, table := range []string{"procedure_versions", "client_access", "organization_member_access", "workflow_runs"} {
			db.Exec(`DELETE FROM `+table+` WHERE organization_id=$1`, org)
		}
		db.Exec(`DELETE FROM organizations WHERE id=$1`, org)
	}()
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_native',true,'Synthetic','fixture')`, org)
	must(err)
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Example','2026-09') RETURNING id::text`, org).Scan(&closeID))
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,home_currency,fiscal_year_start,environment) VALUES($1,$3,'Example','fixture','active',$2,now(),'123','USD',1,'production') RETURNING id::text`, closeID, rand.Text(), org).Scan(&conn))
	insert := func(name, body string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by,extraction_state) VALUES($1,$2,$3,$4,'fixture','ready') RETURNING id::text`, closeID, name, preparation.Digest([]byte(body)), body).Scan(&id))
		return id
	}
	capture := insert("QuickBooks transactions", "Captured native records")
	chart := insert("QuickBooks accounts", "Captured account catalog")
	must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 hour',now()-interval '1 hour','{"realm":"123"}') RETURNING id::text`, closeID, conn, capture).Scan(&syncID))
	policy, books := nativeCodingFixture()
	var multi map[string]any
	must(json.Unmarshal(books.Rows[2].Record, &multi))
	multi["Id"] = "44"
	multi["TotalAmt"] = 600
	lines := multi["Line"].([]any)
	second := map[string]any{}
	for k, v := range lines[0].(map[string]any) {
		second[k] = v
	}
	second["Id"] = "2"
	multi["Line"] = append(lines, second)
	rawMulti, _ := json.Marshal(multi)
	books.Rows = append(books.Rows, preparation.LedgerRow{Kind: "Deposit", ProviderID: "44", Date: "2026-09-15", Currency: "USD", Record: rawMulti})
	captureJSON := func(records []preparation.LedgerRow) string {
		byKind := map[string][]json.RawMessage{}
		for _, row := range records {
			byKind[row.Kind] = append(byKind[row.Kind], row.Record)
		}
		raw, _ := json.Marshal(map[string]any{"realm": "123", "records": byKind})
		return string(raw)
	}
	_, err = db.Exec(`UPDATE quickbooks_entity_syncs SET payload=$2 WHERE id::text=$1`, syncID, captureJSON(books.Rows))
	must(err)

	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": chart, "accounts": books.AccountCatalog, "reports": map[string]any{}})
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, conn, rand.Text(), string(payload))
	must(err)
	for _, row := range books.Rows {
		_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,$2,$3,'1',$4,now(),'USD',($5::jsonb->>'TotalAmt')::numeric,$5)`, syncID, row.Kind, row.ProviderID, row.Date, string(row.Record))
		must(err)
	}
	must(db.QueryRow(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) VALUES($1,'Example','coding',$2,true,'Synthetic accountant rule','fixture') RETURNING id::text`, org, policy.Instructions).Scan(&policyID))
	actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "user_native"}, Organization: org, Permissions: []string{"clients:all", "workpapers:review", "procedures:manage"}}
	h := savedCloses(db, strings.Repeat("a", 32))
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), actor))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	status := func() goalProgress {
		t.Helper()
		var result struct {
			Progress goalProgress `json:"progress"`
		}
		must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/goal", nil, 200), &result))
		return result.Progress
	}
	call("POST", "/v1/closes/"+closeID+"/goal", map[string]any{"confirmed": true}, 200)
	if p := status(); p.CodingCount != 0 || !strings.Contains(strings.Join(p.Needs, " "), "Apply the current approved client coding procedure") {
		t.Fatalf("missing apply is not a precise firm decision: %+v", p)
	}
	var before procedureImpact
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/procedures/impact?kind=coding", nil, 200), &before))
	call("POST", "/v1/closes/"+closeID+"/procedures/apply", map[string]any{"kind": "coding", "version_id": policyID, "impact_digest": before.ImpactDigest, "confirmed": true}, 200)
	sweepCloseGoals(context.Background(), db)
	p := status()
	if p.CodingCount != 3 || len(p.Coding) != 3 || len(p.Items) != 0 {
		t.Fatalf("browser-closed goal did not prepare native candidates: %+v", p)
	}
	if !strings.Contains(strings.Join(p.Needs, " "), "Deposit #44") {
		t.Fatal("mapped multi-line deposit lost its ambiguity question")
	}
	if p.Ready {
		t.Fatal("native activity incorrectly satisfies document readiness")
	}
	raw, _ := json.Marshal(p.Plan)
	if !bytes.Contains(raw, []byte("#correct-bills-41")) {
		t.Fatalf("missing review deep link: %s", raw)
	}
	var impact procedureImpact
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/procedures/impact?kind=coding", nil, 200), &impact))
	var native []map[string]any
	must(json.Unmarshal(impact.Native, &native))
	if len(native) != 3 || string(impact.Affected) != "[]" {
		t.Fatalf("native impact confused records with workpapers: %+v", impact)
	}
	var count, attempts, history int
	must(db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE attempted_at IS NOT NULL),sum(jsonb_array_length(candidate_reviews)) FROM bill_corrections WHERE close_id=$1`, closeID).Scan(&count, &attempts, &history))
	if count != 3 || attempts != 0 || history != 3 {
		t.Fatalf("candidate side effects: %d %d %d", count, attempts, history)
	}
	sweepCloseGoals(context.Background(), db)
	must(db.QueryRow(`SELECT sum(jsonb_array_length(candidate_reviews)) FROM bill_corrections WHERE close_id=$1`, closeID).Scan(&history))
	if history != 3 {
		t.Fatal("replay duplicated preparation")
	}
	tx, err := db.Begin()
	must(err)
	ready, err := closeReadiness(t.Context(), tx, closeID)
	tx.Rollback()
	must(err)
	if !strings.Contains(strings.Join(ready.Blockers, " "), "Bill #41 coding candidate needs review") {
		t.Fatalf("candidate missing from sign-off: %+v", ready)
	}
	item, err := readBillCorrection(t.Context(), db, closeID, "Bill", "41")
	must(err)
	path := "/v1/closes/" + closeID + "/quickbooks/bills/41/candidate-review"
	call("POST", path, map[string]any{"digest": "old", "reason": "Rule does not apply to this service", "confirmed": true}, 409)
	actor.Permissions = []string{"clients:all"}
	call("POST", path, map[string]any{"digest": item.Digest, "reason": "Rule does not apply to this service", "confirmed": true}, 403)
	actor.Permissions = []string{"clients:all", "workpapers:review"}
	call("POST", path, map[string]any{"digest": item.Digest, "reason": "Rule does not apply to this service", "confirmed": true}, 200)
	sweepCloseGoals(context.Background(), db)
	item, err = readBillCorrection(t.Context(), db, closeID, "Bill", "41")
	must(err)
	if item.State != "dismissed" || !bytes.Contains(item.CandidateReviews, []byte("user_native")) {
		t.Fatalf("human disposition lost: %+v", item)
	}
	if status().CodingCount != 2 {
		t.Fatal("dismissed candidate was recreated")
	}
	var changed procedureImpact
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/procedures/impact?kind=coding", nil, 200), &changed))
	if changed.ImpactDigest == impact.ImpactDigest {
		t.Fatal("policy impact ignored native review")
	}
	// Source changes must withdraw obsolete proposals, and later eligibility may restore them.
	recapture := func(records []preparation.LedgerRow) {
		t.Helper()
		capture = insert("QuickBooks refreshed transactions", rand.Text())
		must(db.QueryRow(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,evidence_id,started_at,finished_at,payload) VALUES($1,$2,'2026-09','fixture','completed',$3,now()-interval '1 second',now(),'{"realm":"123"}') RETURNING id::text`, closeID, conn, capture).Scan(&syncID))
		_, err = db.Exec(`UPDATE quickbooks_entity_syncs SET payload=$2 WHERE id::text=$1`, syncID, captureJSON(records))
		must(err)
		_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),now(),'fixture')`, closeID, conn, rand.Text(), string(payload))
		must(err)
		for _, row := range records {
			_, err = db.Exec(`INSERT INTO quickbooks_transaction_rows(sync_id,kind,provider_id,revision,txn_date,provider_updated_at,currency,amount,record) VALUES($1,$2,$3,'2',$4,now(),'USD',($5::jsonb->>'TotalAmt')::numeric,$5)`, syncID, row.Kind, row.ProviderID, row.Date, string(row.Record))
			must(err)
		}
	}
	changedRecords := append([]preparation.LedgerRow{}, books.Rows...)
	changedRecords[1].Record = json.RawMessage(strings.ReplaceAll(string(changedRecords[1].Record), ":25", ":75"))
	changedRecords[2].Record = json.RawMessage(strings.ReplaceAll(string(changedRecords[2].Record), `"value":"11"`, `"value":"12"`))
	recapture(changedRecords)
	sweepCloseGoals(context.Background(), db)
	for _, pair := range [][2]string{{"Purchase", "42"}, {"Deposit", "43"}} {
		item, err = readBillCorrection(t.Context(), db, closeID, pair[0], pair[1])
		must(err)
		if item.State != "withdrawn" || !bytes.Contains(item.CandidateReviews, []byte("withdrawn")) {
			t.Fatalf("obsolete proposal survived changed capture: %+v", item)
		}
	}
	if status().CodingCount != 0 {
		t.Fatal("withdrawn proposals remain review candidates")
	}
	var refreshed struct {
		State  string
		Before struct {
			Total json.Number `json:"TotalAmt"`
		} `json:"before_bill"`
	}
	must(json.Unmarshal(call("GET", "/v1/closes/"+closeID+"/quickbooks/purchases/42/correction", nil, 200), &refreshed))
	if refreshed.State != "not_prepared" || refreshed.Before.Total.String() != "75" {
		t.Fatalf("withdrawn record did not open current capture: %+v", refreshed)
	}

	recapture(books.Rows)
	sweepCloseGoals(context.Background(), db)
	if status().CodingCount != 2 {
		t.Fatal("restored eligibility did not re-prepare withdrawn candidates, or recreated a dismissal")
	}
	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) VALUES($1,'Example','coding',$2,true,'Stop automatic coding','fixture')`, org, strings.Replace(policy.Instructions, "Automatic native correction candidates: true", "Automatic native correction candidates: false", 1))
	must(err)
	sweepCloseGoals(context.Background(), db)
	if status().CodingCount != 0 {
		t.Fatal("rule-off left pending native candidates")
	}
	item, err = readBillCorrection(t.Context(), db, closeID, "Deposit", "43")
	must(err)
	if item.State != "withdrawn" {
		t.Fatalf("rule-off proposal: %+v", item)
	}

}

func TestNativeCandidatesDoNotReplaceWorkpaperApproval(t *testing.T) {
	p := goalProgress{approve: 2, Items: []goalItem{{Status: "needs_you"}, {Status: "needs_you"}}, CodingCount: 1, Coding: []nativeCodingReview{{ID: "1", Entity: "Bill", BillID: "41", Target: "Office Supplies", Rule: "Memo"}}}
	plan := goalPlan(p, booksRead{}, "1", "Example", "2026-09", "signoff")
	for _, step := range plan {
		if step.Key == "review" {
			if step.Title != "Approve every draft" || !strings.Contains(step.Text, "2 documents") {
				t.Fatalf("native candidates hide workpaper approvals: %+v", step)
			}
			return
		}
	}
	t.Fatal("missing workpaper review step")
}
