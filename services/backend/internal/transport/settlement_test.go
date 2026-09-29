package transport

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/preparation"
	"close.local/backend/internal/reconciliation"
)

func TestStripeExportIsRecognizedPreparedAndExported(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not configured")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Stripe settlement " + rand.Text()
	var closeID string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-09') RETURNING id::text`, entity).Scan(&closeID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, table := range []string{"preparation_jobs", "preparation_plans", "workpapers", "evidence", "client_closes"} {
			key := "close_id"
			if table == "client_closes" {
				key = "id"
			}
			db.Exec("DELETE FROM "+table+" WHERE "+key+"=$1", closeID)
		}
	}()
	token := strings.Repeat("s", 32)
	h := modelHandler(t, apiWithStore(db, token))
	base := "/v1/closes/" + closeID
	send := func(method, path, contentType string, body io.Reader, status int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, path, body)
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	upload := func(name, content string) map[string]string {
		t.Helper()
		var b bytes.Buffer
		m := multipart.NewWriter(&b)
		f, _ := m.CreateFormFile("file", name)
		io.WriteString(f, content)
		m.Close()
		var result map[string]string
		json.Unmarshal(send("POST", base+"/evidence", m.FormDataContentType(), &b, 200), &result)
		return result
	}
	report := upload("stripe-payouts-2026-09.csv", "automatic_payout_effective_at,automatic_payout_id,balance_transaction_id,currency,fee,gross,net,reporting_category\n2026-09-03 00:00:00,po_1,txn_1,usd,3.20,100.00,96.80,charge\n2026-09-03 00:00:00,po_1,txn_2,usd,0.00,-10.00,-10.00,refund\n2026-09-30 00:00:00,po_2,txn_3,usd,1.46,40.00,38.54,charge\n")
	bank := upload("bank-2026-09.csv", "id,date,amount\nB1,2026-09-04,86.80\nB2,2026-10-01,38.54\nB3,2026-09-15,-300.00")
	for n := 0; ; n++ {
		worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db)
		if e != nil || n > 20 {
			t.Fatal("intake did not settle", e)
		}
		if !worked {
			break
		}
	}
	var revision int
	var raw []byte
	if err = db.QueryRow(`SELECT revision,proposal FROM preparation_intakes WHERE job_id::text=$1`, report["job_id"]).Scan(&revision, &raw); err != nil {
		t.Fatal(err)
	}
	var proposal preparation.Proposal
	json.Unmarshal(raw, &proposal)
	if proposal.Kind != "settlement" || proposal.Engine != "recognized_export" || !strings.Contains(proposal.Summary, "2 automatic payouts expected 2026-09-03 to 2026-09-30") {
		t.Fatalf("Stripe export was not recognized without a model: %+v", proposal)
	}
	t.Setenv("OPENAI_API_KEY", "mock-only-key")
	t.Setenv("OPENAI_MODEL", "mock-only-model")
	calls := 0
	originalTransport := http.DefaultTransport
	http.DefaultTransport = modelRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, io.ErrUnexpectedEOF
	})
	second := upload("stripe-payouts-copy.csv", "automatic_payout_effective_at,automatic_payout_id,balance_transaction_id,currency,fee,gross,net,reporting_category\n2026-09-10 00:00:00,po_9,txn_9,usd,0.30,10.00,9.70,charge\n")
	if worked, e := preparation.ProcessIntake(modelContext(t, context.Background()), db); e != nil || !worked {
		t.Fatal("agent intake did not run", e)
	}
	http.DefaultTransport = originalTransport
	if err = db.QueryRow(`SELECT proposal FROM preparation_intakes WHERE job_id::text=$1`, second["job_id"]).Scan(&raw); err != nil || calls != 0 || !strings.Contains(string(raw), `"recognized_export"`) {
		t.Fatalf("agent engine should recognize the export before any model call: %d calls, %s %v", calls, raw, err)
	}
	input, _ := json.Marshal(reconciliation.SettlementInput{Entity: entity, ClosePeriod: "2026-09", Currency: "USD", BankAccount: "Operating bank", ReportReference: "linked", BankReference: "linked", MethodConfirmed: true, CoverageConfirmed: true})
	plan := map[string]any{"kind": "settlement", "input": json.RawMessage(input), "work_id": report["job_id"], "work_revision": revision, "sources": []map[string]string{{"id": report["id"], "role": "processor_report", "location": "All rows"}, {"id": bank["id"], "role": "statement", "location": "All rows"}}}
	prepare := func(status int) {
		t.Helper()
		body, _ := json.Marshal(plan)
		send("POST", base+"/preparations", "application/json", bytes.NewReader(body), status)
	}
	prepare(422)
	plan["sources"].([]map[string]string)[1]["role"] = "bank"
	prepare(200)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal("settlement job did not run", e)
	}
	var paperID string
	var paperRaw []byte
	if err = db.QueryRow(`SELECT id::text,paper FROM workpapers WHERE close_id=$1 AND kind='settlement'`, closeID).Scan(&paperID, &paperRaw); err != nil {
		t.Fatal(err)
	}
	var paper struct {
		reconciliation.SettlementWorkpaper
		Sources []preparation.Source `json:"sources"`
	}
	json.Unmarshal(paperRaw, &paper)
	if len(paper.Blockers) != 0 || len(paper.Payouts) != 2 || paper.Payouts[1].Deposit == nil || paper.Payouts[1].Deposit.ID != "B2" || paper.Input.ReportReference != "stripe-payouts-2026-09.csv · All rows" || len(paper.Sources) != 2 || paper.Sources[0].ID != bank["id"] || paper.Sources[0].Role != "bank" {
		t.Fatalf("evidence-bound settlement: %s", paperRaw)
	}
	payouts := upload("stripe-payouts-report-2026-09.csv", "balance_transaction_id,currency,effective_at,net,payout_expected_arrival_date,payout_id,payout_status,reporting_category\ntxn_m1,usd,2026-09-02 10:00:00,-125.05,2026-09-03,po_M1,paid,payout\n")
	activity := upload("stripe-activity-2026-09.csv", "balance_transaction_id,created,currency,gross,fee,net,reporting_category\ntxn_a1,2026-09-01 12:00:00,usd,130.00,4.95,125.05,charge\n")
	deposits := upload("bank-manual-2026-09.csv", "id,date,amount\nM1,2026-09-03,125.05\n")
	manual, _ := json.Marshal(reconciliation.SettlementInput{Method: reconciliation.PayoutsMethod, Entity: entity, ClosePeriod: "2026-09", Currency: "USD", BankAccount: "Operating bank", ReportReference: "linked", BankReference: "linked", MethodConfirmed: true, CoverageConfirmed: true})
	body, _ := json.Marshal(map[string]any{"kind": "settlement", "input": json.RawMessage(manual), "sources": []map[string]string{{"id": payouts["id"], "role": "processor_report", "location": "All rows"}, {"id": deposits["id"], "role": "bank", "location": "All rows"}, {"id": activity["id"], "role": "activity_report", "location": "All rows"}}})
	var created map[string]string
	json.Unmarshal(send("POST", base+"/preparations", "application/json", bytes.NewReader(body), 200), &created)
	var stored struct {
		Input reconciliation.SettlementInput `json:"input"`
	}
	if err = db.QueryRow(`SELECT plan FROM preparation_plans WHERE id::text=$1 AND close_id=$2`, created["id"], closeID).Scan(&raw); err != nil || json.Unmarshal(raw, &stored) != nil {
		t.Fatal(err)
	}
	rolled, err := reconciliation.PrepareSettlement(stored.Input)
	if err != nil || rolled.RollForward == nil || rolled.RollForward.Change != "0.00" || stored.Input.ActivityReference != "stripe-activity-2026-09.csv · All rows" {
		t.Fatalf("the activity report is bound from its evidence and rolls the balance forward: %v %s", err, raw)
	}
	exported := send("GET", base+"/export", "application/json", nil, 200)
	archive, err := zip.NewReader(bytes.NewReader(exported), int64(len(exported)))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if file.Name == "workpapers/"+paperID+"-payouts.csv" {
			f, _ := file.Open()
			table, _ := io.ReadAll(f)
			if !strings.Contains(string(table), "po_2,2026-09-30,1,40.00,1.46,38.54,matched,B2,2026-10-01,yes") {
				t.Fatalf("payout table: %s", table)
			}
			return
		}
	}
	t.Fatal("close package omitted the settlement payouts table")
}
