package transport

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"close.local/backend/internal/auth"
	"close.local/backend/internal/preparation"
	"github.com/golang-jwt/jwt/v5"
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
			db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, id)
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

	// A newer Xero capture supplies the chart of accounts.
	var xero string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) SELECT id,organization_id,entity,'fixture','active',$2,now(),'t-1' FROM client_closes WHERE id=$1 RETURNING id::text`, current, rand.Text()).Scan(&xero))
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor,captured_at) VALUES($1,$2,'t-1','2026-09','d',$3,'fixture',now()+interval '1 minute')`, current, xero,
		`{"accounts":{"Accounts":[{"AccountID":"x-1","Name":"Prepaid Expenses","Status":"ACTIVE"},{"AccountID":"x-2","Name":"Insurance expense","Status":"ARCHIVED"}]}}`)
	must(err)
	if h := read(current, job, 200); h["prepaid_account"].InLedger == nil || !*h["prepaid_account"].InLedger || h["expense_account"].InLedger == nil || !*h["expense_account"].InLedger {
		t.Fatalf("the proposed accounts are checked against the Xero chart of accounts: %+v", h)
	}
}

func TestClientMemoryShowsWhoDecidedAndRetiresAliases(t *testing.T) {
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
	org := "org_Memory" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Memory fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_a',true,'Synthetic','fixture'),($1,'user_b',true,'Synthetic','fixture')`, org)
	must(err)
	_, err = db.Exec(`INSERT INTO client_access(organization_id,subject,client_entity,granted,reason,actor) VALUES($1,'user_a','Alpha',true,'Synthetic','fixture')`, org)
	must(err)
	var current, prior, beta, paper, review string
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-09') RETURNING id::text`, org).Scan(&current))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Alpha','2026-08') RETURNING id::text`, org).Scan(&prior))
	must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,'Beta','2026-09') RETURNING id::text`, org).Scan(&beta))
	defer func() {
		for _, q := range []string{`DELETE FROM standing_approvals WHERE organization_id=$1`, `DELETE FROM client_aliases WHERE organization_id=$1`,
			`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT w.id FROM workpapers w JOIN client_closes c ON c.id=w.close_id WHERE c.organization_id=$1)`,
			`DELETE FROM workpapers WHERE close_id IN (SELECT id FROM client_closes WHERE organization_id=$1)`,
			`DELETE FROM client_closes WHERE organization_id=$1`, `DELETE FROM client_access WHERE organization_id=$1`, `DELETE FROM organization_member_access WHERE organization_id=$1`, `DELETE FROM organizations WHERE id=$1`} {
			db.Exec(q, org)
		}
	}()
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','memory-paper','{"input":{"source_reference":"Allianz","expense_account":"Insurance expense","prepaid_account":"Prepaid insurance"}}') RETURNING id::text`, prior).Scan(&paper))
	must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'memory-paper','approved','Reviewed','user_partner') RETURNING id::text`, paper).Scan(&review))
	_, err = db.Exec(`INSERT INTO standing_approvals(organization_id,client_entity,workpaper_id,review_id,granted_by,reason) VALUES($1,'Alpha',$2,$3,'user_a','Carry the schedule while nothing changes')`, org, paper, review)
	must(err)
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES($1,'Alpha','alpha holdings','user_a'),($1,'Beta','beta co','user_b')`, org)
	must(err)

	store := savedCloses(db, strings.Repeat("a", 32))
	type memory struct {
		Aliases []struct {
			Alias        string
			RetireReason string  `json:"retire_reason"`
			RetiredBy    string  `json:"retired_by"`
			RetiredAt    *string `json:"retired_at"`
		}
		Standing []struct {
			CloseID     string `json:"close_id"`
			WorkpaperID string `json:"workpaper_id"`
			GrantedBy   string `json:"granted_by"`
			InForce     bool   `json:"in_force"`
		} `json:"standing_approvals"`
		Accounts []struct {
			Kind, Field string
			Values      []usualValue
		}
	}
	call := func(subject string, permissions []string, method, closeID string, body any, want int) memory {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/v1/closes/"+closeID+"/memory", bytes.NewReader(raw)).WithContext(auth.WithActor(t.Context(), &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		store.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s %v: %d %s", method, closeID, body, w.Code, w.Body.String())
		}
		var out memory
		json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	reviewer := []string{"workpapers:review"}
	got := call("user_a", nil, "GET", current, nil, 200)
	if len(got.Aliases) != 1 || got.Aliases[0].Alias != "alpha holdings" || len(got.Standing) != 1 || !got.Standing[0].InForce || got.Standing[0].CloseID != prior || got.Standing[0].WorkpaperID != paper {
		t.Fatalf("the client's names and standing approvals, with where they were granted: %+v", got)
	}
	if len(got.Accounts) != 2 || got.Accounts[0].Field != "expense_account" || got.Accounts[0].Values[0].Value != "Insurance expense" || got.Accounts[0].Values[0].WorkpaperID != paper || got.Accounts[0].Values[0].CloseID != prior || got.Accounts[0].Values[0].ApprovedBy != "user_partner" {
		t.Fatalf("each account used before names the approved draft it comes from and who approved it: %+v", got.Accounts)
	}
	call("user_b", nil, "GET", current, nil, 404)
	retire := func(subject string, permissions []string, closeID, alias, reason string, want int) {
		t.Helper()
		call(subject, permissions, "POST", closeID, map[string]any{"action": "retire_alias", "alias": alias, "reason": reason, "confirmed": true}, want)
	}
	retire("user_a", nil, current, "alpha holdings", "Sold the holding company", 403)
	retire("user_a", reviewer, current, "alpha holdings", "", 422)
	retire("user_a", reviewer, current, "beta co", "Not this client", 404)
	retire("user_a", reviewer, current, "alpha holdings", "Sold the holding company", 200)
	retire("user_a", reviewer, current, "alpha holdings", "Again", 404)
	tx, _ := db.Begin()
	names, err := preparation.ClientAliases(t.Context(), tx, current)
	tx.Rollback()
	if err != nil || len(names) != 0 {
		t.Fatalf("a retired name no longer identifies the client: %v %v", names, err)
	}
	got = call("user_a", nil, "GET", current, nil, 200)
	if len(got.Aliases) != 1 || got.Aliases[0].RetiredAt == nil || got.Aliases[0].RetiredBy != "user_a" || got.Aliases[0].RetireReason != "Sold the holding company" {
		t.Fatalf("a retired name stays listed with who retired it and why: %+v", got.Aliases)
	}
	_, err = db.Exec(`INSERT INTO client_aliases(organization_id,client_entity,alias,confirmed_by) VALUES($1,'Alpha','alpha holdings','user_a') ON CONFLICT DO NOTHING`, org)
	must(err)
	tx, _ = db.Begin()
	names, _ = preparation.ClientAliases(t.Context(), tx, current)
	tx.Rollback()
	if len(names) != 1 {
		t.Fatalf("a retired name can be confirmed again: %v", names)
	}
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'memory-paper','changes_requested','Reopened','user_partner')`, paper)
	must(err)
	if got = call("user_a", nil, "GET", current, nil, 200); len(got.Standing) != 1 || got.Standing[0].InForce || len(got.Accounts) != 0 {
		t.Fatalf("a grant whose approval changed is listed as not in force, and the reopened draft is no longer an approval: %+v", got)
	}
	var changed string
	must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid','memory-changed','{"sources":[{"id":"1","digest":"gone"}],"input":{"source_reference":"Zurich","expense_account":"Old expense","prepaid_account":"Old prepaid"}}') RETURNING id::text`, prior).Scan(&changed))
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,'memory-changed','approved','Reviewed','user_partner')`, changed)
	must(err)
	if got = call("user_a", nil, "GET", current, nil, 200); len(got.Accounts) != 0 {
		t.Fatalf("an approved draft whose source changed contributes no account: %+v", got.Accounts)
	}
	_, err = db.Exec(`UPDATE standing_approvals SET revoked_by='user_a',revoked_at=now() WHERE organization_id=$1`, org)
	must(err)
	if got = call("user_a", nil, "GET", current, nil, 200); len(got.Standing) != 0 {
		t.Fatalf("a revoked grant is no longer listed: %+v", got.Standing)
	}
}
