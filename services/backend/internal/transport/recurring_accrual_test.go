package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/preparation"
)

func TestRecurringAccrualFreshReviewAndPortfolioDependency(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Recurring accrual " + rand.Text()
	var prior, next string
	for _, p := range []struct {
		period string
		target *string
	}{{"2026-08", &prior}, {"2026-09", &next}} {
		if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,$2) RETURNING id::text`, entity, p.period).Scan(p.target); err != nil {
			t.Fatal(err)
		}
	}
	defer func() {
		for _, q := range []string{`DELETE FROM quickbooks_snapshots WHERE close_id IN ($1,$2)`, `DELETE FROM quickbooks_connections WHERE close_id IN ($1,$2)`, `DELETE FROM preparation_jobs WHERE close_id IN ($1,$2)`, `DELETE FROM workpapers WHERE close_id IN ($1,$2)`, `DELETE FROM evidence WHERE close_id IN ($1,$2)`, `DELETE FROM client_closes WHERE id IN ($1,$2)`} {
			if _, e := db.Exec(q, prior, next); e != nil {
				t.Error(e)
			}
		}
	}()
	var oldSource, currentSource, ledgerSource string
	for _, v := range []struct {
		id                           *string
		close, name, digest, content string
	}{{&oldSource, prior, "August service.txt", "august-source", "August service incurred"}, {&currentSource, next, "September service.txt", "september-source", "September service incurred"}, {&ledgerSource, next, "September ledger.txt", "september-ledger", "Current Trial Balance"}} {
		if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, v.close, v.name, v.digest, v.content).Scan(v.id); err != nil {
			t.Fatal(err)
		}
	}
	input := accruals.Input{Entity: entity, SourceReference: "August service", ClosePeriod: "2026-08", ServiceStart: "2026-08-01", ServiceEnd: "2026-08-31", Amount: "100.00", BookedBalance: "0.00", BookingReference: "August Trial Balance", EstimationBasis: "Fixed monthly service", AccountantRuleReference: "Accountant approval: fixed monthly service", ExpenseAccount: "Service expense", LiabilityAccount: "Accrued service", Currency: "USD", Basis: "accrual", Method: accruals.RecurringMethod, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, ReverseNextMonth: true}
	raw, _ := json.Marshal(input)
	body, err := preparation.Calculate(preparation.Plan{Kind: "accrual", Input: raw, Sources: []preparation.Source{{ID: oldSource, Digest: "august-source", Name: "August service.txt", Role: "invoice"}}}, entity, "2026-08")
	if err != nil {
		t.Fatal(err)
	}
	var paper string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'accrual',$2,$3) RETURNING id::text`, prior, preparation.Digest(body), string(body)).Scan(&paper); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic fixed monthly rule','accountant')`, paper, preparation.Digest(body)); err != nil {
		t.Fatal(err)
	}
	var connection string
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) SELECT $1,organization_id,entity,'fixture','active',$2,now()+interval '1 day','123',entity FROM client_closes WHERE id=$1 RETURNING id::text`, next, rand.Text()).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"period": "2026-09", "source_id": ledgerSource, "accounts": []map[string]string{{"Id": "17", "Name": "Accrued service", "AccountType": "Other Current Liability"}}, "reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD", "ReportBasis": "Accrual"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": ""}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}}, "Rows": map[string]any{"Row": []map[string]any{{"type": "Data", "ColData": []map[string]string{{"id": "17", "value": "Accrued service"}, {"value": ""}, {"value": "25.00"}}}}}}}})
	if _, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, next, connection, rand.Text(), string(payload)); err != nil {
		t.Fatal(err)
	}
	h := apiWithStore(db, "recurring-accrual-fixture-token-00000000")
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer recurring-accrual-fixture-token-00000000")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	path := "/v1/closes/" + next + "/recurring-accruals"
	var preview struct {
		Snapshot recurringSnapshot      `json:"snapshot"`
		Items    []recurringAccrualItem `json:"items"`
	}
	// The agent read September's service document as an accrual for the rule's amount.
	var job string
	if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, next, "intake:"+currentSource).Scan(&job); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2::bigint,jsonb_build_object('kind','accrual','facts',jsonb_build_array(jsonb_build_object('field','amount','value','100','candidates',jsonb_build_array(jsonb_build_object('value','100.00','source_id',$3::text))),jsonb_build_object('field','currency','value','USD'))))`, job, currentSource, currentSource); err != nil {
		t.Fatal(err)
	}
	// Documents that do not themselves show one amount of 100 in a clear currency are not offered: one whose 100 is
	// cited from another document, one with two amounts, one whose currency the reading left in conflict.
	for i, facts := range []string{`[{"field":"amount","value":"100","candidates":[{"value":"100","source_id":"0"}]}]`, `[{"field":"amount","value":"250","candidates":[{"value":"250","source_id":"SELF"}]},{"field":"amount","value":"100","candidates":[{"value":"100","source_id":"SELF"}]}]`, `[{"field":"amount","value":"100","candidates":[{"value":"100","source_id":"SELF"}]},{"field":"currency","value":"","candidates":[{"value":"EUR","source_id":"SELF"},{"value":"USD","source_id":"SELF"}]}]`} {
		var other, otherJob string
		if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,'Other service','fixture') RETURNING id::text`, next, fmt.Sprintf("other-%d.txt", i), rand.Text()).Scan(&other); err != nil {
			t.Fatal(err)
		}
		if err = db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, next, "intake:"+other).Scan(&otherJob); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,jsonb_build_object('kind','accrual','facts',$3::jsonb))`, otherJob, other, strings.ReplaceAll(facts, "SELF", other)); err != nil {
			t.Fatal(err)
		}
	}
	if err = json.Unmarshal(call("GET", path, nil, 200), &preview); err != nil || len(preview.Items) != 1 || preview.Items[0].Blocker != "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	chosen := preview.Items[0]
	// The one confirmation starts from the Trial Balance's credit balance, last month's reversal and the matching document.
	if chosen.BookedBalance != "25.00" || chosen.ReverseNextMonth != input.ReverseNextMonth || chosen.SuggestedEvidence != currentSource {
		t.Fatalf("the preview prefills what the records show: %+v", chosen)
	}
	request := map[string]any{"paper_id": chosen.ID, "digest": chosen.Digest, "review_id": chosen.ReviewID, "snapshot_id": preview.Snapshot.ID, "evidence_id": currentSource, "booked_balance": "25.00", "reverse_next_month": false, "confirmed": true}
	call("POST", path, request, 202)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal("recurring job", e)
	}
	var created string
	var result []byte
	if err = db.QueryRow(`SELECT id::text,paper FROM workpapers WHERE close_id=$1 AND paper->'recurring_accrual'->>'paper_id'=$2`, next, paper).Scan(&created, &result); err != nil {
		t.Fatal(err)
	}
	var prepared struct {
		accruals.Workpaper
		Sources []preparation.Source `json:"sources"`
	}
	if json.Unmarshal(result, &prepared) != nil || prepared.Proposal == nil || prepared.Proposal.Amount != "75.00" || len(prepared.Sources) != 2 || prepared.Sources[0].ID != currentSource || prepared.Sources[1].ID != ledgerSource {
		t.Fatal("fresh ledger or source not bound", string(result))
	}
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','September reviewed independently','accountant')`, created, preparation.Digest(result)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'changes_requested','August source no longer supports rule','accountant')`, paper, preparation.Digest(body)); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id::text=$1`, created).Scan(&state); err != nil || state != "source_changed" {
		t.Fatalf("portfolio dependency did not reopen September: %s %v", state, err)
	}
}

func TestOneDocumentIsOfferedToOneRuleInItsCurrency(t *testing.T) {
	rule := func(amount, currency string) recurringAccrualItem {
		var item recurringAccrualItem
		item.Amount, item.Paper.Input.Currency = amount, currency
		return item
	}
	items := []recurringAccrualItem{rule("100.00", "USD"), rule("200.00", "USD"), rule("200.00", "USD")}
	suggestEvidence(items, []accrualDocument{{id: "d1", amount: 10000}, {id: "d2", amount: 20000}, {id: "d3", amount: 10000, currency: "EUR"}})
	if items[0].SuggestedEvidence != "d1" || items[1].SuggestedEvidence != "" || items[2].SuggestedEvidence != "" {
		t.Fatalf("a euro document is not offered for a dollar rule, and a document is never offered to two rules: %+v", items)
	}
}
