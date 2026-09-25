package transport

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestStandingApprovalApprovesUnchangedCarriedMonths(t *testing.T) {
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
	ctx := context.Background()
	firm := "org_Standing" + rand.Text()
	_, err = db.Exec(`INSERT INTO organizations(id,name) VALUES($1,'Standing fixture')`, firm)
	must(err)
	closes := []string{}
	open := func(org, entity, period string) string {
		t.Helper()
		var id string
		must(db.QueryRow(`INSERT INTO client_closes(organization_id,entity,period) VALUES($1,$2,$3) RETURNING id::text`, org, entity, period).Scan(&id))
		closes = append(closes, id)
		return id
	}
	defer func() {
		for _, id := range closes {
			db.Exec(`DELETE FROM standing_approvals WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM preparation_jobs WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id IN (SELECT id FROM workpapers WHERE close_id=$1)`, id)
			db.Exec(`DELETE FROM workpapers WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM evidence WHERE close_id=$1`, id)
			db.Exec(`DELETE FROM client_closes WHERE id=$1`, id)
		}
		db.Exec(`DELETE FROM organization_member_access WHERE organization_id=$1`, firm)
		db.Exec(`DELETE FROM organizations WHERE id=$1`, firm)
	}()
	type paper struct{ id, digest, review string }
	save := func(closeID string, body map[string]any) paper {
		t.Helper()
		var source, sourceDigest string
		must(db.QueryRow(`INSERT INTO evidence(close_id,name,digest,content,created_by) VALUES($1,'policy.txt',$2,'Policy','fixture') RETURNING id::text,digest`, closeID, rand.Text()).Scan(&source, &sourceDigest))
		body["sources"] = []map[string]string{{"id": source, "digest": sourceDigest, "role": "invoice"}}
		raw, _ := json.Marshal(body)
		var p paper
		must(db.QueryRow(`INSERT INTO workpapers(close_id,kind,digest,paper) VALUES($1,'prepaid',$2,$3) RETURNING id::text,digest`, closeID, rand.Text(), string(raw)).Scan(&p.id, &p.digest))
		return p
	}
	approve := func(p *paper, actor string) {
		t.Helper()
		must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Reviewed',$3) RETURNING id::text`, p.id, p.digest, actor).Scan(&p.review))
	}
	carry := func(closeID string, from paper, extra map[string]any) paper {
		t.Helper()
		body := map[string]any{"input": map[string]string{"source_reference": "Allianz"}, "carry_forward": map[string]string{"paper_id": from.id, "digest": from.digest, "review_id": from.review}}
		for k, v := range extra {
			body[k] = v
		}
		return save(closeID, body)
	}
	decision := func(p paper) (actor, note string) {
		t.Helper()
		err := db.QueryRow(`SELECT actor,note FROM workpaper_reviews WHERE workpaper_id=$1 ORDER BY id DESC LIMIT 1`, p.id).Scan(&actor, &note)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		return actor, note
	}
	sweep := func() int {
		t.Helper()
		_, approved, err := sweepStandingApprovals(ctx, db, "0")
		must(err)
		return approved
	}
	token := strings.Repeat("x", 32)
	handler := apiWithStore(db, token)
	call := func(method, closeID, paperID, body string, want int) []byte {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/closes/"+closeID+"/workpapers/"+paperID+"/standing-approval", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s standing approval: %d %s", method, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	const local = "local-workspace (development)"
	client := "Standing " + rand.Text()
	september, october := open("local-development", client, "2026-09"), open("local-development", client, "2026-10")

	unapproved := save(september, map[string]any{"input": map[string]string{"source_reference": "Draft"}})
	call("POST", september, unapproved.id, `{"action":"grant","reason":"Annual policy","confirmed":true}`, 409)
	_, err = db.Exec(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'changes_requested','Fix the term','user_partner')`, unapproved.id, unapproved.digest)
	must(err)
	call("POST", september, unapproved.id, `{"action":"grant","reason":"Annual policy","confirmed":true}`, 409)

	root := save(september, map[string]any{"input": map[string]string{"source_reference": "Allianz"}})
	approve(&root, "user_partner")
	var grant struct{ ID string }
	json.Unmarshal(call("POST", september, root.id, `{"action":"grant","reason":"Annual policy, straight line","confirmed":true}`, 200), &grant)
	call("POST", september, root.id, `{"action":"grant","reason":"Again","confirmed":true}`, 409)
	carried := carry(october, root, nil)
	superseded := carry(october, root, nil)
	revised := carry(october, root, map[string]any{"revises": superseded.id})
	blocked := carry(october, root, map[string]any{"blockers": []string{"Service end is missing."}})
	withEntry := carry(october, root, map[string]any{"proposal": map[string]string{"date": "2026-10-31", "debit_account": "Insurance expense", "credit_account": "Prepaid insurance", "amount": "100.00"}})

	var covering struct {
		Grant *struct {
			ID          string
			WorkpaperID string `json:"workpaper_id"`
		}
	}
	json.Unmarshal(call("GET", october, carried.id, "", 200), &covering)
	if covering.Grant == nil || covering.Grant.ID != grant.ID || covering.Grant.WorkpaperID != root.id {
		t.Fatalf("a carried month is covered by its schedule's standing approval: %+v", covering.Grant)
	}
	if got := sweep(); got != 1 {
		t.Fatalf("one unchanged carried month is approved: %d", got)
	}
	if actor, note := decision(carried); actor != local || !strings.Contains(note, "Standing approval #"+grant.ID) {
		t.Fatalf("the approval is recorded under the granter and names the standing approval: %s %s", actor, note)
	}
	if actor, _ := decision(revised); actor != "" {
		t.Fatal("a revision is never approved automatically")
	}
	if actor, _ := decision(blocked); actor != "" {
		t.Fatal("a draft with a blocker is never approved automatically")
	}
	if actor, _ := decision(withEntry); actor != "" {
		t.Fatal("a draft proposing a journal entry is never approved automatically")
	}
	if got := sweep(); got != 0 {
		t.Fatalf("a second sweep approves nothing new: %d", got)
	}

	// The grant rests on the approval it was given with.
	carried.review = ""
	must(db.QueryRow(`SELECT review_id FROM workpaper_review_state WHERE id=$1`, carried.id).Scan(&carried.review))
	november := open("local-development", client, "2026-11")
	next := carry(november, carried, nil)
	reapproved := root
	must(db.QueryRow(`INSERT INTO workpaper_reviews(workpaper_id,paper_digest,decision,note,actor) VALUES($1,$2,'approved','Reviewed again','user_partner') RETURNING id::text`, root.id, root.digest).Scan(&reapproved.review))
	carry(november, reapproved, nil)
	if got := sweep(); got != 0 {
		t.Fatal("a later decision on the granting schedule ends the grant's authority, even for a fresh carry")
	}
	var status struct {
		Grant *struct {
			InForce bool `json:"in_force"`
		}
	}
	json.Unmarshal(call("GET", november, next.id, "", 200), &status)
	if status.Grant == nil || status.Grant.InForce {
		t.Fatalf("the page says a grant resting on a superseded approval is not in effect: %+v", status.Grant)
	}
	_, err = db.Exec(`DELETE FROM workpaper_reviews WHERE workpaper_id=$1 AND note='Reviewed again'`, root.id)
	must(err)
	if got := sweep(); got != 1 {
		t.Fatalf("a carried month of a carried month is covered too: %d", got)
	}
	if actor, _ := decision(next); actor != local {
		t.Fatal("the second generation is approved under the granter")
	}

	call("POST", october, next.id, `{"action":"revoke","grant_id":"`+grant.ID+`","confirmed":true}`, 404)
	call("POST", november, next.id, `{"action":"revoke","grant_id":"`+grant.ID+`","confirmed":true}`, 200)
	december := open("local-development", client, "2026-12")
	next.review = ""
	must(db.QueryRow(`SELECT review_id FROM workpaper_review_state WHERE id=$1`, next.id).Scan(&next.review))
	carry(december, next, nil)
	if got := sweep(); got != 0 {
		t.Fatal("a revoked standing approval approves nothing")
	}

	// A signed-off target, another client, a departed granter and firm independence all stop it.
	other := save(september, map[string]any{"input": map[string]string{"source_reference": "Hiscox"}})
	approve(&other, "user_partner")
	call("POST", september, other.id, `{"action":"grant","reason":"Hiscox policy","confirmed":true}`, 200)
	signed := open("local-development", "Signed "+client, "2026-10")
	carry(signed, other, nil)
	_, err = db.Exec(`INSERT INTO close_signoffs(close_id,decision,snapshot_digest,snapshot,note,actor) VALUES($1,'signed_off','d','{}','Signed','fixture')`, october)
	must(err)
	carry(october, other, nil)
	if got := sweep(); got != 0 {
		t.Fatal("a signed-off close and another client's carried draft are not approved")
	}
	_, err = db.Exec(`DELETE FROM close_signoffs WHERE close_id=$1`, october)
	must(err)

	firmSeptember, firmOctober := open(firm, "Firm client", "2026-09"), open(firm, "Firm client", "2026-10")
	firmRoot := save(firmSeptember, map[string]any{"input": map[string]string{"source_reference": "Zurich"}})
	approve(&firmRoot, "user_partner")
	_, err = db.Exec(`INSERT INTO standing_approvals(organization_id,client_entity,workpaper_id,review_id,granted_by,reason) VALUES($1,'Firm client',$2,$3,'user_partner','Zurich policy')`, firm, firmRoot.id, firmRoot.review)
	must(err)
	firmCarried := carry(firmOctober, firmRoot, nil)
	_, err = db.Exec(`INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by,workpaper_ids) VALUES($1,$2,'[]','completed','user_partner',jsonb_build_array($3::text))`, firmOctober, rand.Text(), firmCarried.id)
	must(err)
	if got := sweep(); got != 1 {
		t.Fatalf("only the local October draft is approved; the departed granter's is not: %d", got)
	}
	_, err = db.Exec(`INSERT INTO organization_member_access(organization_id,subject,granted,reason,actor) VALUES($1,'user_partner',true,'Synthetic enrollment','fixture')`, firm)
	must(err)
	_, err = db.Exec(`UPDATE organizations SET require_independent_review=true WHERE id=$1`, firm)
	must(err)
	if got := sweep(); got != 0 {
		t.Fatal("firm independence refuses the granter who also prepared the carry")
	}
	_, err = db.Exec(`UPDATE organizations SET require_independent_review=false,enabled=false WHERE id=$1`, firm)
	must(err)
	if got := sweep(); got != 0 {
		t.Fatal("a disabled firm receives no automatic approvals")
	}
	_, err = db.Exec(`UPDATE organizations SET enabled=true WHERE id=$1`, firm)
	must(err)
	if got := sweep(); got != 1 {
		t.Fatal("a member granter's carried month is approved once the firm allows it")
	}
}
