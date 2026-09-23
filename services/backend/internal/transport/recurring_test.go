package transport

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"

	"close.local/backend/internal/prepaids"
	"close.local/backend/internal/preparation"
)

func TestRecurringComparisonRefreshesOnlyCurrentPeriod(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entity := "Recurring fixture " + rand.Text()
	var origin string
	if err = db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-12') RETURNING id::text`, entity).Scan(&origin); err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, query := range []string{
			`DELETE FROM quickbooks_snapshots WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM quickbooks_connections WHERE client_entity=$1`,
			`DELETE FROM preparation_jobs WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM evidence WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
			`DELETE FROM client_closes WHERE entity=$1`,
		} {
			if _, e := db.Exec(query, entity); e != nil {
				t.Error(e)
			}
		}
	}()
	var source string
	if err = db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt','invoice-digest','Synthetic invoice','fixture') RETURNING id::text`, origin).Scan(&source); err != nil {
		t.Fatal(err)
	}
	input := prepaids.Input{Entity: entity, SourceReference: "Synthetic insurance", Amount: "120.00", ServiceStart: "2026-12-01", ServiceEnd: "2027-02-28", ClosePeriod: "2026-12", Currency: "USD", Basis: "accrual", ExpenseAccount: "Insurance expense", PrepaidAccount: "Prepaid insurance", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	inputRaw, _ := json.Marshal(input)
	parentBody, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: inputRaw, Sources: []preparation.Source{{ID: source, Digest: "invoice-digest", Name: "invoice.txt", Role: "invoice", Location: "line 1"}}}, entity, "2026-12")
	if err != nil {
		t.Fatal(err)
	}
	var parent string
	if err = db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, origin, preparation.Digest(parentBody), string(parentBody)).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic reviewed schedule','accountant')`, parent, preparation.Digest(parentBody)); err != nil {
		t.Fatal(err)
	}
	h := apiWithStore(db, "recurring-fixture-workspace-token-000000")
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer recurring-fixture-workspace-token-000000")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var carry struct {
		Items []carryItem `json:"items"`
	}
	json.Unmarshal(call("GET", "/v1/closes/"+origin+"/carry-forward", nil, 200), &carry)
	if len(carry.Items) != 1 || !carry.Items[0].Eligible {
		t.Fatal("reviewed schedule was not eligible")
	}
	var next struct {
		CloseID string `json:"close_id"`
	}
	json.Unmarshal(call("POST", "/v1/closes/"+origin+"/carry-forward", map[string]any{"confirmed": true, "items": carry.Items}, 200), &next)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal("carry job", e)
	}
	var connection string
	if err = db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) SELECT $1,organization_id,entity,'fixture','active',$2,now()+interval '1 day','123','Synthetic' FROM client_closes WHERE id=$1 RETURNING id::text`, next.CloseID, rand.Text()).Scan(&connection); err != nil {
		t.Fatal(err)
	}
	base := "/v1/closes/" + next.CloseID + "/recurring"
	snapshot := func(replaces string) string {
		t.Helper()
		var evidence string
		if err := db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'trial-balance.txt',$2,'Synthetic period report','fixture') RETURNING id::text`, next.CloseID, rand.Text()).Scan(&evidence); err != nil {
			t.Fatal(err)
		}
		if replaces != "" {
			if _, err := db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, replaces, evidence); err != nil {
				t.Fatal(err)
			}
		}
		payload, _ := json.Marshal(map[string]any{"period": "2027-01", "source_id": evidence, "accounts": []map[string]string{{"Id": "7", "Name": "Prepaid insurance", "AccountSubType": "PrepaidExpenses"}}, "reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD", "ReportBasis": "Accrual"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "Account"}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}}, "Rows": map[string]any{"Row": []map[string]any{{"type": "Data", "ColData": []map[string]string{{"id": "7", "value": "Prepaid insurance"}, {"value": "50.00"}, {"value": ""}}}}}}}})
		if err := db.QueryRow(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture') RETURNING id::text`, next.CloseID, connection, rand.Text(), string(payload)).Scan(new(string)); err != nil {
			t.Fatal(err)
		}
		return evidence
	}
	firstEvidence := snapshot("")
	var preview struct {
		Snapshot recurringSnapshot `json:"snapshot"`
		Items    []recurringItem   `json:"items"`
	}
	json.Unmarshal(call("GET", base, nil, 200), &preview)
	if len(preview.Items) != 1 || preview.Items[0].Blocker != "" || preview.Items[0].Scheduled != "40.00" || preview.Items[0].AccountID != "7" {
		t.Fatalf("unsafe comparison preview: %+v", preview.Items)
	}
	request := map[string]any{"paper_id": preview.Items[0].ID, "digest": preview.Items[0].Digest, "snapshot_id": preview.Snapshot.ID, "booked_balance": "50.00", "confirmed": true}
	call("POST", base, request, 202)
	call("POST", base, request, 202)
	call("POST", base, map[string]any{"paper_id": request["paper_id"], "digest": request["digest"], "snapshot_id": request["snapshot_id"], "booked_balance": "45.00", "confirmed": true}, 409)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal("comparison job", e)
	}
	var revised struct {
		prepaids.Workpaper
		Revises string               `json:"revises"`
		Sources []preparation.Source `json:"sources"`
	}
	var paperRaw []byte
	if err = db.QueryRow(`SELECT paper FROM workpapers WHERE close_id=$1 AND paper->>'revises'=$2`, next.CloseID, preview.Items[0].ID).Scan(&paperRaw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(paperRaw, &revised) != nil || revised.Proposal == nil || revised.Proposal.Amount != "10.00" || revised.Revises != preview.Items[0].ID || len(revised.Sources) != 2 || revised.Sources[1].ID != firstEvidence {
		t.Fatalf("unsafe revised work: %+v", revised)
	}
	call("POST", base, request, 409)
	secondEvidence := snapshot(firstEvidence)
	json.Unmarshal(call("GET", base, nil, 200), &preview)
	if len(preview.Items) != 1 || preview.Items[0].Blocker != "" || preview.Items[0].ID == request["paper_id"] {
		t.Fatal("new capture did not reopen only latest recurring revision")
	}
	call("POST", base, map[string]any{"paper_id": preview.Items[0].ID, "digest": preview.Items[0].Digest, "snapshot_id": preview.Snapshot.ID, "booked_balance": "40.00", "confirmed": true}, 202)
	if worked, e := preparation.ProcessOne(context.Background(), db); e != nil || !worked {
		t.Fatal("refreshed comparison job", e)
	}
	if err = db.QueryRow(`SELECT paper FROM workpapers WHERE close_id=$1 AND paper->>'revises'=$2`, next.CloseID, preview.Items[0].ID).Scan(&paperRaw); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(paperRaw, &revised) != nil || revised.Proposal != nil || revised.Sources[1].ID != secondEvidence {
		t.Fatal("fresh snapshot was not used", string(paperRaw))
	}
}
