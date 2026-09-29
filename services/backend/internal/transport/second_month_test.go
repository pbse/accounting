package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

// TestSecondMonthCarriesOrdinaryAndChangedSchedules is the month-two proof of docs/maturity.md: signing off
// September carries its reviewed schedules into October, the worker prepares them from the prior schedule, the
// standing approvals approve the unchanged drafts, and the October Trial Balance then revises each schedule:
// the one the ledger agrees with proposes nothing, the one it differs from proposes the adjustment. Both
// revisions wait for a person; the standing approvals never touch them.
func TestSecondMonthCarriesOrdinaryAndChangedSchedules(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	entity := "Second month " + rand.Text()
	var september string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&september))
	defer func() {
		for _, query := range []string{
			`DELETE FROM standing_approvals WHERE client_entity=$1`,
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM close_scopes WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM quickbooks_connections WHERE client_entity=$1`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.entity=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`UPDATE evidence SET superseded_by=NULL WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM client_closes WHERE entity=$1`,
		} {
			if _, e := db.Exec(query, entity); e != nil {
				t.Error(e)
			}
		}
	}()
	token := strings.Repeat("m", 32)
	h := apiWithStore(db, token)
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	// Two reviewed September schedules, each with its invoice and a standing approval.
	schedule := func(reference, amount, end, expense, prepaid string) string {
		t.Helper()
		var source, digest string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,'Synthetic invoice','fixture') RETURNING id::text,digest`, september, reference+".txt", rand.Text()).Scan(&source, &digest))
		input := prepaids.Input{Entity: entity, SourceReference: reference, Amount: amount, ServiceStart: "2026-09-01", ServiceEnd: end, ClosePeriod: "2026-09", Currency: "USD", Basis: "accrual", ExpenseAccount: expense, PrepaidAccount: prepaid, Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
		raw, _ := json.Marshal(input)
		body, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: raw, Sources: []preparation.Source{{ID: source, Digest: digest, Name: reference + ".txt", Role: "invoice", Location: "line 1"}}}, entity, "2026-09")
		must(err)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, september, preparation.Digest(body), string(body)).Scan(&id))
		call("POST", "/v1/closes/"+september+"/workpapers/"+id+"/reviews", map[string]any{"decision": "approved", "note": "Reviewed the policy and the schedule", "digest": preparation.Digest(body)}, 200)
		call("POST", "/v1/closes/"+september+"/workpapers/"+id+"/standing-approval", map[string]any{"action": "grant", "reason": "Fixed-term policy, straight line", "confirmed": true}, 200)
		return id
	}
	insurance := schedule("Allianz policy", "120.00", "2026-11-30", "Insurance expense", "Prepaid insurance")
	rent := schedule("Office lease", "240.00", "2027-02-28", "Rent expense", "Prepaid rent")

	// Signing off September starts October: both schedules are queued for next-month preparation.
	var state readiness
	must(json.Unmarshal(call("GET", "/v1/closes/"+september+"/readiness", nil, 200), &state))
	call("POST", "/v1/closes/"+september+"/scope", map[string]any{"digest": state.Digest, "scope": closeScope{Description: "Prepaid schedules only.", RequiredPapers: []string{insurance, rent}, ExternalSteps: []externalStep{}}}, 200)
	must(json.Unmarshal(call("GET", "/v1/closes/"+september+"/readiness", nil, 200), &state))
	if len(state.Blockers) != 0 {
		t.Fatalf("September is ready to sign: %v", state.Blockers)
	}
	var signed struct {
		Carried    int    `json:"carried"`
		CarryError string `json:"carry_error"`
	}
	must(json.Unmarshal(call("POST", "/v1/closes/"+september+"/signoff", map[string]any{"digest": state.Digest, "expected_decision_id": state.DecisionID, "action": "sign", "note": "September closed", "confirmed": true}, 200), &signed))
	if signed.Carried != 2 || signed.CarryError != "" {
		t.Fatalf("sign-off carries both reviewed schedules into October: %+v", signed)
	}
	var october string
	must(db.QueryRow(`SELECT id::text FROM client_closes WHERE entity=$1 AND period='2026-10'`, entity).Scan(&october))
	for worked := true; worked; {
		worked, err = preparation.ProcessOne(ctx, db)
		must(err)
	}
	carried := func(predecessor string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`SELECT id::text FROM workpapers WHERE close_id=$1 AND paper->'carry_forward'->>'paper_id'=$2`, october, predecessor).Scan(&id))
		return id
	}
	octoberInsurance, octoberRent := carried(insurance), carried(rent)
	var scheduled string
	must(db.QueryRow(`SELECT r->>'remaining' FROM workpapers, jsonb_array_elements(paper->'schedule') r WHERE id=$1 AND r->>'period'='2026-10'`, octoberInsurance).Scan(&scheduled))
	if scheduled != "40.00" {
		t.Fatalf("October continues the prior schedule: closing balance %s", scheduled)
	}

	// The ordinary month: the unchanged carried drafts are approved under the standing approvals.
	if _, approved, err := sweepStandingApprovals(ctx, db, "0"); err != nil || approved != 2 {
		t.Fatalf("both carried October drafts are approved automatically: %d %v", approved, err)
	}
	var actor, note string
	must(db.QueryRow(`SELECT actor,note FROM workpaper_reviews WHERE workpaper_id=$1 ORDER BY id DESC LIMIT 1`, octoberInsurance).Scan(&actor, &note))
	var granter string
	must(db.QueryRow(`SELECT granted_by FROM standing_approvals WHERE workpaper_id=$1`, insurance).Scan(&granter))
	if actor != granter || !strings.Contains(note, "Standing approval #") {
		t.Fatalf("the approval is the granter's and names the standing approval: %s %s", actor, note)
	}

	// October's Trial Balance arrives: insurance agrees with the schedule, rent does not.
	var connection string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) SELECT $1,organization_id,entity,'fixture','active',$2,now()+interval '1 day','123','Synthetic' FROM client_closes WHERE id=$1 RETURNING id::text`, october, rand.Text()).Scan(&connection))
	var capture string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'trial-balance.txt',$2,'Synthetic period report','fixture') RETURNING id::text`, october, rand.Text()).Scan(&capture))
	row := func(id, name, debit string) map[string]any {
		return map[string]any{"type": "Data", "ColData": []map[string]string{{"id": id, "value": name}, {"value": debit}, {"value": ""}}}
	}
	payload, _ := json.Marshal(map[string]any{"period": "2026-10", "source_id": capture,
		"accounts": []map[string]string{{"Id": "7", "Name": "Prepaid insurance", "AccountSubType": "PrepaidExpenses"}, {"Id": "9", "Name": "Prepaid rent", "AccountSubType": "PrepaidExpenses"}},
		"reports":  map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD", "ReportBasis": "Accrual"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "Account"}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}}, "Rows": map[string]any{"Row": []map[string]any{row("7", "Prepaid insurance", "40.00"), row("9", "Prepaid rent", "150.00")}}}}})
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, october, connection, rand.Text(), string(payload))
	must(err)
	if _, queued, err := sweepComparisons(ctx, db, "0"); err != nil || queued != 2 {
		t.Fatalf("the agent compares both schedules with the Trial Balance: %d %v", queued, err)
	}
	for worked := true; worked; {
		worked, err = preparation.ProcessOne(ctx, db)
		must(err)
	}
	revision := func(predecessor string) (proposal *prepaids.Adjustment, state string) {
		t.Helper()
		var raw []byte
		must(db.QueryRow(`SELECT paper,review_state FROM workpaper_review_state WHERE close_id=$1 AND paper->>'revises'=$2`, october, predecessor).Scan(&raw, &state))
		var paper prepaids.Workpaper
		must(json.Unmarshal(raw, &paper))
		return paper.Proposal, state
	}
	if proposal, state := revision(octoberInsurance); proposal != nil || state != "pending" {
		t.Fatalf("the schedule the ledger agrees with proposes nothing and waits for a person to confirm the balance: %+v %s", proposal, state)
	}
	if proposal, state := revision(octoberRent); proposal == nil || proposal.Amount != "10.00" || state != "pending" {
		t.Fatalf("the schedule the ledger differs from proposes the difference for a person: %+v %s", proposal, state)
	}
	if _, approved, err := sweepStandingApprovals(ctx, db, "0"); err != nil || approved != 0 {
		t.Fatalf("a standing approval never approves a comparison, even one with no adjustment: %d %v", approved, err)
	}
	var stale string
	must(db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id=$1`, octoberRent).Scan(&stale))
	if stale == "approved" {
		t.Fatalf("the revised carried draft no longer stands as the current approval: %s", stale)
	}
}
