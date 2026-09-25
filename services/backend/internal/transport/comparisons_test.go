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

func TestBookedBalanceReadsTheNormalSideOnly(t *testing.T) {
	row := func(schedule string, columns []string, cells ...string) recurringItem {
		raw, _ := json.Marshal(func() []map[string]string {
			out := []map[string]string{}
			for _, c := range cells {
				out = append(out, map[string]string{"value": c})
			}
			return out
		}())
		return recurringItem{Schedule: schedule, Columns: columns, Cells: raw}
	}
	columns := []string{"Account", "Debit", "Credit"}
	for name, c := range map[string]struct {
		item recurringItem
		want int64
		ok   bool
	}{
		"prepaid debit":              {row("", columns, "Prepaid insurance", "1,050.00", ""), 105000, true},
		"accumulated depreciation":   {row(prepaids.Depreciation, columns, "Accumulated depreciation", "", "70.00"), 7000, true},
		"deferred revenue":           {row(prepaids.DeferredRevenue, columns, "Deferred revenue", "", "300.00"), 30000, true},
		"prepaid on the credit side": {row("", columns, "Prepaid insurance", "", "10.00"), 0, false},
		"two debit columns":          {row("", []string{"Account", "Debit", "Debit", "Credit"}, "Prepaid", "1.00", "2.00", ""), 0, false},
		"cells not matching columns": {row("", columns, "Prepaid insurance", "50.00"), 0, false},
		"an extra cell":              {row("", columns, "Prepaid insurance", "50.00", "", "9.00"), 0, false},
		"no credit column":           {row("", []string{"Account", "Debit"}, "Prepaid insurance", "50.00"), 0, false},
		"unreadable amount":          {row("", columns, "Prepaid insurance", "fifty", ""), 0, false},
		"a fraction of a cent":       {row("", columns, "Prepaid insurance", "50.005", ""), 0, false},
	} {
		if got, ok := bookedBalance(c.item); got != c.want || ok != c.ok {
			t.Errorf("%s: got %d %v, want %d %v", name, got, ok, c.want, c.ok)
		}
	}
}

