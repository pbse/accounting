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

	"close.local/backend/internal/accruals"
	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
	"close.local/backend/internal/quickbooks"
)

func TestAccountingMethodsDurableReviewAndSourceInvalidation(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", database)
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
	var closeID string
	entity := "Accounting methods " + rand.Text()
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2028-02') RETURNING id::text`, entity).Scan(&closeID))
	defer func() {
		for _, q := range []string{`DELETE FROM preparation_jobs WHERE close_id=$1`, `DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, `DELETE FROM workpapers WHERE close_id=$1`, `DELETE FROM evidence WHERE close_id=$1`, `DELETE FROM client_closes WHERE id=$1`} {
			_, e := db.Exec(q, closeID)
			if e != nil {
				t.Error(e)
			}
		}
	}()
	sources := []preparation.Source{}
	for _, role := range []string{"invoice", "support"} {
		var id string
		content := "Synthetic " + role + " terms and ledger"
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,$2,$3,$4,'fixture') RETURNING id::text`, closeID, role+".txt", preparation.Digest([]byte(content)), content).Scan(&id))
		sources = append(sources, preparation.Source{ID: id, Digest: preparation.Digest([]byte(content)), Name: role + ".txt", Role: role, Location: "all lines"})
	}
	token := strings.Repeat("m", 32)
	h := apiWithStore(db, token)
	call := func(method, path string, body any, want int) []byte {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %d: %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	prepaid := prepaids.Input{Entity: entity, SourceReference: "Invoice and credit", Amount: "100.00", ServiceStart: "2028-01-16", ServiceEnd: "2028-03-15", ClosePeriod: "2028-02", Currency: "USD", Basis: "accrual", ExpenseAccount: "Software", PrepaidAccount: "Prepaid software", Method: prepaids.DailyMethod, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, CreditAmount: "10.00", CreditPeriod: "2028-02", CreditReference: "Credit document", BookedBalance: "30.00", BookingReference: "Month-end ledger"}
	accrual := accruals.Input{Entity: entity, SourceReference: "Completed service", ServiceStart: "2028-02-01", ServiceEnd: "2028-02-29", ClosePeriod: "2028-02", Amount: "1200.00", BookedBalance: "200.00", BookingReference: "Accrued liability ledger", EstimationBasis: "Approved service estimate", ExpenseAccount: "Consulting", LiabilityAccount: "Accrued consulting", Currency: "USD", Basis: "accrual", Method: accruals.Method, MethodConfirmed: true, BookingConfirmed: true, ExamplesConfirmed: true, ReverseNextMonth: true}
	call("POST", "/v1/accruals:prepare", accrual, 200)
	papers := []string{}
	for n, input := range []any{prepaid, accrual} {
		kind := []string{"prepaid", "accrual"}[n]
		raw, _ := json.Marshal(input)
		plan := preparation.Plan{Kind: kind, Input: raw, Sources: sources}
		plans, _ := json.Marshal([]preparation.Plan{plan})
		var job, paper, state string
		must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,$3,'fixture') RETURNING id::text`, closeID, preparation.Digest(plans), string(plans)).Scan(&job))
		worked, e := preparation.ProcessOne(context.Background(), db)
		must(e)
		if !worked {
			t.Fatal("job not processed")
		}
		must(db.QueryRow(`SELECT state,COALESCE(workpaper_ids->>0,'') FROM preparation_jobs WHERE id=$1`, job).Scan(&state, &paper))
		if state != "completed" {
			t.Fatal("calculation failed", state)
		}
		papers = append(papers, paper)
		var result []byte
		must(db.QueryRow(`SELECT paper FROM workpapers WHERE id=$1`, paper).Scan(&result))
		if n == 0 {
			var p prepaids.Workpaper
			must(json.Unmarshal(result, &p))
			if p.Proposal.Amount != "8.40" {
				t.Fatal("wrong credit adjustment")
			}
			if _, e = (quickbooks.Sandbox{}).Prepare(context.Background(), p); e == nil || !strings.Contains(e.Error(), "draft review only") {
				t.Fatal("new method reached sandbox posting", e)
			}
		} else {
			var p accruals.Workpaper
			must(json.Unmarshal(result, &p))
			if p.Reversal == nil || p.Reversal.Date != "2028-03-01" || p.Proposal.Amount != "1000.00" {
				t.Fatal("wrong saved accrual")
			}
		}
		path := "/v1/closes/" + closeID + "/workpapers/" + paper + "/reviews"
		var review struct {
			Digest   string `json:"digest"`
			ReviewID string `json:"review_id"`
			State    string `json:"state"`
		}
		must(json.Unmarshal(call("GET", path, nil, 200), &review))
		if review.State != "pending" {
			t.Fatal("draft inherited approval")
		}
		call("POST", path, map[string]any{"decision": "approved", "note": "Synthetic accountant review of examples, method and source balances", "digest": review.Digest, "expected_review_id": review.ReviewID}, 200)
	}
	call("GET", "/v1/closes/"+closeID+"/export", nil, 200)
	var replacement string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'changed.txt','changed','Changed terms','fixture') RETURNING id::text`, closeID).Scan(&replacement))
	_, err = db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, sources[0].ID, replacement)
	must(err)
	for _, paper := range papers {
		var state string
		must(db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE id=$1`, paper).Scan(&state))
		if state != "source_changed" {
			t.Fatal("stale accounting approval survived")
		}
	}
}
