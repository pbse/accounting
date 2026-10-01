package transport

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"close.local/backend/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestMorningBriefSaysWhatTheAgentDidAndWhoNeedsAPerson(t *testing.T) {
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
	org, other := "org_Morning"+rand.Text(), "org_Morning"+rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Morning fixture'),($2,'Other firm')`, org, other)
	must(err)
	for _, subject := range []string{"user_partner", "user_member"} {
		_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,$2,true,'Synthetic enrollment','fixture')`, org, subject)
		must(err)
	}
	now := time.Now().UTC()
	current, prior := now.Format("2006-01"), time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
	closes := []string{}
	open := func(firm, entity, period string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, firm, entity, period).Scan(&id))
		closes = append(closes, id)
		return id
	}
	harbor, dental, bakery, foreign := open(org, "Harbor", prior), open(org, "Dental", prior), open(org, "Bakery", current), open(other, "Foreign", prior)
	garden := open(org, "Garden", prior)
	defer func() {
		for _, id := range closes {
			db.Exec(`DELETE FROM standing_approvals WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_entity_syncs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM xero_snapshots WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM xero_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM quickbooks_connections WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, org)
		db.Exec(`DELETE FROM organizations WHERE id IN ($1,$2)`, org, other)
	}()
	paper := func(closeID string, body map[string]any, by any) string {
		t.Helper()
		var source, digest string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'invoice.txt',$2,'Invoice','fixture') RETURNING id::text,digest`, closeID, rand.Text()).Scan(&source, &digest))
		if _, ok := body["sources"]; !ok {
			body["sources"] = []map[string]string{{"id": source, "digest": digest}}
		}
		raw, _ := json.Marshal(body)
		var id string
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper,created_by) VALUES($1,'prepaid',$2,$3,$4) RETURNING id::text`, closeID, rand.Text(), string(raw), by).Scan(&id))
		return id
	}
	review := func(paperID, note string) {
		t.Helper()
		_, e := db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,'approved',$2,'user_partner' FROM workpapers WHERE id=$1`, paperID, note)
		must(e)
	}

	paper(harbor, map[string]any{"input": map[string]string{"source_reference": "Allianz"}}, nil)
	typed := paper(harbor, map[string]any{"input": map[string]string{"source_reference": "Typed"}}, "user_partner")
	paper(harbor, map[string]any{"revises": typed, "sources": []any{}, "input": map[string]string{"source_reference": "Typed"}}, nil)

	var connection, evidence string
	must(db.QueryRow(`INSERT INTO quickbooks_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,realm) VALUES($1,$2,'Dental','fixture','active',$3,now(),'123') RETURNING id::text`, dental, org, rand.Text()).Scan(&connection))
	must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'snapshot.txt',$2,'{}','scheduled-quickbooks-refresh') RETURNING id::text`, dental, rand.Text()).Scan(&evidence))
	for _, capture := range [][2]string{{"now()", "scheduled-quickbooks-refresh"}, {"now()-interval '2 days'", "scheduled-quickbooks-refresh"}, {"now()", "user_partner"}} {
		_, err = db.Exec(`INSERT INTO quickbooks_snapshots(close_id,connection_id,realm,digest,payload,started_at,captured_at,actor) VALUES($1,$2,'123',$3,$4,now(),`+capture[0]+`,$5)`, dental, connection, rand.Text(), `{"period":"`+prior+`","source_id":"`+evidence+`"}`, capture[1])
		must(err)
	}
	_, err = db.Exec(`INSERT INTO quickbooks_entity_syncs(close_id,connection_id,period,requested_by,state,finished_at,exceptions) VALUES($1,$2,$3,'scheduled-quickbooks-refresh','completed',now(),'[{"type":"no_payee","kind":"Purchase","provider_ids":["7"],"finding":"No payee","question":"Who was paid?"}]')`, dental, connection, prior)
	must(err)
	schedule := paper(dental, map[string]any{"input": map[string]string{"source_reference": "Zurich"}}, "user_partner")
	review(schedule, "Reviewed")
	var grant string
	must(db.QueryRow(`INSERT INTO standing_approvals(organization_id,client_entity,workpaper_id,review_id,granted_by,reason) SELECT $1,'Dental',id,(SELECT max(id) FROM workpaper_reviews WHERE workpaper_id=$2),'user_partner','Zurich policy' FROM workpapers WHERE id=$2 RETURNING id::text`, org, schedule).Scan(&grant))
	review(paper(dental, map[string]any{"input": map[string]string{"source_reference": "Zurich October"}}, "user_partner"), "Standing approval #"+grant+standingNote)
	review(paper(dental, map[string]any{"input": map[string]string{"source_reference": "Hiscox"}}, "user_partner"), "Standing approval #"+grant+": I approved this myself.")
	lookalike := paper(dental, map[string]any{"input": map[string]string{"source_reference": "Chubb"}}, "user_partner")
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) SELECT id,digest,'approved',$2,'user_other' FROM workpapers WHERE id=$1`, lookalike, "Standing approval #"+grant+standingNote)
	must(err)
	var xero string
	must(db.QueryRow(`INSERT INTO xero_connections(close_id,organization_id,client_entity,actor,state,oauth_digest,oauth_expires_at,tenant_id) VALUES($1,$2,'Garden','fixture','active',$3,now(),'t-1') RETURNING id::text`, garden, org, rand.Text()).Scan(&xero))
	_, err = db.Exec(`INSERT INTO xero_snapshots(close_id,connection_id,tenant_id,period,digest,payload,actor) VALUES($1,$2,'t-1',$3,'d','{}','fixture'),($1,$2,'t-1',$3,'d','{}','scheduled-xero-refresh')`, garden, xero, prior)
	must(err)
	_, err = db.Exec(`INSERT INTO replay_runs(organization_id,close_id,model,actor,items) VALUES($1,$2,'model-b','scheduled-replay','[{"kept":true},{"kept":false},{"kept":false,"error":"Evidence changed"}]'),($1,$2,'model-b','user_partner','[]')`, org, garden)
	must(err)
	paper(foreign, map[string]any{"input": map[string]string{"source_reference": "Foreign"}}, nil)
	_ = bakery

	mux := http.NewServeMux()
	registerEffort(mux, db)
	h := organizationBoundary(db, mux)
	read := func(subject string, permissions ...string) (out struct {
		Overnight struct {
			Snapshots int `json:"month_end_snapshots"`
			Drafts    int `json:"drafts_prepared"`
			Standing  int `json:"standing_approvals"`
		}
		Closes []struct {
			CloseID string `json:"close_id"`
			Needs   []string
		}
		Needing, Quiet int
		Check          *struct {
			Model           string
			ReasoningEffort *string `json:"reasoning_effort"`
			Kept, Scored    int
		} `json:"model_check"`
	}) {
		t.Helper()
		actor := &auth.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Organization: org, Permissions: permissions}
		r := httptest.NewRequest("GET", "/v1/closes/morning", nil).WithContext(auth.WithActor(t.Context(), actor))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("morning: %d %s", w.Code, w.Body.String())
		}
		return out
	}
	got := read("user_partner", "clients:all")
	if got.Overnight.Snapshots != 2 || got.Overnight.Drafts != 1 || got.Overnight.Standing != 1 {
		t.Errorf("the past day counts the scheduled QuickBooks and Xero snapshots, the new agent draft and the automatic approval only: %+v", got.Overnight)
	}
	if got.Check == nil || got.Check.Model != "model-b" || got.Check.Kept != 1 || got.Check.Scored != 2 {
		t.Errorf("the latest automatic model check counts the readings it scored: %+v", got.Check)
	}
	if got.Check == nil || got.Check.ReasoningEffort != nil {
		t.Fatalf("legacy effort must remain unknown: %+v", got.Check)
	}
	_, err = db.Exec(`UPDATE replay_runs SET reasoning_effort='high' WHERE organization_id=$1`, org)
	must(err)
	got = read("user_partner", "clients:all")
	if got.Check == nil || got.Check.ReasoningEffort == nil || *got.Check.ReasoningEffort != "high" {
		t.Fatalf("the morning brief must expose the recorded effort: %+v", got.Check)
	}
	if got.Needing != 2 || got.Quiet != 2 || len(got.Closes) != 2 || got.Closes[0].CloseID != harbor || got.Closes[1].CloseID != dental {
		t.Fatalf("closes needing a person are listed most first; the quiet one is counted: %+v", got)
	}
	if n := got.Closes[0].Needs; len(n) != 2 || n[0] != "approve 1 draft" || n[1] != "no month-end snapshot yet" {
		t.Errorf("Harbor needs its draft approved and a month-end snapshot: %v", n)
	}
	if n := got.Closes[1].Needs; len(n) != 1 || n[0] != "1 new client question since last review" {
		t.Errorf("Dental's new capture finding is a question since its last review: %v", n)
	}
	if member := read("user_member"); member.Needing != 0 || member.Quiet != 0 || member.Overnight.Drafts != 0 || member.Overnight.Snapshots != 0 || member.Overnight.Standing != 0 || member.Check != nil {
		t.Fatalf("a member without client grants sees nothing: %+v", member)
	}
}