func TestCarriedSchedulesArePreparedFromTheTrialBalance(t *testing.T) {
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
	entity := "Comparison fixture " + rand.Text()
	var origin string
	must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,'2026-12') RETURNING id::text`, entity).Scan(&origin))
	defer func() {
		for _, query := range []string{
			`DELETE FROM close_signoffs WHERE close_id IN (SELECT id FROM client_closes WHERE entity=$1)`,
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
	var source string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt','invoice-digest','Synthetic invoice','fixture') RETURNING id::text`, origin).Scan(&source))
	input := prepaids.Input{Entity: entity, SourceReference: "Synthetic insurance", Amount: "120.00", ServiceStart: "2026-12-01", ServiceEnd: "2027-02-28", ClosePeriod: "2026-12", Currency: "USD", Basis: "accrual", ExpenseAccount: "Insurance expense", PrepaidAccount: "Prepaid insurance", Method: prepaids.Method, MethodConfirmed: true, BookingConfirmed: true}
	inputRaw, _ := json.Marshal(input)
	parentBody, err := preparation.Calculate(preparation.Plan{Kind: "prepaid", Input: inputRaw, Sources: []preparation.Source{{ID: source, Digest: "invoice-digest", Name: "invoice.txt", Role: "invoice", Location: "line 1"}}}, entity, "2026-12")
	must(err)
	var parent string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, origin, preparation.Digest(parentBody), string(parentBody)).Scan(&parent))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Synthetic reviewed schedule','accountant')`, parent, preparation.Digest(parentBody))
	must(err)
	h := apiWithStore(db, "comparison-fixture-workspace-token-00000")
	call := func(method, path string, value any, want int) []byte {
		t.Helper()
		body, _ := json.Marshal(value)
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer comparison-fixture-workspace-token-00000")
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
	var next struct {
		CloseID string `json:"close_id"`
	}
	json.Unmarshal(call("POST", "/v1/closes/"+origin+"/carry-forward", map[string]any{"confirmed": true, "items": carry.Items}, 200), &next)
	if worked, e := preparation.ProcessOne(ctx, db); e != nil || !worked {
		t.Fatal("carry job", e)
	}
	var connection string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm,company_name) SELECT $1,organization_id,entity,'fixture','active',$2,now()+interval '1 day','123','Synthetic' FROM client_closes WHERE id=$1 RETURNING id::text`, next.CloseID, rand.Text()).Scan(&connection))
	evidence := ""
	snapshot := func(accounts []map[string]string, debit, credit string) {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'trial-balance.txt',$2,'Synthetic period report','fixture') RETURNING id::text`, next.CloseID, rand.Text()).Scan(&id))
		if evidence != "" {
			_, e := db.Exec(`UPDATE evidence SET superseded_by=$2 WHERE id=$1`, evidence, id)
			must(e)
		}
		evidence = id
		payload, _ := json.Marshal(map[string]any{"period": "2027-01", "source_id": id, "accounts": accounts, "reports": map[string]any{"TrialBalance": map[string]any{"Header": map[string]string{"Currency": "USD", "ReportBasis": "Accrual"}, "Columns": map[string]any{"Column": []map[string]string{{"ColTitle": "Account"}, {"ColTitle": "Debit"}, {"ColTitle": "Credit"}}}, "Rows": map[string]any{"Row": []map[string]any{{"type": "Data", "ColData": []map[string]string{{"id": "7", "value": "Prepaid insurance"}, {"value": debit}, {"value": credit}}}}}}}})
		_, e := db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, next.CloseID, connection, rand.Text(), string(payload))
		must(e)
	}
	sweep := func() int {
		t.Helper()
		_, queued, err := sweepComparisons(ctx, db, "0")
		must(err)
		return queued
	}
	account := []map[string]string{{"Id": "7", "Name": "Prepaid insurance", "AccountSubType": "PrepaidExpenses"}}

	snapshot(account, "", "10.00")
	if got := sweep(); got != 0 {
		t.Fatal("a prepaid balance on the credit side is left to a person")
	}
	snapshot(append(account, map[string]string{"Id": "8", "Name": "Prepaid insurance", "AccountSubType": "PrepaidExpenses"}), "50.00", "")
	if got := sweep(); got != 0 {
		t.Fatal("an ambiguous account is left to a person")
	}
	snapshot(account, "50.00", "")
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed','fixture')`, next.CloseID)
	must(err)
	if got := sweep(); got != 0 {
		t.Fatal("a signed-off close is not prepared")
	}
	if got, e := queueComparisons(ctx, db, next.CloseID); e != nil || got != 0 {
		t.Fatal("a close signed off after it was picked is not prepared either")
	}
	_, err = db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, next.CloseID)
	must(err)
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id=$1`, connection)
	must(err)
	if got := sweep(); got != 0 {
		t.Fatal("an inactive snapshot is not used")
	}
	_, err = db.Exec(`UPDATE quickbooks_connections SET state='active' WHERE id=$1`, connection)
	must(err)
	var carried string
	must(db.QueryRow(`SELECT id::text FROM workpapers WHERE close_id=$1 AND paper ? 'carry_forward'`, next.CloseID).Scan(&carried))
	var failed string
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,jsonb_build_array(jsonb_build_object('revises',$3::text,'sources',jsonb_build_array(jsonb_build_object('id',$4::text)))),'failed','accountant') RETURNING id::text`, next.CloseID, rand.Text(), carried, evidence).Scan(&failed))
	if got := sweep(); got != 0 {
		t.Fatal("a person's earlier comparison against this capture, even a failed one, is left to them")
	}
	_, err = db.Exec(`DELETE FROM preparation_jobs WHERE id=$1`, failed)
	must(err)

	var limit int
	must(db.QueryRow(`SELECT max_pending_jobs FROM organizations o JOIN client_closes c ON c.organization_id=o.id WHERE c.id=$1`, next.CloseID).Scan(&limit))
	defer db.Exec(`UPDATE organizations SET max_pending_jobs=$2,enabled=true WHERE id=(SELECT organization_id FROM client_closes WHERE id=$1)`, next.CloseID, limit)
	var blocker string
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,created_by) VALUES($1,$2,'[]','fixture') RETURNING id::text`, next.CloseID, rand.Text()).Scan(&blocker))
	var active int
	must(db.QueryRow(`SELECT count(*) FROM preparation_jobs j JOIN client_closes c ON c.id=j.close_id JOIN client_closes n ON n.organization_id=c.organization_id WHERE n.id=$1 AND j.state IN ('queued','waiting_evidence','interpreting','needs_confirmation','waiting_client')`, next.CloseID).Scan(&active))
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=$2 WHERE id=(SELECT organization_id FROM client_closes WHERE id=$1)`, next.CloseID, active)
	must(err)
	if got := sweep(); got != 0 {
		t.Fatal("at the firm's job limit nothing is queued, and the sweep still completes")
	}
	_, err = db.Exec(`UPDATE organizations SET max_pending_jobs=$2 WHERE id=(SELECT organization_id FROM client_closes WHERE id=$1)`, next.CloseID, limit)
	must(err)
	_, err = db.Exec(`DELETE FROM preparation_jobs WHERE id=$1`, blocker)
	must(err)
	_, err = db.Exec(`UPDATE organizations SET enabled=false WHERE id=(SELECT organization_id FROM client_closes WHERE id=$1)`, next.CloseID)
	must(err)
	disabled := sweep()
	_, err = db.Exec(`UPDATE organizations SET enabled=true WHERE id=(SELECT organization_id FROM client_closes WHERE id=$1)`, next.CloseID)
	must(err)
	if disabled != 0 {
		t.Fatal("a disabled firm is not prepared")
	}
	if got := sweep(); got != 1 {
		t.Fatalf("one comparison is prepared from the Trial Balance: %d", got)
	}
	var by, plans string
	must(db.QueryRow(`SELECT created_by,plans::text FROM preparation_jobs WHERE close_id=$1 AND state='queued'`, next.CloseID).Scan(&by, &plans))
	if by != "scheduled-recurring-comparison" || !strings.Contains(plans, `"booked_balance": "50.00"`) || !strings.Contains(plans, "Agent-read from QuickBooks snapshot") {
		t.Fatalf("the job is the agent's, with the Trial Balance balance and a reference saying so: %s %s", by, plans)
	}
	if got := sweep(); got != 0 {
		t.Fatal("a queued comparison is not queued again")
	}
	if worked, e := preparation.ProcessOne(ctx, db); e != nil || !worked {
		t.Fatal("comparison job", e)
	}
	var revised struct {
		prepaids.Workpaper
		Revises string `json:"revises"`
	}
	var raw []byte
	must(db.QueryRow(`SELECT paper FROM workpapers WHERE close_id=$1 AND paper->>'revises'=$2`, next.CloseID, carried).Scan(&raw))
	if json.Unmarshal(raw, &revised) != nil || revised.Proposal == nil || revised.Proposal.Amount != "10.00" {
		t.Fatalf("the draft proposes the month's entry for a person to review: %s", raw)
	}
	var state string
	must(db.QueryRow(`SELECT review_state FROM workpaper_review_state WHERE close_id=$1 AND paper->>'revises'=$2`, next.CloseID, carried).Scan(&state))
	if state != "pending" {
		t.Fatalf("the prepared entry waits for a person's approval: %s", state)
	}
	if got := sweep(); got != 0 {
		t.Fatal("the revision already built from this capture is not revised again")
	}
}
