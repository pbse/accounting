package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

func TestCarryForwardRoundingLineageAndFreshReview(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Carry fixture " + rand.Text()
	var origin string
	if err = db.QueryRow("INSERT INTO client_closes(entity,period) VALUES($1,'2026-12') RETURNING id::text", entity).Scan(&origin); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"close_signoffs", "preparation_jobs", "workpapers", "evidence", "close_scopes"} {
			if _, e := db.Exec("DELETE FROM "+table+" WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)", entity); e != nil {
				t.Error(e)
			}
		}
		if _, e := db.Exec("DELETE FROM client_closes WHERE entity=$1", entity); e != nil {
			t.Error(e)
		}
		if _, e := db.Exec("DELETE FROM procedure_versions WHERE lower(client_entity)=lower($1)", entity); e != nil {
			t.Error(e)
		}
	}()
	sources := []preparation.Source{}
	for _, name := range []string{"invoice.txt", "mapping.txt"} {
		var id string
		digest := preparation.Digest([]byte(name))
		if err = db.QueryRow("INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$2,'fixture') RETURNING id::text", origin, name, digest).Scan(&id); err != nil {
			t.Fatal(err)
		}
		role := "support"
		if name == "invoice.txt" {
			role = "invoice"
		}
		sources = append(sources, preparation.Source{ID: id, Digest: digest, Name: name, Role: role, Location: "line 1"})
	}
	input := prepaids.Input{Entity: entity, SourceReference: "Synthetic annual terms", Amount: "100.01", ServiceStart: "2026-12-01", ServiceEnd: "2027-02-28", ClosePeriod: "2026-12", Currency: "USD", Basis: "accrual", ExpenseAccount: "Insurance", PrepaidAccount: "Prepaid insurance", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, BookedBalance: "100.01", BookingReference: "December ledger"}
	raw, _ := json.Marshal(input)
	body, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: raw, Sources: sources}, entity, "2026-12")
	if err != nil {
		t.Fatal(err)
	}
	var first string
	if err = db.QueryRow("INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text", origin, preparation.Digest(body), string(body)).Scan(&first); err != nil {
		t.Fatal(err)
	}
	token := "carry-test-workspace-token-000000000"
	h := apiWithStore(db, token)
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		b, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	preview := func(id string) []carryItem {
		t.Helper()
		var result struct {
			Items []carryItem `json:"items"`
		}
		json.Unmarshal(call("GET", "/v1/closes/"+id+"/carry-forward", nil, 200), &result)
		return result.Items
	}
	approve := func(closeID, id, decision string) {
		var digest, version string
		if e := db.QueryRow("SELECT digest,review_id FROM workpaper_review_state WHERE id=$1", id).Scan(&digest, &version); e != nil {
			t.Fatal(e)
		}
		call("POST", "/v1/closes/"+closeID+"/workpapers/"+id+"/reviews", map[string]any{"decision": decision, "note": "Synthetic review; schedule only", "digest": digest, "expected_review_id": version}, 200)
	}
	if preview(origin)[0].Eligible {
		t.Fatal("Unreviewed schedule eligible")
	}
	var pendingPreview struct {
		Unresolved []unresolvedCarryItem `json:"unresolved"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+origin+"/carry-forward", nil, 200), &pendingPreview)
	if len(pendingPreview.Unresolved) != 1 || pendingPreview.Unresolved[0].Kind != "draft" || pendingPreview.Unresolved[0].ID != first {
		t.Fatal("Unreviewed draft absent from carry-over preview")
	}
	approve(origin, first, "approved")
	selected := preview(origin)
	// Signing off the month carries its reviewed schedules into the next month without another step.
	var ready readiness
	json.Unmarshal(call("GET", "/v1/closes/"+origin+"/readiness", nil, 200), &ready)
	call("POST", "/v1/closes/"+origin+"/scope", map[string]any{"digest": ready.Digest, "scope": closeScope{Description: "Prepaids only", RequiredPapers: []string{first}}}, 200)
	json.Unmarshal(call("GET", "/v1/closes/"+origin+"/readiness", nil, 200), &ready)
	var signed struct {
		Carried int `json:"carried"`
	}
	json.Unmarshal(call("POST", "/v1/closes/"+origin+"/signoff", map[string]any{"digest": ready.Digest, "expected_decision_id": ready.DecisionID, "action": "sign", "note": "Synthetic month end", "confirmed": true}, 200), &signed)
	if signed.Carried != 1 || preview(origin)[0].JobID == "" {
		t.Fatalf("sign-off must carry the reviewed schedule: %+v", signed)
	}
	apply := func(closeID string, items []carryItem) (string, string) {
		t.Helper()
		var result struct {
			CloseID string   `json:"close_id"`
			Jobs    []string `json:"job_ids"`
		}
		json.Unmarshal(call("POST", "/v1/closes/"+closeID+"/carry-forward", map[string]any{"confirmed": true, "items": items}, 200), &result)
		if len(result.Jobs) != 1 {
			t.Fatal("Missing durable job")
		}
		return result.CloseID, result.Jobs[0]
	}
	january, job := apply(origin, selected)
	var procedureID string
	if err = db.QueryRow("INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,enabled,reason,actor) SELECT organization_id,entity,'prepaid','Synthetic current procedure',true,'Synthetic approval','fixture' FROM client_closes WHERE id=$1 RETURNING id::text", origin).Scan(&procedureID); err != nil {
		t.Fatal(err)
	}
	var procedurePreview struct {
		Procedures []preparation.Procedure `json:"procedures"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+origin+"/carry-forward", nil, 200), &procedurePreview)
	if len(procedurePreview.Procedures) == 0 || procedurePreview.Procedures[0].ID != procedureID {
		t.Fatal("Current approved procedure absent from preview")
	}
	call("POST", "/v1/closes/"+origin+"/carry-forward", map[string]any{"confirmed": true, "items": selected, "procedure_ids": []string{procedureID}}, 422)
	call("POST", "/v1/closes/"+origin+"/carry-forward", map[string]any{"confirmed": true, "procedure_ids": []string{procedureID}}, 200)
	call("POST", "/v1/closes/"+origin+"/carry-forward", map[string]any{"confirmed": true, "procedure_ids": []string{procedureID}}, 200)
	var applied int
	if err = db.QueryRow("SELECT count(*) FROM evidence WHERE close_id=$1 AND procedure_id::text=$2 AND superseded_by IS NULL", january, procedureID).Scan(&applied); err != nil || applied != 1 {
		t.Fatal("Procedure not applied exactly once", applied, err)
	}
	var unresolvedJob string
	if err = db.QueryRow("INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','failed','fixture') RETURNING id::text", origin, "unresolved:"+rand.Text()).Scan(&unresolvedJob); err != nil {
		t.Fatal(err)
	}
	var unresolvedPreview struct {
		Items  []unresolvedCarryItem `json:"unresolved"`
		Digest string                `json:"unresolved_digest"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+origin+"/carry-forward", nil, 200), &unresolvedPreview)
	if len(unresolvedPreview.Items) != 1 || unresolvedPreview.Items[0].ID != unresolvedJob {
		t.Fatal("Unresolved source work missing from preview", unresolvedPreview.Items)
	}
	carryTasks := map[string]any{"confirmed": true, "carry_unresolved": true, "unresolved_digest": unresolvedPreview.Digest}
	call("POST", "/v1/closes/"+origin+"/carry-forward", carryTasks, 200)
	call("POST", "/v1/closes/"+origin+"/carry-forward", carryTasks, 200)
	var scopeVersions int
	var scopeRaw []byte
	if err = db.QueryRow("SELECT count(*) FROM close_scopes WHERE close_id=$1", january).Scan(&scopeVersions); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT scope FROM close_scopes WHERE close_id=$1 ORDER BY id DESC LIMIT 1", january).Scan(&scopeRaw); err != nil {
		t.Fatal(err)
	}
	var scope closeScope
	if json.Unmarshal(scopeRaw, &scope) != nil || scopeVersions != 1 || len(scope.ExternalSteps) != 1 || scope.ExternalSteps[0].Complete {
		t.Fatal("Carried unresolved work was duplicated or marked complete", scopeVersions, scope)
	}
	var readiness struct {
		Blockers []string `json:"blockers"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+january+"/readiness", nil, 200), &readiness)
	if !slices.ContainsFunc(readiness.Blockers, func(value string) bool { return strings.Contains(value, scope.ExternalSteps[0].Name) }) {
		t.Fatal("Carried unresolved step did not block sign-off", readiness.Blockers)
	}
	retryClose, retryJob := apply(origin, selected)
	if january != retryClose || job != retryJob {
		t.Fatal("Retry duplicated work")
	}
	process := func(job string) (string, prepaids.Workpaper) {
		t.Helper()
		if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
			t.Fatal("Worker did not prepare", e)
		}
		var paperID, state string
		var raw []byte
		if e := db.QueryRow("SELECT state,COALESCE(workpaper_ids->>0,'') FROM preparation_jobs WHERE id=$1", job).Scan(&state, &paperID); e != nil || state != "completed" {
			t.Fatal(state, e)
		}
		if e := db.QueryRow("SELECT paper,review_state FROM workpaper_review_state WHERE id=$1", paperID).Scan(&raw, &state); e != nil || state != "pending" {
			t.Fatal("Approval inherited", state, e)
		}
		var paper prepaids.Workpaper
		json.Unmarshal(raw, &paper)
		if paper.Proposal != nil || paper.Input.BookingConfirmed || paper.Input.MethodConfirmed || paper.Input.BookedBalance != "" || paper.Input.BookingReference != "" {
			t.Fatal("Carry-forward asserted posting authority")
		}
		var linked struct {
			Sources []preparation.Source      `json:"sources"`
			Carry   *preparation.CarryForward `json:"carry_forward"`
		}
		json.Unmarshal(raw, &linked)
		if len(linked.Sources) != 2 || len(linked.Carry.SourceIDs) != 2 {
			t.Fatal("Lost source lineage")
		}
		return paperID, paper
	}
	call("POST", "/v1/closes/"+january+"/jobs/"+job+"/cancel", map[string]any{}, 200)
	_, resumed := apply(origin, preview(origin))
	if resumed != job {
		t.Fatal("Cancelled carry-forward duplicated its job")
	}
	janPaper, jan := process(job)
	if jan.PeriodExpense != "33.33" || jan.PrepaidBalance != "33.35" || jan.Input.ClosePeriod != "2027-01" {
		t.Fatal("January rounding incorrect", jan)
	}
	if preview(january)[0].Eligible {
		t.Fatal("Next month inherited approval")
	}
	approve(january, janPaper, "approved")
	february, febJob := apply(january, preview(january))
	febPaper, feb := process(febJob)
	if feb.PeriodExpense != "33.35" || feb.PrepaidBalance != "0.00" {
		t.Fatal("Final residual lost", feb)
	}
	approve(february, febPaper, "approved")
	if preview(february)[0].Eligible {
		t.Fatal("Finished schedule carried beyond its term")
	}
	// Changing the root review invalidates both generations, without changing their history.
	approve(origin, first, "changes_requested")
	for _, id := range []string{janPaper, febPaper} {
		var state string
		db.QueryRow("SELECT review_state FROM workpaper_review_state WHERE id=$1", id).Scan(&state)
		if state != "source_changed" {
			t.Fatal("Ancestor change failed to invalidate carried work", id, state)
		}
	}
	if preview(january)[0].Eligible {
		t.Fatal("Stale lineage eligible")
	}
	// A fresh review permits a fresh run; the old result remains historical.
	approve(origin, first, "approved")
	_, newJob := apply(origin, preview(origin))
	if newJob == job {
		t.Fatal("Changed review reused old authority")
	}
	// The worker rechecks after enqueue, too.
	approve(origin, first, "changes_requested")
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal(e)
	}
	var state string
	db.QueryRow("SELECT state FROM preparation_jobs WHERE id=$1", newJob).Scan(&state)
	if state != "failed" {
		t.Fatal("Worker accepted a stale predecessor", state)
	}
	// A mid-month depreciation schedule carries into the month after its useful life.
	var august, asset string
	if err = db.QueryRow("INSERT INTO client_closes(entity,period) VALUES($1,'2029-08') RETURNING id::text", entity).Scan(&august); err != nil {
		t.Fatal(err)
	}
	laptop := prepaids.Input{Entity: entity, SourceReference: "Synthetic laptop invoice", Amount: "3600.00", ServiceStart: "2026-09-01", ServiceEnd: "2029-08-31", ClosePeriod: "2029-08", Currency: "USD", Basis: "accrual", ExpenseAccount: "Depreciation expense", PrepaidAccount: "Accumulated depreciation", Schedule: prepaids.Depreciation, Convention: prepaids.MidMonth, AccountantRuleReference: "Fixed asset policy v2", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, BookedBalance: "3550.00", BookingReference: "August ledger"}
	var invoice string
	if err = db.QueryRow("INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'laptop.txt',$2,'laptop.txt','fixture') RETURNING id::text", august, preparation.Digest([]byte("laptop.txt"))).Scan(&invoice); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(laptop)
	if body, err = preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: raw, Sources: []preparation.Source{{ID: invoice, Digest: preparation.Digest([]byte("laptop.txt")), Name: "laptop.txt", Role: "invoice", Location: "line 1"}}}, entity, "2029-08"); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text", august, preparation.Digest(body), string(body)).Scan(&asset); err != nil {
		t.Fatal(err)
	}
	approve(august, asset, "approved")
	if items := preview(august); len(items) != 1 || !items[0].Eligible {
		t.Fatalf("the half month after the useful life must carry forward: %+v", items)
	}
}
