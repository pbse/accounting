package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestHintsRememberApprovedAccountsAndCheckTheLedger(t *testing.T) {
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
	client := "Memory " + rand.Text()
	closes := map[string]string{}
	open := func(entity, period string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO client_closes(entity,period) VALUES($1,$2) RETURNING id::text`, entity, period).Scan(&id))
		closes[entity+period] = id
		return id
	}
	current, july, march, old, future := open(client, "2026-09"), open(client, "2026-07"), open(client, "2026-03"), open(client, "2025-09"), open(client, "2026-10")
	other := open("Other "+client, "2026-09")
	defer func() {
		for _, id := range closes {
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM procedure_versions WHERE client_entity=$1`, client)
	}()
	paper := func(closeID, source, account, decision, revises string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"revises": revises, "input": map[string]string{"source_reference": source, "prepaid_account": account, "expense_account": "Insurance expense"}})
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text`, closeID, rand.Text(), string(body)).Scan(&id))
		if decision != "" {
			_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,$2,'Reviewed','user_partner' FROM workpapers WHERE id=$1`, id, decision)
			must(e)
		}
		return id
	}
	paper(july, "Allianz", "Prepaid insurance", "approved", "")
	paper(march, "Allianz", "Prepaid insurance", "approved", "")
	paper(march, "Legacy", "Prepaid legacy", "approved", "")
	rent := paper(july, "Adobe", "Prepaid rent", "approved", "")
	paper(july, "Adobe Creative Cloud", "Prepaid software", "approved", rent)
	paper(july, "Deposit", "Prepaid deposits", "changes_requested", "")
	paper(old, "Old", "Prepaid insurance", "approved", "")
	paper(future, "Future", "Prepaid insurance", "approved", "")
	paper(other, "Other", "Prepaid other", "approved", "")

	var source, job, stranger string
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Invoice','fixture') RETURNING id::text`, current, rand.Text()).Scan(&source))
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, current, rand.Text()).Scan(&job))
	_, err = db.Exec(`INSERT INTO preparation_intakes(job_id,source_id,proposal) VALUES($1,$2,'{"kind":"prepaid","engine":"openai","facts":[{"field":"prepaid_account","value":"Prepaid Expenses"},{"field":"expense_account","value":"insurance expense"}]}')`, job, source)
	must(err)
	must(db.QueryRow(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by) VALUES($1,$2,'[]','needs_confirmation','fixture') RETURNING id::text`, other, rand.Text()).Scan(&stranger))

	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	type hint struct {
		Field           string
		InLedger        *bool `json:"in_ledger"`
		Procedure       string
		ProcedureUsable bool `json:"procedure_usable"`
		Usual           []struct {
			Value      string
			Count      int
			LastPeriod string `json:"last_period"`
			Usable     bool
		}
	}
	read := func(closeID, jobID string, want int) map[string]hint {
		t.Helper()
		r := httptest.NewRequest("GET", "/v1/closes/"+closeID+"/work/"+jobID+"/hints", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("hints: %d %s", w.Code, w.Body.String())
		}
		var out struct{ Fields []hint }
		json.Unmarshal(w.Body.Bytes(), &out)
		byField := map[string]hint{}
		for _, h := range out.Fields {
			byField[h.Field] = h
		}
		return byField
	}
	usual := func(h hint) string {
		parts := []string{}
		for _, u := range h.Usual {
			mark := ""
			if u.Usable {
				mark = "+"
			}
			parts = append(parts, u.Value+"×"+string(rune('0'+u.Count))+"@"+u.LastPeriod+mark)
		}
		return strings.Join(parts, ", ")
	}

	hints := read(current, job, 200)
	if got := usual(hints["prepaid_account"]); got != "Prepaid insurance×2@2026-07, Prepaid software×1@2026-07, Prepaid legacy×1@2026-03" || hints["prepaid_account"].InLedger != nil {
		t.Fatalf("approved, current papers of this client over 12 close months are remembered; no snapshot, no ledger check: %s %v", got, hints["prepaid_account"].InLedger)
	}
	read(current, stranger, 404)

	var connection, evidenceID string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) SELECT id,organization_id,entity,'fixture','active',$2,now(),'123' FROM client_closes WHERE id=$1 RETURNING id::text`, current, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.json',$2,'{}','fixture') RETURNING id::text`, current, rand.Text()).Scan(&evidenceID))
	_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,actor) VALUES($1,$2,'123',$3,$4,now(),'fixture')`, current, connection, rand.Text(),
		`{"period":"2026-09","source_id":"`+evidenceID+`","accounts":[{"Id":"1","Name":"Insurance","FullyQualifiedName":"Prepaid:Insurance"},{"Id":"2","Name":"Prepaid insurance","FullyQualifiedName":"Prepaid insurance","Active":true},{"Id":"3","Name":"Insurance expense","FullyQualifiedName":"Insurance expense"},{"Id":"4","Name":"Prepaid software","Active":false},{"Id":"5","Name":"Prepaid software","FullyQualifiedName":"Other:Prepaid software","Active":true},{"Id":"6","Name":"Prepaid legacy","Active":false}]}`)
	must(err)
	hints = read(current, job, 200)
	if p, e := hints["prepaid_account"], hints["expense_account"]; p.InLedger == nil || *p.InLedger || e.InLedger == nil || !*e.InLedger || usual(p) != "Prepaid insurance×2@2026-07+, Prepaid software×1@2026-07, Prepaid legacy×1@2026-03" {
		t.Fatalf("the proposed account is checked against the snapshot, and only a single active account can be used: %+v %+v %s", p, e, usual(p))
	}

	_, err = db.Exec(`INSERT INTO procedure_versions(organization_id,client_entity,kind,instructions,prepaid_account,enabled,reason,actor) SELECT organization_id,entity,'prepaid','Synthetic','Prepaid software',true,'Synthetic approval','fixture' FROM client_closes WHERE id=$1`, current)
	must(err)
	if p := read(current, job, 200)["prepaid_account"]; p.Procedure != "Prepaid software" || p.ProcedureUsable || usual(p) != "Prepaid insurance×2@2026-07, Prepaid software×1@2026-07, Prepaid legacy×1@2026-03" {
		t.Fatalf("an approved procedure's account is named, and a remembered account that conflicts with it cannot be used: %+v %s", p, usual(p))
	}

	_, err = db.Exec(`UPDATE quickbooks_connections SET state='reconnect_required' WHERE id=$1`, connection)
	must(err)
	if p := read(current, job, 200)["prepaid_account"]; p.InLedger != nil {
		t.Fatalf("a stale snapshot says nothing about the ledger: %+v", p)
	}
}
